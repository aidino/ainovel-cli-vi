package eval

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

// RunOptions điều khiển một lần chạy case đơn lẻ.
type RunOptions struct {
	OutputDir string        // Thư mục đầu ra cách ly (bắt buộc)
	Timeout   time.Duration // Giới hạn thời gian thực cho mỗi case; 0 nghĩa là không giới hạn
	Progress  io.Writer     // Xuất dòng tiến độ (tùy chọn, nil thì không in)
}

// RunCase điều khiển một case: lắp ráp host → khởi động → đẩy tiến theo giới hạn chương → đến điểm thì Abort.
// bundle đã được phía gọi thực hiện ghi đè variant (nếu có). error trả về chính là "lỗi runtime" (cơ sở để hard fail);
// viết xong bình thường hoặc dừng lại bình thường đều trả về nil.
//
// RunCase độc chiếm và dọn dẹp OutputDir: StartPrepared chỉ đặt lại progress/checkpoints, không dọn dẹp chapters/
// foundation hay các sản phẩm khác, việc tái sử dụng thư mục cũ sẽ khiến tàn dư làm ô nhiễm diag và novel_context. Do đó dọn sạch trước khi chạy, đảm bảo cách ly.
func RunCase(cfg bootstrap.Config, bundle assets.Bundle, c Case, opts RunOptions) error {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return fmt.Errorf("RunCase: thiếu OutputDir")
	}
	if err := os.RemoveAll(opts.OutputDir); err != nil {
		return fmt.Errorf("dọn thư mục đầu ra: %w", err)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("tạo thư mục đầu ra: %w", err)
	}
	cfg.OutputDir = opts.OutputDir
	if c.Style != "" {
		cfg.Style = c.Style
	}

	eng, err := host.New(cfg, bundle, host.WithFileLog("headless.log", false))
	if err != nil {
		return fmt.Errorf("lắp ráp host: %w", err)
	}
	defer eng.Close()
	if logErr := eng.FileLogError(); logErr != nil {
		return fmt.Errorf("log file đánh giá không dùng được: %w", logErr)
	}

	prompt, err := startup.PrepareQuick(c.Prompt)
	if err != nil {
		return err
	}
	if err := eng.PrepareUserRules(prompt); err != nil {
		return fmt.Errorf("chuẩn bị quy tắc người dùng: %w", err)
	}
	if err := eng.StartPrepared(prompt); err != nil {
		return fmt.Errorf("khởi động: %w", err)
	}

	return drive(eng, c.MaxChapters, opts)
}

// driveEngine là interface engine tối thiểu mà drive tiêu thụ (*host.Host tự nhiên đáp ứng). Tách ra để
// viết bài kiểm thử xác định cho kỷ luật drain-to-Done——logic đồng thời đoạn này từng dính lỗi send-on-closed-channel.
type driveEngine interface {
	Events() <-chan host.Event
	Stream() <-chan string
	Done() <-chan struct{}
	Snapshot() host.UISnapshot
	Abort() bool
}

// drive tiêu thụ luồng sự kiện của engine, đến giới hạn chương hoặc timeout thì Abort ngay, đợi Done để thu dọn.
//
// Kỷ luật then chốt: Dù hoàn thành bình thường, dừng ngang do giới hạn chương hay timeout, đều phải drain đến Done rồi mới trả về. waitDone ngầm
// của host sẽ gửi vào done một lần, còn eng.Close() (defer của RunCase) sẽ close(done)——trả về sớm kích hoạt Close
// sẽ cạnh tranh đóng kênh với việc gửi của waitDone gây ra panic (send on closed channel). headless cũng dựa vào "Done trước
// Close sau". Đồng thời phải hút cạn Events và Stream, tránh làm nghẽn engine.
func drive(eng driveEngine, maxChapters int, opts RunOptions) error {
	var timeoutCh <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	aborted, timedOut := false, false
	// finish được gọi sau khi drain đến Done (hoặc kênh bị đóng): nếu timeout thì trả về error, nếu không thì kết thúc bình thường.
	finish := func() error {
		if timedOut {
			return fmt.Errorf("chạy quá thời gian (%s)", opts.Timeout)
		}
		return nil
	}
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return finish()
			}
			if opts.Progress != nil && strings.TrimSpace(ev.Summary) != "" {
				fmt.Fprintf(opts.Progress, "    [%s] %s\n", ev.Category, ev.Summary)
			}
			if !aborted && capReached(eng.Snapshot(), maxChapters) {
				eng.Abort()
				aborted = true
				timeoutCh = nil // Đã đạt điều kiện dừng, chuyển sang dọn dẹp bình thường, không còn bị ràng buộc bởi timeout (tránh phán đoán nhầm dừng thành công là timeout)
			}
		case <-eng.Stream():
			// Hút cạn luồng tăng dần, không tiêu thụ nội dung——eval không quan tâm luồng chính văn, chỉ nhìn vào sự thật đã lưu vào đĩa.
		case _, ok := <-eng.Done():
			if !ok {
				return finish()
			}
			return finish()
		case <-timeoutCh:
			eng.Abort() // Tại đây aborted chắc chắn là false (dừng do cap sẽ gán timeoutCh thành nil)
			aborted, timedOut = true, true
			timeoutCh = nil // Vô hiệu hóa bộ hẹn giờ, tiếp tục drain cho đến Done, sau đó finish trả về lỗi timeout
		}
	}
}

// capReached đánh giá xem đã đạt điều kiện dừng chưa. maxChapters>0 tính theo số chương đã hoàn thành; <=0 coi là "loại quy hoạch",
// hoàn thành quy hoạch (vào writing hoặc đã complete) là dừng.
func capReached(snap host.UISnapshot, maxChapters int) bool {
	if maxChapters <= 0 {
		return snap.Phase == string(domain.PhaseWriting) || snap.Phase == string(domain.PhaseComplete)
	}
	return snap.CompletedCount >= maxChapters
}