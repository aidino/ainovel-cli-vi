package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// premiseHeadingAliases ánh xạ các biến thể tiêu đề (kể cả tiếng Trung của bản gốc)
// về dạng chuẩn tiếng Việt, đảm bảo đọc được premise cũ lẫn premise mới.
var premiseHeadingAliases = map[string]string{
	"Định vị thể loại":                          "Định vị thể loại",
	"Thể loại và tông giọng":                    "Thể loại và tông giọng",
	"Xung đột cốt lõi":                          "Xung đột cốt lõi",
	"Mục tiêu nhân vật chính":                   "Mục tiêu nhân vật chính",
	"Hướng kết thúc":                            "Hướng kết thúc",
	"Vùng cấm khi viết":                         "Vùng cấm khi viết",
	"Điểm mạnh khác biệt":                       "Điểm mạnh khác biệt",
	"Móc khác biệt":                             "Móc khác biệt",
	"Lời hứa cốt lõi":                           "Lời hứa cốt lõi",
	"Động cơ truyện":                            "Động cơ truyện",
	"Trục chính quan hệ / trưởng thành":         "Trục chính quan hệ / trưởng thành",
	"Lộ trình thăng cấp":                        "Lộ trình thăng cấp",
	"Bước ngoặt giữa truyện":                    "Bước ngoặt giữa truyện",
	"Luận đề kết truyện":                        "Luận đề kết truyện",
	"Độ phù hợp dạng ngắn":                      "Độ phù hợp dạng ngắn",
	"Vì sao tác phẩm phù hợp dạng ngắn / kết trong một tập": "Độ phù hợp dạng ngắn",
}

func parsePremiseSections(premise string) map[string]string {
	lines := strings.Split(premise, "\n")
	sections := make(map[string]string)
	var current string
	var body []string

	flush := func() {
		if current == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			sections[current] = text
		}
		body = body[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := canonicalPremiseHeading(trimmed); ok {
			flush()
			current = heading
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonicalPremiseHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if title == "" {
		return "", false
	}
	canonical, ok := premiseHeadingAliases[title]
	return canonical, ok
}

func premiseStructure(premise string, tier domain.PlanningTier) map[string]any {
	sections := parsePremiseSections(premise)
	required := requiredPremiseHeadings(tier)
	found := make([]string, 0, len(required))
	var missing []string
	for _, heading := range required {
		if _, ok := sections[heading]; ok {
			found = append(found, heading)
			continue
		}
		missing = append(missing, heading)
	}

	structure := map[string]any{
		"template_ready": len(missing) == 0,
		"found":          found,
		"missing":        missing,
	}
	if len(sections) > 0 {
		structure["section_count"] = len(sections)
	}
	return structure
}

func requiredPremiseHeadings(tier domain.PlanningTier) []string {
	common := []string{
		"Thể loại và tông giọng",
		"Định vị thể loại",
		"Xung đột cốt lõi",
		"Mục tiêu nhân vật chính",
		"Hướng kết thúc",
		"Vùng cấm khi viết",
		"Điểm mạnh khác biệt",
		"Móc khác biệt",
		"Lời hứa cốt lõi",
	}

	switch tier {
	case domain.PlanningTierLong:
		return append(common,
			"Động cơ truyện",
			"Trục chính quan hệ / trưởng thành",
			"Lộ trình thăng cấp",
			"Bước ngoặt giữa truyện",
			"Luận đề kết truyện",
		)
	case domain.PlanningTierMid:
		return append(common,
			"Động cơ truyện",
			"Bước ngoặt giữa truyện",
		)
	case domain.PlanningTierShort:
		return append(common,
			"Độ phù hợp dạng ngắn",
		)
	default:
		return common
	}
}
