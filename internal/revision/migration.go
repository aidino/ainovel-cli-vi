package revision

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// MigrateLegacyBaseline từ bản chiếu nghiệp vụ phiên bản cũ sinh ngược bản ghi chương. Nhật ký hội thoại không phải sự kiện tác phẩm,
// nên không tham gia di chuyển. Trước khi lưu sẽ chiếu thuận một lần so với trạng thái gốc, có sai biệt chỉ ghi log: nâng cấp không thay đổi
// trạng thái thế giới trên đĩa, cũng không vì thế mà chặn người dùng.
func MigrateLegacyBaseline(st *store.Store) error {
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("đọc tiến độ: %w", err)
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}

	chapters := slices.Clone(progress.CompletedChapters)
	slices.Sort(chapters)
	existing := make([]domain.ChapterRecord, 0, len(chapters))
	missing := make(map[int]bool)
	for _, chapter := range chapters {
		record, err := st.ChapterRecords.Load(chapter)
		if err != nil {
			return fmt.Errorf("đọc bản ghi nghiệm thu chương %d: %w", chapter, err)
		}
		if record != nil {
			existing = append(existing, *record)
			continue
		}
		missing[chapter] = true
	}
	if len(missing) == 0 {
		return ValidateRecords(existing)
	}

	previous, err := loadLegacyProjection(st, chapters, progress)
	if err != nil {
		return err
	}
	pending := make(map[int]*domain.ChapterRecord, len(missing))
	for _, summary := range previous.summaries {
		if !missing[summary.Chapter] {
			continue
		}
		record, err := buildLegacyRecord(st, summary)
		if err != nil {
			return err
		}
		pending[summary.Chapter] = &record
	}

	restoreLegacyFacts(existing, pending, chapters, &previous)
	records := slices.Clone(existing)
	for _, chapter := range chapters {
		if record := pending[chapter]; record != nil {
			records = append(records, *record)
		}
	}
	projected, err := NewProjector(st).build(records)
	if err != nil {
		return fmt.Errorf("sự kiện chương phiên bản cũ không thể phát lại: %w", err)
	}
	if err := compareProjection(previous, projected); err != nil {
		slog.Warn("kết quả tái tạo dữ liệu cũ có sai biệt với trạng thái gốc, lần tái tạo thế giới tiếp theo sẽ lấy bản ghi làm chuẩn", "module", "migration", "err", err)
	}
	for _, chapter := range chapters {
		if record := pending[chapter]; record != nil {
			if err := st.ChapterRecords.Save(*record); err != nil {
				return fmt.Errorf("lưu baseline sửa đổi chương %d: %w", chapter, err)
			}
		}
	}
	return nil
}

func loadLegacyProjection(st *store.Store, chapters []int, progress *domain.Progress) (projection, error) {
	result := projection{
		hookHistory: progress.HookHistory, strandHistory: progress.StrandHistory,
	}
	for _, chapter := range chapters {
		summary, err := st.Summaries.LoadSummary(chapter)
		if err != nil {
			return result, fmt.Errorf("đọc tóm tắt chương %d: %w", chapter, err)
		}
		if summary == nil {
			slog.Warn("chương cũ thiếu tóm tắt, thiết lập baseline bằng sự kiện rỗng", "module", "migration", "chapter", chapter)
			summary = &domain.ChapterSummary{}
		}
		summary.Chapter = chapter
		result.summaries = append(result.summaries, *summary)
	}
	var err error
	if result.timeline, err = st.World.LoadTimeline(); err != nil {
		return result, fmt.Errorf("đọc dòng thời gian: %w", err)
	}
	if result.foreshadow, err = st.World.LoadForeshadowLedger(); err != nil {
		return result, fmt.Errorf("đọc sổ chi tiết gieo mầm: %w", err)
	}
	if result.relationships, err = st.World.LoadRelationships(); err != nil {
		return result, fmt.Errorf("đọc quan hệ nhân vật: %w", err)
	}
	if result.stateChanges, err = st.World.LoadStateChanges(); err != nil {
		return result, fmt.Errorf("đọc thay đổi trạng thái: %w", err)
	}
	if result.cast, err = st.Cast.Load(); err != nil {
		return result, fmt.Errorf("đọc danh sách nhân vật phụ: %w", err)
	}
	style, err := st.World.LoadAuthorRevisionStyle()
	if err != nil {
		return result, fmt.Errorf("đọc phong cách chỉnh sửa người dùng: %w", err)
	}
	if style != nil {
		result.style = *style
	}
	result.normalizeLegacy(chapters)
	return result, nil
}

// normalizeLegacy loại bỏ các mục trong dữ liệu cũ không thuộc baseline: tham chiếu chương chưa hoàn thành do crash giữa chừng commit,
// ID rỗng và gieo trùng lặp mà phiên bản cũ cho phép. Chúng không ảnh hưởng viết, commit đợi khôi phục sẽ ghi lại sau di chuyển.
func (p *projection) normalizeLegacy(chapters []int) {
	completed := func(chapter int) bool {
		_, ok := slices.BinarySearch(chapters, chapter)
		return ok
	}
	before := len(p.timeline) + len(p.stateChanges) + len(p.relationships) + len(p.foreshadow)
	p.timeline = slices.DeleteFunc(p.timeline, func(e domain.TimelineEvent) bool { return !completed(e.Chapter) })
	p.stateChanges = slices.DeleteFunc(p.stateChanges, func(c domain.StateChange) bool { return !completed(c.Chapter) })
	p.relationships = slices.DeleteFunc(p.relationships, func(r domain.RelationshipEntry) bool { return !completed(r.Chapter) })

	var ledger []domain.ForeshadowEntry
	index := make(map[string]int)
	for _, entry := range p.foreshadow {
		if entry.ID == "" || !completed(entry.PlantedAt) {
			continue
		}
		if entry.Status == "resolved" && !completed(entry.ResolvedAt) {
			entry.Status, entry.ResolvedAt = "advanced", 0
		}
		// gieo trùng lặp phiên bản cũ sẽ thêm mục cùng ID, mục sau mới là trạng thái hoạt động thời điểm đó.
		if i, seen := index[entry.ID]; seen {
			ledger[i] = entry
			continue
		}
		index[entry.ID] = len(ledger)
		ledger = append(ledger, entry)
	}
	p.foreshadow = ledger
	if after := len(p.timeline) + len(p.stateChanges) + len(p.relationships) + len(p.foreshadow); after < before {
		slog.Warn("bỏ qua các mục dữ liệu cũ không thuộc baseline", "module", "migration", "count", before-after)
	}
}

func buildLegacyRecord(st *store.Store, summary domain.ChapterSummary) (domain.ChapterRecord, error) {
	chapter := summary.Chapter
	final, err := st.Drafts.LoadChapterText(chapter)
	if err != nil {
		return domain.ChapterRecord{}, fmt.Errorf("đọc nội dung chương %d: %w", chapter, err)
	}
	draft, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return domain.ChapterRecord{}, fmt.Errorf("đọc bản thảo lịch sử chương %d: %w", chapter, err)
	}
	hasFinal, hasDraft := strings.TrimSpace(final) != "", strings.TrimSpace(draft) != ""
	content := draft
	switch {
	case !hasFinal && !hasDraft:
		content = ""
		slog.Warn("cả nội dung lẫn bản thảo chương cũ đều thiếu, thiết lập baseline bằng nội dung rỗng, vui lòng dùng viết lại để tái tạo", "module", "migration", "chapter", chapter)
	case !hasDraft:
		content = final
		slog.Info("chương cũ không có bản thảo lịch sử, thiết lập baseline legacy bằng nội dung hiện tại", "module", "migration", "chapter", chapter)
	case !hasFinal:
		// bản thảo chính là nội dung đã commit lúc đó, ghi lại là khôi phục xác định.
		if err := st.Drafts.SaveFinalChapter(chapter, draft); err != nil {
			return domain.ChapterRecord{}, fmt.Errorf("khôi phục nội dung chương %d: %w", chapter, err)
		}
		slog.Warn("nội dung chương cũ thiếu, đã khôi phục từ bản thảo", "module", "migration", "chapter", chapter)
	case domain.NormalizeChapterContent(draft) != domain.NormalizeChapterContent(final):
		slog.Info("nội dung chương cũ có chỉnh sửa bên ngoài, sau di chuyển cần chạy /sync", "module", "migration", "chapter", chapter)
	}
	content = domain.NormalizeChapterContent(content)

	return domain.ChapterRecord{
		Version: domain.ChapterRecordVersion, Chapter: chapter, Revision: 1,
		Origin: domain.ChapterOriginLegacy, Content: content,
		ContentSHA256: domain.ChapterContentSHA256(content),
		Facts: domain.ChapterFacts{
			Title: summary.Title, Summary: summary.Summary,
			Characters: slices.Clone(summary.Characters), KeyEvents: slices.Clone(summary.KeyEvents),
		},
		AcceptedAt: legacyAcceptedAt(st, chapter),
	}, nil
}

func legacyAcceptedAt(st *store.Store, chapter int) time.Time {
	if checkpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit"); checkpoint != nil && !checkpoint.OccurredAt.IsZero() {
		return checkpoint.OccurredAt
	}
	if info, err := os.Stat(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter))); err == nil {
		return info.ModTime()
	}
	return time.Now()
}

func restoreLegacyFacts(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, previous *projection) {
	for chapter, record := range pending {
		record.Facts.HookType = historyAt(previous.hookHistory, chapter)
		record.Facts.DominantStrand = historyAt(previous.strandHistory, chapter)
	}
	for _, event := range previous.timeline {
		if record := pending[event.Chapter]; record != nil {
			record.Facts.TimelineEvents = append(record.Facts.TimelineEvents, event)
		}
	}
	for _, change := range previous.stateChanges {
		if record := pending[change.Chapter]; record != nil {
			record.Facts.StateChanges = append(record.Facts.StateChanges, change)
		}
	}
	for _, relation := range previous.relationships {
		if record := pending[relation.Chapter]; record != nil {
			record.Facts.RelationshipChanges = append(record.Facts.RelationshipChanges, relation)
		}
	}
	for _, entry := range previous.cast {
		record := pending[entry.FirstSeenChapter]
		if record == nil || entry.BriefRole == "" {
			continue
		}
		name := castNameInSummary(entry, record.Facts.Characters)
		if name != "" {
			record.Facts.CastIntros = append(record.Facts.CastIntros, domain.CastIntro{Name: name, BriefRole: entry.BriefRole})
		}
	}
	restoreForeshadow(existing, pending, chapters, previous)
}

// restoreForeshadow khôi phục mục sổ thành plant/advance/resolve từng chương. Mục không khôi phục được
// cho thấy bản ghi hiện có mâu thuẫn với sổ, loại bỏ và ghi log, không để một chi tiết gieo mầm chặn nâng cấp.
func restoreForeshadow(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, previous *projection) {
	kept := previous.foreshadow[:0]
	for _, entry := range previous.foreshadow {
		if !restoreForeshadowEntry(existing, pending, chapters, entry) {
			slog.Warn("chi tiết gieo mầm không thể khôi phục từ dữ liệu cũ, đã bỏ qua", "module", "migration", "id", entry.ID, "status", entry.Status)
			continue
		}
		kept = append(kept, entry)
	}
	previous.foreshadow = kept
}

func restoreForeshadowEntry(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, entry domain.ForeshadowEntry) bool {
	type placement struct {
		chapter int
		update  domain.ForeshadowUpdate
	}
	var placements []placement
	place := func(chapter int, update domain.ForeshadowUpdate) bool {
		if pending[chapter] == nil {
			return false
		}
		placements = append(placements, placement{chapter, update})
		return true
	}
	if !hasForeshadowAction(existing, pending, entry.ID, "plant") &&
		!place(entry.PlantedAt, domain.ForeshadowUpdate{ID: entry.ID, Action: "plant", Description: entry.Description}) {
		return false
	}
	switch entry.Status {
	case "planted":
	case "advanced":
		if !hasForeshadowAction(existing, pending, entry.ID, "advance") &&
			!place(firstPendingChapterAtOrAfter(chapters, pending, entry.PlantedAt), domain.ForeshadowUpdate{ID: entry.ID, Action: "advance"}) {
			return false
		}
	case "resolved":
		if !hasForeshadowAction(existing, pending, entry.ID, "resolve") &&
			!place(entry.ResolvedAt, domain.ForeshadowUpdate{ID: entry.ID, Action: "resolve"}) {
			return false
		}
	default:
		return false
	}
	// xác nhận tất cả điểm rơi rồi mới ghi, tránh mục khôi phục dở dang lẫn vào bản ghi.
	for _, p := range placements {
		record := pending[p.chapter]
		record.Facts.ForeshadowUpdates = append(record.Facts.ForeshadowUpdates, p.update)
	}
	return true
}

func hasForeshadowAction(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, id, action string) bool {
	hasAction := func(record domain.ChapterRecord) bool {
		for _, update := range record.Facts.ForeshadowUpdates {
			if update.ID == id && update.Action == action {
				return true
			}
		}
		return false
	}
	for _, record := range existing {
		if hasAction(record) {
			return true
		}
	}
	for _, record := range pending {
		if hasAction(*record) {
			return true
		}
	}
	return false
}

func firstPendingChapterAtOrAfter(chapters []int, pending map[int]*domain.ChapterRecord, chapter int) int {
	for _, candidate := range chapters {
		if candidate >= chapter && pending[candidate] != nil {
			return candidate
		}
	}
	return 0
}

func castNameInSummary(entry domain.CastEntry, characters []string) string {
	for _, name := range characters {
		if name == entry.Name || slices.Contains(entry.Aliases, name) {
			return name
		}
	}
	return ""
}

func historyAt(history []string, chapter int) string {
	if chapter > 0 && chapter <= len(history) {
		return history[chapter-1]
	}
	return ""
}

func compareProjection(previous, projected projection) error {
	if chapter, ok := mismatchedChapter(previous.summaries, projected.summaries,
		func(a, b domain.ChapterSummary) bool {
			return a.Chapter == b.Chapter && a.Title == b.Title && a.Summary == b.Summary &&
				slices.Equal(a.Characters, b.Characters) && slices.Equal(a.KeyEvents, b.KeyEvents)
		}, func(value domain.ChapterSummary) int { return value.Chapter }); ok {
		return fmt.Errorf("tóm tắt chương %d không nhất quán", chapter)
	}
	if chapter, ok := mismatchedChapter(previous.timeline, projected.timeline,
		func(a, b domain.TimelineEvent) bool {
			return a.Chapter == b.Chapter && a.Time == b.Time && a.Event == b.Event && slices.Equal(a.Characters, b.Characters)
		}, func(value domain.TimelineEvent) int { return value.Chapter }); ok {
		return fmt.Errorf("dòng thời gian chương %d không nhất quán", chapter)
	}
	if id, ok := mismatchedKey(previous.foreshadow, projected.foreshadow,
		func(value domain.ForeshadowEntry) string { return value.ID }); ok {
		return fmt.Errorf("chi tiết gieo mầm %q không nhất quán", id)
	}
	if key, ok := mismatchedKey(previous.relationships, projected.relationships,
		func(value domain.RelationshipEntry) string {
			return relationshipKey(value.CharacterA, value.CharacterB)
		}); ok {
		return fmt.Errorf("quan hệ nhân vật %q không nhất quán", key)
	}
	if chapter, ok := mismatchedChapter(previous.stateChanges, projected.stateChanges,
		func(a, b domain.StateChange) bool { return a == b },
		func(value domain.StateChange) int { return value.Chapter }); ok {
		return fmt.Errorf("thay đổi trạng thái chương %d không nhất quán", chapter)
	}
	if chapter := mismatchedHistory(previous.hookHistory, projected.hookHistory); chapter != 0 {
		return fmt.Errorf("hook_type chương %d không nhất quán", chapter)
	}
	if chapter := mismatchedHistory(previous.strandHistory, projected.strandHistory); chapter != 0 {
		return fmt.Errorf("dominant_strand chương %d không nhất quán", chapter)
	}
	if !equalStyle(previous.style, projected.style) {
		return fmt.Errorf("phong cách chỉnh sửa người dùng không nhất quán")
	}
	return nil
}

func mismatchedChapter[T any](a, b []T, equal func(T, T) bool, chapter func(T) int) (int, bool) {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if !equal(a[i], b[i]) {
			return chapter(a[i]), true
		}
	}
	if len(a) > limit {
		return chapter(a[limit]), true
	}
	if len(b) > limit {
		return chapter(b[limit]), true
	}
	return 0, false
}

func mismatchedKey[T comparable](a, b []T, key func(T) string) (string, bool) {
	right := make(map[string]T, len(b))
	for _, value := range b {
		right[key(value)] = value
	}
	for _, value := range a {
		k := key(value)
		if actual, ok := right[k]; !ok || actual != value {
			return k, true
		}
		delete(right, k)
	}
	for _, value := range b {
		if _, extra := right[key(value)]; extra {
			return key(value), true
		}
	}
	return "", false
}

func mismatchedHistory(a, b []string) int {
	a, b = trimHistory(a), trimHistory(b)
	for i := 0; i < max(len(a), len(b)); i++ {
		if historyAt(a, i+1) != historyAt(b, i+1) {
			return i + 1
		}
	}
	return 0
}

func trimHistory(values []string) []string {
	end := len(values)
	for end > 0 && values[end-1] == "" {
		end--
	}
	return values[:end]
}

func equalStyle(a, b domain.AuthorRevisionStyle) bool {
	if !slices.Equal(a.Prose, b.Prose) || !slices.Equal(a.Taboos, b.Taboos) || !a.UpdatedAt.Equal(b.UpdatedAt) || len(a.Dialogue) != len(b.Dialogue) {
		return false
	}
	for i := range a.Dialogue {
		if a.Dialogue[i].Name != b.Dialogue[i].Name || !slices.Equal(a.Dialogue[i].Rules, b.Dialogue[i].Rules) {
			return false
		}
	}
	return true
}
