package imp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

// TestDiscardAnalysesAfter 守护 #4a：dọn dẹp 越过新鲜tiền tố 的旧分析工件，
// 保证"重分析某chương 即失效其后全部分析"，防止陈旧 ledger 随后续chương被复用。
func TestDiscardAnalysesAfter(t *testing.T) {
	ws := OpenWorkspace(t.TempDir())
	for c := 1; c <= 5; c++ {
		if err := writeArtifact(ws, analysisPath(c), "d", ChapterAnalysisPayload{Facts: ImportedChapterFacts{Chapter: c}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := discardAnalysesAfter(ws, 2, 5); err != nil {
		t.Fatalf("dọn dẹp 不应thất bại：%v", err)
	}
	for c := 1; c <= 2; c++ {
		if !ws.has(analysisPath(c)) {
			t.Fatalf("新鲜tiền tố chương  %d 应giữ lại ", c)
		}
	}
	for c := 3; c <= 5; c++ {
		if ws.has(analysisPath(c)) {
			t.Fatalf("越过新鲜tiền tố 的chương  %d 应被dọn dẹp ", c)
		}
	}
}

// analyzeFixture 构造一份含 n chương 、chính văn都很短的切分，用于批次/分析kiểm tra 。
func analyzeFixture(t *testing.T, n int) ([]byte, *Segmentation) {
	t.Helper()
	var b strings.Builder
	for c := 1; c <= n; c++ {
		b.WriteString("第")
		b.WriteString(strings.Repeat("一", 1))
		b.WriteString("chương \nchính văn\n")
	}
	norm := []byte(b.String())
	units := buildSourceUnits(norm, 0)
	var ds []BoundaryDecision
	for i := 0; i < len(units); i += 2 { // 每 2 dòng 一chương （tiêu đề dòng  + chính văndòng ）
		ds = append(ds, BoundaryDecision{UnitID: units[i].ID, Kind: kindChapter, Title: units[i].Text})
	}
	seg, err := resolveSegmentation(norm, units, ds)
	if err != nil {
		t.Fatalf("fixture 切分thất bại：%v", err)
	}
	if len(seg.Chapters) != n {
		t.Fatalf("fixture chương 数 %d != %d", len(seg.Chapters), n)
	}
	return norm, seg
}

func TestPlanBatchOutputBudgetCaps(t *testing.T) {
	_, seg := analyzeFixture(t, 10)
	// 输入宽松，但可见输出ngân sách 只够 2 chương （#83 批次粒度守卫，§20.4.2）。
	b := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 250, PerChapterOutput: 100, PromptOverhead: 0}
	end := planBatch(seg.Chapters, 0, 0, b)
	if end != 2 {
		t.Fatalf("输出ngân sách 应把批次限到 2 chương ，得 end=%d", end)
	}
}

func TestPlanBatchInputBudgetCaps(t *testing.T) {
	_, seg := analyzeFixture(t, 10)
	// 输出宽松，但输入chữ 节ngân sách 只够约 1 chương 。
	one := chapterBytes(seg.Chapters, 0)
	b := AnalyzeBudget{ContextBytes: one + 1, MaxOutputTokens: 1 << 20, PerChapterOutput: 1, PromptOverhead: 0}
	end := planBatch(seg.Chapters, 0, 0, b)
	if end != 1 {
		t.Fatalf("输入ngân sách 应把批次限到 1 chương ，得 end=%d", end)
	}
}

func factsJSON(chapter int, title string) string {
	f := map[string]any{
		"chapter": chapter, "title": title, "summary": "摘要", "core_event": "核心事件",
		"key_events": []string{"事件"}, "hook": nil, "scenes": []string{}, "characters": []string{},
		"character_evidence": []any{}, "world_evidence": []any{}, "timeline_events": []any{},
		"foreshadow_updates": []any{}, "relationship_changes": []any{}, "state_changes": []any{},
		"hook_type": "mystery", "dominant_strand": "quest",
	}
	data, _ := json.Marshal(f)
	return string(data)
}

func TestValidateBatchRejections(t *testing.T) {
	_, seg := analyzeFixture(t, 2)
	// 数量không khớp 
	bad := &AnalysisBatchResult{Chapters: []ImportedChapterFacts{{Chapter: 1}}}
	if err := validateBatch(bad, seg, 0, 2); err == nil {
		t.Fatal("数量không khớp 应từ chối")
	}
	// hook_type 非法
	var f ImportedChapterFacts
	_ = json.Unmarshal([]byte(factsJSON(1, seg.Chapters[0].Title)), &f)
	f.HookType = "bogus"
	if err := validateBatch(&AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}, seg, 0, 1); err == nil {
		t.Fatal("非法 hook_type 应từ chối")
	}
	// 枚举大小写变体：校验通过并就地归一化为小写——commit_chapter 不复验枚举，
	// 变体直通正式trạng thái会被精确串消费的逻辑视为chưa biết 类型。
	_ = json.Unmarshal([]byte(factsJSON(1, seg.Chapters[0].Title)), &f)
	f.HookType, f.DominantStrand = "Crisis", "QUEST"
	got := &AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}
	if err := validateBatch(got, seg, 0, 1); err != nil {
		t.Fatalf("大小写变体应通过校验：%v", err)
	}
	if got.Chapters[0].HookType != "crisis" || got.Chapters[0].DominantStrand != "quest" {
		t.Fatalf("枚举应归一化为小写落盘：%+v", got.Chapters[0])
	}
}

func TestAnalyzeNextPersistsWithRebatchOnTruncation(t *testing.T) {
	norm, seg := analyzeFixture(t, 2)
	book := t.TempDir()
	ws := &Workspace{dir: book}
	// 首批 2 chương 截断：第 1 chương 完整、第 2 chương 半截 → 打捞第 1 chương liên tục tiền tố （§9.5）。
	truncated := `{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,{"chapter":2,"summary":"截断`
	m := &mockModel{
		responses: []string{truncated},
		stops:     []agentcore.StopReason{agentcore.StopReasonLength},
	}
	budget := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 1000, PerChapterOutput: 10, PromptOverhead: 0}
	done, err := AnalyzeNext(context.Background(), m, "sys", ws, norm, seg, "segid", "v1", budget, callProfile{})
	if err != nil {
		t.Fatalf("AnalyzeNext: %v", err)
	}
	if done != 1 {
		t.Fatalf("截断应打捞第 1 chương liên tục tiền tố ，得 %d", done)
	}
	if !ws.has(analysisPath(1)) || ws.has(analysisPath(2)) {
		t.Fatal("应只落盘第 1 chương ")
	}
	if analyzedChapters(ws, seg, norm, "segid", "v1") != 1 {
		t.Fatal("已分析chương 数nên là 1")
	}
	// failures/ 应lưu 原始响应与打捞trạng thái（§14.2）。
	if !ws.has("failures/last-response.txt") || !ws.has("failures/last.json") {
		t.Fatal("应lưu thất bại原始响应与元数据")
	}
}

func TestSalvagePrefixContiguous(t *testing.T) {
	_, seg := analyzeFixture(t, 3)
	// 前 2 chương 完整，第 3 chương 被截断。
	raw := `{"chapters":[` +
		factsJSON(1, seg.Chapters[0].Title) + `,` +
		factsJSON(2, seg.Chapters[1].Title) + `,` +
		`{"chapter":3,"summary":"截断`
	got := salvagePrefix(raw, seg, 0)
	if len(got) != 2 {
		t.Fatalf("应打捞前 2 chương liên tục tiền tố ，得 %d", len(got))
	}
	if got[0].Chapter != 1 || got[1].Chapter != 2 {
		t.Fatal("tiền tố chương 号不liên tục ")
	}
}

func TestSalvagePrefixStopsAtGap(t *testing.T) {
	_, seg := analyzeFixture(t, 3)
	// 第 1 chương 后直接跳到第 3 chương  → 打捞在跳号处dừng ，只trả về 第 1 chương 。
	raw := `{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,` + factsJSON(3, seg.Chapters[2].Title) + `]}`
	got := salvagePrefix(raw, seg, 0)
	if len(got) != 1 {
		t.Fatalf("跳号处应dừng ，得 %d", len(got))
	}
}

// TestAnalyzedChaptersInvalidatesOnUpstreamChange xác minh切分身份或 prompt phiên bản 变化使已落盘分析失效（不变量 1）。
// 这是 InputDigest 机制真正落地的核心：改上游即失效下游，而非只看tệp是否存在。
func TestAnalyzedChaptersInvalidatesOnUpstreamChange(t *testing.T) {
	norm, seg := analyzeFixture(t, 2)
	ws := &Workspace{dir: t.TempDir()}
	m := &mockModel{responses: []string{
		`{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,` + factsJSON(2, seg.Chapters[1].Title) + `]}`,
	}}
	budget := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 1 << 20, PerChapterOutput: 10, PromptOverhead: 0}
	if _, err := AnalyzeNext(context.Background(), m, "sys", ws, norm, seg, "segid-A", "v1", budget, callProfile{}); err != nil {
		t.Fatalf("AnalyzeNext: %v", err)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-A", "v1"); got != 2 {
		t.Fatalf("同身份/phiên bản 应认 2 chương ，得 %d", got)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-B", "v1"); got != 0 {
		t.Fatalf("切分身份变化应使分析全部失效，得 %d", got)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-A", "v2"); got != 0 {
		t.Fatalf("prompt phiên bản 变化应使分析全部失效，得 %d", got)
	}
}
