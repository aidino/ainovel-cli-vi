package host

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// Cỗ máy trạng thái ngân sách: tiến lên đơn điệu, mỗi lần di chuyển kích hoạt tác dụng phụ đúng một lần, không lùi lại.
// Tăng ngân sách = Người dùng ủy quyền lại = Sửa cấu hình rồi khởi động lại/Instance Host mới, không lùi trạng thái trong instance hiện tại.
const (
	budgetNormal      int32 = iota // Chưa đến mức cảnh báo
	budgetWarned                   // Đã phát cảnh báo, chưa vượt tuyến
	budgetStopPending              // Đã vượt tuyến, chờ dừng máy ở ranh giới subagent
	budgetStopped                  // Đã thực thi dừng máy
)

// BudgetSentinel giám sát chi phí lũy kế, thực thi chính sách ngân sách của người dùng (khối config budget).
//
// Định vị hợp hiến (architecture.md §8.3/§10): không đánh giá hành vi model - dừng máy khi vượt tuyến
// tương đương với việc người dùng nhấn Abort thủ công vào thời điểm đó, Host chỉ thay mặt thực thi
// một chỉ thị đã ký trước. Nó ảnh hưởng đến luồng điều khiển, do đó không phải là observer,
// định vị là thành phần chính sách Host ngang cấp với flow.Dispatcher; Route/tầng công cụ không nhận biết.
//
// Thời điểm dừng máy: mặc định ở ranh giới subagent (Host gọi đồng bộ HandleBoundary), không lãng phí chương đang chạy (in-flight);
// khi hardStop=true thì vượt tuyến lập tức dừng. Xử lý ranh giới diễn ra trước khi flow.Dispatcher phái phát bước tiếp theo, Route/tầng công cụ không nhận biết ngân sách.
type BudgetSentinel struct {
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64              // Chi phí lũy kế hiện tại (bọc usage.Totals; có thể tiêm stub kiểm thử)
	abort   func(reason string)         // Bọc dừng máy Host (kèm sự kiện nguyên nhân)
	report  func(level, summary string) // Đầu ra cảnh báo (emitEvent + notify, do Host tiêm)

	state atomic.Int32

	// Phát hiện vùng mù tính phí: model không có giá trong registry và provider không tự báo cáo cost thì mỗi lần ghi sổ tăng thêm $0,
	// ngân sách vô hiệu hóa một cách im lặng. Phán đoán dựa trên "nhiều lần tăng không liên tiếp" thay vì total==0 - cách sau
	// không bắt được trường hợp đổi sang model không giá giữa chừng bằng /model (total dừng ở giá trị lịch sử khác không nhưng không tăng nữa).
	// Model miễn phí cũng bị tính, thông báo "ngân sách sẽ không kích hoạt" cũng đúng với chúng.
	lastTotal   atomic.Uint64 // math.Float64bits(lần gọi lại trước của chi phí lũy kế)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool
}

// blindZeroStreak Báo động sau bao nhiêu lần ghi sổ tăng 0 liên tiếp. Model tính phí bình thường mỗi lần tăng chắc chắn > 0
// (cost là float lũy kế không làm tròn), chọn 5 chỉ để tránh nhiễu cực đoan, không phải là ngưỡng chính sách có thể điều chỉnh.
const blindZeroStreak = 5

// NewBudgetSentinel Tạo lính gác ngân sách; trả về nil khi chính sách chưa kích hoạt (mọi phương thức an toàn với nil).
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// OnCost Được UsageTracker gọi mỗi lần sau khi ghi sổ mang theo chi phí lũy kế mới nhất (ngoài khóa).
// Một lần gọi lại có thể vượt qua hai cấp (normal→warned→stopPending), hai tác dụng phụ được kích hoạt mỗi cái một lần.
func (s *BudgetSentinel) OnCost(total float64) {
	if s == nil {
		return
	}
	if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
		if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
			s.report("warn", fmt.Sprintf("Vùng mù ngân sách: Ghi sổ liên tục nhưng chi phí lũy kế dừng ở $%.2f không tăng nữa (model hiện tại không có giá trên registry và provider chưa tự báo cáo cost, hoặc là model miễn phí) - giới hạn ngân sách sẽ không kích hoạt", total))
		}
	} else {
		s.zeroStreak.Store(0)
	}
	if total >= s.limit*s.warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("Cảnh báo ngân sách: Đã tiêu $%.2f, đạt %.0f%% của ngân sách $%.2f", total, s.warnRatio*100, s.limit))
	}
	if total >= s.limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if s.hardStop {
			s.report("error", fmt.Sprintf("Ngân sách đã hết: Đã tiêu $%.2f, vượt quá ngân sách $%.2f, lập tức dừng máy", total, s.limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("Ngân sách đã hết: Đã tiêu $%.2f, vượt quá ngân sách $%.2f, sẽ dừng máy sau khi nhiệm vụ subagent hiện tại kết thúc", total, s.limit))
	}
}

// HandleEvent Thực thi dừng máy đang chờ ở ranh giới subagent. Đăng ký phải có trước Dispatcher.
// Không bỏ qua IsError - trả về lỗi cũng là ranh giới, không nên trì hoãn dừng máy do subagent thất bại.
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	s.HandleBoundary()
}

func (s *BudgetSentinel) HandleBoundary() bool {
	if s == nil || s.state.Load() != budgetStopPending {
		return false
	}
	s.stop(s.costNow())
	return true
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("Dừng máy do ngân sách: Đã tiêu $%.2f, vượt quá ngân sách $%.2f; có thể khôi phục chạy tiếp sau khi tăng budget.book_usd", total, s.limit))
	}
}

// Refuse Kiểm tra trước khi khởi động: Ngân sách đã vượt trả về lỗi từ chối (Start/Resume/Continue gọi trên đường dẫn khôi phục).
// Người dùng tăng ngân sách = ủy quyền lại, Refuse sẽ tự nhiên cho qua với cấu hình mới.
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	if cost := s.costNow(); cost >= s.limit {
		return fmt.Errorf("Sách này đã tiêu $%.2f, đạt giới hạn ngân sách $%.2f; vui lòng tăng cấu hình budget.book_usd rồi thử lại", cost, s.limit)
	}
	return nil
}

// Limit Trả về giới hạn ngân sách (dùng cho UI hiển thị); chưa kích hoạt trả về 0.
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	return s.limit
}
