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

// EditChapterTool 对章节草稿做定点字符串替换，适用于打磨场景。
// 相比 draft_chapter 整章重写，token 节省 10x+。
//
// 落盘契约：只改 drafts/{ch:02d}.draft.md，禁止直接改 chapters/（终稿由 commit_chapter 独占）。
// Seed 语义：drafts 不存在但 chapters 有 → 自动把 chapters 复制到 drafts 作为起点。
// 归属检查：仅允许编辑已完成且位于 PendingRewrites 队列中的章节。
//
// 本工具是 agentcore.EditTool 的薄封装，找-换逻辑（多级容错匹配、diff 输出、行尾/BOM 保留）
// 全部复用上游实现。
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

// ReadOnly 明确声明写工具（配合 ConcurrencySafeTool 防止被并发调度）。
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe 显式禁止并发：同章节多次 edit_chapter 并行会读-改-写竞态，
// 即使不同章节并行也会穿插 checkpoint 顺序。统一串行最稳。
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription 供 UI/日志展示当前工具的活动描述。
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

	// 归属检查：机械落实 writer 协议。新章初稿只能整章覆盖，不能依赖
	// 模型自行遵守提示词后仍把脆弱的精确编辑暴露为可执行路径。
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

	// Seed：drafts 不存在时从 chapters 复制一份作为起点
	if err := t.ensureDraft(a.Chapter); err != nil {
		return nil, err
	}

	// 委托 agentcore.EditTool 完成找-换
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

	// 附加指引：让 writer 知道后续步骤，避免遗漏 check_consistency / commit_chapter
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["next_step"] = "edit đã ghi xuống đĩa. Còn lỗi cứng có thể edit_chapter lại; nếu không thì check_consistency rồi commit_chapter"
	return json.Marshal(passthrough)
}

// ensureDraft 保证 drafts/{ch}.draft.md 存在：
//   - 已有草稿 → 直接返回
//   - 无草稿但有终稿 → 把终稿复制到 drafts 作为修改起点（常见于打磨场景）
//   - 都没有 → 报错，提示先用 draft_chapter 创建初稿
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