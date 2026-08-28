package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// PlanStartDecision quyết định khởi động: chọn quy hoạch sư và tạo ra văn bản nhiệm vụ (đã được mở rộng nếu cần).
type PlanStartDecision struct {
	Planner string `json:"planner"` // architect_long | architect_short
	Task    string `json:"task"`    // nhiệm vụ hoàn chỉnh giao cho quy hoạch sư (bao gồm yêu cầu đã được mở rộng)
	Reason  string `json:"reason"`
}

func (d *PlanStartDecision) Validate() error {
	if d.Planner != "architect_long" && d.Planner != "architect_short" {
		return fmt.Errorf("planner không hợp lệ: %q (chọn architect_long / architect_short)", d.Planner)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("task không được rỗng")
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được rỗng")
	}
	return nil
}

// planStartContract nằm kề PlanStartDecision: các trường đều required, planner là enum đóng.
var planStartContract = llmcontract.Contract{
	Name:        "arbiter_plan_start",
	Description: "Phán quyết khởi động: chọn quy hoạch sư và sinh văn bản nhiệm vụ đầy đủ",
	Schema: schema.Object(
		schema.Property("planner", schema.Enum("quy hoạch sư", "architect_long", "architect_short")).Required(),
		schema.Property("task", schema.String("nhiệm vụ đầy đủ giao cho quy hoạch sư (gồm yêu cầu đã mở rộng)")).Required(),
		schema.Property("reason", schema.String("lý do lựa chọn")).Required(),
	),
}

// planStartPayload là tải trọng người dùng của plan_start (sự kiện là đầu vào, không có trạng thái store——sách mới).
type planStartPayload struct {
	Requirement string `json:"requirement"`
	Style       string `json:"style,omitempty"`
}

// DecidePlanStart phán quyết khởi động: dựa vào nhu cầu của người dùng để chọn quy hoạch sư; khi nhu cầu quá ngắn (< 20 chữ),
// sẽ tự động bổ sung phương hướng dị biệt hóa, độc giả mục tiêu, điểm tiêu thụ cốt lõi, và ít nhất một cái móc phi truyền thống vào task.
// Ngữ nghĩa thất bại: trả về error → caller báo lỗi rõ ràng và dừng khởi động (giai đoạn khởi động có người dùng tại chỗ, báo lỗi tốt hơn là đoán).
func DecidePlanStart(ctx context.Context, model agentcore.ChatModel, systemPrompt, requirement, style string) (PlanStartDecision, error) {
	payload, err := marshalPayload(planStartPayload{Requirement: requirement, Style: style})
	if err != nil {
		return PlanStartDecision{}, err
	}
	return decide(ctx, model, planStartContract, systemPrompt, payload, (*PlanStartDecision).Validate)
}