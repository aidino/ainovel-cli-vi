package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Đồng sáng tạo khởi động lạnh: làm rõ yêu cầu từ con số 0, tạo ra chỉ thị sáng tác cho toàn bộ cuốn sách.
const coCreateSystemPrompt = `Bạn là một trợ lý đồng sáng tạo tiểu thuyết. Nhiệm vụ của bạn không phải là trực tiếp bắt đầu viết tiểu thuyết, mà là thông qua nhiều vòng hội thoại ngắn gọn để giúp người dùng làm rõ yêu cầu sáng tác, và liên tục sắp xếp ra một đoạn chỉ thị sáng tác tiếng Trung có thể giao trực tiếp cho engine sáng tác.

Mỗi vòng phản hồi phải xuất ra nghiêm ngặt theo định dạng XML sau, bao gồm bốn thẻ, xuất hiện theo thứ tự, mỗi thẻ đều phải có thẻ mở đóng đúng đắn:

<reply>
Phản hồi tự nhiên tiếng Trung cho người dùng xem: trước tiên phản hồi đầu vào của người dùng, sau đó đưa ra tối đa 1 đến 2 câu hỏi then chốt nhất hiện tại. Nếu thông tin đã đủ để bắt đầu sáng tác, hãy nói cho người dùng biết có thể nhấn Ctrl+S để bắt đầu.
</reply>

<draft>
Bản nháp chỉ thị sáng tác hoàn chỉnh hiện tại, sử dụng Markdown: bắt đầu trực tiếp từ tiêu đề cấp 2, ví dụ "## Chủ đề", "## Yếu tố then chốt", "## Thông tin chờ làm rõ"; dùng dấu đầu dòng liệt kê các điểm chính. Mỗi vòng đều phải **cập nhật lũy kế** trên kết luận đã có, hấp thu ý đồ mới nhất của người dùng; cho dù vòng này không có thêm mới cũng phải viết lại nguyên bản nháp hoàn chỉnh - không được bỏ qua, không viết chỗ ngậm kiểu "(giữ nguyên vòng trước)".
</draft>
` + coCreateProtocolTail

// Đồng sáng tạo theo giai đoạn: tiểu thuyết đã viết được một phần, quy hoạch hướng đi của "giai đoạn tiếp theo". Bên gọi cần thêm tóm tắt trạng thái câu chuyện hiện tại
// vào sau prompt này (đoạn "## Trạng thái câu chuyện hiện tại"), để model quy hoạch trên cơ sở nội dung đã viết.
const stageCoCreateSystemPrompt = `Bạn là một trợ lý "đồng sáng tạo theo giai đoạn" tiểu thuyết. Cuốn tiểu thuyết này đã viết được một phần (tiến độ xem "Trạng thái câu chuyện hiện tại" bên dưới). Người dùng đã tạm dừng, muốn cùng bạn quy hoạch hướng đi của "giai đoạn tiếp theo", rồi mới tiếp tục sáng tác.

Nhiệm vụ của bạn không phải là viết tiếp chính văn, mà là thông qua nhiều vòng hội thoại ngắn gọn giúp người dùng nghĩ rõ đoạn sau này (vài chương tiếp theo / arc tiếp theo / tập tiếp theo) sẽ đi về đâu, và liên tục sắp xếp ra một đoạn "brief hướng đi tiếp theo", để engine sáng tác đẩy tiến dựa vào đó.

Thiết luật: mọi đề xuất phải nhất quán với kịch tình, nhân vật, chi tiết gieo mầm đã xảy ra trong "Trạng thái câu chuyện hiện tại", tuyệt đối không lật đổ hoặc phớt lờ nội dung đã viết; chỉ quy hoạch "tiếp theo đi thế nào", không thiết kế lại toàn bộ cuốn sách.

Mỗi vòng phản hồi phải xuất ra nghiêm ngặt theo định dạng XML sau, bao gồm bốn thẻ, xuất hiện theo thứ tự, mỗi thẻ đều phải có thẻ mở đóng đúng đắn:

<reply>
Phản hồi tự nhiên tiếng Trung cho người dùng xem: trước tiên phản hồi đầu vào của người dùng, sau đó đưa ra tối đa 1 đến 2 câu hỏi then chốt nhất hiện tại. Nếu hướng đi tiếp theo đã đủ rõ ràng, hãy nói cho người dùng biết có thể nhấn Ctrl+S để giao hướng đi cho engine sáng tác, tiếp tục sáng tác.
</reply>

<draft>
"Brief hướng đi tiếp theo" hoàn chỉnh hiện tại, sử dụng Markdown: bắt đầu trực tiếp từ tiêu đề cấp 2, ví dụ "## Hướng đi tiếp theo", "## Bước ngoặt then chốt", "## Chi tiết gieo mầm cần thu", "## Nhịp độ và thiên phúc"; dùng dấu đầu dòng liệt kê các điểm chính. Mỗi vòng đều phải **cập nhật lũy kế** trên kết luận đã có, hấp thu ý đồ mới nhất của người dùng; cho dù vòng này không có thêm mới cũng phải viết lại nguyên brief hoàn chỉnh - không được bỏ qua, không viết chỗ ngậm kiểu "(giữ nguyên vòng trước)".
</draft>
` + coCreateProtocolTail

// coCreateProtocolTail là phần đuôi giao thức đầu ra dùng chung cho hai chế độ đồng sáng tạo (<ready> / <suggestions> + quy phạm đầu ra).
// Hai chế độ chỉ khác nhau ở bối cảnh mở đầu và ngữ nghĩa <draft>, giao thức hoàn toàn giống nhau.
const coCreateProtocolTail = `
<ready>false</ready>

<suggestions>
1-3 câu "điều người dùng có thể muốn nói tiếp theo", mỗi dòng một câu bắt đầu bằng "- ". Đây là dẫn dắt khi người dùng kẹt ý,
nhấn phím số để điền vào ô nhập, người dùng có thể chỉnh sửa thêm rồi gửi.

Yêu cầu:
- Đứng ở giọng điệu người dùng, giống như người dùng nói với bạn, đừng viết thành trợ lý hỏi ngược.
- Mỗi câu không quá 25 chữ, đa dạng hóa kiểu câu, tránh nghìn bài một điệu.
- Đưa ra khuynh hướng / lựa chọn / ý đồ bổ sung, đừng viết thiết lập hoàn chỉnh thay người dùng trong một câu.
</suggestions>

Quy phạm đầu ra:
- Bắt buộc dùng bốn thẻ XML: <reply> / <draft> / <ready> / <suggestions>, mỗi thẻ đều phải mở đóng hoàn chỉnh.
- Tên thẻ chỉ được dùng tiếng Anh viết thường, đừng sửa thành <REPLY> / <REWRITE> / <PHANHOI> hay bất kỳ biến thể nào khác.
- Bên ngoài thẻ đừng thêm bất kỳ giải thích, suy nghĩ hay hàng rào mã nào.
- Trong <draft> cho phép nhiều dòng Markdown, viết xuống dòng trực tiếp, không cần bất kỳ escape nào.
- <ready> chỉ viết true hoặc false. Khi thông tin đã đủ thì điền true.
- Khi <ready>true</ready> thì <suggestions> có thể để trống (giữ nguyên thẻ trống <suggestions></suggestions> là được).`

// CoCreateProgressKind định danh loại nội dung của hàm gọi lại stream.
const (
	CoCreateProgressThinking = "thinking"
	CoCreateProgressReply    = "reply"
)

// Đầu ra thẻ XML bốn đoạn. Phong cách XML mạnh mẽ hơn marker ngoặc vuông - trong dữ liệu huấn luyện Claude/GPT có rất nhiều
// định dạng kiểu <thinking>...</thinking>, model hầu như sẽ không sửa <reply> thành <REWRITE> hoặc các biến thể khác;
// thẻ đóng cũng giúp việc cắt cụt ở giữa stream chính xác hơn (không phụ thuộc vào việc tìm marker tiếp theo để ngắt đuôi).
const (
	tagReply       = "reply"
	tagDraft       = "draft"
	tagReady       = "ready"
	tagSuggestions = "suggestions"
)

func coCreateStream(ctx context.Context, models *bootstrap.ModelSet, sessions *store.SessionStore, sysPrompt string, history []CoCreateMessage, onProgress func(kind, text string)) (reply CoCreateReply, err error) {
	if len(history) == 0 {
		return CoCreateReply{}, fmt.Errorf("cocreate history is empty")
	}

	model := models.ForRole("thinking")

	msgs := []agentcore.Message{agentcore.SystemMsg(sysPrompt)}
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "assistant":
			msgs = append(msgs, assistantMsg(content))
		default:
			msgs = append(msgs, agentcore.UserMsg(content))
		}
	}

	var raw, thinking strings.Builder

	// Để khắc phục lỗi ngẫu nhiên như "cocreate empty response" cần xem thực tế model trả về gì.
	// Mỗi vòng đều ghi ra đĩa toàn bộ vào <output>/meta/sessions/cocreate.jsonl, cùng vị trí với nhật ký session của sáng tác chính thức.
	start := time.Now()
	defer func() {
		if sessions == nil {
			return
		}
		if logErr := sessions.LogCoCreate(coCreateLogEntry{
			Time:         time.Now(),
			DurationMS:   time.Since(start).Milliseconds(),
			InputHistory: history,
			RawResponse:  raw.String(),
			RawLen:       len([]rune(raw.String())),
			Thinking:     thinking.String(),
			ParsedReply:  reply.Message,
			ParsedDraft:  reply.Prompt,
			ParsedReady:  reply.Ready,
			ParsedSugs:   reply.Suggestions,
			Error:        errString(err),
		}); logErr != nil {
			slog.Warn("Ghi nhật ký hội thoại đồng sáng tạo ra đĩa thất bại", "module", "cocreate", "err", logErr)
		}
	}()

	streamCh, err := model.GenerateStream(ctx, msgs, nil, agentcore.WithMaxTokens(2048))
	if err != nil {
		return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", err)
	}

	var streamed bool
	for ev := range streamCh {
		switch ev.Type {
		case agentcore.StreamEventThinkingDelta:
			thinking.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressThinking, thinking.String())
			}
		case agentcore.StreamEventTextDelta:
			streamed = true
			raw.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressReply, extractReplyPreview(raw.String()))
			}
		case agentcore.StreamEventDone:
			if !streamed {
				raw.WriteString(ev.Message.TextContent())
			}
		case agentcore.StreamEventError:
			if ev.Err != nil {
				return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", ev.Err)
			}
			return CoCreateReply{}, fmt.Errorf("cocreate generate failed")
		}
	}

	// Channel fallback: model kiểu suy nghĩ (R1/GLM-Z1/QwQ v.v.) thỉnh thoảng viết câu trả lời hoàn chỉnh vào
	// reasoning_content rồi không chuyển lại kênh final answer, dẫn đến raw bị trống nhưng thinking chứa
	// đủ bốn đoạn. Thực tế xem meta/sessions/cocreate.jsonl - trực tiếp lấy thinking làm raw để giải tích,
	// tầng giao thức đã có xử lý hạ cấp (khi không có đánh dấu [REPLY] thì coi cả đoạn là reply), sau khi cứu vãn thì trải nghiệm UI không khác biệt.
	rawText := raw.String()
	if strings.TrimSpace(rawText) == "" {
		if t := strings.TrimSpace(thinking.String()); t != "" {
			rawText = t
		}
	}
	reply, err = parseCoCreateResponse(rawText)
	return reply, err
}

// coCreateLogEntry là cấu trúc một dòng ghi vào meta/sessions/cocreate.jsonl.
// Tên trường bám sát thói quen tra cứu trực tiếp jsonl (snake_case), tiện cho jq lọc.
type coCreateLogEntry struct {
	Time         time.Time         `json:"time"`
	DurationMS   int64             `json:"duration_ms"`
	InputHistory []CoCreateMessage `json:"input_history"`
	RawResponse  string            `json:"raw_response"`
	RawLen       int               `json:"raw_len"`
	Thinking     string            `json:"thinking,omitempty"`
	ParsedReply  string            `json:"parsed_reply"`
	ParsedDraft  string            `json:"parsed_draft"`
	ParsedReady  bool              `json:"parsed_ready"`
	ParsedSugs   []string          `json:"parsed_sugs,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assistantMsg(text string) agentcore.Message {
	return agentcore.Message{
		Role:      agentcore.RoleAssistant,
		Content:   []agentcore.ContentBlock{agentcore.TextBlock(text)},
		Timestamp: time.Now(),
	}
}

// parseCoCreateResponse giải tích đầu ra thẻ XML. Nếu model không tuân thủ giao thức (nói trực tiếp ngôn ngữ tự nhiên),
// thì hiển thị cả đoạn như reply, draft để trống để session giữ lại vòng trước.
func parseCoCreateResponse(raw string) (CoCreateReply, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CoCreateReply{}, fmt.Errorf("cocreate empty response")
	}

	reply, draft, ready, suggestions := splitCoCreateMarkers(raw)
	if reply == "" {
		// Model không tuân thủ giao thức XML: cả đoạn lấy làm reply.
		return CoCreateReply{Message: raw, Prompt: "", Ready: false, Raw: raw}, nil
	}
	return CoCreateReply{
		Message:     reply,
		Prompt:      draft,
		Ready:       ready,
		Suggestions: suggestions,
		Raw:         raw,
	}, nil
}

// splitCoCreateMarkers cắt văn bản theo bốn thẻ XML.
// Thẻ có thể bị thiếu (ở giữa stream hoặc model bỏ sót), phần thiếu tương ứng với trường là rỗng / false / nil.
// Khi thiếu thẻ đóng, extractTagContent sẽ lấy đến cuối chuỗi, vẫn cố gắng giải tích.
func splitCoCreateMarkers(s string) (reply, draft string, ready bool, suggestions []string) {
	reply = extractTagContent(s, tagReply)
	draft = extractTagContent(s, tagDraft)
	readyStr := strings.ToLower(extractTagContent(s, tagReady))
	ready = readyStr == "true" || readyStr == "yes"
	suggestions = parseSuggestions(extractTagContent(s, tagSuggestions))
	return
}

// extractTagContent trích xuất văn bản giữa <tag>...</tag> từ s.
// Đỡ đạn cho ba tình huống lỗi ngẫu nhiên, tránh hạ cấp trực tiếp mất trường:
//  1. Có mở không đóng (ở giữa stream) → cắt trước thẻ mở đã biết tiếp theo
//  2. Không mở có đóng (model gõ sai, ví dụ <suggestions> viết thành <uggestions>) → bắt đầu từ vị trí
//     kết thúc của thẻ đóng hoàn chỉnh đã biết gần nhất, cho đến trước </tag>
//  3. reply hoàn toàn không có thẻ mở (model mở đầu trực tiếp bằng ngôn ngữ tự nhiên, cuối dán </reply>) → từ đầu đến </reply>
func extractTagContent(s, tag string) string {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	oIdx := strings.Index(s, open)
	if oIdx >= 0 {
		rest := s[oIdx+len(open):]
		if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
			return strings.TrimSpace(rest[:cIdx])
		}
		// Có mở không đóng → cắt trước thẻ mở đã biết tiếp theo
		for _, other := range []string{"<reply>", "<draft>", "<ready>", "<suggestions>"} {
			if other == open {
				continue
			}
			if idx := strings.Index(rest, other); idx >= 0 {
				rest = rest[:idx]
			}
		}
		return strings.TrimSpace(rest)
	}

	// Không mở có đóng → bắt đầu từ vị trí kết thúc của thẻ đóng hoàn chỉnh đã biết gần nhất, đến </tag>.
	if cIdx := strings.Index(s, closeTag); cIdx >= 0 {
		prefix := s[:cIdx]
		start := 0
		for _, t := range []string{"</reply>", "</draft>", "</ready>", "</suggestions>"} {
			if t == closeTag {
				continue
			}
			if i := strings.LastIndex(prefix, t); i >= 0 {
				if end := i + len(t); end > start {
					start = end
				}
			}
		}
		return strings.TrimSpace(prefix[start:])
	}
	return ""
}

// parseSuggestions trích xuất từng dòng của đoạn <suggestions>, bỏ tiền tố danh sách như "- " / "* " / "1. " v.v.
// Giữ lại tối đa 3 câu; bỏ qua dòng trống, quá ngắn (<2 chữ), cả dòng giống thẻ XML (di chứng sót lại do đỡ đạn thẻ mở gõ sai,
// ví dụ <uggestions>).
func parseSuggestions(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// cả dòng giống thẻ XML → bỏ qua (phòng ngừa ô nhiễm do thẻ mở gõ sai)
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		// bóc tiền tố danh sách
		switch {
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(line[2:])
		case isOrderedSuggestion(line):
			line = stripOrderedPrefix(line)
		}
		if len([]rune(line)) < 2 {
			continue
		}
		out = append(out, line)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// isOrderedSuggestion phán đoán đầu dòng có dạng "1. " / "12. " (số + dấu chấm + khoảng trắng) hay không.
func isOrderedSuggestion(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func stripOrderedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return line
	}
	return strings.TrimSpace(line[i+2:])
}

// extractReplyPreview xem trước stream: cung cấp một đoạn văn bản có thể hiển thị cho UI khi raw vẫn đang phát triển.
// Tìm nội dung sau <reply>, cắt đến trước </reply> hoặc thẻ mở tiếp theo <draft>.
// Khi model nửa tuân thủ (thiếu thẻ mở <reply>), từ đầu đến </reply> hoặc <draft> đều được tính là reply.
func extractReplyPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	open := "<" + tagReply + ">"
	closeTag := "</" + tagReply + ">"
	draftOpen := "<" + tagDraft + ">"

	rest := trimmed
	if rIdx := strings.Index(trimmed, open); rIdx >= 0 {
		rest = trimmed[rIdx+len(open):]
	}
	if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	if dIdx := strings.Index(rest, draftOpen); dIdx >= 0 {
		rest = rest[:dIdx]
	}
	return strings.TrimSpace(rest)
}
