package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/chapterfacts"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/utils"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/revision"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// CommitChapterTool Gửi chương: tải chính văn → lưu bản thảo cuối → tạo tóm tắt → cập nhật trạng thái → cập nhật tiến độ.
type CommitChapterTool struct {
	store      *store.Store
	styleStats *StyleStatsIndex
}

// NewCommitChapterTool Tạo công cụ gửi. styleStats phải được chia sẻ với novel_context,
// Đảm bảo sau khi thêm mới, làm lại và khôi phục hoàn tất thì làm mới cùng một chỉ mục thống kê.
func NewCommitChapterTool(store *store.Store, styleStats *StyleStatsIndex) *CommitChapterTool {
	if styleStats == nil {
		panic("tools: NewCommitChapterTool requires StyleStatsIndex")
	}
	return &CommitChapterTool{store: store, styleStats: styleStats}
}

func (t *CommitChapterTool) chapterStyleDelta(chapter int) (domain.StyleDelta, error) {
	record, err := t.store.ChapterRecords.Load(chapter)
	if err != nil || record == nil {
		return domain.StyleDelta{}, err
	}
	return record.StyleDelta, nil
}

// commitOutput Nhúng trường mở rộng trên domain.CommitResult, giữ cho gói domain không phụ thuộc vào rules.
// Do trường nhúng sẽ được JSON marshaler nâng lên (promoted), kết quả tuần tự hóa tương đương cấu trúc phẳng.
type commitOutput struct {
	domain.CommitResult
	RuleViolations []rules.Violation `json:"rule_violations,omitempty"`
}

// commitArgs Là tải trọng cấu trúc chuẩn hóa của Saga gửi. Lần thực thi đầu ghi nó cùng với ảnh chụp chính văn
// PendingCommit；Khôi phục sự cố luôn phát lại ý đồ bị đóng băng này, bỏ qua tham số và bản thảo do Worker mới tạo.
type commitArgs struct {
	Chapter int `json:"chapter"`
	domain.ChapterFacts
}

func (t *CommitChapterTool) Name() string { return "commit_chapter" }
func (t *CommitChapterTool) Description() string {
	return "Nộp bản cuối chương. Tải phần thân bản thảo lưu thành bản cuối, cập nhật dòng thời gian, chi tiết gieo mầm, quan hệ, trạng thái nhân vật và tiến độ." +
		"Trả về dữ kiện có cấu trúc: next_chapter / review_required / arc_end / volume_end / needs_expansion / book_complete / flow..."
}
func (t *CommitChapterTool) Label() string { return "nộp chương" }

// Công cụ ghi (Saga có thể khôi phục chéo miền: tải trọng đầy đủ→bản thảo cuối/trạng thái→tiến độ→checkpoint), cấm đồng thời.
func (t *CommitChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *CommitChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *CommitChapterTool) StrictSchema() bool                     { return true }

func (t *CommitChapterTool) Schema() map[string]any {
	props := []schema.Prop{schema.Property("chapter", schema.Int("số chương")).Required()}
	props = append(props, chapterfacts.Properties(true)...)
	return schema.Object(props...)
}

func (t *CommitChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var requested commitArgs
	if err := json.Unmarshal(args, &requested); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if requested.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	existingPending, err := t.store.Signals.LoadPendingCommit()
	if err != nil {
		return nil, fmt.Errorf("load pending commit: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && existingPending.Chapter != requested.Chapter {
		return nil, fmt.Errorf("tồn tại lần nộp chương chưa khôi phục: chương %d (giai đoạn %s), hãy khôi phục hoặc nộp lại chương đó trước: %w", existingPending.Chapter, existingPending.Stage, errs.ErrToolConflict)
	}
	if existingPending != nil {
		switch existingPending.Stage {
		case domain.CommitStageStarted, domain.CommitStageStateApplied, domain.CommitStageProgressMarked, domain.CommitStageSignalSaved:
		default:
			return nil, fmt.Errorf("giai đoạn pending commit không hợp lệ: %q: %w", existingPending.Stage, errs.ErrToolConflict)
		}
	}

	a := requested
	if existingPending != nil && existingPending.Stage != domain.CommitStageProgressMarked && existingPending.Stage != domain.CommitStageSignalSaved {
		if len(existingPending.Payload) == 0 {
			return nil, fmt.Errorf("chương %d có lần nộp chưa hoàn thành bản cũ nhưng thiếu payload tái phát được; từ chối ghi đè bằng tham số mới sinh, hãy khôi phục từ checkpoint gần nhất hoặc đối chiếu thủ công meta/pending_commit.json: %w",
				existingPending.Chapter, errs.ErrToolConflict)
		}
		if err := json.Unmarshal(existingPending.Payload, &a); err != nil {
			return nil, fmt.Errorf("decode pending commit payload: %w: %w", errs.ErrStoreRead, err)
		}
		if a.Chapter != existingPending.Chapter {
			return nil, fmt.Errorf("số chương payload pending commit không khớp: bản ghi=%d payload=%d: %w", existingPending.Chapter, a.Chapter, errs.ErrToolConflict)
		}
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
	}
	completed := slices.Contains(progress.CompletedChapters, a.Chapter)
	if existingPending != nil && (existingPending.Stage == domain.CommitStageProgressMarked || existingPending.Stage == domain.CommitStageSignalSaved) {
		if !completed {
			return nil, fmt.Errorf("pending commit đã tới %s, nhưng progress chưa đánh dấu chương %d hoàn thành: %w", existingPending.Stage, a.Chapter, errs.ErrToolConflict)
		}
		return t.finishPendingCommit(*existingPending, progress)
	}
	if existingPending == nil || existingPending.Stage == domain.CommitStageStarted {
		if err := t.validateCommitArgs(a); err != nil {
			// Phiên bản cũ có thể đã để lại gửi bị đóng băng trước khi phát hiện sự thật làm lại bất hợp pháp. Tiếp tục giữ lại
			// tải trọng bất biến này chỉ khiến mỗi lần thử lại lặp lại cùng một lỗi, do đó hủy đóng băng rõ ràng,
			// để Writer có thể sửa tham số rồi gửi lại; chính văn và ghi chép chương đều không thay đổi ở đây.
			if existingPending != nil && existingPending.Rewrite &&
				(errors.Is(err, errs.ErrToolArgs) || errors.Is(err, errs.ErrToolPrecondition)) {
				if clearErr := t.store.Signals.ClearPendingCommit(); clearErr != nil {
					return nil, fmt.Errorf("kiểm tra nộp làm lại thất bại (%v), và dọn nộp đóng băng thất bại: %w: %w", err, errs.ErrStoreWrite, clearErr)
				}
				return nil, fmt.Errorf("nộp làm lại còn sót của bản cũ chưa qua kiểm tra, đã gỡ đóng băng; hãy sửa rồi nộp lại: %w", err)
			}
			return nil, err
		}
	}

	if existingPending != nil && existingPending.Rewrite {
		if !completed {
			return nil, fmt.Errorf("nộp làm lại yêu cầu chương %d đã tồn tại bản cuối: %w", a.Chapter, errs.ErrToolConflict)
		}
		return t.executeRewriteCommit(a, progress, *existingPending, true)
	}
	if existingPending == nil && completed {
		if slices.Contains(progress.PendingRewrites, a.Chapter) {
			content, err := t.validateRewriteDraft(a.Chapter, a.Title, progress)
			if err != nil {
				return nil, err
			}
			payload, err := json.Marshal(a)
			if err != nil {
				return nil, fmt.Errorf("marshal rewrite payload: %w", err)
			}
			now := time.Now().Format(time.RFC3339)
			mode := "rewrite"
			if progress.Flow == domain.FlowPolishing {
				mode = "polish"
			}
			pending := domain.PendingCommit{Chapter: a.Chapter, Stage: domain.CommitStageStarted,
				Rewrite: true, RewriteMode: mode, Payload: payload, DraftContent: content,
				Summary: a.Summary, HookType: a.HookType,
				DominantStrand: a.DominantStrand, StartedAt: now, UpdatedAt: now}
			if err := t.store.Signals.SavePendingCommit(pending); err != nil {
				return nil, fmt.Errorf("save rewrite pending commit: %w: %w", errs.ErrStoreWrite, err)
			}
			return t.executeRewriteCommit(a, progress, pending, false)
		}
		return t.buildSkipResult(a.Chapter, progress)
	}

	// Gửi mới phải qua kiểm tra hàng đợi giai đoạn hiện tại/làm lại; PendingCommit thông thường đã có là giao thức khôi phục,
	// Cho phép vượt qua cửa sổ gián đoạn 'Progress đã lưu trước/Phase đã hoàn thành' để tiếp tục kết thúc.
	if existingPending == nil {
		if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
			// Xung đột hàng đợi giữ nguyên (đã có phân loại ErrToolConflict); lỗi IO khác thuộc về Precondition.
			if errors.Is(err, errs.ErrToolConflict) {
				return nil, err
			}
			return nil, fmt.Errorf("chương hiện tại không cho phép nộp: %w: %w", errs.ErrToolPrecondition, err)
		}
		if progress.Flow != domain.FlowRewriting && progress.Flow != domain.FlowPolishing {
			expected := progress.NextChapter()
			if a.Chapter != expected {
				return nil, fmt.Errorf("viết tiếp bình thường chỉ được nộp chương kế tiếp %d, nhận được chương %d: %w", expected, a.Chapter, errs.ErrToolConflict)
			}
		}
	}

	// Chặn vượt ranh giới mô hình phân lớp: phải trước bất kỳ thao tác ghi nào, nếu không commit vượt ranh giới sẽ làm hỏng file chương, tóm tắt,
	// Progress Tất cả đều hỏng. boundary tái sử dụng cho bước 6b bên dưới tính tín hiệu arc/tập.
	var boundary *store.ArcBoundary
	if progress.Layered {
		b, bErr := t.store.Outline.CheckArcBoundary(a.Chapter)
		if bErr != nil {
			return nil, fmt.Errorf("phát hiện ranh giới arc thất bại chapter=%d: %w: %w", a.Chapter, errs.ErrStoreRead, bErr)
		}
		if b == nil {
			return nil, fmt.Errorf(
				"chương %d không nằm trong phạm vi đại cương phân tầng: khi viết phải expand_arc triển khai arc hoặc append_volume thêm tập trước; nếu toàn sách đã hoàn thành hãy gọi save_foundation type=complete_book: %w",
				a.Chapter, errs.ErrToolPrecondition)
		}
		boundary = b
	}

	// 1. Đóng băng chính văn chương. Gửi lần đầu đọc từ bản thảo và lưu cùng PendingCommit; khi khôi phục
	// chỉ dùng ảnh chụp này, tránh Worker mới đè bản thảo trước khi thử lại tạo thành 'sự thật cũ + chính văn mới'.
	var content string
	if existingPending != nil {
		content = existingPending.DraftContent
		if content == "" {
			return nil, fmt.Errorf("lần nộp chưa hoàn thành chương %d thiếu draft_content, không thể chứng minh phần thân khôi phục khớp với lần nộp gốc: %w",
				a.Chapter, errs.ErrToolConflict)
		}
	} else {
		var loadErr error
		content, _, loadErr = t.store.Drafts.LoadChapterContent(a.Chapter)
		if loadErr != nil {
			return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, loadErr)
		}
	}
	if content == "" {
		return nil, fmt.Errorf("không tìm thấy nội dung cho chương %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	wordCount := utils.CountWords(content)

	var pending domain.PendingCommit
	if existingPending != nil {
		pending = *existingPending
	} else {
		payload, err := json.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("marshal commit payload: %w", err)
		}
		now := time.Now().Format(time.RFC3339)
		pending = domain.PendingCommit{
			Chapter: a.Chapter, Stage: domain.CommitStageStarted, Payload: payload, DraftContent: content,
			Summary: a.Summary, HookType: a.HookType, DominantStrand: a.DominantStrand,
			StartedAt: now, UpdatedAt: now,
		}
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("save pending commit: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// StageStarted có thể chỉ ra chưa ghi công cụ nào, cũng có thể sụp đổ giữa chừng khi tăng trạng thái; tải trọng đầy đủ
	// tất cả thao tác phải là idempotent, do đó phát lại thống nhất. StageStateApplied thì trực tiếp vào Progress.
	if pending.Stage == domain.CommitStageStarted {
		// 2. Lưu bản thảo cuối
		if err := t.store.Drafts.SaveFinalChapter(a.Chapter, content); err != nil {
			return nil, fmt.Errorf("save final chapter: %w: %w", errs.ErrStoreWrite, err)
		}
		style, err := t.chapterStyleDelta(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load chapter style: %w: %w", errs.ErrStoreRead, err)
		}
		if _, err := t.store.ChapterRecords.Accept(a.Chapter, domain.ChapterOriginGenerated, content, a.ChapterFacts, style); err != nil {
			return nil, fmt.Errorf("save chapter record: %w: %w", errs.ErrStoreWrite, err)
		}

		// 3. Lưu tóm tắt
		summary := domain.ChapterSummary{
			Chapter: a.Chapter, Title: a.Title, Summary: a.Summary, Characters: a.Characters, KeyEvents: a.KeyEvents,
		}
		if err := t.store.Summaries.SaveSummary(summary); err != nil {
			return nil, fmt.Errorf("save summary: %w: %w", errs.ErrStoreWrite, err)
		}

		// 4. Cập nhật tăng trạng thái
		if len(a.TimelineEvents) > 0 {
			for i := range a.TimelineEvents {
				a.TimelineEvents[i].Chapter = a.Chapter
			}
			if err := t.store.World.AppendTimelineEvents(a.TimelineEvents); err != nil {
				return nil, fmt.Errorf("append timeline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.ForeshadowUpdates) > 0 {
			if err := t.store.World.UpdateForeshadow(a.Chapter, a.ForeshadowUpdates); err != nil {
				return nil, fmt.Errorf("update foreshadow: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.RelationshipChanges) > 0 {
			for i := range a.RelationshipChanges {
				a.RelationshipChanges[i].Chapter = a.Chapter
			}
			if err := t.store.World.UpdateRelationships(a.RelationshipChanges); err != nil {
				return nil, fmt.Errorf("update relationships: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.StateChanges) > 0 {
			for i := range a.StateChanges {
				a.StateChanges[i].Chapter = a.Chapter
			}
			if err := t.store.World.AppendStateChanges(a.StateChanges); err != nil {
				return nil, fmt.Errorf("append state changes: %w: %w", errs.ErrStoreWrite, err)
			}
		}

		// 4b. Tích lũy sổ nhân vật phụ: nhân vật không cốt lõi xuất hiện trong chương này vào cast_ledger, để novel_context gọi lại.
		// Khi thất bại chỉ cảnh báo không chặn commit——sổ là dữ liệu phụ, có thể tự lành qua commit chương tiếp theo.
		if len(a.Characters) > 0 {
			coreNames, err := loadCoreCharacterNameSet(t.store)
			if err != nil {
				return nil, fmt.Errorf("load core characters for cast ledger: %w: %w", errs.ErrStoreRead, err)
			}
			if err := t.store.Cast.MergeAppearances(a.Chapter, a.Characters, a.CastIntros, coreNames); err != nil {
				slog.Warn("cộng dồn danh bạ nhân vật phụ thất bại, bỏ qua", "module", "commit", "chapter", a.Chapter, "err", err)
			}
		}

		pending.Stage = domain.CommitStageStateApplied
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("update pending commit stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 5. Cập nhật tiến độ
	if !completed {
		if err := t.store.Progress.MarkChapterComplete(a.Chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
			return nil, fmt.Errorf("mark chapter complete: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 6. Phán đoán xem có cần đọc kiểm không
	progress, err = t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	completedCount := 0
	if progress != nil {
		completedCount = len(progress.CompletedChapters)
	}

	// 6b. Tín hiệu arc/tập mô hình dài: boundary đã kiểm tra trước ở đầu vào, khi Layered đảm bảo không nil
	var arcEnd, volumeEnd, needsExpansion, needsNewVolume bool
	var vol, arc, nextVol, nextArc int
	if progress != nil && progress.Layered && boundary != nil {
		arcEnd = boundary.IsArcEnd
		volumeEnd = boundary.IsVolumeEnd
		vol = boundary.Volume
		arc = boundary.Arc
		needsExpansion = boundary.NeedsExpansion
		needsNewVolume = boundary.NeedsNewVolume
		nextVol = boundary.NextVolume
		nextArc = boundary.NextArc
		if err := t.store.Progress.UpdateVolumeArc(vol, arc); err != nil {
			return nil, fmt.Errorf("update volume/arc: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	var reviewRequired bool
	var reviewReason string
	if progress != nil && progress.Layered {
		reviewRequired, reviewReason = domain.ShouldArcReview(arcEnd, volumeEnd, vol, arc)
	} else {
		reviewRequired, reviewReason = domain.ShouldReview(completedCount)
	}

	// 7. Cấu tạo tín hiệu cấu trúc
	result := domain.CommitResult{
		Chapter:        a.Chapter,
		Committed:      true,
		WordCount:      wordCount,
		NextChapter:    a.Chapter + 1,
		ReviewRequired: reviewRequired,
		ReviewReason:   reviewReason,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		Feedback:       a.Feedback,
		// (feedback đồng thời lưu bền vững vào nhóm phản hồi, xem persistFeedback bên dưới——giá trị trả về chỉ là hình ảnh,
		// architect qua novel_context tiêu thụ là sự thật store)
		ArcEnd:         arcEnd,
		VolumeEnd:      volumeEnd,
		Volume:         vol,
		Arc:            arc,
		NeedsExpansion: needsExpansion,
		NeedsNewVolume: needsNewVolume,
		NextVolume:     nextVol,
		NextArc:        nextArc,
	}

	// 8. Phán đoán trạng thái hoàn thành: không phân lớp viết xong chương cuối / phân lớp tập cuối chương cuối → MarkComplete
	bookComplete, err := t.applyCompletion(&result, progress)
	if err != nil {
		return nil, err
	}
	if bookComplete {
		result.BookComplete = true
	}
	latestProgress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress after completion: %w: %w", errs.ErrStoreRead, err)
	}
	if latestProgress != nil {
		result.Flow = string(latestProgress.Flow)
	}

	// 8.5 Nhóm phản hồi là sự thật bền vững cho quy hoạch sau này, do Architect tiêu thụ trong lần thao tác cấu trúc tiếp theo.
	if a.Feedback != nil && (strings.TrimSpace(a.Feedback.Deviation) != "" || strings.TrimSpace(a.Feedback.Suggestion) != "") {
		if err := t.store.Outline.AppendOutlineFeedback(store.ChapterFeedback{
			Chapter: a.Chapter, Deviation: a.Feedback.Deviation, Suggestion: a.Feedback.Suggestion,
		}); err != nil {
			return nil, fmt.Errorf("persist outline feedback: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// Quy tắc cơ học là một phần đầu ra, phải cố định trước ProgressMarked, khi khôi phục trả về trực tiếp cùng đầu ra.
	violations := t.checkRules(content)
	output, err := json.Marshal(commitOutput{CommitResult: result, RuleViolations: violations})
	if err != nil {
		return nil, fmt.Errorf("marshal commit output: %w", err)
	}

	pending.Stage = domain.CommitStageProgressMarked
	pending.Result = &result
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit result: %w: %w", errs.ErrStoreWrite, err)
	}

	// 9. Thêm checkpoint. Phải trước khi xóa pending_commit, đảm bảo sau khi khởi động lại thấy
	// pending_commit luôn có thể thúc đẩy chạy lại bù checkpoint thiếu.
	if err := t.appendCommitCheckpoint(a.Chapter); err != nil {
		return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 10. Xóa trạng thái giữa tiến độ
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// Lưu bền vững sự thật vi phạm: đánh giá của editor qua novel_context tiêu thụ (giá trị trả về chỉ là hình ảnh——
	// writer dừng cứng ngay sau commit, giá trị trả về không ai đọc được). best-effort.
	if err := t.store.World.SaveRuleViolations(a.Chapter, violations); err != nil {
		slog.Warn("ghi vi phạm cơ khí xuống đĩa thất bại", "module", "tools", "chapter", a.Chapter, "err", err)
	}
	t.refreshStyleStats(a.Chapter, content)
	return output, nil
}

// finishPendingCommit Kết thúc cửa sổ gián đoạn ProgressMarked/SignalSaved. Thêm checkpoint theo
// digest là idempotent; chỉ khi dọn dẹp checkpoint và trạng thái giữa thành công mới xóa bản ghi khôi phục.
func (t *CommitChapterTool) finishPendingCommit(pending domain.PendingCommit, progress *domain.Progress) (json.RawMessage, error) {
	if pending.Stage == domain.CommitStageProgressMarked {
		if err := t.appendCommitCheckpoint(pending.Chapter); err != nil {
			return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
		}
		pending.Stage = domain.CommitStageSignalSaved
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}
	t.refreshStyleStats(pending.Chapter, pending.DraftContent)
	if len(pending.Output) > 0 {
		return append(json.RawMessage(nil), pending.Output...), nil
	}
	if pending.Result != nil {
		return json.Marshal(pending.Result)
	}
	return t.buildSkipResult(pending.Chapter, progress)
}

func (t *CommitChapterTool) validateRewriteDraft(chapter int, title string, progress *domain.Progress) (string, error) {
	content, _, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return "", fmt.Errorf("rewrite: load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return "", fmt.Errorf("không tìm thấy nội dung cho chương %d: %w", chapter, errs.ErrToolPrecondition)
	}
	changed, err := t.rewriteChanged(chapter, content, title)
	if err != nil {
		return "", err
	}
	if changed {
		return content, nil
	}
	mode := "viết lại"
	if progress != nil && progress.Flow == domain.FlowPolishing {
		mode = "đánh bóng"
	}
	return "", fmt.Errorf("phần thân và tiêu đề chương %d đều không thay đổi, không phát hiện sửa đổi %s: %w",
		chapter, mode, errs.ErrToolPrecondition)
}

func (t *CommitChapterTool) rewriteChanged(chapter int, content, title string) (bool, error) {
	existingFinal, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return false, fmt.Errorf("rewrite: load final chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if existingFinal != content {
		return true, nil
	}
	summary, err := t.store.Summaries.LoadSummary(chapter)
	if err != nil {
		return false, fmt.Errorf("rewrite: load chapter summary: %w: %w", errs.ErrStoreRead, err)
	}
	return summary == nil || strings.TrimSpace(summary.Title) != strings.TrimSpace(title), nil
}

func (t *CommitChapterTool) appendCommitCheckpoint(chapter int) error {
	_, err := t.store.Checkpoints.AppendArtifacts(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
		fmt.Sprintf("summaries/%02d.json", chapter),
		store.ChapterRecordPath(chapter),
	)
	return err
}

// checkRules Thực hiện kiểm tra cơ học chính văn chương: Lint cơ sở sản phẩm tích hợp (cơ chế còn lại, luôn thực thi)
// + Check quy tắc người dùng (đọc structured của ảnh chụp sách; thiếu ảnh chụp lùi về mặc định tích hợp, đảm bảo cơ sở cơ học luôn có).
func (t *CommitChapterTool) checkRules(text string) []rules.Violation {
	violations := rules.Lint(text)
	structured := rules.SystemDefaults().Structured
	if snap, err := t.store.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	return append(violations, rules.Check(text, structured)...)
}

// executeRewriteCommit Xử lý gửi chương làm bóng/làm lại: ghi đè bản thảo cuối và tóm tắt, cập nhật số chữ, drain hàng đợi.
// Bỏ qua tất cả thêm trạng thái thế giới (timeline / foreshadow / relationship / state_changes) và phát hiện ranh giới arc,
// những cái này đã được áp dụng khi gửi chương gốc.
func (t *CommitChapterTool) executeRewriteCommit(a commitArgs, progress *domain.Progress, pending domain.PendingCommit, recovering bool) (json.RawMessage, error) {
	chapter := a.Chapter
	// 1. Chỉ dùng chính văn làm lại bị đóng băng lần gửi đầu, khôi phục sự cố không được dùng draft bị đè sau đó.
	content := pending.DraftContent
	if content == "" {
		return nil, fmt.Errorf("nộp làm lại chương %d thiếu draft_content, không thể khôi phục an toàn: %w", chapter, errs.ErrToolConflict)
	}
	wordCount := utils.CountWords(content)

	// 2. Chính văn hoặc tiêu đề ít nhất một mục thay đổi; làm bóng tiêu đề không cần giả mạo thay đổi chính văn.
	if !recovering {
		changed, err := t.rewriteChanged(chapter, content, a.Title)
		if err != nil {
			return nil, err
		}
		if !changed {
			mode := "viết lại"
			if progress != nil && progress.Flow == domain.FlowPolishing {
				mode = "đánh bóng"
			}
			return nil, fmt.Errorf("chính văn và tiêu đề chương %d đều không thay đổi, không phát hiện thay đổi %s: %w",
				chapter, mode, errs.ErrToolPrecondition)
		}
	}

	if pending.Stage == domain.CommitStageStarted {
		// 3. Trước tiên cấu tạo tập bản ghi ứng viên đầy đủ và phát lại kiểm tra. Cài đặt cũ ghi đè bản ghi trước rồi xây dựng lại hình chiếu,
		// một khi chuỗi sự thật không đóng sẽ để tải trọng lỗi trên đĩa, các lần thử lại sau luôn đọc base hỏng.
		existing, err := t.store.ChapterRecords.Load(chapter)
		if err != nil {
			return nil, fmt.Errorf("rewrite: load chapter record: %w: %w", errs.ErrStoreRead, err)
		}
		var existingUpdates []domain.ForeshadowUpdate
		var style domain.StyleDelta
		if existing != nil {
			existingUpdates = existing.Facts.ForeshadowUpdates
			style = existing.StyleDelta
		}
		recovered, err := t.restoreRewritePlants(chapter, existingUpdates, &a.ChapterFacts)
		if err != nil {
			return nil, err
		}
		candidate, err := t.store.ChapterRecords.Prepare(
			chapter, domain.ChapterOriginGenerated, content, a.ChapterFacts, style,
		)
		if err != nil {
			return nil, fmt.Errorf("rewrite: prepare chapter record: %w: %w", errs.ErrStoreRead, err)
		}
		chapters := slices.Clone(progress.CompletedChapters)
		slices.Sort(chapters)
		records := make([]domain.ChapterRecord, 0, len(chapters))
		for _, completedChapter := range chapters {
			if completedChapter == chapter {
				records = append(records, *candidate)
				continue
			}
			record, err := t.store.ChapterRecords.Load(completedChapter)
			if err != nil {
				return nil, fmt.Errorf("rewrite: load chapter record %d: %w: %w", completedChapter, errs.ErrStoreRead, err)
			}
			if record == nil {
				return nil, fmt.Errorf("rewrite: chương %d thiếu bản ghi chấp nhận: %w", completedChapter, errs.ErrToolConflict)
			}
			records = append(records, *record)
		}
		if err := revision.ValidateRecords(records); err != nil {
			if clearErr := t.store.Signals.ClearPendingCommit(); clearErr != nil {
				return nil, fmt.Errorf("rewrite: kiểm tra chuỗi dữ kiện chương thất bại (%v), và dọn nộp đóng băng thất bại: %w: %w", err, errs.ErrStoreWrite, clearErr)
			}
			return nil, fmt.Errorf("rewrite: kiểm tra chuỗi dữ kiện chương thất bại, đã gỡ đóng băng và chưa ghi kết quả làm lại: %w: %w", errs.ErrToolPrecondition, err)
		}

		// 4. Sau khi kiểm tra qua mới đè bản ghi quyền uy và bản thảo cuối; cùng tải trọng đóng băng có thể an toàn phát lại.
		if err := t.store.Drafts.SaveFinalChapter(chapter, content); err != nil {
			return nil, fmt.Errorf("rewrite: save final chapter: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.ChapterRecords.Save(*candidate); err != nil {
			return nil, fmt.Errorf("rewrite: save chapter record: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(recovered) > 0 {
			slog.Warn("đã khôi phục dữ kiện gieo bị mất bản cũ từ sổ chi tiết gieo mầm", "module", "commit", "chapter", chapter, "foreshadows", recovered)
		}
		if err := revision.NewProjector(t.store).Apply(records); err != nil {
			return nil, fmt.Errorf("rewrite: rebuild chapter projections: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Summaries.SaveSummary(domain.ChapterSummary{
			Chapter: chapter, Title: a.Title, Summary: a.Summary, Characters: a.Characters, KeyEvents: a.KeyEvents,
		}); err != nil {
			return nil, fmt.Errorf("rewrite: save summary: %w: %w", errs.ErrStoreWrite, err)
		}
		pending.Stage = domain.CommitStageStateApplied
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("rewrite: update pending state stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 5. Cập nhật số chữ (MarkChapterComplete với chương đã hoàn thành là idempotent: replaces word count, slice.Contains ngăn chặn trùng lặp vào hàng đợi)
	if progress.Phase != domain.PhaseComplete {
		if err := t.store.Progress.MarkChapterComplete(chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
			return nil, fmt.Errorf("rewrite: update word count: %w: %w", errs.ErrStoreWrite, err)
		}

		// 6. Drain hàng đợi đang xử lý; khi hàng đợi trống CompleteRewrite sẽ tự động chuyển flow về writing
		if err := t.store.Progress.CompleteRewrite(chapter); err != nil {
			return nil, fmt.Errorf("rewrite: complete rewrite: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 7. Đọc ảnh chụp Progress sau khi drain, trả về như sự thật
	mode := pending.RewriteMode
	if mode == "" {
		mode = "rewrite"
	}
	latest, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("rewrite: load progress after drain: %w: %w", errs.ErrStoreRead, err)
	}
	remaining := []int{}
	nextChapter := chapter + 1
	flow := string(domain.FlowWriting)
	if latest != nil {
		remaining = append(remaining, latest.PendingRewrites...)
		nextChapter = latest.NextChapter()
		flow = string(latest.Flow)
	}
	drained := len(remaining) == 0

	// Đánh giá hoàn kết sau khi làm trống hàng đợi: gửi làm lại không qua đường chính applyCompletion, hoàn kết chỉ có thể kích hoạt tại đây.
	//   - Phân lớp + sáng tác xuôi: đánh giá tổng layeredComplete (cấu trúc tập kết thúc viết xong / chưa tuyên bố đi cấp chất lượng).
	//   - Phân lớp + reopen làm lại (ReopenedFromComplete): làm lại chỉ đổi chương đã có, không tăng giảm cấu trúc, theo cấu trúc hoàn chỉnh
	//     tức là hoàn kết lại——nếu do làm lại xáo trộn một manh mối nào đó thì kẹt ở writing, cuối tập cuối sẽ rơi vào vòng lặp chết tiếp tục viết vượt ranh giới.
	//   - Không phân lớp: viết đủ TotalChapters tức hoàn kết (làm lại không tăng giảm số chương, vốn dĩ đã đủ).
	bookComplete := false
	if drained && latest != nil {
		reComplete := false
		switch {
		case latest.Layered && latest.ReopenedFromComplete:
			reComplete, err = layeredStructurallyComplete(t.store, latest)
		case latest.Layered:
			reComplete, err = layeredComplete(t.store, latest)
		default:
			reComplete = latest.TotalChapters > 0 && len(latest.CompletedChapters) >= latest.TotalChapters
		}
		if err != nil {
			return nil, fmt.Errorf("rewrite: evaluate completion: %w: %w", errs.ErrStoreRead, err)
		}
		if reComplete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return nil, fmt.Errorf("rewrite: mark complete: %w: %w", errs.ErrStoreWrite, err)
			}
			bookComplete = true
			p, err := t.store.Progress.Load()
			if err != nil {
				return nil, fmt.Errorf("rewrite: reload completed progress: %w: %w", errs.ErrStoreRead, err)
			}
			if p != nil {
				flow = string(p.Flow)
			}
		}
	}

	// Cùng đường chính: rewrite/polish cũng làm kiểm tra cơ học và lưu bền vững (làm lại xong lưu bản ghi mới, vi phạm cũ coi như đã xóa)
	violations := t.checkRules(content)
	output, err := json.Marshal(map[string]any{
		"chapter": chapter, "rewritten": true, "mode": mode, "word_count": wordCount,
		"remaining_queue": remaining, "queue_drained": drained, "next_chapter": nextChapter,
		"flow": flow, "book_complete": bookComplete, "rule_violations": violations,
	})
	if err != nil {
		return nil, fmt.Errorf("rewrite: marshal output: %w", err)
	}
	pending.Stage = domain.CommitStageProgressMarked
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending progress stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 8. Checkpoint sau đó đánh dấu signal_saved, cuối cùng dọn PendingCommit.
	if err := t.appendCommitCheckpoint(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("rewrite: clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("rewrite: clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.World.SaveRuleViolations(chapter, violations); err != nil {
		slog.Warn("Ghi đĩa vi phạm cơ học thất bại", "module", "tools", "chapter", chapter, "err", err)
	}
	t.refreshStyleStats(chapter, content)
	return output, nil
}

// restoreRewritePlants Chỉ sửa dạng hỏng đơn do phiên bản cũ gây ra: sổ chi tiết gieo mầm vẫn ghi nhận chương này
// của plant, nhưng bản ghi tiếp nhận chương này đã bị đè bởi làm lại thất bại. Sổ cung cấp id đầy đủ, mô tả và chương gieo,
// do đó có thể khôi phục xác định; các không nhất quán khác tiếp tục báo lỗi rõ ràng, không đoán sự thật cốt truyện.
func (t *CommitChapterTool) restoreRewritePlants(chapter int, existing []domain.ForeshadowUpdate, facts *domain.ChapterFacts) ([]string, error) {
	planted := make(map[string]struct{}, len(existing)+len(facts.ForeshadowUpdates))
	for _, update := range existing {
		if update.Action == "plant" {
			planted[update.ID] = struct{}{}
		}
	}
	for _, update := range facts.ForeshadowUpdates {
		if update.Action == "plant" {
			planted[update.ID] = struct{}{}
		}
	}

	ledger, err := t.store.World.LoadForeshadowLedger()
	if err != nil {
		return nil, fmt.Errorf("rewrite: load foreshadow ledger for recovery: %w: %w", errs.ErrStoreRead, err)
	}
	var restored []domain.ForeshadowUpdate
	var ids []string
	for _, entry := range ledger {
		if entry.PlantedAt != chapter {
			continue
		}
		if _, ok := planted[entry.ID]; ok {
			continue
		}
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Description) == "" {
			return nil, fmt.Errorf("rewrite: sổ chi tiết gieo mầm chương %d thiếu id hoặc description khôi phục được: %w", chapter, errs.ErrToolConflict)
		}
		planted[entry.ID] = struct{}{}
		restored = append(restored, domain.ForeshadowUpdate{
			ID: entry.ID, Action: "plant", Description: entry.Description,
		})
		ids = append(ids, entry.ID)
	}
	if len(restored) > 0 {
		facts.ForeshadowUpdates = append(restored, facts.ForeshadowUpdates...)
	}
	return ids, nil
}

func (t *CommitChapterTool) refreshStyleStats(chapter int, content string) {
	if content == "" {
		var err error
		content, err = t.store.Drafts.LoadChapterText(chapter)
		if err != nil {
			slog.Error("cập nhật chỉ mục thống kê văn phong thất bại", "module", "tools", "chapter", chapter, "err", err)
			return
		}
		if content == "" {
			slog.Error("cập nhật chỉ mục thống kê văn phong thất bại", "module", "tools", "chapter", chapter, "err", errors.New("bản cuối không tồn tại"))
			return
		}
	}
	t.styleStats.ChapterCommitted(chapter, content)
}

// buildSkipResult Xây dựng sự thật trả về cho 'gửi lặp lại chương đã hoàn thành' phù hợp với commit bình thường.
// Người điều phối dựa vào đó ra quyết định tiếp theo (writer/editor/architect phân phát), mà không bị ảo giác do nhận gợi ý prose.
func (t *CommitChapterTool) buildSkipResult(chapter int, progress *domain.Progress) (json.RawMessage, error) {
	_, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("load completed chapter: %w: %w", errs.ErrStoreRead, err)
	}

	result := domain.CommitResult{
		Chapter:     chapter,
		Committed:   true,
		WordCount:   wordCount,
		NextChapter: chapter + 1,
	}

	if progress != nil && progress.Layered {
		boundary, err := t.store.Outline.CheckArcBoundary(chapter)
		if err != nil {
			return nil, fmt.Errorf("check completed chapter boundary: %w: %w", errs.ErrStoreRead, err)
		}
		if boundary != nil {
			result.ArcEnd = boundary.IsArcEnd
			result.VolumeEnd = boundary.IsVolumeEnd
			result.Volume = boundary.Volume
			result.Arc = boundary.Arc
			result.NeedsExpansion = boundary.NeedsExpansion
			result.NeedsNewVolume = boundary.NeedsNewVolume
			result.NextVolume = boundary.NextVolume
			result.NextArc = boundary.NextArc
		}
		result.ReviewRequired, result.ReviewReason = domain.ShouldArcReview(result.ArcEnd, result.VolumeEnd, result.Volume, result.Arc)
	} else if progress != nil {
		result.ReviewRequired, result.ReviewReason = domain.ShouldReview(len(progress.CompletedChapters))
	}

	if progress != nil {
		if progress.Phase == domain.PhaseComplete {
			result.BookComplete = true
		}
		result.Flow = string(progress.Flow)
	}

	return json.Marshal(result)
}

// loadCoreCharacterNameSet Tải tập tên nhân vật đã có trong characters.json (gồm bí danh).
// Dùng làm tập lọc 'cốt lõi đã biết' của cast_ledger——nhân vật cốt lõi không vào sổ phụ.
// Khi tải thất bại trả về nil (khi merge tất cả characters đều vào ledger, có thể chấp nhận).
func loadCoreCharacterNameSet(s *store.Store) (map[string]bool, error) {
	chars, err := s.Characters.Load()
	if err != nil {
		return nil, err
	}
	if len(chars) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(chars)*2)
	for _, c := range chars {
		if c.Name != "" {
			set[c.Name] = true
		}
		for _, alias := range c.Aliases {
			if alias != "" {
				set[alias] = true
			}
		}
	}
	return set, nil
}

// applyCompletion Phán đoán commit lần này có làm toàn sách hoàn kết không, nếu có thì MarkComplete và trả về true.
//   - Không phân lớp: viết xong tổng số chương thỏa thuận tức hoàn kết.
//   - Phân lớp: kiến trúc sư rõ ràng save_foundation type=complete_book là đường chính; ở đây thêm một lớp
//     chống đỡ xác định (xem layeredComplete)——ngăn mô hình ở đích không append_volume cũng không
//     complete_book, dẫn đến livelock 'writer chạy không vượt chương → lính gác ranh giới chặn → lặp lại thử'
//     (nguyên nhân gốc vụ 《Phàm Cốt》ch204..347).
func (t *CommitChapterTool) applyCompletion(result *domain.CommitResult, progress *domain.Progress) (bool, error) {
	if progress == nil {
		return false, nil
	}
	if progress.Phase == domain.PhaseComplete {
		return true, nil
	}
	if progress.Layered {
		complete, err := layeredComplete(t.store, progress)
		if err != nil {
			return false, fmt.Errorf("evaluate layered completion: %w: %w", errs.ErrStoreRead, err)
		}
		if complete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
			}
			return true, nil
		}
		return false, nil
	}
	if progress.TotalChapters > 0 && result.NextChapter > progress.TotalChapters {
		if err := t.store.Progress.MarkComplete(); err != nil {
			return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
		}
		return true, nil
	}
	return false, nil
}

// ── Phán đoán hoàn kết phân lớp (cấp gói: commit_chapter và save_volume_summary hai điểm kích hoạt dùng chung) ──
//
// Kiểm tra hoàn kết luôn xảy ra ở công cụ 'mảnh sự thật cuối cùng rơi xuống':
//   - Chưa tuyên bố kết thúc: commit chương cuối (layeredBookComplete cấp chất lượng)
//   - Đã tuyên bố kết thúc: mảnh ghép cuối của đường chính xuôi là ba liên kết cuối tập (đánh giá→tóm tắt arc→tóm tắt tập),
//     do đó điểm kích hoạt ở save_volume_summary; sau khi drain làm lại ba liên đã đủ thì do commit kích hoạt.

// layeredStructurallyComplete Phán đoán truyện dài phân lớp có 'hoàn thành cấu trúc' không: hàng đợi làm lại trống + không arc khung chờ mở rộng
// + tất cả chương đã mở rộng đều đã viết. Đây là sự thật trạng thái cuối xác định, không chứa phán đoán ngữ nghĩa chi tiết gieo mầm/tuyến dài——dùng làm 'chống trạng thái cuối
// vòng lặp chết' lưới an toàn (sau khi trống làm lại dựa vào đây hoàn kết lại).
func layeredStructurallyComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	// 1. Hàng đợi làm lại phải trống
	if len(progress.PendingRewrites) > 0 {
		return false, nil
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("load layered outline: %w", err)
	}
	if len(volumes) == 0 {
		return false, nil
	}
	// 2. Không thể còn arc khung chờ mở rộng (trong kế hoạch vẫn còn nội dung phải viết)
	for i := range volumes {
		for j := range volumes[i].Arcs {
			if !volumes[i].Arcs[j].IsExpanded() {
				return false, nil
			}
		}
	}
	// 3. Chương đã mở rộng phải viết xong hết
	expanded := len(domain.FlattenOutline(volumes))
	return expanded > 0 && len(progress.CompletedChapters) >= expanded, nil
}

// finaleWrapped Ba liên kết cuối của tập kết thúc (đánh giá arc/tóm tắt arc/tóm tắt tập) có đầy đủ không.
// Hoàn kết kết thúc không yêu cầu chi tiết gieo mầm/tuyến dài về không, nhưng phải đợi arc cuối qua cổng chất lượng biên tập——kết cục là phần quan trọng nhất toàn sách,
// Hoàn kết không thể tranh trước đánh giá của editor (có thể vào hàng đợi làm lại) và tóm tắt ghi đĩa.
func finaleWrapped(st *store.Store, progress *domain.Progress) (bool, error) {
	last := progress.LatestCompleted()
	if last <= 0 {
		return false, nil
	}
	b, err := st.Outline.CheckArcBoundary(last)
	if err != nil {
		return false, fmt.Errorf("check finale boundary: %w", err)
	}
	if b == nil || !b.IsArcEnd {
		return false, nil
	}
	hasReview, err := st.World.HasArcReview(last)
	if err != nil {
		return false, fmt.Errorf("load finale review: %w", err)
	}
	hasArcSummary, err := st.Summaries.HasArcSummary(b.Volume, b.Arc)
	if err != nil {
		return false, fmt.Errorf("load finale arc summary: %w", err)
	}
	hasVolumeSummary, err := st.Summaries.HasVolumeSummary(b.Volume)
	if err != nil {
		return false, fmt.Errorf("load finale volume summary: %w", err)
	}
	return hasReview && hasArcSummary && hasVolumeSummary, nil
}

// layeredComplete Đánh giá tổng hoàn kết của sáng tác xuôi phân lớp:
//   - Đã tuyên bố tập kết thúc (cuốn cuối layered_outline có final)→ cấu trúc viết xong + ba liên kết cuối tập đầy đủ
//     tức hoàn kết, không yêu cầu chi tiết gieo mầm/tuyến dài về không. Tập kết thúc cả tập nhắm mục tiêu thu tuyến (kiến trúc sư quy hoạch đã đưa tuyến dài/
//     chi tiết gieo mầm phân bổ vào các arc), sai sót cá biệt thuộc vấn đề chất lượng biên tập, không nên kẹt toàn sách ngoài trạng thái cuối——nếu không
//     sách estimated_scale đánh giá cao vĩnh viễn không thể hoàn bản hợp pháp (nguyên nhân sâu xa của vụ ngắt stop guard 140 chương).
//   - Chưa tuyên bố → cấp chất lượng layeredBookComplete, phòng mô hình không kết thúc cũng không hoàn bản khi cạn kiệt đại cương
//     kết thúc quá sớm.
func layeredComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("load layered outline: %w", err)
	}
	if domain.FinaleVolume(volumes) > 0 {
		structurallyComplete, err := layeredStructurallyComplete(st, progress)
		if err != nil || !structurallyComplete {
			return structurallyComplete, err
		}
		return finaleWrapped(st, progress)
	}
	return layeredBookComplete(st, progress)
}

// ReconcileLayeredCompletion Căn cứ sự thật bền vững hiện tại bổ sung trạng thái hoàn kết sách phân lớp.
// save_volume_summary Đường bình thường và khôi phục sự cố Engine dùng chung đầu vào này, tránh tóm tắt tập đã lưu,
// Progress chưa kịp MarkComplete thì vĩnh viễn mất điểm kích hoạt hoàn kết tự động.
func ReconcileLayeredCompletion(st *store.Store) (bool, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil || !progress.Layered {
		return false, nil
	}
	if progress.Phase == domain.PhaseComplete {
		return true, nil
	}
	if progress.Phase != domain.PhaseWriting {
		return false, nil
	}
	complete, err := layeredComplete(st, progress)
	if err != nil || !complete {
		return complete, err
	}
	if err := st.Progress.MarkComplete(); err != nil {
		return false, fmt.Errorf("mark complete: %w", err)
	}
	return true, nil
}

// layeredBookComplete Dùng sự thật khách quan phán đoán truyện dài phân lớp đã thực sự viết xong chưa, đối chiếu danh sách phán đoán hoàn kết architect-long.md
// vài mục định lượng được + sự thật cấu trúc. Trên cơ sở cấu trúc hoàn chỉnh yêu cầu chi tiết gieo mầm về không, tuyến dài thu lại——bất kỳ cái nào không thỏa đều
// nhường chỗ cho kiến trúc sư tiếp tục expand_arc / append_volume, tuyệt đối không tranh kết thúc khi truyện chưa viết xong. Không có compass thì bảo thủ
// phán là chưa hoàn kết. Đây là phán đoán hoàn kết 'cấp chất lượng' khi chưa tuyên bố tập kết thúc, nghiêm ngặt hơn layeredStructurallyComplete.
func layeredBookComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	structurallyComplete, err := layeredStructurallyComplete(st, progress)
	if err != nil || !structurallyComplete {
		return structurallyComplete, err
	}
	// 4. Chi tiết gieo mầm hoạt động phải về 0 (lời hứa đã thực hiện)
	active, err := st.World.LoadActiveForeshadow()
	if err != nil {
		return false, fmt.Errorf("load active foreshadow: %w", err)
	}
	if len(active) > 0 {
		return false, nil
	}
	// 5. Tuyến dài hoạt động của la bàn phải thu lại (không la bàn / tuyến dài chưa rõ đều giao lại kiến trúc sư phán quyết)
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		return false, fmt.Errorf("load compass: %w", err)
	}
	if compass == nil || len(compass.OpenThreads) > 0 {
		return false, nil
	}
	return true, nil
}