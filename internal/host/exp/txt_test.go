package exp

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestStripChapterTitleHeader(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		title string
		want  string
	}{
		{"plain body untouched", "他望着窗外。", "雨夜归人", "他望着窗外。"},
		{"strip h1 chinese title", "# 第 1 chương   雨夜归人\n\n他望着窗外。", "雨夜归人", "他望着窗外。"},
		{"strip h2 with chapter token", "## 第二chương \n\n他望着窗外。", "", "他望着窗外。"},
		{"keep body even if no header", "chính văn第一câu 。\n第二câu 。", "", "chính văn第一câu 。\n第二câu 。"},
		{"do not strip non-chapter heading", "# 序chương \n他望着窗外。", "边村浮生", "# 序chương \n他望着窗外。"},
		{"single line header only", "# 第 1 chương ", "", ""},
		// writer 把纯chương名当tiêu đề 写进首dòng  → 与xuất 器统一tiêu đề lặp lại ，应剥掉
		{"strip h1 matching chapter title", "# 边村浮生\n\n天还没亮。", "边村浮生", "天还没亮。"},
		// 首dòng  h1 但文chữ 不bằng 本chương tiêu đề  → 视为chính văn，giữ lại 
		{"keep h1 not matching title", "# 别的小tiêu đề \nchính văn。", "边村浮生", "# 别的小tiêu đề \nchính văn。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripChapterTitleHeader(c.in, c.title)
			if got != c.want {
				t.Fatalf("stripChapterTitleHeader\nin   = %q\ntitle= %q\nwant = %q\ngot  = %q", c.in, c.title, c.want, got)
			}
		})
	}
}

func TestBuildTitleIndex(t *testing.T) {
	outline := []domain.OutlineEntry{
		{Chapter: 1, Title: "雨夜归人"},
		{Chapter: 2, Title: ""}, // trốngtiêu đề 应被过滤
		{Chapter: 3, Title: "破晓"},
	}
	idx := buildTitleIndex(outline)
	if got := idx[1]; got != "雨夜归人" {
		t.Errorf("ch1 title: got %q want người về đêm mưa", got)
	}
	if _, ok := idx[2]; ok {
		t.Errorf("ch2 should be absent (empty title)")
	}
	if got := idx[3]; got != "破晓" {
		t.Errorf("ch3 title: got %q want bình minh", got)
	}
}

func TestBuildLocations(t *testing.T) {
	volumes := []domain.VolumeOutline{
		{Index: 1, Title: "起源", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "少年初登场", Chapters: []domain.OutlineEntry{{}, {}}}, // 2 chương 
			{Index: 2, Title: "宗门试炼", Chapters: []domain.OutlineEntry{{}}},      // 1 chương 
		}},
		{Index: 2, Title: "崛起", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "初战", Chapters: []domain.OutlineEntry{{}}},
		}},
	}
	locs := buildLocations(volumes)

	// 只验tập归属：arc 不再进 location，但arc 层仍参与toàn cục chương 号累加。
	if loc := locs[1]; !loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch1 should be first of volume 1: %+v", loc)
	}
	if loc := locs[2]; loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch2 should be volume 1, not first: %+v", loc)
	}
	// ch3 是arc  2 的首chương ，但仍在tập 1 内 → 不是tập首。
	if loc := locs[3]; loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch3 (arc 2, same volume) should not be first of volume: %+v", loc)
	}
	if loc := locs[4]; !loc.IsFirstOfVolume || loc.VolumeIdx != 2 {
		t.Errorf("ch4 should start volume 2: %+v", loc)
	}
}

func TestRenderTXT_TitleAndChapter(t *testing.T) {
	got := renderTXT(
		"光斑",
		[]int{1, 2},
		chapterTitleIndex{1: "雨夜归人", 2: "破晓"},
		nil,
		map[int]string{
			1: "# 第 1 chương  雨夜归人\n\n他望着窗外。",
			2: "她推开门。",
		},
	)
	if !strings.HasPrefix(got, "《光斑》\n\n") {
		t.Errorf("missing book title at start:\n%s", got)
	}
	// premise 不进xuất ：tên sách 后应直接是chương，不夹任何前情提要
	if !strings.Contains(got, "第 1 chương   雨夜归人") {
		t.Errorf("missing ch1 header")
	}
	if !strings.Contains(got, "他望着窗外。") {
		t.Errorf("missing ch1 body")
	}
	if strings.Contains(got, "# 第 1 chương ") {
		t.Errorf("body markdown header not stripped:\n%s", got)
	}
	if !strings.Contains(got, "第 2 chương   破晓") {
		t.Errorf("missing ch2 header")
	}
}

func TestRenderTXT_EmptyBookTitleNoTitleLine(t *testing.T) {
	got := renderTXT(
		"",
		[]int{1},
		chapterTitleIndex{1: "雨夜归人"},
		nil,
		map[int]string{1: "chính văn。"},
	)
	if strings.Contains(got, "《") {
		t.Errorf("should not contain book title brackets: %s", got)
	}
	if !strings.HasPrefix(got, "第 1 chương   雨夜归人") {
		t.Errorf("expect chapter header at very start: %s", got)
	}
}

// TestRenderTXT_LayeredVolume xác minh分层đại cương只在tập首插tập分隔，arc 分隔永不xuất hiện 
// （issue #27：版式定为"《tên sách 》→tập分隔→chươngchính văn"）。
func TestRenderTXT_LayeredVolume(t *testing.T) {
	locs := map[int]chapterLocation{
		1: {VolumeIdx: 1, VolumeTitle: "起源", IsFirstOfVolume: true},
		2: {VolumeIdx: 1, VolumeTitle: "起源"},
	}
	got := renderTXT(
		"X", []int{1, 2},
		chapterTitleIndex{1: "A", 2: "B"},
		locs,
		map[int]string{1: "chính văn一。", 2: "chính văn二。"},
	)
	if !strings.Contains(got, "第 1 tập  起源") {
		t.Errorf("missing volume header: %s", got)
	}
	if strings.Contains(got, "arc ") {
		t.Errorf("arc divider should never appear: %s", got)
	}
	// tậptiêu đề 只在第一chương 前xuất hiện 一次
	if strings.Count(got, "第 1 tập") != 1 {
		t.Errorf("volume header should appear exactly once: %s", got)
	}
}

func TestRenderTXT_ChapterWithoutTitleFallsBackToNumberOnly(t *testing.T) {
	got := renderTXT(
		"", []int{5},
		chapterTitleIndex{}, // 没有tiêu đề 
		nil,
		map[int]string{5: "chính văn。"},
	)
	if !strings.Contains(got, "第 5 chương \n\n") {
		t.Errorf("expect 'first 5 chương' fallback header: %s", got)
	}
}
