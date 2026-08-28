package imp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Các thẻ mã hóa nguồn được hỗ trợ, ghi vào Manifest và sự kiện tiến độ, không dự phòng trong im lặng (RFC §7.1).
const (
	encodingUTF8    = "utf-8"
	encodingUTF8BOM = "utf-8-bom"
	encodingGB18030 = "gb18030"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decoded là kết quả của một lần giải mã: văn bản + mã hóa thực tế được chọn.
type decoded struct {
	text     string
	encoding string
}

// decodeSource giải mã theo trình tự UTF-8 / UTF-8 BOM / GB18030, trả về mã hóa được chọn.
// Thất bại trực tiếp khi không thể giải mã đáng tin cậy hoặc xuất hiện ký tự thay thế, lỗi bao gồm kết quả phát hiện, không giấu "thử GB18030" thành dự phòng im lặng.
func decodeSource(raw []byte) (decoded, error) {
	if bytes.HasPrefix(raw, utf8BOM) {
		body := raw[len(utf8BOM):]
		if !utf8.Valid(body) {
			return decoded{}, fmt.Errorf("Tuyên bố UTF-8 BOM nhưng nội dung không phải UTF-8 hợp lệ")
		}
		return decoded{text: string(body), encoding: encodingUTF8BOM}, nil
	}
	if utf8.Valid(raw) {
		return decoded{text: string(raw), encoding: encodingUTF8}, nil
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil {
		return decoded{}, fmt.Errorf("Không phải UTF-8 hợp lệ, giải mã GB18030 cũng thất bại: %w", err)
	}
	if !utf8.Valid(out) {
		return decoded{}, fmt.Errorf("Kết quả giải mã GB18030 vẫn không phải UTF-8 hợp lệ, không thể giải mã đáng tin cậy")
	}
	if i := bytes.IndexRune(out, utf8.RuneError); i >= 0 {
		return decoded{}, fmt.Errorf("Giải mã GB18030 xuất hiện ký tự thay thế (U+FFFD @ byte %d), không thể giải mã đáng tin cậy; vui lòng xác nhận mã hóa file", i)
	}
	return decoded{text: string(out), encoding: encodingGB18030}, nil
}

// normalize chỉ thực hiện chuyển đổi không làm thay đổi nội dung văn học: CRLF/CR thống nhất thành LF.
// Giữ lại dòng trống, thụt lề, dòng tiêu đề và ký tự trong văn bản chính; không xóa văn bản ở phần đầu, chương rỗng, quảng cáo hoặc cái gọi là nhiễu phần đuôi (RFC §7.2).
// BOM đã được bóc tách trong giai đoạn decodeSource.
func normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// Ingest đọc file nguồn, giải mã, chuẩn hóa và tạo nguyên tử bản chụp nhanh không gian làm việc meta/import/ bằng rename thư mục.
// Trả về handle của không gian làm việc và Manifest; bên gọi dựa vào đó để phát ra sự kiện tiến độ.
func Ingest(bookDir, sourcePath string, in Intent) (*Workspace, *Manifest, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("Đọc file nguồn: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, fmt.Errorf("File nguồn trống: %s", sourcePath)
	}
	dec, err := decodeSource(raw)
	if err != nil {
		return nil, nil, err
	}
	normBytes := []byte(normalize(dec.text))

	m := Manifest{
		Version:          workspaceSchemaVersion,
		SourceName:       filepath.Base(sourcePath),
		RawSHA256:        Digest(raw),
		NormalizedSHA256: Digest(normBytes),
		Encoding:         dec.encoding,
		SizeBytes:        int64(len(raw)),
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if in.Version == 0 {
		in.Version = workspaceSchemaVersion
	}

	ws, err := createWorkspace(bookDir, m, in, normBytes)
	if err != nil {
		return nil, nil, err
	}
	return ws, &m, nil
}

// SourceUnit là tọa độ ổn định mà model có thể tham chiếu (RFC §7.3).
// ID chỉ được sử dụng để hiển thị và tham chiếu model; tất cả các phán đoán thứ tự/bao gồm/tăng dần phải tuân theo thứ tự số (Line, Part), cấm so sánh thứ tự từ điển với chuỗi ID.
type SourceUnit struct {
	ID        string `json:"id"`   // L1257; dòng vượt ngân sách tách thành L1257.1, L1257.2
	Line      int    `json:"line"` // Bắt đầu từ 1
	Part      int    `json:"part"` // 0=cả dòng; phân mảnh ảo 1..N
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Text      string `json:"text"`
}

// unitLess xác định thứ tự toàn phần của SourceUnit: Line trước Part sau, đều là so sánh số (Sửa đổi A1).
func unitLess(a, b SourceUnit) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Part < b.Part
}

// buildSourceUnits xây dựng bảng tọa độ ổn định từ văn bản được chuẩn hóa.
// Dòng bình thường là một unit; khi byte dòng đơn vượt quá maxUnitBytes, chỉ tạo nhiều unit ảo ở ranh giới ký tự UTF-8,
// không ghi lại source.txt, không chèn ngắt dòng mềm, không thay đổi bất kỳ ký tự nguồn nào (RFC §7.3). maxUnitBytes<=0 có nghĩa là không phân mảnh.
func buildSourceUnits(normalized []byte, maxUnitBytes int) []SourceUnit {
	var units []SourceUnit
	n := len(normalized)
	line := 0
	offset := 0
	for offset < n {
		nl := bytes.IndexByte(normalized[offset:], '\n')
		lineEnd := n
		if nl >= 0 {
			lineEnd = offset + nl
		}
		line++
		if maxUnitBytes > 0 && lineEnd-offset > maxUnitBytes {
			part := 0
			s := offset
			for s < lineEnd {
				e := s + maxUnitBytes
				if e >= lineEnd {
					e = lineEnd
				} else {
					for e > s && !utf8.RuneStart(normalized[e]) {
						e--
					}
					if e == s { // Biện pháp dự phòng khẩn cấp cho một rune đơn quá dài
						e = s + maxUnitBytes
					}
				}
				part++
				units = append(units, SourceUnit{
					ID: fmt.Sprintf("L%d.%d", line, part), Line: line, Part: part,
					StartByte: s, EndByte: e, Text: string(normalized[s:e]),
				})
				s = e
			}
		} else {
			units = append(units, SourceUnit{
				ID: fmt.Sprintf("L%d", line), Line: line, Part: 0,
				StartByte: offset, EndByte: lineEnd, Text: string(normalized[offset:lineEnd]),
			})
		}
		if nl < 0 {
			break
		}
		offset = lineEnd + 1
	}
	return units
}

// resolveBoundaryByte ánh xạ một quyết định ranh giới vào vị trí byte chính xác:
// Không có anchor sẽ lấy điểm bắt đầu unit; có anchor yêu cầu bắt trúng từng chữ duy nhất trong unit đó, rồi ánh xạ thành độ lệch byte (RFC §8.3).
func resolveBoundaryByte(unitByID map[string]SourceUnit, unitID, anchor string) (int, error) {
	u, ok := unitByID[unitID]
	if !ok {
		return 0, fmt.Errorf("Ranh giới tham chiếu unit không tồn tại: %s", unitID)
	}
	if anchor == "" {
		return u.StartByte, nil
	}
	switch strings.Count(u.Text, anchor) {
	case 0:
		return 0, fmt.Errorf("Điểm neo %q không nằm trong unit %s", anchor, unitID)
	case 1:
		return u.StartByte + strings.Index(u.Text, anchor), nil
	default:
		return 0, fmt.Errorf("Điểm neo %q không phải là duy nhất trong unit %s", anchor, unitID)
	}
}
