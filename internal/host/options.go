package host

import "log/slog"

type newOptions struct {
	logFile       string
	logAlsoStderr bool
	logAttrs      []slog.Attr
}

// NewOption cấu hình quá trình khởi tạo Host, tài nguyên runtime vẫn do Host nắm giữ.
type NewOption func(*newOptions)

// WithFileLog để Host giữ một phiên log runtime. Log chỉ mở sau khi nhận được quyền thuê thư mục tiểu thuyết,
// và đóng sau khi Host hoàn tất việc đóng toàn bộ log. Nếu mở thất bại, tiếp tục dùng logger của tiến trình hiện tại,
// bên gọi phải xử lý lỗi này rõ ràng qua FileLogError.
func WithFileLog(filename string, alsoStderr bool, attrs ...slog.Attr) NewOption {
	return func(opts *newOptions) {
		opts.logFile = filename
		opts.logAlsoStderr = alsoStderr
		opts.logAttrs = append([]slog.Attr(nil), attrs...)
	}
}
