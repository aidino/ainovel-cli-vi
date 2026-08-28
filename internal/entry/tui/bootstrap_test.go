package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

func TestBootstrapExistingBookFailureStaysInWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{existing: true, err: errors.New("di chuyển thất bại")})
	if !handled || cmd == nil {
		t.Fatal("đã có 作品khôi phục thất bại仍应刷新bảng làm việc ")
	}
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("đã có 作品khôi phục thất bại后应留在bảng làm việc ，得 mode=%v", got.mode)
	}
	if got.err == nil || got.err.Error() != "di chuyển thất bại" {
		t.Fatalf("bảng làm việc 应展示原始lỗi ，得 %v", got.err)
	}
}

// TestBootstrapCompletedBookLandsOnDoneWorkbench 守护hoàn kết 书的khởi động 落点：resumeLabel 对
// complete trả về nhãn trống, hành vi cũ đưa về trang chào mừng - trang chào mừng không nhắc gì đến sách đã có, người dùng sẽ tưởng sách bị mất,
// và /reopen、/export、làm lại 输入的自然位置都在完成态bảng làm việc 。
func TestBootstrapCompletedBookLandsOnDoneWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if !handled || cmd == nil {
		t.Fatal("completed bootstrap 应被xử lý 并trả về 命令")
	}
	got := next.(Model)
	if got.mode != modeDone {
		t.Fatalf("hoàn kết 书应落完成态bảng làm việc ，得 mode=%v", got.mode)
	}
	if got.textarea.Placeholder != donePlaceholder {
		t.Fatalf("应给出完成态引导（含 /reopen），得 %q", got.textarea.Placeholder)
	}

	// 已在bảng làm việc （如会话内hoàn kết 后又收到 bootstrap）不得被lặp lại 切态。
	m = Model{mode: modeRunning, textarea: textarea.New()}
	next, _, _ = m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if next.(Model).mode != modeRunning {
		t.Fatal("非欢迎trang 不应被 completed bootstrap 切态")
	}
}
