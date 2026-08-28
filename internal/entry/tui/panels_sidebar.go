package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStateContent renders the raw content of the status bar (no borders), used for stateVP.SetContent.
func renderStateContent(snap host.UISnapshot, contentW int) string {
	contentW = max(12, contentW)
	agents := sidebarAgents(snap.Agents)
	idleAgents := sidebarIdleAgents(snap.Agents)
	var sections []string

	if snap.RecoveryLabel != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorMuted).Italic(true).
			Render(truncate(snap.RecoveryLabel, contentW)))
	}

	var overview strings.Builder
	overview.WriteString(renderField("Trạng thái", snapshotRuntimeStateLabel(snap.RuntimeState)))
	overview.WriteString(renderField("Giai đoạn", snapshotPhaseLabel(snap.Phase)))
	overview.WriteString(renderField("Quy trình", snapshotFlowLabel(snap.Flow)))
	if snap.AdvanceMode == "review" {
		advance := "Nghiệm thu từng chương"
		if snap.AdvancePermitChapter > 0 {
			advance = fmt.Sprintf("Đã thông qua Chương %d", snap.AdvancePermitChapter)
		}
		overview.WriteString(renderField("Đẩy tiến", advance))
	} else if snap.AdvanceMode == "auto" {
		overview.WriteString(renderField("Đẩy tiến", "Tự động"))
	}
	if snap.Layered {
		overview.WriteString(renderField("Đã hoàn thành", fmt.Sprintf("%d chương", snap.CompletedCount)))
		// Quy hoạch động phân tầng: cột phải chỉ hiển thị các chương đã mở ra của arc hiện tại, "đã quy hoạch" cũng dùng chung tiêu chuẩn,
		// nếu không sẽ lẫn ước lượng thô EstimatedChapters của arc bộ khung (vd 92) vào, không khớp với đại cương.
		// giá trị progress.TotalChapters chỉ dùng cho quyết định ContextProfile nội bộ, không rò rỉ ra UI.
		if planned := len(snap.Outline); planned > 0 {
			overview.WriteString(renderField("Đã quy hoạch", fmt.Sprintf("%d chương", planned)))
		}
	} else {
		switch {
		case snap.TotalChapters > 0:
			overview.WriteString(renderField("Tiến độ", fmt.Sprintf("%d / %d chương", snap.CompletedCount, snap.TotalChapters)))
		default:
			overview.WriteString(renderField("Đã hoàn thành", fmt.Sprintf("%d chương", snap.CompletedCount)))
		}
	}
	overview.WriteString(renderField("Số chữ", formatNumber(snap.TotalWordCount)))
	if label, ch := inProgressDisplay(snap); label != "" {
		overview.WriteString(renderField(label, fmt.Sprintf("Chương %d", ch)))
	}
	if headline := snapshotHeadline(snap); headline != "" {
		label := "Hiện tại"
		if !snap.IsRunning {
			label = "Chờ khôi phục"
		}
		overview.WriteString(renderHighlightField(label, truncate(headline, contentW-10)))
	}
	sections = append(sections, renderSidebarSection("Tổng quan", overview.String(), contentW))

	if len(agents) > 0 {
		var agentBody strings.Builder
		for _, agent := range agents {
			agentBody.WriteString(renderAgentLine(agent, contentW))
			agentBody.WriteString("\n")
		}
		if len(idleAgents) > 0 {
			agentBody.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Chờ lệnh: " + truncate(strings.Join(idleAgents, " · "), max(8, contentW-2))))
			agentBody.WriteString("\n")
		}
		sections = append(sections, renderSidebarSection("Nhân vật đang chạy", agentBody.String(), contentW))
	}

	if len(snap.PendingRewrites) > 0 {
		var rewrite strings.Builder
		rewrite.WriteString(renderHighlightField("Hàng đợi", fmt.Sprintf("%v", snap.PendingRewrites)))
		if snap.RewriteReason != "" {
			rewrite.WriteString(renderField("Nguyên nhân", truncate(snap.RewriteReason, contentW-10)))
		}
		sections = append(sections, renderSidebarSection("Làm lại", rewrite.String(), contentW))
	}

	if snap.PendingSteer != "" {
		sections = append(sections, renderSidebarSection("Can thiệp",
			renderHighlightField("Chờ xử lý", truncate(snap.PendingSteer, contentW-10)), contentW))
	}
	if snap.HasAdvanceHold {
		sections = append(sections, renderSidebarSection("Dừng nghiệm thu",
			renderHighlightField("Đang đợi", truncate(snap.AdvanceHoldReason, contentW-10)), contentW))
	}

	if body := renderUsageSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Sử dụng", body, contentW))
	}

	if body := renderCacheSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Cache", body, contentW))
	}

	return strings.Join(sections, "\n\n")
}

func renderAgentLine(agent host.AgentSnapshot, width int) string {
	stateColor := taskStatusColor(agent.State)
	icon := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateIcon(agent.State))
	badge := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateLabel(agent.State))
	name := lipgloss.NewStyle().Bold(true).Foreground(bodyTextColor).Render(agentDisplayName(agent.Name))
	line := icon + " " + name + " " + badge

	taskLine := agentTaskLine(agent)
	if taskLine != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Render("  "+truncate(taskLine, max(8, width-2)))
	}

	detail := agent.Summary
	if agent.Tool != "" {
		detail = agent.Tool
	}
	if agent.State == "idle" && detail == "Chờ lệnh" {
		detail = ""
	}
	if detail != "" && detail != taskLine {
		line += "\n" + lipgloss.NewStyle().Foreground(colorMuted).Render("  "+truncate(detail, max(8, width-2)))
	}
	if ctx := agentContextLine(agent); ctx != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  "+truncate(ctx, max(8, width-2)))
	}
	return line
}

func renderSidebarSection(title, body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lineW := max(0, width-lipgloss.Width(title)-1)
	header := panelTitleStyle.Render(title) + " " +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", lineW))
	card := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorDim).
		PaddingLeft(1).
		Render(body)
	return header + "\n" + card
}

func sidebarAgents(agents []host.AgentSnapshot) []host.AgentSnapshot {
	var out []host.AgentSnapshot
	for _, agent := range agents {
		if agent.State == "idle" {
			continue
		}
		out = append(out, agent)
	}
	if len(out) == 0 {
		out = append(out, agents...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i], out[j]
		if agentStateRank(li.State) != agentStateRank(lj.State) {
			return agentStateRank(li.State) < agentStateRank(lj.State)
		}
		return agentOrder(li.Name) < agentOrder(lj.Name)
	})
	return out
}

func sidebarIdleAgents(agents []host.AgentSnapshot) []string {
	var names []string
	hasActive := false
	for _, agent := range agents {
		if agent.State != "idle" {
			hasActive = true
			continue
		}
		names = append(names, agentDisplayName(agent.Name))
	}
	if !hasActive {
		return nil
	}
	sort.Strings(names)
	return names
}

// inProgressDisplay Tính nhãn và số chương cho trường "đang tiến hành".
// Chọn động từ theo flow (đánh bóng/viết lại/sáng tác); in_progress_chapter không khớp với flow sẽ coi là stale:
//   - ở chế độ polishing/rewriting, chương không nằm trong pending_rewrites → quay lại chương đầu của hàng đợi
//   - trường bằng 0 thì không render
func inProgressDisplay(snap host.UISnapshot) (label string, chapter int) {
	ch := snap.InProgressChapter
	switch snap.Flow {
	case "polishing":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang đánh bóng", ch
	case "rewriting":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang viết lại", ch
	default:
		if ch <= 0 {
			return "", 0
		}
		return "Đang viết", ch
	}
}

func snapshotHeadline(snap host.UISnapshot) string {
	if snap.PendingSteer != "" {
		if snap.StateLabel == "wait_resuming" {
			return "Chờ khôi phục: Xử lý can thiệp người dùng"
		}
		return "Trạng thái khôi phục bất thường: " + snap.StateLabel
	}
	if len(snap.PendingRewrites) > 0 {
		if snap.StateLabel == "wait_resuming" {
			return "Chờ khôi phục: Xử lý làm lại"
		}
		return "Đang đợi xử lý làm lại"
	}
	if snap.AdvanceMode == "review" && !snap.IsRunning && snap.Phase == "writing" {
		return "Nghiệm thu từng chương: đang đợi thông qua chương tiếp theo"
	}
	return ""
}

func snapshotPhaseLabel(phase string) string {
	switch phase {
	case "premise":
		return "Tiền đề"
	case "outline":
		return "Đại cương"
	case "writing":
		return "Viết"
	case "complete":
		return "Hoàn thành"
	case "init":
		return "Khởi tạo"
	default:
		if phase == "" {
			return "-"
		}
		return phase
	}
}

func snapshotRuntimeStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "pausing":
		return "Đang tạm dừng"
	case "paused":
		return "Đã tạm dừng"
	case "completed":
		return "Đã hoàn thành"
	default:
		return "Nhàn rỗi"
	}
}

func snapshotFlowLabel(flow string) string {
	switch flow {
	case "":
		return "-"
	case "writing":
		return "Viết"
	case "reviewing":
		return "Đọc kiểm"
	case "rewriting":
		return "Viết lại"
	case "polishing":
		return "Đánh bóng"
	case "steering":
		return "Can thiệp"
	default:
		return flow
	}
}

func renderUsageSidebar(snap host.UISnapshot, width int) string {
	if snap.TotalInputTokens <= 0 && snap.TotalOutputTokens <= 0 && snap.TotalCostUSD <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderField("Đầu vào", formatTokensCompact(snap.TotalInputTokens)))
	b.WriteString(renderField("Đầu ra", formatTokensCompact(snap.TotalOutputTokens)))
	if cost := formatCostUSD(snap.TotalCostUSD); cost != "" {
		b.WriteString(renderField("Chi phí", cost))
	}
	if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
		b.WriteString(renderField("Tiết kiệm", saved))
	}
	if snap.BudgetLimitUSD > 0 {
		pct := snap.TotalCostUSD / snap.BudgetLimitUSD * 100
		b.WriteString(renderField("Ngân sách", fmt.Sprintf("$%.2f/$%.2f (%.0f%%)", snap.TotalCostUSD, snap.BudgetLimitUSD, pct)))
	}

	agentStats := usageStatsByCost(snap.CachePerAgent)
	if len(agentStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Nhân vật", width))
		limit := min(len(agentStats), 4)
		for i := 0; i < limit; i++ {
			a := agentStats[i]
			b.WriteString(renderUsageLine(agentDisplayName(a.Role), eventAgentColor(a.Role), a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	modelStats := usageStatsByCost(snap.CachePerModel)
	if len(modelStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Model", width))
		limit := min(len(modelStats), 4)
		for i := 0; i < limit; i++ {
			a := modelStats[i]
			b.WriteString(renderUsageLine(modelDisplayName(a.Model), bodyTextColor, a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func usageStatsByCost(in []host.AgentCacheStat) []host.AgentCacheStat {
	out := append([]host.AgentCacheStat(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input+out[i].Output > out[j].Input+out[j].Output
	})
	return out
}

func renderUsageGroupHeader(label string, width int) string {
	line := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("·", max(8, width-lipgloss.Width(label)-3)))
	return lipgloss.NewStyle().Foreground(colorMuted).Render(label+" ") + line + "\n"
}

func renderUsageLine(name string, color lipgloss.TerminalColor, input, output int, cost float64, width int) string {
	nameW := 11
	if width < 24 {
		nameW = 8
	}
	nameCell := lipgloss.NewStyle().Foreground(color).Width(nameW).
		Render(truncate(name, nameW))
	tokens := formatTokensCompact(input + output)
	right := tokens
	if costStr := formatCostUSD(cost); costStr != "" {
		right += " · " + costStr
	}
	// Cắt cụt đến số dòng tối đa (tránh lỗi vượt quá giới hạn khi cửa sổ quá nhỏ), không để lại khoảng trắng dư thừa; hiển thị rõ ràng, tránh
	// "gpt-5.6-sol5.3k" các tên model bị dính liền với lượng dùng.
	return fitInlineLine(nameCell+" "+lipgloss.NewStyle().Foreground(colorDim).Render(right), width)
}

func modelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	parts := strings.Split(model, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[1:], "/")
	}
	if len(parts) == 2 {
		return parts[1]
	}
	return model
}

// renderCacheSidebar render khu vực "Cache" ở cột trái.
//
// 3 trạng thái:
//  1. Hoàn toàn không tốn token: trả về rỗng, không render section
//  2. Mọi role trong phiên hiện tại đều chạy model không hỗ trợ prompt cache: chỉ render một dòng nhắc nhở "chưa bật"
//  3. Đã bật: phần trên "tỉ lệ hit tích lũy/10 lần gần nhất · tiết kiệm · đọc/ghi" + phân cách + dòng per-role
func renderCacheSidebar(snap host.UISnapshot, width int) string {
	if snap.MissingAssistantUsage > 0 && snap.TotalInputTokens <= 0 {
		warn := lipgloss.NewStyle().Foreground(colorError).Bold(true).
			Render(fmt.Sprintf("⚠ Upstream không trả usage (%d lần)", snap.MissingAssistantUsage))
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Kiểm tra provider stream_options.include_usage", max(8, width-2)))
		return warn + "\n" + hint + "\n"
	}

	if snap.TotalInputTokens <= 0 && snap.TotalCacheWriteTokens <= 0 {
		return ""
	}

	// Chưa bật toàn bộ quá trình → hiển thị một dòng giải thích, tránh người dùng hiểu lầm "hit 0% cần kiểm tra"
	if !snap.OverallCacheCapable && snap.TotalCacheReadTokens == 0 && snap.TotalCacheWriteTokens == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Model hiện tại chưa bật prompt cache", max(8, width-2))) + "\n"
	}

	var b strings.Builder

	// Chỉ số tổng hợp ở trên: tích lũy + N lần gần nhất mỗi cái một dòng, nhãn rõ ràng, tránh kiểu "X% · N lần gần nhất Y%"
	// lẫn lộn 3 loại dấu phân cách làm mờ nghĩa.
	overallHit := cacheHitRate(snap.TotalCacheReadTokens, snap.TotalInputTokens)
	b.WriteString(renderField("Hit tích lũy", colorPercent(overallHit)))
	if snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
		recent := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
		b.WriteString(renderField(fmt.Sprintf("Hit %d lần gần nhất", snap.OverallRecentSamples), colorPercent(recent)))
	}

	if savedStr := formatCostUSD(snap.TotalSavedUSD); savedStr != "" {
		b.WriteString(renderField("Tiết kiệm", savedStr))
	}

	// Lượng đọc/ghi chia 2 dòng. Ghi bằng 0 là bình thường ở giao thức OpenAI/Gemini——
	// Hai hãng này caching tự động, ghi cache hoàn toàn miễn phí (lần đầu miss tính giá đầu vào bình thường,
	// lập cache không thu phí phụ), nên giao thức không lộ trường cache_creation, không cần thiết.
	// Chỉ Anthropic/Bedrock mới báo lượng ghi, vì ghi bị tính phí phụ,
	// phải đưa lượng này cho người dùng để tính phí.
	b.WriteString(renderField("Lượng đọc cache", formatTokensCompact(snap.TotalCacheReadTokens)))
	if snap.TotalCacheWriteTokens > 0 {
		b.WriteString(renderField("Lượng ghi cache", formatTokensCompact(snap.TotalCacheWriteTokens)))
	} else if snap.TotalCacheReadTokens > 0 {
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("(cache tự động không phí phụ)")
		b.WriteString(renderField("Lượng ghi cache", "0 "+hint))
	}

	// Đứt gãy = tiền tố không ngắn đi mà lượng hit giảm đột ngột (các trường hợp hợp lệ như đổi chương/nén đã được miễn). Nếu số lần lớn thường là
	// do server đuổi hoặc chuyển tiếp, xem warn "Đứt gãy chuỗi cache" trong tui.log.
	if snap.TotalCacheBreaks > 0 {
		v := lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("%d lần", snap.TotalCacheBreaks))
		b.WriteString(renderField("Đứt gãy chuỗi", v))
	}

		v := lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("%d times", snap.TotalCacheBreaks))
		b.WriteString(renderField("Cache Breaks", v))
	}

	var roles []host.AgentCacheStat
	for _, a := range snap.CachePerAgent {
		if a.Role != "arbiter" {
			roles = append(roles, a)
		}
	}
	if len(roles) > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render(strings.Repeat("·", max(8, width-12))))
		b.WriteString("\n")
		for _, a := range roles {
			b.WriteString(renderCacheAgentLine(a, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// colorPercent Tô màu phần trăm theo tỉ lệ hit rồi chuyển thành chuỗi, chỉ dùng cho cột giá trị.
func colorPercent(p float64) string {
	return lipgloss.NewStyle().Foreground(cacheHitColor(p)).Bold(true).
		Render(formatPercent(p))
}

// renderCacheAgentLine Render dòng cho từng role: role + tỉ lệ hit + đọc cache / tổng đầu vào.
//
// Hiển thị cả tử số mẫu số (cacheRead / input) để người dùng thấy rõ nguồn gốc tỉ lệ hit,
// cũng để nhận ra dữ liệu may mắn kiểu "tỉ lệ cao nhưng mẫu nhỏ" (vd 100% / 1k không đáng tin bằng 80% / 300k).
//
// Tỉ lệ ưu tiên dùng giá trị ổn định của cửa sổ trượt; khi không có mẫu thì dùng tích lũy. Cột trái chỉ dùng "/" ở đây,
// nghĩa chuyên biệt (dấu chia toán học: lượng hit cache / tổng lượng đầu vào), không nhầm lẫn với dấu khác.
//
// Hiển thị role:
//	Chưa bật     "WRITER        Chưa bật"
//	Đã bật     "WRITER        85%  · 323k / 394k"
//	Không có cache  Hiện rõ "Chưa bật", không lẫn vào 0/0 gây nhiễu phán đoán
func renderCacheAgentLine(a host.AgentCacheStat, width int) string {
	// Tên role hoàn toàn khớp với khu vực "Nhân vật đang chạy"; Width lấy 12 để ARCHITECT dài nhất
	// vẫn còn 1 khoảng trắng ở cuối để phân cách, các role khác tự điền bên phải.
	roleStyle := lipgloss.NewStyle().Foreground(eventAgentColor(a.Role)).Width(12)
	roleCell := roleStyle.Render(agentDisplayName(a.Role))

	if !a.CacheCapable {
		dim := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		_ = width
		return roleCell + dim.Render("Chưa bật")
	}

	// Ưu tiên tỉ lệ hit ổn định; không có mẫu thì dùng tích lũy.
	hit := cacheHitRate(a.RecentCacheRead, a.RecentInput)
	if a.RecentSamples == 0 || a.RecentInput == 0 {
		hit = cacheHitRate(a.CacheRead, a.Input)
	}
	// Phần trăm cố định 4 cột ("100%"), tránh cột lượng đọc nhảy qua lại.
	pctCell := lipgloss.NewStyle().Width(4).
		Render(colorPercent(hit))

	// Đọc tích lũy / Đầu vào tích lũy — Dù tỉ lệ ở trên dùng cửa sổ trượt, tử mẫu vẫn dùng tích lũy, vì
	// "thấy được quy mô" mới là mục đích chính của cột này; phần trăm đã cho tín hiệu ổn định.
	tokens := lipgloss.NewStyle().Foreground(colorDim).Render(
		" · " + formatTokensCompact(a.CacheRead) + " / " + formatTokensCompact(a.Input))
	_ = width
	return role + pctCell + tokens
}

// cacheHitRate Tính phần trăm trực tiếp vì input đã bao gồm cacheRead.
// Trả về 0 khi input == 0, tránh hit giả.
func cacheHitRate(cacheRead, input int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input) * 100
}

// cacheHitColor Tô màu tỉ lệ hit: ≥50% Xanh / 20-50% Vàng / <20% Đỏ.
// Ngược với mức dùng context: tỉ lệ hit cache càng cao càng tốt.
func cacheHitColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 50:
		return colorSuccess
	case percent >= 20:
		return colorReview
	default:
		return colorError
	}
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// formatTokensCompact Render số token thành dạng gọn "8.2k" / "1.4M".
// Dùng cho dòng per-role hẹp, tránh bị đẩy ra do dấu phẩy của formatNumber.
func formatTokensCompact(n int) string {
	if n <= 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func contextScopeLabel(scope string) string {
	switch scope {
	case "baseline":
		return "Cơ sở"
	case "projected":
		return "Phóng chiếu"
	case "recovered":
		return "Khôi phục"
	case "committed":
		return "Đã commit"
	case "skipped":
		return "Bỏ qua (đứt gãy)"
	default:
		return scope
	}
}

func contextStrategyLabel(strategy string) string {
	switch strategy {
	case "":
		return ""
	case "tool_result_microcompact":
		return "Nén nhẹ kết quả công cụ"
	case "light_trim":
		return "Cắt tỉa nhẹ"
	case "full_summary":
		return "Tóm tắt đầy đủ"
	default:
		return strategy
	}
}

func agentDisplayName(name string) string {
	return strings.ToUpper(name)
}

func agentTaskLine(agent host.AgentSnapshot) string {
	if agent.TaskKind != "" {
		return taskKindLabel(agent.TaskKind)
	}
	if agent.Summary != "" {
		return agent.Summary
	}
	return ""
}

func agentContextLine(agent host.AgentSnapshot) string {
	ctx := agent.Context
	if ctx.ContextWindow <= 0 || ctx.Tokens <= 0 {
		return ""
	}
	percentColor := contextPercentColor(ctx.Percent)
	percentStr := lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("ctx %.0f%%", ctx.Percent))
	parts := []string{percentStr}
	if scope := contextScopeLabel(ctx.Scope); scope != "" {
		parts = append(parts, scope)
	}
	if strategy := contextStrategyLabel(ctx.Strategy); strategy != "" {
		parts = append(parts, strategy)
	}
	return strings.Join(parts, " · ")
}

func agentStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "failed":
		return 1
	default:
		return 2
	}
}

func agentOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "architect"):
		return 0
	case name == "editor":
		return 2
	case name == "writer":
		return 3
	default:
		return 9
	}
}

func agentStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "failed":
		return "Lỗi"
	case "idle":
		return "Chờ lệnh"
	default:
		return state
	}
}

func agentStateIcon(state string) string {
	switch state {
	case "running":
		return "●"
	case "failed":
		return "×"
	default:
		return "·"
	}
}

func taskStatusColor(status string) lipgloss.AdaptiveColor {
	switch status {
	case "running":
		return colorSuccess
	case "queued":
		return colorMuted
	case "failed", "canceled":
		return colorError
	case "succeeded":
		return colorSuccess
	default:
		return colorDim
	}
}

func taskKindLabel(kind string) string {
	switch kind {
	case "foundation_plan":
		return "Quy hoạch cơ sở"
	case "chapter_write":
		return "Sáng tác chương"
	case "chapter_review":
		return "Đọc kiểm chương"
	case "chapter_rewrite":
		return "Viết lại chương"
	case "chapter_polish":
		return "Đánh bóng chương"
	case "arc_expand":
		return "Mở rộng arc"
	case "volume_append":
		return "Quy hoạch tập tiếp theo"
	case "steer_apply":
		return "Xử lý can thiệp"
	default:
		return kind
	}
}
