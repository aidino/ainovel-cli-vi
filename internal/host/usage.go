package host

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/models"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// recentSampleCap là kích thước cửa sổ trượt: chỉ giữ lại (cacheRead, input) của N lần gọi gần nhất của mỗi role
// làm mẫu, dùng để so sánh tỷ lệ trúng "Tích lũy vs N lần gần nhất" ở cột trái, nhận diện "lôi kéo giai đoạn đầu" vs "tỷ lệ trúng thấp ổn định".
const recentSampleCap = 10

// Ngưỡng kép để xác định sự đứt gãy chuỗi cache (dựa trên kinh nghiệm thực nghiệm của Claude Code): lượng truy cập giảm hơn
// 5% (tương đối) so với lần trước và mức giảm ≥ 2000 tokens (tuyệt đối) mới được coi là đứt gãy——ngưỡng tương đối đơn lẻ sẽ bị nhiễu tiền tố nhỏ che lấp
// Ngưỡng kép để xác định sự đứt gãy chuỗi cache (dựa trên kinh nghiệm thực nghiệm của Claude Code): 
// lượng truy cập giảm hơn 5% so với lần trước (tương đối) và mức giảm ≥ 2000 tokens (tuyệt đối) 
// mới được coi là đứt gãy—ngưỡng tương đối đơn lẻ sẽ bị nhiễu tiền tố nhỏ che lấp, 
// ngưỡng tuyệt đối đơn lẻ sẽ bỏ lỡ sự suy giảm đáng kể của tiền tố lớn.
const (
	cacheBreakKeepRatio     = 0.95
	cacheBreakMinDropTokens = 2000
)

//   - Khi sổ đăng ký không có model này, quay lui về msg.Usage.Cost.Total (do provider tự mang theo, có thể là 0)
//   - Chuyển đổi model nóng (/model) các tin nhắn tiếp theo sẽ tự động tính giá theo model mới, tin nhắn cũ giữ nguyên chi phí cũ
//
// Đồng thời duy trì theo chiều per-role (writer/editor/architect):
//   - Dữ liệu trúng cache tích lũy → hiệu quả tối ưu hóa tổng thể
//   - Cửa sổ trượt N lần gần nhất → phân biệt giữa lôi kéo giai đoạn đầu vs tỷ lệ trúng thấp ổn định
//   - Khi registry không có model này, quay lại dùng msg.Usage.Cost.Total (do provider tự cung cấp, có thể là 0)
//   - Khi chuyển đổi model nóng (/model), các tin nhắn tiếp theo sẽ được tính phí theo model mới, tin nhắn cũ giữ nguyên chi phí cũ
//
// Đồng thời duy trì theo chiều per-role (writer/editor/architect):
//   - Dữ liệu tích lũy → hiệu quả tối ưu hóa tổng thể
//   - Cửa sổ trượt N lần gần nhất → phân biệt giữa "lôi kéo giai đoạn đầu" vs "tỷ lệ trúng thấp ổn định"
//   - Nhãn CacheCapable → phân biệt giữa "chưa kích hoạt" và "thực sự trúng 0%"
//
// An toàn luồng.
type UsageTracker struct {
	store    *storepkg.Store
	mu       sync.Mutex
	overall  agentTotals
	perAgent map[string]*agentTotals // key là tên role sau khi đã chuẩn hóa
	perModel map[string]*agentTotals // key là provider/model; khi provider không xác định thì thoái hóa thành model
	modelSet *bootstrap.ModelSet
	// Nếu không, mỗi lần khởi động sẽ biến các vết nứt cũ thành báo động giả. Không lưu trữ lâu dài.
	cacheTrack map[string]*cacheTrackState

	// missingAssistantUsage tích lũy số lần "nhận tin nhắn assistant nhưng Usage là nil".
	// Thực tế xảy ra khi backend tương thích OpenAI tự dựng không gửi chunk usage cuối cùng
	// DeepSeek chính thức và proxy (SiliconFlow, v.v.) giao thức stream_options.include_usage của OpenAI — partial.Usage luôn là nil,
	// mọi trường tích lũy đều dừng ở 0. Bộ đếm giúp UI có thể thông báo trực tiếp cho người dùng
	// "là do thượng nguồn không trả về usage chứ không phải do code bị lỗi", thay vì loay hoay sửa code panel cache.
	missingAssistantUsage int
	loggedMissingUsage    bool // Chỉ cảnh báo một lần trong suốt phiên, tránh spam log tui.log

	// saveCh được kích hoạt không chặn bởi Record sau khi đã tích lũy; autoSaveLoop lắng nghe và ghi đĩa theo debounce.
	// buffered=1: Nhiều Record liên tiếp được gộp thành một tín hiệu ghi; nếu đầy thì bỏ qua, đợi tick tiếp theo sẽ ghi chung.
	saveCh       chan struct{}
	autoSaveMu   sync.Mutex
	autoSaveDone chan struct{}

	// onCost được gọi bên ngoài khóa sau mỗi lần ghi sổ, mang theo chi phí tích lũy mới nhất (để BudgetSentinel kiểm tra vượt ngân sách).
	// Bắt buộc phải được thiết lập thông qua SetOnCost trước khi Record đồng thời bắt đầu, sau đó chỉ đọc.
	onCost func(total float64)

	// onMissingUsage được gọi một lần khi lần đầu tiên phát hiện "tin nhắn assistant không có Usage" (cùng lúc với slog warn
	// ). Khi kích hoạt ngân sách, điều này có nghĩa là vùng mù tính phí——chi phí luôn là 0, ngân sách không bao giờ kích hoạt, bắt buộc phải cảnh báo.
	onMissingUsage func()
}

// usageSample là mẫu trúng cache của một lần OnMessage, chỉ ghi lại tử số và mẫu số của tỷ lệ trúng.
type usageSample struct {
	CacheRead int
	Input     int
}

// cacheTrackState là đường cơ sở chuỗi cache của một phiên hiện tại của role. task (văn bản nhiệm vụ spawn) là
// danh tính phiên: đổi task = spawn mới = huyết thống cache mới (prompt_cache_key có #seq), yêu cầu đầu tiên
// tỷ lệ trúng thấp là bình thường, trực tiếp đổi đường cơ sở mà không so sánh——nếu không "phiên trước rất ngắn, tiền tố yêu cầu đầu tiên của phiên mới ngược lại dài hơn"
// sẽ báo sai là đứt gãy. Ngữ nghĩa Input (bao gồm CacheRead, xem chú thích computeCost) hoàn toàn bằng "độ dài tiền tố
// mà server xử lý", từ đó có thể phân biệt 3 hướng: tiền tố thu ngắn = nén trong phiên (hợp lệ, đặt lại đường cơ sở);
// tiền tố tăng và tỷ lệ trúng tăng theo = liên kết khỏe mạnh; tiền tố tăng nhưng tỷ lệ trúng giảm đột ngột = đứt gãy.
type cacheTrackState struct {
	task          string
	lastPrefix    int
	lastCacheRead int
	lastAt        time.Time
}

// agentTotals là bộ đếm tích lũy của một agent.
//   - Saved là phần chênh lệch được tính ngược lại dựa trên dữ liệu trúng hiện tại "nếu tính phí theo giá không có cache"
//   - CacheCapable chỉ được đặt thành true sau khi role này trải qua ít nhất một lần gọi "model được biết là có hỗ trợ cache"
//   - samples là ring buffer độ dài cố định, recentSampleCap lần đầu được thêm trực tiếp, sau đó xoay vòng theo sampleIdx
type agentTotals struct {
	Input        int
	Output       int
	CacheRead    int
	CacheWrite   int
	Cost         float64
	Saved        float64
	CacheCapable bool
	CacheBreaks  int // live số lần đứt gãy chuỗi cache phát hiện được (không tính replay)
	samples      []usageSample
	sampleIdx    int
}

func NewUsageTracker(set *bootstrap.ModelSet, store *storepkg.Store) *UsageTracker {
	return &UsageTracker{
		modelSet:   set,
		store:      store,
		perAgent:   make(map[string]*agentTotals, 4),
		perModel:   make(map[string]*agentTotals, 4),
		cacheTrack: make(map[string]*cacheTrackState, 4),
		saveCh:     make(chan struct{}, 1),
	}
}

// Record phân phối một tin nhắn agent vào hai luồng tích lũy / chẩn đoán.
//
// Việc tích lũy chỉ xem Usage có tồn tại hay không——"tin nhắn nào mang Usage" là chi tiết lắp ráp của agentcore/litellm adapter
// (giao thức upstream đặt usage ở top-level của phản hồi), tương lai nếu quy tắc lắp ráp thay đổi cũng không cần sửa ở đây.
// Việc chẩn đoán yêu cầu Role=Assistant và Content không rỗng, để tránh AbortMsg / khôi phục ngoại lệ / tool /
// tin nhắn user làm ô nhiễm bộ đếm missingAssistantUsage.
func (t *UsageTracker) Record(agentName, task string, msg agentcore.AgentMessage) {
	if t == nil {
		return
	}
	m, ok := msg.(agentcore.Message)
	if !ok {
		return
	}
	if m.Usage == nil {
		if m.Role == agentcore.RoleAssistant && len(m.Content) > 0 {
			t.flagMissingUsage(agentName)
		}
		return
	}
	role := agentRoleName(agentName)
	t.noteCacheBreak(role, task, *m.Usage)
	provider, modelName := usageActualModel(m.Usage)
	t.accumulate(role, provider, modelName, *m.Usage)
}

// noteCacheBreak là phát hiện đứt gãy chuỗi cache (chỉ quan sát, không sửa chữa, chỉ gọi trong luồng Record live).
//
// Phán đoán: trong cùng một phiên (role+task) tiền tố (Input, bao gồm CacheRead) không bị thu ngắn, nhưng số lượng trúng so với lần trước
// giảm >5% và mức giảm ≥2000 tokens. task thay đổi = spawn mới = huyết thống cache mới, trực tiếp đổi đường cơ sở mà không
// so sánh; tiền tố thu ngắn cho thấy đây là nén ngữ cảnh, thuộc loại giảm hợp lệ, chỉ đặt lại đường cơ sở không cảnh báo. Việc quy trách nhiệm được gợi ý theo mức độ ưu tiên:
// gợi ý: khoảng cách vượt quá TTL → nghi ngờ hết hạn; khoảng cách rất ngắn và byte client đáng lẽ phải ổn định → nghi ngờ bị đẩy ra khỏi server/
// trôi dạt định tuyến (trạm trung chuyển thăm dò ngược dòng là nguyên nhân phổ biến).
func (t *UsageTracker) noteCacheBreak(role, task string, u agentcore.Usage) {
	now := time.Now()
	prefix := u.Input // các provider của litellm đảm bảo Input bao gồm CacheRead

	t.mu.Lock()
	st := t.cacheTrack[role]
	if st == nil || st.task != task {
		t.cacheTrack[role] = &cacheTrackState{task: task, lastPrefix: prefix, lastCacheRead: u.CacheRead, lastAt: now}
		t.mu.Unlock()
		return
	}
	prevPrefix, prevRead, prevAt := st.lastPrefix, st.lastCacheRead, st.lastAt
	st.lastPrefix, st.lastCacheRead, st.lastAt = prefix, u.CacheRead, now

	broke := prevPrefix > 0 && prefix >= prevPrefix &&
		float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
		prevRead-u.CacheRead >= cacheBreakMinDropTokens
	if broke {
		t.overall.CacheBreaks++
		per := t.perAgent[role]
		if per == nil {
			per = &agentTotals{}
			t.perAgent[role] = per
		}
		per.CacheBreaks++
	}
	t.mu.Unlock()

	if !broke {
		return
	}
	gap := now.Sub(prevAt).Round(time.Second)
	hint := "Nghi vấn bị đẩy ra phía máy chủ/trôi dạt định tuyến (trạm trung chuyển thăm dò ngược dòng là nguyên nhân phổ biến)"
	if gap > time.Hour {
		hint = "Nghi vấn hết hạn 1h TTL"
	} else if gap > 5*time.Minute {
		hint = "Nghi vấn hết hạn 5m TTL"
	}
	slog.Warn("Đứt gãy chuỗi cache: Tiền tố không thu gọn nhưng lượt truy cập giảm đột ngột",
		"module", "usage", "role", role,
		"cache_read", fmt.Sprintf("%d→%d", prevRead, u.CacheRead),
		"prefix", fmt.Sprintf("%d→%d", prevPrefix, prefix),
		"gap", gap.String(), "hint", hint)
	t.notifyDirty()
}

func usageActualModel(u *agentcore.Usage) (provider, modelName string) {
	if u == nil {
		return "", ""
	}
	return strings.TrimSpace(u.Provider), strings.TrimSpace(u.Model)
}

// flagMissingUsage tích lũy một lần sự kiện "có vẻ là phản hồi LLM thật nhưng không lấy được usage", toàn bộ phiên chỉ in
// log warn một lần để tránh làm ngập tui.log.
func (t *UsageTracker) flagMissingUsage(agentName string) {
	t.mu.Lock()
	t.missingAssistantUsage++
	shouldLog := !t.loggedMissingUsage
	t.loggedMissingUsage = true
	t.mu.Unlock()
	if shouldLog {
		slog.Warn("Phản hồi LLM không mang theo dữ liệu usage, bảng cache/chi phí sẽ không có tích lũy——thông thường do upstream streaming không gửi final usage chunk theo giao thức include_usage của OpenAI",
			"module", "usage", "agent", agentName)
		if t.onMissingUsage != nil {
			t.onMissingUsage()
		}
	}
	t.notifyDirty()
}

// SetOnMissingUsage đăng ký một callback sử dụng một lần khi "lần đầu phát hiện thiếu usage".
// Bắt buộc phải gọi một lần trong thời kỳ cấu tạo Host, trước khi Record đồng thời bắt đầu.
func (t *UsageTracker) SetOnMissingUsage(cb func()) {
	if t == nil {
		return
	}
	t.onMissingUsage = cb
}

// notifyDirty kích hoạt một tín hiệu ghi đĩa không chặn, được autoSaveLoop thực sự ghi theo debounce.
// Kênh tín hiệu buffered=1: nhiều Record liên tiếp được gộp thành một yêu cầu lưu là được.
func (t *UsageTracker) notifyDirty() {
	if t == nil || t.saveCh == nil {
		return
	}
	select {
	case t.saveCh <- struct{}{}:
	default:
	}
}

// accumulate tạo một bản ghi lượng dùng và đẩy vào hàng đợi. Nếu bản ghi toàn số 0 sẽ bị vứt bỏ.
// Nó ánh xạ provider LLM tới đơn giá chi phí trong cấu hình (nếu tồn tại, nếu không dùng bảng giá default).
// provider/model trống nghĩa là "dùng model hiện tại trong ModelSet theo role" (đường dẫn thời gian thực); không trống nghĩa là
// "ép buộc tính phí theo model chỉ định" (đường dẫn replay dùng _meta trong session jsonl).
// resolveCost thực thi ngoài khóa (nó chỉ đọc modelSet/Registry), trong khóa chỉ thực hiện phép cộng.
func (t *UsageTracker) accumulate(role, provider, modelName string, u agentcore.Usage) {
	provider, modelName = t.effectiveModel(role, provider, modelName)
	cost, saved, capable := t.resolveCost(modelName, u)

	t.mu.Lock()
	addUsage(&t.overall, u, cost, saved, capable)

	per := t.perAgent[role]
	if per == nil {
		per = &agentTotals{}
		t.perAgent[role] = per
	}
	addUsage(per, u, cost, saved, capable)

	if key := modelUsageKey(provider, modelName); key != "" {
		perModel := t.perModel[key]
		if perModel == nil {
			perModel = &agentTotals{}
			t.perModel[key] = perModel
		}
		addUsage(perModel, u, cost, saved, capable)
	}
	total := t.overall.Cost
	t.mu.Unlock()

	t.notifyDirty()
	if t.onCost != nil {
		t.onCost(total)
	}
}

// SetOnCost đăng ký callback ghi sổ (mang theo chi phí tích lũy mới nhất, gọi ngoài khóa).
// Bắt buộc phải gọi một lần trong thời kỳ cấu tạo Host, trước khi Record đồng thời bắt đầu.
func (t *UsageTracker) SetOnCost(cb func(total float64)) {
	if t == nil {
		return
	}
	t.onCost = cb
}

func (t *UsageTracker) effectiveModel(role, provider, modelName string) (string, string) {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		if t != nil && t.modelSet != nil {
			p, m, _ := t.modelSet.CurrentSelection(role)
			return p, m
		}
		return "", ""
	}
	if provider == "" && t != nil && t.modelSet != nil {
		p, m, _ := t.modelSet.CurrentSelection(role)
		if m == modelName {
			provider = p
		}
	}
	return provider, modelName
}

func modelUsageKey(provider, modelName string) string {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	switch {
	case modelName == "":
		return ""
	case provider == "":
		return modelName
	default:
		return provider + "/" + modelName
	}
}

// addUsage cộng dồn token và chi phí của một lần gọi vào một bảng tổng.
// Bắt buộc gọi khi đang nắm giữ UsageTracker.mu.
//
// CacheCapable được xác định ưu tiên dựa trên "thực tế": chỉ cần thấy CacheRead hoặc CacheWrite > 0, chứng minh
// phía upstream đã thực sự thực hiện prompt caching. CacheReadCostPer1M trong bảng đăng ký chỉ là fallback,
// vì các model backend tự xây dựng (mimo-v2.5-pro / proxy trong nước, v.v.) thường không nằm trong danh mục
// định giá của BerriAI/litellm, nhưng Usage thực tế hoàn toàn có dữ liệu cache, UI không nên chẩn đoán nhầm thành "chưa kích hoạt".
func addUsage(t *agentTotals, u agentcore.Usage, cost, saved float64, capable bool) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Cost += cost
	t.Saved += saved
	if capable || u.CacheRead > 0 || u.CacheWrite > 0 {
		t.CacheCapable = true
	}
	pushSample(t, u.CacheRead, u.Input)
}

// pushSample đẩy một mẫu vào ring buffer. recentSampleCap lần đầu là append thuần, sau đó xoay vòng ghi đè.
func pushSample(t *agentTotals, cacheRead, input int) {
	s := usageSample{CacheRead: cacheRead, Input: input}
	if len(t.samples) < recentSampleCap {
		t.samples = append(t.samples, s)
		return
	}
	t.samples[t.sampleIdx] = s
	t.sampleIdx = (t.sampleIdx + 1) % recentSampleCap
}

// recentSums trả về tổng số cacheRead và input trong cửa sổ trượt, làm tử số và mẫu số cho "tỷ lệ trúng N lần gần nhất".
// Sử dụng sum/sum thay vì "trung bình của các tỷ lệ đơn lẻ" để tránh mẫu nhỏ (input=vài trăm token) khuếch đại nhiễu.
func recentSums(t *agentTotals) (cacheRead, input int) {
	for _, s := range t.samples {
		cacheRead += s.CacheRead
		input += s.Input
	}
	return cacheRead, input
}

// Totals trả về bản chụp nhanh của tổng tích lũy.
func (t *UsageTracker) Totals() (cost float64, input, output, cacheRead, cacheWrite int) {
	if t == nil {
		return 0, 0, 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Cost, t.overall.Input, t.overall.Output, t.overall.CacheRead, t.overall.CacheWrite
}

// SavedUSD trả về số USD tích lũy tiết kiệm được nhờ trúng cache.
func (t *UsageTracker) SavedUSD() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Saved
}

// OverallRecent trả về tổng cacheRead, tổng input và số mẫu trong cửa sổ trượt (≤ recentSampleCap lần).
func (t *UsageTracker) OverallRecent() (cacheRead, input, samples int) {
	if t == nil {
		return 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, in := recentSums(&t.overall)
	return r, in, len(t.overall.samples)
}

// OverallCacheBreaks trả về tổng số lần đứt gãy chuỗi cache phát hiện qua live.
func (t *UsageTracker) OverallCacheBreaks() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheBreaks
}

// OverallCacheCapable tổng thể đã trải qua ít nhất một model được biết là có hỗ trợ cache hay chưa.
func (t *UsageTracker) OverallCacheCapable() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheCapable
}

// MissingAssistantUsage trả về số lần tích lũy "nhận được tin nhắn assistant nhưng Usage là nil".
// Lớn hơn 0 thường có nghĩa là upstream streaming không phát final usage chunk của OpenAI,
// UI dựa vào đó để hiển thị thông báo thay vì nhầm tưởng rằng bản thân mô-đun cache bị hỏng.
func (t *UsageTracker) MissingAssistantUsage() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.missingAssistantUsage
}

// ── Bền vững hóa ──

// Snapshot sao chép trạng thái tích lũy hiện tại thành domain.UsageState có thể tuần tự hóa.
// Mẫu trong cửa sổ trượt không được đưa vào snapshot——đó là cửa sổ chẩn đoán ngắn hạn, không có nhiều ý nghĩa khi truyền qua tiến trình.
func (t *UsageTracker) Snapshot() domain.UsageState {
	if t == nil {
		return domain.UsageState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := domain.UsageState{
		Schema:       domain.UsageSchemaVersion,
		UpdatedAt:    time.Now(),
		Overall:      totalsSnapshot(&t.overall),
		PerAgent:     make(map[string]domain.AgentUsageTotals, len(t.perAgent)),
		PerModel:     make(map[string]domain.AgentUsageTotals, len(t.perModel)),
		MissingUsage: t.missingAssistantUsage,
	}
	for role, v := range t.perAgent {
		state.PerAgent[role] = totalsSnapshot(v)
	}
	for model, v := range t.perModel {
		state.PerModel[model] = totalsSnapshot(v)
	}
	return state
}

// LoadFromStore Đọc snapshot bền vững từ store.Usage và nạp lại vào bộ nhớ. Trả về true nếu
// tải thành công một trạng thái không rỗng (schema khớp); false nếu không có tệp hoặc không khả dụng, phía gọi
// nên tiếp tục thực hiện session replay để nạp lại một lần.
func (t *UsageTracker) LoadFromStore() (bool, error) {
	if t == nil || t.store == nil {
		return false, nil
	}
	state, err := t.store.Usage.Load()
	if err != nil {
		return false, fmt.Errorf("mở tệp log thất bại: %w", err)
	}
	if state == nil {
		return false, nil
	}
	t.applyState(*state)
	return true, nil
}

// SaveNow lập tức ghi snapshot hiện tại ra đĩa. Luồng autoSaveLoop / Close đều ghi qua đây.
func (t *UsageTracker) SaveNow() error {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Usage.Save(t.Snapshot())
}

// StartAutoSave khởi tạo một goroutine, lắng nghe saveCh + debounce ghi ra đĩa. Trước khi ctx done sẽ
// flush trạng thái chưa được lưu cuối cùng ra. Close kích hoạt flush + thoát qua cancel ctx.
func (t *UsageTracker) StartAutoSave(ctx context.Context) {
	if t == nil || t.store == nil {
		return
	}
	done := make(chan struct{})
	t.autoSaveMu.Lock()
	t.autoSaveDone = done
	t.autoSaveMu.Unlock()
	go func() {
		defer close(done)
		t.autoSaveLoop(ctx)
	}()
}

// WaitAutoSave đợi quá trình flush cuối cùng sau khi hủy hoàn tất. Host.Close gọi cancel trước,
// sau đó đợi ở đây, tránh việc autoSaveLoop và SaveNow trước khi thoát ghi đồng thời cùng một bản ghi.
func (t *UsageTracker) WaitAutoSave() {
	if t == nil {
		return
	}
	t.autoSaveMu.Lock()
	done := t.autoSaveDone
	t.autoSaveMu.Unlock()
	if done != nil {
		<-done
	}
}

// autoSaveLoop tiết lưu tín hiệu dirty tần suất cao thành 500ms một lần ghi ra đĩa.
//
// Thuyết minh thiết kế: 500ms là giá trị kinh nghiệm——mỗi chương 1-2 turn LLM, ghi ra đĩa 1-2 lần là hoàn toàn chấp nhận được;
// ngay cả khi người dùng nhấn ctrl+C thủ công để thoát không kịp kích hoạt timer, luồng hủy ctx cũng sẽ flush lần cuối cùng.
// Sự cố treo thực sự (OS kill -9) sẽ làm mất phần tích lũy trong 0.5s gần nhất——upstream session jsonl vẫn là
// sự kiện hoàn chỉnh, lần khởi động sau sẽ replay từ sessions/ để bù đắp chênh lệch.
func (t *UsageTracker) autoSaveLoop(ctx context.Context) {
	const debounce = 500 * time.Millisecond
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	var pending bool
	flush := func() {
		if err := t.SaveNow(); err != nil {
			slog.Error("ghi lại lượng sử dụng thất bại", "module", "usage", "err", err)
		}
		pending = false
	}
	for {
		select {
		case <-ctx.Done():
			slog.Warn("ghi hàng đợi lượng sử dụng quá hạn, vứt bỏ một bản ghi")
			// Không trả về lỗi: ghi sổ lượng sử dụng thuộc luồng phụ, không nên chặn nghiệp vụ chính
			if pending {
				flush()
			}
			return
		case <-t.saveCh:
			if pending {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			timer.Reset(debounce)
			pending = true
		case <-timer.C:
			flush()
		}
	}
}

// applyState ghi lại snapshot đã lưu bền vững vào bộ nhớ. Chỉ gọi lúc khởi động (sau LoadFromStore / replay),
// lúc này autoSaveLoop chưa được khởi động / Record cũng không kích hoạt đồng thời, có thể không giữ khóa; nhưng vẫn giữ mu để phòng
// trường hợp kiểm thử hoặc thứ tự gọi trong tương lai thay đổi sinh ra đồng thời.
func (t *UsageTracker) applyState(state domain.UsageState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.overall = totalsFromState(state.Overall)
	if state.PerAgent == nil {
		t.perAgent = make(map[string]*agentTotals, 4)
	} else {
		t.perAgent = make(map[string]*agentTotals, len(state.PerAgent))
		for role, v := range state.PerAgent {
			tot := totalsFromState(v)
			t.perAgent[role] = &tot
		}
	}
	if state.PerModel == nil {
		t.perModel = make(map[string]*agentTotals, 4)
	} else {
		t.perModel = make(map[string]*agentTotals, len(state.PerModel))
		for model, v := range state.PerModel {
			tot := totalsFromState(v)
			t.perModel[model] = &tot
		}
	}
	t.missingAssistantUsage = state.MissingUsage
}

// totalsSnapshot sao chép agentTotals trong bộ nhớ thành domain.AgentUsageTotals có thể bền vững.
// Vùng đệm vòng mẫu cố tình không được mang ra ngoài — xem chú thích của UsageState.
func totalsSnapshot(t *agentTotals) domain.AgentUsageTotals {
	if t == nil {
		return domain.AgentUsageTotals{}
	}
	return domain.AgentUsageTotals{
		Input:        t.Input,
		Output:       t.Output,
		CacheRead:    t.CacheRead,
		CacheWrite:   t.CacheWrite,
		Cost:         t.Cost,
		Saved:        t.Saved,
		CacheCapable: t.CacheCapable,
		CacheBreaks:  t.CacheBreaks,
	}
}

// totalsFromState khôi phục dạng bền vững thành agentTotals trên bộ nhớ. samples để trống, sau khi khởi động lại
// sẽ tích lũy lại từ 0, sau vài vòng Record là có thể phục hồi ngữ nghĩa "tỷ lệ trúng N lần gần nhất".
func totalsFromState(s domain.AgentUsageTotals) agentTotals {
	return agentTotals{
		Input:        s.Input,
		Output:       s.Output,
		CacheRead:    s.CacheRead,
		CacheWrite:   s.CacheWrite,
		Cost:         s.Cost,
		Saved:        s.Saved,
		CacheCapable: s.CacheCapable,
		CacheBreaks:  s.CacheBreaks,
	}
}

// AgentUsage là bản chụp nhanh lượng dùng tích lũy của một agent (lộ ra cho UI).
type AgentUsage struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// PerAgent trả về lượng dùng tích lũy của từng role. Kết quả sắp xếp giảm dần theo số lượng CacheRead, bỏ qua những role chưa tiêu thụ token.
func (t *UsageTracker) PerAgent() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perAgent))
	for role, v := range t.perAgent {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		recentRead, recentInput := recentSums(v)
		out = append(out, AgentUsage{
			Role:            role,
			Input:           v.Input,
			Output:          v.Output,
			CacheRead:       v.CacheRead,
			CacheWrite:      v.CacheWrite,
			Cost:            v.Cost,
			Saved:           v.Saved,
			CacheCapable:    v.CacheCapable,
			RecentCacheRead: recentRead,
			RecentInput:     recentInput,
			RecentSamples:   len(v.samples),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CacheRead != out[j].CacheRead {
			return out[i].CacheRead > out[j].CacheRead
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// PerModel trả về lượng dùng tích lũy của từng model. Kết quả sắp xếp giảm dần theo chi phí, tiếp đó là lượng đầu vào.
func (t *UsageTracker) PerModel() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perModel))
	for model, v := range t.perModel {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		out = append(out, AgentUsage{
			Model:        model,
			Input:        v.Input,
			Output:       v.Output,
			CacheRead:    v.CacheRead,
			CacheWrite:   v.CacheWrite,
			Cost:         v.Cost,
			Saved:        v.Saved,
			CacheCapable: v.CacheCapable,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// resolveCost đồng thời trả về cost / saved / capable của tin nhắn này.
//   - cost: nếu trúng sổ đăng ký thì tính nhân theo 4 mục; không trúng thì thoái lùi về cost mặc định của provider
//   - saved: chỉ > 0 khi trúng sổ đăng ký, CacheRead > 0 và InputCost > CacheReadCost
//   - capable: trúng sổ đăng ký và model đó có CacheReadCostPer1M > 0 → được biết là có hỗ trợ prompt caching
//
// modelName ưu tiên dùng tham số từ bên gọi truyền vào (khi replay lấy từ _meta.model của session jsonl).
func (t *UsageTracker) resolveCost(modelName string, u agentcore.Usage) (cost, saved float64, capable bool) {
	if entry, ok := models.DefaultRegistry().Resolve(modelName); ok {
		c := computeCost(u, *entry)
		s := computeSaved(u, *entry)
		canCache := entry.CacheReadCostPer1M > 0
		if c > 0 {
			return c, s, canCache
		}
	}
	if u.Cost != nil {
		return u.Cost.Total, 0, false
	}
	return 0, 0, false
}

// agentRoleName chuẩn hóa tên subagent thành tên role.
// architect_short/mid/long đều được gộp vào architect; các tên khác trả về nguyên trạng.
func agentRoleName(agentName string) string {
	if strings.HasPrefix(agentName, "architect_") {
		return "architect"
	}
	return agentName
}

// computeCost tính toán chi phí USD của lần gọi này dựa trên đơn giá $/1M tokens.
//
// Tiền đề ngữ nghĩa (được các provider của litellm thống nhất đảm bảo, xem anthropic.go / bedrock.go /
// điểm lắp ráp Usage của openai.go / gemini.go / compat.go):
//
//	u.Input  = toàn bộ token đầu vào, **bao gồm** CacheRead; không bao gồm CacheWrite
//	u.Output = token đầu ra
//
// Do đó nonCachedInput = u.Input - u.CacheRead được áp dụng cho mọi provider.
// Nhánh dự phòng được giữ lại để đối phó với trường hợp một provider nào đó trả về dữ liệu rác trong tương lai mà không bị hỏng.
func computeCost(u agentcore.Usage, e models.ModelEntry) float64 {
	nonCachedInput := u.Input - u.CacheRead
	if nonCachedInput < 0 {
		nonCachedInput = u.Input
	}
	c := 0.0
	c += float64(nonCachedInput) * e.InputCostPer1M / 1_000_000
	c += float64(u.Output) * e.OutputCostPer1M / 1_000_000
	c += float64(u.CacheRead) * e.CacheReadCostPer1M / 1_000_000
	c += float64(u.CacheWrite) * e.CacheWriteCostPer1M / 1_000_000
	return c
}

// computeSaved ước tính lượng USD tiết kiệm được nhờ trúng CacheRead so với việc "tính phí theo giá đầu vào thông thường".
// Lưu ý rằng chi phí vượt trội của CacheWrite không được khấu trừ — nó thuộc về khoản đầu tư cần thiết "dọn đường cho những lần trúng sau",
// lợi ích thực sự phụ thuộc vào việc thu hồi tích lũy từ CacheRead phía sau.
func computeSaved(u agentcore.Usage, e models.ModelEntry) float64 {
	if u.CacheRead <= 0 || e.InputCostPer1M <= 0 {
		return 0
	}
	delta := e.InputCostPer1M - e.CacheReadCostPer1M
	if delta <= 0 {
		return 0
	}
	return float64(u.CacheRead) * delta / 1_000_000
}
