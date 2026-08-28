package tui

import "github.com/charmbracelet/lipgloss"

// Bảng màu theme — tông ấm phong cách sách
// AdaptiveColor: Light = giá trị nền sáng, Dark = giá trị nền tối
//
// Nguyên tắc thiết kế: Light giữ nguyên ổn định (nền sáng đã chỉnh ra hiệu ứng ưng ý); Dark thống nhất sẽ
// tăng sáng ~25% lightness, hơi tăng bão hòa, đảm bảo nền tối có đủ độ tương phản (colorDim trước đây #6b6355
// gần như không nhìn thấy trên nền đen #1c1c1c, đường phân cách/chữ phụ biến mất hết).
//
// colorAccent2 nền tối đổi từ #7a9e7e thành xanh lục #5fb8a3, tách biệt với "xanh lá khỏe mạnh" của colorSuccess
// —— trước đây hai màu này y hệt nhau, làm nhãn màu của architect agent và cảm giác vui sướng "hit cao" lẫn lộn.
// bodyTextColor là chiến lược tiền cảnh của "chính văn trung tính":
//   - Terminal tối → NoColor, kế thừa tiền cảnh mặc định terminal, tránh việc chúng ta nhét cứng màu trắng kem #e8e0d0 vào theme tự chọn của người dùng
//     nền ấm/lạnh gây chói (người dùng test thực tế màu mặc định nền tối dễ đọc hơn).
//   - Terminal sáng → dùng mốc Light của colorText (nâu sậm #3d3529), giữ tông ấm thương hiệu;
//     màu đen mặc định nền sáng tương phản quá gắt, màu nâu sậm đã chỉnh nhìn trên nền sáng sẽ dịu hơn.
//
// AdaptiveColor hai đầu đều phải cho giá trị màu, không có mức "không màu", nên ở đây khi khởi động sẽ kiểm tra nền một lần,
// sau đó mọi giá trị tổng quan/chính văn chương/mô tả lệnh, v.v. "chính văn trung tính" đều trỏ tới bodyTextColor.
var bodyTextColor lipgloss.TerminalColor = func() lipgloss.TerminalColor {
	if lipgloss.HasDarkBackground() {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color("#3d3529")
}()

var (
	colorText    = lipgloss.AdaptiveColor{Light: "#3d3529", Dark: "#e8e0d0"}
	colorDim     = lipgloss.AdaptiveColor{Light: "#8a7e6b", Dark: "#8a8175"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#7a7060", Dark: "#b8b09c"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "#b8860b", Dark: "#e5b449"}
	colorAccent2 = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#5fb8a3"}
	colorRunning = lipgloss.AdaptiveColor{Light: "#6f8641", Dark: "#b5d075"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#7ec488"}
	colorError   = lipgloss.AdaptiveColor{Light: "#b5433a", Dark: "#e07060"}
	colorReview  = lipgloss.AdaptiveColor{Light: "#b07530", Dark: "#e09b5a"}
	colorContext = lipgloss.AdaptiveColor{Light: "#6b5a9e", Dark: "#a890d8"}
	colorTool    = lipgloss.AdaptiveColor{Light: "#3a7a8a", Dark: "#7ec5d8"}
)

// Ánh xạ màu nhãn trạng thái
var statusColors = map[string]lipgloss.AdaptiveColor{
	"READY":    colorDim,
	"PAUSING":  colorAccent,
	"PAUSED":   colorAccent,
	"RUNNING":  colorRunning,
	"REVIEW":   colorReview,
	"REWRITE":  colorReview,
	"COMPLETE": colorSuccess,
	"ERROR":    colorError,
}

// Hiển thị trạng thái: icon + nhãn tiếng Việt. Nhất quán với theme tông ấm tổng thể, tránh khối màu đặc đột ngột.
// Icon của RUNNING để trống, do spinner frame tự động điền, để cảm giác động hòa vào chính chỉ báo trạng thái.
var statusDisplay = map[string]struct {
	icon  string
	label string
}{
	"READY":    {"○", "Sẵn sàng"},
	"RUNNING":  {"", "Đang chạy"},
	"REVIEW":   {"◆", "Đọc kiểm"},
	"REWRITE":  {"◆", "Làm lại"},
	"COMPLETE": {"●", "Hoàn thành"},
	"PAUSED":   {"⏸", "Tạm dừng"},
	"PAUSING":  {"⏸", "Đang tạm dừng"},
	"ERROR":    {"✕", "Lỗi"},
}

// Ánh xạ màu phân loại sự kiện
var categoryColors = map[string]lipgloss.AdaptiveColor{
	"DISPATCH": colorAccent,
	"DECISION": colorContext,
	"TOOL":     colorTool,
	"SYSTEM":   colorAccent,
	"USER":     colorAccent2,
	"REVIEW":   colorReview,
	"CHECK":    colorSuccess,
	"ERROR":    colorError,
	"AGENT":    colorMuted,
	"CONTEXT":  colorContext,
	"COMPACT":  colorContext,
}

// Style cơ sở
var (
	baseBorder = lipgloss.RoundedBorder()

	topBarStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(0, 1)

	statusIconStyle = lipgloss.NewStyle().
			Bold(true)

	statusLabelStyle = lipgloss.NewStyle().
				Foreground(colorText)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	fieldLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(10)

	// fieldValueStyle / cardContentStyle dùng bodyTextColor —— giá trị vùng tổng quan (trạng thái chạy,
	// số chương đã hoàn thành, số chữ, v.v.), mục đại cương, danh sách nhân vật, tóm tắt chương, v.v. "nội dung chính văn trung tính"
	// đi theo màu tiền cảnh mặc định terminal trên nền tối (tránh nhét cứng màu trắng kem chói theme), nền sáng dùng nâu sậm giữ tông ấm.
	// Các phần tử có tính ngữ nghĩa mạnh (tiêu đề, giá trị highlight, trạng thái, lỗi, tô màu tỉ lệ hit, v.v.) vẫn đi theo colorAccent /
	// colorError, v.v. màu theme.
	fieldValueStyle = lipgloss.NewStyle().Foreground(bodyTextColor)

	highlightValueStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	contextUsageMetaStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	cardTitleStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	cardContentStyle = lipgloss.NewStyle().Foreground(bodyTextColor)
)
