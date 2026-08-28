package rules

import (
	"strings"
)

// Check kiểm tra máy móc chính văn chương theo quy tắc cấu trúc, trả về danh sách sự thật vi phạm.
//
// Hợp đồng thiết kế:
//   - Chỉ trả về sự thật, không ra lệnh (luật thép 1)
//   - Không cản trở luồng của bất kỳ bên gọi nào
//   - severity ánh xạ cố định theo loại quy tắc (xem bảng chú thích ở types.go)
//
// Tham số:
//   - text: chính văn chương (bản thảo hoặc bản cuối đều được)
//   - s: quy tắc cấu trúc sau khi gộp; khi IsEmpty sẽ trực tiếp trả về nil.
func Check(text string, s Structured) []Violation {
	if s.IsEmpty() {
		return nil
	}

	var violations []Violation
	violations = appendForbiddenChars(violations, text, s.ForbiddenChars)
	violations = appendForbiddenPhrases(violations, text, s.ForbiddenPhrases)
	violations = appendFatigueWords(violations, text, s.FatigueWords)
	return violations
}

// forbidden_chars: xuất hiện ≥1 lần là error.
// Cùng một quy tắc chỉ tạo ra một violation, actual là số lần xuất hiện.
func appendForbiddenChars(vs []Violation, text string, list []string) []Violation {
	for _, ch := range list {
		if ch == "" {
			continue
		}
		n := strings.Count(text, ch)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_chars",
			Target:   ch,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// forbidden_phrases: xuất hiện ≥1 lần là error; hành vi giống với forbidden_chars, chỉ khác tên rule.
func appendForbiddenPhrases(vs []Violation, text string, list []string) []Violation {
	for _, ph := range list {
		if ph == "" {
			continue
		}
		n := strings.Count(text, ph)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_phrases",
			Target:   ph,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// fatigue_words: số lần xuất hiện trong chương này vượt quá ngưỡng mới vi phạm, cấp warning.
// Không cộng dồn xuyên chương —— vấn đề xuyên chương để sau cho chẩn đoán.
func appendFatigueWords(vs []Violation, text string, m map[string]int) []Violation {
	for word, limit := range m {
		if word == "" || limit <= 0 {
			continue
		}
		n := strings.Count(text, word)
		if n <= limit {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "fatigue_words",
			Target:   word,
			Limit:    limit,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}
