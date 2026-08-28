package diag

// Severity biểu thị mức độ nghiêm trọng của phát hiện.
type Severity string

const (
	SevCritical Severity = "critical" // Cản trở tiến độ hoặc hỏng dữ liệu
	SevWarning  Severity = "warning"  // Có thể giảm chất lượng hoặc lãng phí token
	SevInfo     Severity = "info"     // Mục có thể tối ưu
)

// Category nhóm các phát hiện theo chiều.
type Category string

const (
	CatFlow     Category = "flow"     // Luồng bị kẹt, trạng thái bất thường, vấn đề khôi phục
	CatQuality  Category = "quality"  // Điểm đọc kiểm, thực hiện hợp đồng, tính nhất quán
	CatPlanning Category = "planning" // Khoảng trống đại cương, chi tiết gieo mầm trôi dạt, la bàn lỗi thời
	CatContext  Category = "context"  // Bất thường nhân vật/dòng thời gian/quan hệ
)

// Confidence biểu thị độ tin cậy của quy tắc phán đoán.
type Confidence string

const (
	ConfHigh   Confidence = "high"   // Tính xác định cao, đáng tin cậy
	ConfMedium Confidence = "medium" // Phán đoán theo kinh nghiệm, có thể nhận định sai
	ConfLow    Confidence = "low"    // Tín hiệu thô, chỉ mang tính tham khảo
)

// AutoLevel biểu thị Finding có thể chuyển thành hành động tự động hay không.
type AutoLevel string

const (
	AutoNone    AutoLevel = "none"    // Chỉ báo cáo, không tự động
	AutoSuggest AutoLevel = "suggest" // Đề xuất hành động nhưng cần xác nhận thủ công
	AutoSafe    AutoLevel = "safe"    // Có thể thực thi tự động an toàn
)

// Finding là một kết quả chẩn đoán có thể thực thi.
type Finding struct {
	Rule       string     // Tên quy tắc, ví dụ "StaleForeshadow"
	Category   Category   // Phân loại
	Severity   Severity   // Mức độ nghiêm trọng
	Confidence Confidence // Độ tin cậy phán đoán
	AutoLevel  AutoLevel  // Cấp độ tự động hóa
	Target     string     // Phạm vi tác dụng đề xuất, ví dụ "runtime.flow"
	Title      string     // Tóm tắt một dòng
	Evidence   string     // Bằng chứng dữ liệu cụ thể
	Suggestion string     // Đề xuất cải tiến (hướng tới prompt/flow/config)
}

// RuleFunc là chữ ký thống nhất của quy tắc chẩn đoán.
type RuleFunc func(snap *Snapshot) []Finding

// ActionKind biểu thị loại hành động chẩn đoán.
type ActionKind string

const (
	ActionEmitNotice      ActionKind = "emit_notice"       // Phát thông báo hệ thống
	ActionEnqueueFollowUp ActionKind = "enqueue_follow_up" // Tạo đề xuất xử lý tiếp theo
)

// Action là hành động có thể thực thi do Planner tạo dựa trên Finding độ tin cậy cao.
type Action struct {
	SourceRule  string     // Tên quy tắc nguồn
	Kind        ActionKind // Loại hành động
	Severity    Severity   // Kế thừa từ Finding
	Summary     string     // Mô tả ngắn gọn
	Message     string     // Tin nhắn truyền cho luồng điều khiển
	Fingerprint string     // Dấu vân tay ổn định của Finding nguồn, dùng để khử trùng lặp runtime
}

// Stats là các chỉ số tổng quan hiển thị cùng phát hiện.
type Stats struct {
	CompletedChapters int
	TotalChapters     int
	TotalWords        int
	AvgWordsPerCh     int
	Phase             string
	Flow              string
	PlanningTier      string
	ReviewCount       int
	RewriteCount      int
	AvgReviewScore    float64
	ForeshadowOpen    int
	ForeshadowStale   int
}

// Report là đầu ra hoàn chỉnh của một lần chạy chẩn đoán.
type Report struct {
	Stats    Stats
	Findings []Finding
	Actions  []Action
}
