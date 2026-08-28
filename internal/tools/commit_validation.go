package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/chapterfacts"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// validateCommitArgs Kiểm tra tải trọng ngữ nghĩa đầy đủ do mô hình gửi trước khi tạo PendingCommit.
// Lỗi trả về trực tiếp cho mô hình sửa; không tạo trạng thái bán thành phẩm, cũng không đoán giá trị thiếu.
func (t *CommitChapterTool) validateCommitArgs(a commitArgs) error {
	if err := chapterfacts.Validate(a.ChapterFacts); err != nil {
		return fmt.Errorf("%v: %w", err, errs.ErrToolArgs)
	}

	if len(a.ForeshadowUpdates) > 0 {
		ledger, err := t.store.World.LoadForeshadowLedger()
		if err != nil {
			return fmt.Errorf("load foreshadow ledger: %w: %w", errs.ErrStoreRead, err)
		}
		// Sổ là hình chiếu toàn sách, còn Projector phát lại bản ghi chương theo thứ tự. Khi làm lại chương đầu sổ
		// vẫn chứa chi tiết gieo mầm của các chương sau——cho qua chúng, kiểm tra trước khi gửi sẽ trái ngược kết luận phát lại,
		// mô hình không thể sửa, hàng đợi làm lại theo đó bị khóa. Do đó nhất luật lấy 'chương này có thể thấy' làm chuẩn.
		plantedAt := make(map[string]int, len(ledger))
		for _, entry := range ledger {
			plantedAt[entry.ID] = entry.PlantedAt
		}
		declared := make(map[string]struct{}, len(a.ForeshadowUpdates))
		for i, update := range a.ForeshadowUpdates {
			switch update.Action {
			case "plant":
				declared[update.ID] = struct{}{}
			case "advance", "resolve":
				if _, ok := declared[update.ID]; ok {
					continue
				}
				at, known := plantedAt[update.ID]
				if !known {
					return fmt.Errorf("foreshadow_updates[%d] references unknown id %q: %w", i, update.ID, errs.ErrToolPrecondition)
				}
				if at > a.Chapter {
					return fmt.Errorf("foreshadow_updates[%d] chi tiết gieo mầm %q gieo tại chương %d, không thể đẩy hoặc thu tại chương %d: %w",
						i, update.ID, at, a.Chapter, errs.ErrToolPrecondition)
				}
			}
		}
	}
	return nil
}