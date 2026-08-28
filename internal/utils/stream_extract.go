package utils

import "strings"

// JSONFieldExtractor trích xuất giá trị chuỗi của một trường chỉ định từ các mảnh JSON streaming.
//
// Khi LLM sinh tool call theo chế độ streaming, tham số đến theo từng mảnh (OpenAI/Anthropic)
// hoặc đến một lần (Gemini). Bộ trích xuất này dùng máy trạng thái quét từng ký tự,
// sau khi phát hiện key mục tiêu thì trích giá trị chuỗi của nó, xử lý escaping JSON.
type JSONFieldExtractor struct {
	key      string // mục tiêu khớp, ví dụ "content" hoặc "task"
	state    extractState
	matchPos int
	escape   bool
	buf      strings.Builder
}

type extractState int

const (
	stateScan    extractState = iota // quét, tìm key mục tiêu
	stateColon                       // đã khớp key, chờ dấu hai chấm và dấu nháy mở
	stateExtract                     // đang trích giá trị chuỗi
)

func NewFieldExtractor(fieldName string) *JSONFieldExtractor {
	return &JSONFieldExtractor{key: `"` + fieldName + `"`}
}

// Feed xử lý một đoạn delta, trả về văn bản trích được (có thể rỗng).
func (e *JSONFieldExtractor) Feed(delta string) string {
	e.buf.Reset()
	for _, r := range delta {
		switch e.state {
		case stateScan:
			e.feedScan(r)
		case stateColon:
			e.feedColon(r)
		case stateExtract:
			e.feedExtract(r)
		}
	}
	return e.buf.String()
}

func (e *JSONFieldExtractor) feedScan(r rune) {
	if e.matchPos < len(e.key) && byte(r) == e.key[e.matchPos] {
		e.matchPos++
		if e.matchPos == len(e.key) {
			e.state = stateColon
			e.matchPos = 0
		}
		return
	}
	e.matchPos = 0
	if byte(r) == e.key[0] {
		e.matchPos = 1
	}
}

func (e *JSONFieldExtractor) feedColon(r rune) {
	switch r {
	case ':', ' ', '\t':
		// bỏ qua
	case '"':
		e.state = stateExtract
		e.escape = false
	default:
		e.state = stateScan
		e.matchPos = 0
		if byte(r) == e.key[0] {
			e.matchPos = 1
		}
	}
}

func (e *JSONFieldExtractor) feedExtract(r rune) {
	if e.escape {
		e.escape = false
		switch r {
		case 'n':
			e.buf.WriteByte('\n')
		case 't':
			e.buf.WriteByte('\t')
		case 'r':
			e.buf.WriteByte('\r')
		case '"', '\\', '/':
			e.buf.WriteRune(r)
		default:
			e.buf.WriteByte('\\')
			e.buf.WriteRune(r)
		}
		return
	}
	switch r {
	case '\\':
		e.escape = true
	case '"':
		e.state = stateScan
		e.matchPos = 0
	default:
		e.buf.WriteRune(r)
	}
}

// Reset đặt lại trạng thái (gọi khi sang lượt tin nhắn LLM mới).
func (e *JSONFieldExtractor) Reset() {
	e.state = stateScan
	e.matchPos = 0
	e.escape = false
}

// ThinkingSep là dấu phân cách giữa văn bản suy nghĩ và phần thân.
// StreamFilter chèn dấu này trước đoạn văn bản suy nghĩ, TUI dựa vào đó để chuyển kiểu hiển thị.
const ThinkingSep = "\x02"

// StreamFilter phân biệt trả lời văn bản và lời gọi tool JSON của SubAgent.
// Trả lời văn bản đánh dấu là nội dung suy nghĩ (tiền tố ThinkingSep); lời gọi tool JSON chỉ trích trường chỉ định.
//
// Căn cứ phán đoán: gặp { thì vào chế độ JSON (theo dõi độ sâu ngoặc nhọn),
// độ sâu về không thì trở lại chế độ văn bản.
type StreamFilter struct {
	fieldExt   *JSONFieldExtractor
	mode       filterMode
	braceDepth int
	inString   bool // đang trong chuỗi JSON (không đếm ngoặc nhọn)
	escJSON    bool // escaping trong chuỗi JSON
	thinking   bool // hiện đang ở đoạn văn bản suy nghĩ
	buf        strings.Builder
}

type filterMode int

const (
	filterText filterMode = iota // trả lời văn bản, truyền thẳng
	filterJSON                   // lời gọi tool JSON, trích trường mục tiêu
)

func NewStreamFilter(fieldName string) *StreamFilter {
	return &StreamFilter{fieldExt: NewFieldExtractor(fieldName)}
}

// Feed xử lý một đoạn delta, trả về văn bản có thể hiển thị.
// Trả lời văn bản xuất trực tiếp; giá trị trường mục tiêu trong JSON được trích xuất; phần còn lại của cấu trúc JSON bị bỏ.
func (f *StreamFilter) Feed(delta string) string {
	f.buf.Reset()
	for _, r := range delta {
		switch f.mode {
		case filterText:
			if r == '{' {
				f.thinking = false
				f.mode = filterJSON
				f.braceDepth = 1
				f.inString = false
				f.escJSON = false
				f.fieldExt.Reset()
				f.feedExtractor(r)
			} else {
				if !f.thinking {
					f.thinking = true
					f.buf.WriteString(ThinkingSep)
				}
				f.buf.WriteRune(r)
			}
		case filterJSON:
			f.feedExtractor(r)
			f.trackBraces(r)
		}
	}
	return f.buf.String()
}

// feedExtractor đưa từng ký tự cho fieldExt, kết quả trích ghi vào buf.
func (f *StreamFilter) feedExtractor(r rune) {
	if text := f.fieldExt.Feed(string(r)); text != "" {
		f.buf.WriteString(text)
	}
}

// trackBraces theo dõi độ sâu ngoặc nhọn JSON, độ sâu về không thì chuyển về chế độ văn bản.
func (f *StreamFilter) trackBraces(r rune) {
	if f.escJSON {
		f.escJSON = false
		return
	}
	if f.inString {
		switch r {
		case '\\':
			f.escJSON = true
		case '"':
			f.inString = false
		}
		return
	}
	switch r {
	case '"':
		f.inString = true
	case '{':
		f.braceDepth++
	case '}':
		f.braceDepth--
		if f.braceDepth <= 0 {
			f.mode = filterText
		}
	}
}

// Reset đặt lại trạng thái.
func (f *StreamFilter) Reset() {
	f.mode = filterText
	f.braceDepth = 0
	f.inString = false
	f.escJSON = false
	f.thinking = false
	f.fieldExt.Reset()
}