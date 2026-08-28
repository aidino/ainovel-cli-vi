package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReopenBookTool Mở lại sách đã hoàn kết vào trạng thái làm lại, do Engine gọi ở ranh giới hành động can thiệp.
// Sau khi hoàn bản completePhaseGate chặn cứng mọi phân phát subagent, người dùng không thể làm lại chương đã viết.
// Công cụ này không phải subagent, có thể gọi trong kỳ complete: nó chuyển nguyên tử phase về writing, chương mục tiêu vào
// PendingRewrites, flow=rewriting, sau đó Flow Router theo hàng đợi làm lại có sẵn phân phát writer viết lại từng chương,
// Chạy xong hàng đợi commit_chapter sẽ tự động hoàn kết lại. Các logic nặng như Gate / Router / edit / commit đều không cần thay đổi.
type ReopenBookTool struct {
	store *store.Store
}

func NewReopenBookTool(s *store.Store) *ReopenBookTool {
	return &ReopenBookTool{store: s}
}

func (t *ReopenBookTool) Name() string  { return "reopen_book" }
func (t *ReopenBookTool) Label() string { return "mở lại làm lại" }

func (t *ReopenBookTool) Description() string {
	return "Mở lại toàn sách đã hoàn thành (phase=complete) đưa vào trạng thái làm lại, dùng khi người dùng sau khi hoàn tất yêu cầu viết lại/đánh bóng vài chương." +
		"chapters là số chương đã hoàn thành cần làm lại; sau khi gọi các chương này vào hàng viết lại, Host sẽ điều writer viết lại từng chương, sửa xong hết thì tự hoàn thành lại." +
		"Chỉ dùng khi toàn sách đã hoàn thành và người dùng yêu cầu rõ ràng sửa chương đã viết; người dùng muốn thêm tình tiết/mở rộng không thuộc phạm vi làm lại, đừng dùng tool này."
}

// Công cụ ghi, cấm đồng thời.
func (t *ReopenBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *ReopenBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *ReopenBookTool) ActivityDescription(_ json.RawMessage) string {
	return "mở lại toàn sách để làm lại"
}

func (t *ReopenBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapters", schema.Array("danh sách số chương đã hoàn thành cần làm lại (ít nhất một chương)", schema.Int(""))).Required(),
		schema.Property("reason", schema.String("lý do làm lại (tùy chọn, như \"dọn ký tự đặc biệt\")")),
	)
}

func (t *ReopenBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapters []int  `json:"chapters"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if len(a.Chapters) == 0 {
		return nil, fmt.Errorf("chapters không được rỗng, cần chỉ rõ chương làm lại: %w", errs.ErrToolArgs)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
	}
	// Chỉ có thể làm lại chương đã viết; số chương không nằm trong tập hoàn thành thuộc về viết tiếp/vượt ranh giới, từ chối rõ ràng hướng người dùng đi điều chỉnh dung lượng.
	var invalid []int
	for _, ch := range a.Chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("chương %v chưa viết xong, reopen chỉ làm lại được chương đã hoàn thành (thêm/mở rộng tình tiết hãy đi đường điều chỉnh độ dài): %w", invalid, errs.ErrToolPrecondition)
	}

	// Kiểm tra trước phase được store.Reopen đỡ đáy (chỉ complete có thể gọi).
	if err := t.store.Progress.Reopen(a.Chapters, a.Reason); err != nil {
		return nil, fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint：Đối xứng với complete_book (GlobalScope + meta/progress.json).
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return nil, fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"reopened":         true,
		"phase":            string(domain.PhaseWriting),
		"pending_rewrites": a.Chapters,
		"next_step":        "đã mở lại và đưa chương mục tiêu vào hàng. Hãy chờ Host điều writer làm lại từng chương; sửa xong tất cả sẽ tự động hoàn thành lại.",
	})
}