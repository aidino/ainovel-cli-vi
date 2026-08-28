package imp

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeSourceUTF8(t *testing.T) {
	d, err := decodeSource([]byte("第一chương 　风起\nchính văn"))
	if err != nil {
		t.Fatalf("utf-8: %v", err)
	}
	if d.encoding != encodingUTF8 || d.text != "第一chương 　风起\nchính văn" {
		t.Fatalf("utf-8 kết quả không khớp ：%+v", d)
	}
}

func TestDecodeSourceUTF8BOM(t *testing.T) {
	raw := append(append([]byte{}, utf8BOM...), []byte("楔子")...)
	d, err := decodeSource(raw)
	if err != nil {
		t.Fatalf("bom: %v", err)
	}
	if d.encoding != encodingUTF8BOM || d.text != "楔子" {
		t.Fatalf("bom kết quả không khớp ：%+v", d)
	}
}

func TestDecodeSourceGB18030(t *testing.T) {
	want := "第一chương 　风起\nchính vănnội dung "
	gb, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatalf("编码 GB18030 kiểm tra 数据thất bại：%v", err)
	}
	d, err := decodeSource(gb)
	if err != nil {
		t.Fatalf("gb18030: %v", err)
	}
	if d.encoding != encodingGB18030 || d.text != want {
		t.Fatalf("gb18030 kết quả không khớp ：%+v", d)
	}
}

func TestDecodeSourceBOMInvalidBodyFails(t *testing.T) {
	raw := append(append([]byte{}, utf8BOM...), []byte{0xFF, 0xFE}...)
	if _, err := decodeSource(raw); err == nil {
		t.Fatal("声明 BOM 但chính văn非法应thất bại")
	}
}

func TestNormalizeLineEndings(t *testing.T) {
	if got := normalize("a\r\nb\rc\nd"); got != "a\nb\nc\nd" {
		t.Fatalf("归一化không khớp ：%q", got)
	}
	// trốngdòng 与缩进phải giữ lại 。
	if got := normalize("第一chương \r\n\r\n\tchính văn"); got != "第一chương \n\n\tchính văn" {
		t.Fatalf("空dòng /缩进未giữ lại ：%q", got)
	}
}
