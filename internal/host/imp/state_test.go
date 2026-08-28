package imp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func mustLoadState(t *testing.T, w *Workspace) Facts {
	t.Helper()
	f, err := LoadState(w)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return f
}

func TestNextActionChain(t *testing.T) {
	cases := []struct {
		name string
		f    Facts
		want Action
	}{
		{"空", Facts{}, ActionIngest},
		{"已建区待切分", Facts{WorkspaceReady: true}, ActionSegment},
		{"已切分待确认", Facts{WorkspaceReady: true, Segmented: true}, ActionAwaitConfirmation},
		{"已确认待分析", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3}, ActionAnalyze},
		{"分析未满", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 2}, ActionAnalyze},
		{"分析齐待综合", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3}, ActionSynthesize},
		{"综合后 uncertain 待phán quyết ", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true}, ActionAwaitStoryResolution},
		{"uncertain 已phán quyết 待发布", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true, StoryResolved: true}, ActionPublish},
		{"明确trạng thái待发布", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true}, ActionPublish},
		{"全部nhất quán ", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, Published: true}, ActionDone},
		{"发布终态短路上游失鲜", Facts{Published: true}, ActionDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextAction(c.f)
			if got != c.want {
				t.Fatalf("NextAction=%s want=%s", got, c.want)
			}
			// 对同一事实快照恒定。
			if NextAction(c.f) != got {
				t.Fatal("NextAction 对同一 Facts 不恒定")
			}
		})
	}
}

func TestLoadStateReflectsWorkspace(t *testing.T) {
	book := t.TempDir()
	// 未建区：非活动 → ingest。
	w := OpenWorkspace(book)
	if NextAction(mustLoadState(t, w)) != ActionIngest {
		t.Fatal("空书应先 ingest")
	}
	// 建区后：workspace ready、未切分 → segment。
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一chương \nchính văn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	f := mustLoadState(t, ws)
	if !f.WorkspaceReady || f.Segmented {
		t.Fatalf("建区后事实không khớp ：%+v", f)
	}
	if NextAction(f) != ActionSegment {
		t.Fatal("建区后应 segment")
	}
}

func TestLoadStateReportsCorruptArtifact(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一chương \nchính văn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.writeAtomic(fileSegmentation, []byte("{")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(ws); err == nil || !strings.Contains(err.Error(), "công kiện cắt") {
		t.Fatalf("损坏工件不得伪装成尚未切分: %v", err)
	}
}

func TestIngestSnapshotConsistent(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	content := "第一chương \r\nchính văn一\r\n\r\n第二chương \r\nchính văn二"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, m, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if m.Encoding != encodingUTF8 || m.SourceName != "book.txt" {
		t.Fatalf("manifest không khớp ：%+v", m)
	}
	snap, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	// 源快照phải 已归一化，và摘要与 manifest nhất quán 。
	if string(snap) != "第一chương \nchính văn一\n\n第二chương \nchính văn二" {
		t.Fatalf("源快照未归一化：%q", snap)
	}
	if Digest(snap) != m.NormalizedSHA256 {
		t.Fatal("源快照摘要与 manifest không nhất quán")
	}
}

// TestGuidanceChangeInvalidatesSegmentation 守护 §18.3：切分指导是 segmentation 的语义输入，
// 指导变化使旧切分（及其全部下游）自然失配重做，不需要手工失效quy tắc 。
func TestGuidanceChangeInvalidatesSegmentation(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一chương \nchính văn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一chương ", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	if !mustLoadState(t, ws).Segmented {
		t.Fatal("无指导时切分应有效")
	}
	if err := ws.writeAtomic(fileGuidance, []byte("幕间也是独立chương")); err != nil {
		t.Fatal(err)
	}
	if mustLoadState(t, ws).Segmented {
		t.Fatal("指导变化后旧切分应失效（需重识别）")
	}
}

// TestResumeSummary 守护 §18.2 khởi động nhắc nhở ：无工作区trả về 空串；停在半路时给出giai đoạn化mô tả ，
// 使người dùng 不必等到创作被门禁từ chối才发现这本书停在nhập 半路。
func TestResumeSummary(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("无nhập 工作区nên trả về空串，得 %q", got)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("第一chương \nchính văn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "Chưa hoàn thành cắt") {
		t.Fatalf("刚建区应nhắc nhở 未完成切分，得 %q", got)
	}
	// 切分+确认就绪、分析 0/1 → nhắc nhở 分析进度。
	norm, _ := ws.LoadSource()
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一chương ", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	raw, _ := ws.readBytes(fileSegmentation)
	if err := writeArtifact(ws, fileConfirmation, Digest(raw), Confirmation{Method: confirmMethodAuto, Chapters: 1}); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "Đã phân tích 0/1") {
		t.Fatalf("应nhắc nhở 分析进度，得 %q", got)
	}
}

// TestResumeStatusPublishedIsTerminal 守护发布终态（实测事故）：书已全量发布后，
// segmentPromptVersion nâng cấp 使工作区công kiện cắt失鲜，ResumeStatus 不得据此把书判回
// "nhập 半路"——否则 startEngine 跨重启门禁会永久拒启已发布书的续写。
func TestResumeStatusPublishedIsTerminal(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("第一chương \nchính văn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, _ := ws.LoadSource()
	// 用旧phiên bản 号写切分：mô phỏng发布后 prompt nâng cấp 导致的 digest 失配。
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一chương ", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", "seg-v0"), seg); err != nil {
		t.Fatal(err)
	}
	// 未发布 + 切分失鲜：仍是半路nhập ，门禁应拦。
	if active, done, err := ResumeStatus(st); err != nil || !active || done {
		t.Fatalf("未发布的失鲜工作区应判未完成（active=%v done=%v）", active, done)
	}
	// 正式库已按该切分全量落库 → 发布对账通过，终态不受上游失鲜影响。
	if err := st.Book.Save(domain.BookMetadata{Title: "kiểm tra 书", Synopsis: "kiểm tra tóm tắt "}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("前提"); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "第一chương "}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatal(err)
	}
	if active, done, err := ResumeStatus(st); err != nil || !active || !done {
		t.Fatalf("已发布书应判nhập 完成（active=%v done=%v）", active, done)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("已发布书不应nhắc nhở 未完成nhập ，得 %q", got)
	}
}

func TestImportPreconditions(t *testing.T) {
	// trống书通过。
	empty := store.NewStore(t.TempDir())
	if err := checkImportPreconditions(empty); err != nil {
		t.Fatalf("空书应通过前置校验：%v", err)
	}
	// 有完成chương被拒。
	nonEmpty := store.NewStore(t.TempDir())
	if err := nonEmpty.Progress.Save(&domain.Progress{CompletedChapters: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(nonEmpty); err == nil {
		t.Fatal("非空书nên bị từ chối绝nhập ")
	}
	withBook := store.NewStore(t.TempDir())
	if err := withBook.Book.Save(domain.BookMetadata{Title: "đã có 作品", Synopsis: "đã có tóm tắt "}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(withBook); err == nil {
		t.Fatal("đã có 作品thông tin 时nên bị từ chối绝nhập ")
	}
}
