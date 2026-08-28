package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStatusBar Render thanh trạng thái sử dụng ở dưới cùng màn hình, chiếm dòng trống cuối cùng của khu vực nhập liệu (không tốn thêm chiều cao):
//
//	◆ provider model(cửa sổ,suy nghĩ) │ ↑đầu vào ↓đầu ra ⚡hit cache gần đây │ chi phí(/ngân sách) tiết kiệmX    ./thư mục sách
//
// Mục đích là "nhìn một phát thấy ngay chi phí": danh tính model phải trả phí, token tích lũy phiên, cảnh báo chi phí sắp chạm ngân sách.
// Dữ liệu từ UISnapshot poll mỗi 3s (mỗi lần gọi model xong usage sẽ được cộng dồn);
// chi tiết per-role/per-model và chẩn đoán cache vẫn do cột trái đảm nhận, ở đây không lặp lại.
func renderStatusBar(snap host.UISnapshot, outputDir string, width int) string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	val := lipgloss.NewStyle().Foreground(colorMuted)

	var segs []string
	if snap.ModelName != "" {
		s := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("◆") + " "
		if snap.Provider != "" {
			s += dim.Render(snap.Provider) + " "
		}
		s += val.Render(snap.ModelName)
		if suffix := modelInfoSuffix(snap); suffix != "" {
			s += dim.Render("(" + suffix + ")")
		}
		segs = append(segs, s)
	}
	if snap.TotalInputTokens > 0 || snap.TotalOutputTokens > 0 {
		s := dim.Render("↑") + val.Render(formatTokensCompact(snap.TotalInputTokens)) +
			" " + dim.Render("↓") + val.Render(formatTokensCompact(snap.TotalOutputTokens))
		// Tỉ lệ hit gần đây chỉ hiển thị khi model thật sự hỗ trợ prompt cache và có mẫu, tránh hiểu nhầm "0% cần kiểm tra".
		if snap.OverallCacheCapable && snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
			rate := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
			s += " " + lipgloss.NewStyle().Foreground(cacheHitColor(rate)).Render("⚡"+formatPercent(rate))
		}
		segs = append(segs, s)
	}
	if snap.TotalCostUSD > 0 || snap.BudgetLimitUSD > 0 {
		cost := formatCostUSD(snap.TotalCostUSD)
		if cost == "" {
			cost = "$0"
		}
		style := val
		if snap.BudgetLimitUSD > 0 {
			// Sắp chạm/vượt ngân sách dùng màu cảnh báo —— thanh trạng thái luôn hiện, là vị trí cần thấy ngân sách nhất.
			switch ratio := snap.TotalCostUSD / snap.BudgetLimitUSD; {
			case ratio >= 1:
				style = lipgloss.NewStyle().Foreground(colorError).Bold(true)
			case ratio >= 0.8:
				style = lipgloss.NewStyle().Foreground(colorReview)
			}
		}
		s := style.Render(cost)
		if snap.BudgetLimitUSD > 0 {
			s += dim.Render("/" + formatCostUSD(snap.BudgetLimitUSD))
		}
		if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
			s += dim.Render(" Tiết kiệm " + saved)
		}
		segs = append(segs, s)
	}

	left := strings.Join(segs, dim.Render(" │ "))
	var right string
	if outputDir != "" {
		right = dim.Render("./" + filepath.Base(outputDir))
	}
	if left == "" && right == "" {
		return dim.Render("READY")
	}
	return joinInlineSides(left, right, width)
}

// modelInfoSuffix Lắp ráp ngoặc chú thích model: cửa sổ ngữ cảnh + cấp độ suy nghĩ, vd "200K,med".
func modelInfoSuffix(snap host.UISnapshot) string {
	var parts []string
	if w := formatContextWindow(snap.ModelContextWindow); w != "" {
		parts = append(parts, w)
	}
	if t := formatThinkingLevel(snap.ThinkingLevel); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, ",")
}

func formatThinkingLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return "auto"
	case "medium":
		return "med"
	default:
		return strings.ToLower(strings.TrimSpace(level))
	}
}
