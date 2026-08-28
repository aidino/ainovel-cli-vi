// Package imp cài đặt đường ống import ngữ nghĩa phân giai đoạn của tiểu thuyết bên ngoài (docs/import-pipeline.md).
//
// Model chịu trách nhiệm hiểu ngữ nghĩa mở, code chịu trách nhiệm tọa độ, phủ, kiểu, băm, thứ tự và lũy đẳng; tất cả sản phẩm ngữ nghĩa sau khi
// được xác minh trong không gian làm việc độc lập (meta/import/) mới đăng lên trạng thái sách chính thức. Hành động tiếp theo chỉ được suy ra từ công kiện
// (NextAction), không lưu enum giai đoạn sẽ bị trôi dạt, việc khôi phục không phụ thuộc from=N.
package imp

import "time"

// Options kiểm soát một lần import. Khi khôi phục các trường có thể rỗng, suy ra trực tiếp từ không gian làm việc hoạt động và Intent đã lưu.
type Options struct {
	SourcePath      string // bắt buộc điền cho import mới; khi khôi phục có thể rỗng
	AutoConfirm     bool   // --yes: tự động chấp nhận cắt sau khi qua xác minh phủ
	StoryResolution string // --story=open|closed: chọn trước chỉ khi synthesis trả về uncertain
	ContinueAfter   bool   // --continue: không tạo Hold import hoàn thành
	Guidance        string // --guide: Hướng dẫn cắt bằng ngôn ngữ tự nhiên, sau khi lưu vào không gian làm việc sẽ tự nhiên làm cho việc cắt cũ không khớp để nhận diện lại
	// AcceptSegmentation: Xác nhận thủ công rõ ràng sau khi xem trước ở TUI (y). Cho qua lần cắt hiện tại một lần, không ghi intent;
	// Sự khác biệt với --yes: --yes là ủy quyền mù quáng không xem trước, không cho qua các bản cắt có chú thích dung sai (Notes), y là phán quyết sau khi xem trước.
	AcceptSegmentation bool
}

// intent trích xuất quyền của người dùng cần lưu trữ từ Options.
func (o Options) intent() Intent {
	return Intent{
		Version:             workspaceSchemaVersion,
		AutoConfirm:         o.AutoConfirm,
		StoryResolution:     o.StoryResolution,
		ContinueAfterImport: o.ContinueAfter,
	}
}

// Stage biểu thị giai đoạn hiện tại của quy trình import, chỉ dùng để hiển thị UI, không phải nguồn sự thật khôi phục (RFC §14.1).
type Stage string

const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)

// Event là sự kiện tiến độ mà quy trình import phát ra ngoài. Event là hình chiếu, không tham gia khôi phục.
type Event struct {
	Time      time.Time
	Stage     Stage
	Current   int       // tiến độ chương/khoảng
	Total     int       // tổng số
	Message   string    // mô tả có thể đọc được bằng người
	Level     string    // ""=tiến độ bình thường; "warn"=trạng thái cảnh báo như thử lại lùi bước/hỏi lại xác minh
	Key       string    // Khi không rỗng UI sẽ cập nhật tại chỗ các sự kiện liên tiếp có cùng Key (ví dụ 7 lần thử lại biến động trên một dòng), căn chỉnh cơ chế ID của bảng sự kiện
	RetryAt   time.Time // khác không = thời điểm kết thúc thử lại lần sau; UI dựa vào đây để render đếm ngược từng giây, đến giờ thì xóa (yêu cầu đang trên đường)
	Err       error     // mang theo khi StageError
	Continued bool      // Được Host đặt khi StageDone: Có tự động tiếp sức khởi động Engine chưa (--continue × auto)
}
