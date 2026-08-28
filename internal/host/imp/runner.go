package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/logger"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Phiên bản prompt/schema được đưa vào InputDigest của từng giai đoạn; khi nâng cấp hợp đồng prompt sẽ tăng dần để làm vô hiệu hóa các công cụ ở hạ lưu một cách tự nhiên.
const (
	segmentPromptVersion = "seg-v2" // v2: ranh giới chỉ đặt tại nơi phân tách thực, tiêu đề sao chép y nguyên (phối hợp kiểm tra lại tiêu đề)
	analyzePromptVersion = "analyze-v1"
	confirmMethodAuto    = "auto_authorized"
	confirmMethodUser    = "user_confirmed" // Xác nhận thủ công rõ ràng bằng phím y sau khi xem trước trên TUI
)

// Prompts là từ gợi ý hệ thống cho các hàm ngữ nghĩa. Tổng hợp chia làm 2 giai đoạn: Synthesize ra toàn sách BookSynthesis,
// Range ra khoảng liên tục của sách dài RangeDigest; cấu trúc đầu ra của cả hai khác nhau, cần sử dụng từ gợi ý tương ứng.
type Prompts struct {
	Segment    string
	Analyze    string
	Synthesize string
	Range      string
}

// RunBudgets là ngân sách đầu vào/đầu ra của các hàm ngữ nghĩa. Phiên bản đầu tiên dùng hằng số bảo thủ;
// Tương lai nên được suy ra từ giới hạn context window / completion của model architect hiện tại, để kích thước lô tăng tự nhiên theo năng lực (RFC §9.2/§21).
type RunBudgets struct {
	MaxUnitBytes         int
	SegmentChunkBytes    int
	SegmentContextMargin int
	SegmentMaxTokens     int
	Analyze              AnalyzeBudget
	SynthesizeRangeBytes int
	SynthesizeMaxTokens  int
}

// DefaultRunBudgets trả về ngân sách mặc định bảo thủ, dùng để phòng hờ khi năng lực model không rõ (thăm dò thất bại).
func DefaultRunBudgets() RunBudgets {
	return RunBudgets{
		MaxUnitBytes:         8000,
		SegmentChunkBytes:    24000,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     8192,
		Analyze:              AnalyzeBudget{ContextBytes: 24000, MaxOutputTokens: 8000, PerChapterOutput: 900, PromptOverhead: 2000},
		SynthesizeRangeBytes: 16000,
		SynthesizeMaxTokens:  8192,
	}
}

// ModelRuntime chứa đựng các sự thật về năng lực model cần thiết cho các lệnh gọi ngữ nghĩa của imp, được Host tiêm vào sau khi thăm dò ranh giới (RFC §13/§17).
// Giúp ngân sách kép tăng tự nhiên theo context/completion, thinking được gửi theo năng lực; khi toàn giá trị 0 sẽ lùi về mặc định bảo thủ,
// hành vi nhất quán với trước khi kết nối năng lực. Đầu ra có cấu trúc không gửi response_format theo năng lực provider (xem chú thích callProfile).
type ModelRuntime struct {
	ContextTokens   int                     // Giới hạn ngữ cảnh đầu vào (token)
	MaxOutputTokens int                     // Giới hạn đầu ra hiển thị một lần (token)
	Thinking        agentcore.ThinkingLevel // Đã resolve theo năng lực; ThinkingAuto("") biểu thị không gửi rõ ràng
}

// profile dẫn xuất các tùy chọn năng lực gọi (thinking) của runtime này.
func (rt ModelRuntime) profile() callProfile {
	return callProfile{thinking: rt.Thinking}
}

// Caller là một cấp độ model của hàm ngữ nghĩa: model + sự thật năng lực của model đó (RFC §13.1/§17).
// segment/analyze/synthesize tự giữ cấp độ của mình, ngân sách và tùy chọn gọi đều dẫn xuất theo cấp độ tương ứng,
// cửa sổ nhỏ của cấp độ rẻ tiền chỉ ràng buộc hàm của chính nó, không làm liên lụy các giai đoạn khác.
type Caller struct {
	Model   callModel
	Runtime ModelRuntime
}

// budgetsFromRuntime dẫn xuất ngân sách các hàm ngữ nghĩa từ giới hạn context/completion thực của model (RFC §9.2/§21).
// Điều này mới làm cho việc "đổi model mạnh hơn tự động mở rộng lô, giảm số lần gọi" thành lập; khi năng lực không rõ lùi về mặc định bảo thủ.
func budgetsFromRuntime(rt ModelRuntime) RunBudgets {
	if rt.ContextTokens <= 0 || rt.MaxOutputTokens <= 0 {
		return DefaultRunBudgets()
	}
	const bytesPerToken = 3 // Chuyển đổi bảo thủ tiếng Trung UTF-8: token→byte (đánh giá thấp dung lượng sẽ an toàn hơn)
	out := rt.MaxOutputTokens
	// Ngân sách đầu vào: Ngữ cảnh trừ đi đầu ra hiển thị và ~10% dự trữ suy luận/hệ thống sau đó chuyển đổi theo byte.
	reserve := rt.ContextTokens / 10
	inTokens := rt.ContextTokens - out - reserve
	if inTokens < 2000 {
		inTokens = 2000
	}
	inBytes := inTokens * bytesPerToken
	return RunBudgets{
		MaxUnitBytes:         min(inBytes/2, 32000),
		SegmentChunkBytes:    inBytes,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     out,
		Analyze: AnalyzeBudget{
			ContextBytes:     inBytes,
			MaxOutputTokens:  out,
			PerChapterOutput: 900,
			PromptOverhead:   2000,
		},
		SynthesizeRangeBytes: inBytes,
		SynthesizeMaxTokens:  out,
	}
}

// Confirmation là công cụ xác nhận cắt phân, ràng buộc segmentation hiện tại (RFC §8.4).
type Confirmation struct {
	Method   string `json:"method"`
	Chapters int    `json:"chapters"`
}

// StoryResolution là phán quyết của người dùng về trạng thái câu chuyện uncertain, ràng buộc synthesis hiện tại (RFC §10.4).
type StoryResolution struct {
	Choice string `json:"choice"` // open / closed
}

// Deps là phụ thuộc hẹp của runner (RFC §17). Ba hàm ngữ nghĩa tự khai báo cấp độ model;
// Host mặc định tất cả rơi vào architect, tầng cấu hình có thể trỏ các hàm có tính cơ học mạnh hơn vào cấp độ rẻ hơn (RFC §13.1).
type Deps struct {
	Store         *store.Store
	CommitChapter ChapterCommitter
	Segment       Caller
	Analyze       Caller
	Synthesize    Caller // range digest và book synthesis cùng cấp độ (cùng một giai đoạn tổng hợp)
	Prompts       Prompts
	Budgets       RunBudgets
}

// budgetsFromDeps dẫn xuất ngân sách theo năng lực cấp độ riêng của từng hàm ngữ nghĩa (RFC §9.2/§13.1).
func budgetsFromDeps(d Deps) RunBudgets {
	seg := budgetsFromRuntime(d.Segment.Runtime)
	ana := budgetsFromRuntime(d.Analyze.Runtime)
	syn := budgetsFromRuntime(d.Synthesize.Runtime)
	return RunBudgets{
		MaxUnitBytes:         seg.MaxUnitBytes,
		SegmentChunkBytes:    seg.SegmentChunkBytes,
		SegmentContextMargin: seg.SegmentContextMargin,
		SegmentMaxTokens:     seg.SegmentMaxTokens,
		Analyze:              ana.Analyze,
		SynthesizeRangeBytes: syn.SynthesizeRangeBytes,
		SynthesizeMaxTokens:  syn.SynthesizeMaxTokens,
	}
}

// Run thực thi pipeline nạp hoàn chỉnh: LoadState → NextAction → thực thi một hành động → đọc lại sự thật.
// Chạy trong goroutine riêng; kênh sự kiện trả về sẽ do hàm này đóng.
func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.CommitChapter == nil ||
		deps.Segment.Model == nil || deps.Analyze.Model == nil || deps.Synthesize.Model == nil {
		return nil, fmt.Errorf("deps không hoàn chỉnh")
	}
	if deps.Budgets == (RunBudgets{}) {
		deps.Budgets = budgetsFromDeps(deps)
	}
	// Nhật ký quy trình nạp độc lập thành file: bản sao hoàn chỉnh của một lần nạp (sự kiện, thử lại, chuỗi lỗi hoàn chỉnh) không trộn lẫn với
	// nhật ký Engine/TUI, khi kiểm tra chỉ cần xem file này. Tạo thất bại phải phản hồi lại——bảng điều khiển sẽ hướng dẫn người dùng
	// xem logs/import.log, lùi về trong im lặng tương đương với việc trỏ đến một file không tồn tại (Debug-First).
	log, closeLog, logErr := logger.FileLogger(deps.Store.Dir(), "import.log")
	log.Info("imp nạp runtime của model",
		"segment_ctx", deps.Segment.Runtime.ContextTokens,
		"analyze_ctx", deps.Analyze.Runtime.ContextTokens,
		"synthesize_ctx", deps.Synthesize.Runtime.ContextTokens,
		"analyze_max_output", deps.Analyze.Runtime.MaxOutputTokens,
		"analyze_context_bytes", deps.Budgets.Analyze.ContextBytes)
	events := make(chan Event, 32)
	go func() {
		defer close(events)
		defer closeLog()
		r := &runner{deps: deps, opts: opts, events: events, ws: OpenWorkspace(deps.Store.Dir()), log: log}
		if logErr != nil {
			r.emit(StageIngesting, 0, 0, fmt.Sprintf("Tạo file nhật ký nạp thất bại (%v), bản sao lần này chuyển sang nhật ký mặc định", logErr), nil)
		}
		r.run(ctx)
	}()
	return events, nil
}

type runner struct {
	deps   Deps
	opts   Options
	events chan Event
	ws     *Workspace
	act    Action       // hành động thực thi hiện tại, dành cho giai đoạn đánh dấu công cụ thất bại
	log    *slog.Logger // Nhật ký nạp độc quyền (logs/import.log); khi nil lùi về logger mặc định
}

func (r *runner) emit(stage Stage, current, total int, msg string, err error) {
	r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err})
}

func (r *runner) send(ev Event) {
	r.logEvent(ev)
	// Sự kiện trạng thái cuối và điểm dừng chứa tín hiệu thành bại/cần hành động duy nhất (xác nhận xem trước, gợi ý --story bị mất người dùng sẽ không biết nên làm gì),
	// phải được gửi đi đáng tin cậy; chỉ các sự kiện tiến độ ở giữa mới có thể bị loại bỏ khi ùn ứ.
	if ev.Stage == StageError || ev.Stage == StageDone ||
		ev.Stage == StageAwaitingConfirmation || ev.Stage == StageAwaitingStoryStatus {
		r.events <- ev
		return
	}
	select {
	case r.events <- ev:
	default: // bỏ qua tiến độ khi kênh đầy, tuyệt đối không block pipeline
	}
}

// logEvent sao chép từng sự kiện tiến độ vào nhật ký nạp độc quyền (<gốc sách>/logs/import.log): dòng thử lại trên bảng điều khiển sẽ ghi đè tại chỗ,
// bảng điều khiển biến mất theo phím Esc, nhật ký là ghi chép quy trình hoàn chỉnh duy nhất có thể kiểm tra sau này (§14.1).
func (r *runner) logEvent(ev Event) {
	log := r.log
	if log == nil {
		log = slog.Default()
	}
	args := []any{"stage", string(ev.Stage)}
	if ev.Total > 0 {
		args = append(args, "progress", fmt.Sprintf("%d/%d", ev.Current, ev.Total))
	}
	if ev.Err != nil {
		args = append(args, "err", ev.Err)
	}
	level := slog.LevelInfo
	switch {
	case ev.Stage == StageError:
		level = slog.LevelError // Trạng thái cuối thất bại là dòng nên được lọc ra nhất trong nhật ký, không thể rớt xuống INFO
	case ev.Level == "warn":
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, ev.Message, args...)
}

func (r *runner) fail(msg string, err error) {
	r.saveFailure(err)
	r.emit(StageError, 0, 0, msg, err)
}

// saveFailure thống nhất đưa các thất bại mang phản hồi gốc vào failures/ (điểm rơi thứ ba của RFC §14.2),
// tất cả các hàm ngữ nghĩa như segment/synthesize dùng chung cách phòng hờ này; đường dẫn trục vớt cắt ngắn phân tích đã ghi tại chỗ metadata chi tiết hơn.
// Thất bại không có phản hồi gốc (IO, hủy bỏ, kiểm tra trước) không có đầu ra model nào để lưu, nên không ghi.
func (r *runner) saveFailure(err error) {
	var se *errSemantic
	var tr *errTruncated
	switch {
	case errors.As(err, &se):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error()}, se.Raw)
	case errors.As(err, &tr):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error(), StopReason: "length"}, tr.Raw)
	}
}

// facts kết hợp sự thật của không gian làm việc với đối soát xuất bản chính thức.
func (r *runner) facts() (Facts, error) {
	return CollectFacts(r.deps.Store, r.ws)
}

// profileFor dẫn xuất tùy chọn gọi của một cấp độ nào đó, và phản hồi lùi lại yêu cầu/hỏi lại kiểm tra về luồng sự kiện của giai đoạn tương ứng——
// thử lại lùi lại có thể tích lũy im lặng trên 2 phút, không phản hồi người dùng sẽ tưởng lầm là treo máy (§14.1).
// Key chỉ dành cho yêu cầu lùi lại (mang thời gian hết hạn): đó là trạng thái chớp nhoáng trong cùng một lần gọi, UI cập nhật tại chỗ một dòng (nhấp nháy "Lần thứ N").
// Hỏi lại kiểm tra là sự kiện ngữ nghĩa xuyên suốt các lần gọi——cắt phân gọi theo từng lô, các lô độc lập hỏi lại, dùng chung Key sẽ làm lô sau ghi đè lô trước,
// ăn mất manh mối kiểm tra (thực tế bảng điều khiển chỉ còn một dòng có unit_id thay đổi liên tục), vì vậy mỗi cái thành một dòng giữ lại lịch sử.
func (r *runner) profileFor(c Caller, stage Stage) callProfile {
	prof := c.Runtime.profile()
	prof.log = r.log
	prof.notify = func(msg string, retryAt time.Time) {
		ev := Event{Time: time.Now(), Stage: stage, Message: msg, Level: "warn", RetryAt: retryAt}
		if !retryAt.IsZero() {
			ev.Key = "retry:" + string(stage)
		}
		r.send(ev)
	}
	prof.progress = func(current, total int, msg string) {
		r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg})
	}
	return prof
}

// applyGuidance lưu trữ hướng dẫn rõ ràng --guide lần này thành đầu vào ngữ nghĩa không gian làm việc (RFC §18.3).
// Hướng dẫn là một trong những đầu vào của segmentation InputDigest: nội dung thay đổi tự nhiên làm cắt phân cũ và toàn bộ hạ lưu của nó mất khớp và phải làm lại,
// không viết quy tắc vô hiệu hóa thủ công. Khi không gian làm việc chưa được thiết lập thì bỏ qua trước, vòng lặp tiếp theo sau ingest sẽ ghi.
func (r *runner) applyGuidance() error {
	g := strings.TrimSpace(r.opts.Guidance)
	if g == "" || !r.ws.Active() {
		return nil
	}
	existing, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("đọc hướng dẫn cắt phân đã có: %w", err)
	}
	if existing == g {
		return nil
	}
	// Sau khi xuất bản bắt đầu, các công cụ chính thức không thể bị ghi đè (§12.2): lúc này cắt lại tất nhiên sẽ đụng bức tường chết "từ chối ghi đè" tại publish,
	// và trước khi đụng tường sẽ trả lại toàn bộ quá trình gọi model cắt phân/phân tích/tổng hợp——hãy đẩy thất bại lên sớm ở mức không chi phí.
	// book là mục ghi đầu tiên của xuất bản, sự tồn tại của nó tức là xuất bản đã bắt đầu (kiểm tra trước khi nạp đảm bảo cuốn sách ban đầu là trống).
	book, err := r.deps.Store.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book != nil {
		return fmt.Errorf("Foundation chính thức đã bắt đầu xuất bản, --guide cắt lại sẽ xung đột với nội dung đã xuất bản và bị từ chối ghi đè, không tiếp nhận hướng dẫn cắt phân nữa")
	}
	return r.ws.writeAtomic(fileGuidance, []byte(g))
}

// checkSourceIdentity chặn "không gian làm việc đang tiến hành nhưng truyền vào file nguồn khác": ingest chỉ thực thi khi không có không gian làm việc,
// nếu không so sánh, /import B.txt sẽ im lặng tiếp tục từ điểm ngắt của A, xuất bản xong A mà B chưa đọc một byte nào (RFC §12.1/§18.2).
// Truyền lại đường dẫn cùng một file là thói quen phổ biến (/import phục hồi cùng đường dẫn), so sánh theo tóm tắt nội dung thay vì từ chối mọi đường dẫn.
func (r *runner) checkSourceIdentity() error {
	if r.opts.SourcePath == "" || !r.ws.Active() {
		return nil
	}
	m, err := r.ws.LoadManifest()
	if err != nil {
		return nil // ba món bộ nhận dạng không đọc được sẽ theo chẩn đoán hỏng của ingest, không báo lỗi lặp lại ở đây
	}
	raw, err := os.ReadFile(r.opts.SourcePath)
	if err != nil {
		return fmt.Errorf("đọc file nguồn %s：%w", r.opts.SourcePath, err)
	}
	if Digest(raw) != m.RawSHA256 {
		return fmt.Errorf("Đã có bản nạp của %q đang tiến hành, file nguồn lần này khác với nội dung của nó: vui lòng hoàn thành hoặc hủy bỏ bản nạp cũ (xóa meta/import/) trước khi nạp sách mới", m.SourceName)
	}
	return nil
}

func (r *runner) run(ctx context.Context) {
	if err := r.checkSourceIdentity(); err != nil {
		r.fail("Kiểm tra danh tính file nguồn", err)
		return
	}
	var previous *Facts
	for {
		if ctx.Err() != nil {
			r.fail("Người dùng hủy bỏ", ctx.Err())
			return
		}
		if err := r.applyGuidance(); err != nil {
			r.fail("Ghi hướng dẫn cắt phân", err)
			return
		}
		facts, err := r.facts()
		if err != nil {
			r.fail("Đọc trạng thái nạp", err)
			return
		}
		if previous != nil && facts == *previous {
			r.fail("Nạp đình trệ", fmt.Errorf("Sau khi thực thi hành động, sự thật không thay đổi, hành động tiếp theo vẫn là %q", NextAction(facts)))
			return
		}
		snapshot := facts
		previous = &snapshot
		act := NextAction(facts)
		r.act = act
		err = nil
		switch act {
		case ActionIngest:
			err = r.ingest(ctx)
		case ActionSegment:
			err = r.segment(ctx)
		case ActionAwaitConfirmation:
			if !r.confirm() {
				return // Chế độ tương tác: chờ người dùng xác nhận, dừng ở đây
			}
		case ActionAnalyze:
			err = r.analyze(ctx)
		case ActionSynthesize:
			err = r.synthesize(ctx)
		case ActionAwaitStoryResolution:
			if !r.resolveStoryStatus() {
				return // Không có phán quyết rõ ràng: dừng ở đây, chờ --story=open|closed
			}
		case ActionPublish:
			err = r.publish(ctx)
		case ActionDone:
			r.emit(StageDone, 0, 0, "Nạp hoàn tất, chờ nghiệm thu viết tiếp", nil)
			return
		default:
			err = fmt.Errorf("Hành động không xác định %q", act)
		}
		if err != nil {
			r.fail("Nạp thất bại", err)
			return
		}
	}
}

func (r *runner) ingest(ctx context.Context) error {
	// Đến bước ingest mà thư mục đã tồn tại = bộ ba nhận dạng (manifest/source/intent) bị thiếu hoặc hỏng:
	// createWorkspace sẽ từ chối với lý do "Đã tồn tại (không tham số /import có thể phục hồi)", chạy lại không tham số lại vì
	// WorkspaceReady=false quay về đây yêu cầu đường dẫn nguồn——hai thông báo đánh nhau, người dùng không có đường đi.
	if r.ws.Active() {
		return fmt.Errorf("meta/import/ đã tồn tại nhưng danh tính không gian làm việc không khả dụng (manifest/source/intent bị thiếu hoặc hỏng), vui lòng xác nhận thủ công rồi xóa thư mục đó và nạp lại")
	}
	if err := checkImportPreconditions(r.deps.Store); err != nil {
		return err
	}
	if r.opts.SourcePath == "" {
		return fmt.Errorf("Nạp mới cần đường dẫn file nguồn")
	}
	r.emit(StageIngesting, 0, 0, "Đọc, giải mã, chuẩn hóa và chụp nhanh file nguồn...", nil)
	_, m, err := Ingest(r.deps.Store.Dir(), r.opts.SourcePath, r.opts.intent())
	if err != nil {
		return err
	}
	r.emit(StageIngesting, 0, 0, fmt.Sprintf("Bản chụp nguồn đã sẵn sàng: %s (Mã hóa %s, %d byte)", m.SourceName, m.Encoding, m.SizeBytes), nil)
	return nil
}

func (r *runner) segment(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	units := buildSourceUnits(src, r.deps.Budgets.MaxUnitBytes)
	guidance, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("Đọc hướng dẫn cắt phân: %w", err)
	}
	r.emit(StageSegmenting, 0, 0, fmt.Sprintf("Nhận dạng ngữ nghĩa ranh giới chương (%d đơn vị tọa độ)...", len(units)), nil)
	digest := segmentInputDigest(Digest(src), guidance, segmentPromptVersion)
	// Danh tính bộ đệm lô liên kết thêm MaxUnitBytes: bảng unit được xác định duy nhất bởi (nguồn chuẩn hóa, MaxUnitBytes), đổi model
	// cấp độ thay đổi MaxUnitBytes sẽ định hình lại phân mảnh ảo của dòng cực dài——chuỗi ID (L1.1…) và điểm cuối lô có thể tái hiện nhưng phạm vi
	// byte đã đổi, chỉ dựa vào đối khớp ID điểm cuối sẽ dùng lại ranh giới cũ sai lệch (anchor mất khớp thất bại chắc chắn hoặc cắt sai im lặng).
	chunkIdentity := fmt.Sprintf("%s\x00units:%d", digest, r.deps.Budgets.MaxUnitBytes)
	seg, err := Segment(ctx, r.deps.Segment.Model, r.deps.Prompts.Segment, src, units, guidance,
		r.deps.Budgets.SegmentChunkBytes, r.deps.Budgets.SegmentContextMargin, r.deps.Budgets.SegmentMaxTokens,
		r.profileFor(r.deps.Segment, StageSegmenting), r.ws, chunkIdentity)
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSegmentation, digest, *seg); err != nil {
		return err
	}
	// Cắt phân cuối cùng đã ghi ra đĩa, bộ nhớ đệm cấp lô đã hoàn thành sứ mệnh; xóa thất bại không ảnh hưởng đến tính đúng đắn (digest vẫn nhất quán), nhưng phải để lại dấu vết.
	if cerr := r.ws.clearDir(dirSegmentChunks); cerr != nil {
		r.emit(StageSegmenting, 0, 0, fmt.Sprintf("Xóa bộ đệm cấp lô thất bại (không ảnh hưởng kết quả cắt phân): %v", cerr), nil)
	}
	r.emit(StageSegmenting, len(seg.Chapters), len(seg.Chapters),
		fmt.Sprintf("Cắt phân hoàn tất: %d chương, %d khu vực phụ", len(seg.Chapters), len(seg.Matter)), nil)
	return nil
}

// confirm xử lý xác nhận cắt phân. --yes tự động chấp nhận và viết công cụ confirmation; nếu không sẽ hiển thị bản xem trước và dừng.
func (r *runner) confirm() bool {
	seg, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		r.fail("Đọc kết quả cắt phân", err)
		return false
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		r.fail("Đọc ý định nạp", err)
		return false
	}
	accept := r.opts.AcceptSegmentation
	auto := r.opts.AutoConfirm || (in != nil && in.AutoConfirm)
	// Cắt có dung sai ngữ nghĩa từng xảy ra (Notes khác rỗng: Hấp thụ chương rỗng/đáy điểm bắt đầu/xóa trùng lặp) sẽ không được --yes cho qua mù quáng:
	// Cấu trúc đã bị viết lại xác định, bắt buộc phải đối chiếu thủ công——nếu không chú thích dung sai dưới --yes không ai thấy, bằng với viết lại âm thầm.
	// Nhấn y sau khi xem trước TUI để đi vào AcceptSegmentation (phán quyết rõ ràng sau khi xem trước), không chịu giới hạn này.
	blockedByNotes := auto && !accept && len(seg.Payload.Notes) > 0
	if blockedByNotes {
		auto = false
	}
	if !auto && !accept {
		msg := buildConfirmPreview(&seg.Payload)
		if blockedByNotes {
			msg += "  ! Tồn tại chú thích dung sai cắt, --yes chưa cho qua tự động, vui lòng đối chiếu thủ công\n"
		}
		r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), msg, nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSegmentation)
	if err != nil {
		r.fail("Đọc công cụ cắt phân", err)
		return false
	}
	method, doneMsg := confirmMethodAuto, "Đã tự động chấp nhận cắt phân (--yes)"
	if accept {
		method, doneMsg = confirmMethodUser, "Đã xác nhận cắt phân (kiểm tra thủ công)"
	}
	conf := Confirmation{Method: method, Chapters: len(seg.Payload.Chapters)}
	if err := writeArtifact(r.ws, fileConfirmation, Digest(raw), conf); err != nil {
		r.fail("Viết công cụ xác nhận", err)
		return false
	}
	r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), doneMsg, nil)
	return true
}

// buildConfirmPreview lắp ráp xem trước xác nhận phân đoạn: số lượng chương, khu vực phụ, tất cả tiêu đề chương và đánh dấu uncertain (RFC §8.4).
// Liệt kê đầy đủ, có thể cuộn xem trong viewport của bảng điều khiển; không đặt giới hạn cắt ngắn.
func buildConfirmPreview(seg *Segmentation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Đã cắt phân %d chương", len(seg.Chapters))
	if len(seg.Matter) > 0 {
		fmt.Fprintf(&b, ", %d khu vực phụ", len(seg.Matter))
	}
	if len(seg.Uncertain) > 0 {
		fmt.Fprintf(&b, " (%d chương đáng ngờ)", len(seg.Uncertain))
	}
	b.WriteString(", vui lòng đối chiếu:\n")
	uncertain := make(map[int]bool, len(seg.Uncertain))
	for _, n := range seg.Uncertain {
		uncertain[n] = true
	}
	for _, c := range seg.Chapters {
		fmt.Fprintf(&b, "  Chương %d %s", c.Number, c.Title)
		if uncertain[c.Number] {
			b.WriteString("  [Đáng ngờ]")
		}
		b.WriteByte('\n')
	}
	for _, mt := range seg.Matter {
		fmt.Fprintf(&b, "  [%s] %s\n", mt.Kind, mt.Title)
	}
	// Hướng dẫn dung sai giai đoạn cắt phân (chẳng hạn như tiêu đề giữ chỗ đoạn văn bản trống được hợp nhất vào đoạn trước) phải xuất hiện tại điểm dừng thủ công, nếu không thì hành vi hấp thụ sẽ trở thành ghi đè im lặng.
	for _, n := range seg.Notes {
		fmt.Fprintf(&b, "  ! %s\n", n)
	}
	// Thông báo thao tác (nhấn y để xác nhận / --guide cắt lại / phím Esc) được khối tạm dừng TUI kết xuất thống nhất, ở đây chỉ để lại thực tế để tránh tài liệu bị lệch.
	return b.String()
}

func (r *runner) analyze(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	total := len(seg.Chapters)
	// Digest từng chương chỉ ràng buộc nội dung chương này, không bao gồm bối cảnh lô và ledger tiền truyện. Nếu chương thứ K cần được phân tích lại do thiếu sót/không khớp,
	// các công cụ cũ có digest khớp chính xác sẽ bị tái sử dụng với ledger đã bị vô hiệu hóa. Làm sạch phần đuôi sau phần đầu trước khi bắt đầu phân tích,
	// bắt buộc "phân tích lại một chương sẽ làm mất hiệu lực của tất cả phân tích theo sau", sau đó phân tích phía trước sẽ không còn sinh ra đuôi lỗi thời nữa (RFC §9.6 / #4a).
	if err := discardAnalysesAfter(r.ws, analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion), total); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion)
		if start >= total {
			break
		}
		r.emit(StageAnalyzing, start, total, fmt.Sprintf("Đang phân tích các lô liên tục từ chương %d...", start+1), nil)
		done, err := AnalyzeNext(ctx, r.deps.Analyze.Model, r.deps.Prompts.Analyze, r.ws, src, seg, segArt.InputDigest, analyzePromptVersion, r.deps.Budgets.Analyze, r.profileFor(r.deps.Analyze, StageAnalyzing))
		if err != nil {
			return err
		}
		if done == 0 {
			break
		}
	}
	r.emit(StageAnalyzing, total, total, "Khai thác sự thật từng chương hoàn tất", nil)
	return nil
}

func (r *runner) synthesize(ctx context.Context) error {
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	total := len(segArt.Payload.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("Phân tích từng chương không đầy đủ: %d/%d", len(facts), total)
	}
	r.emit(StageSynthesizing, 0, total, "Quy nạp ngữ nghĩa toàn bộ cuốn sách theo từng lớp...", nil)
	syn, err := Synthesize(ctx, r.deps.Synthesize.Model, r.deps.Prompts.Synthesize, r.deps.Prompts.Range, r.ws, facts,
		r.deps.Budgets.SynthesizeRangeBytes, r.deps.Budgets.SynthesizeMaxTokens, r.profileFor(r.deps.Synthesize, StageSynthesizing))
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSynthesis, synthesisInputDigest(facts), *syn); err != nil {
		return err
	}
	r.emit(StageSynthesizing, total, total, fmt.Sprintf("Tổng hợp hoàn thành: %d tập, trạng thái câu chuyện %s", len(syn.Structure), syn.StoryStatus), nil)
	return nil
}

func (r *runner) publish(ctx context.Context) error {
	synArt, err := readArtifact[BookSynthesis](r.ws, fileSynthesis)
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	total := len(seg.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("Phân tích trước khi xuất bản không đầy đủ: %d/%d", len(facts), total)
	}
	closed, err := r.resolveStory(&synArt.Payload)
	if err != nil {
		return err
	}
	manifest, err := r.ws.LoadManifest()
	if err != nil {
		return err
	}
	f, err := AssembleFoundation(&synArt.Payload, facts, closed, manifest.SourceName)
	if err != nil {
		return err
	}
	r.emit(StageValidating, 0, total, "Lắp ráp Foundation được xác thực thành công", nil)

	r.emit(StagePublishing, 0, total, "Đang xuất bản Foundation chính thức...", nil)
	if err := publishFoundation(r.deps.Store, f); err != nil {
		return err
	}
	// Quá trình nạp hoàn thành Hold phải trước khi bất kỳ chương nào được gửi và được lưu trữ vĩnh viễn: nếu gặp sự cố giữa lúc "gửi chương cuối cùng" và "thiết lập Hold",
	// sau khi khởi động lại isPublished=true → nhập được đánh giá là đã hoàn thành nhưng thiếu thiết lập Hold, Engine sẽ nhầm lẫn coi việc nạp sách như viết tiếp dừng máy thông thường.
	// Đặt sau publishFoundation (RunMeta đã được khởi tạo), trước khi gửi chương, đóng hoàn toàn cửa sổ này; thiết lập lại idemptotent khi chạy lại quá trình xuất bản
	// (--continue không đặt Hold, để bàn giao tiếp sức tự động, RFC §12.4).
	if err := r.setCompletionHold(); err != nil {
		return fmt.Errorf("Thiết lập hoàn thành nạp Hold：%w", err)
	}
	for i, c := range seg.Chapters {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.emit(StagePublishing, c.Number, total, fmt.Sprintf("Đang xuất bản chương %d/%d: %s", c.Number, total, c.Title), nil)
		if err := publishChapter(ctx, r.deps.Store, r.deps.CommitChapter, c.Number, seg.Content(src, i), facts[i]); err != nil {
			return err
		}
	}
	return nil
}

// storyChoice trả về phán quyết hợp lệ của trạng thái uncertain: ưu tiên ràng buộc phán quyết đã lưu của synthesis hiện tại, tiếp theo là opts lần này, cuối cùng là intent ban đầu.
// Phán quyết đã lưu phải được kiểm tra InputDigest có nhất quán với synthesis hiện tại hay không——sau khi tổng hợp lại, phán quyết cũ sẽ không hợp lệ, không thể im lặng
// áp dụng open/closed cũ vào kết quả mới, nếu không người dùng sẽ không bị truy vấn lại (RFC §10.4). Hiển thị rõ --story (intent) là lệnh thường trực của người dùng cho tổng hợp, có thể bảo lưu.
func (r *runner) storyChoice() (string, error) {
	if raw, err := r.ws.readBytes(fileSynthesis); err == nil {
		if art, aerr := readArtifact[StoryResolution](r.ws, fileStoryResolve); aerr == nil && art.InputDigest == Digest(raw) {
			return art.Payload.Choice, nil
		} else if aerr != nil && !os.IsNotExist(aerr) {
			return "", fmt.Errorf("Đọc phán quyết trạng thái câu chuyện: %w", aerr)
		}
	} else {
		return "", fmt.Errorf("Đọc công cụ tổng hợp: %w", err)
	}
	if r.opts.StoryResolution != "" {
		return r.opts.StoryResolution, nil
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		return "", fmt.Errorf("Đọc ý định nạp: %w", err)
	}
	return in.StoryResolution, nil
}

// resolveStoryStatus sẽ ghi vào story-resolution.json (kết hợp với synthesis hiện tại) khi uncertain và có phán quyết rõ ràng,
// để NextAction của hạ lưu sẽ tự động bỏ qua; nếu không có phán quyết thì hiển thị đang chờ và dừng.
func (r *runner) resolveStoryStatus() bool {
	choice, err := r.storyChoice()
	if err != nil {
		r.fail("Đọc phán quyết trạng thái câu chuyện", err)
		return false
	}
	if choice != storyOpen && choice != storyClosed {
		r.emit(StageAwaitingStoryStatus, 0, 0, "Đánh giá tổng hợp rằng trạng thái câu chuyện là uncertain, vui lòng sử dụng --story=open|closed để làm rõ rồi thử lại", nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSynthesis)
	if err != nil {
		r.fail("Đọc kết quả tổng hợp", err)
		return false
	}
	if err := writeArtifact(r.ws, fileStoryResolve, Digest(raw), StoryResolution{Choice: choice}); err != nil {
		r.fail("Ghi phán quyết trạng thái câu chuyện", err)
		return false
	}
	return true
}

// resolveStory đưa ra trạng thái kết thúc câu chuyện dựa trên kết quả tổng hợp và phán quyết rõ ràng của người dùng (RFC §10.4).
func (r *runner) resolveStory(syn *BookSynthesis) (bool, error) {
	switch syn.StoryStatus {
	case storyClosed:
		return true, nil
	case storyOpen:
		return false, nil
	case storyUncertain:
		choice, err := r.storyChoice()
		if err != nil {
			return false, err
		}
		switch choice {
		case storyClosed:
			return true, nil
		case storyOpen:
			return false, nil
		default:
			return false, fmt.Errorf("Trạng thái câu chuyện uncertain, cần --story=open|closed")
		}
	default:
		return false, fmt.Errorf("story_status không xác định: %q", syn.StoryStatus)
	}
}

// setCompletionHold thiết lập lệnh nạp hoàn thành Hold; chỉ có --continue mới bỏ qua (RFC §12.4).
// Lỗi phải lan truyền——Hold là đảm bảo duy nhất để "không viết tiếp sai lầm sau khi nạp", thất bại im lặng tương đương với bảo vệ bị vô hiệu hóa.
func (r *runner) setCompletionHold() error {
	in, err := r.ws.LoadIntent()
	if err != nil {
		return fmt.Errorf("Đọc ý định nạp: %w", err)
	}
	if r.opts.ContinueAfter || (in != nil && in.ContinueAfterImport) {
		return nil
	}
	return r.deps.Store.RunMeta.SetAdvanceHold(domain.AdvanceHold{
		After:  domain.AdvanceHoldAtBoundary,
		Reason: "Nạp tiểu thuyết bên ngoài hoàn tất, chờ nghiệm thu viết tiếp",
	})
}
