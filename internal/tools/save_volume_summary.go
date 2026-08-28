package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveVolumeSummaryTool Lưu tóm tắt cấp tập, Editor gọi khi kết thúc tập.
type SaveVolumeSummaryTool struct {
	store *store.Store
}

func NewSaveVolumeSummaryTool(store *store.Store) *SaveVolumeSummaryTool {
	return &SaveVolumeSummaryTool{store: store}
}

func (t *SaveVolumeSummaryTool) Name() string { return "save_volume_summary" }
func (t *SaveVolumeSummaryTool) Description() string {
	return "Lưu tóm tắt cấp tập (chế độ trường thiên, gọi khi kết thúc tập)"
}
func (t *SaveVolumeSummaryTool) Label() string { return "lưu tóm tắt tập" }

// Công cụ ghi, cấm đồng thời.
func (t *SaveVolumeSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveVolumeSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveVolumeSummaryTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("volume", schema.Int("số tập")).Required(),
		schema.Property("title", schema.String("tiêu đề tập")).Required(),
		schema.Property("summary", schema.String("tóm tắt tập (dưới 500 từ)")).Required(),
		schema.Property("key_events", schema.Array("sự kiện then chốt trong tập", schema.String(""))).Required(),
	)
}

func (t *SaveVolumeSummaryTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume    int      `json:"volume"`
		Title     string   `json:"title"`
		Summary   string   `json:"summary"`
		KeyEvents []string `json:"key_events"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if a.Volume <= 0 {
		return nil, fmt.Errorf("volume must be > 0")
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Summary) == "" {
		return nil, fmt.Errorf("title and summary are required: %w", errs.ErrToolArgs)
	}
	volSummary := domain.VolumeSummary{
		Volume:    a.Volume,
		Title:     a.Title,
		Summary:   a.Summary,
		KeyEvents: a.KeyEvents,
	}
	existing, err := t.store.Summaries.LoadVolumeSummary(a.Volume)
	if err != nil {
		return nil, fmt.Errorf("load volume summary: %w: %w", errs.ErrStoreRead, err)
	}
	if existing != nil {
		if !reflect.DeepEqual(*existing, volSummary) {
			return nil, fmt.Errorf("tóm tắt tập %d đã tồn tại với nội dung khác, từ chối ghi đè: %w", a.Volume, errs.ErrToolConflict)
		}
	} else {
		if err := requireAggregateTarget(t.store, flow.AggregateVolumeSummary, a.Volume, 0, 0); err != nil {
			return nil, err
		}
		if err := t.store.Summaries.SaveVolumeSummary(volSummary); err != nil {
			return nil, fmt.Errorf("save volume summary: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.VolumeScope(a.Volume), "volume_summary",
		fmt.Sprintf("summaries/vol-v%02d.json", a.Volume),
	); err != nil {
		return nil, fmt.Errorf("checkpoint volume summary: %w", err)
	}

	result := map[string]any{"saved": true, "type": "volume_summary", "volume": a.Volume}
	// Điểm kích hoạt hoàn kết đường chính kết thúc: mảnh ghép cuối của ba liên kết cuối tập là tóm tắt tập, sau khi lưu nếu toàn sách đã
	// đáp ứng điều kiện hoàn kết thì tại chỗ MarkComplete (kiểm tra hoàn kết luôn xảy ra ở công cụ sự thật rơi xuống cuối cùng,
	// cùng mô hình với commit_chapter; vị ngữ xem layeredComplete của commit_chapter.go).
	complete, err := ReconcileLayeredCompletion(t.store)
	if err != nil {
		return nil, fmt.Errorf("reconcile book completion: %w", err)
	}
	if complete {
		result["book_complete"] = true
	}

	return json.Marshal(result)
}