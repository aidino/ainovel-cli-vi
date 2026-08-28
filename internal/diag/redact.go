package diag

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SkelEvent là bộ xương hành vi của một tin nhắn hội thoại sau khi làm nhạy: giữ lại tín hiệu cấu trúc (vai trò / công cụ / lỗi /
// dấu vân tay lặp lại), tất cả văn bản tự do (chính văn, prompt, suy nghĩ) đều bị che. Đây là một lớp
// chiếu nghiêm ngặt hơn store.compactMessage - cái sau nén theo dung lượng (>4KB), ở đây không xét dung lượng,
// bất kỳ văn bản nào cũng không lọt ra ngoài.
type SkelEvent struct {
	Agent    string     // Nguồn hội thoại: writer-ch07 / architect-arc02 …
	Role     string     // assistant / tool / user
	Tools    []SkelTool // Các lời gọi công cụ trong tin nhắn này
	ErrClass string     // role=tool và is_error: dòng đầu lỗi (chuỗi lỗi framework, không chứa chính văn)
	TextSha  string     // Hash ngắn của chính văn bị che; cùng sha = tạo lại cùng một đoạn (tín hiệu vòng lặp)
	Redacted int        // Số khối văn bản/suy nghĩ bị che trong mục này (dùng để tự kiểm làm nhạy)
}

// SkelTool là hình chiếu làm nhạy của một lời gọi công cụ.
type SkelTool struct {
	Name     string            // Tên công cụ (tín hiệu cấu trúc, không chứa chính văn)
	Args     map[string]string // key → giá trị gốc vô hướng / chuỗi ngắn có ngoặc kép / "<redacted len sha>"
	Invalid  bool              // ArgsInvalid: tham số từ model không thể phân tích (#34 tín hiệu)
	ParseErr string            // ArgsParseError: lý do phân tích thất bại
}

// redactMessage chiếu một agentcore.Message thành bộ xương hành vi.
func redactMessage(agent string, m agentcore.Message) SkelEvent {
	ev := SkelEvent{Agent: agent, Role: string(m.Role)}
	isErr, _ := m.Metadata["is_error"].(bool)

	var text strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case agentcore.ContentText:
			// kết quả lỗi tool giữ lại dòng đầu: đây là chuỗi lỗi của chính chúng ta (như InputValidationError),
			// không chứa chính văn, và là chìa khóa để định vị vòng lặp. Các văn bản còn lại đều đưa vào bể che.
			if m.Role == agentcore.RoleTool && isErr && ev.ErrClass == "" {
				ev.ErrClass = firstLine(b.Text, 160)
				continue
			}
			if strings.TrimSpace(b.Text) != "" {
				text.WriteString(b.Text)
				ev.Redacted++
			}
		case agentcore.ContentThinking:
			if strings.TrimSpace(b.Thinking) != "" {
				text.WriteString(b.Thinking)
				ev.Redacted++
			}
		case agentcore.ContentToolCall:
			if b.ToolCall != nil {
				ev.Tools = append(ev.Tools, redactToolCall(b.ToolCall))
			}
		}
	}
	if t := text.String(); t != "" {
		ev.TextSha = shortHash(t)
	}
	return ev
}

// redactToolCall chiếu một lời gọi công cụ: tên công cụ + tham số (giá trị làm nhạy) + dấu hiệu ngoại lệ phân tích.
func redactToolCall(tc *agentcore.ToolCall) SkelTool {
	return SkelTool{
		Name:     tc.Name,
		Args:     redactArgs(tc.Args),
		Invalid:  tc.ArgsInvalid,
		ParseErr: tc.ArgsParseError,
	}
}

// redactArgs chiếu đối tượng tham số công cụ thành key → giá trị làm nhạy. Tham số không phải đối tượng trả về nil
// (ArgsInvalid/ParseErr đã được ghi riêng trong SkelTool).
func redactArgs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = projectValue(v)
	}
	return out
}

// projectValue chiếu một giá trị tham số theo loại JSON:
//   - Vô hướng (số / bool / null): giá trị gốc chính là tín hiệu cấu trúc, giữ lại (chapter: 7)
//   - Chuỗi kiểu định danh ngắn: giữ lại kèm ngoặc kép, bộc lộ loại (chapter: "7" ← #34 tín hiệu số dạng chuỗi)
//   - Chuỗi, đối tượng, mảng chứa tiếng Trung / khoảng trắng / văn bản dài: che thành <redacted …> (chính văn không lọt ra ngoài)
//   - Đã là chỗ dành sẵn [session_compact: …]: an toàn và có thông tin, giữ nguyên
func projectValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return redactPlaceholder(s)
		}
		if strings.HasPrefix(str, store.CompactTag) {
			return str
		}
		// Chỉ giữ lại giá trị ngắn "giống định danh/số/enum" (chapter:"7"、type:"premise"、agent:"writer");
		// Bất kỳ chuỗi nào chứa tiếng Trung, khoảng trắng hoặc ký hiệu khác đều coi là chính văn, che hết.
		if utf8.RuneCountInString(str) <= 32 && isStructuralToken(str) {
			return strconv.Quote(str)
		}
		return redactPlaceholder(str)
	case '{':
		return fmt.Sprintf("<redacted object len=%d>", len(raw))
	case '[':
		return fmt.Sprintf("<redacted array len=%d>", len(raw))
	default:
		return s
	}
}

// isStructuralToken phán đoán chuỗi có "giống định danh" không - thuần ASCII gồm chữ cái / số / `_-.:/`,
// không khoảng trắng, không tiếng Trung. Dùng để phân biệt tín hiệu cấu trúc (giữ lại) và đoạn chính văn (che).
func isStructuralToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}

func redactPlaceholder(s string) string {
	return fmt.Sprintf("<redacted len=%d sha=%s>", utf8.RuneCountInString(s), shortHash(s))
}

// shortHash lấy hash ngắn của văn bản; chỉ dùng để phán đoán "cùng một đoạn văn bản có lặp lại không", không dùng cho mục đích mã hóa.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// firstLine lấy dòng đầu và cắt đứt theo rune, dùng cho tóm tắt chuỗi lỗi.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max]) + "…"
	}
	return s
}
