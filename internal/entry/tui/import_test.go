package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// TestImportHistoryCoalescesRetryLines 守护thử lại dòng 原地cập nhật ：同 Key liên tục 事件只占一dòng 
// （"第 N 次"在一dòng 跳动），被普通进度dòng 隔断后另起一dòng ，保持时间序。
func TestImportHistoryCoalescesRetryLines(t *testing.T) {
	s := newImportState(1, "book.txt", 100, 40, nil)
	base := len(s.history)
	retry := func(msg string) imp.Event {
		return imp.Event{Time: time.Now(), Stage: imp.StageSegmenting, Message: msg, Level: "warn", Key: "retry:segmenting"}
	}
	s.appendEvent(retry("1s 后thử lại （第 1 次）"), 80)
	s.appendEvent(retry("2s 后thử lại （第 2 次）"), 80)
	s.appendEvent(retry("4s 后thử lại （第 3 次）"), 80)
	if got := len(s.history) - base; got != 1 {
		t.Fatalf("同 Key liên tục thử lại 应合并为 1 dòng ，得 %d", got)
	}
	if last := s.history[len(s.history)-1]; last.message != "4s 后thử lại （第 3 次）" {
		t.Fatalf("合并dòng 应cập nhật 为最新tin nhắn ，得 %q", last.message)
	}
	// 普通进度dòng 隔断后，新thử lại 另起一dòng 。
	s.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAnalyzing, Message: "分析第 1 chương 起的liên tục 批次..."}, 80)
	s.appendEvent(retry("1s 后thử lại （第 1 次）"), 80)
	if got := len(s.history) - base; got != 3 {
		t.Fatalf("隔断后thử lại 应另起一dòng ，共 3 dòng ，得 %d", got)
	}
}

// TestRenderImportLineWrapsWithoutClipping 守护lỗi 详情完整可见：chính văn按扣除tiền tố 后的
// 剩余宽度换dòng 、续dòng 对齐，任何一dòng 都不得超出 contentW——viewport 对超宽dòng 是硬裁，
// lỗi 里的 HTTP trạng thái/provider/model 正是排查依据，截掉bằng 白báo lỗi。
func TestRenderImportLineWrapsWithoutClipping(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageSegmenting,
		message: "切分区间 L1..L171",
		err: errors.New("imp: model gọi thất bại（请求tham số 非法，HTTP 400，openrouter，deepseek/deepseek-chat）：" +
			"Provider returned error: invalid request payload with a very long gateway message tail"),
	}
	const contentW = 80
	out := renderImportLine(ln, contentW, time.Now())
	// 换dòng 可能在任意chữ 符处断开，去掉空白后比对，只xác minhnội dung 一个chữ 不丢。
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' {
				return -1
			}
			return r
		}, s)
	}
	for _, want := range []string{"HTTP 400", "openrouter", "gateway message tail"} {
		if !strings.Contains(norm(out), norm(want)) {
			t.Fatalf("dòng nội dung 缺少 %q：%q", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("第 %d dòng 宽 %d 超出 %d，会被 viewport 裁掉：%q", i, w, contentW, line)
		}
	}
	// 窄终端：tiền tố （时间戳+图标+长giai đoạn名）可占掉大半dòng 宽，chính văn须另起dòng 而非按giới hạn dưới 硬凑超宽。
	ln.stage = imp.StageAwaitingConfirmation
	const narrowW = 40
	for i, line := range strings.Split(renderImportLine(ln, narrowW, time.Now()), "\n") {
		if w := lipgloss.Width(line); w > narrowW {
			t.Fatalf("窄终端第 %d dòng 宽 %d 超出 %d：%q", i, w, narrowW, line)
		}
	}
}

// TestRenderImportLineMultilineBlock 守护多dòng 块tin nhắn （切分确认预览）的排版：续dòng 整体
// 浅缩进（2 列），不得按tiền tố 宽对齐——40+ 列tiền tố 会把整块chươngdanh sách 挤到bảng điều khiển 右半，左半全空。
func TestRenderImportLineMultilineBlock(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageAwaitingConfirmation,
		current: 157, total: 157,
		message: "已切分 157 chương ，请核对：\n  第1chương  引子\n  第2chương  我故意的\n",
	}
	const contentW = 100
	out := strings.Split(renderImportLine(ln, contentW, time.Now()), "\n")
	if len(out) != 3 {
		t.Fatalf("nên làtiền tố dòng  + 2 个chính văndòng ，得 %d dòng ：%q", len(out), out)
	}
	for i, line := range out[1:] {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("第 %d dòng 超宽 %d：%q", i+1, w, line)
		}
		if strings.HasPrefix(line, strings.Repeat(" ", 20)) {
			t.Fatalf("多dòng 块续dòng 不应按tiền tố 宽对齐：%q", line)
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("多dòng 块续dòng 应浅缩进 2 列：%q", line)
		}
	}
}

// TestWrapTextResetsAtNewlines 守护多dòng tin nhắn 换dòng ：'\n' 处phải đặt lại dòng 宽计数，否则只要
// 任一dòng kích hoạt 换dòng ，其后每dòng 都会被误判超宽插入伪换dòng +缩进，整份确认预览被打散。
func TestWrapTextResetsAtNewlines(t *testing.T) {
	in := strings.Repeat("宽", 30) + "\n短dòng 一\n短dòng 二"
	out := wrapText(in, 20)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 20 {
			t.Fatalf("第 %d dòng 宽 %d 超出 20：%q", i, w, l)
		}
	}
	if !strings.Contains(out, "\n短dòng 一\n短dòng 二") {
		t.Fatalf("原有短dòng 不得被打散：%q", out)
	}
}

// TestImportEscResumeGate 守护nhập bảng điều khiển  Esc 的落点：从欢迎trang 发起的nhập thành công收尾后，
// 关bảng điều khiển phải 补跑一次khôi phục （bootstrap 的 Resume 只在khởi động 时跑），否则người dùng 被留在没有
// 续ghi 口的欢迎trang ；出错终态与bảng làm việc 场景只关bảng điều khiển ；đang chạy  Esc 仍是取消而非关闭。
func TestImportEscResumeGate(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	// tea.Batch thực thi 后trả về  BatchMsg（子命令不被thực thi ），以此区分"焦点+khôi phục "与纯焦点。
	isBatch := func(cmd tea.Cmd) bool {
		_, ok := cmd().(tea.BatchMsg)
		return ok
	}
	newM := func(mode appMode, st *importState) Model {
		return Model{mode: mode, importer: st, textarea: textarea.New()}
	}

	m := newM(modeNew, &importState{done: true, stage: imp.StageDone})
	next, cmd := m.handleImportKey(esc)
	if next.(Model).importer != nil {
		t.Fatal("终态 Esc 应关闭bảng điều khiển ")
	}
	if !isBatch(cmd) {
		t.Fatal("欢迎trang nhập thành công关bảng điều khiển 应附带khôi phục 命令")
	}

	m = newM(modeNew, &importState{done: true, stage: imp.StageError, err: errors.New("boom")})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("出错终态不应kích hoạt khôi phục （书可能根本没nhập thành công）")
	}

	m = newM(modeRunning, &importState{done: true, stage: imp.StageDone})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("bảng làm việc 自有门禁，不应lặp lại kích hoạt khôi phục ")
	}

	canceled := false
	m = newM(modeNew, &importState{cancel: func() { canceled = true }})
	next, _ = m.handleImportKey(esc)
	if !canceled || next.(Model).importer == nil {
		t.Fatal("đang chạy  Esc 应取消nhập vàgiữ lại bảng điều khiển 等 runner 收尾")
	}
}

// TestRetryCountdown 守护倒计时渲染契约（事件bảng điều khiển 与nhập bảng điều khiển 共用）：
// 未设截止或已到点trả về 空（请求已在途）；剩余时间向上取整到秒，逐秒递减và不xuất hiện  0s。
func TestRetryCountdown(t *testing.T) {
	now := time.Now()
	if got := retryCountdown(time.Time{}, now); got != "" {
		t.Fatalf("零值截止nên trả về空，得 %q", got)
	}
	if got := retryCountdown(now.Add(-time.Second), now); got != "" {
		t.Fatalf("已到点nên trả về空，得 %q", got)
	}
	if got := retryCountdown(now.Add(7500*time.Millisecond), now); got != "Thử lại sau 8s" {
		t.Fatalf("7.5s 应上取整为 8s，得 %q", got)
	}
	if got := retryCountdown(now.Add(300*time.Millisecond), now); got != "Thử lại sau 1s" {
		t.Fatalf("不足 1s 应hiển thị  1s，得 %q", got)
	}
}

// TestParseImportArgsGuide 守护 --guide phân tích ：自然语言指导可含空格（其后 token 全部并入），
// 可与其它选项组合（置于最后），空nội dung báo lỗi。
func TestParseImportArgsGuide(t *testing.T) {
	opts, err := parseImportArgs([]string{"--guide=幕间·X", "也是", "独立chương"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Guidance != "幕间·X 也是 独立chương" {
		t.Fatalf("含空格指导应整体并入，得 %q", opts.Guidance)
	}
	opts, err = parseImportArgs([]string{"book.txt", "--yes", "--guide=序chương 并入第一chương "})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AutoConfirm || opts.SourcePath != "book.txt" || opts.Guidance != "序chương 并入第一chương" {
		t.Fatalf("与其它选项组合phân tích không khớp ：%+v", opts)
	}
	if _, err := parseImportArgs([]string{"--guide="}); err == nil {
		t.Fatal("空 --guide nên báo lỗi")
	}
}
