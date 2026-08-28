package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// InterventionFacts là gói sự kiện cho phân loại can thiệp (snapshot thời điểm Collect).
// Engine đối chiếu Phase/QueueHead trước khi thực thi Dispatch ở biên (giữa tham vấn và thực thi có thể bị dán đoạn)
// worker chạy, sự kiện có thể đã được đẩy lên; không khớp → hủy bỏ và hỏi lại bằng sự kiện mới).
type InterventionFacts struct {
	Phase                    string           `json:"phase,omitempty"`
	Flow                     string           `json:"flow,omitempty"`
	Title                    string           `json:"title,omitempty"`
	CompletedChapters        int              `json:"completed_chapters"`
	OutlinedChapters         int              `json:"outlined_chapters,omitempty"`
	DynamicPlanning          bool             `json:"dynamic_planning"`
	NextChapter              int              `json:"next_chapter,omitempty"`
	PendingRewrites          []int            `json:"pending_rewrites,omitempty"`
	ReopenCount              int              `json:"reopen_count,omitempty"` // Số lần người dùng dùng /reopen mở lại sách đã hoàn kết
	FoundationMissing        []string         `json:"foundation_missing,omitempty"`
	PlanningTier             string           `json:"planning_tier,omitempty"`
	AdvanceMode              string           `json:"advance_mode,omitempty"`
	HasAdvanceHold           bool             `json:"has_advance_hold"`
	AdvanceHoldAfter         string           `json:"advance_hold_after,omitempty"`
	AdvanceHoldTargetChapter int              `json:"advance_hold_target_chapter,omitempty"`
	AdvanceHoldReason        string           `json:"advance_hold_reason,omitempty"`
	Running                  bool             `json:"running"`                  // Khi can thiệp đến, có run nào đang chạy không
	CheckpointSeq            int64            `json:"checkpoint_seq,omitempty"` // Checkpoint mới nhất lúc Collect; cho Engine đối chiếu
	RecentDecisions          []RecentDecision `json:"recent_decisions,omitempty"`
}

// RecentDecision là ký ức can thiệp: tóm tắt vài lần phán quyết gần nhất, bao quát tham chiếu chéo kiểu "lần trước sửa sao rồi".
type RecentDecision struct {
	At     string `json:"at"`
	Input  string `json:"input"`
	Reason string `json:"reason,omitempty"`
}

// QueueHead trả về đầu hàng đợi làm lại (nếu không có thì trả về 0), cho Engine đối chiếu.
func (f InterventionFacts) QueueHead() int {
	if len(f.PendingRewrites) > 0 {
		return f.PendingRewrites[0]
	}
	return 0
}

// CollectInterventionFacts đọc đủ sự kiện phân loại từ store. Bất kỳ lỗi đọc sự kiện kiểm soát nào cũng báo lỗi rõ ràng.
// trả về lỗi, cấm Arbiter đưa ra quyết định ngữ nghĩa trên snapshot không hoàn chỉnh được ghép từ giá trị không.
func CollectInterventionFacts(st *storepkg.Store) (InterventionFacts, error) {
	var f InterventionFacts
	if st == nil {
		return f, fmt.Errorf("store không được rỗng")
	}
	missing, err := st.FoundationMissing()
	if err != nil {
		return f, fmt.Errorf("đọc trạng thái thiết lập nền tảng: %w", err)
	}
	f.FoundationMissing = missing
	book, err := st.Book.Load()
	if err != nil {
		return f, fmt.Errorf("đọc thông tin tác phẩm: %w", err)
	}
	if book != nil {
		f.Title = book.Title
	}
	p, err := st.Progress.Load()
	if err != nil {
		return f, fmt.Errorf("đọc tiến độ: %w", err)
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.Flow = string(p.Flow)
		f.CompletedChapters = len(p.CompletedChapters)
		f.DynamicPlanning = p.Layered
		if p.Layered {
			outline, outlineErr := st.Outline.LoadOutline()
			if outlineErr != nil {
				return f, fmt.Errorf("đọc đại cương chi tiết hiện tại: %w", outlineErr)
			}
			f.OutlinedChapters = len(outline)
		} else {
			f.OutlinedChapters = p.TotalChapters
		}
		f.NextChapter = p.NextChapter()
		f.PendingRewrites = append([]int(nil), p.PendingRewrites...)
		f.ReopenCount = p.ReopenCount
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return f, fmt.Errorf("đọc metadata vận hành: %w", err)
	}
	if meta != nil {
		f.PlanningTier = string(meta.PlanningTier)
		f.AdvanceMode = string(meta.AdvanceMode)
		if meta.AdvanceHold != nil {
			f.HasAdvanceHold = true
			f.AdvanceHoldAfter = string(meta.AdvanceHold.After)
			f.AdvanceHoldTargetChapter = meta.AdvanceHold.TargetChapter
			f.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		f.CheckpointSeq = cp.Seq
	}
	recent, err := st.Decisions.Recent(5)
	if err != nil {
		return f, fmt.Errorf("đọc phán quyết gần đây: %w", err)
	}
	for _, r := range recent {
		if r.Kind != "intervention" {
			continue
		}
		f.RecentDecisions = append(f.RecentDecisions, RecentDecision{
			At: r.At, Input: truncateRunes(r.Input, 80), Reason: r.Reason,
		})
	}
	return f, nil
}

// AdvanceHoldOp hành động tạm dừng một lần: tạm dừng tại biên giới công việc, sau khi dọn sạch làm lại, hoặc khi hoàn thành chương mục tiêu; cũng có thể dùng để hủy.
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}

// ReopenOp làm lại toàn bộ sách: đưa sách về trạng thái làm lại và cho chương mục tiêu vào hàng đợi (chỉ hợp lệ khi phase=complete).
type ReopenOp struct {
	Chapters []int  `json:"chapters"`
	Reason   string `json:"reason,omitempty"`
}

// InterventionDecision phán quyết can thiệp. Tổ hợp hành động tự do, trình tự thực thi do Engine cố định:
// answer → rules → hold → reopen → dispatch; có tối đa một dispatch (do sự kiện giới hạn).
type InterventionDecision struct {
	Answer   string         `json:"answer,omitempty"`
	Rules    string         `json:"rules,omitempty"`
	Hold     *AdvanceHoldOp `json:"hold,omitempty"`
	Reopen   *ReopenOp      `json:"reopen,omitempty"`
	Dispatch *DispatchOp    `json:"dispatch,omitempty"`
	Reason   string         `json:"reason"`
}

var interventionContract = llmcontract.Contract{
	Name:        "arbiter_intervention",
	Description: "Phán quyết can thiệp người dùng: trả lời, quy tắc, tạm dừng, mở lại và điều phát",
	Schema: schema.Object(
		schema.Property("answer", llmcontract.Nullable(schema.String("văn bản hiển thị lại cho người dùng; rỗng thì null"))).Required(),
		schema.Property("rules", llmcontract.Nullable(schema.String("nguyên văn quy tắc viết dài hạn cần ghi xuống đĩa; rỗng thì null"))).Required(),
		schema.Property("hold", llmcontract.Nullable(schema.Object(
			schema.Property("cancel", schema.Bool("có hủy lệnh tạm dừng một lần đang có hay không")).Required(),
			schema.Property("after", llmcontract.Nullable(schema.Enum("điểm kích hoạt tạm dừng; khi hủy là null", string(domain.AdvanceHoldAtBoundary), string(domain.AdvanceHoldAfterRewritesDrained), string(domain.AdvanceHoldAtChapter)))).Required(),
			schema.Property("target_chapter", llmcontract.Nullable(schema.Int("chương mục tiêu khi after=chapter; trường hợp khác là null"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("tóm tắt nguyện vọng người dùng; khi hủy có thể là null"))).Required(),
		))).Required(),
		schema.Property("reopen", llmcontract.Nullable(schema.Object(
			schema.Property("chapters", schema.Array("số chương cần mở lại", schema.Int("số chương"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("lý do mở lại"))).Required(),
		))).Required(),
		schema.Property("dispatch", dispatchSchema("mục tiêu điều phát; khi không cần điều phát là null")).Required(),
		schema.Property("reason", schema.String("lý do phán quyết trong một câu")).Required(),
	),
}

// ValidateAgainst thực hiện kiểm tra cơ học theo sự kiện (tính hợp pháp trong kịch bản; hành động vượt kịch bản đã bị hệ thống kiểu từ chối).
func (d *InterventionDecision) ValidateAgainst(f InterventionFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được rỗng")
	}
	if d.Answer == "" && d.Rules == "" && d.Hold == nil && d.Reopen == nil && d.Dispatch == nil {
		return fmt.Errorf("quyết định rỗng: ít nhất phải có một hành động hoặc answer")
	}
	if err := d.Dispatch.validate(); err != nil {
		return err
	}
	if err := validateDispatchAgainst(d.Dispatch, f.Phase); err != nil {
		return err
	}
	complete := f.Phase == string(domain.PhaseComplete)
	if d.Reopen != nil {
		if !complete {
			return fmt.Errorf("reopen chỉ dùng cho kỳ hoàn thành (phase=%s hiện tại)", f.Phase)
		}
		if len(d.Reopen.Chapters) == 0 {
			return fmt.Errorf("reopen.chapters không được rỗng")
		}
		for _, ch := range d.Reopen.Chapters {
			if ch < 1 || ch > f.CompletedChapters {
				return fmt.Errorf("chương reopen %d vượt ranh (đã hoàn thành %d chương)", ch, f.CompletedChapters)
			}
		}
	}
	if complete && d.Dispatch != nil {
		return fmt.Errorf("kỳ hoàn thành cấm điều phát trực tiếp; làm lại dùng reopen (sau khi vào hàng, Router tự điều phát)")
	}
	if d.Hold != nil && !d.Hold.Cancel {
		if f.Phase != string(domain.PhaseWriting) {
			return fmt.Errorf("tạm dừng một lần chỉ dùng cho giai đoạn viết (phase=%s hiện tại)", f.Phase)
		}
		hold := domain.AdvanceHold{After: d.Hold.After, TargetChapter: d.Hold.TargetChapter, Reason: d.Hold.Reason}
		if err := hold.Validate(); err != nil {
			return fmt.Errorf("hold không hợp lệ: %w", err)
		}
		nextChapter := f.NextChapter
		if nextChapter == 0 {
			nextChapter = f.CompletedChapters + 1
		}
		if hold.After == domain.AdvanceHoldAtChapter && hold.TargetChapter < nextChapter {
			return fmt.Errorf("chương mục tiêu %d sớm hơn chương kế tiếp hiện tại %d", hold.TargetChapter, nextChapter)
		}
	}
	return nil
}

// validateDispatchAgainst áp dụng kỷ luật giai đoạn vào hệ thống phòng thủ cơ học. Architect có thể
// bảo trì cấu trúc trong kỳ quy hoạch và kỳ sáng tác; Writer/Editor chỉ có thể tiêu thụ
// dữ kiện tác phẩm đã hoàn thiện và tiến vào writing.
func validateDispatchAgainst(dispatch *DispatchOp, phase string) error {
	if dispatch == nil {
		return nil
	}
	if phase == "" {
		return fmt.Errorf("thiếu phase, cấm thực thi điều phát")
	}
	if phase == string(domain.PhaseComplete) {
		return fmt.Errorf("kỳ hoàn thành cấm điều phát trực tiếp")
	}
	switch dispatch.Agent {
	case "writer", "editor":
		if phase != string(domain.PhaseWriting) {
			return fmt.Errorf("%s chỉ điều phát được trong giai đoạn writing (phase=%s hiện tại)", dispatch.Agent, phase)
		}
	}
	return nil
}

// DecideIntervention phân loại can thiệp. Ngữ nghĩa thất bại: trả về error → caller hiển thị lại
// lý do thất bại thật sự, và không sinh ra bất kỳ lệnh ghi nào (thà không làm còn hơn làm sai).
func DecideIntervention(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts InterventionFacts, text string) (InterventionDecision, error) {
	payload, err := marshalPayload(struct {
		Intervention string            `json:"intervention"`
		Facts        InterventionFacts `json:"facts"`
	}{Intervention: text, Facts: facts})
	if err != nil {
		return InterventionDecision{}, err
	}
	return decide(ctx, model, interventionContract, systemPrompt, payload, func(d *InterventionDecision) error {
		return d.ValidateAgainst(facts)
	})
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}