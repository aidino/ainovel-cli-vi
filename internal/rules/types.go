// Package rules hiện thực hóa lớp đầu vào sở thích người dùng (Policy): chuẩn hóa quy tắc viết từ các nguồn, gộp thành
// điểm khôi phục của cuốn sách này (xem snapshot.go), lúc chạy được novel_context tiêm vào, commit_chapter kiểm tra máy móc.
//
// Rule là sự thật loại thứ tư, xếp ngang hàng với Progress / Checkpoint / Artifact, nhưng tính chất ngược lại:
// ba loại trước là đầu ra hệ thống, Rule là đầu vào bền bỉ của ý định người dùng.
//
// Ràng buộc thiết kế (không thể thỏa hiệp):
//   - Công cụ chỉ trả về sự thật, không trả về lệnh (Violation là sự thật, do biên tập viên quyết định có kích hoạt viết lại không)
//   - Không đưa vào đường dẫn verdict mới (tái sử dụng PendingRewrites)
//   - Không đưa vào trường độ nghiêm ngặt (severity được ánh xạ cố định theo loại quy tắc, biên tập viên tự chủ phán quyết ngữ nghĩa)
//   - Không động đến Flow Router (quy tắc không tham gia định tuyến)
package rules

// SourceKind đánh dấu nguồn file quy tắc, chỉ dùng để tạo nhãn nguồn (ví dụ global:my-style.md).
type SourceKind int

const (
	// SourceGlobal — Sở thích toàn cục của người dùng (tất cả .md trong thư mục ~/.ainovel/rules/, gộp theo thứ tự từ điển tên file), tái sử dụng xuyên sách.
	SourceGlobal SourceKind = iota
	// SourceProject — Quy tắc cuốn sách này (tất cả .md trong thư mục ./.ainovel/rules/, gộp theo thứ tự từ điển tên file), độ ưu tiên cao nhất.
	SourceProject
)

// String trả về tên dễ đọc của nguồn, dùng cho tiền tố nhãn nguồn.
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// Structured chứa các trường quy tắc cấu trúc có thể kiểm tra máy móc (kết quả ứng viên/gộp sau khi chuẩn hóa các nguồn).
// Số từ chương cố tình không nằm ở đây: dài bao nhiêu tính là một chương là vấn đề toàn vẹn tự sự, thuộc quyền định đoạt ngữ nghĩa (người viết/biên tập viên),
// số hóa thành đường cứng máy móc sẽ xúi giục model bơm nước để vượt vạch —— ý muốn số từ đi theo kênh ngôn ngữ tự nhiên preferences.
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty dùng để phán đoán xem có hoàn toàn không có quy tắc cấu trúc nào không; checker có thể dựa vào đó để bỏ qua.
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity đánh dấu mức độ nghiêm trọng của Violation.
// Ánh xạ cố định (người dùng không thể cấu hình):
//
//	forbidden_chars xuất hiện             -> Error
//	forbidden_phrases xuất hiện           -> Error
//	fatigue_words vượt ngưỡng             -> Warning
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Violation là đầu ra của checker: trần thuật sự thật chương này đã vi phạm một quy tắc máy móc nào đó.
//
// Chú ý: commit_chapter truyền suốt các violations vào JSON trả về, không cản trở commit;
// biên tập viên khi đọc kiểm sẽ ánh xạ những sự thật này vào 7 chiều hiện tại (thẩm mỹ/nhịp điệu/nhân vật/nhất quán),
// do LLM tự chủ quyết định xem có nâng cấp phán quyết để kích hoạt đánh bóng/viết lại không.
type Violation struct {
	Rule     string   `json:"rule"`             // forbidden_chars / forbidden_phrases / fatigue_words
	Target   string   `json:"target,omitempty"` // Đối tượng vi phạm cụ thể (từ/ký tự nào)
	Limit    any      `json:"limit,omitempty"`  // Ngưỡng; fatigue_words=int / forbidden_*=rỗng
	Actual   any      `json:"actual"`           // Giá trị thực tế: số lần xuất hiện
	Severity Severity `json:"severity"`         // error / warning
}
