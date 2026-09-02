package agents

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

type stubSummaryModel struct{ calls int }

func (m *stubSummaryModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("<summary>checkpoint</summary>")},
	}}, nil
}

func (m *stubSummaryModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *stubSummaryModel) SupportsTools() bool { return true }

func toolCallResult(id, name, args, result string) []agentcore.AgentMessage {
	return []agentcore.AgentMessage{
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)})},
		},
		agentcore.ToolResultMsg(id, json.RawMessage(strconv.Quote(result)), false),
	}
}

// Worker chỉ có một tin nhắn nhiệm vụ mỗi lần chạy, phần còn lại là các nhóm tool.
// Nguyên văn chương của Editor không đi qua vi nén, chỉ có thể dựa vào tóm tắt toàn
// phần; nó phải thực sự cắt được trong hình thái này, và nguyên văn mới nhất phải
// giữ nguyên ở phần đuôi.
func TestRoleContextManagerSummarizesToolLoop(t *testing.T) {
	msgs := []agentcore.AgentMessage{agentcore.UserMsg("Tạo tóm tắt tập 1")}
	msgs = append(msgs, toolCallResult("ctx", "novel_context", `{}`, strings.Repeat("x", 8000))...)
	for i := 1; i <= 8; i++ {
		chapter := strconv.Itoa(i)
		msgs = append(msgs, toolCallResult("ch"+chapter, "read_chapter", `{"chapter":`+chapter+`}`, strings.Repeat("章", 4000))...)
	}

	model := &stubSummaryModel{}
	projection, err := newRoleContextManager(editorContextProfile, model, 32000, "novel_context").Project(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls == 0 || !projection.ShouldCommit {
		t.Fatalf("kỳ vọng LLM summary chạy và commit, calls=%d commit=%v", model.calls, projection.ShouldCommit)
	}
	if summary, ok := projection.Messages[0].(corecontext.ContextSummary); !ok || !strings.Contains(summary.Summary, "checkpoint") {
		t.Fatalf("phép chiếu phải bắt đầu bằng summary, nhận được %T", projection.Messages[0])
	}
	last := projection.Messages[len(projection.Messages)-1].(agentcore.Message)
	if !strings.Contains(last.TextContent(), "章") {
		t.Fatal("bằng chứng read_chapter mới nhất phải giữ nguyên trong phần đuôi")
	}
}
