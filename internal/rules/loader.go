package rules

import (
	"os"
	"path/filepath"
)

// LoadOptions liệt kê các thư mục nguồn của file rules, phục vụ RawFileSources quét và chuẩn hóa.
//
// Thư mục không tồn tại không tính là lỗi, quét sẽ bỏ qua im lặng.
type LoadOptions struct {
	// HomeRulesDir là thư mục ~/.ainovel/rules/; quét mọi .md tầng trên cùng bên dưới (hợp nhất theo thứ tự từ điển tên file). Rỗng nghĩa là bỏ qua.
	HomeRulesDir string

	// ProjectRulesDir là thư mục ./.ainovel/rules/ (phản chiếu toàn cục, cũng quét mọi .md tầng trên cùng bên dưới). Rỗng nghĩa là bỏ qua.
	ProjectRulesDir string
}

// ainovelDirName là tên dotdir mà ainovel dùng chung cho hai cấp user / project.
// Toàn cục ~/.ainovel/rules/ và dự án ./.ainovel/rules/ đối xứng nhau qua đây.
const ainovelDirName = ".ainovel"

// DefaultProjectRulesDir ghép đường dẫn tuyệt đối của ./.ainovel/rules/ (dựa trên thư mục dự án cho trước).
// Phía gọi truyền vào gốc dự án, tránh phụ thuộc cwd bên trong loader; phản chiếu DefaultHomeRulesDir.
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ainovelDirName, "rules")
}

// DefaultHomeRulesDir ghép đường dẫn tuyệt đối của thư mục ~/.ainovel/rules/.
// Giải thuật home thất bại thì trả chuỗi rỗng (phía gọi dựa vào đó để bỏ qua nguồn này).
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ainovelDirName, "rules")
}

// homeRulesReadme là phần hướng dẫn ghi vào ~/.ainovel/rules/README.txt khi lần đầu dẫn dắt.
// Cố tình dùng đuôi .txt thay vì .md — bộ quét chỉ nhận .md, phần hướng dẫn này sẽ không bị coi là rules để chuẩn hóa.
const homeRulesReadme = `Đặt sở thích viết toàn cục tại đây, hiệu lực với mọi bộ sách.

Tạo một file .md mới (ví dụ my-style.md), viết yêu cầu bằng lời bình dân là được —
không cần bất kỳ định dạng nào, không cần YAML:

    # Nhân vật
    - Nhân vật chính Lâm Trần đừng viết thành thánh thiện, ngoài lạnh trong nóng là đủ
    # Văn phong
    - Dùng cảm nhận cơ thể (đốt ngón tay trắng bệch) thay thế nhãn dán cảm xúc (căng thẳng)
    - Hội thoại đừng quá văn viết, mỗi chương khoảng 3000 từ
    - Đừng xuất hiện kiểu văn AI "một mức độ nào đó"

Viết xong không cần quan tâm định dạng: hệ thống sẽ dùng model chuẩn hóa các yêu cầu
ngôn ngữ tự nhiên này thành ràng buộc có cấu trúc (khoảng số từ, từ cấm, ngưỡng từ mệt
mỏi v.v.), tự động tuân thủ khi viết, tự động tự kiểm khi nộp.

Nhiều file .md hợp nhất theo thứ tự từ điển tên file; file ẩn đầu chấm, file không phải
.md đều bị bỏ qua (vì vậy README.txt này không bị coi là rules).

Cơ sở máy móc: câu AI sáo rỗng, từ mệt mỏi thời kỳ đầu đã được mặc định sẵn, dùng ngay
được, không viết cũng không sao.

Ưu tiên tải (cao -> thấp): ./.ainovel/rules/*.md (sách này) > ~/.ainovel/rules/*.md (ở đây) > mặc định nội bộ
`

// EnsureHomeRulesDir cố gắng tạo thư mục ~/.ainovel/rules/ và ghi README.txt dẫn dắt,
// để người dùng phát hiện điểm mở rộng sở thích toàn cục này và biết cách viết.
// Chỉ là nice-to-have, không phải đường dẫn then chốt: giải thuật home thất bại hay ghi lỗi đều nuốt im lặng, tuyệt đối không chặn khởi động.
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt tạo thư mục và ghi README.txt theo mẫu dẫn dắt hiện tại, là hạt nhân khả kiểm của EnsureHomeRulesDir.
// README.txt là file dẫn dắt do hệ thống sinh (sở thích người dùng viết trong *.md, nó không bị quét tải), mỗi lần đều ghi đè
// bằng mẫu mới nhất — không giữ nội dung cũ, nên cũng không cần bất kỳ logic tương thích phiên bản nào.
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions dựng LoadOptions thông dụng theo thư mục làm việc hiện tại.
//
// Phù hợp gọi một lần lúc Host khởi động, để dịch vụ quy tắc người dùng dùng chung một cấu hình nguồn.
// Giải thuật cwd thất bại thì ProjectRulesDir để rỗng (việc quét sẽ bỏ qua nguồn này).
//
// Ngữ nghĩa đường dẫn: ProjectRulesDir gắn với **thư mục làm việc hiện tại (cwd)** chứ không phải outputDir.
// Người dùng cd đến thư mục khác khởi động viết sách khác, ./.ainovel/rules/ tự nhiên đi theo cwd; nếu cần dùng chung
// xuyên sách, đặt vào thư mục toàn cục ~/.ainovel/rules/ là được (mọi .md bên dưới đều được tải).
func DefaultOptions() LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
