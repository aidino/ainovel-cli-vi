package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"

	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/notify"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// engine là công cụ thực thi mang tính quyết định: đọc sự kiện → Route → xác minh tiền trạm → trực tiếp chạy Worker →
// kiểm tra đẩy tiến → vòng lặp; kịch bản ngữ nghĩa tham vấn Arbiter theo nhu cầu. Nó thực thi quyết định, không tham gia đánh giá văn học
// (docs/engine-rfc.md). goroutine đơn chạy tuần tự, trạng thái điều khiển chỉ thay đổi ở ranh giới vòng lặp.
type engine struct {
	store   *storepkg.Store
	workers *subagent.Runner

	arbiterModel    agentcore.ChatModel
	failurePrompt   string
	planStartPrompt string // Từ khóa hệ thống phán quyết khởi động: khi phán quyết chưa hoàn thành, engine dựa vào StartPrompt để phán quyết tại chỗ
	style           string // Tên phong cách, khi phán quyết bổ sung sẽ truyền cho DecidePlanStart
	// reconsult đưa can thiệp hết hạn trở lại đường dẫn phán quyết đầy đủ của host (lưu trữ/kiểm toán/áp dụng toàn bộ hành động),
	// thực thi bất đồng bộ——engine chỉ vứt bỏ phái phát hết hạn, không tự thực hiện phán quyết lại một cách chắp vá.
	reconsult func(text string)

	observer  *observer
	budget    *BudgetSentinel
	gate      *ChapterAdvanceGate
	refresh   func() // Làm mới RestorePack trước mỗi lần phái phát writer
	emitEvent func(Event)
	notify    func(kind, level, title, body string)
	onPause   func(summary string) // engine tự động tạm dừng (ngắt kết nối do bế tắc/phán quyết thất bại abort): theo ngữ nghĩa tạm dừng thống nhất của host (lifecycle=paused)
	onDone    func()               // run kết thúc (bất kỳ lý do nào); host xác định trạng thái cuối cùng dựa trên sự kiện store

	mu      sync.Mutex
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	running bool
	pending []controlOp       // Hành động trạng thái kiểm soát của can thiệp, gửi ở ranh giới
	next    *flow.Instruction // Chỉ thị ưu tiên thực thi ở vòng tiếp theo (plan_start / arbiter dispatch)
	// deferGateForNext chỉ sinh tử cùng next: hold+dispatch bắt buộc phải chạy theo cặp
	// editor/writer trước, để nó thiết lập hàng đợi làm lại, sau đó Gate mới có thể phán đoán rewrites_drained.
	deferGateForNext bool

	// Theo dõi bế tắc: sau khi vòng trước thực thi, Route vẫn tạo ra cùng một key chỉ thị thì sẽ tích lũy.
	// Chỉ thị Router là hình chiếu của điều kiện hậu nhiệm vụ; hoàn thành thực sự sẽ làm chỉ thị tiếp theo thay đổi.
	lastKey string
	repeats int
	// Thử lại khi thất bại: cùng một key chỉ thị chỉ thử lại một lần, thất bại nữa thì hỏi Arbiter.
	failedKey string
	// Giữ lại lỗi Worker gần nhất của cùng chỉ thị, để phán quyết bế tắc thấy được nguyên nhân thất bại thực sự.
	lastWorkerErrorKey string
	lastWorkerError    error
}

// deadlockConsultAt / deadlockAbortAt: repeats đạt ngưỡng trước thì hỏi Arbiter, đạt ngưỡng sau thì ngắt cứng.
// Engine mang tính quyết định phải đưa ra giới hạn trên rõ ràng cho vòng lặp không có tiến triển (RFC §5).
const (
	deadlockConsultAt = 3
	deadlockAbortAt   = 5
)

// controlOp là hành động sửa đổi trạng thái kiểm soát trong phán quyết can thiệp (gửi ở ranh giới; RFC §3).
// text/facts giữ lại ngữ cảnh tham vấn ban đầu: khi dispatch đối soát thất bại sẽ hỏi lại bằng sự kiện mới.
type controlOp struct {
	hold     *arbiter.AdvanceHoldOp
	reopen   *arbiter.ReopenOp
	dispatch *arbiter.DispatchOp
	text     string
	facts    arbiter.InterventionFacts
}

// start khởi động vòng lặp engine; nếu đang chạy thì no-op (trả về false).
func (e *engine) start(initial *flow.Instruction) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agentcore.WithToolProgress(ctx, e.observer.workerProgress)
	e.cancel = cancel
	e.running = true
	// Khi initial trống thì không ghi đè e.next——sự can thiệp trong thời gian ngừng máy có thể đã được đưa vào
	// phái phát phán quyết (như editor làm lại) thông qua applyControlOp, start(nil) xóa nó đi sẽ làm Route phái writer viết tiếp,
	// trái với ý định của người dùng.
	if initial != nil {
		e.next = initial
		e.deferGateForNext = false
	}
	e.lastKey, e.repeats, e.failedKey = "", 0, ""
	e.lastWorkerErrorKey, e.lastWorkerError = "", nil
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(ctx)
	}()
	return true
}

// abort hủy bỏ vòng lặp hiện tại (ngữ nghĩa tạm dừng; checkpoint đảm bảo không mất mát).
func (e *engine) abort() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// wait chờ Engine goroutine hiện tại thoát hoàn toàn. Host.Close sẽ cancel trước rồi mới gọi nó,
// đảm bảo viết công cụ và runEnded đều kết thúc rồi mới đóng kênh sự kiện và thoát tiến trình.
func (e *engine) wait() {
	e.wg.Wait()
}

func (e *engine) isRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// enqueue đưa hành động trạng thái kiểm soát của can thiệp vào hàng đợi ranh giới (engine đang chạy); trả về false nghĩa là chưa chạy,
// bên gọi nên tự thực thi ngay lập tức.
func (e *engine) enqueue(op controlOp) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return false
	}
	e.pending = append(e.pending, op)
	return true
}

func (e *engine) run(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancel = nil
		leftover := e.pending
		e.pending = nil
		e.mu.Unlock()
		// Thoát khỏi tình trạng tương tranh: các hành động can thiệp còn sót lại khi enqueue đồng thời với thoát không được âm thầm vứt bỏ——
		// hold/reopen là việc ghi lại sự kiện một cách lũy đẳng, dùng ctx độc lập để thực thi bổ sung; dispatch thì không có engine để phái đi,
		// khôi phục lưu trữ bền vững PendingSteer (host có thể đã dọn dẹp theo kiểu "vào hàng đợi thành công"), lần sau
		// khi Resume/Continue sẽ phát lại toàn bộ can thiệp.
		for _, op := range leftover {
			if op.dispatch != nil {
				if op.text != "" {
					if err := e.store.RunMeta.SetPendingSteer(op.text); err != nil {
						slog.Warn("Ghi lưu lại can thiệp còn sót thất bại", "module", "engine", "err", err)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Engine đã dừng, phái phát phán quyết chưa thực thi; can thiệp đã được giữ lại, sẽ tự động phán quyết lại khi tiếp tục sáng tác"})
				op.dispatch = nil
			}
			if op.hold != nil || op.reopen != nil {
				if err := e.applyControlOp(context.Background(), op); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
						Summary: "Thất bại khi bù can thiệp lúc engine thoát: " + err.Error()})
				}
			}
		}
		e.onDone()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		// hold+dispatch bắt buộc phải để cho phái phát theo cặp thiết lập sự kiện làm lại trước; các trường hợp khác kiểm tra đồng loạt trước khi phái phát
		// Gate, đảm bảo boundary hold và review không có quyền hạn sẽ không chạy dư một Worker nào.
		deferGate := e.applyPendingOps(ctx) || e.nextDefersGate()
		if !deferGate {
			if e.gate.HandleBoundary() {
				return
			}
		}

		inst := e.takeNext()
		if inst == nil {
			state, err := flow.LoadState(e.store)
			if err != nil {
				e.pauseWithNotify(notify.KindWorkerFailure, "Đọc sự kiện định tuyến thất bại, đã tạm dừng: "+err.Error())
				return
			}
			// Tóm tắt tập có thể đã ghi đĩa nhưng quá trình chưa kịp MarkComplete. Khi công cụ tổng hợp đã hoàn thiện
			// đầu tiên bổ sung phán quyết hoàn thành theo sự kiện, sau đó giao cho Router, tránh việc tập kết thúc bị phái đi nhầm thành tiếp tục tập.
			if state.AggregateRefresh == nil && state.Progress != nil && state.Progress.Layered &&
				state.Progress.Phase == domain.PhaseWriting && state.ArcBoundary != nil &&
				state.ArcBoundary.IsVolumeEnd && state.HasArcReview && state.HasArcSummary && state.HasVolumeSummary {
				complete, reconcileErr := tools.ReconcileLayeredCompletion(e.store)
				if reconcileErr != nil {
					e.pauseWithNotify(notify.KindWorkerFailure, "Khôi phục trạng thái hoàn kết thất bại, đã tạm dừng: "+reconcileErr.Error())
					return
				}
				if complete {
					continue
				}
			}
			inst = flow.Route(state)
		}
		if inst == nil {
			var err error
			inst, err = e.planStartFallback(ctx)
			if err != nil {
				e.pauseWithNotify(notify.KindPlanStart, "Đọc sự kiện khôi phục quy hoạch thất bại, đã tạm dừng: "+err.Error())
				return
			}
		}
		if inst == nil {
			// Kịch bản ngữ nghĩa hoặc trạng thái kết thúc: hoàn bản → kết thúc dứt điểm; phần còn lại (Steering còn sót lại v.v.)
			// → tự nhiên ngừng máy, đợi người dùng Continue / can thiệp.
			return
		}
		replaced, err := e.precheck(inst)
		if err != nil {
			e.pauseWithNotify(notify.KindWorkerFailure, "Xác minh tiền trạm phái phát thất bại, đã tạm dừng: "+err.Error())
			return
		}
		if replaced != nil {
			inst = replaced
		}
		allowed, gateErr := e.gate.Allow(inst)
		if gateErr != nil {
			e.pauseWithNotify(notify.KindAdvanceGate, "Lỗi kiểm soát đẩy tiến chương, đã tạm dừng: "+gateErr.Error())
			return
		}
		if !allowed {
			return
		}
		if stop := e.trackDeadlock(ctx, &inst); stop {
			return
		}
		if inst == nil {
			continue // Phán quyết bế tắc yêu cầu tính toán lại định tuyến
		}

		err = e.runWorker(ctx, inst)
		if ctx.Err() != nil {
			return
		}
		e.rememberWorkerError(inst, err)
		if err != nil {
			// trackDeadlock ghi trước lần thử này trước khi phái phát. Nếu chưa đi vào Worker hợp lệ
			// các lỗi thực thi ngữ nghĩa không được tính là "cùng một nhiệm vụ không có tiến triển".
			e.discardNonSemanticDeadlockAttempt(inst, err)
			if stop := e.handleWorkerError(ctx, inst, err); stop {
				return
			}
		}

		// Ranh giới chính sách: Dừng lỗ ngân sách ưu tiên hơn tạm dừng nghiệm thu/đẩy tiến.
		if e.budget.HandleBoundary() {
			return
		}
		if e.gate.HandleBoundary() {
			return
		}
	}
}

func (e *engine) takeNext() *flow.Instruction {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst := e.next
	e.next = nil
	e.deferGateForNext = false
	return inst
}

func (e *engine) nextDefersGate() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next != nil && e.deferGateForNext
}

// planStartFallback bao phủ hai khoảng trống khi sự kiện quy hoạch vắng mặt và Route không thể suy luận ra nhà quy hoạch:
//  1. Phán quyết đã ghi đĩa, save_foundation đầu tiên chưa diễn ra → tiếp tục chạy theo PlanStartRecord đã cố định,
//     không phán quyết lại (RFC §6); sau khi foundation đầu tiên ghi đĩa, tier sẽ vào vị trí, bổ sung nhánh để tiếp quản.
//  2. Phán quyết chưa bao giờ hoàn tất (model bị lỗi lúc khởi động) nhưng sự kiện đầu vào StartPrompt vẫn còn → phán quyết bổ sung tại chỗ.
//     Đây là lần thử lại phán quyết đầu tiên, không vi phạm "khôi phục không phụ thuộc vào phán quyết lại"——kỷ luật đó áp dụng cho phán quyết đã tồn tại.
//     Phán quyết bổ sung thất bại sẽ đi vào tạm dừng hiển thị: khởi động thất bại không cho phép ngừng máy âm thầm.
func (e *engine) planStartFallback(ctx context.Context) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return nil, nil
	}
	if progress.Phase == domain.PhaseWriting || progress.Phase == domain.PhaseComplete {
		return nil, nil
	}
	meta, err := e.store.RunMeta.Load()
	if err != nil {
		return nil, fmt.Errorf("load run meta: %w", err)
	}
	if meta == nil || meta.PlanningTier != "" {
		return nil, nil
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w", err)
	}
	if len(missing) == 0 {
		return nil, nil
	}
	if meta.PlanStart != nil {
		return &flow.Instruction{
			Agent:  meta.PlanStart.Planner,
			Task:   meta.PlanStart.PlannerTask,
			Reason: "Bắt đầu quy hoạch theo phán quyết khởi động đã cố định",
		}, nil
	}
	if meta.StartPrompt == "" {
		return nil, nil
	}
	return e.retryPlanStart(ctx, meta.StartPrompt), nil
}

// retryPlanStart phán quyết bổ sung quyết định khởi động và cố định lại (phán quyết rơi vào sự kiện trước rồi mới thực thi, đồng cấu với StartPrepared).
func (e *engine) retryPlanStart(ctx context.Context, prompt string) *flow.Instruction {
	start := time.Now()
	decision, derr := runObservedDecision(e.observer, "Bổ sung phán quyết khởi động", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(ctx, e.arbiterModel, e.planStartPrompt, prompt, e.style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: prompt,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	rec, recErr := e.store.Decisions.Append(rec)
	if recErr != nil {
		slog.Warn("Ghi log kiểm toán phán quyết khởi động bổ sung ra đĩa thất bại", "module", "engine", "err", recErr)
	}
	if derr != nil {
		e.pauseWithNotify(notify.KindPlanStart, "Phán quyết khởi động thất bại, đã tạm dừng (vui lòng kiểm tra cấu hình model/mạng rồi tiếp tục): "+derr.Error())
		return nil
	}
	if err := e.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: prompt, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		e.pauseWithNotify(notify.KindPlanStart, "Phán quyết khởi động không thể ghi ra đĩa, đã tạm dừng: "+err.Error())
		return nil
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: fmt.Sprintf("Phán quyết khởi động đã được bổ sung (nhà quy hoạch: %s——%s)", decision.Planner, decision.Reason)})
	return &flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}
}

// precheck là hóa thân mang tính quyết định của ToolGate gốc: phái phát không hợp lệ sẽ bị ghi đè trực tiếp, không cần văn bản hướng dẫn.
func (e *engine) precheck(inst *flow.Instruction) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		// Lối thoát hợp pháp duy nhất trong kỳ hoàn bản là reopen (hành động can thiệp), bất kỳ phái phát nào đều bị vứt bỏ trực tiếp.
		slog.Warn("Phái phát trong kỳ hoàn bản bị vứt bỏ", "module", "engine", "agent", inst.Agent)
		return &flow.Instruction{}, nil // Đặt rỗng: vòng Route tiếp theo trở về nil tự nhiên ngừng máy
	}
	if inst.Agent == "writer" {
		if progress == nil || progress.Phase != domain.PhaseWriting {
			phase := "<nil>"
			if progress != nil {
				phase = string(progress.Phase)
			}
			return nil, fmt.Errorf("writer chỉ có thể phái phát trong giai đoạn writing (phase hiện tại=%s): %w", phase, errInvalidWriteTarget)
		}
		ch, err := writerTargetChapter(e.store)
		if err != nil {
			return nil, err
		}
		if ch > 0 {
			if err := tools.EnsureChapterExpanded(e.store, ch); err != nil {
				if !errors.Is(err, errs.ErrToolPrecondition) {
					return nil, err
				}
				// Chương mục tiêu chưa được triển khai → đổi phái phát dứt điểm thành architect_long triển khai (văn bản hướng dẫn của gate gốc
				// là nói cho LLM; Engine trực tiếp làm điều đúng đắn).
				return &flow.Instruction{
					Agent:  "architect_long",
					Task:   fmt.Sprintf("Arc tiếp theo là bộ xương (%s). Gọi save_foundation(type=expand_arc) để triển khai arc tiếp theo; nếu tập hiện tại đã viết xong, đổi dùng type=append_volume để thêm và triển khai tập tiếp theo.", err),
					Reason: "Chương mục tiêu sáng tác chưa được triển khai, triển khai trước rồi mới viết tiếp",
				}, nil
			}
		}
		e.refresh()
	}
	return nil, nil
}

// writerTargetChapter suy luận chương mà lần phái phát tiếp theo writer sẽ thực sự viết (phần đầu hàng đợi làm lại, nếu không thì là chương tiếp theo).
func writerTargetChapter(st *storepkg.Store) (int, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return 0, fmt.Errorf("progress chưa khởi tạo")
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0], nil
	}
	return progress.NextChapter(), nil
}

// trackDeadlock duy trì bộ đếm bế tắc: Agent+Task giống nhau liên tục xuất hiện cho thấy vòng trước
// đã không thỏa mãn điều kiện hậu của định tuyến. Các checkpoint trung gian plan/draft/edit bên trong Worker
// chỉ dùng để khôi phục và quan sát, không thể đặt lại bộ đếm cấp Engine (issue #84).
// Khi repeats đạt ngưỡng thì tham vấn Arbiter, giới hạn cứng sẽ trực tiếp ngắt kết nối.
// Trả về stop=true nghĩa là vòng này nên kết thúc tuần hoàn; inst có thể bị Arbiter ghi đè (reroute) hoặc đặt thành nil (tính toán lại).
func (e *engine) trackDeadlock(ctx context.Context, inst **flow.Instruction) (stop bool) {
	in := *inst
	if in == nil || in.Agent == "" {
		*inst = nil
		return false
	}
	key := instructionKey(in)
	if key == e.lastKey {
		e.repeats++
	} else {
		e.lastKey, e.repeats = key, 1
	}
	if e.repeats < deadlockConsultAt {
		return false
	}
	if e.repeats >= deadlockAbortAt {
		e.pauseStuck(notify.KindDeadlock, in, fmt.Sprintf("Ngắt kết nối bế tắc: Chỉ thị liên tục %d lần không có tiến triển (%s), đã tạm dừng chờ can thiệp thủ công", e.repeats, in.Agent))
		return true
	}
	// Tham vấn bế tắc qua Arbiter (repeats ∈ [consultAt, abortAt)). Phán quyết retry không xóa bộ đếm.
	facts := e.failureFacts("deadlock", in, e.workerErrorFor(in))
	decision, err := runObservedDecision(e.observer, "Phán quyết bế tắc", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("deadlock", in, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindDeadlock, "Phán quyết bế tắc thất bại, đã tạm dừng chờ can thiệp thủ công: "+err.Error())
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		*inst = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		return false
	default: // abort
		e.pauseStuck(notify.KindDeadlock, in, "Phán quyết bế tắc: "+decision.Reason)
		return true
	}
}

// runWorker trực tiếp chạy một subagent một lần: sự kiện DISPATCH + trung kế tiến độ + phân tích kết quả.
func (e *engine) runWorker(ctx context.Context, inst *flow.Instruction) error {
	e.observer.dispatchStart(inst.Agent, inst.Task, inst.Reason)
	// Nhiệm vụ Writer được đánh dấu trước là đang tiến hành (giống với Dispatcher cũ: UI đại cương ngay lập tức phản ánh "▸ Đang tiến hành").
	if inst.Agent == "writer" && inst.Chapter > 0 {
		if err := e.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: %w", errInvalidWriteTarget, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
		if err := e.store.Progress.StartChapter(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: Đánh dấu trước chương %d đang tiến hành thất bại: %w", errInvalidWriteTarget, inst.Chapter, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
	}

	// Tiến độ Worker được trung kế qua ctx ToolProgress tới observer.
	runCtx := agentcore.WithToolProgress(ctx, func(p agentcore.ProgressPayload) {
		e.observer.workerProgress(p)
	})
	_, err := e.workers.Run(runCtx, inst.Agent, inst.Task)
	if err == nil {
		// Thành công thì xóa bỏ dấu vết thất bại: lần thất bại tiếp theo của cùng key sẽ lại được hưởng hạn mức "thử lại một lần trước".
		e.failedKey = ""
	}
	e.observer.dispatchFinish(inst.Agent, err)
	return err
}

// handleWorkerError thử lại chỉ thị đó một lần trước, sau đó giao loại lỗi và sự kiện hiện tại cho Arbiter.
// Engine không mã hóa cứng những lỗi thực thi nào là "tất yếu không thể khôi phục"; việc đổi phái phát ngữ nghĩa do model quyết định, ranh giới Store tiếp tục
// chịu trách nhiệm ngăn chặn ghi đè bất hợp pháp.
func (e *engine) handleWorkerError(ctx context.Context, inst *flow.Instruction, werr error) (stop bool) {
	msg := werr.Error()

	key := instructionKey(inst)
	if e.failedKey != key {
		// Lần đầu thất bại: chỉ thị ban đầu thử lại một lần (vòng tiếp theo Route tính lại, theo kiểu điều khiển theo sự kiện thì mặc nhiên là lũy đẳng).
		e.failedKey = key
		return false
	}
	e.failedKey = ""
	facts := e.failureFacts("worker_failure", inst, werr)
	decision, err := runObservedDecision(e.observer, "Phán quyết thất bại", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("worker_failure", inst, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindWorkerFailure, "Phán quyết thất bại không khả dụng, đã tạm dừng chờ can thiệp thủ công: "+msg+contentFilterAdvice(werr))
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		e.mu.Lock()
		e.next = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		e.deferGateForNext = false
		e.mu.Unlock()
		return false
	default: // abort
		e.pauseStuck(notify.KindWorkerFailure, inst, "Phán quyết thất bại: "+decision.Reason+contentFilterAdvice(werr))
		return true
	}
}

// pauseStuck tạm dừng khi engine từ bỏ một chỉ thị: chương làm lại ra khỏi hàng đợi trước rồi mới dừng. Chỉ dùng khi engine đã phán định chỉ thị đó
// không có lối thoát (ngắt kết nối bế tắc, phán quyết bế tắc/thất bại trả về abort), các sự cố hạ tầng như phán quyết không khả dụng vẫn đi theo
// pauseWithNotify——đó là vấn đề bên ngoài, không nên đền bù bằng một chương làm lại.
func (e *engine) pauseStuck(kind string, inst *flow.Instruction, body string) {
	if e.dropStuckRewrite(inst) {
		body += fmt.Sprintf("；chương %d đã được đưa ra khỏi hàng đợi làm lại (giữ bản thảo cuối cùng của phiên bản trước), tiếp tục sáng tác sẽ đẩy tiến từ các chương tiếp theo", inst.Chapter)
	}
	e.pauseWithNotify(kind, body)
}

// dropStuckRewrite đưa các chương làm lại bị kẹt ra khỏi hàng đợi. PendingRewrites là sự kiện đã lưu, nếu engine từ bỏ
// chỉ thị này mà không lấy ra khỏi hàng đợi thì khi khởi động lại sẽ lập tức phát lại đúng chỉ thị chết đó, làm kẹt vĩnh viễn toàn bộ quyển sách (issue #110).
// Trả về true nghĩa là đã thực sự lấy ra khỏi hàng đợi.
func (e *engine) dropStuckRewrite(inst *flow.Instruction) bool {
	if inst == nil || inst.Agent != "writer" || inst.Chapter <= 0 {
		return false
	}
	progress, err := e.store.Progress.Load()
	if err != nil || progress == nil || !slices.Contains(progress.PendingRewrites, inst.Chapter) {
		return false
	}
	if err := e.store.Progress.CompleteRewrite(inst.Chapter); err != nil {
		slog.Warn("Đưa chương làm lại bị kẹt ra khỏi hàng đợi thất bại", "module", "engine", "chapter", inst.Chapter, "err", err)
		return false
	}
	return true
}

// discardNonSemanticDeadlockAttempt thu hồi những ghi chép trước của trackDeadlock cho lần phái phát này
// về thử nghiệm ngữ nghĩa. Chỉ loại trừ các loại lỗi ổn định khi lời gọi model không được thực thi hoàn chỉnh; content_filter
// được giữ lại trên đường dẫn tự phục hồi ban đầu, max_turns, stop_guard, các trạng thái không có tiến triển thực sự như hủy bỏ vẫn tính là số đếm.
func (e *engine) discardNonSemanticDeadlockAttempt(inst *flow.Instruction, werr error) {
	if inst == nil || !isNonSemanticWorkerFailure(werr) {
		return
	}
	key := instructionKey(inst)
	if e.lastKey != key || e.repeats <= 0 {
		return
	}
	e.repeats--
	if e.repeats == 0 {
		e.lastKey = ""
	}
}

// isNonSemanticWorkerFailure chỉ nhận diện các lỗi "lần thực thi model này không tạo ra ngữ nghĩa có thể đánh giá".
// Ưu tiên dựa vào giao ước chuỗi lỗi của agentcore; khi chuỗi lỗi bị phía nhà cung cấp làm phẳng thì tái sử dụng phân loại log.
func isNonSemanticWorkerFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, agentcore.ErrContextOverflow) || errors.Is(err, agentcore.ErrStreamPartial) {
		return true
	}
	providerErr := agentcore.ClassifyProvider(err)
	classified := errors.Is(providerErr, agentcore.ErrProviderStreamIdle) ||
		errors.Is(providerErr, agentcore.ErrProviderQuota) ||
		errors.Is(providerErr, agentcore.ErrProviderRateLimit) ||
		errors.Is(providerErr, agentcore.ErrProviderTimeout) ||
		errors.Is(providerErr, agentcore.ErrProviderAuth) ||
		errors.Is(providerErr, agentcore.ErrProviderNetwork) ||
		errors.Is(providerErr, agentcore.ErrProviderOverloaded)
	return classified || errorKind(err, err.Error()) == "overloaded"
}

func instructionKey(inst *flow.Instruction) string {
	if inst == nil {
		return ""
	}
	return inst.Agent + "\x00" + inst.Task
}

func (e *engine) rememberWorkerError(inst *flow.Instruction, workerErr error) {
	if workerErr == nil || inst == nil {
		e.lastWorkerErrorKey, e.lastWorkerError = "", nil
		return
	}
	e.lastWorkerErrorKey, e.lastWorkerError = instructionKey(inst), workerErr
}

func (e *engine) workerErrorFor(inst *flow.Instruction) error {
	if e.lastWorkerErrorKey != instructionKey(inst) {
		return nil
	}
	return e.lastWorkerError
}

// contentFilterAdvice đính kèm hướng dẫn thực thi cho người dùng khi bị tạm dừng do kiểm duyệt nội dung chặn.
// Việc kiểm duyệt là một hộp đen của nhà cung cấp dịch vụ, kiểm tra trước/né tránh đều không khả thi, điều có thể làm chỉ là giao quyết định vào tay người dùng;
// Bản thân việc chặn không bị ngắt sớm——việc đổi ngữ cảnh rồi phái phát lại có tỷ lệ tự khỏi (đã test thực tế ở ch21-24),
// đi hết "thử lại miễn phí → trọng tài" rồi mới tạm dừng.
func contentFilterAdvice(werr error) string {
	if !errors.Is(werr, agentcore.ErrProviderContentFilter) {
		return ""
	}
	return ". Đây là chặn kiểm duyệt nội dung của nhà cung cấp dịch vụ (không phải lỗi cục bộ), tùy chọn: dùng /model chuyển sang nhà cung cấp dịch vụ không có lớp kiểm duyệt rồi nhập \"tiếp tục\"; hoặc sửa đổi từ ngữ bản thảo chương này (drafts/) rồi tiếp tục; thử lại y nguyên rất có thể vẫn bị chặn"
}

// errInvalidWriteTarget đánh dấu mục tiêu sáng tác bất hợp pháp bị chặn bởi xác minh tiền trạm runWorker, dùng cho chuỗi lỗi và
// sự kiện Arbiter giữ lại ngữ nghĩa ổn định; việc có thử lại hoặc đổi phái phát vẫn do quy trình thất bại thống nhất định đoạt.
var errInvalidWriteTarget = errors.New("Mục tiêu sáng tác không hợp lệ")

func (e *engine) failureFacts(kind string, inst *flow.Instruction, workerErr error) arbiter.FailureFacts {
	f := arbiter.FailureFacts{Kind: kind, Agent: inst.Agent, Task: inst.Task, Repeats: e.repeats}
	if workerErr != nil {
		f.Error = workerErr.Error()
		f.ErrorKind = errorKind(workerErr, f.Error)
		if f.ErrorKind == "" {
			f.ErrorKind = "unknown"
		}
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "Đọc trạng thái thiết lập cơ bản thất bại: "+err.Error())
	} else {
		f.FoundationGap = missing
	}
	p, err := e.store.Progress.Load()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "Đọc tiến độ sáng tác thất bại: "+err.Error())
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.NextChapter = p.NextChapter()
		f.PendingQueue = p.PendingRewrites
	}
	return f
}

func (e *engine) recordFailureDecision(kind string, inst *flow.Instruction, facts arbiter.FailureFacts, d arbiter.FailureDecision, derr error) {
	rec := storepkg.DecisionRecord{Kind: kind, Decider: "arbiter", Input: inst.Agent + ": " + inst.Task, Reason: d.Reason}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(d); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("Ghi log kiểm toán phán quyết ra đĩa thất bại", "module", "engine", "kind", kind, "err", err)
	}
}

// applyPendingOps gửi các hành động can thiệp của trạng thái kiểm soát trong biên giới vòng lặp; xóa sạch vòng lặp——đồng bộ hóa tái tham vấn
// (reconsult) sẽ thêm vào hành động mới trong quá trình áp dụng, bắt buộc phải tiêu hóa hết trong biên giới này, nếu không ở giữa sẽ
// phái phát dư thêm một worker (can thiệp phải có hiệu lực trước các tác vụ sáng tác tiếp theo).
// Trả về việc có tồn tại hold+dispatch bắt buộc phải thực thi phái phát theo cặp trước hay không; trong trường hợp đó bên gọi sẽ hoãn kiểm tra Gate.
func (e *engine) applyPendingOps(ctx context.Context) (deferGate bool) {
	for {
		e.mu.Lock()
		ops := e.pending
		e.pending = nil
		e.mu.Unlock()
		if len(ops) == 0 {
			return deferGate
		}
		for _, op := range ops {
			pairedHoldDispatch := op.hold != nil && !op.hold.Cancel && op.dispatch != nil
			err := e.applyControlOp(ctx, op)
			if err != nil {
				// Hành động lưu không thành công: host đã dọn dẹp PendingSteer theo "vào hàng đợi thành công",
				// ở đây lưu lại toàn bộ can thiệp, phán quyết thử lại khi khôi phục/tiếp tục (hành động lũy đẳng + tái tham vấn theo sự kiện mới).
				if op.text != "" {
					if serr := e.store.RunMeta.SetPendingSteer(op.text); serr != nil {
						slog.Warn("Ghi lưu lại can thiệp thất bại", "module", "engine", "err", serr)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Thực thi hành động can thiệp thất bại, đã giữ lại; sẽ tự động thử lại khi khôi phục/tiếp tục"})
			} else if pairedHoldDispatch && e.nextDefersGate() {
				// Chỉ khi hold và phái phát theo cặp đều thành công mới cho phép bỏ qua lần Gate này.
				// Nếu hold ghi thất bại hoặc phái phát bị vứt bỏ do sự kiện quá hạn mà vẫn tiếp tục bỏ qua, đều sẽ làm cho
				// Worker không được bảo vệ tiến lên.
				deferGate = true
			}
		}
	}
}

// applyControlOp thực thi một hành động ở trạng thái điều khiển (hold ghi thẳng RunMeta, reopen gọi lõi tool, dispatch thì đối chiếu trước).
// Khi engine chưa chạy, host sẽ trực tiếp gọi trong luồng can thiệp; trả về thất bại lưu bền vững đầu tiên (bên gọi dựa vào đó để quyết định có
// giữ lại PendingSteer để phục hồi phát lại hay không).
func (e *engine) applyControlOp(ctx context.Context, op controlOp) error {
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	if op.dispatch != nil {
		// Expect phải đối chiếu trước khi lưu các hành động cặp đôi như hold xuống đĩa. Nếu không, khi hóa đơn phân công hết hạn, hold cũ
		// sẽ còn sót lại, và xung đột với hold phán quyết dựa trên sự kiện mới, kết quả là chỉ có dừng nhưng lại bỏ sót việc sửa.
		fresh, err := arbiter.CollectInterventionFacts(e.store)
		if err != nil {
			return fmt.Errorf("Làm mới sự kiện can thiệp: %w", err)
		}
		if fresh.Phase != op.facts.Phase || fresh.Flow != op.facts.Flow ||
			fresh.QueueHead() != op.facts.QueueHead() {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Phái phát phán quyết đã lỗi thời (sự kiện đẩy tiến), phán quyết lại bằng sự kiện mới nhất"})
			e.recordStale(op)
			if op.text != "" && e.reconsult != nil {
				// Đồng bộ tái tham vấn: sự can thiệp phải có hiệu lực trước khi sáng tác tiếp theo——không đồng bộ sẽ làm engine
				// lại phái một worker trước khi phán quyết mới có kết quả. Các hành động mới được làm trống bởi applyPendingOps ở biên giới này.
				e.reconsult(op.text)
			}
			return nil
		}
	}
	if op.hold != nil {
		if op.hold.Cancel {
			meta, err := e.store.RunMeta.Load()
			if err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Đọc tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err
			}
			if meta != nil && meta.AdvanceHold != nil {
				if err := e.store.RunMeta.ClearAdvanceHold(*meta.AdvanceHold); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Hủy tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
					return err
				}
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã hủy tạm dừng một lần", Level: "info"})
		} else {
			hold := domain.AdvanceHold{After: op.hold.After, TargetChapter: op.hold.TargetChapter, Reason: op.hold.Reason}
			if err := e.store.RunMeta.SetAdvanceHold(hold); err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Thiết lập tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err // dispatch liên kết không được thực thi khi hold chưa được ghi xuống đĩa
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã thiết lập tạm dừng một lần: " + op.hold.Reason, Level: "info"})
		}
	}
	if op.reopen != nil {
		args, _ := json.Marshal(map[string]any{"chapters": op.reopen.Chapters, "reason": op.reopen.Reason})
		if _, err := tools.NewReopenBookTool(e.store).Execute(ctx, args); err != nil {
			e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Mở lại làm lại thất bại: " + err.Error(), Level: "error"})
			fail(err)
		} else {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
				Summary: fmt.Sprintf("Đã mở lại làm lại toàn sách: chương %v vào hàng đợi", op.reopen.Chapters), Level: "info"})
		}
	}
	if op.dispatch != nil {
		// Expect đã được đối chiếu trước khi bất kỳ trạng thái nào ghi vào cặp. CheckpointSeq chỉ giữ để kiểm toán chứ không tham gia
		// vào đối soát: khi có sự can thiệp, phần lớn worker đang chạy, seq ắt hẳn sẽ tăng.
		e.mu.Lock()
		// Khung thời gian đã biết (ranh giới best-effort, xem điểm ③ engine-arbiter.md): kể từ đây lệnh phân công được lưu trong bộ nhớ,
		// nếu bị kill -9 trước khi worker kịp chạy (lệnh defer không thực thi), sẽ làm mất ý định của lệnh phân công lần này——
		// Việc thoát bình thường/Abort đã có run defer lưu trữ PendingSteer dự phòng.
		e.next = &flow.Instruction{Agent: op.dispatch.Agent, Task: interventionDispatchTask(op.dispatch.Task, op.text), Reason: "Phán quyết can thiệp của người dùng"}
		e.deferGateForNext = op.hold != nil && !op.hold.Cancel
		e.mu.Unlock()
	}
	return firstErr
}

// interventionDispatchTask giữ nguyên sự can thiệp ban đầu của người dùng, để tránh Arbiter tình cờ làm rộng thêm
// mục tiêu thay đổi khi thuật lại. Ở mức độ phân bổ xuống, có thể đọc và hiểu ngữ cảnh rộng hơn để đưa ra phán quyết, nhưng chỉ được coi là nguồn cấp phép để hành động.
func interventionDispatchTask(task, original string) string {
	task = strings.TrimSpace(task)
	if strings.TrimSpace(original) == "" {
		return task
	}
	return task + "\n\nCan thiệp gốc của người dùng (nguồn ủy quyền duy nhất của lần sửa đổi này; bối cảnh chỉ dùng để hiểu, không được mở rộng mục tiêu hay phạm vi):\n" + original
}

func (e *engine) recordStale(op controlOp) {
	rec := storepkg.DecisionRecord{Kind: "decision_stale", Decider: "engine", Input: op.text}
	if data, err := json.Marshal(op.facts); err == nil {
		rec.Facts = data
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("Ghi lại stale thất bại", "module", "engine", "err", err)
	}
}

// pauseWithNotify tự động tạm ngừng engine (kết thúc do rơi vào bế tắc/phán quyết từ bỏ): thông báo rời màn hình + sử dụng chức năng host thống nhất
// về cơ chế dừng (onPause → abortWithEvent:lifecycle=paused + sự kiện trong màn hình + hủy ctx).
func (e *engine) pauseWithNotify(kind, body string) {
	e.notify(kind, "warn", "ainovel: Engine tạm dừng", body)
	if e.onPause != nil {
		e.onPause(body)
		return
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: body, Level: "warn"})
	e.abort()
}

// completionSummary báo cáo kết thúc chắc chắn khi hoàn bản, không dùng cuộc gọi LLM.
func completionSummary(progress domain.Progress, book domain.BookMetadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "《%s》sáng tác hoàn tất: Tổng %d chương %d chữ", book.Title, len(progress.CompletedChapters), progress.TotalWordCount)
	return b.String()
}
