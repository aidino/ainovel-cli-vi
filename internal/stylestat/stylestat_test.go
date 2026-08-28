package stylestat

import (
	"strings"
	"testing"
)

func chapterWith(body string) string {
	return "# Tiêu đề\n" + body
}

func TestComputeBelowMinChapters(t *testing.T) {
	in := Input{Chapters: []string{"a", "b", "c", "d"}}
	if Compute(in) != nil {
		t.Fatal("below minChapters should return nil")
	}
}

func TestComputePatterns(t *testing.T) {
	body := "Anh không phải đang giận, mà là đang sợ. Hắn im lặng mấy nhịp hơi. Như một ngọn đèn. Ánh mắt nàng lóe lên hoảng loạn, tim thắt lại. Cảm thấy một cảm giác khó tả lạnh buốt.\nPhần thân.\n"
	chapters := make([]string, 6)
	for i := range chapters {
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	want := map[string]int{
		"Câu chỉnh đính 'không phải… mà là…'":                          6,
		"Từ nhịp thời gian 'X nhịp hơi'":                               6,
		"Ẩn dụ mở 'như thể/tựa như/như một'":                            6,
		"Nhịp im lặng 'im lặng/không nói gì'":                           6,
		"Khuôn mẫu thần thái 'lóe lên/khóe môi nhếch'":                  6,
		"Phản ứng thân thể 'tim thắt/người run/rùng mình'":             6,
		"Dấu hiệu tư duy 'trong đầu nghĩ/cảm thấy/ý thức được'":        6,
		"Khuôn sáo trừu tượng 'một cảm giác khó tả/điều quan trọng là'": 6,
	}
	for _, p := range s.Patterns {
		if w, ok := want[p.Name]; ok && p.Total != w {
			t.Errorf("%s total: got %d want %d", p.Name, p.Total, w)
		}
		if p.PerChapter != 1.0 {
			t.Errorf("%s per_chapter: got %v want 1.0", p.Name, p.PerChapter)
		}
	}
	if len(s.Patterns) != len(want) {
		t.Errorf("want %d pattern classes, got %d: %+v", len(want), len(s.Patterns), s.Patterns)
	}
}

func TestComputeTopPhrasesWithStopwords(t *testing.T) {
	// "đỉnh Thanh Vân" xuất hiện tần suất cao; "Lục Cửu Uyên" là tên nhân vật, phải bị lọc
	line := "Mọi người hướng mắt về đỉnh Thanh Vân, Lục Cửu Uyên khoanh tay đứng.\n"
	chapters := make([]string, 10)
	for i := range chapters {
		chapters[i] = chapterWith(strings.Repeat(line, 3))
	}
	s := Compute(Input{Chapters: chapters, Stopwords: []string{"Lục Cửu Uyên"}})
	if s == nil {
		t.Fatal("expected stats")
	}
	var hasMountain, hasName bool
	for _, p := range s.TopPhrases {
		if strings.Contains(p.Text, "thanh vân") {
			hasMountain = true
		}
		if strings.Contains(p.Text, "cửu uyên") || strings.Contains(p.Text, "lục cửu") {
			hasName = true
		}
	}
	if !hasMountain {
		t.Errorf("expected 'thanh vân' phrase mined, got %+v", s.TopPhrases)
	}
	if hasName {
		t.Errorf("character name should be filtered, got %+v", s.TopPhrases)
	}
}

func TestComputeRepeatedSentences(t *testing.T) {
	motto := "Kiếp này chẳng thể đi xa, mong thay ta ngắm núi sông phương ấy."
	chapters := make([]string, 6)
	for i := range chapters {
		body := "Thân chương, không lặp.\n"
		if i%2 == 0 {
			body += motto + "\n"
		}
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if len(s.RepeatedSentences) == 0 {
		t.Fatalf("expected repeated sentence, got none")
	}
	got := s.RepeatedSentences[0]
	if got.Chapters != 3 || got.Count != 3 {
		t.Errorf("repeated sentence: %+v", got)
	}
	if !strings.HasPrefix(got.Text, "Kiếp này chẳng thể đi xa") {
		t.Errorf("text: %q", got.Text)
	}
}

func TestComputeEndingAndOpening(t *testing.T) {
	short := chapterWith("Suốt đêm không ngủ.\nPhần thân rất dài rất dài.\nAnh ấy bước đi.")
	long := chapterWith("Chuyện ban ngày.\nPhần thân.\nĐây là một câu kết cực kỳ cực kỳ dài, dài vượt xa ngưỡng sáu mươi ký tự đã đặt ra, dùng để kiểm tra trung vị nhé.")
	chapters := []string{short, short, short, long, long}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if s.Ending.ShortRatio != 0.6 {
		t.Errorf("short_ratio: got %v want 0.6", s.Ending.ShortRatio)
	}
	if s.OpeningTimeRate != 0.6 {
		t.Errorf("opening_time_rate: got %v want 0.6", s.OpeningTimeRate)
	}
}

func TestComputeTitleFormats(t *testing.T) {
	chapters := make([]string, 5)
	for i := range chapters {
		chapters[i] = chapterWith("Phần thân.")
	}
	// Trộn lẫn → lên báo
	s := Compute(Input{Chapters: chapters, Titles: []string{"Chương 1 Gió nổi", "Mây dồn", "Chương 3 Sấm động"}})
	if s.TitleFormats == nil || s.TitleFormats.WithPrefix != 2 || s.TitleFormats.WithoutPrefix != 1 {
		t.Errorf("title formats: %+v", s.TitleFormats)
	}
	// Thống nhất → không lên báo
	s = Compute(Input{Chapters: chapters, Titles: []string{"Gió nổi", "Mây dồn"}})
	if s.TitleFormats != nil {
		t.Errorf("uniform titles should not report: %+v", s.TitleFormats)
	}
}
