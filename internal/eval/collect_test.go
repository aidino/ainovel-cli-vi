package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestCollectReadsStyleUsageAndToolCalls(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	progress := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		TotalChapters:     5,
		CompletedChapters: []int{3, 1, 5, 2, 4},
	}
	if err := s.Progress.Save(progress); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chương 1 Gió nổi"},
		{Chapter: 2, Title: "Phá thế"},
		{Chapter: 3, Title: "Chương 3 Vào cục"},
		{Chapter: 4, Title: "Truy vấn"},
		{Chapter: 5, Title: "Chương 5 Tiếng vọng"},
	}); err != nil {
		t.Fatalf("save outline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "林墨", Aliases: []string{"药童"}}}); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	for ch := 1; ch <= 5; ch++ {
		text := "# tiêu đề \n\n不是风停了，而是所有人都屏住呼吸。沉默了片刻，林墨握紧药囊。"
		if err := s.Drafts.SaveFinalChapter(ch, text); err != nil {
			t.Fatalf("save chapter %d: %v", ch, err)
		}
	}
	if err := s.Usage.Save(domain.UsageState{
		Overall:      domain.AgentUsageTotals{Input: 100, Output: 40, Cost: 0.12},
		MissingUsage: 1,
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}
	writeSessionLine(t, dir, "meta/sessions/agents/writer-ch01.jsonl", agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{
			agentcore.ToolCallBlock(agentcore.ToolCall{Name: "dispatch"}),
			agentcore.ToolCallBlock(agentcore.ToolCall{Name: "novel_context"}),
		},
	})

	col := Collect(dir, nil)
	if len(col.LoadErrors) != 0 {
		t.Fatalf("不应有đọc lỗi : %v", col.LoadErrors)
	}
	if col.Style.Status != "ok" || col.Style.Stats == nil {
		t.Fatalf("stylestat 应可计算，nhận được  status=%s stats=%v", col.Style.Status, col.Style.Stats)
	}
	if col.Style.Stats.TitleFormats == nil {
		t.Fatal("tiêu đề 混用应被 stylestat 捕获")
	}
	if !col.Usage.UsageRecorded || col.Usage.Input != 100 || col.Usage.Output != 40 || col.Usage.CostUSD != 0.12 {
		t.Fatalf("usage đọc không chính xác : %+v", col.Usage)
	}
	if col.ToolCalls != 2 {
		t.Fatalf("tool calls = %d want 2", col.ToolCalls)
	}
}

func TestCollectStyleInsufficientSample(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "只有一chương "); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	col := Collect(dir, nil)
	if col.Style.Status != "insufficient_sample" {
		t.Fatalf("一chương 样本应 insufficient_sample，nhận được  %s", col.Style.Status)
	}
}

func TestCollectFailsLoudWhenCompletedChapterMissing(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	col := Collect(dir, nil)
	if !containsString(col.LoadErrors, "progress đánh dấu đã hoàn thành nhưng bản cuối trống") {
		t.Fatalf("缺终稿应进入 LoadErrors，thực tế  %v", col.LoadErrors)
	}
}

func TestChapterTitleUsesLayeredEntryChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1,
			Arcs: []domain.ArcOutline{
				{Index: 1}, // arc chưa mở rộng không nên làm trôi vị trí các chương tiếp theo
				{
					Index: 2,
					Chapters: []domain.OutlineEntry{
						{Chapter: 7, Title: "第七chương  真tiêu đề "},
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("save layered outline: %v", err)
	}

	got := chapterTitle(s, 7, "# chính văn兜底tiêu đề \n\nnội dung ", func(string, error) {})
	if got != "第七chương  真tiêu đề " {
		t.Fatalf("应按 entry.Chapter 匹配分层tiêu đề ，nhận được  %q", got)
	}
}

func TestChapterTitleUsesCommittedTitle(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "计划tiêu đề "}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "终稿tiêu đề "}); err != nil {
		t.Fatal(err)
	}
	got := chapterTitle(s, 1, "# chính văntiêu đề \n\nnội dung ", func(string, error) {})
	if got != "终稿tiêu đề " {
		t.Fatalf("chapterTitle = %q, want committed title", got)
	}
}

func containsString(items []string, sub string) bool {
	for _, item := range items {
		if strings.Contains(item, sub) {
			return true
		}
	}
	return false
}

func writeSessionLine(t *testing.T, root, rel string, msg agentcore.Message) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}