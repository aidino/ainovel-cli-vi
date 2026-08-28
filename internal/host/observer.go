package host

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// errorKind classifies a runtime error into a stable, short label for log
// filtering and alert routing. Returns "" when no special tag applies.
//
// err is the live error chain (may be nil after JSON serialization); msg is
// the rendered string fallback used when the chain has been flattened
// (e.g. inside sub-agent JSON results).
func errorKind(err error, msg string) string {
	if kind := agentcore.ErrorKind(err); kind != "" && kind != "unknown" {
		return kind
	}
	if msg == "" {
		return ""
	}
	if kind := agentcore.ErrorKind(errors.New(msg)); kind != "unknown" {
		return kind
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "tool argument validation failed"):
		return "tool_validation"
	case strings.Contains(lower, "too many concurrent requests"):
		return "overloaded"
	// providerError sẽ đính kèm kiểu có cấu trúc của litellm vào cuối văn bản.
	// HTTP/2 INTERNAL_ERROR bản thân nó không có từ khóa phân loại, giữ lại đánh dấu network rõ ràng này là được.
	case strings.Contains(lower, "[network,"):
		return "network"
	}
	return ""
}

// Bộ đếm ID sự kiện tăng dần đơn điệu; kết hợp với timestamp để tạo ID ổn định.
var eventIDCounter uint64

func nextEventID() string {
	return fmt.Sprintf("e%d", atomic.AddUint64(&eventIDCounter, 1))
}

// activeCall ghi lại ID, thời gian bắt đầu và summary của một lần gọi đang diễn ra (TOOL / DISPATCH).
// summary được điền lại vào finish Event khi hoàn thành, đảm bảo replay (runtime queue) có thể khôi phục nội dung dòng.
type activeCall struct {
	id      string
	start   time.Time
	summary string
	depth   int
}

// observer chiếu phân phối của Engine và tiến độ của Worker ra kênh đầu ra của Host.
// Nó là một observer thuần túy, không tham gia vào bất kỳ quyết định điều khiển nào.
type observer struct {
	emitEv  func(Event)
	emitD   func(string)
	emitC   func()
	store   *storepkg.Store // dùng cho lưu trữ runtime queue (ReplayQueue tiêu thụ)
	agents  map[string]*agentState
	agentMu sync.Mutex

	// aborting do Host đặt tại điểm vào Abort()/Close(), xóa tại Start/Resume/Continue.
	// Khi được đặt, mọi sự kiện lỗi phát sinh từ context-cancel sẽ bị chặn (vừa đúng kỳ vọng
	// người dùng, vừa tránh lặp với sự kiện "người dùng tạm dừng thủ công"). Lỗi thực sự (không phải cancel) vẫn báo cáo bình thường.
	aborting atomic.Bool

	streamThinking      bool
	lastThinkingByAgent map[string]string          // agent → văn bản thinking tích lũy gần nhất (dùng để trích xuất delta tăng dần)
	dispatchStarts      map[string]*activeCall     // dispatched agent → lệnh gọi DISPATCH đang diễn ra
	toolStarts          map[string]*activeCall     // agent → lệnh gọi TOOL đang diễn ra
	streamExtractors    map[string]*agentExtractor // agent → bộ trích xuất nội dung tham số JSON của lệnh gọi công cụ hiện tại
	streamArgPrefixes   map[string]string          // agent/tool → tiền tố luồng tham số, dùng để nhận diện sớm nhãn nhẹ
	streamArgLabels     map[string]string          // agent/tool → tên hiển thị đã nhận diện sớm từ luồng tham số
	retryEvents         map[string]string          // retry scope → event ID, cập nhật tại chỗ cùng một dòng (2/7)
	streamHasContent    bool                       // streamRound hiện tại đã xuất nội dung chưa (để xét xem có cần ngắt đoạn)
	streamLastByte      byte                       // byte cuối của lần xuất luồng gần nhất (dùng để bù chính xác ngắt dòng)
}

// agentExtractor ghi lại tên công cụ và instance bộ trích xuất mà một agent đang trích xuất.
// Tên công cụ dùng để phát hiện "lệnh gọi công cụ mới đã bắt đầu", tránh bị ô nhiễm bởi tàn dư của vòng trước.
type agentExtractor struct {
	tool       string
	ext        *jsonFieldExtractor
	emittedAny bool // extractor này đã từng sinh ra nội dung chưa; dùng để bù ngắt đoạn trước lần xuất đầu tiên
}

type agentState struct {
	name    string
	state   string
	tool    string
	summary string
	turn    int
	context AgentContextSnapshot
	updated time.Time
}

func newObserver(s *storepkg.Store, emitEv func(Event), emitD func(string), emitC func()) *observer {
	return &observer{
		emitEv:              emitEv,
		emitD:               emitD,
		emitC:               emitC,
		store:               s,
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
		streamArgPrefixes:   make(map[string]string),
		streamArgLabels:     make(map[string]string),
		retryEvents:         make(map[string]string),
	}
}

// ── Cổng vào trực tiếp từ Engine ──
//
// Engine chạy trực tiếp Worker, nguồn sự kiện chia làm hai đường:
//  1. dispatchStart/dispatchFinish —— Engine gọi trực tiếp tại ranh giới phân phối (dòng DISPATCH)
//  2. workerProgress —— Chuyển tiếp tiến độ của Worker (ctx ToolProgress),
//     được handleToolUpdate xử lý thống nhất TOOL/chính văn stream/thinking/retry/context
//     (dòng TOOL/chính văn stream/thinking/retry/context).

// dispatchStart ghi lại một lần phân phối Worker bắt đầu và phát dòng DISPATCH.
func (o *observer) dispatchStart(agent, task, reason string) {
	summary := dispatchSummary(agent, task)
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = ""
		a.summary = fmt.Sprintf("engine → %s", summary)
	})
	id := nextEventID()
	o.dispatchStarts[agent] = &activeCall{id: id, start: time.Now(), summary: summary}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "DISPATCH",
		Agent:    agent,
		Summary:  summary,
		Detail:   dispatchDetail(task, reason),
		Level:    "info",
	})
}

// dispatchFinish ghi dòng DISPATCH thành trạng thái hoàn thành và reset trạng thái Worker;
// dọn dẹp các dòng TOOL mồ côi dưới tên Worker này (nhánh abort/lỗi có thể thiếu ProgressToolEnd).
func (o *observer) dispatchFinish(agent string, runErr error) {
	o.updateAgent(agent, func(a *agentState) {
		a.state = "idle"
		a.tool = ""
	})
	delete(o.lastThinkingByAgent, agent)
	if call, ok := o.toolStarts[agent]; ok {
		delete(o.toolStarts, agent)
		delete(o.streamExtractors, agent)
		o.emitCallFinish(call, "TOOL", agent, runErr)
	}
	if call, ok := o.dispatchStarts[agent]; ok {
		delete(o.dispatchStarts, agent)
		o.emitCallFinish(call, "DISPATCH", agent, runErr)
	}
	o.streamClear()
}

// workerProgress điều chỉnh chuyển tiếp tiến độ Worker thành xử lý ToolExecUpdate có sẵn.
func (o *observer) workerProgress(p agentcore.ProgressPayload) {
	payload := p
	o.handleToolUpdate(agentcore.Event{Type: agentcore.EventToolExecUpdate, Progress: &payload})
}

func (o *observer) finalize() {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	for _, a := range o.agents {
		a.state = "idle"
		a.tool = ""
	}
}

// setAborting được Host gọi tại các điểm chuyển đổi vòng đời như Abort/Close/Start, kiểm soát
// xem các sự kiện phái sinh từ "context canceled" có cần bị chặn không (tránh lặp với "người dùng tạm dừng thủ công").
func (o *observer) setAborting(v bool) { o.aborting.Store(v) }

func (o *observer) retryEventID(scope string, attempt int) string {
	if strings.TrimSpace(scope) == "" {
		scope = "engine"
	}
	if o.retryEvents == nil {
		o.retryEvents = make(map[string]string)
	}
	if attempt <= 1 || o.retryEvents[scope] == "" {
		o.retryEvents[scope] = nextEventID()
	}
	return o.retryEvents[scope]
}

// emitAndLog dùng cho trạng thái "bắt đầu" của sự kiện dạng lệnh gọi: gửi cho TUI nhưng không ghi vào runtime queue,
// tránh lúc replay bị lặp "bắt đầu một dòng, hoàn thành lại thêm một dòng". slog do host.emitEvent ghi thống nhất.
func (o *observer) emitAndLog(ev Event) {
	o.emitEv(ev)
}

// persistEvent ghi sự kiện vào runtime queue (slog do host.emitEvent ghi thống nhất).
func (o *observer) persistEvent(ev Event) {
	if o.store == nil || o.store.Runtime == nil {
		return
	}
	priority := domain.RuntimePriorityBackground
	switch {
	case ev.Level == "error":
		priority = domain.RuntimePriorityControl
	case ev.Category == "SYSTEM" || ev.Category == "ERROR":
		priority = domain.RuntimePriorityControl
	}
	if _, err := o.store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Time:     ev.Time,
		Priority: priority,
		Category: ev.Category,
		Summary:  ev.Summary,
		Payload:  ev,
	}); err != nil {
		slog.Warn("lưu trữ sự kiện runtime thất bại", "module", "observer", "category", ev.Category, "err", err)
	}
}

func (o *observer) updateAgent(name string, fn func(*agentState)) {
	if name == "" {
		return
	}
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	a, ok := o.agents[name]
	if !ok {
		a = &agentState{name: name, state: "idle"}
		o.agents[name] = a
	}
	fn(a)
	a.updated = time.Now()
}

func (o *observer) agentSnapshots() []AgentSnapshot {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	snaps := make([]AgentSnapshot, 0, len(o.agents))
	for _, a := range o.agents {
		snaps = append(snaps, AgentSnapshot{
			Name:      a.name,
			State:     a.state,
			Summary:   a.summary,
			Tool:      a.tool,
			Turn:      a.turn,
			Context:   a.context,
			UpdatedAt: a.updated,
		})
	}
	return snaps
}
