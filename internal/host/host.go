package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	runtimelog "github.com/voocel/ainovel-cli/internal/logger"
	modelreg "github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/revision"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

// Host là lớp vỏ bao bọc runtime: vòng đời/cửa vào can thiệp/chiếu sự kiện/quản lý model.
// Việc điều phối và thực thi nằm ở engine (vòng lặp tất định); phán quyết ngữ nghĩa nằm ở arbiter (LLM-as-function).
type Host struct {
	cfg             bootstrap.Config
	bundle          assets.Bundle
	store           *storepkg.Store
	bookLease       *bookLease
	styleStats      *tools.StyleStatsIndex
	models          *bootstrap.ModelSet
	engine          *engine
	thinkingApplier agents.ApplyThinking // Liên kết các Worker khi điều chỉnh cường độ suy luận trong /model
	writerRestore   *ctxpack.WriterRestorePack
	userRules       *userrules.Service
	observer        *observer
	usage           *UsageTracker
	usageCancel     context.CancelFunc  // Dừng autoSaveLoop và kích hoạt lần flush cuối cùng
	budget          *BudgetSentinel     // Chính sách ngân sách; nếu chưa kích hoạt thì nil (các phương thức an toàn với nil)
	gate            *ChapterAdvanceGate // Component chính sách hợp nhất giữa việc cấp phép chương và tạm dừng một lần
	notifier        *notify.Notifier    // Cảnh báo chạy ngầm không người trực; nếu chưa kích hoạt thì nil (Send an toàn với nil)
	configPath      string              // Đích ghi đĩa cấu hình: /config, /model ghi bản hiện tại đang dùng (nếu có cấp độ dự án thì ghi vào đó, không thì ghi toàn cục)
	logCleanup      func()
	fileLogErr      error

	events   chan Event
	streamCh chan string
	done     chan struct{}

	mu         sync.Mutex
	lifecycle  lifecycle
	cocreating bool   // Chiếm dụng đồng sáng tác giai đoạn: chặn sự can thiệp đồng thời của import/simulate/continue trong cửa sổ paused
	exclusive  string // Chiếm dụng tác vụ độc quyền chạy nền (nhập/mô phỏng/sửa đổi): chuỗi khác rỗng biểu thị có tác vụ đang chạy, chặn các truy cập độc quyền đồng thời khác
	// exclusiveCancel là hàm hủy của tác vụ độc quyền hiện tại: dừng cứng do ngân sách/tạm dừng thủ công phải có khả năng dừng cả tiến trình nhập liệu
	// đang đốt tiền, chứ không chỉ Engine - abortWithEvent sẽ hủy nó khi Engine chưa chạy (cơ chế dừng
	// dùng chung giữa callback abort của lính gác ngân sách và Abort thủ công). releaseExclusive sẽ dọn dẹp nó.
	exclusiveCancel context.CancelFunc
	closeOnce       sync.Once
	asyncWG         sync.WaitGroup
	closing         bool

	interMu sync.Mutex // Phán quyết can thiệp theo tuần tự FIFO (cùng một thời điểm tối đa chỉ có một yêu cầu tư vấn đang thực hiện)

	outputMu     sync.RWMutex
	outputClosed bool

	// runCtx ràng buộc việc gọi phán quyết LLM (phán quyết khởi động/phân loại can thiệp) ở phía host; Close sẽ hủy nó,
	// tránh tình trạng khi thoát vẫn còn phán quyết đang chờ mà không thể ngắt.
	runCtx    context.Context
	runCancel context.CancelFunc
}

type lifecycle string

const (
	lifecycleIdle      lifecycle = "idle"
	lifecycleRunning   lifecycle = "running"
	lifecyclePaused    lifecycle = "paused"
	lifecycleCompleted lifecycle = "completed"
)

// New tạo ra Host mới.
func New(cfg bootstrap.Config, bundle assets.Bundle, options ...NewOption) (*Host, error) {
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, err
	}
	var opts newOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}

	bookLease, err := acquireBookLease(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	keepBookLease := false
	var logCleanup func()
	defer func() {
		if keepBookLease {
			return
		}
		if err := bookLease.Close(); err != nil {
			slog.Error("giải phóng quyền chiếm dụng thư mục tiểu thuyết thất bại", "module", "host", "dir", cfg.OutputDir, "err", err)
		}
		if logCleanup != nil {
			logCleanup()
		}
	}()

	var fileLogErr error
	if opts.logFile != "" {
		logCleanup, fileLogErr = runtimelog.SetupFile(cfg.OutputDir, opts.logFile, opts.logAlsoStderr, opts.logAttrs...)
		if fileLogErr != nil {
			logCleanup = nil
			slog.Warn("log file không dùng được, tiếp tục dùng log tiến trình hiện tại", "module", "host", "file", opts.logFile, "err", fileLogErr)
		}
	}

	slog.Info("khởi động", "module", "boot", "provider", cfg.Provider, "model", cfg.ModelName, "output", cfg.OutputDir)

	store := storepkg.NewStore(cfg.OutputDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}
	if err := upgradeProject(store); err != nil {
		return nil, err
	}
	// RunMeta là nguồn sự thật cho mọi ngữ nghĩa kiểm soát, bắt buộc phải hoàn thành kiểm tra hợp lệ trước khi cấu trúc model/tác vụ nền.
	// Advance mode không xác định sẽ trực tiếp trả về lỗi cấu trúc; cấm việc phỏng đoán giáng cấp rồi tiếp tục ghi đĩa.
	if err := store.RunMeta.Init(cfg.Style, cfg.Provider, cfg.ModelName); err != nil {
		return nil, fmt.Errorf("init run meta: %w", err)
	}
	// Khởi chạy goroutine nền để làm mới metadata model (cửa sổ/giá cả) từ OpenRouter, cache trên đĩa 24 giờ.
	modelreg.StartPricingRefresh(modelreg.DefaultRegistry(), bootstrap.DefaultConfigDir())

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("create models: %w", err)
	}
	slog.Info("model sẵn sàng", "module", "boot", "summary", models.Summary())

	usage := NewUsageTracker(models, store)
	// Ưu tiên đọc từ meta/usage.json; các trường hợp sau đây đều dùng sessions/*.jsonl để lấp lại một lần:
	//   - File không tồn tại (trước khi lưu đĩa lần đầu)
	//   - Phiên bản schema không khớp (vứt bỏ định dạng cũ sau khi nâng cấp trong tương lai)
	//   - File tồn tại nhưng bị hỏng / lỗi IO (không thể để dữ liệu hỏng làm tích lũy bị reset vĩnh viễn)
	// Lấp lại xong lập tức SaveNow, cố định kết quả, lần sau khởi động sẽ Load trúng trực tiếp.
	loaded, loadErr := usage.LoadFromStore()
	if loadErr != nil {
		slog.Warn("tải usage thất bại, sẽ thử lấp lại từ sessions", "module", "usage", "err", loadErr)
	}
	if !loaded {
		if n, err := usage.ReplaySessions(cfg.OutputDir); err != nil {
			slog.Warn("usage replay thất bại", "module", "usage", "err", err)
		} else if n > 0 {
			slog.Info("usage đã lấp lại xong từ session", "module", "usage", "messages", n)
			if err := usage.SaveNow(); err != nil {
				slog.Warn("lưu usage sau lấp lại thất bại", "module", "usage", "err", err)
			}
		}
	}
	usageCtx, usageCancel := context.WithCancel(context.Background())
	usage.StartAutoSave(usageCtx)

	// onGuardBlock Khai báo trước: h phải được tạo xong thì mới gán được closure đẩy sự kiện lên giao diện.
	var onGuardBlock func(agent, reason string, consecutive int32)
	styleStats := tools.NewStyleStatsIndex(store)
	workers, restore, applyThinking := agents.BuildWorkers(cfg, store, styleStats, models, bundle, usage.Record,
		func(agent, reason string, consecutive int32) {
			if onGuardBlock != nil {
				onGuardBlock(agent, reason, consecutive)
			}
		})
	store.Signals.ClearStaleSignals()

	h := &Host{
		cfg:             cfg,
		bundle:          bundle,
		store:           store,
		bookLease:       bookLease,
		styleStats:      styleStats,
		models:          models,
		thinkingApplier: applyThinking,
		writerRestore:   restore,
		userRules:       userrules.NewService(store, models.Default, rules.DefaultOptions()),
		usage:           usage,
		usageCancel:     usageCancel,
		configPath:      bootstrap.EffectiveConfigPath(),
		logCleanup:      logCleanup,
		fileLogErr:      fileLogErr,
		events:          make(chan Event, 100),
		streamCh:        make(chan string, 256),
		done:            make(chan struct{}, 4),
		lifecycle:       lifecycleIdle,
	}
	h.runCtx, h.runCancel = context.WithCancel(context.Background())
	h.observer = newObserver(store, h.emitEvent, h.emitDelta, h.emitClear)
	// Arbiter ở phía host và Worker dùng chung một chuỗi ToolProgress → observer → bàn làm việc.
	h.runCtx = agentcore.WithToolProgress(h.runCtx, h.observer.workerProgress)
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	}
	// Lính gác ngân sách: Engine gọi trực tiếp HandleBoundary ở mỗi ranh giới vòng lặp (không qua đăng ký sự kiện nữa).
	if sentinel := NewBudgetSentinel(cfg.Budget,
		func() float64 { c, _, _, _, _ := usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: level, Title: "ainovel: Ngân sách", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		usage.SetOnCost(sentinel.OnCost)
		// Cảnh báo vùng mù tính phí: khi model không báo cáo usage thì chi phí luôn bằng 0, ngân sách không bao giờ kích hoạt - cầu chì chưa được nối phải gọi người ngay.
		usage.SetOnMissingUsage(func() {
			const blind = "vùng mù ngân sách: model không trả dữ liệu usage, thống kê chi phí là 0, trần ngân sách sẽ không kích hoạt (model tùy chỉnh hãy xác nhận giá registry hoặc include_usage thượng nguồn)"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: "warn", Title: "ainovel: Ngân sách", Body: blind})
		})
	}
	// Cổng đẩy tiến hợp nhất: thực thi hold một lần, và ngăn chặn chương mới không có giấy phép trong chế độ review.
	h.gate = NewChapterAdvanceGate(store,
		func(reason string) {
			h.abortWithEvent(reason, "info")
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: "info", Title: "ainovel: chờ duyệt", Body: reason})
		},
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: level, Title: "ainovel: đẩy chương", Body: summary})
		},
	)
	// StopGuard chặn sự kiện hiển thị lên giao diện: blocked là hành động tự phục hồi tần suất cao, chỉ đưa vào luồng sự kiện trong màn hình (đẩy thông báo sẽ làm trôi màn hình);
	// escalated / hard_stop có nghĩa là tác vụ con lượt này bị bỏ đi, sự kiện + notify sẽ được phát ra theo cặp (kiến trúc §2.3).
	onGuardBlock = func(agent, reason string, n int32) {
		switch reason {
		case "escalated":
			body := fmt.Sprintf("%s quay rỗng %d lần liên tiếp không ghi sản phẩm cần thiết xuống đĩa, tác vụ lượt này chấm dứt, giao lại Engine xử lý", agent, n)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		case "hard_stop":
			body := fmt.Sprintf("%s bị provider từ chối trả lời (safety/content_filter), tác vụ lượt này chấm dứt ngay", agent)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		default: // blocked
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent,
				Summary: fmt.Sprintf("StopGuard: %s chưa hoàn thành sản phẩm cần thiết đã định kết thúc, đã chặn và thúc giục (lần thứ %d liên tiếp)", agent, n), Level: "info"})
		}
	}
	// Engine: động cơ thực thi tất định (docs/engine-rfc.md). arbiter dùng Default model (hạn chế tạm thời,
	// xem engine-arbiter.md §4.2).
	h.engine = &engine{
		store:           store,
		workers:         workers,
		arbiterModel:    newUsageTrackedModel(models.Default, "arbiter", usage.Record),
		failurePrompt:   bundle.Prompts.ArbiterFailure,
		planStartPrompt: bundle.Prompts.ArbiterPlanStart,
		style:           cfg.Style,
		// Đồng bộ hỏi lại: chặn vòng lặp động cơ một lần phán quyết (vài giây), đổi lấy "can thiệp có hiệu lực trước các sáng tác tiếp theo".
		reconsult: h.handleIntervention,
		observer:  h.observer,
		budget:    h.budget,
		gate:      h.gate,
		refresh:   h.refreshWriterRestore,
		emitEvent: h.emitEvent,
		notify: func(kind, level, title, body string) {
			h.notifier.Send(notify.Notification{Kind: kind, Level: level, Title: title, Body: body})
		},
		onPause: func(summary string) { h.abortWithEvent(summary, "warn") },
		onDone:  h.runEnded,
	}

	keepBookLease = true
	return h, nil
}

// ── Vòng đời ──

// PrepareUserRules Tạo snapshot quy tắc người dùng của cuốn sách này trong chế độ tạo mới (tất định ở phía khởi động, không đi vào Run sáng tác chính).
//
// Tham số đầu vào là yêu cầu sáng tác **nguyên bản** của người dùng (chưa qua đóng gói BuildStartPrompt) —— quá trình chuẩn hóa cần chính quy tắc người dùng,
// không phải bộ khung khởi động. Cửa vào phải được gọi một lần trước StartPrepared (cả hai đường dẫn tạo mới quick/cocreate đều đi qua đây).
//
// Chuẩn hóa thất bại chỉ giáng cấp không báo lỗi (đường dẫn tăng cường); chỉ khi không thể ghi đĩa snapshot mới trả về error để ngừng mở sách ——
// quá trình chạy tiếp theo sẽ không có nguồn sự thật ổn định (xem thiết kế §Thất bại và giáng cấp).
func (h *Host) PrepareUserRules(rawPrompt string) error {
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.Build(context.Background(), rawPrompt)
	if err != nil {
		return fmt.Errorf("ghi ảnh chụp quy tắc người dùng xuống đĩa thất bại, không thể tiếp tục: %w", err)
	}
	logUserRulesSnapshot(snap)
	return nil
}

// ensureUserRules Đảm bảo snapshot tồn tại trong đường dẫn khôi phục; nếu thiếu thì tạo theo
// system_defaults + rules file.
func (h *Host) ensureUserRules() {
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.GetOrBuild(context.Background())
	if err != nil {
		slog.Warn("đọc/tạo ảnh chụp quy tắc người dùng thất bại, runtime sẽ lùi về mặc định nội bộ", "module", "rules", "err", err)
		return
	}
	logUserRulesSnapshot(snap)
}

// logUserRulesSnapshot Echo lúc khởi động: cho người dùng thấy hệ thống đã hiểu các quy tắc là gì (tái sử dụng log, không thêm cơ chế mới).
func logUserRulesSnapshot(snap *rules.Snapshot) {
	if snap == nil {
		return
	}
	slog.Info("ảnh chụp quy tắc người dùng",
		"module", "rules",
		"status", string(snap.Status),
		"nguồn", snap.Sources,
		"cụm từ cấm", len(snap.Structured.ForbiddenPhrases),
		"từ mệt mỏi", len(snap.Structured.FatigueWords),
	)
	if snap.Status == rules.StatusDegraded {
		slog.Warn("một số quy tắc chưa phân tích được, đã chạy theo raw preferences (có thể tạo lại ảnh chụp)",
			"module", "rules", "uncertain", snap.Uncertain)
	}
}

// StartPrepared Bắt đầu sáng tác bằng yêu cầu sáng tác **nguyên bản** của người dùng: phán quyết plan_start sẽ chọn quy hoạch sư và mở rộng
// nhu cầu, kết quả phán quyết sẽ được cố định thành
// sự thật (PlanStartRecord) trước rồi mới khởi động Engine —— khôi phục luôn phụ thuộc vào sự thật đã ghi đĩa, không làm lại phán quyết đã có.
// Sự thật đầu vào (StartPrompt) được ghi đĩa trước khi phán quyết: khi phán quyết thất bại, nó là căn cứ để động cơ phán quyết bù,
// khởi động thất bại có thể tự phục hồi từ bất kỳ cửa vào khôi phục nào (Resume/Tiếp tục), không phải là ngõ cụt.
func (h *Host) StartPrepared(rawRequirement string) error {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng kết thúc đồng sáng tác trước")
	}
	h.mu.Unlock()

	rawRequirement = strings.TrimSpace(rawRequirement)
	if rawRequirement == "" {
		return fmt.Errorf("prompt is required")
	}
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	if err := h.store.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("reset checkpoints: %w", err)
	}
	if err := h.store.Progress.Init(0); err != nil {
		return fmt.Errorf("init progress: %w", err)
	}
	// Sự thật đầu vào ghi đĩa trước phán quyết: sau khi phán quyết thất bại (lỗi model v.v...), StartPrompt vẫn còn đó,
	// khi khôi phục/tiếp tục, động cơ sẽ dựa vào đó để phán quyết bù (planStartFallback), khởi động thất bại không còn là ngõ cụt.
	if err := h.store.RunMeta.SetStartPrompt(rawRequirement); err != nil {
		return fmt.Errorf("ghi yêu cầu sáng tác: %w", err)
	}

	// Khởi động phán quyết: thất bại thì báo lỗi rõ ràng và dừng lại (người dùng đang có mặt lúc khởi động, báo lỗi tốt hơn là phỏng đoán).
	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "phán quyết khởi động", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterPlanStart, rawRequirement, h.cfg.Style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: rawRequirement,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	var recErr error
	if rec, recErr = h.store.Decisions.Append(rec); recErr != nil {
		slog.Warn("ghi kiểm toán phán quyết khởi động xuống đĩa thất bại", "module", "host", "err", recErr)
	}
	if derr != nil {
		return fmt.Errorf("phán quyết khởi động thất bại: %w", derr)
	}
	if err := h.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: rawRequirement, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		return fmt.Errorf("ghi phán quyết khởi động: %w", err)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("bắt đầu sáng tác (quy hoạch sư: %s — %s)", decision.Planner, decision.Reason), Level: "info"})
	if !h.startEngine(&flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}) {
		return fmt.Errorf("Engine đang chạy hoặc đang dừng, không thể khởi động sách mới")
	}
	return nil
}

// refuseNewBookOverExisting Từ chối mở sách mới trong thư mục đã có các chương hoàn thành: StartPrepared sẽ reset
// checkpoints và progress, thao tác nhầm sẽ âm thầm xóa sạch chuỗi tiến độ của toàn bộ cuốn sách (sau khi nhập liệu xong dừng ở trang chào mừng
// bấm nhầm Enter là kịch bản điển hình nhất). Chỉ nhìn vào số chương đã hoàn thành —— những tàn dư của giai đoạn quy hoạch/khởi động thất bại không tạo thành chương,
// thì cho qua để giữ lại đường dẫn tự phục hồi qua việc nhấn Ctrl+S trong cùng phiên đồng sáng tác để thử lại và phán quyết bù.
func (h *Host) refuseNewBookOverExisting() error {
	progress, err := h.store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}
	book, err := h.store.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		return fmt.Errorf("thư mục đầu ra đã có chương, nhưng thông tin tác phẩm không tồn tại")
	}
	name := book.Title
	return fmt.Errorf("thư mục đầu ra đã có tiến độ sáng tác %d chương của \"%s\", tạo mới sẽ reset tiến độ và checkpoint của nó: để viết tiếp vui lòng vào qua cổng khôi phục (khởi động lại ứng dụng sẽ tự động khôi phục), để viết sách mới vui lòng đổi thư mục đầu ra",
		len(progress.CompletedChapters), name)
}

// startEngine Cửa vào thống nhất để khởi động động cơ (Start/Resume/Continue/khởi động lại sau can thiệp dùng chung).
// lifecycle bắt buộc phải được đặt thành running trước khi khởi chạy goroutine: động cơ có thể kết thúc ngay lập tức (hoàn thành sách/không có định tuyến),
// runEnded sẽ chốt lifecycle về trạng thái cuối; nếu đảo ngược thứ tự, runEnded chạy trước, ở đây ghi running sau,
// UI sẽ vĩnh viễn hiển thị "đang chạy" trong khi động cơ thực tế đã dừng.
func (h *Host) startEngine(initial *flow.Instruction) bool {
	// Kiểm soát vượt qua khởi động lại: khi có không gian làm việc nhập liệu chưa hoàn thành, cấm Engine thông thường tiêu thụ trạng thái bán xuất bản (RFC §12.5).
	active, done, importErr := imp.ResumeStatus(h.store)
	if importErr != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc trạng thái nhập liệu thất bại, đã chặn việc sáng tác thông thường ghi đè lên các artifact hiện có: " + importErr.Error()})
		return false
	}
	if active && !done {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Có tiến trình nhập tiểu thuyết bên ngoài chưa hoàn thành, vui lòng thực thi /import để khôi phục hoàn tất trước khi tiếp tục sáng tác"})
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	// Khi tác vụ chạy ngầm độc quyền (nhập liệu/mô phỏng) đang diễn ra, động cơ không được chạy cướp trước, để tránh tranh chấp ghi với nó. Đây là backstop hợp nhất
	// cho mọi đường dẫn khởi động động cơ (Resume/Continue khởi động lại/tiếp sức tự động/next) —— lính gác cổng là lớp bảo vệ thứ nhất, đây là lớp bảo vệ cuối cùng.
	if h.exclusive != "" {
		return false
	}
	// lifecycle có thể đã là paused, nhưng goroutine Engine cũ vẫn đang thực thi defer thoát.
	// Bắt buộc phải đối chiếu đồng thời với trạng thái thật của Engine; nếu không sẽ đổi lifecycle về running, trong khi start
	// thực tế là no-op, sau đó runEnded cũ lại chốt nó thành idle.
	if h.engine.isRunning() {
		return false
	}
	h.observer.setAborting(false)
	previous := h.lifecycle
	h.lifecycle = lifecycleRunning
	if !h.engine.start(initial) {
		h.lifecycle = previous
		return false
	}
	return true
}

// Reopen Bắt buộc mở lại sách đã hoàn kết sang trạng thái sáng tác. Hoàn thành sách và mở lại đều là những quyết định nặng ký: hoàn thành sách có thể do architect phán quyết,
// mở lại thì chỉ có thể do người dùng chủ động khởi xướng (/reopen), không qua model phán quyết. Khi direction khác rỗng sẽ được đăng ký thành can thiệp chờ xử lý,
// khi khôi phục sẽ qua Arbiter phán quyết và tiêm vào trước (cùng kênh với can thiệp khi dừng máy), sau đó tiếp tục chạy động cơ (định tuyến cuối tập sẽ phân phát viết tập tiếp).
func (h *Host) Reopen(direction string) error {
	h.mu.Lock()
	switch {
	case h.lifecycle == lifecycleRunning:
		h.mu.Unlock()
		return fmt.Errorf("động cơ sáng tác đang chạy, không cần mở lại")
	case h.cocreating:
		h.mu.Unlock()
		return fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng kết thúc đồng sáng tác trước")
	case h.exclusive != "":
		ex := h.exclusive
		h.mu.Unlock()
		return fmt.Errorf("%s đang diễn ra, vui lòng hoàn thành trước rồi mới mở lại", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}

	if err := h.store.Progress.ReopenContinue(); err != nil {
		return err
	}
	reopenEvent := Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã mở lại sách về trạng thái sáng tác (người dùng hủy phán quyết hoàn kết)", Level: "info"}
	if d := strings.TrimSpace(direction); d != "" {
		reopenEvent.Detail = reopenEvent.Summary + "\nHướng viết tiếp: " + d
	}
	h.emitEvent(reopenEvent)
	if d := strings.TrimSpace(direction); d != "" {
		if err := h.store.RunMeta.SetPendingSteer(d); err != nil {
			return fmt.Errorf("đã mở lại, nhưng đăng ký hướng viết tiếp thất bại: %v, vui lòng nhập lại hướng trực tiếp vào ô nhập liệu", err)
		}
	}
	return nil
}

// Resume Chế độ khôi phục: tạo resume prompt từ checkpoint + progress và khởi động.
func (h *Host) Resume() (string, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return "", fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return "", fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng kết thúc đồng sáng tác trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		return "", fmt.Errorf("%s đang diễn ra, vui lòng hoàn thành trước khi khôi phục sáng tác", ex)
	}
	h.mu.Unlock()
	label, err := resumeLabel(h.store)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", nil // Chế độ tạo mới, không có khôi phục
	}
	if err := h.requireCleanChapters(); err != nil {
		return label, err
	}
	if err := h.budget.Refuse(); err != nil {
		return "", err
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Khôi phục sáng tác: " + label, Level: "info"})
	for _, w := range h.store.CheckConsistency() {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Cảnh báo tính nhất quán: " + w, Level: "warn"})
	}
	// Đảm bảo snapshot quy tắc người dùng tồn tại; nếu có rồi thì đọc nhẹ nhàng.
	h.ensureUserRules()
	h.refreshWriterRestore()
	// Can thiệp chờ xử lý (còn lại từ kỳ dừng máy/còn sót lại do sự cố lúc phán quyết) bắt buộc phải ưu tiên trước phán quyết chạy tiếp của động cơ ——
	// nếu không động cơ có thể cướp trước phán quyết rồi viết tiếp những chương đi ngược lại với can thiệp. Thực thi đồng bộ (chặn vài giây có thể chấp nhận được,
	// UI đã hiển thị "Khôi phục sáng tác"); sau khi doIntervention thành công, nó sẽ tự xóa PendingSteer và kéo động cơ lên theo hướng
	// restart=true. Không có can thiệp chờ xử lý → trực tiếp chạy tiếp.
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return label, fmt.Errorf("đọc can thiệp chờ xử lý: %w", err)
	}
	if meta != nil && meta.PendingSteer != "" {
		if err := h.doIntervention(meta.PendingSteer, true); err != nil {
			return label, err
		}
	} else {
		// Chỉ khôi phục sự thật, không khôi phục phiên hội thoại (RFC §6): Engine tính toán lại định tuyến từ store và chạy tiếp.
		if !h.startEngine(nil) {
			return label, fmt.Errorf("Engine đang hoàn tất đợt dừng trước đó, vui lòng thử khôi phục lại sau")
		}
	}
	// lifecycle do startEngine / runEnded quản lý, ở đây không ghi đè nữa ——
	// nếu động cơ kết thúc ngay lập tức (hoàn kết v.v...), ghi đè sẽ làm cho trạng thái cuối trở lại running.
	return label, nil
}

// handleIntervention Điều chỉnh cho callback hỏi lại không có giá trị trả về của Engine; lỗi đã được phát sự kiện qua doIntervention.
func (h *Host) handleIntervention(text string) {
	_ = h.doIntervention(text, false)
}

// doIntervention Là đường dẫn phán quyết thống nhất cho can thiệp người dùng: Collect → Decide → Thực thi.
// Tuần tự hóa FIFO (tối đa một lần tư vấn trong hàng chờ cùng lúc); answer/rules được thực thi tức thì, các hành động trạng thái kiểm soát
// (hold/reopen/dispatch) sẽ được xếp hàng chờ đệ trình ở biên trong lúc động cơ đang chạy, và thực thi ngay lập tức khi dừng máy.
// Khi restart=true (ngữ nghĩa của Continue), sau khi xử lý can thiệp xong sẽ đảm bảo động cơ chạy.
func (h *Host) doIntervention(text string, restart bool) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	// Bảo vệ chống sập: lưu trữ bền vững trước khi phán quyết (PendingSteer), áp dụng thành công hoặc đã echo thất bại ngay lúc đó rồi thì mới xóa nguyên tử
	// (ClearHandledSteer đồng thời reset FlowSteering). Sập trong lúc phán quyết → Resume lần sau phát lại.
	if err := h.store.RunMeta.SetPendingSteer(text); err != nil {
		wrapped := fmt.Errorf("lưu trữ can thiệp thất bại, đã dừng phán quyết: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	clearPending := func() error {
		if err := h.store.ClearHandledSteer(); err != nil {
			return fmt.Errorf("xóa can thiệp đã xử lý thất bại: %w", err)
		}
		return nil
	}

	facts, err := arbiter.CollectInterventionFacts(h.store)
	if err != nil {
		wrapped := fmt.Errorf("thu thập sự thật can thiệp thất bại, chưa gọi Arbiter: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	facts.Running = h.engine.isRunning()

	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "phán quyết can thiệp người dùng", func() (arbiter.InterventionDecision, error) {
		return arbiter.DecideIntervention(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterIntervention, facts, text)
	})

	rec := storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: text,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		rec.CheckpointSeq = cp.Seq
	}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := h.store.Decisions.Append(rec); err != nil {
		wrapped := fmt.Errorf("ghi đĩa kiểm toán phán quyết can thiệp thất bại, từ chối thực thi hành động: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}

	if derr != nil {
		// Thà không động còn hơn động nhầm: không tạo ra bất kỳ ghi đĩa nào. Lỗi gọi hàm và
		// lỗi xác thực output dùng chung một kênh error, bắt buộc phải echo nguyên bản, không được ngụy trang chung thành "không thể hiểu".
		// Đã báo ngay lúc đó → xóa pending (nếu không Resume lần tới sẽ tự động phát lại cùng một can thiệp thất bại).
		h.emitEvent(newInterventionFailureEvent(derr))
		if err := clearPending(); err != nil {
			return fmt.Errorf("%v；%w", derr, err)
		}
		return derr
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Phán quyết: " + decision.Reason, Level: "info"})
	if decision.Answer != "" {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: decision.Answer, Level: "info"})
	}
	// Nếu bất kỳ hành động nào lưu trữ thất bại → Giữ lại PendingSteer (lúc khôi phục phát lại toàn bộ để phán quyết lại;
	// hold/reopen có tính chất idempotent, dispatch dùng sự thật mới hỏi lại, phát lại an toàn).
	var actionErr error
	if decision.Rules != "" {
		if snap, _, err := h.userRules.AddRuntimeRule(h.runCtx, decision.Rules); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Ghi quy tắc sáng tác xuống đĩa thất bại: " + err.Error(), Level: "error"})
			actionErr = err
		} else if snap != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Quy tắc sáng tác đã được cập nhật và lưu lại", Level: "info"})
		}
	}

	if decision.Hold != nil || decision.Reopen != nil || decision.Dispatch != nil {
		op := controlOp{hold: decision.Hold, reopen: decision.Reopen, dispatch: decision.Dispatch, text: text, facts: facts}
		if !h.engine.enqueue(op) {
			// Động cơ chưa chạy: thực thi ngay; lưu trữ thất bại → Giữ lại PendingSteer, khôi phục lại sẽ phát lại toàn bộ can thiệp.
			if err := h.engine.applyControlOp(context.Background(), op); err != nil {
				h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Thực thi hành động can thiệp thất bại, đã giữ lại; sẽ tự động thử lại khi khôi phục/tiếp tục"})
				return err
			}
			// reopen/dispatch thể hiện ý định sáng tác tiếp, kéo động cơ lên.
			if decision.Reopen != nil || decision.Dispatch != nil {
				restart = true
			}
		}
	}
	if actionErr != nil {
		// Giữ lại PendingSteer: lúc khôi phục/tiếp tục sẽ phát lại toàn bộ để phán quyết lại.
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Một phần hành động can thiệp chưa thành công, can thiệp đã được giữ lại; tự động thử lại lúc khôi phục/tiếp tục"})
		return actionErr
	}
	// Hành động đã được áp dụng/xếp hàng chờ thành công, xóa bảo vệ sập (nếu sau khi vào hàng chờ động cơ bị lỗi hoặc thoát race thì
	// engine sẽ ghi lại PendingSteer để backup).
	if err := clearPending(); err != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: err.Error()})
		return err
	}

	if restart && !h.engine.isRunning() {
		if err := h.budget.Refuse(); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: err.Error(), Level: "warn"})
			return err
		}
		h.refreshWriterRestore()
		if !h.startEngine(nil) {
			// Lúc này can thiệp đã có hiệu lực và xóa PendingSteer, chỉ là động cơ chưa thể chạy tiếp ngay lập tức - không thể nói dối là "đã lưu".
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Can thiệp đã có hiệu lực, nhưng Engine không thể chạy tiếp ngay; vui lòng tiếp tục trong ô nhập liệu hoặc khởi động lại ứng dụng để khôi phục"})
			return fmt.Errorf("can thiệp đã có hiệu lực, nhưng Engine không thể chạy tiếp ngay")
		}
	}
	return nil
}

func newInterventionFailureEvent(err error) Event {
	detail := err.Error()
	return Event{
		Time:     time.Now(),
		Category: "ERROR",
		Agent:    "arbiter",
		Summary:  "Phán quyết can thiệp thất bại: " + detail + " (không có bất kỳ sửa đổi nào)",
		Detail:   detail,
		Kind:     errorKind(err, detail),
		Level:    "error",
	}
}

// arbiterModel Trả về model phán quyết có tracking sử dụng (token/chi phí tính vào ngân sách và hệ thống usage).
func (h *Host) arbiterModel() agentcore.ChatModel {
	return newUsageTrackedModel(h.models.Default, "arbiter", h.usage.Record)
}

// Continue Gọi khi người dùng gõ vào ô nhập liệu sau khi dừng máy: Phán quyết can thiệp + Đảm bảo động cơ chạy lại.
func (h *Host) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text is required")
	}
	h.mu.Lock()
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng kết thúc đồng sáng tác trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		// Trong lúc có tác vụ độc quyền bắt buộc phải chặn trước phán quyết: nếu không Arbiter đã sửa PendingSteer/quy tắc/trạng thái điều khiển xong xuôi thì động cơ mới bị cổng gác chặn.
		return fmt.Errorf("%s đang diễn ra, vui lòng hoàn thành trước khi tiếp tục sáng tác", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}

	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[Tiếp tục] " + text, Level: "info"})
		return h.doIntervention(text, true)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể tiếp tục sáng tác")
	}
	return err
}

// SetAdvanceMode Thay đổi chế độ đẩy tiến chương tất định. Nó chỉ ghi ý định chạy của người dùng,
// không gọi Arbiter, cũng không khởi động ngầm Engine vốn đã dừng.
func (h *Host) SetAdvanceMode(mode domain.ChapterAdvanceMode) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()
	if err := h.store.RunMeta.SetAdvanceMode(mode); err != nil {
		return err
	}
	label := "Tự động đẩy tiến"
	if mode == domain.ChapterAdvanceReview {
		label = "Nghiệm thu từng chương"
	}
	summary := "Chế độ đẩy tiến chương đã được chuyển sang " + label
	h.mu.Lock()
	state := h.lifecycle
	h.mu.Unlock()
	if mode == domain.ChapterAdvanceAuto && state != lifecycleRunning && state != lifecycleCompleted {
		summary += "; hiện vẫn đang tạm dừng, hãy gõ lệnh tiếp tục để khôi phục chạy"
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
	return nil
}

// AdvanceOneChapter cấp phép một chương chính xác trong chế độ nghiệm thu từng chương và khởi động Engine.
func (h *Host) AdvanceOneChapter() error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	h.mu.Lock()
	running, cocreating, ex := h.lifecycle == lifecycleRunning, h.cocreating, h.exclusive
	h.mu.Unlock()
	if running || h.engine.isRunning() {
		return fmt.Errorf("việc sáng tác vẫn đang chạy hoặc đang hoàn tất việc tạm dừng, vui lòng thực thi /next sau")
	}
	if cocreating {
		return fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng kết thúc đồng sáng tác trước")
	}
	if ex != "" {
		return fmt.Errorf("%s đang diễn ra, vui lòng hoàn thành trước rồi thực thi /next", ex)
	}
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("RunMeta chưa khởi tạo")
	}
	if meta.AdvanceMode != domain.ChapterAdvanceReview {
		return fmt.Errorf("/next chỉ dùng cho chế độ nghiệm thu từng chương, vui lòng thực thi /review on trước")
	}
	if meta.AdvanceHold != nil {
		return fmt.Errorf("vẫn còn ý định tạm dừng một lần đang chờ xử lý (%s), vui lòng khôi phục hoặc hoàn thành can thiệp hiện tại trước", meta.AdvanceHold.Reason)
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	progress, err := h.store.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil || progress.Phase != domain.PhaseWriting {
		phase := "<nil>"
		if progress != nil {
			phase = string(progress.Phase)
		}
		return fmt.Errorf("giai đoạn hiện tại không thể cấp phép chương mới (phase=%s)", phase)
	}
	target := progress.NextChapter()
	if target <= 0 {
		return fmt.Errorf("không thể suy luận chương tiếp theo từ tiến độ hiện tại")
	}
	if err := h.store.RunMeta.GrantAdvancePermit(target); err != nil {
		return err
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("đã thông qua chương %d; sau khi submit, chương này sẽ hoàn thành các review cần thiết và bảo trì cấu trúc arc/tập trước khi đợi thông qua lần nữa", target), Level: "info"})
	h.refreshWriterRestore()
	if !h.startEngine(nil) {
		// Việc cấp phép được lưu trữ theo số chương và đảm bảo idempotent với cùng một mục tiêu, bên gọi có thể thử lại sau mà không bị cấp phép trùng.
		return fmt.Errorf("cấp phép chương đã lưu, nhưng Engine vẫn đang hoàn tất việc dừng vòng trước; vui lòng thử lại /next sau")
	}
	return nil
}

// Steer gửi can thiệp của người dùng (có thể dùng bất cứ lúc nào khi đang chạy; khi dừng máy thì sẽ quyết định có kéo động cơ lên hay không tùy hành động phán quyết).
// TUI đợi kết quả qua tea.Cmd, do đó có thể nhận phán quyết thật/lỗi lưu trữ mà không chặn giao diện.
func (h *Host) Steer(text string) error {
	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[Người dùng can thiệp] " + text, Level: "info"})
		return h.doIntervention(text, false)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể gửi can thiệp")
	}
	return err
}

// Abort tạm dừng vòng lặp động cơ hiện tại.
func (h *Host) Abort() bool {
	return h.abortWithEvent("Người dùng tạm dừng sáng tác hiện tại thủ công", "warn")
}

// abortWithEvent thực thi tạm dừng với sự kiện nguyên nhân chỉ định. Dừng máy do ngân sách và tạm dừng thủ công dùng chung cơ chế dừng,
// chỉ khác văn bản sự kiện (dừng máy do ngân sách = chỉ thị Abort người dùng ký trước, ngữ nghĩa tương đương tạm dừng thủ công).
func (h *Host) abortWithEvent(summary, level string) bool {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	if running {
		h.lifecycle = lifecyclePaused
	}
	cancelExclusive := h.exclusiveCancel
	h.mu.Unlock()
	if running {
		// Việc đặt cờ phải xảy ra trước engine.abort: sự lan truyền cancel sẽ lập tức gây ra sự kiện stream init / worker thất bại,
		// observer sẽ dựa vào cờ này để nhận diện đó là nhiễu phái sinh từ abort và chặn lại.
		h.observer.setAborting(true)
		h.engine.abort()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	// Engine chưa chạy nhưng tác vụ độc quyền (nhập liệu, v.v.) đang chạy: nó cũng đang đốt tiền, dừng cứng do ngân sách/tạm dừng thủ công bắt buộc phải dừng được nó
	// nếu không thì chính sách ngân sách đối với tác vụ nhập liệu chỉ là bù nhìn (docs/import-pipeline.md §13.1).
	if cancelExclusive != nil {
		cancelExclusive()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	return false
}

// Close kết thúc động cơ và đóng kênh sự kiện.
//
// Ngữ nghĩa bền vững Usage: hủy autoSaveLoop trước (nó tự flush trạng thái bẩn cuối cùng),
// rồi bù thêm một lần SaveNow đồng bộ để chốt sổ. Sau khi kết thúc, vài trăm token cuối cùng của lệnh gọi LLM in-flight
// bị mất sẽ được bù lại tự động khi replay session jsonl ở lần khởi động tiếp theo.
func (h *Host) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		cancelExclusive := h.exclusiveCancel
		h.mu.Unlock()

		h.observer.setAborting(true)
		if h.runCancel != nil {
			h.runCancel() // Ngắt lệnh gọi phán quyết phía host và chuyển tiếp supervisor đang diễn ra
		}
		if cancelExclusive != nil {
			cancelExclusive()
		}
		h.engine.abort()
		h.engine.wait()
		h.asyncWG.Wait()

		if h.usageCancel != nil {
			h.usageCancel()
			h.usageCancel = nil
		}
		h.usage.WaitAutoSave()
		if err := h.usage.SaveNow(); err != nil {
			slog.Warn("ghi đĩa usage trước khi thoát thất bại", "module", "usage", "err", err)
		}
		h.closeOutputChannels()
		if err := h.bookLease.Close(); err != nil {
			slog.Error("giải phóng quyền chiếm dụng thư mục tiểu thuyết thất bại", "module", "host", "dir", h.cfg.OutputDir, "err", err)
		}
		if h.logCleanup != nil {
			h.logCleanup()
			h.logCleanup = nil
		}
	})
}

// FileLogError trả về lỗi khởi tạo file log ở giai đoạn cấu trúc; không thay đổi trong suốt vòng đời của Host.
func (h *Host) FileLogError() error {
	return h.fileLogErr
}

// runEnded được gọi lại bởi engine.onDone khi vòng lặp động cơ kết thúc (bất kể nguyên nhân): định trạng thái cuối theo thực tế trong store.
//   - Phase=Complete  → Đánh dấu completed, phát sự kiện "hoàn thành sáng tác"
//   - Khác            → Đánh dấu idle/paused, phát sự kiện "sáng tác dừng"
func (h *Host) runEnded() {
	h.observer.finalize()

	h.mu.Lock()
	progress, err := h.store.Progress.Load()
	if err != nil {
		if h.lifecycle == lifecycleRunning {
			h.lifecycle = lifecycleIdle
		}
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc tiến độ khi kết thúc động cơ thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	book, err := h.store.Book.Load()
	if err != nil {
		h.lifecycle = lifecycleIdle
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc thông tin tác phẩm khi kết thúc động cơ thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		if book == nil {
			h.lifecycle = lifecycleIdle
			h.mu.Unlock()
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
				Summary: "không tìm thấy thông tin tác phẩm khi kết thúc động cơ"})
			select {
			case h.done <- struct{}{}:
			default:
			}
			return
		}
		h.lifecycle = lifecycleCompleted
		// Chốt sổ khi hoàn thành sách: tạo theo định mệnh (store đã có toàn bộ sự kiện thực tế, không tốn cuộc gọi LLM; xem phần cuối RFC).
		summary := completionSummary(*progress, *book)
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "success"})
		h.notifier.Send(notify.Notification{
			Kind: notify.KindRunEnd, Level: "info", Title: "ainovel: Hoàn thành sáng tác",
			Body: h.runEndBody("", summary),
		})
	} else {
		wasRunning := h.lifecycle == lifecycleRunning
		if wasRunning {
			h.lifecycle = lifecycleIdle
		}
		completed := 0
		title := ""
		if progress != nil {
			completed = len(progress.CompletedChapters)
		}
		if book != nil {
			title = book.Title
		}
		h.mu.Unlock()
		if wasRunning {
			summary := fmt.Sprintf("Động cơ dừng (đã hoàn thành %d chương)", completed)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "warn"})
			h.notifier.Send(notify.Notification{
				Kind: notify.KindRunEnd, Level: "warn", Title: "ainovel: Sáng tác dừng",
				Body: h.runEndBody(title, summary),
			})
		}
	}

	select {
	case h.done <- struct{}{}:
	default:
	}
}

// runEndBody lắp ráp nội dung thông báo run_end: tên sách + tóm tắt tiến độ + chi phí tích lũy.
func (h *Host) runEndBody(title, summary string) string {
	if name := strings.TrimSpace(title); name != "" {
		summary = "《" + name + "》" + summary
	}
	cost, _, _, _, _ := h.usage.Totals()
	if cost > 0 {
		summary += fmt.Sprintf(" · Chi phí $%.2f", cost)
	}
	return summary
}

// ── Kênh truyền ──

// StreamClearSentinel thông qua streamCh gửi một luồng đơn để báo hiệu "xóa round stream hiện tại".
// Không dùng clearCh riêng nữa — hai kênh không theo thứ tự dẫn đến header ✻ hay bị rớt vào cuối round trước đó.
const StreamClearSentinel = "\x00\x00CLEAR\x00\x00"

func (h *Host) Events() <-chan Event  { return h.events }
func (h *Host) Stream() <-chan string { return h.streamCh }
func (h *Host) Done() <-chan struct{} { return h.done }
func (h *Host) Dir() string           { return h.store.Dir() }

// ── Phát sự kiện ──

func (h *Host) emitEvent(ev Event) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	// Khóa đọc đảm bảo sự kiện trước khi đóng được viết ra hoàn chỉnh; sự kiện sau khi đóng sẽ bị từ chối thẳng.
	LogEvent(ev)
	select {
	case h.events <- ev:
	default:
		select {
		case <-h.events:
		default:
		}
		select {
		case h.events <- ev:
		default:
		}
	}
}

func (h *Host) emitDelta(delta string) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	select {
	case h.streamCh <- delta:
	default:
		select {
		case <-h.streamCh:
		default:
		}
		select {
		case h.streamCh <- delta:
		default:
		}
	}
}

func (h *Host) closeOutputChannels() {
	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	if h.outputClosed {
		return
	}
	h.outputClosed = true
	close(h.done)
	close(h.events)
	close(h.streamCh)
}

func (h *Host) emitClear() {
	// Đi qua "sentinel" của streamCh, đảm bảo gửi đến TUI theo đúng thứ tự trong cùng một kênh với emitDelta.
	h.emitDelta(StreamClearSentinel)
}

// ── Snapshot (Tổng hợp trạng thái TUI) ──

func (h *Host) Snapshot() UISnapshot {
	h.mu.Lock()
	state := h.lifecycle
	provider, model, _ := h.models.CurrentSelection("default")
	modelWindow, _ := h.cfg.ResolveContextWindow(provider, model)
	thinkingLevel := h.cfg.ResolveReasoningEffort("default")
	style := h.cfg.Style
	h.mu.Unlock()

	// Tự động phân giải cửa sổ ngữ cảnh của model hiện tại, sau khi /model hoặc /config chuyển đổi, Snapshot tiếp theo sẽ tự động phản ánh.
	cost, tokIn, tokOut, cacheRead, cacheWrite := h.usage.Totals()
	saved := h.usage.SavedUSD()
	overallCapable := h.usage.OverallCacheCapable()
	recentRead, recentInput, recentSamples := h.usage.OverallRecent()
	perAgent := h.usage.PerAgent()
	cacheStats := make([]AgentCacheStat, 0, len(perAgent))
	for _, a := range perAgent {
		cacheStats = append(cacheStats, AgentCacheStat{
			Role:            a.Role,
			Input:           a.Input,
			Output:          a.Output,
			CacheRead:       a.CacheRead,
			CacheWrite:      a.CacheWrite,
			Cost:            a.Cost,
			Saved:           a.Saved,
			CacheCapable:    a.CacheCapable,
			RecentCacheRead: a.RecentCacheRead,
			RecentInput:     a.RecentInput,
			RecentSamples:   a.RecentSamples,
		})
	}
	perModel := h.usage.PerModel()
	modelStats := make([]AgentCacheStat, 0, len(perModel))
	for _, a := range perModel {
		modelStats = append(modelStats, AgentCacheStat{
			Model:        a.Model,
			Input:        a.Input,
			Output:       a.Output,
			CacheRead:    a.CacheRead,
			CacheWrite:   a.CacheWrite,
			Cost:         a.Cost,
			Saved:        a.Saved,
			CacheCapable: a.CacheCapable,
		})
	}

	snap := UISnapshot{
		Provider:               provider,
		ModelName:              model,
		ModelContextWindow:     modelWindow,
		ThinkingLevel:          thinkingLevel,
		Style:                  style,
		RuntimeState:           string(state),
		IsRunning:              state == lifecycleRunning,
		TotalInputTokens:       tokIn,
		TotalOutputTokens:      tokOut,
		TotalCacheReadTokens:   cacheRead,
		TotalCacheWriteTokens:  cacheWrite,
		TotalCostUSD:           cost,
		TotalSavedUSD:          saved,
		BudgetLimitUSD:         h.budget.Limit(),
		OverallCacheCapable:    overallCapable,
		OverallRecentCacheRead: recentRead,
		OverallRecentInput:     recentInput,
		OverallRecentSamples:   recentSamples,
		TotalCacheBreaks:       h.usage.OverallCacheBreaks(),
		CachePerAgent:          cacheStats,
		CachePerModel:          modelStats,
		MissingAssistantUsage:  h.usage.MissingAssistantUsage(),
	}

	if book, _ := h.store.Book.Load(); book != nil {
		snap.BookTitle = book.Title
		snap.Synopsis = truncate(book.Synopsis, 200)
	}
	progress, _ := h.store.Progress.Load()
	if progress != nil {
		snap.Phase = string(progress.Phase)
		snap.Flow = string(progress.Flow)
		snap.CurrentChapter = progress.CurrentChapter
		snap.TotalChapters = progress.TotalChapters
		snap.CompletedCount = len(progress.CompletedChapters)
		snap.TotalWordCount = progress.TotalWordCount
		snap.InProgressChapter = progress.InProgressChapter
		snap.PendingRewrites = progress.PendingRewrites
		snap.RewriteReason = progress.RewriteReason
		snap.Layered = progress.Layered
		if progress.CurrentVolume > 0 {
			snap.CurrentVolumeArc = fmt.Sprintf("Tập %d·Arc %d", progress.CurrentVolume, progress.CurrentArc)
		}
	}
	if meta, _ := h.store.RunMeta.Load(); meta != nil {
		snap.PendingSteer = meta.PendingSteer
		snap.AdvanceMode = string(meta.AdvanceMode)
		snap.AdvancePermitChapter = meta.AdvancePermitChapter
		if meta.AdvanceHold != nil {
			snap.HasAdvanceHold = true
			snap.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}

	snap.Agents = h.observer.agentSnapshots()
	snap.StatusLabel = deriveStatusLabel(snap)

	// Nhãn khôi phục
	if label, err := resumeLabel(h.store); err == nil && label != "" {
		snap.RecoveryLabel = label
	}

	h.fillDetails(&snap, progress)

	return snap
}

// fillDetails Điền khu vực chi tiết: thiết lập, vai trò, commit/review/tóm tắt gần đây.
func (h *Host) fillDetails(snap *UISnapshot, progress *domain.Progress) {
	if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
		snap.Premise = truncate(premise, 80)
	}
	if outline, _ := h.store.Outline.LoadOutline(); len(outline) > 0 {
		completed := make(map[int]struct{})
		if progress != nil {
			completed = make(map[int]struct{}, len(progress.CompletedChapters))
			for _, chapter := range progress.CompletedChapters {
				completed[chapter] = struct{}{}
			}
		}
		for _, e := range outline {
			title := e.Title
			if _, ok := completed[e.Chapter]; ok {
				committedTitle, err := h.store.Summaries.LoadSummaryTitle(e.Chapter)
				if err != nil {
					slog.Warn("chiếu tiêu đề chương thất bại", "module", "host.snapshot", "chapter", e.Chapter, "err", err)
				} else if strings.TrimSpace(committedTitle) != "" {
					title = committedTitle
				}
			}
			snap.Outline = append(snap.Outline, OutlineSnapshot{
				Chapter: e.Chapter, Title: title, CoreEvent: e.CoreEvent,
			})
		}
	}
	if progress != nil && progress.Layered {
		if compass, _ := h.store.Outline.LoadCompass(); compass != nil {
			snap.CompassDirection = compass.EndingDirection
			snap.CompassScale = compass.EstimatedScale
		}
		if volumes, _ := h.store.Outline.LoadLayeredOutline(); len(volumes) > 0 {
			for _, v := range volumes {
				if v.Index > progress.CurrentVolume {
					snap.NextVolumeTitle = v.Title
					break
				}
			}
		}
	}
	if chars, _ := h.store.Characters.Load(); len(chars) > 0 {
		for _, c := range chars {
			label := c.Name
			if c.Role != "" {
				label += "（" + c.Role + "）"
			}
			snap.Characters = append(snap.Characters, label)
		}
	}
	if ledger, _ := h.store.Cast.Load(); len(ledger) > 0 {
		snap.SupportingCount = len(ledger)
		recent, _ := h.store.Cast.RecentActive(5)
		for _, e := range recent {
			label := e.Name
			if e.BriefRole != "" {
				label += "（" + e.BriefRole + "）"
			}
			snap.RecentSupporting = append(snap.RecentSupporting, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
		wc := progress.ChapterWordCounts[lastCh]
		snap.LastCommitSummary = fmt.Sprintf("Chương %d %d chữ", lastCh, wc)
	}
	currentCh := 1
	if progress != nil && len(progress.CompletedChapters) > 0 {
		currentCh = progress.CompletedChapters[len(progress.CompletedChapters)-1]
	}
	if review, err := h.store.World.LoadLastReview(currentCh); err == nil && review != nil {
		snap.LastReviewSummary = fmt.Sprintf("verdict=%s %d vấn đề", review.Verdict, len(review.Issues))
		if len(review.AffectedChapters) > 0 {
			snap.LastReviewSummary += fmt.Sprintf(" ảnh hưởng %v", review.AffectedChapters)
		}
	}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		snap.LastCheckpointName = fmt.Sprintf("%s.%s", cp.Scope, cp.Step)
	}
	if progress != nil {
		for i := len(progress.CompletedChapters) - 1; i >= 0 && len(snap.RecentSummaries) < 2; i-- {
			ch := progress.CompletedChapters[i]
			if summary, err := h.store.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.RecentSummaries = append(snap.RecentSummaries,
					fmt.Sprintf("Chương %d: %s", ch, truncate(summary.Summary, 50)))
			}
		}
	}
}

func deriveStatusLabel(s UISnapshot) string {
	switch {
	case s.Phase == string(domain.PhaseComplete):
		return "COMPLETE"
	case s.Flow == string(domain.FlowReviewing):
		return "REVIEW"
	case s.Flow == string(domain.FlowRewriting) || s.Flow == string(domain.FlowPolishing):
		return "REWRITE"
	case s.RuntimeState == "running":
		return "RUNNING"
	default:
		return "READY"
	}
}

// ── Quản lý model ──

func (h *Host) ConfiguredProviders() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	providers := make([]string, 0, len(h.cfg.Providers))
	for name := range h.cfg.Providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

func (h *Host) ConfiguredModels(provider string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.CandidateModels(provider)
}

func (h *Host) CurrentModelSelection(role string) (string, string, bool) {
	return h.models.CurrentSelection(role)
}

func (h *Host) SwitchModel(role, provider, model string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}
	if err := h.models.Swap(role, provider, model); err != nil {
		return err
	}
	if role == "" || role == "default" {
		h.cfg.Provider = provider
		h.cfg.ModelName = model
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Provider = provider
		rc.Model = model
		h.cfg.Roles[role] = rc
	}
	// Đổi model không làm thay đổi ý định cường độ suy luận đã lưu: chỉ khi ban hành mới bị giới hạn theo năng lực của model mới.
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("lưu cấu hình thất bại", "module", "host", "err", err)
		}
	}
	h.applyThinkingLocked(role)
	// Đánh một dòng warn khi chuyển sang model chưa đăng ký, nhắc người dùng rằng đã dùng fallback 128k —— truyện dài dễ bị nén sớm.
	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	window, source := h.cfg.ResolveContextWindow(provider, model)
	bootstrap.LogContextWindowChoice(logRole, model, window, source)

	// Không cần bối cảnh thường trú để liên kết: ContextManager của writer/architect/editor đi theo
	// ContextManagerFactory, lần spawn tiếp theo sẽ tự động xây dựng lại theo cửa sổ model mới.

	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Model đã được chuyển đổi: %s → %s/%s", role, provider, model),
		Level:    "info",
	})
	return nil
}

// concreteThinkingRoles là các vai trò cụ thể có thể áp dụng cường độ suy luận (nhất quán với định tuyến agents.ApplyThinking).
// Khi gọi default sẽ áp dụng lại từng cái theo ResolveReasoningEffort của mỗi vai trò.
var concreteThinkingRoles = []string{"architect", "writer", "editor"}

// CurrentThinking trả về chuỗi gốc cường độ suy luận hiện đang có hiệu lực của một vai trò nào đó (dùng để đồng bộ giá trị hiện tại trên bảng điều khiển /model).
func (h *Host) CurrentThinking(role string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.ResolveReasoningEffort(strings.ToLower(strings.TrimSpace(role)))
}

func (h *Host) AvailableThinking(role string) []agentcore.ThinkingLevel {
	h.mu.Lock()
	model := h.models.ForRole(strings.ToLower(strings.TrimSpace(role)))
	h.mu.Unlock()
	return agents.AvailableThinkingForModel(model)
}

// resolveThinkingForRoleLocked tính toán cường độ suy luận thực tế có hiệu lực của một vai trò nào đó: lấy ý định ban đầu của nó
// (ResolveReasoningEffort: cấp vai trò → mặc định cấp cao nhất), rồi giới hạn theo năng lực model hiện tại của vai trò đó.
// Việc giới hạn chỉ xảy ra trên "đường dẫn có hiệu lực" này, không ghi ngược lại cấu hình - bộ lưu trữ luôn giữ ý định ban đầu của người dùng.
func (h *Host) resolveThinkingForRoleLocked(role string) agentcore.ThinkingLevel {
	parsed, _ := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role))
	resolved, _ := agents.ResolveThinkingForModel(h.models.ForRole(role), parsed)
	return resolved
}

// applyThinkingLocked ban hành cường độ có hiệu lực cho live agent; mỗi vai trò tự giới hạn theo model của chính mình.
func (h *Host) applyThinkingLocked(role string) {
	if h.thinkingApplier == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || role == "default" {
		for _, r := range concreteThinkingRoles {
			h.thinkingApplier(r, h.resolveThinkingForRoleLocked(r))
		}
		return
	}
	h.thinkingApplier(role, h.resolveThinkingForRoleLocked(role))
}

// SetRoleThinking thiết lập cường độ suy luận của một vai trò nào đó (hoặc default): xác thực → lưu trữ bền vững → liên kết live agent → sự kiện.
// Phản chiếu cấu trúc của SwitchModel; trực giao với việc chọn model, có thể điều chỉnh riêng biệt. level rỗng = không ghi đè (kế thừa).
func (h *Host) SetRoleThinking(role, level string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	parsed, err := agents.ParseThinkingLevel(level)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	// Bộ lưu trữ giữ lại ý định ban đầu: trực tiếp lưu trữ bền vững cường độ do người dùng chọn, việc giới hạn chỉ xảy ra theo năng lực model lúc ban hành (applyThinkingLocked).
	if role == "" || role == "default" {
		h.cfg.ReasoningEffort = string(parsed)
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.ReasoningEffort = string(parsed)
		h.cfg.Roles[role] = rc
	}
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("lưu cấu hình thất bại", "module", "host", "err", err)
		}
	}

	// Liên kết live: vai trò cụ thể áp dụng trực tiếp; default thì duyệt qua các vai trò cụ thể để áp dụng lại theo ResolveReasoningEffort
	// (cái nào đã bị ghi đè ở cấp vai trò thì giữ nguyên của chính nó, cái nào chưa bị ghi đè thì nhận mặc định mới).
	h.applyThinkingLocked(role)

	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	shown := string(parsed)
	if shown == "" {
		shown = "Mặc định (kế thừa)"
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Cường độ suy luận đã được chuyển đổi: %s → %s", logRole, shown),
		Level:    "info",
	})
	return nil
}

// ── Phát lại sự kiện ──

func (h *Host) ReplayQueue(afterSeq int64) ([]domain.RuntimeQueueItem, error) {
	if h.store == nil || h.store.Runtime == nil {
		return nil, nil
	}
	return h.store.Runtime.LoadQueueAfter(afterSeq)
}

// ── Đồng sáng tác ──

// CoCreateStream Đồng sáng tác khởi động lạnh: làm rõ nhu cầu từ đầu, xuất ra chỉ thị sáng tác cho toàn bộ cuốn sách.
func (h *Host) CoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, coCreateSystemPrompt, history, onProgress)
}

// StageCoCreateStream Đồng sáng tác giai đoạn: lập kế hoạch định hướng tiếp theo dựa trên nội dung đã viết.
// System prompt = prompt giai đoạn + tóm tắt trạng thái câu chuyện hiện tại, để trợ lý biết "đã viết những gì".
func (h *Host) StageCoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, stageSystemPrompt(h.store), history, onProgress)
}

// stagePlanPrefix Gói "brief định hướng tiếp theo" được tạo ra từ đồng sáng tác thành một can thiệp quy hoạch giai đoạn, giao cho Arbiter phán quyết.
// Chỉ dán nhãn sự thật [Quy hoạch giai đoạn] + phát biểu trung lập, không ghi cứng "làm thế nào để triển khai" —— định tuyến cụ thể (compass / architect /
// user_rules) giao cho tiêu chí phán quyết "Quy hoạch giai đoạn" trong arbiter-intervention.md, tránh tạo thành nguồn chân lý thứ hai với prompt,
// cũng không chặn đường yêu cầu kiểu văn phong đi qua user_rules (giữ nguyên tắc "Phân loại phán quyết thuộc về LLM"). Continue sẽ đè thêm tiền tố [Người dùng can thiệp].
const stagePlanPrefix = "[Quy hoạch giai đoạn] Tôi tạm dừng sáng tác, và cùng trợ lý đồng sáng tác vạch ra định hướng tiếp theo dưới đây, vui lòng phán quyết cách triển khai theo phân loại can thiệp của bạn, sau đó tiếp tục sáng tác. Định hướng tiếp theo như sau:\n\n"

// PauseForCoCreate Vào đồng sáng tác giai đoạn: đặt cờ chiếm dụng đồng sáng tác, nếu đang chạy thì tạm dừng luôn Engine.
// Trả về false nghĩa là không thể vào (toàn bộ sách đã hoàn thành hoặc đã ở trong đồng sáng tác), bên gọi có thể bỏ qua.
// Cờ chiếm dụng trong cửa sổ đồng sáng tác sẽ chặn sự can thiệp đồng thời của import/simulate/start/resume/continue ——
// sau khi tạm dừng trong lúc đang chạy, lifecycle=paused, mutually exclusive ==running hiện tại mất hiệu lực, dựa vào cờ này để bù đắp;
// Đã dừng (idle/paused) cũng cho phép vào, quy hoạch xong thông qua Continue để chạy tiếp.
func (h *Host) PauseForCoCreate() bool {
	h.mu.Lock()
	if h.cocreating || h.lifecycle == lifecycleCompleted {
		h.mu.Unlock()
		return false
	}
	h.cocreating = true
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	// Trong lúc chạy, tái sử dụng abortWithEvent để dừng máy (running→paused + setAborting + Abort + sự kiện), cùng thứ tự
	// với tạm dừng thủ công, không viết lại lần nữa; đã dừng (idle/paused) chỉ đặt cờ, quy hoạch xong qua Continue chạy tiếp.
	if running {
		h.abortWithEvent("Vào đồng sáng tác giai đoạn, sáng tác đã tạm dừng", "info")
	} else {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Vào đồng sáng tác giai đoạn", Level: "info"})
	}
	return true
}

// ResumeFromCoCreate Kết thúc đồng sáng tác giai đoạn: lấy định hướng tiếp theo do đồng sáng tác tạo ra làm can thiệp để tiêm vào và khôi phục sáng tác.
// Sau khi xóa cờ chiếm dụng, tái sử dụng đường dẫn tiêm dừng máy của Continue (chịu ràng buộc ngân sách trước).
// Chú ý: khi draft rỗng thì trả về sớm, không xóa cờ là cố ý (đồng sáng tác chưa kết thúc); TUI side canStart() guard
// cùng dùng chung tiêu chí "không rỗng" này, đảm bảo đường dẫn này không thể tiếp cận, cocreating sẽ không bị rò rỉ vì điều này.
func (h *Host) ResumeFromCoCreate(draft string) error {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return fmt.Errorf("draft is required")
	}
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("not in co-create")
	}
	h.cocreating = false
	h.mu.Unlock()

	// abort của PauseForCoCreate là bất đồng bộ: đợi vòng lặp engine thực sự hội tụ mới tiếp tục, quay về tiền đề "dừng máy thật sự"
	// giống như Continue sau khi tạm dừng thủ công. Cửa sổ đồng sáng tác là thang thời gian tương tác người-máy, polling ngắn sẽ không cảm nhận được.
	for h.engine.isRunning() {
		time.Sleep(20 * time.Millisecond)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đồng sáng tác giai đoạn hoàn tất, đã tiêm định hướng tiếp theo và khôi phục sáng tác", Level: "info"})
	return h.Continue(stagePlanPrefix + draft)
}

// CancelCoCreate Hủy bỏ đồng sáng tác giai đoạn: xóa cờ chiếm dụng, giữ trạng thái tạm dừng (người dùng có thể tiếp tục nhập hoặc khởi động lại Resume).
func (h *Host) CancelCoCreate() {
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return
	}
	h.cocreating = false
	h.mu.Unlock()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã thoát đồng sáng tác giai đoạn, sáng tác giữ nguyên tạm dừng (có thể tiếp tục nhập trong ô nhập liệu)", Level: "info"})
}

// ── Công cụ ──

func (h *Host) refreshWriterRestore() {
	if h.writerRestore != nil {
		h.writerRestore.Refresh(h.store)
	}
}

func (h *Host) CheckChapterRevisions() ([]int, error) {
	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, fmt.Errorf("đọc bản ghi khôi phục sửa đổi: %w", err)
	}
	if pending != nil {
		chapters := make([]int, 0, len(pending.Items))
		for _, item := range pending.Items {
			chapters = append(chapters, item.Chapter)
		}
		return chapters, nil
	}
	changes, err := revision.Scan(h.store)
	if err != nil {
		return nil, err
	}
	return revision.ChangedChapters(changes), nil
}

func (h *Host) SyncChapterRevisions(ctx context.Context) (*revision.Result, error) {
	if err := h.acquireExclusive("đồng bộ sửa đổi chương"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	defer h.releaseExclusive()

	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, err
	}
	if pending == nil {
		changes, err := revision.Scan(h.store)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			return &revision.Result{}, nil
		}
		if err := h.budget.Refuse(); err != nil {
			return nil, err
		}
	}
	model := h.models.ForRoleWithFailover("editor", func(ev bootstrap.FailoverEvent) {
		slog.Warn("chuyển đổi provider sửa đổi chương", "module", "revision", "role", ev.Role,
			"reason", ev.Reason, "from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel), "err", ev.Err)
	})
	model = newUsageTrackedModel(model, "editor", h.usage.Record)
	service := revision.NewService(h.store, model, h.bundle.Prompts.RevisionAnalyze, h.styleStats)
	return service.Sync(ctx)
}

func (h *Host) requireCleanChapters() error {
	chapters, err := h.CheckChapterRevisions()
	if err != nil {
		return fmt.Errorf("kiểm tra sửa đổi bên ngoài của chương: %w", err)
	}
	if len(chapters) > 0 {
		return fmt.Errorf("phát hiện chính văn chương đã bị sửa đổi bên ngoài: %v; vui lòng thực thi /sync trước", chapters)
	}
	return nil
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// ImportFrom Khởi động một lần nhập dữ liệu biên dịch ngữ nghĩa tiểu thuyết bên ngoài: ingest → segment → analyze → synthesize → publish.
// Model chỉ phán quyết các ngữ nghĩa mở (ranh giới/sự thật/tổng hợp), Go quản lý tọa độ/ghi đè/tính bình đẳng; không thể chạy đồng thời với Engine,
// sau khi hoàn tất nhập dữ liệu, AdvanceHold sẽ quyết định xem có viết tiếp hay không.
// Kênh sự kiện trả về sẽ bị đóng bởi imp.Run, bên gọi chịu trách nhiệm tiêu thụ (nếu đầy sẽ vứt bỏ để phòng ngừa chặn coroutine đường ống).
func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	// Kiểm tra ngân sách khởi động cùng nguyên tắc với Start/Resume/Continue: nhập liệu là lời gọi model toàn quy trình,
	// ngân sách đã quá hạn sẽ không được khởi động (§13.1 "Tích hợp giám sát ngân sách hiện có").
	if err := h.budget.Refuse(); err != nil {
		return nil, err
	}
	if err := h.acquireExclusive("nhập liệu"); err != nil {
		return nil, err
	}
	// Đăng ký hàm hủy: dừng cứng do ngân sách/tạm dừng thủ công sẽ thông qua abortWithEvent hủy context nhập liệu của chính nó
	// (nếu không lính gác ngân sách chỉ đi tạm dừng Engine vốn chưa chạy, quá trình nhập liệu vẫn tiếp tục đốt tiền).
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	deps := imp.Deps{
		Store:         h.store,
		CommitChapter: tools.NewCommitChapterTool(h.store, h.styleStats),
		Segment:       h.importCaller("segment"),
		Analyze:       h.importCaller("analyze"),
		Synthesize:    h.importCaller("synthesize"),
		Prompts: imp.Prompts{
			Segment:    h.bundle.Prompts.ImportSegment,
			Analyze:    h.bundle.Prompts.ImportAnalyze,
			Synthesize: h.bundle.Prompts.ImportSynthesize,
			Range:      h.bundle.Prompts.ImportRange,
		},
	}
	ch, err := imp.Run(ctx, deps, opts)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return h.superviseImport(ch, opts), nil
}

// ImportResumeHint Trả về một dòng gợi ý nhập liệu chưa hoàn thành (nếu không có thì trả về chuỗi rỗng), cung cấp cho TUI chủ động thông báo khi khởi động (RFC §18.2).
// Chỉ gọi một lần khi khởi động: Nội bộ sẽ tính toán lại InputDigest của các thành phần trong không gian làm việc, không phù hợp đưa vào vòng lặp kiểm tra snapshot.
func (h *Host) ImportResumeHint() string {
	return imp.ResumeSummary(h.store)
}

// importCaller Phân giải cấp độ model cho hàm ngữ nghĩa nhập liệu (RFC §13.1): Nếu tồn tại cấu hình roles cho import_<fn>
// thì sử dụng cấp độ đó (lượng dùng cũng tính vào sổ của vai trò đó), ngược lại sẽ sử dụng architect. Đây là cấu hình gọi, không làm thay đổi bất kỳ thỏa thuận ngữ nghĩa nào.
func (h *Host) importCaller(fn string) imp.Caller {
	role := "import_" + fn
	if _, _, explicit := h.models.CurrentSelection(role); !explicit {
		role = "architect"
	}
	model := h.models.ForRoleWithFailover(role, func(ev bootstrap.FailoverEvent) {
		slog.Warn("chuyển đổi provider nhập liệu", "module", "import", "role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err)
	})
	model = newUsageTrackedModel(model, role, h.usage.Record)
	return imp.Caller{Model: model, Runtime: h.importModelRuntime(role, model)}
}

// importModelRuntime Dò tìm khả năng gọi của model được chọn, cung cấp cho imp dùng trong ngân sách kép / tự thích ứng thinking (RFC §13/§21).
// Các trường dò tìm thất bại sẽ để nguyên giá trị 0, bên imp sẽ fallback về mặc định bảo thủ, đảm bảo có thể chạy đúng đắn kể cả khi không có thông tin khả năng.
// Cấu trúc xuất dữ liệu do llmcontract của imp tự đọc sự thật từ model mỗi lần trước khi yêu cầu, không lưu lại trong Runtime.
func (h *Host) importModelRuntime(role string, model agentcore.ChatModel) imp.ModelRuntime {
	var rt imp.ModelRuntime
	provider, name, _ := h.models.CurrentSelection(role)
	if name == "" {
		name = bootstrap.ModelName(model)
		provider = bootstrap.ModelProvider(model)
	}
	// Giới hạn ngữ cảnh / hoàn thiện: registry là nguồn đáng tin cậy duy nhất (hàm Info() của model bị bọc không chứa thông tin về cửa sổ context).
	rt.ContextTokens, _ = h.cfg.ResolveContextWindow(provider, name)
	if entry, ok := modelreg.DefaultRegistry().Resolve(name); ok {
		rt.MaxOutputTokens = entry.MaxTokens
	}
	// thinking: xử lý dựa trên reasoning effort của vai trò và khả năng của model; không hỗ trợ thì không gửi (cùng chiến lược với arbiter).
	if level, err := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role)); err == nil {
		if resolved, ok := agents.ResolveThinkingForModel(model, level); ok {
			rt.Thinking = resolved
		}
	}
	return rt
}

// Simulate Đọc thư mục simulate và tạo hoặc cập nhật gia tăng chân dung mô phỏng.
func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("tạo chân dung mô phỏng"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		h.releaseExclusive()
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	ch, err := sim.Run(ctx, deps, sim.Options{SourceDir: filepath.Join(wd, "simulate")})
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// ImportSimulationProfile Nhập chân dung mô phỏng được tạo trước đó.
func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("nhập chân dung mô phỏng"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	ch, err := sim.RunImport(ctx, h.store, path)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// acquireExclusive Chiếm dụng tự động khe tác vụ độc quyền nền (import/simulate/revision): Engine đang chạy, cửa sổ đồng sáng tác giai đoạn,
// hoặc đã có tác vụ độc quyền đang chạy thì từ chối. Thành công tức là đã đăng ký chiếm dụng, tác vụ kết thúc phải gọi releaseExclusive để giải phóng —— nếu không thì hai tác vụ nhập liệu
// hoặc nhập liệu + mô phỏng sẽ xảy ra tình trạng cạnh tranh giành quyền sửa đổi trên cùng một trạng thái. Vá lỗ hổng trước đây chỉ kiểm tra ==running/cocreating mà không đăng ký chính tác vụ đó.
func (h *Host) acquireExclusive(action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closing:
		return fmt.Errorf("Host đang đóng, không thể %s", action)
	// engine.isRunning() phải được kiểm tra: Abort trước hết sẽ đặt lifecycle=paused sau đó chờ bất đồng bộ cho đến khi goroutine kết thúc,
	// trong cửa sổ đó lifecycle không còn là running nhưng động cơ vẫn có thể đang ghi vào store (cùng nguyên tắc như kiểm soát khởi động).
	case h.lifecycle == lifecycleRunning || h.engine.isRunning():
		return fmt.Errorf("động cơ sáng tác đang chạy hoặc đang dừng, vui lòng chờ để %s", action)
	case h.cocreating:
		return fmt.Errorf("đồng sáng tác giai đoạn đang diễn ra, vui lòng hoàn thành trước khi %s", action)
	case h.exclusive != "":
		return fmt.Errorf("%s đang diễn ra, vui lòng hoàn thành trước khi %s", h.exclusive, action)
	}
	h.exclusive = action
	return nil
}

// releaseExclusive Giải phóng khe tác vụ nền độc quyền (cùng với hàm hủy đã đăng ký).
func (h *Host) releaseExclusive() {
	h.mu.Lock()
	cancel := h.exclusiveCancel
	h.exclusive = ""
	h.exclusiveCancel = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel() // Tác vụ đã kết thúc: giải phóng context nhánh phái sinh; không ảnh hưởng tới runner đã thoát
	}
}

// superviseExclusive Chuyển tiếp các sự kiện tác vụ độc quyền, giải phóng khe chiếm dụng khi kênh bị đóng (tác vụ hoàn tất).
func superviseExclusive[T any](h *Host, src <-chan T) <-chan T {
	out := make(chan T, 32)
	if !h.launchAsync(func() {
		defer close(out)
		defer h.releaseExclusive()
		for ev := range src {
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				// Tiếp tục rút rỗng các kênh nguồn trong suốt quá trình đóng để tránh producer bị chặn bởi các sự kiện trạng thái cuối cùng và không thể thoát ra.
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// superviseImport là chủ sở hữu duy nhất xác định "liệu có tiếp sức sau khi nhập liệu hoàn thành không": chuyển tiếp sự kiện nhập liệu, khi kết thúc thành công sẽ giải phóng khe độc quyền trước,
// sau đó quyết định và thực hiện tiếp sức, cuối cùng ghi kết quả tiếp sức thật vào trường Continued của sự kiện StageDone. TUI chỉ dựa vào đây để render,
// không dùng tham số --continue trên máy cục bộ để đoán mò trạng thái chạy (loại bỏ vấn đề tình trạng tương tranh chạy đua thời gian do 3 bên Runner/Host/TUI tự giải thích).
func (h *Host) superviseImport(src <-chan imp.Event, opts imp.Options) <-chan imp.Event {
	out := make(chan imp.Event, 32)
	if !h.launchAsync(func() {
		defer close(out)
		released := false
		release := func() {
			if !released {
				released = true
				h.releaseExclusive()
			}
		}
		defer release()
		for ev := range src {
			if ev.Stage == imp.StageDone {
				release() // Giải phóng khe độc quyền trước, thì startEngine tiếp sức mới có thể qua cửa bảo vệ độc quyền
				ev.Continued = h.continueAfterImport(opts)
			}
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// launchAsync đăng ký một tác vụ nền trong suốt vòng đời của Host. closing và WaitGroup.Add đều bị bảo vệ
// bởi cùng một khóa, đảm bảo sau khi Close gọi Wait thì sẽ không xảy ra Add mới.
func (h *Host) launchAsync(fn func()) bool {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return false
	}
	h.asyncWG.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.asyncWG.Done()
		fn()
	}()
	return true
}

// runAsync tái sử dụng chức năng đăng ký tác vụ nền của Host, đồng thời trả về lỗi nghiệp vụ cho phía gọi.
func (h *Host) runAsync(fn func() error) (error, bool) {
	result := make(chan error, 1)
	if !h.launchAsync(func() { result <- fn() }) {
		return nil, false
	}
	return <-result, true
}

// continueAfterImport quyết định và thực hiện tiếp sức tự động thực sự từ tham số --continue, trả về việc Engine đã chạy hay chưa.
// Ý định tiếp sức hợp lệ = opts lần này hoặc ý định trong bộ lưu trữ không gian làm việc (bảo vệ kịch bản gọi /import mà không có tham số sau sự cố);
// chỉ tiếp sức trong chế độ tự động (auto) đẩy tiến, do chức năng quy hoạch mở rộng arc tự động tiếp nối truyện mở, hoặc để kết thúc truyện đã hoàn thành; chế độ review giao cho người dùng gọi /next.
func (h *Host) continueAfterImport(opts imp.Options) bool {
	want := opts.ContinueAfter
	if !want {
		in, err := imp.OpenWorkspace(h.store.Dir()).LoadIntent()
		if err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Nhập liệu hoàn thành, nhưng không thể tải ý định tiếp sức tự động: " + err.Error()})
		} else if in != nil {
			want = in.ContinueAfterImport
		}
	}
	if !want {
		return false
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil || meta == nil {
		slog.Warn("tự động tiếp sức sau nhập liệu: tải RunMeta thất bại", "module", "host", "err", err)
		return false
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "Nhập liệu hoàn thành; đang ở chế độ nghiệm thu từng chương, nhập chỉ thị tiếp tục hoặc /next để tiếp sức sáng tác"})
		return false
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: "Nhập liệu hoàn thành, đang tự động tiếp sức sáng tác"})
	if !h.startEngine(nil) {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Khởi động tiếp sức tự động thất bại, vui lòng nhập chỉ thị tiếp tục để khôi phục thủ công"})
		return false
	}
	return true
}

// Export Xuất các chương đã hoàn thành ra tệp bên ngoài (hiện chỉ hỗ trợ TXT).
//
// Khác với ImportFrom: Xuất là một tác vụ chỉ đọc (không động chạm tới Progress / Checkpoint),
// vì thế **không yêu cầu Engine phải dừng** —— đang viết giữa chừng cũng có thể xuất "thành phẩm hiện có" ra ngoài bất cứ lúc nào.
// Chỉ đọc theo nhất quán snapshot giữa Progress.CompletedChapters + bản thảo chương cuối + đại cương + premise.
func (h *Host) Export(ctx context.Context, opts exp.Options) (*exp.Result, error) {
	return exp.Run(ctx, exp.Deps{Store: h.store}, opts)
}
