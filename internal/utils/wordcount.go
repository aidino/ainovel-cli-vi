package utils

import "strings"

// CountWords đếm số từ trong một văn bản đa ngôn ngữ.
// Với văn bản tiếng Việt và các ngôn ngữ Latin: đếm số token phân cách bởi khoảng trắng.
// Với văn bản CJK (Trung/Nhật/Hàn): đếm số ký tự, vì mỗi ký tự tương đương một "chữ".
//
// Thuật toán:
//  1. Đếm token từ việc tách theo khoảng trắng (phù hợp tiếng Việt).
//  2. Đếm ký tự CJK bên trong mỗi token.
//  3. Tổng = token thuần Latin + ký tự CJK.
func CountWords(text string) int {
	if text == "" {
		return 0
	}
	total := 0
	for _, token := range strings.Fields(text) {
		cjk := 0
		nonCjk := 0
		for _, r := range token {
			if r >= 0x4E00 && r <= 0x9FFF ||
				r >= 0x3400 && r <= 0x4DBF ||
				r >= 0xF900 && r <= 0xFAFF {
				cjk++
			} else {
				nonCjk++
			}
		}
		if cjk > 0 && nonCjk == 0 {
			// Token hoàn toàn CJK: mỗi ký tự là một "chữ"
			total += cjk
		} else if nonCjk > 0 {
			// Token chứa ký tự Latin → coi là 1 từ
			total++
		}
	}
	return total
}
