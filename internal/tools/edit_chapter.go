package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	agentcoretools "github.com/voocel/agentcore/tools"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// EditChapterTool Thực hiện thay thế chuỗi định điểm cho bản thảo chương, thích hợp kịch bản làm bóng.
// So với draft_chapter làm lại toàn chương, token tiết kiệm 10x+.
//
// Giao ước lưu: chỉ sửa drafts/{ch:02d}.draft.md, cấm sửa trực tiếp chapters/ (bản cuối do commit_chapter độc chiếm).
// Seed Ngữ nghĩa: drafts không tồn tại nhưng chapters có → tự động copy chapters vào drafts làm điểm bắt đầu.
// Kiểm tra sở hữu: chỉ cho phép chỉnh sửa chương đã hoàn thành và ở trong hàng đợi PendingRewrites.
//
// Công cụ này là bao bọc mỏng của agentcore.EditTool, logic tìm-đổi (khớp chịu lỗi nhiều cấp, xuất diff, giữ cuối dòng/BOM)
// Tất cả tái sử dụng triển khai thượng nguồn.
type EditChapterTool struct {
	store *store.Store
	edit  *agentcoretools.EditTool
}

func NewEditChapterTool(s *store.Store) *EditChapterTool {
	return &EditChapterTool{
		store: s,
		edit:  agentcoretools.NewEdit(s.Dir(), nil),
	}
}

func (t *EditChapterTool) Name() string  { return "edit_chapter" }
func (t *EditChapterTool) Label() string { return "sửa chương" }

// ReadOnly Khai báo rõ công cụ ghi (kết hợp ConcurrencySafeTool phòng ngừa bị lập lịch đồng thời).
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe Cấm đồng thời rõ ràng: cùng chương edit_chapter nhiều lần song song sẽ cạnh tranh đọc-sửa-ghi,
// cho dù chương khác nhau song song cũng sẽ xen kẽ thứ tự checkpoint. Tuần tự thống nhất là ổn định nhất.
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription Dùng cho UI/log hiển thị mô tả hoạt động của công cụ hiện tại.
func (t *EditChapterTool) ActivityDescription(_ json.RawMessage) string { return "sửa bản thảo chương" }

func (t *EditChapterTool) Description() string {
	return "Chỉ thực hiện thay thế chuỗi định điểm trên bản thảo của chương đã hoàn thành và nằm trong hàng PendingRewrites (ưu tiên cho kịch bản đánh bóng, tiết kiệm token hơn draft_chapter viết lại cả chương)." +
		"Cấm dùng tool này cho bản thảo đầu của chương mới; bản thảo đầu có lỗi cứng hãy gọi draft_chapter(mode=\"write\") ghi đè cả chương." +
		"Tìm old_string và thay bằng new_string, yêu cầu khớp chính xác và duy nhất (khớp nhiều chỗ cần replace_all=true)." +
		"old_string phải copy từng chữ từ kết quả read_chapter(source=\"draft\") gần nhất, cấm dựng lại nguyên văn theo trí nhớ;" +
		"lưu ý giá trị trả về là chuỗi JSON, \\n phải khôi phục thành xuống dòng thật. Sau khi draft_chapter ghi đè bản thảo phải read_chapter lại rồi mới sửa." +
		"lỗi không khớp sẽ kèm đoạn ứng viên gần nhất trong bản thảo, hãy copy từng chữ từ ứng viên rồi thử lại." +
		"ghi vào drafts/{ch}.draft.md; khi drafts chưa tồn tại thì tự gieo từ chapters." +
		"từ chối thực thi khi chương đã hoàn thành và không nằm trong hàng PendingRewrites. Mỗi lần gọi chỉ sửa một chỗ, sửa nhiều chỗ hãy gọi nhiều lần."
}

func (t *EditChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("số chương")).Required(),
		schema.Property("old_string", schema.String("đoạn nguyên văn chính xác cần thay, nhiều dòng phải kèm xuống dòng; khi không có replace_all phải xuất hiện duy nhất trong bản thảo")).Required(),
		schema.Property("new_string", schema.String("văn bản mới sau khi thay")).Required(),
		schema.Property("replace_all", schema.Bool("thay mọi chỗ khớp (mặc định false)")),
	)
}

func (t *EditChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.OldString == "" {
		return nil, fmt.Errorf("old_string không được rỗng: %w", errs.ErrToolArgs)
	}
	if a.OldString == a.NewString {
		return nil, fmt.Errorf("old_string trùng new_string, không cần sửa: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}

	// Kiểm tra sở hữu: triển khai cơ học giao thức writer. Bản thảo đầu chương mới chỉ có thể đè toàn chương, không thể phụ thuộc
	// mô hình tự tuân thủ từ gợi ý rồi vẫn phơi bày chỉnh sửa chính xác dễ vỡ thành đường dẫn có thể thực thi.
	completed, err := t.store.Progress.IsChapterCompleted(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if !completed {
		return nil, fmt.Errorf("chương %d chưa hoàn thành, bản thảo đầu cấm dùng edit_chapter; có lỗi cứng hãy gọi draft_chapter(mode=\"write\", chapter=%d) ghi đè cả chương: %w", a.Chapter, a.Chapter, errs.ErrToolPrecondition)
	}
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil || !slices.Contains(progress.PendingRewrites, a.Chapter) {
		return nil, fmt.Errorf("chương %d đã hoàn thành và không nằm trong hàng PendingRewrites, không thể sửa; cần sửa hãy để editor đọc kiểm kích hoạt viết lại/đánh bóng trước: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}

	// Seed：drafts khi không tồn tại từ chapters copy một bản làm điểm bắt đầu
	if err := t.ensureDraft(a.Chapter); err != nil {
		return nil, err
	}

	// Ủy thác agentcore.EditTool hoàn thành tìm-đổi
	subArgs, _ := json.Marshal(map[string]any{
		"path":        fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"file_path":   fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"old_text":    a.OldString,
		"old_string":  a.OldString,
		"new_text":    a.NewString,
		"new_string":  a.NewString,
		"replace_all": a.ReplaceAll,
	})
	result, err := t.edit.Execute(ctx, subArgs)
	if err != nil {
		return nil, fmt.Errorf("apply edit: %w: %w", errs.ErrToolPrecondition, err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "edit",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint edit: %w: %w", errs.ErrStoreWrite, err)
	}

	// Hướng dẫn bổ sung: cho writer biết bước tiếp theo, tránh bỏ sót check_consistency / commit_chapter
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["next_step"] = "edit đã ghi xuống đĩa. Còn lỗi cứng có thể edit_chapter lại; nếu không thì check_consistency rồi commit_chapter"
	return json.Marshal(passthrough)
}

// ensureDraft Đảm bảo drafts/{ch}.draft.md tồn tại:
//   - Đã có bản thảo → trả về trực tiếp
//   - Không bản thảo nhưng có bản cuối → copy bản cuối vào drafts làm điểm bắt đầu sửa (thường thấy trong kịch bản làm bóng)
//   - Đều không có → báo lỗi, gợi ý dùng draft_chapter tạo bản thảo đầu tiên trước
func (t *EditChapterTool) ensureDraft(chapter int) error {
	draft, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load draft: %w: %w", errs.ErrStoreRead, err)
	}
	if draft != "" {
		return nil
	}
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return fmt.Errorf("load chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if text == "" {
		return fmt.Errorf("chương %d không có cả bản thảo lẫn bản cuối, hãy gọi draft_chapter(mode=write, chapter=%d) tạo bản thảo đầu trước: %w", chapter, chapter, errs.ErrToolPrecondition)
	}
	if err := t.store.Drafts.SaveDraft(chapter, text); err != nil {
		return fmt.Errorf("seed draft from chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}