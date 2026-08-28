package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText giải mã các byte file văn bản người dùng cung cấp thành UTF-8: khi UTF-8 không hợp lệ thì chuyển mã theo GB18030
// (GBK siêu tập) — txt tiểu thuyết tiếng Trung lưu hành trên mạng phần lớn là mã GBK, cứ đọc thẳng như UTF-8
// thì toàn chữ loạn. Chuỗi byte không phải GBK sẽ bị bộ giải mã thay bằng U+FFFD (vốn đã là chữ loạn, do phía gọi
// báo lỗi dự phòng zero-hit để dẫn dắt người dùng). Cuối cùng bóc UTF-8 BOM (nếu không khớp đầu dòng sẽ dính nó).
func DecodeText(data []byte) string {
	if !utf8.Valid(data) {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			data = decoded
		}
	}
	return strings.TrimPrefix(string(data), "\uFEFF")
}