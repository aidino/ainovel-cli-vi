package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

type startupMode int

const (
	startupModeQuick startupMode = iota
	startupModeCoCreate
)

func (m startupMode) label() string {
	switch m {
	case startupModeCoCreate:
		return "Quy hoạch đồng sáng tạo"
	default:
		return "Bắt đầu nhanh"
	}
}

func (m startupMode) subtitle() string {
	switch m {
	case startupModeCoCreate:
		return "Trò chuyện với AI để làm rõ trước khi sáng tác"
	default:
		return "Một câu bắt đầu viết ngay"
	}
}

func placeholderForNewMode(mode startupMode) string {
	switch mode {
	case startupModeCoCreate:
		return "Nhập ý tưởng cốt lõi trước, Enter để bắt đầu đồng sáng tạo với AI"
	default:
		return "Nhập một câu yêu cầu tiểu thuyết, Enter để bắt đầu sáng tác ngay"
	}
}

func placeholderForCoCreate(state *cocreateState) string {
	if state == nil {
		return placeholderForNewMode(startupModeCoCreate)
	}
	switch {
	case state.awaiting:
		return "AI đang tổng hợp yêu cầu của bạn..."
	case state.canStart():
		if state.stage {
			return "Tiếp tục bổ sung, hoặc nhấn Ctrl+S để áp dụng hướng đi và tiếp tục sáng tác"
		}
		return "Tiếp tục bổ sung, hoặc nhấn Ctrl+S để bắt đầu sáng tác"
	default:
		return "Tiếp tục bổ sung yêu cầu, Enter để gửi cho AI"
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type cocreateState struct {
	session             *startup.CoCreateSession
	stage               bool // true=đồng sáng tạo theo giai đoạn (quy hoạch hướng đi tiếp theo khi đang chạy); false=đồng sáng tạo khởi động lạnh (làm rõ yêu cầu trước khi khởi động)
	awaiting            bool
	reqID               int
	cancel              context.CancelFunc // Hủy yêu cầu LLM hiện tại
	deltaCh             chan cocreateStreamItem
	doneCh              chan cocreateDoneMsg
	convVP              viewport.Model
	promptVP            viewport.Model
	convFollow          bool // true: nội dung mới stream tự động cuộn xuống đáy; người dùng cuộn lên sẽ thành false ngừng cuộn theo
	selectedSuggestions []string
	// focusPrompt quyết định ↑↓/PgUp/PgDn/Home/End cuộn cột nào: false=cột thoại trái (mặc định),
	// true=cột lệnh sáng tác phải. Trang chào mừng đã tắt báo cáo chuột (giữ sao chép gốc), cột phải tràn dùng Tab chuyển tiêu điểm rồi cuộn bằng phím.
	focusPrompt bool
}

func newCoCreateState(initial string) *cocreateState {
	makeVP := func() viewport.Model {
		vp := viewport.New(0, 0)
		vp.MouseWheelEnabled = true
		vp.MouseWheelDelta = 3
		return vp
	}
	return &cocreateState{
		session:    startup.NewCoCreateSession(strings.TrimSpace(initial)),
		awaiting:   true,
		convVP:     makeVP(),
		promptVP:   makeVP(),
		convFollow: true,
	}
}

// stageCoCreateOpener là lời mở đầu tổng hợp của người dùng trong đồng sáng tạo theo giai đoạn, gửi cho LLM ở lượt user kickoff,
// để trợ lý dựa vào "trạng thái câu chuyện hiện tại" chủ động mở đầu, thay vì để người dùng nói trước.
const stageCoCreateOpener = "Tôi tạm dừng một chút, muốn cùng bạn quy hoạch hướng đi tiếp theo."

// stageCoCreateSystemLine là hiển thị trung lập của câu mở đầu này trên UI: câu mở đầu bản chất do hệ thống tổng hợp,
// người dùng không thực sự nhập, nên không giả làm lời của "bạn", mà dùng dòng hệ thống để giải thích bối cảnh (nó vẫn gửi stageCoCreateOpener
// cho LLM, xem i==0 trong renderCoCreateConversationPanel).
const stageCoCreateSystemLine = "Đã tạm dừng sáng tác, vào đồng sáng tạo theo giai đoạn —— AI sẽ kết hợp tiến độ câu chuyện hiện tại, cùng bạn quy hoạch hướng đi tiếp theo."

// newStageCoCreateState tạo trạng thái đồng sáng tạo theo giai đoạn: seed mở đầu và đánh dấu stage, để runCoCreate chạy
// StageCoCreateStream, Ctrl+S chạy ResumeFromCoCreate.
func newStageCoCreateState() *cocreateState {
	s := newCoCreateState(stageCoCreateOpener)
	s.stage = true
	return s
}

func (s *cocreateState) appendUser(text string) {
	s.resetSuggestionInput()
	s.session.AppendUser(text)
}

func (s *cocreateState) apply(reply host.CoCreateReply) {
	s.awaiting = false
	s.resetSuggestionInput()
	s.session.ApplyReply(reply)
}

func (s *cocreateState) applyDelta(kind, text string) {
	s.session.ApplyDelta(kind, text)
}

func (s *cocreateState) canStart() bool {
	return s.session.CanStart()
}

func (s *cocreateState) initialInput() string {
	return s.session.InitialInput()
}

func (s *cocreateState) streamReply() string {
	return s.session.StreamReply()
}

func (s *cocreateState) draftPrompt() string {
	return s.session.DraftPrompt()
}

func (s *cocreateState) ready() bool {
	return s.session.Ready()
}

func (s *cocreateState) suggestions() []string {
	return s.session.Suggestions()
}

// appendSuggestion thêm đề xuất tương ứng phím số vào đầu vào do phím tắt tạo ra.
// Một khi người dùng sửa đầu vào, tổ hợp current và đề xuất đã chọn không còn khớp, phím số khôi phục thành nhập thông thường.
func (s *cocreateState) appendSuggestion(index int, current string) (string, bool) {
	suggestions := s.suggestions()
	if index < 0 || index >= len(suggestions) {
		return "", false
	}
	if len(s.selectedSuggestions) == 0 {
		if strings.TrimSpace(current) != "" {
			return "", false
		}
	} else if current != strings.Join(s.selectedSuggestions, "；") {
		s.resetSuggestionInput()
		return "", false
	}

	suggestion := strings.TrimSpace(suggestions[index])
	for _, selected := range s.selectedSuggestions {
		if selected == suggestion {
			return current, true
		}
	}

	s.selectedSuggestions = append(s.selectedSuggestions, suggestion)
	return strings.Join(s.selectedSuggestions, "；"), true
}

func (s *cocreateState) resetSuggestionInput() {
	s.selectedSuggestions = nil
}

func (s *cocreateState) buildPrompt() (string, error) {
	return s.session.BuildPrompt()
}

func renderStartupModeBar(width int, mode startupMode) string {
	quick := renderStartupModePill(mode == startupModeQuick, "Bắt đầu nhanh")
	cocreate := renderStartupModePill(mode == startupModeCoCreate, "Quy hoạch đồng sáng tạo")
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("Chế độ khởi động")
	divider := lipgloss.NewStyle().
		Foreground(colorDim).
		Render("·")
	line := title + " " + divider + " " + quick + "  " + cocreate
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(line)
}

func renderStartupModePill(active bool, label string) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if active {
		style = style.Foreground(lipgloss.Color("#1c1a14")).Background(colorAccent).Bold(true)
	} else {
		style = style.Foreground(colorMuted)
	}
	return style.Render(label)
}

// coCreateColumns chia nội dung modal thành hai cột trái phải.
// Cột trái chứa thoại và ô nhập (xếp chồng), cột phải chứa bản thảo lệnh sáng tác; tổng bằng chiều rộng nội dung modal.
func coCreateColumns(bodyW int) (leftW, rightW int) {
	leftW = bodyW * 58 / 100
	if leftW < 42 {
		leftW = bodyW / 2
	}
	rightW = bodyW - leftW
	if rightW < 28 {
		rightW = 28
		leftW = bodyW - rightW
	}
	return leftW, rightW
}

func renderCoCreateBody(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int) string {
	if state == nil {
		return ""
	}
	leftW, rightW := coCreateColumns(width)

	// border phải do leftCol ngoài cùng vẽ, xuyên suốt từ đỉnh đến đáy body; conversation / suggestions /
	// input đều không vẽ border phải riêng. input vẫn là khung bo tròn hoàn chỉnh, margin trái phải 1 cột
	// thẳng hàng padding của conversation, trông đều khoảng cách với hai viền.
	// Trong chế độ đồng sáng tạo textarea cố định 1 dòng (xem model.refitTextareaHeight),
	// chiều cao input = 1 (textarea) + 2 (top/bottom border) = 3 dòng, không bao giờ trôi.
	innerW := leftW - 1 // để 1 cột cho viền phải ngoài cùng

	inputBox := lipgloss.NewStyle().
		Width(innerW-6). // -2 margin -2 padding -2 border
		Border(baseBorder).
		BorderForeground(colorDim).
		Padding(0, 1).
		Margin(0, 1).
		Render(inputView)

	suggestionsBox := renderCoCreateSuggestions(innerW, state)
	suggestionsH := 0
	if suggestionsBox != "" {
		suggestionsH = lipgloss.Height(suggestionsBox)
	}

	convH := height - lipgloss.Height(inputBox) - suggestionsH
	if convH < 4 {
		convH = 4
	}

	convPanel := renderCoCreateConversationPanel(innerW, convH, state, errMsg, spinnerFrame)

	var stack string
	if suggestionsBox == "" {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, inputBox)
	} else {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, suggestionsBox, inputBox)
	}

	leftCol := lipgloss.NewStyle().
		Border(baseBorder, false, true, false, false).
		BorderForeground(colorDim).
		Render(stack)

	rightPanel := renderCoCreatePromptPanel(rightW, height, state)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightPanel)
}

// extractReplyForDisplay cắt đoạn <reply>...</reply> từ nội dung lịch sử assistant.
// Các tag khác (<draft>/<ready>/<suggestions>) là trường giao thức cho model lượt sau, không nên phơi bày cho người dùng.
// Khi model tuân thủ một nửa (thiếu tag mở <reply>), từ đầu đến </reply> hoặc tag mở tiếp theo đều tính là reply.
// Khi hoàn toàn không có tag (đường dẫn dự phòng) trả về nguyên gốc.
func extractReplyForDisplay(content string) string {
	rest := content
	if rIdx := strings.Index(content, "<reply>"); rIdx >= 0 {
		rest = content[rIdx+len("<reply>"):]
	}
	if cIdx := strings.Index(rest, "</reply>"); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	cut := len(rest)
	for _, mark := range []string{"<draft>", "<ready>", "<suggestions>"} {
		if idx := strings.Index(rest, mark); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(rest) && !strings.Contains(content, "<") {
		return content
	}
	return strings.TrimSpace(rest[:cut])
}

// renderCoCreateSuggestions render dòng đề xuất AI trên input. Khi awaiting hoặc không có đề xuất
// trả về chuỗi rỗng để layout tự thu gọn không để lại dòng trống. Tối đa 3 đề xuất, chọn bằng phím 1/2/3.
func renderCoCreateSuggestions(width int, state *cocreateState) string {
	if state == nil || state.awaiting {
		return ""
	}
	sugs := state.suggestions()
	if len(sugs) == 0 {
		return ""
	}
	if len(sugs) > 3 {
		sugs = sugs[:3]
	}

	digits := []string{"❶", "❷", "❸"}
	digitStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(colorMuted)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	lines := []string{hintStyle.Render("Đề xuất của AI (nhấn 1/2/3 để ghép, có thể sửa rồi gửi):")}
	for i, s := range sugs {
		lines = append(lines, digitStyle.Render(digits[i]+" ")+bodyStyle.Render(strings.TrimSpace(s)))
	}

	// Căn chỉnh trái phải với inputBox: trái 2 cột (margin1+padding1), phải tương tự.
	return lipgloss.NewStyle().
		Width(width-2).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
}

func coCreateModalSize(width, height int) (boxW, boxH int) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 24
	}
	boxW = min(max(width*76/100, 88), width-4)
	boxH = min(max(height*72/100, 22), height-4)
	if boxW < 64 {
		boxW = max(width-2, 42)
	}
	if boxH < 14 {
		boxH = max(height-2, 12)
	}
	return boxW, boxH
}

// coCreateInputWidth tính chiều rộng ký tự thực tế có thể nhập của textarea.
// Trang trí cột trái: viền phải ngoài cùng 1 + margin input 2 + border 2 + padding 2 = 7 cột;
// prompt+cursor của textarea chiếm 2 cột; nên textareaW = leftW - 9.
func coCreateInputWidth(width, height int) int {
	boxW, _ := coCreateModalSize(width, height)
	bodyW := boxW - 4
	leftW, _ := coCreateColumns(bodyW)
	inputW := leftW - 9
	if inputW < 20 {
		inputW = 20
	}
	return inputW
}

func renderCoCreateModal(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int, quitPending bool) string {
	if state == nil {
		return ""
	}

	boxW, boxH := coCreateModalSize(width, height)

	// title / subtitle / hint đặt ngoài modal (giữa trên và dưới), để bên trong modal
	// hoàn toàn dành cho body —— viền dọc cột trái và cột phải xuyên từ đỉnh modal xuống đáy.
	// modal chiếm thực tế = boxH (content) + 2 (padding 1*2) + 2 (border) = boxH+4 dòng;
	// Tổng stack = title(1) + subtitle(1) + rỗng(1) + modal(boxH+4) + rỗng(1) + hint(1) = boxH+9.
	// Do đó trừ 5 dòng của boxH cho trang trí ngoài modal để tránh tràn terminal.
	contentH := boxH - 5
	if contentH < 10 {
		contentH = 10
	}

	titleText, subtitleText := "Quy hoạch đồng sáng tạo", "Nói rõ yêu cầu trước, rồi bắt đầu sáng tác"
	if state.stage {
		titleText, subtitleText = "Đồng sáng tạo theo giai đoạn", "Quy hoạch hướng đi tiếp theo, rồi tiếp tục sáng tác"
	}
	headerStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	title := headerStyle.Foreground(colorMuted).Bold(true).Render(titleText)
	subtitle := headerStyle.Foreground(colorDim).Italic(true).Render(subtitleText)

	var hintLine string
	hintStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	if quitPending {
		// quitPending giống với inputHints(); nếu không modal đồng sáng tạo che thanh dưới, người dùng không thấy "nhấn Ctrl+C lần nữa".
		hintLine = hintStyle.Foreground(lipgloss.Color("243")).Bold(true).Render("Press Ctrl+C again to exit")
	} else {
		hintLine = hintStyle.Foreground(colorDim).Italic(true).Render(coCreateHint(state))
	}

	body := renderCoCreateBody(boxW-4, contentH, state, errMsg, inputView, spinnerFrame)
	box := lipgloss.NewStyle().
		Width(boxW).
		Height(contentH).
		Border(baseBorder).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Render(body)

	stack := lipgloss.JoinVertical(lipgloss.Center, title, subtitle, "", box, "", hintLine)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, stack)
}

// coCreateHint tạo gợi ý phím ngắn gọn theo trạng thái, tránh trùng lặp ngữ nghĩa với placeholder.
func coCreateHint(state *cocreateState) string {
	switch {
	case state == nil:
		return "Enter Gửi · Esc Thoát"
	case state.awaiting:
		return "AI đang trả lời · ↑↓ Cuộn thoại · Lăn chuột cuộn lệnh · Esc Thoát"
	case state.canStart():
		action := "Ctrl+S Bắt đầu sáng tác"
		if state.stage {
			action = "Ctrl+S Áp dụng và tiếp tục"
		}
		return "Enter Tiếp tục bổ sung · " + action + " · ↑↓ Cuộn thoại · Lăn chuột cuộn lệnh · Esc Thoát"
	default:
		return "Enter Gửi · ↑↓ Cuộn thoại · Lăn chuột cuộn lệnh · Esc Thoát"
	}
}

func renderCoCreateConversationPanel(width, height int, state *cocreateState, errMsg string, spinnerFrame int) string {
	// Không vẽ border riêng —— viền dọc do leftCol ngoài cùng vẽ.
	// Tổng rộng = width; style.Width = contentW = width-2; sau Padding(0,1) vùng nội dung = contentW-2.
	// Trong dòng phải trừ "▌ " / "  " tiền tố 2 cột, nếu không wrap xong mỗi dòng + tiền tố sẽ tràn vùng 2 cột,
	// gây xuống dòng vật lý của terminal —— lipgloss vẫn coi modal cao cố định, nhưng terminal vẽ cao hơn,
	// khi stream thinking liên tục gây "rung viền". Nên wrapW = contentW - 4.
	contentW := width - 2
	if contentW < 12 {
		contentW = 12
	}
	wrapW := max(12, contentW-4)

	userRole := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Render("Bạn")
	aiRole := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("AI")
	userBody := lipgloss.NewStyle().Foreground(colorAccent2)
	aiBody := lipgloss.NewStyle().Foreground(bodyTextColor)
	thinkingStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	thinkingTag := lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("AI suy nghĩ")

	sysStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	var lines []string
	for i, item := range state.session.History() {
		isUser := item.Role != "assistant"
		// Câu mở đầu tổng hợp của đồng sáng tạo theo giai đoạn (luôn là tin nhắn user history[0]) hiển thị dưới dạng dòng hệ thống trung lập,
		// không giả làm người dùng nhập; nó vẫn được gửi cho LLM ở lượt kickoff user.
		if isUser && state.stage && i == 0 {
			for j, line := range wrapStreamText(stageCoCreateSystemLine, wrapW) {
				prefix := "· "
				if j > 0 {
					prefix = "  "
				}
				lines = append(lines, sysStyle.Render(prefix+line))
			}
			lines = append(lines, "")
			continue
		}
		if isUser {
			lines = append(lines, userRole)
			for _, line := range wrapStreamText(strings.TrimSpace(item.Content), wrapW) {
				// Render toàn bộ dòng một lần để tránh bleed ký tự ANSI ở chỗ nối màu tiền tố reset và màu chính văn.
				lines = append(lines, userBody.Render("▌ "+line))
			}
		} else {
			lines = append(lines, aiRole)
			// trong history, assistant lưu toàn bộ 4 phần Raw (dùng làm ngữ cảnh cho model), UI chỉ hiển thị phần [REPLY].
			display := extractReplyForDisplay(item.Content)
			for _, line := range wrapStreamText(strings.TrimSpace(display), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
		}
		lines = append(lines, "")
	}

	if state.awaiting {
		if t := state.session.StreamThinking(); t != "" {
			lines = append(lines, thinkingTag)
			for _, line := range wrapStreamText(t, wrapW) {
				lines = append(lines, thinkingStyle.Render("  "+line))
			}
			lines = append(lines, "")
		}
		if state.streamReply() != "" {
			lines = append(lines, aiRole)
			for _, line := range wrapStreamText(state.streamReply(), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
			lines = append(lines, "")
		}
		// trang trí sparkle: để người dùng luôn thấy "AI đang làm việc"
		lines = append(lines, strings.TrimLeft(renderEventSparkle(spinnerFrame, contentW), " "))
	}
	if errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Render("! "+errMsg))
	}

	// Dùng viewport thay vì truncate thủ công, để người dùng cuộn xem lại.
	// vp cao = panel cao - 1 dòng tiêu đề. Sau SetContent nếu người dùng ở đáy,
	// tự động cuộn đến mới nhất (stream cuộn theo); người dùng cuộn lên thì tắt convFollow ngừng cuộn.
	vpH := height - 1
	if vpH < 1 {
		vpH = 1
	}
	if state.convVP.Width != contentW || state.convVP.Height != vpH {
		state.convVP.Width = contentW
		state.convVP.Height = vpH
	}
	state.convVP.SetContent(strings.Join(lines, "\n"))
	if state.convFollow {
		state.convVP.GotoBottom()
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)
	return style.Render(panelTitleStyle.Render(":: Cuộc thoại đồng sáng tạo") + "\n" + state.convVP.View())
}

func renderCoCreatePromptPanel(width, height int, state *cocreateState) string {
	readyLabel := "Đã có thể bắt đầu sáng tác"
	if state.stage {
		readyLabel = "Đã có thể áp dụng và tiếp tục"
	}
	status := lipgloss.NewStyle().Foreground(colorDim).Render("Đang tiếp tục đối thoại")
	if state.ready() {
		status = lipgloss.NewStyle().Foreground(colorAccent).Render(readyLabel)
	}
	if state.awaiting {
		status = lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("AI đang tổng hợp")
	}

	// Vùng nội dung = Tổng cột - 2 (padding 0,1 chiếm 2 cột, không có border).
	contentW := width - 2
	if contentW < 8 {
		contentW = 8
	}

	emptyHint := "AI sẽ liên tục tổng hợp ra lệnh cuối cùng có thể trực tiếp đưa vào sáng tác ở đây."
	panelTitle := ":: Lệnh sáng tác hiện tại"
	if state.stage {
		emptyHint = "AI sẽ liên tục tổng hợp bản tóm tắt hướng đi giai đoạn tiếp theo ở đây."
		panelTitle = ":: Hướng đi tiếp theo"
	}
	text := strings.TrimSpace(state.draftPrompt())
	if text == "" {
		text = lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(emptyHint)
	} else {
		text = renderMarkdownPreview(text, max(12, contentW-2))
	}
	vpHeight := height - 5
	if vpHeight < 3 {
		vpHeight = 3
	}
	if state.promptVP.Width != contentW || state.promptVP.Height != vpHeight {
		state.promptVP.Width = contentW
		state.promptVP.Height = vpHeight
	}
	state.promptVP.MouseWheelEnabled = true
	state.promptVP.SetContent(text)

	hint := ""
	if state.promptVP.TotalLineCount() > state.promptVP.VisibleLineCount() {
		switch {
		case state.promptVP.AtTop():
			hint = "↓ Vẫn còn nội dung bên dưới, cuộn chuột hoặc PgDn để xem"
		case state.promptVP.AtBottom():
			hint = "↑ Vẫn còn nội dung bên trên, cuộn chuột hoặc PgUp để xem"
		default:
			hint = "↑↓ Có thể tiếp tục cuộn để xem"
		}
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)

	body := panelTitleStyle.Render(panelTitle) + "\n" + status + "\n\n" + state.promptVP.View()
	if hint != "" {
		body += "\n\n" + lipgloss.NewStyle().
			Width(contentW).
			AlignHorizontal(lipgloss.Center).
			Foreground(colorDim).
			Italic(true).
			Render(hint)
	}
	return style.Render(body)
}

func renderMarkdownPreview(text string, width int) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	h1Style := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	h3Style := lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	codeStyle := lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	var out []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			out = append(out, "")
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			out = append(out, h1Style.Render(title))
		case strings.HasPrefix(line, "## "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			out = append(out, h2Style.Render(title))
		case strings.HasPrefix(line, "### "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			out = append(out, h3Style.Render(title))
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			body := strings.TrimSpace(line[2:])
			wrapped := wrapStreamText(body, max(8, width-4))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render("• ")+cardContentStyle.Render(item))
				} else {
					out = append(out, "  "+cardContentStyle.Render(item))
				}
			}
		case isOrderedMarkdownItem(line):
			prefix, body := splitOrderedMarkdownItem(line)
			wrapped := wrapStreamText(body, max(8, width-len(prefix)-2))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render(prefix+" ")+cardContentStyle.Render(item))
				} else {
					out = append(out, strings.Repeat(" ", len(prefix)+1)+cardContentStyle.Render(item))
				}
			}
		case strings.HasPrefix(line, "> "):
			body := strings.TrimSpace(strings.TrimPrefix(line, "> "))
			for _, item := range wrapStreamText(body, max(8, width-4)) {
				out = append(out, codeStyle.Render("│ "+item))
			}
		default:
			for _, item := range wrapStreamText(line, width) {
				out = append(out, cardContentStyle.Render(item))
			}
		}
	}
	return strings.Join(out, "\n")
}

func isOrderedMarkdownItem(line string) bool {
	if len(line) < 3 {
		return false
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func splitOrderedMarkdownItem(line string) (prefix, body string) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return "", strings.TrimSpace(line)
	}
	return line[:i+1], strings.TrimSpace(line[i+2:])
}
