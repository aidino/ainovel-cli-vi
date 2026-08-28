package tui

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// Run khởi động TUI.
// Quy ước phân tầng chế độ khởi động:
// 1. Chế độ nhanh, chế độ đồng sáng tạo thuộc "quy hoạch khởi động";
// 2. Phiên sáng tác chính thức vào host.Host;
// 3. Tương lai nếu thêm các chế độ chia sẻ như "tiếp tục viết tiểu thuyết hiện có", thống nhất đưa vào internal/entry/startup.
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info) error {
	rt, err := host.New(cfg, bundle, host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	))
	if err != nil {
		return err
	}
	defer rt.Close()

	m := NewModel(rt, build.Version)
	if logErr := rt.FileLogError(); logErr != nil {
		logWarning := fmt.Errorf("Log tệp không khả dụng, tiếp tục sử dụng log terminal: %w", logErr)
		m.err = logWarning
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: logWarning.Error(), Detail: logWarning.Error(),
		})
	}
	// Không bật báo cáo chuột toàn cục khi khởi động: trang chào mừng không dùng chuột, tắt báo cáo có thể giữ nguyên tính năng
	// kéo chọn sao chép gốc của terminal. Khi vào bàn làm việc sáng tác (modeRunning) thì enterRunning mới bật báo cáo,
	// để hỗ trợ click chuyển bảng / lăn chuột / kéo thanh bên.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
