package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// FailureFacts là gói sự kiện dùng chung cho hai kịch bản worker_failure / deadlock:
// Engine đã thực hiện phân loại tất định (retry / lỗi tham số v.v. không đến đây), những gì gửi đến Arbiter là
// phần sót lại "code tất định không đưa ra được lối thoát".
type FailureFacts struct {
	Kind          string   `json:"kind"` // worker_failure | deadlock
	Agent         string   `json:"agent,omitempty"`
	Task          string   `json:"task,omitempty"`
	Error         string   `json:"error,omitempty"` // worker_failure: văn bản lỗi
	ErrorKind     string   `json:"error_kind,omitempty"`
	Repeats       int      `json:"repeats,omitempty"` // deadlock: số lần lệnh tương tự đã được phái
	Phase         string   `json:"phase,omitempty"`
	NextChapter   int      `json:"next_chapter,omitempty"`
	PendingQueue  []int    `json:"pending_rewrites,omitempty"`
	FoundationGap []string `json:"foundation_missing,omitempty"`
	FactWarnings  []string `json:"fact_warnings,omitempty"`
}

// FailureDecision phán quyết thất bại/thế bí.
type FailureDecision struct {
	Action   string      `json:"action"` // retry | reroute | abort
	Dispatch *DispatchOp `json:"dispatch,omitempty"`
	Reason   string      `json:"reason"`
}

func (d *FailureDecision) ValidateAgainst(f FailureFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được rỗng")
	}
	switch d.Action {
	case "retry", "abort":
		return nil
	case "reroute":
		if d.Dispatch == nil {
			return fmt.Errorf("reroute bắt buộc kèm dispatch")
		}
		if err := d.Dispatch.validate(); err != nil {
			return err
		}
		return validateDispatchAgainst(d.Dispatch, f.Phase)
	default:
		return fmt.Errorf("action không hợp lệ: %q (chọn retry / reroute / abort)", d.Action)
	}
}

// failureContract nằm kề FailureDecision: action là enum đóng, dispatch là object có thể null
// (chỉ không phải null khi reroute); tổ hợp chéo các field vẫn do ValidateAgainst kiểm tra dựa theo sự kiện.
var failureContract = llmcontract.Contract{
	Name:        "arbiter_failure",
	Description: "Phán quyết thất bại / thế bí: đưa ra lối thoát",
	Schema: schema.Object(
		schema.Property("action", schema.Enum("lối thoát", "retry", "reroute", "abort")).Required(),
		schema.Property("dispatch", dispatchSchema("mục tiêu điều phát (chỉ đưa khi reroute, nếu không là null)")).Required(),
		schema.Property("reason", schema.String("lý do phán quyết")).Required(),
	),
}

// DecideFailure tham vấn thất bại/thế bí. Ngữ nghĩa thất bại: trả về error → Engine xử lý theo đường thận trọng nhất
// (tạm dừng + notify), tuyệt đối không tham vấn vô hạn.
func DecideFailure(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts FailureFacts) (FailureDecision, error) {
	payload, err := marshalPayload(facts)
	if err != nil {
		return FailureDecision{}, err
	}
	return decide(ctx, model, failureContract, systemPrompt, payload, func(d *FailureDecision) error {
		return d.ValidateAgainst(facts)
	})
}