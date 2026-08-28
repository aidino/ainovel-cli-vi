package domain

import (
	"fmt"
	"strings"
)

// Phase biểu thị giai đoạn sáng tác tiểu thuyết.
type Phase string

const (
	PhaseInit     Phase = "init"
	PhasePremise  Phase = "premise"
	PhaseOutline  Phase = "outline"
	PhaseWriting  Phase = "writing"
	PhaseComplete Phase = "complete"
)

// FlowState loại quy trình hoạt động hiện tại, dùng để khôi phục checkpoint.
type FlowState string

const (
	FlowWriting   FlowState = "writing"
	FlowReviewing FlowState = "reviewing"
	FlowRewriting FlowState = "rewriting"
	FlowPolishing FlowState = "polishing"
	FlowSteering  FlowState = "steering"
)

// PlanningTier biểu thị cấp độ độ dài quy hoạch tác phẩm.
type PlanningTier string

const (
	PlanningTierShort PlanningTier = "short"
	PlanningTierMid   PlanningTier = "mid"
	PlanningTierLong  PlanningTier = "long"
)

// Progress theo dõi tiến độ, cố định vào meta/progress.json.
type Progress struct {
	Phase          Phase `json:"phase"`
	CurrentChapter int   `json:"current_chapter"`
	// TotalChapters trong chế độ không phân tầng là số chương đại cương chi tiết; trong chế độ phân tầng chỉ là giá trị dung lượng bên trong bao gồm ước tính bộ khung, dùng cho chiến lược ngữ cảnh, không đại diện cho tổng số chương cố định của toàn bộ sách.
	TotalChapters     int         `json:"total_chapters"`
	CompletedChapters []int       `json:"completed_chapters"`
	TotalWordCount    int         `json:"total_word_count"`
	ChapterWordCounts map[int]int `json:"chapter_word_counts,omitempty"` // Số từ mỗi chương, hỗ trợ sửa tổng số từ khi viết lại
	InProgressChapter int         `json:"in_progress_chapter,omitempty"` // Chương đang viết (khôi phục cấp độ scene)
	CompletedScenes   []int       `json:"completed_scenes,omitempty"`    // Mã số scene đã hoàn thành của chương hiện tại
	Flow              FlowState   `json:"flow,omitempty"`                // Quy trình hiện tại
	PendingRewrites   []int       `json:"pending_rewrites,omitempty"`    // Hàng đợi chương chờ viết lại
	RewriteReason     string      `json:"rewrite_reason,omitempty"`      // Lý do viết lại
	StrandHistory     []string    `json:"strand_history,omitempty"`      // Ghi lại dominant_strand theo thứ tự chương
	HookHistory       []string    `json:"hook_history,omitempty"`        // Ghi lại hook_type theo thứ tự chương
	// Theo dõi phân tầng trường thiên (chỉ dùng cho chế độ trường thiên, đoản thiên/trung thiên là giá trị 0)
	CurrentVolume int  `json:"current_volume,omitempty"`
	CurrentArc    int  `json:"current_arc,omitempty"`
	Layered       bool `json:"layered,omitempty"`
	// ReopenedFromComplete đánh dấu cuốn sách này được reopen từ trạng thái hoàn kết để vào làm lại. Làm lại chỉ sửa chương đã có, không thêm bớt cấu trúc, nên sau khi làm rỗng thì nên cho qua theo nguyên tắc "cấu trúc hoàn chỉnh tức là hoàn kết lại" (tránh việc chi tiết gieo mầm cuối quyển cuối bị nhiễu loạn do làm lại rồi kẹt ở writing → vòng lặp vô hạn viết tiếp vượt biên); viết tiến tới không đặt cờ này, phán đoán hoàn kết giữ nguyên ngữ nghĩa bảo thủ của việc thu thập manh mối.
	ReopenedFromComplete bool `json:"reopened_from_complete,omitempty"`
	// ReopenCount ghi lại số lần tích lũy cuốn sách này được mở lại từ trạng thái hoàn kết (sự thật kiểm toán /reopen). Nó đồng thời đảm bảo việc hoàn kết lại sau khi mở lại có nội dung progress.json khác với lần hoàn kết trước: checkpoint khử trùng lặp cho cùng digest, hoàn kết lại giống hệt byte sẽ không tạo checkpoint mới, StopGuard sẽ hiểu nhầm complete_book thành công là chạy không tải và nâng cấp lên chấm dứt.
	ReopenCount int `json:"reopen_count,omitempty"`
}

// IsResumable đánh giá xem có thể khôi phục từ điểm dừng không.
func (p *Progress) IsResumable() bool {
	return p.Phase == PhaseWriting && p.CurrentChapter > 0
}

// NextChapter trả về số chương tiếp theo cần viết.
func (p *Progress) NextChapter() int {
	return p.LatestCompleted() + 1
}

// LatestCompleted trả về số chương lớn nhất đã hoàn thành; nếu không có chương hoàn thành thì trả về 0.
func (p *Progress) LatestCompleted() int {
	max := 0
	for _, ch := range p.CompletedChapters {
		if ch > max {
			max = ch
		}
	}
	return max
}

// ContextProfile chiến lược tải ngữ cảnh, tự thích ứng theo tổng số chương.
type ContextProfile struct {
	SummaryWindow  int  // Tải tóm tắt N chương gần nhất
	TimelineWindow int  // Tải dòng thời gian N chương gần nhất
	Layered        bool // true = Bật tải tóm tắt phân tầng (tóm tắt tập + tóm tắt arc + tóm tắt chương)
}

// MemoryPolicy biểu thị chiến lược sử dụng bộ nhớ chia sẻ lúc runtime.
// Nó vừa dùng cho đầu ra ngữ cảnh, vừa dùng cho quyết định handoff / reminder ở tầng host.
type MemoryPolicy struct {
	Mode                string `json:"mode,omitempty"`
	SummaryWindow       int    `json:"summary_window,omitempty"`
	TimelineWindow      int    `json:"timeline_window,omitempty"`
	LayeredSummaries    bool   `json:"layered_summaries,omitempty"`
	SummaryStrategy     string `json:"summary_strategy,omitempty"`
	WorkingRefresh      string `json:"working_refresh,omitempty"`
	EpisodicRefresh     string `json:"episodic_refresh,omitempty"`
	PlanningRefresh     string `json:"planning_refresh,omitempty"`
	FoundationRefresh   string `json:"foundation_refresh,omitempty"`
	PlanningFocus       string `json:"planning_focus,omitempty"`
	FoundationFocus     string `json:"foundation_focus,omitempty"`
	PreviousTailChars   int    `json:"previous_tail_chars,omitempty"`
	ChapterPlanEnabled  bool   `json:"chapter_plan_enabled,omitempty"`
	RelatedLookup       bool   `json:"related_chapter_lookup,omitempty"`
	CurrentOutlineBound bool   `json:"current_outline_bound,omitempty"`
	HandoffPreferred    bool   `json:"handoff_preferred,omitempty"`
	ReadOnlyThreshold   int    `json:"read_only_threshold,omitempty"`
}

// NewContextProfile tính toán chiến lược ngữ cảnh theo tổng số chương.
func NewContextProfile(totalChapters int) ContextProfile {
	switch {
	case totalChapters <= 15:
		return ContextProfile{SummaryWindow: 10, TimelineWindow: 10}
	case totalChapters <= 50:
		return ContextProfile{SummaryWindow: 5, TimelineWindow: 8}
	default:
		return ContextProfile{SummaryWindow: 3, TimelineWindow: 5, Layered: true}
	}
}

// NewChapterMemoryPolicy tạo chiến lược bộ nhớ runtime cho chương dựa theo tiến độ và chiến lược ngữ cảnh.
func NewChapterMemoryPolicy(progress *Progress, profile ContextProfile, currentOutlineBound bool) MemoryPolicy {
	policy := MemoryPolicy{
		Mode:                "chapter",
		SummaryWindow:       profile.SummaryWindow,
		TimelineWindow:      profile.TimelineWindow,
		LayeredSummaries:    profile.Layered,
		WorkingRefresh:      "làm mới mỗi lần tải theo chương",
		EpisodicRefresh:     "làm mới theo nộp chương, đọc kiểm và thay đổi trạng thái trường thiên",
		PreviousTailChars:   800,
		ChapterPlanEnabled:  true,
		CurrentOutlineBound: currentOutlineBound,
		ReadOnlyThreshold:   5,
	}
	if profile.Layered {
		policy.SummaryStrategy = "tóm tắt tập + tóm tắt arc + tóm tắt chương gần đây"
	} else {
		policy.SummaryStrategy = "tóm tắt chương gần đây"
	}
	if progress != nil {
		if progress.TotalChapters > 30 {
			policy.RelatedLookup = true
		}
		if progress.Flow == FlowReviewing || progress.Flow == FlowRewriting || progress.Flow == FlowPolishing {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.HandoffPreferred = true
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.ReadOnlyThreshold = 4
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.ReadOnlyThreshold = 4
		}
	}
	return policy
}

// NewArchitectMemoryPolicy trả về chiến lược bộ nhớ sử dụng ở giai đoạn quy hoạch.
func NewArchitectMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{
		Mode:               "architect",
		PlanningRefresh:    "làm mới khi cấu trúc tập arc, la bàn hoặc tóm tắt cập nhật",
		FoundationRefresh:  "làm mới khi nhân vật, chi tiết gieo mầm, thiết lập thay đổi",
		PlanningFocus:      "đại cương phân tầng, la bàn, tóm tắt tập",
		FoundationFocus:    "thiết lập nhân vật, ảnh chụp nhân vật, sổ chi tiết gieo mầm",
		HandoffPreferred:   true,
		ChapterPlanEnabled: false,
		ReadOnlyThreshold:  4,
	}
}

// RunMeta thông tin meta chạy, cố định vào meta/run.json.
type RunMeta struct {
	StartedAt            string             `json:"started_at"`
	Provider             string             `json:"provider,omitempty"`
	Style                string             `json:"style"`
	Model                string             `json:"model"`
	PlanningTier         PlanningTier       `json:"planning_tier,omitempty"`
	StartPrompt          string             `json:"start_prompt,omitempty"`           // Nhu cầu sáng tác ban đầu của người dùng (sự thật đầu vào, ghi đĩa trước khi phán quyết khởi động; sau khi phán quyết thất bại thì dựa vào đây để phán quyết lại)
	PlanStart            *PlanStartRecord   `json:"plan_start,omitempty"`             // Sự thật phán quyết khởi động, căn cứ duy nhất để khôi phục sập nguồn kỳ quy hoạch
	PendingSteer         string             `json:"pending_steer,omitempty"`          // Lệnh Steer chưa hoàn thành, bơm lại khi khôi phục gián đoạn
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`                     // Chế độ đẩy chương: auto / review
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"` // Số chương tiến tới được cấp phép một lần trong chế độ review
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`           // Ý định tạm dừng một lần được ký bởi can thiệp hiện tại
}

// ChapterAdvanceMode quyết định xem chương mới có cần cấp phép từng chương không.
type ChapterAdvanceMode string

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

// Valid báo cáo xem chế độ đẩy chương có được phiên bản hiện tại hỗ trợ không.
func (m ChapterAdvanceMode) Valid() bool {
	return m == ChapterAdvanceAuto || m == ChapterAdvanceReview
}

// UnsupportedAdvanceModeError biểu thị chế độ điều khiển của sách không được binary hiện tại hỗ trợ.
// Bên gọi phải dừng việc tạo Host có thể viết, và nhắc người dùng sử dụng phiên bản phù hợp; cấm suy đoán hạ cấp.
type UnsupportedAdvanceModeError struct {
	Mode ChapterAdvanceMode
}

func (e *UnsupportedAdvanceModeError) Error() string {
	return fmt.Sprintf("chế độ đẩy chương không được hỗ trợ %q, vui lòng dùng bản ainovel mới hơn đã tạo dự án này", e.Mode)
}

// AdvanceHoldAfter là điều kiện kích hoạt có tính xác định của việc tạm dừng một lần.
type AdvanceHoldAfter string

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

// Valid báo cáo xem điều kiện tạm dừng có được phiên bản hiện tại hỗ trợ không.
func (a AdvanceHoldAfter) Valid() bool {
	return a == AdvanceHoldAtBoundary || a == AdvanceHoldAfterRewritesDrained || a == AdvanceHoldAtChapter
}

// AdvanceHold là ý định tạm dừng một lần được ký bởi can thiệp hiện tại, do biên Host tiêu thụ.
type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}

// Validate xác thực ràng buộc cấu trúc của chính ý định tạm dừng một lần.
func (h AdvanceHold) Validate() error {
	if !h.After.Valid() {
		return fmt.Errorf("điều kiện tạm dừng một lần không được hỗ trợ %q", h.After)
	}
	if h.After == AdvanceHoldAtChapter {
		if h.TargetChapter <= 0 {
			return fmt.Errorf("chương mục tiêu phải lớn hơn 0")
		}
	} else if h.TargetChapter != 0 {
		return fmt.Errorf("điều kiện tạm dừng %q không được đặt chương mục tiêu", h.After)
	}
	if strings.TrimSpace(h.Reason) == "" {
		return fmt.Errorf("lý do tạm dừng một lần không được rỗng")
	}
	return nil
}

// PlanStartRecord sự thật cố định của phán quyết khởi động (phán quyết ghi sự thật trước, rồi mới thực thi; khôi phục không phán quyết lại).
// Sau khi save_foundation đầu tiên ghi đĩa scale, khôi phục kỳ quy hoạch đổi sang do PlanningTier suy luận, bản ghi này chỉ bao phủ khoảng thời gian "từ lúc hoàn thành phán quyết đến lần ghi đĩa đầu tiên". DecisionID liên kết kiểm toán decisions.jsonl.
type PlanStartRecord struct {
	RawPrompt   string `json:"raw_prompt"`
	Planner     string `json:"planner"`
	PlannerTask string `json:"planner_task"`
	DecisionID  string `json:"decision_id,omitempty"`
}