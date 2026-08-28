// Package notify cung cấp kênh cảnh báo không cần người trực.
//
// Định vị hợp hiến (architecture.md §2.3): hành động thuần tầng quan sát — cảnh báo không bao giờ can thiệp vào luồng điều khiển
// (không thử lại, không điều lại, không dừng máy), chỉ là "hô" các sự kiện sẵn có trong TUI ra ngoài màn hình.
// Send chạy bất đồng bộ, không bao giờ chặn Host, thất bại chỉ ghi slog.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notification là toàn bộ dữ kiện của một cảnh báo.
type Notification struct {
	Kind  string `json:"kind"`  // Kinds trả về tên sự kiện ổn định
	Level string `json:"level"` // info / warn / error
	Title string `json:"title"`
	Body  string `json:"body"`
}

const (
	KindRunEnd        = "run_end"
	KindBudget        = "budget"
	KindAdvanceGate   = "advance_gate"
	KindStopGuard     = "stop_guard"
	KindPlanStart     = "plan_start"
	KindDeadlock      = "deadlock"
	KindWorkerFailure = "worker_failure"
)

// Kinds trả về toàn bộ tên sự kiện có thể dùng cho notify.events ở phiên bản hiện tại.
// Đây là nguồn sự thật duy nhất của hợp đồng sự kiện thông báo.
func Kinds() []string {
	return []string{
		KindRunEnd,
		KindBudget,
		KindAdvanceGate,
		KindStopGuard,
		KindPlanStart,
		KindDeadlock,
		KindWorkerFailure,
	}
}

func IsKnownKind(kind string) bool {
	for _, known := range Kinds() {
		if kind == known {
			return true
		}
	}
	return false
}

// Notifier phân phát thông báo theo cấu hình. Giá trị zero không dùng được, bắt buộc tạo qua New; an toàn với nil (Send noop).
type Notifier struct {
	command string          // khi khác rỗng thì thay kênh system (push điện thoại đi đây)
	events  map[string]bool // nil = cho qua mọi kind
	timeout time.Duration
}

// New tạo Notifier. command rỗng thì dùng kênh system tích hợp (bong bóng thông báo Windows /
// macOS osascript / Linux notify-send); events khác rỗng thì chỉ cho qua các kind được liệt kê.
func New(command string, events []string) *Notifier {
	n := &Notifier{command: strings.TrimSpace(command), timeout: 10 * time.Second}
	if len(events) > 0 {
		n.events = make(map[string]bool, len(events))
		for _, ev := range events {
			n.events[ev] = true
		}
	}
	return n
}

// Send gửi một thông báo bất đồng bộ. Việc lọc, thực thi, xử lý thất bại đều không ảnh hưởng phía gọi.
func (n *Notifier) Send(nt Notification) {
	if !n.allows(nt.Kind) {
		return
	}
	go n.deliver(nt)
}

// allows trả về liệu kind đó có được cho qua hay không (chặn khi Notifier nil / không nằm trong events).
func (n *Notifier) allows(kind string) bool {
	if n == nil {
		return false
	}
	return n.events == nil || n.events[kind]
}

// deliver thực thi một lần gửi đồng bộ và ghi lại thất bại, được Send gọi trong goroutine.
func (n *Notifier) deliver(nt Notification) {
	if err := n.deliverError(nt); err != nil {
		slog.Warn("gửi thông báo thất bại", "module", "notify", "kind", nt.Kind, "err", err)
	}
}

// deliverError thực thi một lần gửi đồng bộ và trả về lỗi gốc. Send
// gọi deliver để ghi thất bại; test gọi thẳng phương thức này để tránh lỗi bị che bởi triệu chứng thứ cấp.
func (n *Notifier) deliverError(nt Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	if n.command != "" {
		return runCommand(ctx, n.command, nt)
	}
	return runSystem(ctx, nt)
}

// runCommand thực thi lệnh người dùng cấu hình: các trường truyền qua biến môi trường (một dòng curl không dependency, không rủi ro chèn lệnh
// ), JSON đầy đủ đồng thời ghi vào stdin (tự phân tích ở kịch bản phân phát phức tạp). Vượt thời gian do ctx cưỡng chế.
func runCommand(ctx context.Context, command string, nt Notification) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		powershell, err := findPowerShell()
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Env = notificationEnv(nt)
	payload, _ := json.Marshal(nt)
	cmd.Stdin = strings.NewReader(string(payload))
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("lệnh thông báo vượt thời gian chờ: %w", ctxErr)
		}
		return err
	}
	return nil
}

func notificationEnv(nt Notification) []string {
	return append(os.Environ(),
		"NOTIFY_KIND="+nt.Kind,
		"NOTIFY_LEVEL="+nt.Level,
		"NOTIFY_TITLE="+nt.Title,
		"NOTIFY_BODY="+nt.Body,
	)
}

// runSystem thông báo desktop tích hợp: chỉ bao phủ kịch bản "người đang ở cạnh máy", không tìm thấy lệnh thì im lặng hạ cấp.
func runSystem(ctx context.Context, nt Notification) error {
	switch runtime.GOOS {
	case "windows":
		return runWindowsNotification(ctx, nt)
	case "darwin":
		script := "display notification " + appleScriptString(nt.Body) + " with title " + appleScriptString(nt.Title)
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			slog.Info("thông báo hạ cấp thành log (không có notify-send)", "module", "notify", "title", nt.Title, "body", nt.Body)
			return nil
		}
		return exec.CommandContext(ctx, "notify-send", nt.Title, nt.Body).Run()
	default:
		slog.Info("thông báo hạ cấp thành log (nền tảng không có kênh system)", "module", "notify", "title", nt.Title, "body", nt.Body)
		return nil
	}
}

// runWindowsNotification dùng PowerShell + WinForms NotifyIcon sẵn có của hệ thống.
// Windows 10/11 hiển thị bong bóng ở góc trên bên phải và tích hợp vào trải nghiệm thông báo hệ thống; không cần cài module, đăng ký ứng dụng
// hay kèm theo binary bổ sung. Phía gọi vốn đã chạy bất đồng bộ, giữ tiến trình ngắn chỉ để hệ thống nhận được thông điệp bong bóng.
func runWindowsNotification(ctx context.Context, nt Notification) error {
	powershell, err := findPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsNotificationScript)
	cmd.Env = notificationEnv(nt)
	return cmd.Run()
}

func findPowerShell() (string, error) {
	// Ưu tiên PowerShell 7: pwsh ổn định hơn khi
	// chuyển hướng stdin trong môi trường GitHub Windows runner và Windows hiện đại; Windows PowerShell 5.1 chỉ làm dự phòng tương thích.
	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("Thông báo Windows cần PowerShell, nhưng hệ thống không tìm thấy powershell.exe hoặc pwsh.exe")
}

const windowsNotificationScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$notify = New-Object System.Windows.Forms.NotifyIcon
$notify.Icon = [System.Drawing.SystemIcons]::Information
$notify.BalloonTipTitle = $env:NOTIFY_TITLE
$notify.BalloonTipText = $env:NOTIFY_BODY
$notify.BalloonTipIcon = switch ($env:NOTIFY_LEVEL) {
  'error' { [System.Windows.Forms.ToolTipIcon]::Error; break }
  'warn'  { [System.Windows.Forms.ToolTipIcon]::Warning; break }
  default { [System.Windows.Forms.ToolTipIcon]::Info }
}
$notify.Visible = $true
$notify.ShowBalloonTip(4000)
Start-Sleep -Milliseconds 4500
$notify.Dispose()`

// appleScriptString bọc văn bản bất kỳ thành chuỗi ký tự AppleScript.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}