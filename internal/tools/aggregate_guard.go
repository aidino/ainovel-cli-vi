package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// requireAggregateTarget Ràng buộc ghi mới tổng hợp của Editor vào công cụ chờ bù duy nhất hiện tại của Router.
// Mục tiêu hoàn toàn suy ra từ sự thật đã lưu, không phụ thuộc vào văn bản nhiệm vụ, cũng không tin số chương/tập arc do mô hình tự điền;
// Kết thúc idempotent có cùng nội dung đã lưu được các công cụ nhận diện trước khi gọi hàm này.
func requireAggregateTarget(st *store.Store, kind flow.AggregateKind, volume, arc, endChapter int) error {
	state, err := flow.LoadState(st)
	if err != nil {
		return fmt.Errorf("load aggregate state: %w: %w", errs.ErrStoreRead, err)
	}
	due := state.AggregateRefresh
	if due == nil {
		return fmt.Errorf("hiện không có sản phẩm %s chờ xử lý: %w", kind, errs.ErrToolPrecondition)
	}
	targetMismatch := due.Kind != kind
	switch kind {
	case flow.AggregateArcReview, flow.AggregateArcSummary:
		targetMismatch = targetMismatch || due.Volume != volume || due.Arc != arc
	case flow.AggregateVolumeSummary:
		targetMismatch = targetMismatch || due.Volume != volume
	case flow.AggregateGlobalReview:
		// Đọc kiểm toàn cục không có tọa độ tập arc, chỉ định vị bởi kind và chương kết thúc.
	}
	endMismatch := endChapter > 0 && due.EndChapter != endChapter
	if targetMismatch || endMismatch {
		return fmt.Errorf(
			"mục tiêu ghi tổng hợp không khớp: hiện cần xử lý kind=%s volume=%d arc=%d end_chapter=%d, nhận được kind=%s volume=%d arc=%d end_chapter=%d: %w",
			due.Kind, due.Volume, due.Arc, due.EndChapter,
			kind, volume, arc, endChapter, errs.ErrToolConflict,
		)
	}
	return nil
}