package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// importState là trạng thái modal trong quá trình chạy lệnh /import.
//
// Modal được tạo khi bắt đầu nhập, tiến triển theo luồng sự kiện; sau khi hoàn thành hoặc có lỗi sẽ giữ trên màn hình đợi người dùng Esc để đóng.
// Esc trong khi đang chạy sẽ kích hoạt hủy bỏ (ctx.Cancel), giao cho runner thu dọn ở điểm sự kiện tiếp theo.
type importState struct {
	reqID      int
	source     string
	stage      imp.Stage
	current    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	history    []importLine
	totalLines int // tổng số dòng log (tiếp tục đếm sau khi history đạt importHistoryMax)
	err        error
	done       bool // trạng thái cuối (hoàn thành/lỗi)
	paused     bool // pipeline dừng ở awaiting, channel sự kiện đã đóng: panel có thể đóng, không phải trạng thái cuối
	frame      int  // frame đồng bộ animation chính: dấu sao theo sau và đếm ngược dựa vào nó để tính lại mỗi tick
	cancel     context.CancelFunc
	viewport   viewport.Model
}

type importLine struct {
	at      time.Time
	stage   imp.Stage
	current int
	total   int
	message string
	level   string    // "warn" cảnh báo thử lại/backoff
	key     string    // khi không rỗng, các dòng liên tiếp có cùng key sẽ cập nhật tại chỗ (căn chỉnh cơ chế ID của bảng sự kiện)
	retryAt time.Time // khác không = thời hạn thử lại lần tới, khi render tính số giây còn lại tạo thành đếm ngược
	err     error

	rendered  string // kết quả render được cache theo renderedW; lịch sử có thể lên tới hàng nghìn dòng, sắp xếp lại toàn bộ mỗi tick sẽ làm treo panel
	renderedW int
}

// importHistoryMax là giới hạn trên của số dòng log giữ lại trong bộ nhớ panel: hiển thị từng chương của sách hàng nghìn chương + phát hành từng chương sẽ
// tăng không giới hạn, vừa tốn bộ nhớ vừa làm chậm render lại. File log (logs/import.log) luôn giữ bản ghi toàn bộ.
const importHistoryMax = 1000

func newImportState(reqID int, source string, width, height int, cancel context.CancelFunc) *importState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4)
	s := &importState{
		reqID:     reqID,
		source:    source,
		startedAt: time.Now(),
		stage:     imp.StageIngesting,
		cancel:    cancel,
		viewport:  vp,
	}
	s.refresh(contentW)
	return s
}

func (s *importState) appendEvent(ev imp.Event, contentW int) {
	s.stage = ev.Stage
	s.current = ev.Current
	s.total = ev.Total
	if ev.Err != nil {
		s.err = ev.Err
	}
	line := importLine{
		at: ev.Time, stage: ev.Stage, current: ev.Current, total: ev.Total,
		message: ev.Message, level: ev.Level, key: ev.Key, retryAt: ev.RetryAt, err: ev.Err,
	}
	// Cùng Key và nằm liền kề → cập nhật tại chỗ (7 lần backoff nhảy trên cùng một dòng); bị dòng tiến trình khác ngắt ngang thì xuống dòng mới, giữ thứ tự thời gian.
	if ev.Key != "" && len(s.history) > 0 && s.history[len(s.history)-1].key == ev.Key {
		s.history[len(s.history)-1] = line
	} else {
		s.totalLines++
		s.history = append(s.history, line)
		if len(s.history) > importHistoryMax {
			s.history = append(s.history[:0], s.history[len(s.history)-importHistoryMax:]...)
		}
	}
	if ev.Stage == imp.StageDone || ev.Stage == imp.StageError {
		s.done = true
		s.finishedAt = ev.Time
	}
	s.refresh(contentW)
}

func (s *importState) refresh(contentW int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Nhập tiểu thuyết bên ngoài"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("File nguồn "))
	b.WriteString(s.source)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Bắt đầu "))
	b.WriteString(formatReportTime(s.startedAt))
	if !s.finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  Hoàn thành "))
		b.WriteString(formatReportTime(s.finishedAt))
	}
	b.WriteString("\n\n")

	// Dòng giai đoạn hiện tại
	b.WriteString(mutedStyle.Render("Giai đoạn "))
	b.WriteString(stageStyle.Render(string(s.stage)))
	if s.total > 0 {
		b.WriteString(mutedStyle.Render("  Tiến độ "))
		if s.current > 0 {
			b.WriteString(fmt.Sprintf("%d/%d", s.current, s.total))
		} else {
			b.WriteString(fmt.Sprintf("0/%d", s.total))
		}
	}
	b.WriteString("\n\n")

	// Log lịch sử. Mỗi dòng một cột biểu tượng ngữ nghĩa (căn chỉnh dạng bảng sự kiện):
	// ✗ Đỏ=Lỗi · ↻ Cam=Backoff thử lại/xác minh hỏi lại (cùng key nhảy tại chỗ) · ✓ Xanh lá=Hoàn thành · · Xám=Tiến độ bình thường.
	b.WriteString(titleStyle.Render("Log tiến trình"))
	b.WriteString(" ")
	if s.totalLines > len(s.history) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d mục, chỉ hiển thị %d mục gần nhất, toàn bộ xem tại logs/import.log)", s.totalLines, len(s.history))))
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d mục)", s.totalLines)))
	}
	b.WriteString("\n")
	now := time.Now()
	for i := range s.history {
		ln := &s.history[i]
		// Dòng đã chốt được cache kết quả render theo chiều rộng: refresh chạy mỗi tick animation, sắp xếp lại
		// toàn bộ lịch sử hàng nghìn dòng (wrapText+tô màu từng dòng) là chi phí bậc hai, giai đoạn publish sẽ giật lag thấy rõ.
		// Chỉ những dòng có đếm ngược còn hoạt động mới cần tính lại mỗi tick (sau khi đến giờ tính thêm 2s để xóa huy hiệu).
		live := !ln.retryAt.IsZero() && now.Before(ln.retryAt.Add(2*time.Second))
		if ln.rendered == "" || ln.renderedW != contentW || live {
			ln.rendered = renderImportLine(*ln, contentW, now)
			ln.renderedW = contentW
		}
		b.WriteString("\n")
		b.WriteString(ln.rendered)
	}

	running := !s.done && !s.paused
	if running {
		// Con trỏ theo sau: một ngôi sao giống hệt panel stream theo sau dòng log cuối cùng, nhảy từng frame theo animation chính,
		// hô ứng với dòng chỉ thị "Đang chạy" ở trên cùng——có nó ở cuối log, trong thời gian chờ backoff cũng thấy ngay pipeline vẫn còn sống.
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[s.frame%len(streamCursorFrames)]))
	}

	// Gợi ý thu dọn
	b.WriteString("\n\n")
	switch {
	case s.err != nil:
		b.WriteString(errStyle.Render("Nhập thất bại"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc để đóng panel"))
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		b.WriteString(okStyle.Render("Cắt phân hoàn thành, đang chờ bạn đối chiếu"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("y xác nhận cắt phân và tiếp tục; nếu cần điều chỉnh cắt phân có thể nhấn Esc sau đó dùng /import --guide=<giải thích bằng ngôn ngữ tự nhiên>; Esc để đóng panel"))
	case s.paused:
		// Pipeline dừng ở chỗ chờ phán quyết, channel đã đóng: thao tác theo gợi ý trong panel sau đó nhấn Esc để đóng.
		b.WriteString(okStyle.Render("Nhập đã tạm dừng, đang chờ bạn thao tác"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Làm theo gợi ý phía trên để tiếp tục (ví dụ: /import --story=open|closed); Esc để đóng panel"))
	case s.done:
		b.WriteString(okStyle.Render("Nhập hoàn thành, Foundation và các chương đã sẵn sàng"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc để đóng panel và nối thông cửa ải viết tiếp (engine dừng ở ranh giới chương tiếp theo, đợi bạn nghiệm thu cho qua)"))
	default:
		b.WriteString(dimStyle.Render("Esc để hủy nhập"))
	}

	// Việc bám đuôi chỉ có hiệu lực khi người dùng đang ở dưới cùng: refresh hiện tại chạy mỗi tick (animation/đếm ngược),
	// GotoBottom vô điều kiện sẽ kéo người dùng đang cuộn lên xem trong lúc chạy về dưới cùng mỗi 350ms.
	atBottom := s.viewport.AtBottom()
	s.viewport.SetContent(b.String())
	if running && atBottom {
		s.viewport.GotoBottom()
	}
}

// renderImportLine render một dòng log tiến trình: timestamp + cột icon ngữ nghĩa + giai đoạn (+tiến độ) + phần chính.
// Phần chính ngắt dòng theo chiều rộng còn lại sau khi trừ đi prefix, dòng tiếp theo căn lề với điểm bắt đầu của phần chính; siêu rộng chỉ ngắt dòng tuyệt đối không cắt xén——
// viewport đối với dòng siêu rộng là cắt cứng, trạng thái HTTP/provider/model trong lỗi chính là căn cứ chẩn đoán, cắt đi bằng báo lỗi vô ích.
func renderImportLine(ln importLine, contentW int, now time.Time) string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var p strings.Builder
	p.WriteString(dimStyle.Render(ln.at.Format("15:04:05")))
	p.WriteString(" ")
	switch {
	case ln.err != nil:
		p.WriteString(errStyle.Bold(true).Render("✗"))
	case ln.level == "warn":
		p.WriteString(warnStyle.Bold(true).Render("↻"))
	case ln.stage == imp.StageDone:
		p.WriteString(okStyle.Bold(true).Render("✓"))
	default:
		p.WriteString(dimStyle.Render("·"))
	}
	p.WriteString(" ")
	p.WriteString(stageStyle.Render(string(ln.stage)))
	if ln.total > 0 && ln.current > 0 {
		p.WriteString(mutedStyle.Render(fmt.Sprintf(" %d/%d", ln.current, ln.total)))
	}
	p.WriteString(" ")
	prefix := p.String()

	var text string
	style := lipgloss.NewStyle()
	switch {
	case ln.err != nil:
		text = ln.message + " — " + ln.err.Error()
		style = errStyle
	case ln.level == "warn":
		text = ln.message
		if cd := retryCountdown(ln.retryAt, now); cd != "" {
			text += " · " + cd
		}
		style = warnStyle
	default:
		text = ln.message
	}
	// Sau khi tô màu từng dòng tự ghép lại: lipgloss đối với chuỗi nhiều dòng sẽ đệm mỗi dòng bằng khoảng trắng cho đến dòng rộng nhất trong khối,
	// prefix chỉ ở dòng đầu, render cả khối sẽ làm dòng đầu vượt quá contentW và bị viewport cắt đi.
	prefixW := lipgloss.Width(prefix)
	wrapW := contentW - prefixW
	if wrapW < 20 {
		// Trong terminal hẹp, prefix (timestamp+icon+tên giai đoạn dài+tiến độ) đã chiếm hơn nửa dòng: phần chính sang dòng mới thụt lề nhẹ,
		// chiều rộng ngắt dòng luôn bị ràng buộc bởi contentW——ghép cứng theo giới hạn dưới 20 cột sẽ làm dòng đầu siêu rộng bị viewport cắt đi,
		// vừa đúng cắt mất trạng thái HTTP/provider ở đuôi lỗi làm căn cứ chẩn đoán.
		var out strings.Builder
		out.WriteString(prefix)
		for _, l := range strings.Split(wrapText(text, max(10, contentW-4)), "\n") {
			out.WriteString("\n    ")
			out.WriteString(style.Render(l))
		}
		return out.String()
	}
	// Thông báo khối nhiều dòng (như xem trước xác nhận cắt phân): dòng đầu theo sau prefix, các dòng còn lại thụt lề nhẹ——nếu theo chiều rộng prefix
	// căn lề dòng tiếp theo, prefix hơn 40 cột sẽ ép cả khối nội dung sang nửa phải panel, nửa trái trống trơn.
	head, body := text, ""
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		head, body = text[:i], strings.TrimRight(text[i+1:], "\n")
	}
	lines := strings.Split(wrapText(head, wrapW), "\n")
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(style.Render(lines[0]))
	pad := strings.Repeat(" ", prefixW)
	for _, l := range lines[1:] {
		out.WriteString("\n")
		out.WriteString(pad)
		out.WriteString(style.Render(l))
	}
	if body != "" {
		for _, l := range strings.Split(wrapText(body, contentW-2), "\n") {
			out.WriteString("\n  ")
			out.WriteString(style.Render(l))
		}
	}
	return out.String()
}

func renderImportModal(width, height int, s *importState, frame int) string {
	if s == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	running := !s.done && !s.paused
	if s.viewport.Width != contentW {
		s.viewport.Width = contentW
		s.refresh(contentW)
	}
	vpH := boxH - 4
	if running {
		vpH -= 2 // Dòng chỉ thị hoạt động ở trên cùng + dòng trống
	}
	if s.viewport.Height != vpH {
		s.viewport.Height = vpH
	}

	hint := "  ↑↓ Cuộn · Esc Hủy/Đóng"
	switch {
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		hint = "  ↑↓ Cuộn · y Xác nhận cắt phân · Esc Đóng"
	case running:
		hint = "  ↑↓ Cuộn · Esc Hủy"
	}

	body := strings.Split(s.viewport.View(), "\n")
	if running {
		// Chỉ thị hoạt động khi đang chạy: một ngôi sao giống hệt panel stream + thời gian đã dùng, cập nhật tần số thấp theo animation chính.
		// Treo ngoài viewport ở một dòng cố định——nội dung viewport chỉ refresh theo sự kiện, để animation bên trong sẽ không chuyển động;
		// không có nó, panel sẽ bất động trong quá trình gọi model thời gian dài/chờ backoff thử lại, người dùng sẽ lầm tưởng bị treo.
		star := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[frame%len(streamCursorFrames)])
		status := lipgloss.NewStyle().Foreground(colorMuted).
			Render(fmt.Sprintf(" Đang chạy · Đã dùng %s", formatElapsed(time.Since(s.startedAt))))
		body = append([]string{star + status, ""}, body...)
	}
	modal := renderPaddedModalFrame(boxW, boxH, "Nhập tiểu thuyết bên ngoài", hint, body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// formatElapsed render thời gian đã dùng dạng mm:ss (hơn 1 tiếng sẽ nhảy lên h:mm:ss).
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func (m Model) handleImportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.importer == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		// Vẫn đang chạy (chưa đến trạng thái cuối, chưa tạm dừng) → Esc để hủy, giao runner thu dọn; đã đến trạng thái cuối hoặc đã dừng ở awaiting
		// (đóng channel) → Esc đóng panel. Thiếu nhánh paused sẽ làm panel không thể đóng sau khi ngừng máy awaiting (bị treo).
		if !m.importer.done && !m.importer.paused && m.importer.cancel != nil {
			m.importer.cancel()
			return m, nil
		}
		succeeded := m.importer.stage == imp.StageDone && m.importer.err == nil
		m.importer = nil
		// Nhập thành công từ trang chào mừng: trang chào mừng không có lối vào viết tiếp (Resume của bootstrap chỉ
		// chạy một lần khi khởi động), đóng panel thì chạy bù lại phần khôi phục, để người dùng rơi vào cửa ải Hold hoàn thành nhập ở bàn làm việc,
		// thay vì ở lại trang chào mừng nơi lỡ bấm Enter là "mở sách mới".
		if succeeded && m.mode == modeNew {
			return m, tea.Batch(m.textarea.Focus(), resumeBook(m.runtime))
		}
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.importer.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.importer.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.importer.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.importer.viewport.HalfPageDown()
	case tea.KeyRunes:
		// Tại điểm tạm dừng xác nhận cắt phân nhấn y = chạy lại tại chỗ /import --yes (khôi phục không đường dẫn), cho phép đi qua cắt phân hiện tại một lần.
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') &&
			m.importer.paused && m.importer.stage == imp.StageAwaitingConfirmation {
			return m.confirmImportSegmentation()
		}
	}
	return m, nil
}

// confirmImportSegmentation gom "cho qua sau khi đã xem bản xem trước" thành một phím: chạy lại việc nhập tại chỗ kèm theo
// AcceptSegmentation (khôi phục là phi trạng thái, pipeline tiếp tục từ chỗ thiếu confirmation). Khác biệt của nó với --yes
// là "phán quyết tường minh sau khi đã xem bản xem trước"——cắt phân có ghi chú dung lỗi (Notes) --yes không cho qua, y cho qua;
// chỉ có hiệu lực với Options lần này, không ghi intent.json, sau đó các cắt phân mới tạo bằng --guide vẫn sẽ dừng lại đối chiếu.
// Tiếp tục dùng tên file nguồn và log tiến trình của panel cũ, để xem trước chương có thể cuộn lại xem trong lúc tiếp tục phân tích.
func (m Model) confirmImportSegmentation() (tea.Model, tea.Cmd) {
	prev := m.importer
	m.importSeq++
	state, listenCmd, err := startImportRun(m.runtime, m.importSeq, imp.Options{AcceptSegmentation: true}, m.width, m.height)
	if err != nil {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "Xác nhận cắt phân thất bại: " + err.Error(), Level: "error",
		})
		return m, nil
	}
	state.source = prev.source
	state.history = append([]importLine(nil), prev.history...)
	state.totalLines = prev.totalLines
	boxW, _ := reportModalSize(m.width, m.height)
	state.refresh(paddedModalContentWidth(boxW))
	m.importer = state
	return m, listenCmd
}

// importEventMsg gửi imp.Event một lần.
type importEventMsg struct {
	reqID int
	ev    imp.Event
	ch    <-chan imp.Event // tiếp tục lắng nghe thông báo tiếp theo trên cùng một channel
}

// importClosedMsg là tín hiệu đóng channel sự kiện (goroutine nhập dừng). Cho dù dừng ở trạng thái cuối hay ở awaiting,
// đóng channel đều dựa vào nó để báo cho panel biết có thể đóng một cách tin cậy, tránh việc chỉ nhận trạng thái cuối làm cho panel bị treo sau khi ngừng máy awaiting.
type importClosedMsg struct {
	reqID int
}

// startImport khởi chạy việc nhập một tiểu thuyết bên ngoài: phân tích tham số → tạo modal state → lắng nghe luồng sự kiện.
func startImport(rt *host.Host, reqID int, args []string, width, height int) (*importState, tea.Cmd, error) {
	opts, err := parseImportArgs(args)
	if err != nil {
		return nil, nil, err
	}
	return startImportRun(rt, reqID, opts, width, height)
}

// startImportRun khởi chạy việc nhập bằng Options đã định (y xác nhận và các lần chạy lại nội bộ khác không đi qua phân tích tham số).
// width/height dùng để khởi tạo viewport; hàm cancel treo trên state dùng để Esc hủy.
func startImportRun(rt *host.Host, reqID int, opts imp.Options, width, height int) (*importState, tea.Cmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.ImportFrom(ctx, opts)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newImportState(reqID, opts.SourcePath, width, height, cancel)
	return state, listenImportEvent(reqID, ch), nil
}

func listenImportEvent(reqID int, ch <-chan imp.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return importClosedMsg{reqID: reqID}
		}
		return importEventMsg{reqID: reqID, ev: ev, ch: ch}
	}
}

// parseImportArgs phân tích `/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<giải thích>]`.
// Không có tham số được xem như "khôi phục từ không gian làm việc đang hoạt động", đường dẫn nguồn không phải là mục bắt buộc để khôi phục (RFC §18).
// --guide là hướng dẫn cắt phân bằng ngôn ngữ tự nhiên, có thể chứa khoảng trắng: từ --guide= trở đi, toàn bộ nội dung phía sau gộp vào văn bản hướng dẫn, phải đặt ở cuối cùng.
func parseImportArgs(args []string) (imp.Options, error) {
	var opts imp.Options
	for i := range args {
		a := args[i]
		switch {
		case a == "--yes":
			opts.AutoConfirm = true
		case a == "--continue":
			opts.ContinueAfter = true
		case strings.HasPrefix(a, "--story="):
			v := strings.TrimPrefix(a, "--story=")
			if v != "open" && v != "closed" {
				return imp.Options{}, fmt.Errorf("--story chỉ có thể là open hoặc closed: %q", v)
			}
			opts.StoryResolution = v
		case strings.HasPrefix(a, "--guide="):
			parts := append([]string{strings.TrimPrefix(a, "--guide=")}, args[i+1:]...)
			g := strings.TrimSpace(strings.Join(parts, " "))
			if g == "" {
				return imp.Options{}, fmt.Errorf("--guide cần hướng dẫn cắt phân bằng ngôn ngữ tự nhiên, ví dụ --guide=Giải lao·X cũng là chương độc lập")
			}
			opts.Guidance = g
			return opts, nil
		case strings.HasPrefix(a, "--"):
			return imp.Options{}, fmt.Errorf("tùy chọn không xác định %q (hỗ trợ: --yes / --story=open|closed / --continue / --guide=<hướng dẫn cắt phân>)", a)
		default:
			if opts.SourcePath != "" {
				return imp.Options{}, fmt.Errorf("chỉ chấp nhận một đường dẫn file nguồn: thừa %q", a)
			}
			opts.SourcePath = a
		}
	}
	return opts, nil
}
