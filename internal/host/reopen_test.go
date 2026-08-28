package host

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// TestHostReopen 守护 /reopen 的người dùng 级重开出口：完本是重决策，重开只能由người dùng 显式
// 发起——未hoàn kết từ chối、đang chạy từ chối；重开thành công把 phase 回退 writing，附带的续写方向登记为
// 待xử lý 干预（PendingSteer），khôi phục 时先经 Arbiter phán quyết 注入再续跑。
func TestHostReopen(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	h := &Host{store: st, events: make(chan Event, 8)}

	if err := st.Progress.Init(2); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen(""); err == nil {
		t.Fatal("未hoàn kết 的书应từ chối重开")
	}

	_ = st.Progress.UpdatePhase(domain.PhaseWriting)
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen("以八十年大限开新tập"); err != nil {
		t.Fatalf("hoàn kết 书重开应thành công：%v", err)
	}
	p, _ := st.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("重开后 phase nên là writing，得 %s", p.Phase)
	}
	if len(p.PendingRewrites) != 0 || p.ReopenedFromComplete {
		t.Fatalf("续写重开不得携带làm lại 语义：%+v", p)
	}
	// 重开计数phải 落盘：再hoàn kết 的 progress digest 才会与上次khác nhau ——checkpoint 同 digest
	// 幂等去重，chữ 节giống nhau 的再hoàn kết 无新 checkpoint，StopGuard 会把thành công完本误判trống转终止。
	if p.ReopenCount != 1 {
		t.Fatalf("重开计数nên là 1，得 %d", p.ReopenCount)
	}
	meta, _ := st.RunMeta.Load()
	if meta == nil || !strings.Contains(meta.PendingSteer, "八十年大限") {
		t.Fatalf("续写方向应登记为待xử lý 干预，得 %+v", meta)
	}

	running := &Host{store: st, lifecycle: lifecycleRunning, events: make(chan Event, 1)}
	if err := running.Reopen(""); err == nil {
		t.Fatal("引擎đang chạy 应từ chối重开")
	}
}
