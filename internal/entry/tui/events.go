package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// Các kiểu thông điệp
type (
	eventMsg       host.Event
	snapshotMsg    host.UISnapshot
	doneMsg        struct{ complete bool } // complete=true hoàn bản, false lỗi dừng máy
	abortResultMsg struct{ stopped bool }
	bootstrapMsg   struct {
		existing  bool // Đã có tác phẩm; dù khôi phục thành công hay không đều nên vào bàn làm việc
		resumed   bool
		completed bool // Thư mục là sách đã hoàn kết: vào bàn làm việc trạng thái hoàn thành thay vì trang chào mừng
		err       error
	}
	reportLoadedMsg struct {
		reqID      int
		report     diag.Report
		exportPath string // Đường dẫn tuyệt đối file chẩn đoán ẩn danh; rỗng = xuất thất bại
		exportErr  error
		finishedAt time.Time
	}
	startResultMsg   struct{ err error }
	cocreateDeltaMsg struct {
		reqID int
		kind  string // host.CoCreateProgressThinking | host.CoCreateProgressReply
		text  string
	}
	// cocreateStreamItem là tải trọng nội bộ của deltaCh, mang kind dạng luồng và văn bản tích lũy gửi đến TUI.
	cocreateStreamItem struct {
		kind string
		text string
	}
	cocreateDoneMsg struct {
		reqID int
		reply host.CoCreateReply
		err   error
	}
	steerResultMsg     struct{ err error }
	continueResultMsg  struct{ err error }
	spinnerTickMsg     time.Time
	toolSpinnerTickMsg time.Time // spinner công cụ luồng sự kiện tick độc lập (nhanh hơn, độc lập với thanh trên/ngôi sao)
	streamDeltaMsg     string    // Token delta dạng luồng
	streamClearMsg     struct{}  // Xóa bộ đệm dạng luồng (bắt đầu tin nhắn mới)
	streamFlushTickMsg struct{}  // Tiết lưu làm mới dạng luồng (chỉ lập lịch khi có dữ liệu chờ làm mới)
	quitResetMsg       struct{}  // Đặt lại thời gian chờ Ctrl+C hai lần
	updateCheckMsg     struct {
		result *buildversion.CheckResult
		err    error
	}
)

// --- Hàm Cmd ---

// checkForUpdate truy vấn nền phiên bản mới từ upstream (5s quá hạn, 24h cache tiết lưu). Lỗi đi kèm tin nhắn
// trả về, do Update ghi log nhưng không làm phiền giao diện người dùng.
func checkForUpdate(currentVersion string) tea.Cmd {
	return func() tea.Msg {
		configDir := bootstrap.DefaultConfigDir()
		if configDir == "" {
			return updateCheckMsg{err: fmt.Errorf("không thể xác định thư mục cache kiểm tra cập nhật")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := buildversion.CheckUpdate(ctx, buildversion.CheckOptions{
			CurrentVersion: currentVersion,
			CachePath:      filepath.Join(configDir, "update-check.json"),
		})
		return updateCheckMsg{result: res, err: err}
	}
}

// updateNotesPreviewWidth là chiều rộng tóm tắt một dòng dùng chung cho trang chào và luồng sự kiện. Release từ xa
// không đưa trực tiếp vào terminal: trước tiên xóa ANSI/ký tự điều khiển, sau đó cắt ngắn tường minh, tránh chuỗi điều khiển terminal và dòng quá dài.
const updateNotesPreviewWidth = 56

func formatUpdateNotice(result *buildversion.CheckResult) string {
	notice := fmt.Sprintf("Phiên bản mới %s đã phát hành", result.Latest)
	if preview := updateNotesPreview(result.Notes); preview != "" {
		notice += " · " + preview
	}
	return notice + " · chạy ainovel-cli update để nâng cấp"
}

func updateNotesPreview(notes string) string {
	plain := ansi.Strip(notes)
	plain = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, plain)
	for _, rawLine := range strings.Split(plain, "\n") {
		line := strings.TrimSpace(rawLine)
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*-"))
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			return truncate(line, updateNotesPreviewWidth)
		}
	}
	return ""
}

func listenEvents(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-rt.Events()
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func listenDone(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-rt.Done()
		if !ok {
			return nil
		}
		snap := rt.Snapshot()
		return doneMsg{complete: snap.Phase == "complete"}
	}
}

func tickSnapshot(rt *host.Host) tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return snapshotMsg(rt.Snapshot())
	})
}

func fetchSnapshot(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg(rt.Snapshot())
	}
}

func bootstrapRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		msg := bootstrapMsg{
			existing:  snapshot.Phase != "" || snapshot.BookTitle != "",
			completed: snapshot.Phase == "complete",
		}
		label, err := rt.Resume()
		if err != nil {
			msg.err = err
			return msg
		}
		if label == "" {
			if msg.existing {
				return msg
			}
			return nil
		}
		msg.resumed = true
		return msg
	}
}

// resumeBook chạy bổ sung kiểm tra khôi phục trong phiên (Resume của bootstrap chỉ chạy 1 lần lúc khởi động):
// Đóng bảng điều khiển sau khi import xong, mở lại /reopen đều dựa vào nó để quay lại bàn làm việc sáng tác. Không phát lại hàng đợi sự kiện——sự kiện của phiên này
// đã được listenEvents thường trú hiển thị, phát lại sẽ lặp lại hiển thị. Can thiệp chờ xử lý (như hướng viết tiếp được đăng ký bởi /reopen)
// do Resume đi qua Arbiter phán quyết tiêu hóa trước, rồi mới tiếp tục chạy engine.
func resumeBook(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		label, err := rt.Resume()
		return bootstrapMsg{
			existing: snapshot.Phase != "" || snapshot.BookTitle != "", completed: snapshot.Phase == "complete",
			resumed: label != "", err: err,
		}
	}
}

func startRuntime(rt *host.Host, prompt string) tea.Cmd {
	return func() tea.Msg {
		// Phía khởi động tạo bản chụp quy tắc người dùng xác định cho sách này (chuẩn hóa bằng prompt gốc), phải thực hiện trước StartPrepared.
		if err := rt.PrepareUserRules(prompt); err != nil {
			return startResultMsg{err: err}
		}
		err := rt.StartPrepared(prompt)
		return startResultMsg{err: err}
	}
}

func runCoCreate(rt *host.Host, state *cocreateState) tea.Cmd {
	history := state.session.History()
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	state.deltaCh = make(chan cocreateStreamItem, 64)
	state.doneCh = make(chan cocreateDoneMsg, 1)
	// Đồng sáng tác theo giai đoạn mang theo tóm tắt trạng thái câu chuyện, tạo ra "brief hướng đi tiếp theo"; khởi động lạnh làm rõ yêu cầu từ đầu. Chữ ký của cả hai giống nhau.
	stream := rt.CoCreateStream
	if state.stage {
		stream = rt.StageCoCreateStream
	}
	start := func() tea.Msg {
		go func() {
			reply, err := stream(ctx, history, func(kind, text string) {
				select {
				case state.deltaCh <- cocreateStreamItem{kind: kind, text: text}:
				default:
				}
			})
			state.doneCh <- cocreateDoneMsg{reply: reply, err: err}
			close(state.deltaCh)
			close(state.doneCh)
		}()
		return nil
	}
	return tea.Batch(start, listenCoCreateDelta(state), listenCoCreateDone(state))
}

func listenCoCreateDelta(state *cocreateState) tea.Cmd {
	if state == nil || state.deltaCh == nil {
		return nil
	}
	// Lấy tham chiếu cục bộ của channel: tránh trường hợp state.deltaCh bị gán lại sau đó
	// closure listen cũ đọc nhầm channel mới (mặc dù luồng hiện tại không kích hoạt, nhưng để lại cạm bẫy bảo trì là không nên).
	reqID := state.reqID
	ch := state.deltaCh
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return cocreateDeltaMsg{reqID: reqID, kind: item.kind, text: item.text}
	}
}

func listenCoCreateDone(state *cocreateState) tea.Cmd {
	if state == nil || state.doneCh == nil {
		return nil
	}
	reqID := state.reqID
	ch := state.doneCh
	return func() tea.Msg {
		result, ok := <-ch
		if !ok {
			return nil
		}
		result.reqID = reqID
		return result
	}
}

func steerRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		return steerResultMsg{err: rt.Steer(text)}
	}
}

func continueRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		err := rt.Continue(text)
		return continueResultMsg{err: err}
	}
}

// resumeFromCoCreate tiêm brief hướng đi tiếp theo được tạo ra từ đồng sáng tác theo giai đoạn và khôi phục sáng tác.
// Tái sử dụng continueResultMsg: thành công thì nối tiếp listenDone chạy tiếp, thất bại hiển thị lỗi.
func resumeFromCoCreate(rt *host.Host, draft string) tea.Cmd {
	return func() tea.Msg {
		err := rt.ResumeFromCoCreate(draft)
		return continueResultMsg{err: err}
	}
}

// cancelCoCreate từ bỏ đồng sáng tác theo giai đoạn: xóa cờ chiếm dụng, giữ nguyên trạng thái tạm dừng. Sự kiện chảy về qua kênh events, không cần trả về tin nhắn.
func cancelCoCreate(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		rt.CancelCoCreate()
		return nil
	}
}

func abortRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return abortResultMsg{stopped: rt.Abort()}
	}
}

func loadReport(dir string, reqID int) tea.Cmd {
	return func() tea.Msg {
		s := store.NewStore(dir)
		// Diagnose = Chẩn đoán sáng tác + kiểm tra runtime, Finding runtime cũng vào báo cáo trên màn hình.
		rep, rc := diag.Diagnose(s)
		// Tái sử dụng rep+rc để ghi file chẩn đoán ẩn danh (xuất thất bại không ảnh hưởng đến báo cáo trên màn hình).
		exportPath, exportErr := diag.WriteExport(s, rep, rc)
		return reportLoadedMsg{
			reqID:      reqID,
			report:     rep,
			exportPath: exportPath,
			exportErr:  exportErr,
			finishedAt: time.Now(),
		}
	}
}

func tickSpinner() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// tickToolSpinner điều khiển spinner của dòng "đang tiến hành" trong luồng sự kiện. Độc lập với tickSpinner, nhịp độ nhanh hơn (150ms).
func tickToolSpinner() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return toolSpinnerTickMsg(t)
	})
}

// tickStreamFlush hợp nhất delta dạng luồng trong cửa sổ 16ms. Nó được kích hoạt bởi delta chờ làm mới đầu tiên,
// làm mới xong thì dừng, khi nhàn rỗi sẽ không liên tục đánh thức TUI.
func tickStreamFlush() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return streamFlushTickMsg{}
	})
}

func listenStream(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		delta, ok := <-rt.Stream()
		if !ok {
			return nil
		}
		// sentinel phân phát thành streamClearMsg, đảm bảo đến TUI theo thứ tự emit
		// trong cùng một kênh với delta bình thường. Khi dùng kênh đôi, clearCh và streamCh không có thứ tự, ✻ header thường bị
		// nhét nhầm vào cuối đoạn thinking trước đó.
		if delta == host.StreamClearSentinel {
			return streamClearMsg{}
		}
		return streamDeltaMsg(delta)
	}
}
