package host

import (
	"time"
)

// Event là sự kiện có cấu trúc được TUI tiêu thụ.
//
// Đối với ba loại sự kiện gọi TOOL / DISPATCH / DECISION, bắt đầu và kết thúc của cùng một lần gọi dùng chung một ID:
// Khi bắt đầu, phát ra sự kiện có FinishedAt là giá trị 0 (TUI hiển thị kiểu "đang tiến hành");
// Khi kết thúc, phát ra sự kiện cùng ID, điền vào FinishedAt + Duration (+ Failed),
// TUI dùng ID định vị dòng ban đầu để cập nhật tại chỗ, tránh dư thừa "bắt đầu một dòng, hoàn thành một dòng khác".
//
// Các sự kiện không phải gọi như SYSTEM / ERROR / CONTEXT có ID trống, mỗi sự kiện thêm độc lập.
type Event struct {
	ID         string    // dùng chung cho bắt đầu/kết thúc của cùng một lần gọi; trống đối với sự kiện không phải gọi
	Time       time.Time // thời gian phát ra lần đầu (thời điểm bắt đầu)
	FinishedAt time.Time // giá trị 0 = đang tiến hành; khác 0 = đã hoàn thành
	Failed     bool      // đã hoàn thành nhưng thất bại (chỉ có ý nghĩa ở trạng thái hoàn thành)
	Category   string    // DISPATCH / TOOL / DECISION / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string    // agent sinh ra sự kiện
	Summary    string
	Detail     string        // văn bản đầy đủ, ghi vào nhật ký không bị cắt xén để phục vụ gỡ lỗi; nếu trống thì lùi về Summary. UI chỉ đọc Summary
	Kind       string        // phân loại lỗi (như stream_idle), xuất cùng nhật ký để lọc/cảnh báo; nếu trống thì không xuất
	Level      string        // info / warn / error / success
	Depth      int           // 0 = tầng Engine, 1 = tầng Worker
	Duration   time.Duration // thời gian thực thi khi hoàn thành
	RetryAt    time.Time     // sự kiện loại thử lại: thời điểm kết thúc lần thử lại tiếp theo; UI dựa vào đây để đếm ngược từng giây, đến thời điểm thì xóa (yêu cầu đang xử lý)
}

// Running trả về việc sự kiện có đang tiến hành hay không.
// Chỉ sự kiện loại gọi (có ID TOOL / DISPATCH / DECISION) mới có thể đang tiến hành; các loại khác luôn trả về false.
func (e Event) Running() bool {
	return e.hasLifecycle() && e.FinishedAt.IsZero()
}

func (e Event) hasLifecycle() bool {
	if e.ID == "" {
		return false
	}
	switch e.Category {
	case "TOOL", "DISPATCH", "DECISION":
		return true
	default:
		return false
	}
}

// UISnapshot là ảnh chụp trạng thái tổng hợp cần thiết cho TUI hiển thị.
type UISnapshot struct {
	Provider             string
	BookTitle            string
	ModelName            string
	ModelContextWindow   int // cửa sổ ngữ cảnh của model mặc định hiện tại (giải tích theo thời gian thực khi chuyển đổi /model)
	ThinkingLevel        string
	Style                string
	RuntimeState         string // idle / running / pausing / paused / completed
	StatusLabel          string
	Phase                string
	Flow                 string
	CurrentChapter       int
	TotalChapters        int
	CompletedCount       int
	TotalWordCount       int
	InProgressChapter    int
	PendingRewrites      []int
	RewriteReason        string
	PendingSteer         string
	AdvanceMode          string
	AdvancePermitChapter int
	HasAdvanceHold       bool
	AdvanceHoldReason    string
	RecoveryLabel        string
	IsRunning            bool
	Agents               []AgentSnapshot

	// lượng sử dụng tích lũy (toàn bộ session, xuyên suốt tất cả agent và chuyển đổi model)
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64 // số đô la tiết kiệm được nhờ CacheRead hit (so với việc tính phí toàn bộ đầu vào không dùng cache)
	BudgetLimitUSD        float64 // giới hạn ngân sách (config budget.book_usd); 0 = chưa bật

	// chẩn đoán cache
	OverallCacheCapable    bool // ít nhất một role đã chạy model hỗ trợ prompt cache (để phân biệt "chưa bật" và "0% hit")
	OverallRecentCacheRead int  // tổng cacheRead N lần gần nhất của cửa sổ trượt
	OverallRecentInput     int  // tổng input N lần gần nhất của cửa sổ trượt
	OverallRecentSamples   int  // số mẫu trong cửa sổ trượt (≤ recentSampleCap)
	TotalCacheBreaks       int  // số lần đứt chuỗi cache được phát hiện trực tiếp (tiền tố không ngắn đi nhưng tỷ lệ hit giảm đột ngột), xem chi tiết usage.go noteCacheBreak

	// MissingAssistantUsage > 0 thường có nghĩa là luồng dữ liệu (streaming) từ upstream không tuân theo
	// giao thức stream_options.include_usage của OpenAI để gửi final usage chunk (thường gặp ở proxy tự xây dựng),
	// dẫn đến UsageTracker không nhận được bất kỳ dữ liệu tích lũy nào. UI dựa vào đây để nhắc người dùng kiểm tra backend,
	// tránh làm người dùng hiểu lầm rằng bản thân module cache bị hỏng.
	MissingAssistantUsage int

	// cấp độ cache per-role, sắp xếp giảm dần theo CacheRead, đã lọc các role chưa tiêu thụ token
	CachePerAgent []AgentCacheStat
	CachePerModel []AgentCacheStat

	// thiết lập cơ bản
	Synopsis         string
	Premise          string
	Outline          []OutlineSnapshot
	Characters       []string
	SupportingCount  int      // tổng số nhân vật phụ trong danh sách diễn viên phụ
	RecentSupporting []string // nhân vật phụ hoạt động gần đây (tối đa 5 người, sắp xếp giảm dần theo LastSeenChapter)
	Layered          bool
	CurrentVolumeArc string
	NextVolumeTitle  string
	CompassDirection string
	CompassScale     string

	// chi tiết
	LastCommitSummary  string
	LastReviewSummary  string
	LastCheckpointName string
	RecentSummaries    []string
}

// OutlineSnapshot là tóm tắt hiển thị của mục lục đại cương.
type OutlineSnapshot struct {
	Chapter   int
	Title     string
	CoreEvent string
}

// AgentSnapshot là phần chiếu hiển thị của trạng thái Agent.
type AgentSnapshot struct {
	Name      string
	State     string
	TaskID    string
	TaskKind  string
	Summary   string
	Tool      string
	Turn      int
	Context   AgentContextSnapshot
	UpdatedAt time.Time
}

// AgentCacheStat là lượng tích lũy cache hit của một agent đơn lẻ (chiếu sang cột trái).
// HitRate = CacheRead / Input; Input trong tầng litellm đã được thống nhất là ngữ nghĩa "đã bao gồm CacheRead".
//
// CacheCapable dùng để phân biệt hai loại 0% hit:
//   - true  → model hỗ trợ prompt cache, 0% là do prompt thiết kế kém hoặc tiền tố không ổn định, cần tối ưu
//   - false → model/provider không hỗ trợ prompt cache, 0% là theo dự kiến, không cần khắc phục
//
// Recent* là dữ liệu hit của cửa sổ trượt (N lần gọi gần nhất), so sánh tích lũy để nhận diện "bị kéo tụt ở giai đoạn đầu" vs "tỷ lệ hit thấp ổn định".
type AgentCacheStat struct {
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

// AgentContextSnapshot là tình trạng sử dụng ngữ cảnh của Agent.
type AgentContextSnapshot struct {
	Tokens          int
	ContextWindow   int
	Percent         float64
	Scope           string
	Strategy        string
	ActiveMessages  int
	SummaryMessages int
	CompactedCount  int
	KeptCount       int
}

// CoCreateMessage là tin nhắn của hội thoại đồng sáng tạo.
type CoCreateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CoCreateReply là câu trả lời LLM của hội thoại đồng sáng tạo. Raw giữ nguyên toàn bộ bốn đoạn gốc của model,
// dùng để ghi lại vào history giúp model vòng sau thấy được [DRAFT] của chính mình vòng trước, từ đó mới thực sự
// cập nhật lũy kế trên bản thảo đã có (nếu chỉ có Message không có [DRAFT], sẽ khiến model tóm tắt lại từ đầu dựa vào hội thoại mỗi vòng).
// Suggestions là "điều bạn có thể muốn nói tiếp theo" mà AI chủ động cung cấp, khi người dùng bị kẹt ý thì nhấn phím số để điền nhanh vào ô nhập.
type CoCreateReply struct {
	Message     string
	Prompt      string
	Ready       bool
	Suggestions []string
	Raw         string
}
