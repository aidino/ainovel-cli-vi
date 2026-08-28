package exp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// chapterTitleIndex tìm tiêu đề dựa trên số chương, nếu thiếu trả về chuỗi rỗng.
type chapterTitleIndex map[int]string

func buildTitleIndex(outline []domain.OutlineEntry) chapterTitleIndex {
	idx := make(chapterTitleIndex, len(outline))
	for _, e := range outline {
		if e.Title != "" {
			idx[e.Chapter] = e.Title
		}
	}
	return idx
}

// chapterLocation là vị trí của một chương trong đại cương phân tầng. Chỉ giữ lại thông tin tập cần thiết cho bản in xuất——
// arc không đưa vào xuất (dưới góc nhìn người đọc, arc là cấu trúc nội bộ quá chi tiết).
type chapterLocation struct {
	VolumeIdx       int
	VolumeTitle     string
	IsFirstOfVolume bool
}

// buildLocations tạo {chapter -> location} theo thứ tự chương toàn cục của đại cương phân tầng.
// Số chương được tạo lại theo quy tắc giống FlattenOutline (cộng dồn thứ tự trong tập và trong arc),
// để giữ nhất quán với số chương của Progress.CompletedChapters. Tầng arc vẫn phải duyệt qua (bắt buộc khi tính số chương toàn cục),
// nhưng không đưa vào location——khi xuất chỉ chèn ngắt ở đầu tập.
func buildLocations(volumes []domain.VolumeOutline) map[int]chapterLocation {
	if len(volumes) == 0 {
		return nil
	}
	locs := make(map[int]chapterLocation)
	ch := 0
	for _, v := range volumes {
		firstOfVol := true
		for _, a := range v.Arcs {
			for range a.Chapters {
				ch++
				locs[ch] = chapterLocation{
					VolumeIdx:       v.Index,
					VolumeTitle:     v.Title,
					IsFirstOfVolume: firstOfVol,
				}
				firstOfVol = false
			}
		}
	}
	return locs
}

// chapterHeaderRe khớp dòng đầu tiêu đề Markdown có chứa số chương (# Chương N / ## Chương 12 ...).
var chapterHeaderRe = regexp.MustCompile(`^#+\s+Chương.+?`)

// atxTitleRe trích xuất phần chữ của tiêu đề ATX (# Tiêu đề).
var atxTitleRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// stripChapterTitleHeader loại bỏ nếu dòng đầu là tiêu đề chương trùng lặp với tiêu đề xuất ra.
// Hai trường hợp: 1. "# Chương N ..." (có số chương); 2. Tiêu đề markdown có chữ giống hệt tiêu đề chương này
// (writer thường viết tên chương như tiêu đề ở dòng đầu chính văn, ví dụ "# Nửa đời nổi trôi", trùng với do bộ xuất tạo ra
// "Chương N Nửa đời nổi trôi"). Các thẻ h1 khác (như "# Lời tựa") xem là một phần của chính văn, giữ lại.
// Bên gọi chịu trách nhiệm TrimSpace trước, nên khoảng trắng đầu dòng không được tính đến.
func stripChapterTitleHeader(content, title string) string {
	first, rest, hasNewline := strings.Cut(content, "\n")
	if !isChapterTitleLine(first, title) {
		return content
	}
	if !hasNewline {
		return ""
	}
	return strings.TrimLeft(rest, "\n")
}

func isChapterTitleLine(line, title string) bool {
	if chapterHeaderRe.MatchString(line) {
		return true
	}
	if title = strings.TrimSpace(title); title == "" {
		return false
	}
	m := atxTitleRe.FindStringSubmatch(line)
	return len(m) == 2 && strings.TrimSpace(m[1]) == title
}

// renderTXT nối văn bản cuối cùng.
//
// Thứ tự chương do chapters quyết định (bên gọi đã xóa trùng và sắp xếp tăng dần theo số chương). bodies/titleIdx/locations
// đều xử lý theo "thiếu thì hạ cấp": tiêu đề thiếu chỉ xuất "Chương N"; định vị phân tầng thiếu thì coi như đại cương phẳng.
func renderTXT(
	novelName string,
	chapters []int,
	titleIdx chapterTitleIndex,
	locations map[int]chapterLocation,
	bodies map[int]string,
) string {
	var b strings.Builder

	if name := strings.TrimSpace(novelName); name != "" {
		b.WriteString("《")
		b.WriteString(name)
		b.WriteString("》\n\n")
	}

	useLayered := len(locations) > 0

	for i, ch := range chapters {
		if useLayered {
			if loc, ok := locations[ch]; ok && loc.IsFirstOfVolume {
				b.WriteString("\n═══════════════════════════════════════════\n")
				fmt.Fprintf(&b, "           Tập %d  %s\n", loc.VolumeIdx, strings.TrimSpace(loc.VolumeTitle))
				b.WriteString("═══════════════════════════════════════════\n\n")
			}
		}

		title := strings.TrimSpace(titleIdx[ch])
		if title != "" {
			fmt.Fprintf(&b, "Chương %d  %s\n\n", ch, title)
		} else {
			fmt.Fprintf(&b, "Chương %d\n\n", ch)
		}

		body := stripChapterTitleHeader(strings.TrimSpace(bodies[ch]), title)
		b.WriteString(body)
		b.WriteString("\n")
		if i < len(chapters)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
