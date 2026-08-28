package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newFlagTestHost 造一个最小 Host，只够驱动 cocreating 标记trạng thái机与并发守卫。
// emitEvent 使用非chặn 通道，缓冲 events 即可，无需 observer。
// PauseForCoCreate 的chạy 态分支会调 Engine Abort（复用已xác minh的 Esc tạm dừng đường dẫn），
// 不在此单测；这里只ghi đè非chạy 态与标记/守卫逻辑。
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		engine:     &engine{}, // acquireExclusive kiểm tra engine.isRunning() (cửa kiểm soát dừng)
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("idle 态应cho phép 进入đồng sáng tác giai đoạn")
	}
	if !h.cocreating {
		t.Error("进入后 cocreating nên là true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("非chạy 态进入不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("全书完成后不应cho phép 进入đồng sáng tác giai đoạn")
	}
	if h.cocreating {
		t.Error("từ chối后不应置位 cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("已在共创中应từ chối重入")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("取消后 cocreating 应清空")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("取消不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // không nên panic，不应改trạng thái
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("非共创态 CancelCoCreate nên là no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("空 draft nên báo lỗi")
	}
	if !h.cocreating {
		t.Error("空 draft 在清标记前trả về ，cocreating 应保持 true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## 后续走向\n- 进入第二tập")
	if err == nil || !strings.Contains(err.Error(), "not in co-create") {
		t.Fatalf("非共创态应报 not in co-create，得 %v", err)
	}
}

func TestAcquireExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		exclusive  string
		wantErr    string // trống=kỳ vọng放dòng 
	}{
		{"running", lifecycleRunning, false, "", "đang chạy "},
		{"cocreating", lifecyclePaused, true, "", "đồng sáng tác giai đoạn"},
		{"busy", lifecycleIdle, false, "nhập ", "đang diễn ra"},
		{"idle free", lifecycleIdle, false, "", ""},
		{"paused free", lifecyclePaused, false, "", ""},
	}
	// Abort dừng 窗口：lifecycle 已置 paused 但引擎 goroutine 尚未退净，仍须từ chối——
	// 否则nhập 会与引擎收尾并发写同一 store。
	drain := newFlagTestHost(lifecyclePaused, false)
	drain.engine.running = true
	if err := drain.acquireExclusive("nhập "); err == nil {
		t.Fatal("引擎排水期应từ chối独占作业")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			h.exclusive = c.exclusive
			err := h.acquireExclusive("nhập ")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("应放dòng ，得 %v", err)
				}
				if h.exclusive != "nhập " {
					t.Fatalf("放dòng 后应登记占用，得 %q", h.exclusive)
				}
				h.releaseExclusive()
				if h.exclusive != "" {
					t.Fatalf("释放后占用应清空，得 %q", h.exclusive)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("应含 %q，得 %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "nhập ") {
				t.Errorf("lỗi 文案应带 action %q，得 %v", "nhập ", err)
			}
		})
	}
}

// TestExclusiveBlocksCreationEntries 守护 #2：后台独占作业（nhập /仿写）đang diễn ra时，
// 不仅第二个后台作业被堵，创作ghi 口（Continue/Resume）与新后台作业也phải 被堵，
// 否则 Continue 会在引擎被门禁拦下前就让 Arbiter 改trạng thái、Resume/next 期间引擎可抢跑。
func TestExclusiveBlocksCreationEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	h.exclusive = "nhập "
	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("独占作业期间 ImportFrom nên bị từ chối")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("独占作业期间 Continue nên bị từ chối（须在 Arbiter phán quyết 前挡住）")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("独占作业期间 Resume nên bị từ chối")
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries xác minh共创窗口内独占性入口全部被堵：
// import/start/resume/continue 在 cocreating 期间都nên bị từ chối，补上 paused 期只查 ==running 的缺口。
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("进入đồng sáng tác giai đoạnthất bại")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("共创窗口内 ImportFrom nên bị từ chối")
	}
	if err := h.StartPrepared("写个新故事"); err == nil {
		t.Error("共创窗口内 StartPrepared nên bị từ chối")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("共创窗口内 Resume nên bị từ chối")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("共创窗口内 Continue nên bị từ chối")
	}

	// 退出共创后占用解除（这里走 Cancel；Resume 干预đường dẫn归集成xác minh）
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("退出后占用标记应解除")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store nên trả về空串，得 %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(100); err != nil {
		t.Fatal(err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "影之诗", Synopsis: "少年追索失落的影子。"}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "主角登临绝巅",
		OpenThreads:     []string{"师门血仇未报"},
		EstimatedScale:  "预计 4-6 tập",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"影之诗", "đã hoàn thành 3 chương", "chương tiếp theo là chương 4", "主角登临绝巅", "师门血仇未报", "预计 4-6 tập"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要应含 %q，thực tế :\n%s", want, got)
		}
	}
}

func TestBuildStoryStateSummaryUsesDynamicPlanningWording(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(66); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "tập一", Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "一"}, {Title: "二"}}},
			{Index: 2, EstimatedChapters: 64},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Layered = true
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	if !strings.Contains(got, "hiện đã chi tiết hóa 2 chương (sau này quy hoạch động theo arc)") {
		t.Fatalf("động quy hoạch 摘要口径lỗi :\n%s", got)
	}
	if strings.Contains(got, "66") || strings.Contains(got, "quy hoạch  2 chương ") {
		t.Fatalf("động quy hoạch 摘要不得暗示固定总chương 数:\n%s", got)
	}
}
