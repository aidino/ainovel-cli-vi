// Package exp thực hiện khả năng xuất các chương đã hoàn thành.
//
// Đối xứng với imp/: chỉ IO cục bộ, không phụ thuộc LLM, không sửa trạng thái store. Xuất có thể chạy đồng thời với
// Engine (chỉ đọc Progress + bản thảo cuối của chương), thuộc về năng lực ngang.
//
// Hiện hỗ trợ TXT và EPUB.
package exp

import "github.com/voocel/ainovel-cli/internal/store"

// Format định danh định dạng xuất.
type Format string

const (
	// FormatTXT xuất văn bản thuần túy.
	FormatTXT Format = "txt"
	// FormatEPUB vùng chứa EPUB 3 chuẩn (zip + xhtml).
	FormatEPUB Format = "epub"
)

// Options kiểm soát hành vi xuất. zero-value tương đương "xuất toàn tập ra đường dẫn mặc định, báo lỗi nếu file tồn tại".
//
// Cách thức: 《Tên sách》 → Ngắt tập → Chính văn chương. Hai loại dữ liệu nội bộ không đưa vào xuất: premise (bản vẽ sáng tác,
// chứa độc giả mục tiêu / điểm tiêu thụ cốt lõi / vùng cấm sáng tác v.v., để tác giả và engine xem, không phải lời tựa của người đọc);
// ngắt arc (dưới góc nhìn người đọc arc là cấu trúc nội bộ quá chi tiết). Tên sách và ngắt tập luôn giữ lại.
type Options struct {
	// Khi Format là chuỗi rỗng sẽ suy ra từ hậu tố của OutPath (.txt → TXT, .epub → EPUB);
	// khi OutPath cũng rỗng sẽ lùi về FormatTXT. Bên gọi SDK có thể chỉ định rõ ràng để bỏ qua việc suy luận.
	Format Format

	// OutPath đường dẫn file xuất; rỗng là {novelDir}/{BookMetadata.Title}.{ext}.
	OutPath string

	// From / To phạm vi chương, khoảng đóng. 0 biểu thị từ chương 1 / đến chương cuối.
	// Chương chưa hoàn thành trong phạm vi sẽ bị bỏ qua và ghi vào Result.Skipped, không coi là lỗi.
	From, To int

	// Overwrite có ghi đè khi file tồn tại không; mặc định từ chối.
	Overwrite bool
}

// Deps là dependency cần cho Run. Chỉ store; xuất không cần LLM, prompt, bundle.
type Deps struct {
	Store *store.Store
}

// Result là tóm tắt sản phẩm của một lần xuất thành công.
type Result struct {
	// Path đường dẫn file thực tế đã ghi (tuyệt đối hoặc tương đối do bên gọi truyền).
	Path string
	// Chapters số chương thực tế đã ghi.
	Chapters int
	// Bytes số byte của file (UTF-8).
	Bytes int
	// Skipped các số chương nằm trong phạm vi nhưng chưa hoàn thành.
	Skipped []int
}
