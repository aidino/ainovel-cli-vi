package host

import (
	"strings"
	"unicode/utf8"
)

// toolDisplays ghi lại những công cụ nào có trường nào cần xuất trực tiếp ra terminal khi xuất stream.
// Nếu công cụ không nằm trong map, hoặc có trong map nhưng giá trị là nil/mảng rỗng, thì tham số của nó không xuất (im lặng).
// Mảng giá trị biểu thị đường dẫn khóa JSON cần trích xuất. Hỗ trợ ba định dạng:
// 1. "key": Trường của đối tượng cấp cao nhất
// 2. "array[].key": Trường của mỗi đối tượng trong mảng array nằm dưới đối tượng cấp cao nhất
// 3. "obj.key": Trường của đối tượng obj nằm dưới đối tượng cấp cao nhất
//
// Chế độ thông thường (nakedKey rỗng): tokenizer render args JSON đầu ra từ LLM thành văn bản
// "key: value" thụt lề, các đối tượng/mảng lồng nhau được thụt lề theo cấp độ, string/number/bool được stream ra.
// Khớp hoàn toàn với schema — LLM xuất ra thêm một trường thì trên bảng hiện thêm một dòng, không cần thay đổi bất kỳ mã nguồn nào.
//
// Chế độ stream thuần (nakedKey không rỗng): chỉ stream ra giá trị string của trường cấp cao nhất mục tiêu, các trường khác
// đều bị bỏ qua. Dùng cho draft_chapter, để toàn bộ chương markdown không bị trang trí thành "content: # …".
// Header luôn bắt đầu bằng "✻ ": Đây là tiền tố quy ước để TUI renderStreamContent đi vào đường dẫn renderAgentBlock
// làm nổi bật (✻ vàng + label nền xanh lam gạch chân + gạch ngang mờ), nhất quán với
// header fallback (streamHeaderFallback); nếu đổi thành văn bản thông thường, nó sẽ rơi vào đường dẫn nội dung chính, bị
// gạch bỏ bằng màu mặc định của terminal, và tiêu đề sẽ không còn nổi bật.
var toolDisplays = map[string]toolDisplay{
	"draft_chapter": {nakedKey: "content"},

	"plan_chapter":        {header: "✻ Quy hoạch"},
	"edit_chapter":        {header: "✻ Đánh bóng"},
	"commit_chapter":      {header: "✻ Gửi chương"},
	"save_review":         {header: "✻ Đọc kiểm"},
	"save_arc_summary":    {header: "✻ Tóm tắt arc"},
	"save_volume_summary": {header: "✻ Tóm tắt tập"},
	"save_foundation":     {header: "✻ Thiết lập"},
	"revise_outline":      {header: "✻ Sửa đổi đại cương"},
	"read_chapter":        {header: "✻ Đọc chương"},
	"check_consistency":   {header: "✻ Kiểm tra tính nhất quán"},
	"novel_context":       {header: "✻ Truy vấn bối cảnh"},
}

type toolDisplay struct {
	header   string
	nakedKey string
}

// jsonFieldExtractor dùng để trích xuất các giá trị cấp độ mục tiêu theo thứ tự trong một luồng JSON bị cắt xén cực độ,
// không cần đợi đóng, có thể vừa stream vừa giải mã đoạn văn bản escape lớn. Nó hỗ trợ trích xuất giá trị đường dẫn hai cấp
// (đối tượng vòng ngoài → mảng đối tượng → trường trong đối tượng), như draft_chapter.content.
//
// Giới hạn & giả định:
// - Tên trường phải là literal chuỗi (có ngoặc kép), giá trị có thể là chuỗi.
// - Không hỗ trợ JSONPath bất kỳ, chỉ hỗ trợ mô hình cố định "khớp đối tượng cấp cao nhất -> khớp mảng đối tượng (tùy chọn) -> khớp giá trị trường".
// - Chưa xử lý vấn đề trùng tên trường khi đối tượng lồng nhau sâu (ví dụ trường mục tiêu cùng tên xuất hiện ở mảng/lớp ngoài không khớp).
// - Luồng đầu vào phải liên tục Feed() dưới dạng chunk (chuỗi).
// - Không thực thi giải mã escape \uXXXX, giữ nguyên đầu ra.
// - Giả định mạnh rằng định dạng JSON do model mục tiêu tạo ra về cơ bản là chuẩn (không có khoảng trắng kỳ dị).
type jsonFieldExtractor struct {
	cfg toolDisplay

	state pState
	stack []byte // Ngăn xếp container: 'O' obj / 'A' arr

	keyBuf strings.Builder

	escape bool
	uHex   []byte

	started bool // Liệu đã emit bất kỳ ký tự nào chưa (dùng cho việc xuống dòng giữa header và key đầu tiên)

	done bool
}

type pState int

const (
	psRoot         pState = iota
	psBeforeKey           // Trong obj: chờ key tiếp theo hoặc }
	psInKey               // Trong obj: đang parse key
	psAfterKey            // Trong obj: chờ :
	psBeforeValue         // Chờ ký tự bắt đầu của value
	psStringStream        // Giá trị string, stream emit các ký tự đã xử lý
	psStringSkip          // Giá trị string, bỏ qua (trong chế độ naked stream nếu không phải trường mục tiêu)
	psNumberStream        // Số, stream emit
	psNumberSkip          // Số, bỏ qua
	psPrimStream          // true/false/null, stream emit
	psPrimSkip            // true/false/null, bỏ qua
	psDone                // Container cấp cao nhất đã đóng
)

func newToolExtractor(tool string) *jsonFieldExtractor {
	cfg, ok := toolDisplays[tool]
	if !ok {
		return nil
	}
	return &jsonFieldExtractor{cfg: cfg}
}

func (e *jsonFieldExtractor) Done() bool { return e.done }

func (e *jsonFieldExtractor) Feed(chunk string) string {
	if e.done || chunk == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(chunk); i++ {
		e.step(chunk[i], &out)
		if e.done {
			break
		}
	}
	return out.String()
}

// ── Ngăn xếp container / Thụt lề ──

func (e *jsonFieldExtractor) push(kind byte) {
	e.stack = append(e.stack, kind)
}

func (e *jsonFieldExtractor) pop() {
	if len(e.stack) == 0 {
		return
	}
	e.stack = e.stack[:len(e.stack)-1]
}

func (e *jsonFieldExtractor) parent() byte {
	if len(e.stack) == 0 {
		return 0
	}
	return e.stack[len(e.stack)-1]
}

// writeIndent viết thụt lề hiện tại. Độ sâu = số lớp lồng = len(stack)-1 (bên trong container root không thụt lề).
func (e *jsonFieldExtractor) writeIndent(out *strings.Builder) {
	depth := len(e.stack) - 1
	for range depth {
		out.WriteString("  ")
	}
}

// ── Máy trạng thái ──

func (e *jsonFieldExtractor) step(c byte, out *strings.Builder) {
	switch e.state {
	case psRoot:
		switch c {
		case '{':
			e.push('O')
			e.state = psBeforeKey
		case '[':
			// Thực tế không xảy ra (args công cụ luôn là obj); chấp nhận: coi là arr gốc
			e.push('A')
			e.state = psBeforeValue
		}
	case psBeforeKey:
		switch c {
		case '"':
			e.keyBuf.Reset()
			e.escape = false
			e.state = psInKey
		case '}':
			e.closeContainer(out)
		case ' ', '\t', '\n', '\r', ',':
		}
	case psInKey:
		if e.escape {
			e.keyBuf.WriteByte(c)
			e.escape = false
			return
		}
		if c == '\\' {
			e.escape = true
			return
		}
		if c == '"' {
			e.emitKeyLine(out, e.keyBuf.String())
			e.state = psAfterKey
			return
		}
		e.keyBuf.WriteByte(c)
	case psAfterKey:
		if c == ':' {
			e.state = psBeforeValue
		}
	case psBeforeValue:
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			return
		}
		switch c {
		case '"':
			e.beginString(out)
		case '{':
			e.beginNested('O', out)
		case '[':
			e.beginNested('A', out)
		case ']', '}':
			e.closeContainer(out)
		case 't', 'f', 'n':
			e.beginPrim(c, out)
		default:
			if c == '-' || (c >= '0' && c <= '9') {
				e.beginNumber(c, out)
			}
		}
	case psStringStream:
		e.handleStringByte(c, out, false)
	case psStringSkip:
		e.handleStringByte(c, out, true)
	case psNumberStream:
		if isNumberByte(c) {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psNumberSkip:
		if isNumberByte(c) {
			return
		}
		e.afterValueChar(c, out)
	case psPrimStream:
		if c >= 'a' && c <= 'z' {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psPrimSkip:
		if c >= 'a' && c <= 'z' {
			return
		}
		e.afterValueChar(c, out)
	case psDone:
	}
}

// ── Render dòng ──

// emitKeyLine được gọi khi phân tích xong key trong obj, ghi ra tiền tố "<lf><indent>key:".
// Trong chế độ stream trần không ghi tiền tố key (key được lưu trong keyBuf để beginString đánh giá).
func (e *jsonFieldExtractor) emitKeyLine(out *strings.Builder, key string) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteString(key)
	out.WriteByte(':')
}

// emitArrayItem được gọi khi bắt đầu mỗi phần tử trong arr, ghi ra "<lf><indent>-". Phần tử
// primitive sẽ nối tiếp bằng dấu cách rồi emit giá trị; phần tử struct sẽ được xử lý tự động xuống dòng bởi việc lồng phía sau.
func (e *jsonFieldExtractor) emitArrayItem(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteByte('-')
}

// ── Bắt đầu value ──

func (e *jsonFieldExtractor) beginString(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// Stream trần: chỉ giá trị string của key mục tiêu trong obj cấp cao nhất mới được xuất ra
		if e.cfg.nakedKey == e.keyBuf.String() && len(e.stack) == 1 && e.stack[0] == 'O' {
			e.state = psStringStream
		} else {
			e.state = psStringSkip
		}
		e.escape = false
		e.uHex = nil
		return
	}
	// Tổng quát: trường obj nối tiếp "key: " (đã emit "key:", thêm khoảng trắng); phần tử arr nối tiếp "- "
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	e.state = psStringStream
	e.escape = false
	e.uHex = nil
}

func (e *jsonFieldExtractor) beginNumber(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psNumberSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psNumberStream
}

func (e *jsonFieldExtractor) beginPrim(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psPrimSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psPrimStream
}

func (e *jsonFieldExtractor) beginNested(kind byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// Chế độ stream trần không bung cấu trúc lồng; dùng độ sâu stack để theo dõi đến khi gặp } / ]
		e.push(kind)
		if kind == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
		return
	}
	// Chế độ tổng quát: khi phần tử arr là cấu trúc lồng, emit trước "<indent>-" trên một dòng riêng
	// (sau ":" của obj key không có khoảng trắng, để key con lồng nhau tự động xuống dòng)
	if e.parent() == 'A' {
		e.emitArrayItem(out)
	}
	e.push(kind)
	if kind == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// closeContainer xử lý } hoặc ].
func (e *jsonFieldExtractor) closeContainer(out *strings.Builder) {
	e.pop()
	if len(e.stack) == 0 {
		// Xử lý dự phòng cho args rỗng (như novel_context không truyền tham số): emitKeyLine không có cơ hội xuất header,
		// bổ sung ở đây một lần, tránh rơi vào trường hợp "không có tiêu đề cũng không có nội dung".
		if !e.started && e.cfg.nakedKey == "" && e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
			e.started = true
		}
		// Xuống dòng cuối cùng để tạo ranh giới rõ ràng giữa bảng điều khiển và đoạn xuất ra tiếp theo
		if e.started {
			out.WriteByte('\n')
		}
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// ── stream string ──

func (e *jsonFieldExtractor) handleStringByte(c byte, out *strings.Builder, skipping bool) {
	if e.uHex != nil {
		e.uHex = append(e.uHex, c)
		if len(e.uHex) == 4 {
			if r, ok := parseHex4(e.uHex); ok && !skipping {
				var buf [4]byte
				n := utf8.EncodeRune(buf[:], r)
				out.Write(buf[:n])
			}
			e.uHex = nil
		}
		return
	}
	if e.escape {
		e.escape = false
		if !skipping {
			writeEscapedByte(out, c)
		}
		if c == 'u' {
			e.uHex = make([]byte, 0, 4)
		}
		return
	}
	if c == '\\' {
		e.escape = true
		return
	}
	if c == '"' {
		e.afterValueDone()
		return
	}
	if !skipping {
		out.WriteByte(c)
	}
}

func writeEscapedByte(out *strings.Builder, c byte) {
	switch c {
	case 'n':
		out.WriteByte('\n')
	case 't':
		out.WriteByte('\t')
	case 'r':
		out.WriteByte('\r')
	case '"':
		out.WriteByte('"')
	case '\\':
		out.WriteByte('\\')
	case '/':
		out.WriteByte('/')
	case 'b', 'f':
		// Backspace / Form feed: bỏ qua
	case 'u':
		// Bộ đệm uHex được thiết lập bởi bên gọi; không xuất ra ở đây
	default:
		out.WriteByte('\\')
		out.WriteByte(c)
	}
}

// ── Kết thúc ──

// afterValueDone đóng string (đọc đến `"` cuối cùng) rồi chuyển sang trạng thái tiếp theo.
func (e *jsonFieldExtractor) afterValueDone() {
	e.escape = false
	e.uHex = nil
	if len(e.stack) == 0 {
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// afterValueChar "Ký tự kết thúc" của number / primitive đã được đọc sẽ quyết định trạng thái tiếp theo dựa trên ký tự đó.
// Ký tự này có thể là , / } / ] / khoảng trắng, được hàm này chuyển tiếp và phân phối.
func (e *jsonFieldExtractor) afterValueChar(c byte, out *strings.Builder) {
	switch c {
	case '}', ']':
		e.closeContainer(out)
	case ',', ' ', '\t', '\n', '\r':
		if len(e.stack) == 0 {
			e.state = psDone
			e.done = true
			return
		}
		if e.parent() == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
	}
}

// ── Công cụ ──

func isNumberByte(c byte) bool {
	switch c {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
		'-', '+', '.', 'e', 'E':
		return true
	}
	return false
}

func parseHex4(b []byte) (rune, bool) {
	var r rune
	for _, d := range b {
		var v rune
		switch {
		case d >= '0' && d <= '9':
			v = rune(d - '0')
		case d >= 'a' && d <= 'f':
			v = rune(d-'a') + 10
		case d >= 'A' && d <= 'F':
			v = rune(d-'A') + 10
		default:
			return 0, false
		}
		r = r*16 + v
	}
	return r, true
}
