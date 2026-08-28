package imp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ChapterCommitter là giao diện tối thiểu cần thiết để xuất bản chương, được tools.CommitChapterTool đáp ứng.
// Tái sử dụng saga PendingCommit, checkpoint và kiểm tra tính lũy đẳng của chương hoàn thành, không sao chép bộ logic gửi thứ hai (RFC §12.3).
type ChapterCommitter interface {
	Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// publishFoundation xuất bản Foundation theo trình tự phụ thuộc chính thức, thống nhất với trình tự ghi đĩa tiểu thuyết dài của Architect (RFC §12.2).
// Việc xuất bản lại cùng một nội dung là lũy đẳng (Store ghi đè cùng nội dung + checkpoint xóa trùng lặp).
func publishFoundation(st *store.Store, f *Foundation) error {
	// Đối soát xung đột trước khi xuất bản: công cụ chính thức đã tồn tại và khác biệt sẽ bị từ chối ghi đè (§12.2 / bất biến 6).
	// Cùng một nội dung sẽ được viết tiếp theo tính lũy đẳng (Store ghi đè cùng nội dung + checkpoint xóa trùng lặp).
	if err := checkFoundationConflicts(st, f); err != nil {
		return err
	}
	if err := st.Book.Save(f.Book); err != nil {
		return fmt.Errorf("book：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("checkpoint book：%w", err)
	}
	if err := st.RunMeta.SetPlanningTier(f.PlanningTier); err != nil {
		return fmt.Errorf("planning tier：%w", err)
	}
	// premise
	if err := st.Outline.SavePremise(f.Premise); err != nil {
		return fmt.Errorf("premise：%w", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhasePremise); err != nil {
		return fmt.Errorf("phase premise：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "premise", "premise.md"); err != nil {
		return fmt.Errorf("checkpoint premise：%w", err)
	}
	// characters
	if err := st.Characters.Save(f.Characters); err != nil {
		return fmt.Errorf("characters：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "characters", "characters.json"); err != nil {
		return fmt.Errorf("checkpoint characters：%w", err)
	}
	// world rules
	if err := st.World.SaveWorldRules(f.WorldRules); err != nil {
		return fmt.Errorf("world_rules：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "world_rules", "world_rules.json"); err != nil {
		return fmt.Errorf("checkpoint world_rules：%w", err)
	}
	// layered outline là nguồn duy nhất, Store đồng bộ hóa tái tạo flat outline.
	if err := st.Outline.SaveLayeredOutline(f.Volumes); err != nil {
		return fmt.Errorf("layered outline：%w", err)
	}
	// Tiến độ của giai đoạn đại cương là cơ sở để Engine tính toán lại định tuyến (dung lượng chương/phân tầng/tập arc hiện tại), ghi đĩa thất bại sẽ để lại trạng thái xuất bản
	// không nhất quán, phải phơi bày chứ không được nuốt trọn (RFC §12.2).
	if err := st.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		return fmt.Errorf("phase outline：%w", err)
	}
	if err := st.Progress.SetTotalChapters(domain.EstimatedChapterCapacity(f.Volumes)); err != nil {
		return fmt.Errorf("total chapters：%w", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		return fmt.Errorf("set layered：%w", err)
	}
	if len(f.Volumes) > 0 && len(f.Volumes[0].Arcs) > 0 {
		if err := st.Progress.UpdateVolumeArc(f.Volumes[0].Index, f.Volumes[0].Arcs[0].Index); err != nil {
			return fmt.Errorf("volume arc：%w", err)
		}
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "layered_outline", "layered_outline.json"); err != nil {
		return fmt.Errorf("checkpoint layered outline：%w", err)
	}
	// compass
	if err := st.Outline.SaveCompass(f.Compass); err != nil {
		return fmt.Errorf("compass：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "compass", "meta/compass.json"); err != nil {
		return fmt.Errorf("checkpoint compass：%w", err)
	}
	// Toàn bộ thao tác ghi chính thức khi nạp Foundation đều đã thành công, có thể hiển thị bước vào writing.
	// Không thể tái sử dụng FoundationMissing của quy trình sáng tác thông thường: việc nạp cho phép world_rules để trống,
	// việc coi "giá trị trống hợp pháp" là thiếu sót sẽ làm cho tiến độ mãi mãi dừng ở outline, sau đó StartChapter bị cổng bảo vệ giai đoạn từ chối.
	p, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress：%w", err)
	}
	if p == nil {
		return fmt.Errorf("load progress: progress chưa được khởi tạo")
	}
	if p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
		if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
			return fmt.Errorf("phase writing：%w", err)
		}
	}
	return nil
}

// checkFoundationConflicts kiểm tra tính nhất quán giữa Foundation chờ xuất bản và công cụ chính thức đã có:
// Đã có là rỗng thì coi là xuất bản lần đầu; giống nhau coi là lũy đẳng; khác biệt thì báo xung đột không ghi đè (RFC §12.2 / bất biến 6).
// compass và flat outline dẫn xuất từ layered outline, layered nhất quán tức là dẫn xuất nhất quán, vì vậy không kiểm tra riêng lẻ công cụ dẫn xuất.
// Không được nuốt lỗi đọc thành "file không tồn tại": loader của store trả về (giá trị không, nil) đối với trường hợp bị thiếu, do đó bất kỳ giá trị khác nil nào đều là lỗi thực sự
// (hỏng/quyền/JSON không hợp lệ), nếu tiếp tục coi như giá trị rỗng thì sẽ ghi đè lên các công cụ chính thức không thể đọc được (RFC §12.2).
func checkFoundationConflicts(st *store.Store, f *Foundation) error {
	wantBook := f.Book.Normalized()
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book != nil && !jsonEqual(book, wantBook) {
		return fmt.Errorf("book chính thức và tổng hợp nạp vào xung đột (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	cur, err := st.Outline.LoadPremise()
	if err != nil {
		return fmt.Errorf("Đọc premise chính thức: %w", err)
	}
	if cur != "" && cur != f.Premise {
		return fmt.Errorf("premise chính thức và tổng hợp nạp vào xung đột (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	chars, err := st.Characters.Load()
	if err != nil {
		return fmt.Errorf("Đọc characters chính thức: %w", err)
	}
	if len(chars) > 0 && !jsonEqual(chars, f.Characters) {
		return fmt.Errorf("characters chính thức và tổng hợp nạp vào xung đột (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return fmt.Errorf("Đọc world_rules chính thức: %w", err)
	}
	if len(rules) > 0 && !jsonEqual(rules, f.WorldRules) {
		return fmt.Errorf("world_rules chính thức và tổng hợp nạp vào xung đột (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("Đọc layered_outline chính thức: %w", err)
	}
	if len(layered) > 0 && !jsonEqual(layered, f.Volumes) {
		return fmt.Errorf("layered_outline chính thức và tổng hợp nạp vào xung đột (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	return nil
}

// jsonEqual so sánh hai giá trị có tương đương không bằng byte JSON được chuẩn hóa.
func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// publishChapter tái sử dụng commit_chapter để xuất bản chương đơn; chương đã hoàn thành bị kiểm tra lũy đẳng bỏ qua (RFC §12.3).
func publishChapter(ctx context.Context, st *store.Store, commit ChapterCommitter, chapter int, content string, f ImportedChapterFacts) error {
	completed, err := st.Progress.IsChapterCompleted(chapter)
	if err != nil {
		return fmt.Errorf("load progress ch%d：%w", chapter, err)
	}
	if completed {
		// Sự cố có thể rơi vào khoảng giữa MarkChapterComplete và ClearPendingCommit: tàn dư pending_commit
		// trỏ đến chương này. Việc trực tiếp bỏ qua sẽ bỏ qua nhánh dọn dẹp (bổ sung checkpoint + dọn tàn dư) mà công cụ commit chuẩn bị riêng cho cửa sổ này,
		// chương tiếp theo Execute sẽ từ chối bằng "tồn tại thao tác gửi chương chưa được khôi phục", mỗi lần chạy lại nạp vào đều chết tại một chỗ và
		// cần xóa thủ công meta/pending_commit.json mới có thể mở khóa. Khi chạm phải tàn dư, hãy vẫn đi theo đường dẫn lũy đẳng của công cụ để hoàn thành dọn dẹp.
		pending, err := st.Signals.LoadPendingCommit()
		if err != nil {
			return fmt.Errorf("load pending commit ch%d：%w", chapter, err)
		}
		if pending != nil && pending.Chapter == chapter {
			raw, err := json.Marshal(commitArgs(chapter, f))
			if err != nil {
				return fmt.Errorf("marshal commit ch%d：%w", chapter, err)
			}
			if _, err := commit.Execute(ctx, raw); err != nil {
				return fmt.Errorf("commit ch%d：%w", chapter, err)
			}
		}
		return nil
	}
	if err := st.Drafts.SaveDraft(chapter, content); err != nil {
		return fmt.Errorf("save draft ch%d：%w", chapter, err)
	}
	if err := st.Progress.StartChapter(chapter); err != nil {
		return fmt.Errorf("start ch%d：%w", chapter, err)
	}
	raw, err := json.Marshal(commitArgs(chapter, f))
	if err != nil {
		return fmt.Errorf("marshal commit ch%d：%w", chapter, err)
	}
	if _, err := commit.Execute(ctx, raw); err != nil {
		return fmt.Errorf("commit ch%d：%w", chapter, err)
	}
	return nil
}

// commitArgs ánh xạ sự thật từng chương vào tham số đầu vào của commit_chapter.
func commitArgs(chapter int, f ImportedChapterFacts) map[string]any {
	keyEvents := f.KeyEvents
	if len(keyEvents) == 0 {
		keyEvents = []string{f.CoreEvent} // core_event đã được xác minh không rỗng
	}
	args := map[string]any{
		"chapter":         chapter,
		"title":           f.Title,
		"summary":         f.Summary,
		"characters":      f.Characters,
		"key_events":      keyEvents,
		"hook_type":       f.HookType,
		"dominant_strand": f.DominantStrand,
	}
	if len(f.TimelineEvents) > 0 {
		args["timeline_events"] = f.TimelineEvents
	}
	if len(f.ForeshadowUpdates) > 0 {
		args["foreshadow_updates"] = f.ForeshadowUpdates
	}
	if len(f.RelationshipChanges) > 0 {
		args["relationship_changes"] = f.RelationshipChanges
	}
	if len(f.StateChanges) > 0 {
		args["state_changes"] = f.StateChanges
	}
	return args
}

// isPublished đánh giá xem trạng thái chính thức có phản ánh việc nạp hoàn chỉnh không: Foundation đã ghi vào đĩa và số chương đã hoàn thành đạt kỳ vọng.
// Chỉ đối soát những công cụ do việc nạp tạo ra thực sự——book, premise, flat outline phủ toàn bộ chương, chương hoàn thành——chứ không tái sử dụng
// FoundationMissing(): cái sau là cổng bảo vệ "có thể sáng tác" của quy trình sáng tác thông thường, sẽ đánh giá nhầm việc world_rules hợp pháp để trống
// thành không hoàn chỉnh, dẫn đến đối soát xuất bản mãi không hội tụ (RFC §12.3).
func isPublished(st *store.Store, expected int) (bool, error) {
	if expected == 0 {
		return false, nil
	}
	book, err := st.Book.Load()
	if err != nil {
		return false, fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book == nil {
		return false, nil
	}
	p, err := st.Outline.LoadPremise()
	if err != nil {
		return false, fmt.Errorf("đọc premise chính thức: %w", err)
	}
	if p == "" {
		return false, nil
	}
	o, err := st.Outline.LoadOutline()
	if err != nil {
		return false, fmt.Errorf("đọc outline chính thức: %w", err)
	}
	if len(o) < expected {
		return false, nil
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("đọc progress chính thức: %w", err)
	}
	return prog != nil && len(prog.CompletedChapters) >= expected, nil
}
