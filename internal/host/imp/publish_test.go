package imp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// spyCommitter 记录 Execute gọi số lần ，供发布幂等/khôi phục đường dẫnkiểm tra 。
type spyCommitter struct{ calls int }

func (s *spyCommitter) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	s.calls++
	return json.RawMessage(`{}`), nil
}

func TestCheckFoundationConflictsNormalizesBookMetadata(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "kiểm tra 书", Synopsis: "kiểm tra tóm tắt "}); err != nil {
		t.Fatal(err)
	}
	f := &Foundation{Book: domain.BookMetadata{Title: " kiểm tra 书 ", Synopsis: " kiểm tra tóm tắt  "}}
	if err := checkFoundationConflicts(st, f); err != nil {
		t.Fatalf("规范化后giống nhau 的作品thông tin 不应冲突: %v", err)
	}
}

// TestPublishChapterHandlesStalePendingCommit 守护发布đổ vỡ 窗口的khôi phục ：đổ vỡ 落在
// MarkChapterComplete 与 ClearPendingCommit 之间会残留指向本chương 的 pending_commit。
// 已完成chương 若直接bỏ qua 会绕开 commit công cụ的dọn dẹp 分支，下一chương  Execute 以 ErrToolConflict
// từ chối，nhập 每次重跑死在同一处——命中残留时phải 仍走一次công cụ幂等đường dẫn。
func TestPublishChapterHandlesStalePendingCommit(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 100, "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	f := ImportedChapterFacts{Chapter: 1, Summary: "s", CoreEvent: "c", HookType: "mystery", DominantStrand: "quest"}

	// 无残留：已完成chương 零成本bỏ qua ，不kích hoạt  commit。
	spy := &spyCommitter{}
	if err := publishChapter(context.Background(), st, spy, 1, "chính văn", f); err != nil {
		t.Fatalf("已完成chương 应幂等bỏ qua ：%v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("无残留不应gọi  commit，得 %d 次", spy.calls)
	}

	// 残留指向本chương ：phải 走一次 commit 幂等đường dẫn完成dọn dẹp 。
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1}); err != nil {
		t.Fatal(err)
	}
	if err := publishChapter(context.Background(), st, spy, 1, "chính văn", f); err != nil {
		t.Fatalf("残留dọn dẹp đường dẫn不应thất bại：%v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("命中残留应恰好gọi  commit 一次，得 %d 次", spy.calls)
	}
}
