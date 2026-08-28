package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/utils"
)

const maxEvents = 500

// maxStreamRounds giới hạn số vòng giữ lại của panel stream. Mỗi LLM call kết thúc kích hoạt một streamClear
// mở vòng mới, mỗi chương writer khoảng 3~5 vòng (agent header / suy nghĩ / draft / commit), 32 vòng xấp xỉ bằng
// xem lại đầu ra stream của 6~10 chương gần nhất. Phần chính của chương đã commit được lưu vào store/drafts, vượt quá thì bỏ để tránh
// mỗi token delta kích hoạt O(toàn văn) render lại. Giới hạn bộ nhớ trạng thái ổn định khoảng 512KB, thấp hơn nhiều so với ngưỡng giật lag.
const maxStreamRounds = 32

type focusPane int

const (
	focusEvents focusPane = iota
	focusStream
	focusDetail
	focusState // Thanh bên trạng thái bên trái (có thể cuộn)

	focusPaneCount // Tổng số tiêu điểm, dùng cho Tab xoay vòng
)

type appMode int

const (
	modeNew     appMode = iota // Chờ người dùng nhập yêu cầu tiểu thuyết
	modeRunning                // Đang sáng tác (bao gồm dừng do lỗi, nhập để khôi phục)
	modeDone                   // Sáng tác hoàn thành
)

// Chuỗi frame spinner dùng chung cho thanh trên cùng / hoạt động stream (bubbles.Spinner.MiniDot).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Chuỗi frame spinner chuyên dụng cho dòng "Đang chạy" của luồng sự kiện (bubbles.Spinner.Dot).
// 7 dấu chấm + 1 khoảng trống xoay theo chiều kim đồng hồ quanh lưới 3x3, nhìn giống một vòng tròn tải hoàn chỉnh.
// Dùng chỉ số frame độc lập + tick nhanh hơn, không ảnh hưởng đến nhịp độ của thanh trên cùng và animation ngôi sao.
var toolSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model là trạng thái trên cùng của TUI.
type Model struct {
	runtime        *host.Host
	cocreate       *cocreateState
	help           *helpState
	modelSwitch    *modelSwitchState
	modelConfig    *modelConfigState
	report         *reportState
	version        string
	importer       *importState
	importSeq      int
	simulator      *simulationState
	simSeq         int
	compItems      []commandPaletteItem
	compIdx        int
	compActive     bool
	commandToken   string // token lệnh đã đăng ký hiện tại; chỉ render phần đó, không tô màu tham số
	snapshot       host.UISnapshot
	events         []host.Event
	eventIndex     map[string]int   // event.ID → chỉ số m.events; sự kiện dạng gọi đến cập nhật tại chỗ
	viewport       viewport.Model   // viewport luồng sự kiện
	streamVP       viewport.Model   // viewport đầu ra stream
	detailVP       viewport.Model   // viewport chi tiết bên phải
	stateVP        viewport.Model   // viewport thanh bên trạng thái bên trái (có thể cuộn)
	streamBuf      *strings.Builder // bộ đệm tích lũy văn bản stream
	streamRounds   []string
	textarea       textarea.Model
	width          int
	height         int
	autoScroll     bool
	streamScroll   bool      // panel stream tự động bám theo
	streamDirty    bool      // streamRounds có delta chưa refresh
	flushPending   bool      // đã lên lịch refresh stream một lần, tránh mỗi delta khởi động lại timer
	lastKeyAt      time.Time // thời gian nhấn phím không phải Enter lần cuối; điều tiết KeyEnter chống dán luồng \n kích hoạt nộp nhầm
	inputHistory   []string  // lịch sử nhập liệu đã nộp (khử trùng lặp: liền kề không trùng lặp)
	historyIdx     int       // chỉ số duyệt hiện tại; == len(inputHistory) có nghĩa là "chưa duyệt, đang sửa bản thảo"
	historyDraft   string    // bản thảo được lưu trước khi vào duyệt lịch sử, khôi phục khi quay lại cuối
	focusPane      focusPane
	hoverPane      focusPane
	hoverActive    bool
	mode           appMode
	starting       bool // UI đã vào bàn làm việc, Host đang thực hiện khởi tạo lúc khởi động
	startupMode    startupMode
	importHint     string // gợi ý phát hiện nhập chưa hoàn thành khi khởi động (hiển thị trên màn hình chào mừng; xóa sau khi bắt đầu nhập)
	cocreateSeq    int
	reportSeq      int
	err            error
	spinnerIdx     int
	toolSpinnerIdx int  // chỉ số frame độc lập của dòng đang chạy trong luồng sự kiện (tick 150ms, không ảnh hưởng đến thanh trên/sao)
	toolTicking    bool // đã khởi động timer animation công cụ; tự động dừng khi không có sự kiện đang chạy
	cursorIdx      int  // chỉ số frame con trỏ stream (tiến theo animation chính)
	streamRound    int  // đếm số vòng đầu ra stream
	quitPending    bool // nhấn đúp Ctrl+C xác nhận thoát
	abortPending   bool // chờ Done trả về khi tạm dừng thủ công
	mouseOff       bool // khi true đã vô hiệu hóa báo cáo chuột, để người dùng tự do kéo thả chọn copy; chuyển đổi lại để khôi phục
}

// NewModel tạo TUI Model.
func NewModel(rt *host.Host, version string) Model {
	ta := textarea.New()
	ta.Placeholder = placeholderForNewMode(startupModeQuick)
	ta.CharLimit = 5000
	ta.SetHeight(1)
	// MaxHeight=6 làm cho đầu vào siêu dài tự động wrap theo chiều rộng và hiển thị thành nhiều dòng (giới hạn hình ảnh 6 dòng).
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.Focus()

	// Mặc định Enter không ngắt dòng (nộp bởi handleEnterKey);
	// Chủ động ngắt dòng được gán lại vào ctrl+j (unix \n) và alt+enter (thói quen GUI).
	// Lớp giao thức terminal không phân biệt được Shift+Enter và Enter, nên không hỗ trợ Shift+Enter.
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")

	vp := viewport.New(80, 20)
	vp.SetContent("")

	svp := viewport.New(80, 10)
	svp.SetContent("")

	dvp := viewport.New(40, 20)
	dvp.SetContent("")

	stvp := viewport.New(32, 20)
	stvp.SetContent("")

	// Khi khởi động kiểm tra nhập chưa hoàn thành một lần (LoadState tính lại digest công cụ, không vào vòng lặp snapshot);
	// Sách dở dang nếu không chủ động báo, người dùng chỉ phát hiện khi sáng tác bị từ chối ở cửa ải (RFC §18.2).
	importHint := ""
	if rt != nil {
		importHint = rt.ImportResumeHint()
	}

	return Model{
		runtime:      rt,
		version:      strings.TrimSpace(version),
		autoScroll:   true,
		streamScroll: true,
		mode:         modeNew,
		startupMode:  startupModeQuick,
		importHint:   importHint,
		textarea:     ta,
		viewport:     vp,
		streamVP:     svp,
		detailVP:     dvp,
		stateVP:      stvp,
		streamBuf:    &strings.Builder{},
		eventIndex:   make(map[string]int),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		listenEvents(m.runtime),
		listenDone(m.runtime),
		listenStream(m.runtime),
		tickSnapshot(m.runtime),
		bootstrapRuntime(m.runtime),
		tickSpinner(),
	)
}

func (m *Model) paneAtMouse(x, y int) (focusPane, bool) {
	if m.width == 0 || m.height == 0 {
		return focusEvents, false
	}

	topH, _, bodyH := m.layoutHeights()
	if bodyH < 1 {
		return focusEvents, false
	}

	bodyStartY := topH
	bodyEndY := topH + bodyH
	if y < bodyStartY || y >= bodyEndY {
		return focusEvents, false
	}

	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	centerStartX := leftW
	rightStartX := m.width - rightW

	if x >= rightStartX {
		return focusDetail, true
	}
	if x < centerStartX {
		return focusState, true
	}

	eventH, _ := m.splitHeights(bodyH)
	if y-bodyStartY < eventH {
		return focusEvents, true
	}
	return focusStream, true
}

func (m *Model) paneHighlighted(pane focusPane) bool {
	if m.focusPane == pane {
		return true
	}
	return m.hoverActive && m.hoverPane == pane
}

// hasRunningEvent kiểm tra xem có sự kiện dạng gọi nào chưa hoàn thành (spinner vẫn quay) hay không.
// toolSpinnerTick dùng điều này để xác định xem có đáng render lại không: khi không có sự kiện running, frame spinner không ảnh hưởng đến đầu ra,
// toàn bộ refreshEventViewport chắc chắn là việc vô ích.
func (m *Model) hasRunningEvent() bool {
	for i := range m.events {
		if m.events[i].Running() {
			return true
		}
	}
	return false
}

// flushStreamIfDirty render các streamRounds đã tích lũy lên viewport; đánh dấu là đã refresh.
// Trả về xem có thực sự đã refresh hay không, tiện cho bên gọi quyết định có nên GotoBottom hay không.
func (m *Model) flushStreamIfDirty() bool {
	if !m.streamDirty {
		return false
	}
	m.refreshStreamViewport()
	m.streamDirty = false
	return true
}

// refreshEventViewport render lại nội dung luồng sự kiện và đặt viewport.
func (m *Model) refreshEventViewport() {
	centerW := m.eventFlowWidth()
	content := renderEventContent(m.events, centerW, m.toolSpinnerIdx)
	snap := m.snapshot
	if m.starting {
		snap.IsRunning = true
	}
	if activity := renderEventActivity(snap, m.spinnerIdx, centerW); activity != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n" + activity
		} else {
			content = activity
		}
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshStreamViewport() {
	cursor := ""
	if m.snapshot.IsRunning {
		cursor = renderStreamCursor(m.cursorIdx)
	}
	m.streamVP.SetContent(renderStreamContent(m.streamRounds, m.streamVP.Width, cursor))
}

func (m *Model) refreshDetailViewport() {
	rightW := m.detailWidth()
	if rightW <= 4 {
		return
	}
	m.detailVP.SetContent(renderDetailContent(m.snapshot, rightW-4))
}

// refreshStateViewport refresh nội dung thanh bên trạng thái bên trái lên viewport.
// Nội dung thanh bên hoàn toàn phái sinh từ snapshot, do đó phải refresh lại mỗi khi snapshot hoặc kích thước thay đổi.
func (m *Model) refreshStateViewport() {
	leftW := m.sidebarWidth()
	if leftW <= 4 {
		return
	}
	m.stateVP.SetContent(renderStateContent(m.snapshot, leftW-4))
}

// updateViewportSize cập nhật kích thước viewport dựa trên kích thước cửa sổ hiện tại.
func (m *Model) updateViewportSize() {
	centerW := m.eventFlowWidth()
	rightW := m.detailWidth()
	bodyH := m.bodyHeight()
	eventH, streamH := m.splitHeights(bodyH)
	m.viewport.Width = centerW - 2
	m.viewport.Height = eventH - 1 // -1 cho dòng header panel sự kiện
	m.streamVP.Width = centerW - 2
	m.streamVP.Height = streamH - 1 // -1 cho dòng header panel stream
	m.detailVP.Width = rightW - 2
	m.detailVP.Height = bodyH
	leftW := m.sidebarWidth()
	m.stateVP.Width = max(1, leftW-2)
	m.stateVP.Height = max(1, bodyH-1) // -1 cho khoảng trống trên cùng, dòng dưới cùng trực tiếp hiển thị nội dung
	// Sau khi chiều cao hoặc nội dung ngắn lại, hai cột trái phải cuộn tự do có thể dừng ở mức lệch vượt quá giới hạn (SetContent
	// của bubbles chỉ chống vượt quá dòng cuối), viewport sẽ lấp đầy phần dưới bằng các dòng trống. SetYOffset tự kẹp lại.
	m.stateVP.SetYOffset(m.stateVP.YOffset)
	m.detailVP.SetYOffset(m.detailVP.YOffset)
}

// splitHeights tính toán phân bổ chiều cao cho luồng sự kiện và đầu ra stream.
func (m *Model) splitHeights(bodyH int) (eventH, streamH int) {
	eventH = bodyH * 40 / 100
	if eventH < 3 {
		eventH = 3
	}
	streamH = bodyH - eventH - 1 // -1 cho đường phân cách
	if streamH < 3 {
		streamH = 3
	}
	return
}

func (m *Model) inputWidth() int {
	if m.width == 0 {
		return 60
	}
	return m.width - 6 // border + padding + dấu nhắc "❯ "
}

func (m *Model) currentInputWidth() int {
	if m.cocreate != nil {
		return coCreateInputWidth(m.width, m.height)
	}
	return m.inputWidth()
}

// refitTextareaHeight ước lượng số dòng hiển thị theo nội dung hiện tại, động SetHeight.
// Dòng hiển thị = tổng số đoạn văn bản sau khi ngắt dòng (chia cắt bởi \n) và wrap theo chiều rộng. Cùng với MaxHeight=6
// hiện thực hóa "nội dung siêu dài/ngắt dòng chủ động tự động hiển thị nhiều dòng, tối đa 6 dòng".
func (m *Model) refitTextareaHeight() {
	w := m.textarea.Width()
	if w <= 0 {
		return
	}
	// Ở chế độ đồng sáng tác, input cố định 1 dòng: nội dung nhiều dòng của textarea sẽ được chính textarea cuộn hiển thị theo
	// con trỏ. Nếu không inputBox chiều cao thay đổi theo nội dung sẽ làm cột conversation bên trái co lại,
	// input trôi nổi theo chiều dọc, phá vỡ tính ổn định của layout.
	if m.cocreate != nil {
		m.textarea.SetHeight(1)
		return
	}
	text := m.textarea.Value()
	if text == "" {
		m.textarea.SetHeight(1)
		return
	}
	// Trừ đi 2 cột dư (biểu tượng prompt bên trong textarea + con trỏ), chênh lệch 1 dòng là có thể chấp nhận.
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	total := 0
	for line := range strings.SplitSeq(text, "\n") {
		lw := lipgloss.Width(line)
		if lw == 0 {
			total++
			continue
		}
		total += (lw + contentW - 1) / contentW
	}
	if total < 1 {
		total = 1
	}
	m.textarea.SetHeight(total) // SetHeight bên trong dùng MaxHeight để kẹp (clamp)
}

// resizeTextarea đồng bộ thiết lập chiều rộng và chiều cao dựa trên nội dung.
// Thay thế cho các lệnh gọi SetWidth(currentInputWidth()) nằm rải rác, đảm bảo chiều cao thay đổi theo khi chiều rộng thay đổi.
func (m *Model) resizeTextarea() {
	m.textarea.SetWidth(m.currentInputWidth())
	m.refitTextareaHeight()
}

// maxInputHistory giới hạn độ dài lịch sử để tránh phình bộ nhớ trong các phiên dài.
const maxInputHistory = 200

// pushInputHistory thêm nội dung đã nộp thành công vào lịch sử, khử trùng lặp liền kề. Đồng bộ đặt lại chỉ số duyệt.
func (m *Model) pushInputHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
		if len(m.inputHistory) > maxInputHistory {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// tryHistoryUp lùi về một mục lịch sử cũ hơn; trả về xem có xử lý thao tác nhấn phím không.
// Lần đầu vào duyệt lịch sử sẽ lưu nội dung textarea hiện tại thành bản thảo (draft), khôi phục khi quay lại cuối.
// Bên gọi cần tự xác định trong tình huống nhiều dòng có nên bỏ qua hay không (để textarea xử lý di chuyển con trỏ trong dòng).
func (m *Model) tryHistoryUp() bool {
	if len(m.inputHistory) == 0 || m.historyIdx <= 0 {
		return false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.textarea.Value()
	}
	m.historyIdx--
	m.textarea.SetValue(m.inputHistory[m.historyIdx])
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// tryHistoryDown tiến đến một mục lịch sử mới hơn; khi đến cuối khôi phục draft.
func (m *Model) tryHistoryDown() bool {
	if m.historyIdx >= len(m.inputHistory) {
		return false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.textarea.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.textarea.SetValue(m.inputHistory[m.historyIdx])
	}
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// textareaIsMultiline nội dung textarea hiện tại có chứa ngắt dòng chủ động hay không; dùng để quyết định ↑↓ là duyệt lịch sử hay di chuyển trong dòng.
func (m *Model) textareaIsMultiline() bool {
	return strings.Contains(m.textarea.Value(), "\n")
}

// inputHints tạo văn bản gợi ý phía dưới dựa trên trạng thái hiện tại.
// Thống nhất thêm copySuffix ở cuối, để người dùng ở bất kỳ trạng thái không khẩn cấp nào cũng thấy cách bôi đen copy;
// khi chuột đã tắt thì hiện gợi ý chữ đỏ nổi bật, nhắc nhở nhấn phím lại để khôi phục tương tác chuột.
func (m *Model) inputHints() string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	if m.quitPending {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Bold(true).Render("Press Ctrl+C again to exit")
	}
	limitHint := m.inputLimitHint()
	// Trang chào mừng (modeNew) không bật báo cáo chuột, chỉ cần kéo thả bằng tính năng mặc định của terminal là có thể copy, không cần nhắc Ctrl+R;
	// Bàn làm việc mới bật báo cáo, cần dùng Ctrl+R tắt tạm thời để copy.
	suffix := limitHint + " · Ctrl+R Chuyển chế độ bôi đen copy"
	if m.mode == modeNew {
		suffix = limitHint
	}
	if m.mouseOff && m.mode != modeNew {
		// Bàn làm việc chuyển thủ công sang chế độ bôi đen copy: dùng màu nhấn để nhắc nhở đang ở chế độ "kéo thả chọn tự do", nhấn Ctrl+R để khôi phục
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("✂ Chế độ bôi đen copy: Có thể kéo thả chọn văn bản để copy · Ctrl+R Thoát khôi phục tương tác chuột")
	}
	if m.cocreate != nil {
		scrollHint := " · Tab Cuộn:Hội thoại"
		if m.cocreate.focusPrompt {
			scrollHint = " · Tab Cuộn:Lệnh sáng tác"
		}
		switch {
		case m.cocreate.awaiting:
			return dimStyle.Render("Chờ AI trả lời · Esc Thoát đồng sáng tác" + scrollHint + suffix)
		case m.cocreate.canStart():
			startLabel := "Ctrl+S Bắt đầu sáng tác"
			if m.cocreate.stage {
				startLabel = "Ctrl+S Áp dụng và tiếp tục"
			}
			return dimStyle.Render("Enter Gửi · " + startLabel + " · Esc Thoát đồng sáng tác" + scrollHint + suffix)
		default:
			return dimStyle.Render("Enter Gửi · Esc Thoát đồng sáng tác" + scrollHint + suffix)
		}
	}
	if m.mode == modeNew {
		if m.startupMode == startupModeQuick {
			return dimStyle.Render("Tab Chuyển chế độ khởi động · Gõ / Tìm lệnh · Enter Bắt đầu sáng tác ngay · Esc Xóa nhập liệu" + suffix)
		}
		return dimStyle.Render("Tab Chuyển chế độ khởi động · Gõ / Tìm lệnh · Enter Bắt đầu hội thoại đồng sáng tác · Esc Xóa nhập liệu" + suffix)
	}
	switch m.snapshot.RuntimeState {
	case "pausing":
		return dimStyle.Render("Đang tạm dừng sáng tác · Vui lòng chờ vòng hiện tại kết thúc" + suffix)
	case "paused":
		return dimStyle.Render("Gõ / Tìm lệnh · Enter Tiếp tục sáng tác · Esc Xóa nhập liệu" + suffix)
	}
	return dimStyle.Render("Gõ / Tìm lệnh · Click/Tab Chuyển panel · ↑↓ Cuộn · End Xuống đáy · Ctrl+L Xóa màn hình · Esc Tạm dừng · Enter Gửi" + suffix)
}

func (m *Model) inputLimitHint() string {
	limit := m.textarea.CharLimit
	if limit <= 0 {
		return ""
	}
	used := m.textarea.Length()
	if used < limit*4/5 {
		return ""
	}
	return fmt.Sprintf(" · Đã nhập %d/%d", used, limit)
}

func (m *Model) eventFlowWidth() int {
	if m.width == 0 {
		return 80
	}
	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	return m.width - leftW - rightW
}

func (m *Model) sidebarWidth() int {
	if m.width == 0 {
		return 32
	}
	return m.width * 23 / 100
}

func (m *Model) detailWidth() int {
	if m.width == 0 {
		return 40
	}
	return m.width * 27 / 100
}

func (m *Model) bodyHeight() int {
	_, _, bodyH := m.layoutHeights()
	return bodyH
}

func (m *Model) currentSpinnerFrame() string {
	if !m.snapshot.IsRunning && !m.starting {
		return ""
	}
	return spinnerFrames[m.spinnerIdx%len(spinnerFrames)]
}

func (m *Model) outputDir() string {
	if m.runtime == nil {
		return ""
	}
	return m.runtime.Dir()
}

func defaultSteerPlaceholder() string {
	return "Nhập can thiệp cốt truyện, ví dụ: đẩy tuyến tình cảm lên chương 4"
}

func (m *Model) syncRuntimePlaceholder() {
	if m.mode != modeRunning || m.cocreate != nil {
		return
	}
	if m.starting {
		m.textarea.Placeholder = "Đang khởi tạo sáng tác..."
		return
	}
	switch m.snapshot.RuntimeState {
	case "completed":
		m.textarea.Placeholder = donePlaceholder
	case "pausing":
		m.textarea.Placeholder = "Đang tạm dừng sáng tác..."
	case "paused":
		if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
			m.textarea.Placeholder = "Đang chờ nghiệm thu từng chương: nhập ý kiến sửa đổi, hoặc /next cho qua chương tiếp"
		} else {
			m.textarea.Placeholder = "Sáng tác đã tạm dừng, nhập nội dung bất kỳ để tiếp tục sáng tác"
		}
	default:
		if !m.snapshot.IsRunning {
			if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
				m.textarea.Placeholder = "Đang chờ nghiệm thu từng chương: nhập ý kiến sửa đổi, hoặc /next cho qua chương tiếp"
			} else {
				m.textarea.Placeholder = "Chạy bị gián đoạn, nhập nội dung bất kỳ để khôi phục sáng tác"
			}
		} else {
			m.textarea.Placeholder = defaultSteerPlaceholder()
		}
	}
}

func (m *Model) renderBottomBar() string {
	inputView := highlightCommandToken(m.textarea.View(), m.textarea.Value(), m.commandToken)
	inputBox := renderInputBox(
		inputView,
		m.inputHints(),
		m.snapshot,
		m.outputDir(),
		m.width,
	)
	if m.mode != modeNew || m.cocreate != nil {
		return inputBox
	}
	return renderStartupModeBar(m.width, m.startupMode) + "\n" + inputBox
}

func (m *Model) layoutHeights() (topH, inputH, bodyH int) {
	if m.width == 0 || m.height == 0 {
		return 1, 4, 20
	}
	topH = lipgloss.Height(renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version))
	inputH = lipgloss.Height(m.renderBottomBar())
	bodyH = m.height - topH - inputH
	if bodyH < 3 {
		bodyH = 3
	}
	return
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Đang tải..."
	}
	if m.width < 100 {
		return lipgloss.NewStyle().
			Width(m.width).Height(m.height).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render("Độ rộng terminal không đủ, vui lòng mở rộng tối thiểu 100 cột")
	}
	if m.cocreate != nil {
		return renderCoCreateModal(m.width, m.height, m.cocreate, errorText(m.err), m.textarea.View(), m.spinnerIdx, m.quitPending)
	}
	if m.help != nil {
		return renderHelpModal(m.width, m.height, m.help)
	}
	if m.report != nil {
		return renderReportModal(m.width, m.height, m.report)
	}
	if m.importer != nil {
		// Nhập không phụ thuộc trạng thái chạy của Engine, frame animation lấy trực tiếp từ spinnerIdx (currentSpinnerFrame sẽ trả về rỗng khi engine ngừng).
		return renderImportModal(m.width, m.height, m.importer, m.spinnerIdx)
	}
	if m.simulator != nil {
		return renderSimulationModal(m.width, m.height, m.simulator)
	}

	topBar := renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version)
	inputBox := m.renderBottomBar()
	_, inputH, bodyH := m.layoutHeights()

	var body string
	if m.mode == modeNew {
		errMsg := ""
		if m.err != nil {
			errMsg = m.err.Error()
		}
		body = renderWelcome(m.width, bodyH, errMsg, m.startupMode, m.importHint)
	} else {
		leftW := m.sidebarWidth()
		rightW := m.detailWidth()
		centerW := m.width - leftW - rightW
		eventH, streamH := m.splitHeights(bodyH)

		if m.viewport.Width != centerW-2 || m.viewport.Height != eventH-1 {
			m.viewport.Width = centerW - 2
			m.viewport.Height = eventH - 1 // -1 cho dòng header panel sự kiện
		}
		if m.streamVP.Width != centerW-2 || m.streamVP.Height != streamH-1 {
			m.streamVP.Width = centerW - 2
			m.streamVP.Height = streamH - 1 // -1 cho dòng header panel stream
		}

		eventFlow := renderEventFlowViewport(m.viewport, centerW, eventH, m.paneHighlighted(focusEvents))
		streamPanel := renderStreamPanel(m.streamVP, centerW, streamH, m.paneHighlighted(focusStream), m.snapshot.IsRunning || m.starting, m.spinnerIdx)
		center := lipgloss.JoinVertical(lipgloss.Left, eventFlow, streamPanel)

		left := renderStatePanel(m.stateVP, leftW, bodyH, m.paneHighlighted(focusState))
		right := renderDetailPanel(m.detailVP, rightW, bodyH, m.paneHighlighted(focusDetail))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, topBar, body, inputBox)

	// Xếp chồng popup lên trên: nổi lên trên phần đáy của body, không ảnh hưởng đến layout
	if m.modelSwitch != nil {
		commandBar := renderModelSwitchBar(m.width, m.modelSwitch)
		view = overlayAboveInput(view, commandBar, inputH)
	} else if m.modelConfig != nil {
		view = overlayAboveInput(view, renderModelConfigModal(m.width, m.modelConfig), inputH)
	} else if m.compActive {
		commandBar := renderCommandPalette(m.width, m.compItems, m.compIdx)
		view = overlayAboveInput(view, commandBar, inputH)
	}
	return view
}

// sendCoCreate khởi tạo một vòng yêu cầu đồng sáng tác, xử lý thống nhất reqID, textarea, placeholder.
func (m *Model) sendCoCreate() tea.Cmd {
	m.cocreateSeq++
	m.cocreate.reqID = m.cocreateSeq
	m.cocreate.awaiting = true
	m.resizeTextarea()
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	m.textarea.Blur()
	return runCoCreate(m.runtime, m.cocreate)
}

func (m Model) handleCoCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil {
		return m, nil
	}
	state := m.cocreate

	// Phím ↑↓/PgUp/PgDn/Home/End cuộn; Tab chuyển đổi tiêu điểm cuộn giữa cột hội thoại trái ↔ cột lệnh sáng tác phải
	// (mặc định cột trái, nơi người dùng thường xem lại). Trang chào mừng đã tắt báo cáo chuột để giữ tính năng bôi đen copy mặc định, khi cột phải tràn nội dung thì dựa vào Tab
	// chuyển tiêu điểm rồi cuộn bằng phím. Cột trái: cuộn lên sẽ tắt follow, cuộn xuống đáy sẽ bật lại follow (bám theo dòng).
	switch msg.Type {
	case tea.KeyTab:
		state.focusPrompt = !state.focusPrompt
		return m, nil
	case tea.KeyUp, tea.KeyPgUp:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		state.convFollow = false
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		return m, cmd
	case tea.KeyDown, tea.KeyPgDown:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		if state.convVP.AtBottom() {
			state.convFollow = true
		}
		return m, cmd
	case tea.KeyHome:
		if state.focusPrompt {
			state.promptVP.GotoTop()
			return m, nil
		}
		state.convFollow = false
		state.convVP.GotoTop()
		return m, nil
	case tea.KeyEnd:
		if state.focusPrompt {
			state.promptVP.GotoBottom()
			return m, nil
		}
		state.convFollow = true
		state.convVP.GotoBottom()
		return m, nil
	case tea.KeyEsc:
		return m.exitCoCreate()
	}

	// Cho phép các thao tác chỉnh sửa (nhập ký tự/xóa lùi/con trỏ/Ctrl+U/ngắt dòng nhiều dòng) trong khi chờ AI trả lời——
	// người dùng có thể nhập trước câu tiếp theo trong thời gian AI suy nghĩ. Việc chặn thao tác nộp sẽ nằm trong từng case,
	// để điều tiết Enter đứng trước việc chặn awaiting——như vậy các mảnh \n do dán vào vẫn có thể được thay bằng khoảng trắng.

	switch msg.Type {
	case tea.KeyCtrlS:
		if state.awaiting {
			return m, nil
		}
		if !state.canStart() {
			return m, nil
		}
		// Đồng sáng tác theo giai đoạn: tiêm "brief hướng đi tiếp theo" và khôi phục sáng tác, quay lại bàn làm việc.
		if state.stage {
			draft := state.draftPrompt()
			m.cocreate = nil
			m.err = nil
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(resumeFromCoCreate(m.runtime, draft), m.textarea.Focus())
		}
		// Khởi động lạnh đồng sáng tác: bắt đầu sáng tác bằng lệnh sáng tác đã dọn dẹp xong.
		prompt, err := state.buildPrompt()
		if err != nil {
			m.err = err
			return m, nil
		}
		cmd := m.enterStarting(prompt)
		return m, tea.Batch(startRuntime(m.runtime, prompt), cmd)
	case tea.KeyEnter:
		// Alt+Enter → Chủ động ngắt dòng, để textarea.Update tiếp quản (KeyMap.InsertNewline đã gán phím này)
		if msg.Alt {
			break
		}
		// Khoảng cách với phím ký tự trước đó quá ngắn → coi như mảnh \n của luồng dán: thay bằng khoảng trắng thay vì nộp.
		// Bắt buộc phải phán đoán trước khi chặn awaiting——nếu không mảnh \n dán trong lúc awaiting sẽ bị chặn,
		// dẫn đến "abc\ndef" bị nuốt thành "abcdef", không nhất quán với ngữ nghĩa của luồng cơ sở.
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			state.resetSuggestionInput()
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		// Ý định nộp thực sự: chặn trong thời gian awaiting (không thể gửi nhiều yêu cầu đồng thời)
		if state.awaiting {
			return m, nil
		}
		text := utils.CleanInputLine(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.err = nil
		state.appendUser(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		cmd := m.sendCoCreate()
		return m, cmd
	case tea.KeyCtrlU:
		state.resetSuggestionInput()
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m, nil
	}

	// Phím số 1/2/3 có thể liên tục tổ hợp gợi ý: điền vào lần đầu, sau đó dùng dấu chấm phẩy để thêm, chọn trùng lặp sẽ bị bỏ qua.
	// Bất kỳ chỉnh sửa thủ công nào cũng sẽ thoát khỏi trạng thái tổ hợp phím tắt, sau đó các số giữ ngữ nghĩa nhập liệu bình thường.
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !state.awaiting {
		if r := msg.Runes[0]; r >= '1' && r <= '3' {
			if value, handled := state.appendSuggestion(int(r-'1'), m.textarea.Value()); handled {
				m.textarea.SetValue(value)
				m.textarea.CursorEnd()
				m.refitTextareaHeight()
				return m, nil
			}
		}
	}

	// Chuyển tiếp nhập liệu thông thường cho textarea
	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}
	state.resetSuggestionInput()
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	return m, cmd
}

// exitCoCreate thoát chế độ đồng sáng tác, hủy yêu cầu LLM đang chạy, khôi phục trạng thái ô nhập liệu.
func (m Model) exitCoCreate() (tea.Model, tea.Cmd) {
	if m.cocreate.cancel != nil {
		m.cocreate.cancel()
	}
	stage := m.cocreate.stage
	initial := m.cocreate.initialInput()
	m.cocreate = nil
	m.resizeTextarea()
	// Hủy đồng sáng tác theo giai đoạn: xóa cờ chiếm dụng, giữ trạng thái tạm dừng, quay lại trạng thái nhập liệu của bàn làm việc (không điền lại câu mở đầu tổng hợp).
	if stage {
		m.textarea.SetValue("")
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(cancelCoCreate(m.runtime), fetchSnapshot(m.runtime), m.textarea.Focus())
	}
	m.textarea.SetValue(initial)
	m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
	return m, m.textarea.Focus()
}

// overlayAboveInput xếp chồng overlay trôi nổi lên trên phần đáy của view base (phía trên inputBox),
// không làm thay đổi chiều cao tổng thể của layout. Chỉ che phủ chiều rộng riêng của thẻ overlay, phần bên phải vẫn lộ nội dung bên dưới.
func overlayAboveInput(base, overlay string, inputLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	endY := len(baseLines) - inputLineCount
	startY := endY - len(overLines)
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overLines {
		y := startY + i
		if y >= 0 && y < endY {
			olW := lipgloss.Width(ol)
			// Cắt bỏ olW ký tự hiển thị ở bên trái đường cơ sở, ghép overlay + nội dung còn lại bên phải
			right := ansi.TruncateLeft(baseLines[y], olW, "")
			baseLines[y] = ol + right
		}
	}
	return strings.Join(baseLines, "\n")
}

// isCSILeak phát hiện xem KeyRunes có phải là mảnh rò rỉ của chuỗi escape CSI hay không.
// Khi terminal gửi phím điều hướng \x1b[A, thao tác nhấn nhanh có thể dẫn đến việc chuỗi bị tách rời:
// \x1b được phân tích thành Escape, "[" hoặc "[A" bị rò rỉ vào textarea dưới dạng KeyRunes.
func isCSILeak(runes []rune) bool {
	if len(runes) == 0 || runes[0] != '[' {
		return false
	}
	for _, r := range runes[1:] {
		if (r >= '0' && r <= '9') || r == ';' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
			continue
		}
		return false
	}
	return true
}

// containsSGRFragment phát hiện xem văn bản có chứa mảnh của chuỗi chuột SGR hay không (mẫu "<số;số;").
func containsSGRFragment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			continue
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

func cleanHumanKeyRunes(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		return msg, true
	}
	cleaned := utils.CleanInputRunes(msg.Runes)
	if cleaned == "" {
		return msg, false
	}
	msg.Runes = []rune(cleaned)
	return msg, true
}
