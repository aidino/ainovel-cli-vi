package host

import (
	"context"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// usageTrackedModel gắn theo dõi dùng lượng vào các lệnh gọi model: token/chi phí phải được ghi vào hệ thống ngân sách và usage,
// nếu không trần ngân sách sẽ mù với chi tiêu, hiển thị lượng dùng UI sẽ không chuẩn. Ghi lại danh tính bằng agentName truyền vào——import thuộc về architect,
// phán quyết thuộc về arbiter (UsageTracker tính phí mặc định cho nhân vật không xác định).
type usageTrackedModel struct {
	inner     agentcore.ChatModel
	agentName string
	record    func(agentName, task string, msg agentcore.AgentMessage)
}

func newUsageTrackedModel(inner agentcore.ChatModel, agentName string, record func(string, string, agentcore.AgentMessage)) agentcore.ChatModel {
	if record == nil {
		return inner
	}
	tracked := &usageTrackedModel{inner: inner, agentName: agentName, record: record}
	if capabilities, ok := inner.(llm.CapabilityProvider); ok {
		return &capabilityUsageTrackedModel{usageTrackedModel: tracked, capabilities: capabilities}
	}
	return tracked
}

// capabilityUsageTrackedModel giữ lại interface năng lực tùy chọn của model nền. Wrapper không được
// xóa "không hỗ trợ thinking" thành "năng lực không rõ", nếu không cấp trên sẽ tạo ra tham số mà provider không chấp nhận.
type capabilityUsageTrackedModel struct {
	*usageTrackedModel
	capabilities llm.CapabilityProvider
}

func (m *capabilityUsageTrackedModel) Capabilities() llm.Capabilities {
	return m.capabilities.Capabilities()
}

// JSONSchemaOverride truyền xuyên qua khai báo 3 trạng thái config json_schema của model nền; khi inner không mang theo
// sẽ trả về nil ("chưa cấu hình"), không làm giả năng lực.
func (m *capabilityUsageTrackedModel) JSONSchemaOverride() *bool {
	if o, ok := m.usageTrackedModel.inner.(interface{ JSONSchemaOverride() *bool }); ok {
		return o.JSONSchemaOverride()
	}
	return nil
}

func (m *capabilityUsageTrackedModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if provider, ok := m.usageTrackedModel.inner.(interface {
		StructuredOutputFacts() llmcontract.ModelFacts
	}); ok {
		return provider.StructuredOutputFacts()
	}
	return llmcontract.ModelFacts{
		Capabilities:       m.Capabilities(),
		JSONSchemaOverride: m.JSONSchemaOverride(),
	}
}

func (m *usageTrackedModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	resp, err := m.inner.Generate(ctx, msgs, tools, opts...)
	if err == nil && resp != nil {
		m.record(m.agentName, "", resp.Message)
	}
	return resp, err
}

func (m *usageTrackedModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	// Arbiter chỉ đi Generate; truyền thẳng luồng stream (nếu tương lai dùng stream, usage do bên tiêu thụ ghi bù).
	return m.inner.GenerateStream(ctx, msgs, tools, opts...)
}

func (m *usageTrackedModel) SupportsTools() bool { return m.inner.SupportsTools() }
