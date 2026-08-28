package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Setup khởi tạo slog logger mặc định.
// w là đích đầu ra của log, level là mức log tối thiểu.
func Setup(w io.Writer, level slog.Level) {
	slog.SetDefault(slog.New(newTextHandler(w, level)))
}

func newTextHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Giữ lại ngày, milli giây và múi giờ; log ghi thêm xuyên tiến trình vẫn căn chuẩn được với phiên bản mã và phiên làm việc.
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return a
		},
	})
}

func newSessionLogger(w io.Writer, level slog.Level, sessionAttrs ...slog.Attr) (*slog.Logger, string) {
	sessionID := fmt.Sprintf("%s-p%d", time.Now().Format("20060102T150405.000Z0700"), os.Getpid())
	attrs := make([]slog.Attr, 0, len(sessionAttrs)+1)
	attrs = append(attrs, slog.String("session", sessionID))
	attrs = append(attrs, sessionAttrs...)
	handler := newTextHandler(w, level).WithAttrs(attrs)
	return slog.New(handler), sessionID
}

// FileLogger trả về một logger độc lập ghi vào outputDir/logs/filename cùng hàm dọn dẹp,
// phục vụ các hệ con cần file log riêng (ví dụ luồng nhập liệu). Khi mở thất bại thì lùi về logger mặc định, không làm gián đoạn nghiệp vụ,
// nhưng lỗi phải trả về cho phía gọi để hiển thị cho người dùng — nếu không UI sẽ dẫn người dùng đến một file log không tồn tại.
func FileLogger(outputDir, filename string) (*slog.Logger, func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return slog.Default(), func() {}, err
	}
	logger, sessionID := newSessionLogger(f, slog.LevelDebug)
	logger.Info("bắt đầu phiên log", "module", "logger", "session_id", sessionID)
	return logger, func() {
		logger.Info("kết thúc phiên log", "module", "logger", "session_id", sessionID)
		_ = f.Close()
	}, nil
}

// SetupFile khởi tạo logger mặc định ghi ra file, trả về hàm dọn dẹp.
// alsoStderr=true thì đồng thời ghi ra stderr.
// Khi không mở được thư mục hoặc file log thì trả về lỗi, phía gọi bắt buộc xử lý tường minh; cấm chuyển sang io.Discard
// rồi chạy tiếp, nếu không sẽ mất toàn bộ log vận hành đúng lúc cần gỡ lỗi nhất.
func SetupFile(outputDir, filename string, alsoStderr bool, sessionAttrs ...slog.Attr) (func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return nil, err
	}

	var w io.Writer = f
	if alsoStderr {
		w = io.MultiWriter(os.Stderr, f)
	}
	previous := slog.Default()
	logger, sessionID := newSessionLogger(w, slog.LevelDebug, sessionAttrs...)
	slog.SetDefault(logger)
	logger.Info("bắt đầu phiên log", "module", "logger", "session_id", sessionID)

	return func() {
		logger.Info("kết thúc phiên log", "module", "logger", "session_id", sessionID)
		slog.SetDefault(previous)
		_ = f.Close()
	}, nil
}

func openLogFile(outputDir, filename string) (*os.File, error) {
	logPath := filepath.Join(outputDir, "logs", filename)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("tạo thư mục log %q: %w", filepath.Dir(logPath), err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("mở file log %q: %w", logPath, err)
	}
	return f, nil
}
