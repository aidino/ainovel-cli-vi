package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
)

const resetForeground = "\x1b[39m"

// highlightCommandToken chỉ tô màu cho token lệnh đã được xác nhận, giữ nguyên
// con trỏ, đảo màu và ngắt dòng chuỗi ANSI ban đầu của textarea. Tham số bắt đầu từ ký tự khoảng trắng đầu tiên, luôn sử dụng màu chữ của phần chính.
func highlightCommandToken(inputView, inputValue, commandToken string) string {
	if commandToken == "" {
		return inputView
	}
	fields := strings.Fields(inputValue)
	if len(fields) == 0 || fields[0] != commandToken {
		return inputView
	}
	plain := ansi.Strip(inputView)
	start := strings.Index(plain, commandToken)
	if start < 0 {
		return inputView
	}
	return highlightANSIByteRange(inputView, start, start+len(commandToken))
}

// highlightANSIByteRange phủ màu tiền cảnh lên khoảng byte sau khi đã loại bỏ ANSI. Nếu trong khoảng gặp
// SGR riêng của textarea (ví dụ: con trỏ đảo màu), sẽ phát lại màu nhấn mạnh sau đó; khi kết thúc khoảng chỉ đặt lại
// màu tiền cảnh, không xóa các thuộc tính terminal khác của con trỏ.
func highlightANSIByteRange(value string, start, end int) string {
	if start < 0 || end <= start {
		return value
	}
	marker := lipgloss.NewStyle().Foreground(colorAccent).Render("x")
	markerAt := strings.IndexByte(marker, 'x')
	if markerAt <= 0 {
		return value
	}
	accent := marker[:markerAt]

	var out strings.Builder
	out.Grow(len(value) + len(accent)*2 + len(resetForeground))
	plainPos := 0
	active := false
	var state byte
	for len(value) > 0 {
		sequence, _, size, nextState := ansi.DecodeSequence(value, state, nil)
		state = nextState
		plain := ansi.Strip(sequence)
		if plain == "" {
			out.WriteString(sequence)
			if active {
				out.WriteString(accent)
			}
			value = value[size:]
			continue
		}
		if !active && plainPos == start {
			out.WriteString(accent)
			active = true
		}
		out.WriteString(sequence)
		value = value[size:]
		plainPos += len(plain)
		if active && plainPos >= end {
			out.WriteString(resetForeground)
			active = false
		}
	}
	if active {
		out.WriteString(resetForeground)
	}
	return out.String()
}

// renderInputBox render khu vực nhập liệu ở dưới cùng: ô nhập liệu, dòng gợi ý phím tắt, thanh trạng thái sử dụng ở dưới cùng.
// Ô nhập liệu tự phụ trách nhập và gợi ý, không chứa thanh chế độ khởi động.
func renderInputBox(inputView, hints string, snap host.UISnapshot, outputDir string, width int) string {
	innerW := width - 4 // border + padding
	if innerW < 12 {
		innerW = 12
	}

	// Dòng nhập: dấu nhắc + ô nhập liệu
	prompt := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("❯ ")
	inputLine := prompt + inputView

	// Dòng gợi ý: phím tắt chiếm cả dòng——thông tin chạy như model/chi phí chuyển vào thanh trạng thái dưới cùng, không còn chen chúc ở bên phải cắt lẫn nhau.
	line2 := fitInlineLine(hints, innerW)

	// Khu vực nhập (một box duy nhất, tránh nhìn giống như có hai ô nhập)
	inputStyle := lipgloss.NewStyle().
		Width(width).
		Border(baseBorder, true, false, true, false).
		BorderForeground(colorDim).
		Padding(0, 1)
	inputBlock := inputStyle.Render(inputLine)

	// Dòng gợi ý (không viền, bám sát dưới đường viền ngang dưới)
	hintStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 2)
	hintBlock := hintStyle.Render(line2)

	// Thanh trạng thái chiếm dòng trống cuối cùng ban đầu của khu vực nhập: tổng chiều cao khối không đổi, layoutHeights không cần điều chỉnh.
	statusBlock := hintStyle.Render(renderStatusBar(snap, outputDir, innerW))

	return inputBlock + "\n" + hintBlock + "\n" + statusBlock
}

func joinInlineSides(left, right string, width int) string {
	if width <= 0 {
		return left + right
	}
	if strings.TrimSpace(right) == "" {
		return fitInlineLine(left, width)
	}

	right = fitInlineLine(right, width)
	rightW := ansi.StringWidth(right)
	if rightW >= width {
		return right
	}

	leftMax := width - rightW - 1
	if leftMax < 0 {
		leftMax = 0
	}
	left = fitInlineLine(left, leftMax)
	gap := width - ansi.StringWidth(left) - rightW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func fitInlineLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "...")
}
