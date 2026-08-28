package rules

import (
	"regexp"
	"strings"
)

// Lint kiểm tra giới hạn dưới sản phẩm tích hợp: quét các tàn dư cơ chế trong chính văn, không liên quan đến quy tắc người dùng, luôn thực thi khi commit.
// Cùng hợp đồng với Check —— chỉ trả về sự thật (luật thép 1), không cản trở luồng, do đọc kiểm/người dùng phán quyết.
//
// Hiện có ba loại (tất cả đều từ khiếm khuyết thực chứng của sản phẩm chạy dài thực tế):
//   - markdown_residue: chính văn tàn dư in đậm **, các dòng tiêu đề # ngoài dòng đầu (xuất txt sẽ lộ ký hiệu)
//   - non_cjk_fragments: đoạn chữ Latinh liên tục (ngôn ngữ model hỗn tạp, ví dụ chính văn tiếng Trung lẫn lộn "pattern")
func Lint(text string) []Violation {
	var vs []Violation
	vs = appendMarkdownResidue(vs, text)
	vs = appendNonCJKFragments(vs, text)
	return vs
}

func appendMarkdownResidue(vs []Violation, text string) []Violation {
	if n := strings.Count(text, "**"); n > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "**",
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	headings := 0
	seenContent := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// Tiêu đề # ở dòng không trống đầu tiên là định dạng hợp pháp của file chương (không ghi chết theo số dòng, dung nạp dòng trống phía trước)
		first := !seenContent
		seenContent = true
		if !first && strings.HasPrefix(t, "#") {
			headings++
		}
	}
	if headings > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "#",
			Actual:   headings,
			Severity: SeverityWarning,
		})
	}
	return vs
}

var latinFragmentRe = regexp.MustCompile(`[A-Za-z]{2,}`)

// appendNonCJKFragments báo cáo tổng số lần và ví dụ đã khử trùng lặp của đoạn chữ Latinh.
// Tiếng Anh hợp pháp của đề tài hiện đại (tên thương hiệu/viết tắt) cũng sẽ bị khớp —— sự thật cấp warning, do đọc kiểm phán quyết theo đề tài.
func appendNonCJKFragments(vs []Violation, text string) []Violation {
	matches := latinFragmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return vs
	}
	seen := make(map[string]struct{})
	var examples []string
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if len(examples) < 3 {
			examples = append(examples, m)
		}
	}
	return append(vs, Violation{
		Rule:     "non_cjk_fragments",
		Target:   strings.Join(examples, ", "),
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}
