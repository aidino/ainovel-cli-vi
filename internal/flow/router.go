// Package flow thực hiện định tuyến ngành dọc: Host dựa vào sự thật quyết định tiếp theo gọi subagent nào làm gì.
//
// Nguyên tắc thiết kế:
//   - Route là hàm thuần túy: nhập State, xuất *Instruction. Không IO, không gọi Store, có thể test độc lập.
//   - State được tạo từ Store bởi LoadState (không thuần túy), đọc toàn bộ sự thật mà định tuyến cần trong một lần.
//   - Trả về nil là hợp lệ: biểu thị hiện tại không có chỉ lệnh Worker nào có thể được suy ra từ sự thật xác định;
//     Engine sau đó xử lý theo trạng thái kết thúc, khởi động bù trừ hoặc đợi người dùng can thiệp.
//
// Router bao trùm các quyết định "loại tra bảng" (bước tiếp theo mỗi chương, xử lý sau cuối arc, dẫn động hàng đợi),
// không bao trùm các quyết định "loại hiểu ngữ nghĩa" (chọn nhà quy hoạch, xử lý Steer từ người dùng, xuất tóm tắt).
package flow

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// plannerForTier suy ra danh tính nhà quy hoạch từ cấp độ quy hoạch đã lưu xuống đĩa: short thuộc nhà quy hoạch truyện ngắn,
// mid/long thuộc nhà quy hoạch truyện dài (nhất quán với tiêu chí chọn lọc khi khởi động Arbiter).
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction chỉ thị Worker và nhiệm vụ mà Engine sẽ trực tiếp chạy ở bước tiếp theo.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // Mô tả nhiệm vụ cho subagent
	Reason  string // Lý do định tuyến (dùng cho sự kiện, log và phán quyết thất bại)
	Chapter int    // Số chương liên quan đến nhiệm vụ writer (viết tiếp/viết lại/đánh bóng); 0 là không liên quan (nhiệm vụ editor/architect)
}

type AggregateKind string

const (
	AggregateArcReview     AggregateKind = "arc_review"
	AggregateArcSummary    AggregateKind = "arc_summary"
	AggregateVolumeSummary AggregateKind = "volume_summary"
	AggregateGlobalReview  AggregateKind = "global_review"
)

type AggregateRefresh struct {
	Kind         AggregateKind
	Volume       int
	Arc          int
	StartChapter int
	EndChapter   int
}

// State là đầu vào của Route: tất cả sự thật phải được khai báo rõ ràng ở đây, cấm Route tự đọc Store bên trong.
type State struct {
	Progress *domain.Progress

	// Số chương lớn nhất trong các chương đã hoàn thành; 0 biểu thị chưa bắt đầu sáng tác.
	LastCompleted int

	// Thông tin ranh giới arc của chương trước; khi IsArcEnd=false các trường khác không có ý nghĩa.
	// Nên là nil khi LastCompleted=0 hoặc không phải chế độ Layered.
	ArcBoundary *storepkg.ArcBoundary

	// Ba sự thật xử lý sau cuối arc: đọc kiểm / tóm tắt arc / tóm tắt tập đã hoàn thành chưa.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// Các mục thiết lập nền tảng bị thiếu (tín hiệu bổ sung trong giai đoạn quy hoạch).
	FoundationMissing []string

	// Cấp độ quy hoạch đã lưu (ghi vào RunMeta khi save_foundation lưu scale).
	// Rỗng = quy hoạch lần đầu chưa tạo ra bất kỳ thiết lập nào, không thể xác định danh tính nhà quy hoạch.
	PlanningTier domain.PlanningTier

	// Sách phi phân tầng: chương hoàn thành gần nhất đã có đọc kiểm toàn cục scope=global chưa
	//(chỉ có ý nghĩa tại điểm kích hoạt ShouldReview; sách phân tầng luôn là false).
	HasGlobalReview bool

	// Ảnh hưởng sửa đổi bên ngoài phải được Architect xử lý trước khi viết tiếp. Phản hồi Writer thông thường để lại cho lần
	// thao tác cấu trúc tự nhiên tiếp theo hấp thụ đồng loạt, không điều phối thêm nhà quy hoạch cho mỗi chương.
	ImmediateFeedbackCount int

	// Công cụ arc/tập cần Editor tạo lại sớm nhất sau sửa đổi bên ngoài.
	AggregateRefresh *AggregateRefresh
}

// Route trả về chỉ lệnh xác định bước tiếp theo dựa trên sự thật; trả về nil thì Engine xử lý theo ngữ cảnh gọi.
//
// Độ ưu tiên quyết định (loại trừ lẫn nhau, khớp cái đầu tiên từ trên xuống dưới):
//  1. Phase=Complete        → nil (Host xuất tóm tắt xác định)
//  2. Thiếu mục thiết lập kỳ quy hoạch và xác định được nhà quy hoạch → cùng nhà quy hoạch bù đắp; nếu không nil (Engine khởi động phán quyết bổ sung)
//  3. PendingRewrites khác rỗng  → writer viết lại/đánh bóng theo hàng đợi
//  4. Flow=Reviewing        → nil (dormant: hiện không có người viết, Flow kỳ đọc kiểm thực tế là writing)
//  5. Flow=Steering         → nil (đang xử lý can thiệp của người dùng)
//  6. Sửa đổi bên ngoài làm công cụ tổng hợp hết hiệu lực → editor tạo lại
//  7. Sửa đổi bên ngoài ảnh hưởng quy hoạch sau     → architect xử lý
//  8. Sách phân tầng đến cuối arc          → đọc kiểm, tóm tắt, mở rộng arc hoặc viết tiếp tập
//  9. Đọc kiểm toàn cục phi phân tầng đến hạn       → editor(global review)
//
// 10. Đại cương phi phân tầng đã cạn        → architect (quyết định hoàn kết hoặc nối tiếp đại cương)
// 11. Khác                   → writer (viết next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. Trạng thái kết thúc: Host tạo tóm tắt xác định dựa trên sự thật store
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. Bổ sung kỳ quy hoạch: quyết định loại tra bảng——thiếu gì ở store, danh tính nhà quy hoạch suy ra từ scale đã lưu
	//    (short → architect_short, còn lại → architect_long). tier rỗng nghĩa là quy hoạch lần đầu
	//    chưa lưu bất kỳ thiết lập nào (chọn lọc là phán đoán ngữ nghĩa), do planStartFallback của Engine phán quyết bổ sung.
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("Bổ sung các mục thiếu của thiết lập nền tảng và thông tin tác phẩm: %s; book dùng save_book, các thiết lập nền tảng còn lại dùng save_foundation để ghi xuống đĩa", strings.Join(s.FoundationMissing, ", "))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "Thiết lập nền tảng đã đủ: gọi lại novel_context đọc toàn bộ sản phẩm đã ghi xuống đĩa và foundation_status.fingerprint, kiểm tra tính nhất quán ngữ nghĩa xuyên file rồi gọi audit_foundation; có vấn đề thì sửa trước và kiểm lại"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "Thiết lập nền tảng còn thiếu mục, tiếp tục điều cùng một quy hoạch sư theo mục thiếu",
			}
		}
		return nil
	}

	// 3. Ưu tiên hàng đợi viết lại/đánh bóng (sự thật đã lưu ở tầng công cụ, Router chỉ điều phối theo lệnh)
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Viết lại"
		if p.Flow == domain.FlowPolishing {
			verb = "Đánh bóng"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s chương %d", verb, ch),
			Reason:  fmt.Sprintf("Hàng chờ PendingRewrites còn %d chương", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. Đang đọc kiểm → trả về LLM. Hiện là nhánh dormant: save_review chỉ đặt Flow thành
	//    writing/rewriting/polishing, không có bất kỳ đường dẫn sản xuất nào đặt reviewing (Flow kỳ đọc kiểm thực tế là writing,
	//    "đọc kiểm trước khi viết tiếp" được đảm bảo bởi ưu tiên steering của agentcore, không phụ thuộc nhánh này). Giữ lại để đối xứng với Steering
	//    và nhường định tuyến cho LLM khi editor đặt rõ ràng reviewing trong kỳ đọc kiểm tương lai.
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. Đang xử lý can thiệp của người dùng: Arbiter đang phán quyết, Engine không chiếm quyền
	if p.Flow == domain.FlowSteering {
		return nil
	}
	if refresh := s.AggregateRefresh; refresh != nil {
		switch refresh.Kind {
		case AggregateArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Đọc kiểm tập %d arc %d (chương %d-%d): gọi novel_context(chapter=%d), save_review dùng scope=arc, chapter=%d",
					refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter,
				),
				Reason: "Thiếu đọc kiểm cấp arc",
			}
		case AggregateArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Sinh tóm tắt arc %d tập %d, ảnh chụp nhân vật và quy tắc văn phong (save_arc_summary)", refresh.Volume, refresh.Arc),
				Reason: "Thiếu tóm tắt cấp arc",
			}
		case AggregateVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Sinh tóm tắt tập %d (save_volume_summary)", refresh.Volume),
				Reason: "Thiếu tóm tắt tập",
			}
		case AggregateGlobalReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Đọc kiểm %d chương đầu: gọi novel_context(chapter=%d), save_review dùng scope=global, chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "Thiếu đọc kiểm toàn cục",
			}
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   "Chỉ xử lý writer_feedback chỉnh sửa bên ngoài trong novel_context: đối chiếu tình tiết đã xảy ra với kế hoạch sau này, cần điều chỉnh thì gọi revise_outline hoặc công cụ cấu trúc tương ứng, không cần điều chỉnh thì gọi resolve_outline_feedback; không được xử lý foundation_status hay quy hoạch khác, sau khi ghi xuống đĩa kết thúc bằng một câu",
			Reason: fmt.Sprintf("Có %d ảnh hưởng chỉnh sửa bên ngoài chưa lan tới quy hoạch sau", s.ImmediateFeedbackCount),
		}
	}

	// 8. Xử lý sau cuối arc ở chế độ phân tầng
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Đọc kiểm cấp arc cho tập %d arc %d (chương %d-%d): gọi novel_context(chapter=%d), save_review dùng scope=arc, chapter=%d; issues[].chapters chỉ được rơi vào đúng khoảng đó",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				),
				Reason: "Đọc kiểm cuối arc chưa hoàn thành",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Sinh tóm tắt arc %d tập %d, ảnh chụp nhân vật và quy tắc văn phong (save_arc_summary)", b.Volume, b.Arc),
				Reason: "Tóm tắt arc chưa hoàn thành",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Sinh tóm tắt tập %d (save_volume_summary)", b.Volume),
				Reason: "Tóm tắt tập chưa hoàn thành",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("Mở rộng arc %d của tập %d (save_foundation type=expand_arc)", b.NextVolume, b.NextArc),
				Reason: "Arc khung xương kế tiếp chờ triển khai",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "Tạo tập tiếp theo: đánh giá theo danh sách kiểm tra hoàn thành rồi gọi save_foundation — truyện cần tiếp tục → type=append_volume; truyện gần điểm kết → type=append_volume và tầng trên cùng JSON tập kèm \"final\": true (tập ca nhận, thu toàn bộ đường dây, viết xong tự hoàn thành); mọi điều kiện hoàn thành đã thỏa ngay lúc này → type=complete_book. Cả ba lựa chọn đều phải kèm tham số reason nêu rõ lý do phán quyết",
				Reason: "Cuối tập cần quyết định thêm tập mới, tập ca nhận hay kết thúc toàn truyện",
			}
		}
	}

	// 11. Đọc kiểm toàn cục phi phân tầng: Mỗi ReviewInterval chương một lần (sự thật: global review của chương đó chưa lưu).
	//     Vốn là tín hiệu review_required trong giá trị trả về của commit_chapter, nay suy ra theo sự thật——
	//     giá trị trả về chỉ là hình ảnh phản chiếu của sự thật, Route xem trực tiếp cùng một sự thật từ store.
	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Đọc kiểm toàn cục %d chương đầu (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted),
				Reason: reason,
			}
		}
	}

	// 12. Khi đại cương phi phân tầng cạn kiệt, không thể tiếp tục điều phối chương vượt giới hạn. Để Architect dựa trên sự thật câu chuyện hiện tại
	// quyết định hoàn kết, hoặc dùng revise_outline nối tiếp kế hoạch từ chương next.
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{
			Agent: plannerForTier(s.PlanningTier),
			Task: fmt.Sprintf(
				"Đại cương phi phân tầng đã viết xong (đã hoàn thành %d chương, tổng %d chương): nếu truyện đã thu xong, gọi save_foundation(type=complete_book); nếu vẫn cần tiếp tục, dùng revise_outline nối tiếp kế hoạch từ chương %d",
				len(p.CompletedChapters), p.TotalChapters, next,
			),
			Reason: "Đại cương phi phân tầng đã cạn, cần quyết định hoàn thành hay nối tiếp",
		}
	}

	// 13. Viết tiếp bình thường
	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("Viết chương %d", next),
		Reason:  "Viết tiếp chương kế",
		Chapter: next,
	}
}
