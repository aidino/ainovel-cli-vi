package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAllowsFilter(t *testing.T) {
	if New("", nil).allows(KindDeadlock) != true {
		t.Error("events mặc định nên cho phép tất cả")
	}
	n := New("", []string{KindRunEnd, KindBudget})
	if !n.allows(KindRunEnd) || !n.allows(KindBudget) {
		t.Error("列入的 kind 应放dòng ")
	}
	if n.allows(KindDeadlock) {
		t.Error("未列入的 kind 应chặn")
	}
	var nilN *Notifier
	if nilN.allows(KindRunEnd) {
		t.Error("nil Notifier 应chặn一切")
	}
	nilN.Send(Notification{Kind: KindRunEnd}) // không nên panic
}

func TestKindsAreUniqueAndKnown(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range Kinds() {
		if kind == "" || seen[kind] {
			t.Fatalf("通知事件名phải 非空và唯一: %q", kind)
		}
		seen[kind] = true
		if !IsKnownKind(kind) {
			t.Fatalf("Kinds 与 IsKnownKind không nhất quán: %q", kind)
		}
	}
	if IsKnownKind("repeat") {
		t.Fatal("旧 repeat 事件不应继续xuất hiện 在新契约中")
	}
}

func TestCommandChannelEnvAndStdin(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	jsonFile := filepath.Join(dir, "stdin.json")

	command := `echo "$NOTIFY_KIND|$NOTIFY_LEVEL|$NOTIFY_TITLE|$NOTIFY_BODY" > ` + shellQuote(envFile) + ` && cat > ` + shellQuote(jsonFile)
	if runtime.GOOS == "windows" {
		// Explicit UTF-8 (no BOM) so Chinese title/body survive PowerShell's default code page.
		command = `$utf8 = New-Object System.Text.UTF8Encoding $false; ` +
			`$line = "$env:NOTIFY_KIND|$env:NOTIFY_LEVEL|$env:NOTIFY_TITLE|$env:NOTIFY_BODY"; ` +
			`[System.IO.File]::WriteAllText(` + powerShellQuote(envFile) + `, $line, $utf8); ` +
			`$reader = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), $utf8); ` +
			`$payload = $reader.ReadToEnd(); ` +
			`[System.IO.File]::WriteAllText(` + powerShellQuote(jsonFile) + `, $payload, $utf8)`
	}
	n := New(command, nil)
	nt := Notification{Kind: KindBudget, Level: "warn", Title: "ainovel: ngân sách ", Body: "已花费 $8.00"}
	if err := n.deliverError(nt); err != nil {
		t.Fatalf("command thực thi thất bại: %v", err)
	}

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("command 未thực thi : %v", err)
	}
	if got := strings.TrimSpace(string(env)); got != "budget|warn|ainovel: ngân sách |已花费 $8.00" {
		t.Errorf("biến môi trường 传递không khớp : %q", got)
	}

	raw, err := os.ReadFile(jsonFile)
	if err != nil {
		t.Fatalf("stdin 未传递: %v", err)
	}
	var decoded Notification
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("stdin 非hợp lệ  JSON: %v", err)
	}
	if decoded != nt {
		t.Errorf("stdin JSON không khớp: %+v", decoded)
	}
}

func TestCommandChannelTimeoutKill(t *testing.T) {
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	n := New(command, nil)
	n.timeout = 200 * time.Millisecond

	start := time.Now()
	err := n.deliverError(Notification{Kind: KindRunEnd})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hết giờ 命令nên trả về context deadline exceeded，got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("hết giờ 未强杀, chặn  %v", elapsed)
	}
}

func TestFindPowerShellPrefersPwsh(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell 选择仅适用于 Windows")
	}
	want, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("pwsh.exe 未安装，仅xác minh Windows PowerShell 兼容đường dẫn")
	}
	got, err := findPowerShell()
	if err != nil {
		t.Fatalf("findPowerShell: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)) {
		t.Fatalf("应优先 pwsh.exe，got %q, want %q", got, want)
	}
}

func TestWindowsNotificationScriptUsesEnvironmentWithoutInterpolation(t *testing.T) {
	for _, want := range []string{"$env:NOTIFY_TITLE", "$env:NOTIFY_BODY", "$env:NOTIFY_LEVEL", "ShowBalloonTip"} {
		if !strings.Contains(windowsNotificationScript, want) {
			t.Fatalf("Windows notification script missing %q", want)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func powerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
