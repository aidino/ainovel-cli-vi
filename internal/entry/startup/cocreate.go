package startup

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/host"
)

// CoCreateSession chứa trạng thái phi UI của chế độ đồng sáng tạo.
type CoCreateSession struct {
	history        []host.CoCreateMessage
	draftPrompt    string
	ready          bool
	streamReply    string
	streamThinking string
	suggestions    []string
}

func NewCoCreateSession(initial string) *CoCreateSession {
	return &CoCreateSession{
		history: []host.CoCreateMessage{
			{Role: "user", Content: strings.TrimSpace(initial)},
		},
	}
}

func (s *CoCreateSession) History() []host.CoCreateMessage {
	if s == nil {
		return nil
	}
	return append([]host.CoCreateMessage(nil), s.history...)
}

func (s *CoCreateSession) ApplyReply(reply host.CoCreateReply) {
	if s == nil {
		return
	}
	s.streamReply = ""
	s.streamThinking = ""
	// assistant trong history lưu 3 đoạn Raw đầy đủ (gồm [DRAFT]), mô hình vòng sau mới thấy được
	// bản thảo mình viết ở vòng trước và cập nhật tích lũy trên cơ sở đó; chỉ lưu Message sẽ khiến [DRAFT] hoàn toàn
	// không vào context, mô hình mỗi vòng chỉ có thể quy nạp lại dựa trên hội thoại, chi tiết giai đoạn đầu dễ mất. Ở luồng hạ cấp
	// Raw == Message, là tương đương.
	text := strings.TrimSpace(reply.Raw)
	if text == "" {
		text = strings.TrimSpace(reply.Message)
	}
	if text != "" {
		s.history = append(s.history, host.CoCreateMessage{Role: "assistant", Content: text})
	}
	// Chỉ ghi đè draft khi Prompt khác rỗng: luồng hạ cấp parse sẽ trả về Prompt="", lúc này
	// phải giữ draft của vòng trước, nếu không "chỉ lệnh sáng tác hiện tại" người dùng đã tích lũy sẽ bị xóa sạch bởi phản hồi bị cắt đứt.
	if prompt := strings.TrimSpace(reply.Prompt); prompt != "" {
		s.draftPrompt = prompt
	}
	s.ready = reply.Ready
	// suggestions ghi đè trực tiếp (bao gồm ghi đè thành rỗng): hướng dẫn của mỗi vòng chỉ có ý nghĩa với hiện tại.
	s.suggestions = append(s.suggestions[:0], reply.Suggestions...)
}

func (s *CoCreateSession) AppendUser(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// Người dùng đã quyết định câu tiếp theo nói gì, suggestions lập tức bị hủy, tránh việc AI chưa phản hồi mà
	// gợi ý cũ vẫn treo trên hộp nhập gây hiểu lầm.
	s.suggestions = nil
	s.history = append(s.history, host.CoCreateMessage{Role: "user", Content: text})
}

// ApplyDelta nhận tích lũy luồng; kind="thinking" ghi vào luồng suy luận, "reply" ghi vào xem trước phản hồi.
// Hai luồng tích lũy riêng biệt, UI có thể tô màu hiển thị theo khối, để người dùng thấy LLM đang làm việc trong giai đoạn thinking.
func (s *CoCreateSession) ApplyDelta(kind, text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	switch kind {
	case host.CoCreateProgressThinking:
		s.streamThinking = text
	case host.CoCreateProgressReply:
		s.streamReply = text
	}
}

func (s *CoCreateSession) StreamReply() string {
	if s == nil {
		return ""
	}
	return s.streamReply
}

func (s *CoCreateSession) StreamThinking() string {
	if s == nil {
		return ""
	}
	return s.streamThinking
}

func (s *CoCreateSession) DraftPrompt() string {
	if s == nil {
		return ""
	}
	return s.draftPrompt
}

func (s *CoCreateSession) Suggestions() []string {
	if s == nil {
		return nil
	}
	return s.suggestions
}

func (s *CoCreateSession) Ready() bool {
	if s == nil {
		return false
	}
	return s.ready
}

func (s *CoCreateSession) CanStart() bool {
	return strings.TrimSpace(s.DraftPrompt()) != ""
}

func (s *CoCreateSession) InitialInput() string {
	if s == nil || len(s.history) == 0 {
		return ""
	}
	return strings.TrimSpace(s.history[0].Content)
}

func (s *CoCreateSession) BuildPrompt() (string, error) {
	if s == nil || !s.CanStart() {
		return "", fmt.Errorf("cocreate draft prompt is required")
	}
	return s.DraftPrompt(), nil
}
