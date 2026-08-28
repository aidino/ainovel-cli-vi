package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// outlineGridThreshold Ngưỡng số chương để chuyển đại cương sang nhiều cột.
// Giới hạn short tier là 25 chương, dưới 20 chương một cột là đủ trên một màn hình, và có thể giữ nguyên huy hiệu "đang tiến hành";
// Ở chế độ layered của truyện dài, n sẽ vượt qua 20 khi cuộn, tự động chuyển mượt mà sang nhiều cột.
const outlineGridThreshold = 20

// renderOutlineSection Chọn bố cục theo số chương: ít thì một cột (có huy hiệu "đang tiến hành"), nhiều thì lưới nhiều cột.
func renderOutlineSection(snap host.UISnapshot, contentW int) string {
	if len(snap.Outline) < outlineGridThreshold {
		return renderOutlineList(snap, contentW)
	}
	return renderOutlineGrid(snap, contentW)
}

// renderOutlineList Danh sách chương một cột (dành cho truyện ngắn). Mỗi dòng có huy hiệu "đang tiến hành" ở cuối, nhịp đọc dọc giống với mục lục hơn.
func renderOutlineList(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	for _, e := range snap.Outline {
		ch := fmt.Sprintf("%2d", e.Chapter)
		var marker, chStyle string
		titleStyle := cardContentStyle
		switch {
		case snap.CompletedCount >= e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
		case snap.InProgressChapter == e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
			chStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		default:
			marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		title := truncate(e.Title, contentW-6)
		line := marker + chStyle + " " + titleStyle.Render(title)
		if snap.InProgressChapter == e.Chapter {
			line += lipgloss.NewStyle().Foreground(colorAccent).Italic(true).Render(" Đang tiến hành")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineGrid Điền các chương đại cương thành lưới nhiều cột theo thứ tự "cột trước", tránh để trống nhiều ở màn hình rộng.
// Số cột tự động điều chỉnh theo contentW (1-4), số chương tăng dần trong cột ("đọc hết một cột rồi đọc tiếp cột sau").
// Sự đánh đổi với bố cục cột đơn: từ bỏ huy hiệu " Đang tiến hành" ở cuối——trong bố cục nhiều cột, huy hiệu sẽ phá vỡ căn chỉnh cột,
// hơn nữa ▸ + màu vàng + "Đang sáng tác Chương N" ở cột tổng quan bên trái đã thể hiện rõ thông tin.
func renderOutlineGrid(snap host.UISnapshot, contentW int) string {
	n := len(snap.Outline)
	if n == 0 {
		return ""
	}
	chNumW := 2
	titleW := 0
	for _, e := range snap.Outline {
		if w := len(strconv.Itoa(e.Chapter)); w > chNumW {
			chNumW = w
		}
		if w := lipgloss.Width(e.Title); w > titleW {
			titleW = w
		}
	}
	// Giới hạn độ rộng tiêu đề là 14; cắt ngắn các tiêu đề dài để tránh một vài tiêu đề dài làm to cả cell
	if titleW > 14 {
		titleW = 14
	} else if titleW < 4 {
		titleW = 4
	}
	cellW := 3 + chNumW + titleW // marker(1) + dấu cách(1) + số chương + dấu cách(1) + tiêu đề
	gutter := 4
	cols := (contentW + gutter) / (cellW + gutter)
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	var b strings.Builder
	cellStyle := lipgloss.NewStyle().Width(cellW)
	gutterStr := strings.Repeat(" ", gutter)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= n {
				break
			}
			cell := renderOutlineCell(snap.Outline[idx], snap, chNumW, titleW)
			// Điền thêm vào bằng cellW + gutter nếu các cột sau vẫn còn cell; nếu không thì không điền
			if c < cols-1 && (c+1)*rows+r < n {
				b.WriteString(cellStyle.Render(cell))
				b.WriteString(gutterStr)
			} else {
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineCell render từng cell chương: Hoàn thành（xanh lục●）/ Đang tiến hành（vàng▸）/ Chưa bắt đầu（xám○）。
func renderOutlineCell(e host.OutlineSnapshot, snap host.UISnapshot, chNumW, titleW int) string {
	chStr := fmt.Sprintf("%*d", chNumW, e.Chapter)
	title := truncateWidth(e.Title, titleW)
	var marker, chRendered, titleRendered string
	switch {
	case snap.CompletedCount >= e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = cardContentStyle.Render(title)
	case snap.InProgressChapter == e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
		chRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
	default:
		marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorMuted).Render(title)
	}
	return marker + " " + chRendered + " " + titleRendered
}

// truncateWidth Cắt bớt theo "chiều rộng thị giác" (ký tự chữ Hán tính 2 cột), giống với lipgloss.Width.
// Không thêm dấu chấm lửng, dùng chung cho căn lề cell của lưới và truncate.
func truncateWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > maxW {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// renderDetailContent Xây dựng nội dung bảng chi tiết bên phải.
// Ưu tiên hiển thị thiết lập cơ bản (đại cương, nhân vật), sau đó là thông tin thời gian chạy (commit, đọc kiểm, v.v.).
func renderDetailContent(snap host.UISnapshot, contentW int) string {
	var b strings.Builder

	// Đại cương
	if len(snap.Outline) > 0 {
		outlineHeader := ":: Đại cương"
		if snap.Layered {
			outlineHeader = fmt.Sprintf(":: Đại cương (%s · Đại cương quy hoạch động)", snap.CurrentVolumeArc)
		}
		b.WriteString(panelTitleStyle.Render(outlineHeader))
		b.WriteString("\n")
		b.WriteString(renderOutlineSection(snap, contentW))
		// Gợi ý quy hoạch cuộn
		compassStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		if snap.Layered {
			if snap.NextVolumeTitle != "" {
				b.WriteString(compassStyle.Render("  ┄ Tập tiếp theo: " + snap.NextVolumeTitle))
				b.WriteString("\n")
			}
			b.WriteString(compassStyle.Render("  ··· Các chương tiếp theo tự động được sinh ra trong quá trình sáng tác"))
			b.WriteString("\n")
			if snap.CompassDirection != "" {
				direction := fmt.Sprintf("  → Kết cục: %s", snap.CompassDirection)
				if snap.CompassScale != "" {
					direction += "（" + snap.CompassScale + "）"
				}
				b.WriteString(compassStyle.Render(truncate(direction, contentW)))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	// Nhân vật
	if len(snap.Characters) > 0 {
		b.WriteString(panelTitleStyle.Render(":: Nhân vật"))
		b.WriteString("\n")
		for _, c := range snap.Characters {
			writeBulletWrapped(&b, c, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	// Sinh thái vai phụ: tổng số vai phụ đã xuất hiện + top 5 hoạt động gần đây
	if snap.SupportingCount > 0 {
		b.WriteString(panelTitleStyle.Render(":: Sinh thái vai phụ"))
		b.WriteString("\n")
		b.WriteString(cardContentStyle.Render(truncate(fmt.Sprintf("Đã xuất hiện: %d người", snap.SupportingCount), contentW)))
		b.WriteString("\n")
		for _, name := range snap.RecentSupporting {
			writeBulletWrapped(&b, name, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	if snap.Synopsis != "" {
		b.WriteString(panelTitleStyle.Render(":: Tóm tắt"))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Synopsis, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	// Tiền đề
	if snap.Premise != "" {
		b.WriteString(panelTitleStyle.Render(":: Tiền đề"))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Premise, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	if snap.LastCommitSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Commit gần đây ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastCommitSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if snap.LastReviewSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Đọc kiểm gần đây ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastReviewSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if len(snap.RecentSummaries) > 0 {
		b.WriteString(cardTitleStyle.Render("~ Tóm tắt ~"))
		b.WriteString("\n")
		for _, s := range snap.RecentSummaries {
			writeWrapped(&b, s, contentW, cardContentStyle)
		}
	}

	return b.String()
}

// writeWrapped Viết một đoạn văn bẻ dòng theo chiều rộng thị giác, mỗi dòng render style độc lập.
func writeWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for _, line := range wrapStreamText(text, max(8, contentW)) {
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
}

// writeBulletWrapped Viết một mục "· ": bẻ dòng theo chiều rộng, dòng tiếp thụt lề 2 dấu cách.
func writeBulletWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for i, line := range wrapStreamText(text, max(8, contentW-2)) {
		prefix := "· "
		if i > 0 {
			prefix = "  "
		}
		b.WriteString(style.Render(prefix + line))
		b.WriteString("\n")
	}
}
