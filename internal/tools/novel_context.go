package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// References Tài liệu tham khảo nhúng.
type References struct {
	// V0
	ChapterGuide      string
	HookTechniques    string
	QualityChecklist  string
	OutlineTemplate   string
	CharacterTemplate string
	ChapterTemplate   string
	// V1
	Consistency      string
	ContentExpansion string
	DialogueWriting  string
	// V2
	StyleReference   string // Tham khảo bổ sung phong cách (có thể rỗng)
	LongformPlanning string // Tham khảo quy hoạch truyện dài chung
	Differentiation  string // Tham khảo thiết kế khác biệt chung
	ArcTemplates     string // Mẫu cung thể loại (tải theo style, có thể rỗng)
	AntiAITone       string // Thư viện tiêu chí khử vị AI (writer/editor dùng chung, tiêm toàn bộ quá trình)
}

// ContextTool Lắp ráp ngữ cảnh cần thiết cho chương hiện tại.
type ContextTool struct {
	store      *store.Store
	refs       References
	style      string
	styleStats *StyleStatsIndex
}

type contextReads struct {
	warnings []string
	seen     map[string]struct{}
	err      error
}

func (r *contextReads) warn(scope string, err error) {
	if err == nil || os.IsNotExist(err) {
		return
	}
	msg := fmt.Sprintf("đọc %s thất bại: %v", scope, err)
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	if _, ok := r.seen[msg]; ok {
		return
	}
	r.seen[msg] = struct{}{}
	r.warnings = append(r.warnings, msg)
}

func (r *contextReads) require(scope string, err error) {
	if r.err != nil || err == nil || os.IsNotExist(err) || errors.Is(err, store.ErrOutlineChapterNotFound) {
		return
	}
	r.err = fmt.Errorf("đọc %s thất bại: %w", scope, err)
}

// NewContextTool Tạo công cụ ngữ cảnh. styleStats phải chia sẻ với commit_chapter,
// nếu không làm lại chương xong ngữ cảnh sẽ tiếp tục đọc thống kê cũ.
// user_rules Do buildUserRules trực tiếp đọc ảnh chụp sách này (meta/user_rules.json) tiêm, không còn phụ thuộc tùy chọn tải.
func NewContextTool(
	store *store.Store,
	refs References,
	style string,
	styleStats *StyleStatsIndex,
) *ContextTool {
	if styleStats == nil {
		panic("tools: NewContextTool requires StyleStatsIndex")
	}
	return &ContextTool{store: store, refs: refs, style: style, styleStats: styleStats}
}

func (t *ContextTool) Name() string { return "novel_context" }
func (t *ContextTool) Description() string {
	return "Lấy trạng thái hiện tại và ngữ cảnh sáng tác của tiểu thuyết." +
		"Không truyền chapter: trả về progress_status (các trường tiến độ phase/flow/next_chapter/pending_rewrites...) + thiết lập nền tảng, để phán đoán bước tiếp theo nên làm gì." +
		"Truyền chapter=N: trả thêm ngữ cảnh viết của chương đó gồm tóm tắt tình tiết trước, chi tiết gieo mầm, trạng thái nhân vật, quy tắc văn phong..."
}
func (t *ContextTool) Label() string { return "tải ngữ cảnh" }

// Công cụ chỉ đọc, có thể được lập lịch đồng thời.
func (t *ContextTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *ContextTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ContextTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("số chương. Không truyền thì trả trạng thái tiến độ và thiết lập nền tảng (Architect dùng); truyền vào thì trả thêm ngữ cảnh viết của chương đó (Writer/Editor dùng)")),
	)
}

func (t *ContextTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int `json:"chapter"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	result := make(map[string]any)
	reads := &contextReads{}

	if a.Chapter > 0 {
		// Writer Đường dẫn: tải toàn bộ dữ liệu cơ bản + ngữ cảnh chương
		t.buildBaseContext(result, reads)
		seed := newChapterContextEnvelope()
		state := t.prepareChapterContext(a.Chapter, &seed, reads)
		seed.apply(result)
		t.buildChapterContext(result, state, reads)
		// Sự thật vi phạm cơ học của chương này (khi commit kiểm tra theo user_rules và lưu):
		// editor đánh giá dựa vào đó ánh xạ vào 7 chiều (editor.md §Ánh xạ kiểm tra cơ học); writer khi làm lại tự tra.
		if violations := t.store.World.LoadRuleViolations(a.Chapter); len(violations) > 0 {
			result["rule_violations"] = violations
		}
		// episodic là bản ghi nhớ đã viết vào chính văn, không phải chất liệu chờ viết.
		if epi, ok := result["episodic_memory"].(map[string]any); ok && len(epi) > 0 {
			epi["_usage"] = "Vùng này là bản ghi nhớ dữ kiện đã ghi vào phần thân (để đối chiếu nhất quán và nối tiếp); sao chép nguyên văn nội dung này vào phần thân chương mới là lỗi lặp lại"
		}
	} else {
		// Luồng Architect: chỉ trả về trạng thái + dữ liệu cấu trúc, không tải toàn bộ chính văn
		t.buildProgressStatus(result, reads)
		t.buildArchitectContext(result, reads)
	}

	// Tiêm working_memory.user_rules (đường dẫn canonical). Đường dẫn kiến trúc sư vốn không có working_memory,
	// do buildUserRules tạo mới theo nhu cầu bình chứa chỉ đựng user_rules. Khi thiếu ảnh chụp lùi về mặc định tích hợp,
	// luôn xuất cấu trúc ổn định, tránh LLM thấy user_rules=null đi nhánh bất thường.
	if a.Chapter > 0 {
		t.buildSimulationProfile(result, "working_memory", reads)
	} else {
		t.buildSimulationProfile(result, "planning_memory", reads)
	}

	t.buildUserRules(result, reads)

	if reads.err != nil {
		return nil, reads.err
	}
	if len(reads.warnings) > 0 {
		result["_warnings"] = reads.warnings
	}

	// Ngân sách ưu tiên: khi tổng kích thước vượt ngưỡng thì cắt xén dữ liệu độ ưu tiên thấp; tóm tắt được xây dựng lại sau khi cắt xén,
	// đảm bảo số lượng trường hiển thị và _trimmed nhất quán với payload cuối cùng.
	budget := 60 * 1024
	if a.Chapter > 0 {
		budget = 100 * 1024
	}
	return finalizeContextPayload(result, a.Chapter, budget)
}

func finalizeContextPayload(result map[string]any, chapter, budget int) (json.RawMessage, error) {
	if err := trimByBudget(result, budget); err != nil {
		return nil, err
	}
	result["_loading_summary"] = buildLoadingSummary(result, chapter)
	if err := trimByBudget(result, budget); err != nil {
		return nil, err
	}
	result["_loading_summary"] = buildLoadingSummary(result, chapter)

	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal context payload: %w", err)
	}
	if len(data) > budget {
		return nil, fmt.Errorf("context payload exceeds budget after summary rebuild: size=%d budget=%d", len(data), budget)
	}
	return data, nil
}

// buildLoadingSummary Thống kê lượng dữ liệu các mục từ result đã lắp ráp, tạo một dòng tóm tắt dễ đọc.
func buildLoadingSummary(result map[string]any, chapter int) string {
	var parts []string
	working, _ := result["working_memory"].(map[string]any)
	episodic, _ := result["episodic_memory"].(map[string]any)
	planning, _ := result["planning_memory"].(map[string]any)
	foundation, _ := result["foundation_memory"].(map[string]any)
	referencePack, _ := result["reference_pack"].(map[string]any)

	if chapter > 0 {
		parts = append(parts, fmt.Sprintf("ch=%d", chapter))
		if tier, ok := episodic["planning_tier"].(domain.PlanningTier); ok && tier != "" {
			parts = append(parts, fmt.Sprintf("tier=%s", tier))
		}
	} else {
		parts = append(parts, "architect")
		if tier, ok := planning["planning_tier"].(domain.PlanningTier); ok && tier != "" {
			parts = append(parts, fmt.Sprintf("tier=%s", tier))
		}
	}

	if pos, ok := episodic["position"].(map[string]any); ok {
		parts = append(parts, fmt.Sprintf("V%dA%d", pos["volume"], pos["arc"]))
	}

	var items []string

	if n := firstSliceLen(episodic["character_snapshots"], foundation["character_snapshots"]); n > 0 {
		items = append(items, fmt.Sprintf("nhân vật:%d(ảnh chụp)", n))
	} else if n := firstSliceLen(episodic["characters"], foundation["characters"]); n > 0 {
		items = append(items, fmt.Sprintf("nhân vật:%d", n))
	}

	if len(working) > 0 {
		items = append(items, fmt.Sprintf("working_memory:%d", len(working)))
	}
	if len(episodic) > 0 {
		items = append(items, fmt.Sprintf("episodic:%d", len(episodic)))
	}
	if len(planning) > 0 {
		items = append(items, fmt.Sprintf("planning:%d", len(planning)))
	}
	if len(foundation) > 0 {
		items = append(items, fmt.Sprintf("foundation:%d", len(foundation)))
	}

	if n := firstSliceLen(working["volume_summaries"], planning["volume_summaries"]); n > 0 {
		items = append(items, fmt.Sprintf("tóm tắt tập:%d", n))
	}
	if n := firstSliceLen(working["arc_summaries"], planning["arc_summaries"]); n > 0 {
		items = append(items, fmt.Sprintf("tóm tắt arc:%d", n))
	}
	if n := sliceLen(working["recent_summaries"]); n > 0 {
		items = append(items, fmt.Sprintf("tóm tắt chương:%d", n))
	}

	if n := sliceLen(planning["layered_outline"]); n > 0 {
		items = append(items, fmt.Sprintf("đại cương phân tầng:%d tập", n))
	}

	if n := sliceLen(working["timeline"]); n > 0 {
		items = append(items, fmt.Sprintf("dòng thời gian:%d", n))
	}
	if n := firstSliceLen(episodic["foreshadow_ledger"], foundation["foreshadow_ledger"]); n > 0 {
		items = append(items, fmt.Sprintf("chi tiết gieo mầm:%d", n))
	}
	if n := sliceLen(episodic["relationship_state"]); n > 0 {
		items = append(items, fmt.Sprintf("quan hệ:%d", n))
	}
	if n := sliceLen(episodic["recent_state_changes"]); n > 0 {
		items = append(items, fmt.Sprintf("thay đổi trạng thái:%d", n))
	}
	if _, ok := working["previous_tail"]; ok {
		items = append(items, "đuôi chương trước:ok")
	}
	if _, ok := referencePack["style_rules"]; ok {
		items = append(items, "quy tắc văn phong:ok")
	}
	if n := sliceLen(episodic["related_chapters"]); n > 0 {
		items = append(items, fmt.Sprintf("chương liên quan:%d", n))
	}
	if selected, ok := result["selected_memory"].(map[string]any); ok && len(selected) > 0 {
		if n := sliceLen(selected["story_threads"]); n > 0 {
			items = append(items, fmt.Sprintf("gọi lại manh mối:%d", n))
		}
		if n := sliceLen(selected["review_lessons"]); n > 0 {
			items = append(items, fmt.Sprintf("gọi lại đọc kiểm:%d", n))
		}
	}

	if refs, ok := referencePack["references"].(map[string]string); ok && len(refs) > 0 {
		items = append(items, fmt.Sprintf("tham chiếu:%d mục", len(refs)))
	}
	if len(referencePack) > 0 {
		items = append(items, fmt.Sprintf("gói tham chiếu:%d", len(referencePack)))
	}
	if _, ok := result["memory_policy"]; ok {
		items = append(items, "chiến lược bộ nhớ:ok")
	}
	if _, ok := working["simulation_profile"]; ok {
		items = append(items, "chân dung phỏng văn:ok")
	} else if _, ok := planning["simulation_profile"]; ok {
		items = append(items, "chân dung phỏng văn:ok")
	}
	if warnings, ok := result["_warnings"].([]string); ok && len(warnings) > 0 {
		items = append(items, fmt.Sprintf("cảnh báo:%d", len(warnings)))
	}
	if trimmed, ok := result["_trimmed"].([]string); ok && len(trimmed) > 0 {
		items = append(items, fmt.Sprintf("cắt gọn:%s", strings.Join(trimmed, ",")))
	}

	if len(items) > 0 {
		parts = append(parts, strings.Join(items, " "))
	}
	return strings.Join(parts, " | ")
}

// sliceLen Thử lấy độ dài slice đối với kiểu any.
func sliceLen(v any) int {
	switch s := v.(type) {
	case []domain.ChapterSummary:
		return len(s)
	case []domain.ArcSummary:
		return len(s)
	case []domain.VolumeSummary:
		return len(s)
	case []domain.CharacterSnapshot:
		return len(s)
	case []domain.TimelineEvent:
		return len(s)
	case []domain.ForeshadowEntry:
		return len(s)
	case []domain.RelationshipEntry:
		return len(s)
	case []domain.StateChange:
		return len(s)
	case []domain.VolumeOutline:
		return len(s)
	case []domain.Character:
		return len(s)
	case []domain.RelatedChapter:
		return len(s)
	case []domain.RecallItem:
		return len(s)
	case []planningVolumeOutline:
		return len(s)
	default:
		return 0
	}
}

func firstSliceLen(values ...any) int {
	for _, value := range values {
		if n := sliceLen(value); n > 0 {
			return n
		}
	}
	return 0
}

// loadFilteredCharacters Lọc nhân vật theo Tier và xuất hiện trong cảnh.
// core/important luôn trả về; secondary/decorative chỉ trả về khi đại cương chương hiện tại nhắc đến.
func (t *ContextTool) loadFilteredCharacters(result map[string]any, chapter int, reads *contextReads) {
	chars, err := t.store.Characters.Load()
	if err != nil {
		reads.require("characters", err)
		return
	}
	if len(chars) == 0 {
		return
	}

	// Lấy mô tả cảnh của đại cương chương hiện tại, dùng để khớp nhân vật phụ
	entry, err := t.store.Outline.GetChapterOutline(chapter)
	if err != nil {
		reads.require("current_chapter_outline", err)
		result["characters"] = chars
		return
	}
	if entry == nil {
		result["characters"] = chars
		return
	}
	sceneText := strings.Join(entry.Scenes, " ") + " " + entry.CoreEvent + " " + entry.Title

	var filtered []domain.Character
	for _, c := range chars {
		switch c.Tier {
		case "secondary", "decorative":
			if matchCharacter(sceneText, c) {
				filtered = append(filtered, c)
			}
		default: // core, important, hoặc chưa thiết lập
			filtered = append(filtered, c)
		}
	}
	result["characters"] = filtered
}

// matchCharacter Kiểm tra văn bản cảnh có chứa tên chính thức hoặc bất kỳ bí danh nào của nhân vật không.
func matchCharacter(text string, c domain.Character) bool {
	if strings.Contains(text, c.Name) {
		return true
	}
	for _, alias := range c.Aliases {
		if strings.Contains(text, alias) {
			return true
		}
	}
	return false
}

// loadLayeredSummaries Tải tóm tắt phân lớp: tóm tắt tập + tóm tắt arc tập hiện tại + tóm tắt chương trong arc.
func (t *ContextTool) loadLayeredSummaries(result map[string]any, chapter, summaryWindow int, reads *contextReads) {
	vol, arc, err := t.store.Outline.LocateChapter(chapter)
	if err != nil {
		reads.require("layered_outline_position", err)
		return
	}

	// 1. Tóm tắt tập của tập đã hoàn thành
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		result["volume_summaries"] = volSummaries
	} else {
		reads.require("volume_summaries", err)
	}

	// 2. Tóm tắt arc của arc đã hoàn thành trong tập hiện tại (không gồm arc hiện tại)
	if arcSummaries, err := t.store.Summaries.LoadArcSummaries(vol); err == nil && len(arcSummaries) > 0 {
		var prior []domain.ArcSummary
		for _, s := range arcSummaries {
			if s.Arc < arc {
				prior = append(prior, s)
			}
		}
		if len(prior) > 0 {
			result["arc_summaries"] = prior
		}
	} else {
		reads.require("arc_summaries", err)
	}

	// 3. Tóm tắt chương của N chương gần nhất trong arc hiện tại
	if summaries, err := t.store.Summaries.LoadRecentSummaries(chapter, summaryWindow); err == nil && len(summaries) > 0 {
		result["recent_summaries"] = summaries
	} else {
		reads.require("recent_summaries", err)
	}
}

// loadLayeredCharacters Tải nhân vật ở chế độ Layered: ưu tiên dùng ảnh chụp gần nhất, lùi về thiết lập ban đầu + lọc Tier.
func (t *ContextTool) loadLayeredCharacters(result map[string]any, chapter int, reads *contextReads) {
	snapshots, err := t.store.Characters.LoadLatestSnapshots()
	if err == nil && len(snapshots) > 0 {
		result["character_snapshots"] = snapshots
		// Đồng thời giữ nhân vật core/important trong thiết lập ban đầu (ảnh chụp có thể không chứa nhân vật mới xuất hiện)
		t.loadFilteredCharacters(result, chapter, reads)
		return
	}
	reads.require("character_snapshots", err)
	// Khi không có ảnh chụp lùi về thiết lập ban đầu
	t.loadFilteredCharacters(result, chapter, reads)
}

// writerReferences Trả về tài liệu tham khảo sáng tác. Chương 1 trả về toàn lượng, các chương sau cắt bỏ mẫu không còn cần thiết.
func (t *ContextTool) writerReferences(chapter int) map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	// Tải dần: luôn giữ tham khảo cốt lõi, 3 chương đầu tải thêm hướng dẫn viết đầy đủ
	add("consistency", t.refs.Consistency)
	add("hook_techniques", t.refs.HookTechniques)
	add("quality_checklist", t.refs.QualityChecklist)
	add("anti_ai_tone", t.refs.AntiAITone) // Tiêu chí khử vị AI tiêm toàn bộ quá trình, không cắt xén theo chương
	if chapter <= 3 {
		add("chapter_guide", t.refs.ChapterGuide)
		add("dialogue_writing", t.refs.DialogueWriting)
		add("style_reference", t.refs.StyleReference)
	}

	// Tham khảo bổ sung chỉ tải ở chương đầu
	if chapter <= 1 {
		add("chapter_template", t.refs.ChapterTemplate)
		add("content_expansion", t.refs.ContentExpansion)
	}
	return refs
}

func (t *ContextTool) architectReferences() map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	add("outline_template", t.refs.OutlineTemplate)
	add("character_template", t.refs.CharacterTemplate)
	add("longform_planning", t.refs.LongformPlanning)
	add("differentiation", t.refs.Differentiation)
	add("style_reference", t.refs.StyleReference)
	add("arc_templates", t.refs.ArcTemplates)
	add("anti_ai_tone", t.refs.AntiAITone) // đại cương architect khử giọng AI; cũng đỡ editor đi đường Chapter=0
	return refs
}

// foundationStatus Kiểm tra tính hoàn thiện của thiết lập cơ sở, trả về danh sách mục thiếu.
// Dùng chung logic phán đoán store.FoundationMissing với công cụ save_foundation, đảm bảo LLM từ
// novel_context nhìn thấy ready/missing nhất quán với foundation_ready trả về từ save_foundation
// vĩnh viễn nhất quán (chi tiết như mục bắt buộc của compass truyện dài không bị trôi).
func (t *ContextTool) foundationStatus() (map[string]any, error) {
	missing, err := t.store.FoundationMissing()
	if err != nil {
		return nil, err
	}
	status := map[string]any{"ready": len(missing) == 0}
	if len(missing) > 0 {
		status["missing"] = missing
	}
	if len(missing) == 1 && missing[0] == "foundation_audit" {
		fingerprint, err := t.store.FoundationFingerprint()
		if err != nil {
			return nil, err
		}
		status["fingerprint"] = fingerprint
	}
	if audit, err := t.store.Outline.LoadFoundationAudit(); err != nil {
		return nil, err
	} else if audit != nil && !audit.Ready {
		status["last_audit"] = audit
	}
	return status, nil
}

// trimByBudget Cắt xén result theo độ ưu tiên, để tổng kích thước JSON không vượt quá số byte ngân sách.
// Độ ưu tiên (từ thấp đến cao): references < voice_samples < style_anchors < previous_tail < timeline
//
//	< recent_state_changes < foreshadow_ledger < relationship_state < còn lại (không cắt)
//
// style_stats là tín hiệu cốt lõi cấp toàn sách có giới hạn thể tích, không tham gia cắt xén.
//
// Khóa bị cắt xén sẽ ghi vào result["_trimmed"] để tra log.
func trimByBudget(result map[string]any, budget int) error {
	// Đo kích thước hiện tại trước
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("measure context payload: %w", err)
	}
	if len(data) <= budget {
		return nil
	}

	// Liệt kê các khóa có thể cắt xén theo độ ưu tiên từ thấp đến cao
	trimOrder := []string{
		"references",
		"voice_samples",
		"style_anchors",
		"style_rules",
		"previous_tail",
		"timeline",
		"recent_state_changes",
		"foreshadow_ledger",
		"relationship_state",
	}

	trimmed, _ := result["_trimmed"].([]string)
	trimmed = append([]string(nil), trimmed...)
	for _, key := range trimOrder {
		if !deleteContextKey(result, key) {
			continue
		}
		trimmed = append(trimmed, key)
		result["_trimmed"] = append([]string(nil), trimmed...)
		data, err = json.Marshal(result)
		if err != nil {
			return fmt.Errorf("measure trimmed context payload: %w", err)
		}
		if len(data) <= budget {
			return nil
		}
	}
	return fmt.Errorf("context payload exceeds budget after trimming: size=%d budget=%d", len(data), budget)
}

func deleteContextKey(result map[string]any, key string) bool {
	deleted := false
	for _, containerKey := range []string{
		"working_memory",
		"episodic_memory",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"selected_memory",
	} {
		section, ok := result[containerKey].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := section[key]; ok {
			delete(section, key)
			deleted = true
		}
	}
	return deleted
}

// buildRelatedChapters Tra cứu ngược các chương lịch sử liên quan đến chương hiện tại dựa trên Dữ liệu cấu trúc.
// Đề xuất từ 4 chiều: chi tiết gieo mầm, nhân vật xuất hiện, trạng thái thay đổi, quan hệ, sau khi bỏ trùng tối đa trả về 5 mục.
// Tất cả dữ liệu truyền qua tham số, không làm IO phụ.
func (t *ContextTool) buildRelatedChapters(
	chapter int,
	entry *domain.OutlineEntry,
	foreshadow []domain.ForeshadowEntry,
	relationships []domain.RelationshipEntry,
	stateChanges []domain.StateChange,
	reads *contextReads,
) []domain.RelatedChapter {
	const recentWindow = 10
	const maxResults = 5

	seen := make(map[int]struct{})
	var results []domain.RelatedChapter
	add := func(ch int, reason string) {
		if ch <= 0 || ch >= chapter {
			return
		}
		// Vài chương gần đây quá gần, không đề xuất
		if ch > chapter-recentWindow {
			return
		}
		if _, ok := seen[ch]; ok {
			return
		}
		seen[ch] = struct{}{}
		results = append(results, domain.RelatedChapter{Chapter: ch, Reason: reason})
	}

	// Ghép văn bản đại cương để khớp từ khóa
	outlineText := entry.Title + " " + entry.CoreEvent
	for _, s := range entry.Scenes {
		outlineText += " " + s
	}

	// 1. Tra ngược chi tiết gieo mầm: mô tả của chi tiết gieo mầm hoạt động có liên quan đến đại cương chương hiện tại không
	for _, f := range foreshadow {
		if strings.Contains(outlineText, f.ID) || containsAny(outlineText, strings.Fields(f.Description)) {
			add(f.PlantedAt, fmt.Sprintf("chương gieo chi tiết %s(%s)", f.ID, truncateRunes(f.Description, 15)))
		}
		if len(results) >= maxResults {
			break
		}
	}

	// 2. Tra ngược nhân vật xuất hiện: duyệt hàng loạt một lần, IO giảm từ O(số nhân vật x số chương) xuống O(số chương)
	chars, err := t.store.Characters.Load()
	if err != nil {
		reads.warn("related_chapters.characters", err)
	}
	outlineChars := matchOutlineCharacters(outlineText, chars)
	if len(outlineChars) > 0 {
		appearances, err := t.store.Summaries.FindCharacterAppearances(outlineChars, chapter, recentWindow)
		if err != nil {
			reads.warn("related_chapters.summaries", err)
		}
		for _, name := range outlineChars {
			if len(results) >= maxResults {
				break
			}
			if ch, ok := appearances[name]; ok {
				add(ch, fmt.Sprintf("chương nhân vật '%s' xuất hiện cuối", name))
			}
		}
	}

	// 3. Tra ngược thay đổi trạng thái: thao tác trên slice đã tải, zero IO
	for _, name := range outlineChars {
		if len(results) >= maxResults {
			break
		}
		ch := findLastStateChange(stateChanges, name, chapter)
		if ch > 0 && ch <= chapter-recentWindow {
			add(ch, fmt.Sprintf("chương thay đổi trạng thái '%s'", name))
		}
	}

	// 4. Tra ngược quan hệ: thay đổi cuối cùng của quan hệ giữa các cặp nhân vật liên quan đến chương hiện tại
	if len(relationships) > 0 && len(outlineChars) >= 2 {
		charSet := make(map[string]struct{}, len(outlineChars))
		for _, c := range outlineChars {
			charSet[c] = struct{}{}
		}
		for _, r := range relationships {
			if len(results) >= maxResults {
				break
			}
			_, aIn := charSet[r.CharacterA]
			_, bIn := charSet[r.CharacterB]
			if aIn && bIn {
				add(r.Chapter, fmt.Sprintf("thay đổi quan hệ %s-%s", r.CharacterA, r.CharacterB))
			}
		}
	}

	return results
}

// findLastStateChange Tìm số chương thay đổi gần nhất của thực thể trong danh sách trạng thái thay đổi đã tải.
func findLastStateChange(changes []domain.StateChange, entity string, currentChapter int) int {
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].Entity == entity && changes[i].Chapter < currentChapter {
			return changes[i].Chapter
		}
	}
	return 0
}

// matchOutlineCharacters Khớp tên nhân vật xuất hiện từ văn bản đại cương.
func matchOutlineCharacters(text string, chars []domain.Character) []string {
	var matched []string
	for _, c := range chars {
		if strings.Contains(text, c.Name) {
			matched = append(matched, c.Name)
			continue
		}
		for _, alias := range c.Aliases {
			if strings.Contains(text, alias) {
				matched = append(matched, c.Name)
				break
			}
		}
	}
	return matched
}

// containsAny Kiểm tra text có chứa bất kỳ từ nào trong words không (ít nhất 2 chữ mới khớp, tránh nhiễu).
func containsAny(text string, words []string) bool {
	for _, w := range words {
		if len([]rune(w)) >= 2 && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func (t *ContextTool) selectStoryThreads(state contextBuildState) []domain.RecallItem {
	if state.currentEntry == nil {
		return nil
	}
	if len(state.foreshadow) < storyThreadRecallThreshold {
		return nil
	}

	const maxThreads = 5
	var items []domain.RecallItem
	seen := make(map[string]struct{})
	picked := make(map[string]struct{}) // ID chi tiết gieo mầm đã chọn, để điền tuổi sổ bỏ trùng
	add := func(item domain.RecallItem) {
		key := item.Kind + "|" + item.Key + "|" + item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		picked[item.Key] = struct{}{}
		items = append(items, item)
	}

	// 1. Gọi lại tính liên quan: chi tiết gieo mầm trùng lặp với từ focus của chương hiện tại.
	focusTerms := recallFocusTerms(state.currentEntry, state.chapterPlan)
	focusText := strings.Join(focusTerms, " ")
	for _, entry := range state.foreshadow {
		if !matchesRecallTerms(entry.ID+" "+entry.Description, focusTerms) && !strings.Contains(focusText, entry.ID) {
			continue
		}
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "chương hiện tại có thể cần nối tiếp chi tiết gieo mầm sẵn có",
			Summary: fmt.Sprintf("chi tiết gieo mầm “%s” gieo tại chương %d: %s", entry.ID, entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			return items
		}
	}

	// 2. Điền tuổi sổ: chi tiết gieo mầm không liên quan chương hiện tại nhưng treo lâu chưa thu hồi (cũ nhất ưu tiên), bù đủ suất còn lại.
	//    Bù đắp vùng mù tự nhiên của gọi lại tính liên quan——tuyến treo đơn độc quá lâu, nhưng không đụng từ khóa trong chương này.
	for _, entry := range agingForeshadow(state.foreshadow, state.chapter, picked) {
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "chi tiết gieo mầm treo lâu chưa thu, chú ý đẩy hoặc thu đúng lúc",
			Summary: fmt.Sprintf("chi tiết gieo mầm “%s” gieo tại chương %d, đã %d chương chưa thu: %s", entry.ID, entry.PlantedAt, state.chapter-entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			break
		}
	}

	return items
}

// agingForeshadow Trả về chi tiết gieo mầm chưa thu hồi có tuổi sổ ≥ foreshadowAgingChapters, sắp xếp ưu tiên cũ nhất,
// bỏ qua các mục đã được gọi lại tính liên quan chọn trong picked. Tham số all đã là danh sách active (chưa thu hồi), nên không cần lọc trạng thái nữa.
func agingForeshadow(all []domain.ForeshadowEntry, chapter int, picked map[string]struct{}) []domain.ForeshadowEntry {
	var aging []domain.ForeshadowEntry
	for _, e := range all {
		if _, ok := picked[e.ID]; ok {
			continue
		}
		if e.PlantedAt <= 0 || chapter-e.PlantedAt < foreshadowAgingChapters {
			continue
		}
		aging = append(aging, e)
	}
	sort.SliceStable(aging, func(i, j int) bool {
		return aging[i].PlantedAt < aging[j].PlantedAt
	})
	return aging
}

func (t *ContextTool) selectReviewLessons(chapter int, reads *contextReads) []domain.RecallItem {
	if chapter <= 1 {
		return nil
	}

	var items []domain.RecallItem
	seen := make(map[string]struct{})
	add := func(item domain.RecallItem) {
		key := item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}

	appendReview := func(review *domain.ReviewEntry) bool {
		if review == nil {
			return false
		}
		for i, miss := range review.ContractMisses {
			add(domain.RecallItem{
				Kind:    "review_lesson",
				Key:     fmt.Sprintf("review-%d-contract-%d", review.Chapter, i),
				Chapter: review.Chapter,
				Reason:  "lần đọc kiểm gần đây chỉ ra contract còn thiếu mục",
				Summary: fmt.Sprintf("chương %d contract thiếu mục: %s", review.Chapter, miss),
			})
			if len(items) >= 3 {
				return true
			}
		}
		for i, issue := range review.Issues {
			switch issue.Severity {
			case "", "warning", "error", "critical":
				add(domain.RecallItem{
					Kind:    "review_lesson",
					Key:     fmt.Sprintf("review-%d-issue-%d", review.Chapter, i),
					Chapter: review.Chapter,
					Reason:  "lần đọc kiểm gần đây chỉ ra cần tránh lặp vấn đề",
					Summary: fmt.Sprintf("nhắc đọc kiểm chương %d: %s", review.Chapter, truncateRunes(issue.Description, 36)),
				})
			}
			if len(items) >= 3 {
				return true
			}
		}
		return false
	}

	for ch := chapter - 1; ch >= max(chapter-3, 1); ch-- {
		review, err := t.store.World.LoadReview(ch)
		if err != nil {
			reads.warn("review", err)
			continue
		}
		if appendReview(review) {
			return items
		}
	}

	globalReview, err := t.store.World.LoadLastReview(chapter - 1)
	if err != nil {
		reads.warn("global_review", err)
	} else if appendReview(globalReview) {
		return items
	}
	return items
}

func recallFocusTerms(entry *domain.OutlineEntry, plan *domain.ChapterPlan) []string {
	if entry == nil {
		return nil
	}
	var terms []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			terms = append(terms, v)
		}
	}

	add(entry.Title)
	add(entry.CoreEvent)
	add(entry.Hook)
	for _, scene := range entry.Scenes {
		add(scene)
	}
	if plan != nil {
		add(plan.Goal)
		add(plan.Hook)
		for _, point := range plan.Contract.PayoffPoints {
			add(point)
		}
		add(plan.Contract.HookGoal)
	}
	return terms
}

func matchesRecallTerms(text string, terms []string) bool {
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 {
			continue
		}
		if strings.Contains(text, term) || strings.Contains(term, text) {
			return true
		}
		if hasMeaningfulOverlap(term, text) {
			return true
		}
	}
	return false
}

func hasMeaningfulOverlap(a, b string) bool {
	ar := []rune(strings.TrimSpace(a))
	br := []rune(strings.TrimSpace(b))
	if len(ar) < 5 || len(br) < 5 {
		return false
	}
	shorter := len(ar)
	if len(br) < shorter {
		shorter = len(br)
	}
	threshold := 5
	switch {
	case shorter >= 12:
		threshold = 7
	case shorter >= 9:
		threshold = 6
	}
	return longestCommonSubstringRunes(ar, br) >= threshold
}

const storyThreadRecallThreshold = 6
const storyThreadRecallMinSelected = 2

// foreshadowAgingChapters：Một chi tiết gieo mầm kể từ khi chôn quá nhiều chương vẫn chưa thu hồi, coi là 'treo lâu'.
// Loại chi tiết này dù không liên quan từ khóa chương hiện tại, cũng điền vào story_threads, tránh bị lãng quên hoàn toàn trong truyện dài
// (gọi lại tính liên quan vốn chỉ thấy tuyến liên quan chương này, không thấy tuyến treo đơn độc quá lâu).
// Tuổi sổ là sự thật suy ra từ mã thuần (chương hiện tại - chương chôn), chỉ trần thuật 'đã treo N chương chưa thu hồi', không ra lệnh.
const foreshadowAgingChapters = 30

func longestCommonSubstringRunes(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] != b[j-1] {
				continue
			}
			curr[j] = prev[j-1] + 1
			if curr[j] > best {
				best = curr[j]
			}
		}
		prev = curr
	}
	return best
}

// truncateRunes Cắt chuỗi đến số rune chỉ định.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}