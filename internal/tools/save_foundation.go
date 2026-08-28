package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveFoundationTool Lưu thiết lập cơ bản (premise/outline/characters), Architect chuyên dùng.
type SaveFoundationTool struct {
	store *store.Store
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "Lưu thiết lập nền tảng tiểu thuyết (premise/outline/characters/world_rules/compass...). **Đây là cửa bền vững duy nhất**: nội dung không được lưu qua lời gọi tool này sẽ không vào store, chỉ xuất Markdown/JSON trong tin nhắn coi như mất. Tham số cố định {type, content, scale?, volume?, arc?}. type chọn premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book. Với premise, content phải là chuỗi Markdown; các kiểu khác content ưu tiên truyền thẳng mảng hoặc đối tượng JSON. expand_arc hiệu chỉnh và triển khai một arc khung chưa viết (cần volume + arc, content là {title, goal, chapters}, có thể sửa mục tiêu khung gốc theo phần thân đã hoàn thành); append_volume thêm tập mới (content là VolumeOutline JSON đầy đủ, gồm cấu trúc arc; tầng trên kèm \"final\": true tức tuyên bố tập ca nhận — toàn sách thu ở tập này, viết xong mọi chương là tự hoàn thành, không cần gọi complete_book nữa); update_compass cập nhật hướng kết thúc (content là StoryCompass JSON); complete_book tuyên bố toàn sách hoàn thành (content truyền đối tượng rỗng {}, trực tiếp đẩy Phase=Complete; tool sẽ kiểm tra: mọi chương trong đại cương đã viết xong, hàng làm lại trống, compass không còn open_threads chưa thu — muốn xác nhận trục dài đã thu hết phải update_compass xóa rỗng open_threads ghi xuống đĩa trước, muốn thu sớm dùng final tập ca nhận của append_volume). append_volume / complete_book bắt buộc kèm tham số reason (lý do phán quyết trong một câu, đối chiếu danh sách kiểm hoàn thành, ghi vào kiểm toán phán quyết). scale tùy chọn, chỉ cho short / mid / long."
}
func (t *SaveFoundationTool) Label() string { return "lưu thiết lập" }

// Công cụ ghi (cập nhật chéo miền Outline/Progress/Characters), cấm đồng thời.
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("kiểu thiết lập", "premise", "outline", "layered_outline", "characters", "world_rules", "expand_arc", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": "nội dung. premise truyền chuỗi Markdown; các kiểu khác truyền thẳng mảng hoặc đối tượng JSON là được, cũng tương thích truyền chuỗi JSON. Với expand_arc truyền {title, goal, chapters}, title/goal là quy hoạch arc mục tiêu đã hiệu chỉnh theo dữ kiện hoàn thành.",
		}).Required(),
		schema.Property("scale", schema.Enum("cấp quy hoạch", "short", "mid", "long")),
		schema.Property("volume", schema.Int("số thứ tự tập mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("arc", schema.Int("số thứ tự arc mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("reason", schema.String("lý do phán quyết cuối tập (bắt buộc khi append_volume / complete_book): đối chiếu danh sách kiểm hoàn thành, một câu nêu vì sao nối tập, tuyên bố ca nhận hay hoàn thành")),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Scale   string          `json:"scale"`
		Volume  int             `json:"volume"`
		Arc     int             `json:"arc"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("invalid scale %q, expected short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// Đại cương toàn lượng chỉ thuộc về kỳ quy hoạch. Kỳ sáng tác phải dùng thao tác tăng cường được bảo vệ, sau khi hoàn kết phải mở lại trước;
	// nếu không sẽ bỏ qua bảo vệ chương đã hoàn thành, phá vỡ tính nhất quán giữa Progress và sự thật chương.
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("check foundation phase: %w: %w", errs.ErrStoreRead, err)
	}
	if (a.Type == "outline" || a.Type == "layered_outline") && progress != nil {
		switch progress.Phase {
		case domain.PhaseWriting:
			return nil, fmt.Errorf(
				"giai đoạn viết cấm dùng %s ghi đè toàn lượng đại cương. Hãy dùng revise_outline sửa chương chưa diễn ra, expand_arc triển khai arc khung, hoặc append_volume thêm tập mới: %w",
				a.Type, errs.ErrToolPrecondition)
		case domain.PhaseComplete:
			return nil, fmt.Errorf(
				"toàn sách đã hoàn thành, cấm dùng %s ghi đè toàn lượng đại cương. Hãy mở lại tác phẩm trước, rồi mới dùng thao tác sửa đại cương hoặc viết tiếp được bảo hộ: %w",
				a.Type, errs.ErrToolPrecondition)
		}
	}
	if a.Scale != "" {
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// Chọn một trong ba ở cuối tập (tiếp tục/kết thúc/hoàn kết) là phán đoán ngữ nghĩa nặng nhất toàn sách, lý do phải trở thành sự thật kiểm toán
	// (decisions.jsonl, cùng một luồng với plan_start/intervention), nếu không kết thúc quá sớm/
	// tiếp tục tập không đúng chỉ có thể lật nhật ký hội thoại gỡ lỗi. Ảnh chụp sự thật lấy tiến độ thời điểm phán đoán (trước khi lưu thay đổi).
	volumeEnd := a.Type == "append_volume" || a.Type == "complete_book"
	if volumeEnd && strings.TrimSpace(a.Reason) == "" {
		return nil, fmt.Errorf("%s bắt buộc kèm tham số reason: đối chiếu danh sách kiểm hoàn thành, một câu nêu vì sao lần này nối tập, tuyên bố ca nhận hay hoàn thành: %w", a.Type, errs.ErrToolArgs)
	}
	var volumeEndFacts json.RawMessage
	if volumeEnd {
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress for volume-end facts: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			facts := map[string]any{"completed_chapters": len(p.CompletedChapters)}
			if p.Layered {
				outline, outlineErr := t.store.Outline.LoadOutline()
				if outlineErr != nil {
					return nil, fmt.Errorf("load outlined chapters for volume-end facts: %w: %w", errs.ErrStoreRead, outlineErr)
				}
				facts["dynamic_planning"] = true
				facts["outlined_chapters"] = len(outline)
			} else {
				facts["total_chapters"] = p.TotalChapters
			}
			volumeEndFacts, err = json.Marshal(facts)
			if err != nil {
				return nil, fmt.Errorf("marshal volume-end facts: %w", err)
			}
		}
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("save premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhasePremise); err != nil {
			return nil, fmt.Errorf("update premise phase: %w: %w", errs.ErrStoreWrite, err)
		}

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(len(entries)); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			if err := t.store.Progress.SetLayered(false); err != nil {
				return nil, fmt.Errorf("disable layered mode: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Progress.UpdateVolumeArc(0, 0); err != nil {
				return nil, fmt.Errorf("reset volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Outline.ClearLayeredOutline(); err != nil {
				return nil, fmt.Errorf("clear layered outline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("save layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.EstimatedChapterCapacity(volumes)
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(total); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetLayered(true); err != nil {
			return nil, fmt.Errorf("enable layered mode: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			if err := t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index); err != nil {
				return nil, fmt.Errorf("set initial volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["volumes"] = len(volumes)
		result["dynamic_planning"] = true
		result["outlined_chapters"] = len(domain.FlattenOutline(volumes))

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("save characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("save world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var expansion domain.ArcExpansion
		if err := decode("expand_arc", &expansion); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, expansion); err != nil {
			return nil, fmt.Errorf("expand arc: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["title"] = expansion.Title
		result["goal"] = expansion.Goal
		result["chapters"] = len(expansion.Chapters)
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	case "append_volume":
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("toàn sách đã hoàn thành (phase=complete), không cho phép thêm tập mới: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		prior, err := t.store.Outline.LoadLayeredOutline()
		if err != nil {
			return nil, fmt.Errorf("load layered outline: %w: %w", errs.ErrStoreRead, err)
		}
		if err := t.store.AppendVolume(vol); err != nil {
			return nil, fmt.Errorf("append volume: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = vol.Index
		if vol.Final {
			result["final_volume"] = true
		} else if domain.FinaleVolume(prior) > 0 {
			// Phản hồi sự thật: trạng thái kết thúc đã tuyên bố trước đó bị hủy do thêm tập mới bình thường (tập mới thành tập cuối)
			result["finale_released"] = true
		}
		result["arcs"] = len(vol.Arcs)
		chCount := 0
		for _, arc := range vol.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	case "complete_book":
		// Đầu vào duy nhất hoàn kết toàn sách: đẩy trực tiếp Phase=Complete.
		// Chỉ cho phép ở giai đoạn Writing, ngăn gọi nhầm ở giai đoạn quy hoạch bỏ qua toàn bộ viết sách.
		// Từ chối gọi khi có hàng đợi làm lại——đảm bảo PendingRewrites chạy xong mới có thể kết thúc.
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book chỉ gọi được ở giai đoạn writing (phase=%s hiện tại): %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("còn %d chương trong hàng làm lại, xử lý xong rồi mới gọi complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		// Kiểm tra trước hoàn bản có thể liệt kê phải ở tầng mã (phương pháp chia 3), không thể chỉ dựa vào
		// 'danh sách phán đoán hoàn kết' trong từ gợi ý——sự cố thực: quy hoạch vừa lưu phase chuyển sang writing, mô hình yếu thuận tay
		// gọi nhầm complete_book, chương 0/68 bị đánh dấu hoàn bản trực tiếp.
		if len(progress.CompletedChapters) == 0 {
			return nil, fmt.Errorf("chưa viết chương nào thì không thể hoàn thành sách; sau khi quy hoạch xong, việc viết do hệ thống tự đẩy, không cần gọi complete_book: %w", errs.ErrToolPrecondition)
		}
		next := progress.NextChapter()
		if progress.Layered {
			outline, outlineErr := t.store.Outline.LoadOutline()
			if outlineErr != nil {
				return nil, fmt.Errorf("load outlined chapters: %w: %w", errs.ErrStoreRead, outlineErr)
			}
			if next <= len(outline) {
				return nil, fmt.Errorf("đại cương chi tiết hiện tại còn chương chưa viết (chương kế %d/đã chi tiết %d), không thể hoàn thành; muốn thu sớm hãy đổi sang append_volume và tầng trên JSON tập kèm \"final\": true để tuyên bố tập ca nhận: %w", next, len(outline), errs.ErrToolPrecondition)
			}
		} else if progress.TotalChapters > 0 && next <= progress.TotalChapters {
			return nil, fmt.Errorf("trong đại cương còn chương chưa viết (chương kế %d/tổng %d), không thể hoàn thành; muốn thu sớm hãy đổi sang append_volume và tầng trên JSON tập kèm \"final\": true để tuyên bố tập ca nhận: %w", next, progress.TotalChapters, errs.ErrToolPrecondition)
		}
		// Tuyến dài hoạt động chưa thu lại không thể hoàn bản——Giao ước trường của OpenThreads là 'cần thu lại mới có kết cục'. Đây không phải
		// phán lại ngữ nghĩa: thực sự cho rằng đã thu lại hết, trước tiên update_compass làm trống open_threads rồi mới hoàn bản, biến
		// 'miễn trừ trong luận thuật' thành hành động lưu có thể kiểm toán (thực tế khi nhập sách đã hoàn bản viết tiếp, kiến trúc sư trích dẫn kinh điển vòng qua
		// điều 3 danh sách hoàn kết để hoàn bản trực tiếp, yêu cầu viết tiếp của người dùng bị khóa bởi quy tắc hoàn bản).
		compass, err := t.store.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("load compass: %w: %w", errs.ErrStoreRead, err)
		}
		if compass != nil && len(compass.OpenThreads) > 0 {
			return nil, fmt.Errorf("compass còn %d trục dài đang hoạt động chưa thu (như: %s), không thể hoàn thành. Đã xác nhận thu hết thì trước hết update_compass xóa rỗng open_threads rồi gọi complete_book; còn cần triển khai hãy append_volume (có thể kèm \"final\": true tuyên bố tập ca nhận): %w",
				len(compass.OpenThreads), compass.OpenThreads[0], errs.ErrToolPrecondition)
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("mark complete: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		// Tầng công cụ ép đè LastUpdated thành số chương đã hoàn thành hiện tại, không tin LLM tự điền.
		// LLM thường quên điền hoặc để 0, sẽ khiến diag.CompassDrift báo sai, định tuyến Router biến dạng.
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("save compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown type %q, expected premise/outline/layered_outline/characters/world_rules/expand_arc/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// checkpoint
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}

	if volumeEnd {
		t.recordVolumeEndDecision(a.Type, a.Reason, volumeEndFacts, result)
	}

	// Trả về các mục chưa hoàn thành còn lại. Công cụ ban đầu đủ vẫn sẽ còn foundation_audit; chỉ có
	// audit_foundation đưa ra ready=true với phiên bản lưu thực tế, mới cho phép vào writing.
	remaining, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	return json.Marshal(result)
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "expand_arc", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

// decodeFoundationJSON Phân tích trường content của save_foundation, khi thất bại kèm theo vị trí hàng cột
// và gợi ý sửa chữa phổ biến nhất, để LLM lần thử lại sau có thể định vị trực tiếp thay vì đoán mò.
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `Nguyên nhân phổ biến: dấu ngoặc kép trong chuỗi chưa escape thành \", xuống dòng chưa escape thành \n, hoặc thiếu dấu phẩy giữa các trường đối tượng. Vui lòng tạo lại toàn bộ một lần.`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid content: expected Markdown string or valid JSON value: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

// recordVolumeEndDecision Ghi lý do phán đoán chọn một trong ba cuối tập (tiếp tục/kết thúc/hoàn kết) vào kiểm toán phán quyết.
// best-effort: thay đổi cấu trúc đã lưu, kiểm toán thất bại chỉ cảnh báo không rollback——báo lỗi sẽ khiến mô hình thử lại thao tác đã hoàn thành
// (thêm tập lặp lại).
func (t *SaveFoundationTool) recordVolumeEndDecision(action, reason string, facts json.RawMessage, result map[string]any) {
	decision := map[string]any{"action": action}
	if v, ok := result["volume"]; ok {
		decision["volume"] = v
	}
	if _, ok := result["final_volume"]; ok {
		decision["final"] = true
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		slog.Error("serialize phán quyết cuối tập thất bại", "module", "tools", "action", action, "err", err)
		return
	}
	if _, err := t.store.Decisions.Append(store.DecisionRecord{
		Kind:     "volume_end",
		Decider:  "architect",
		Facts:    facts,
		Decision: raw,
		Reason:   reason,
	}); err != nil {
		slog.Error("ghi kiểm toán phán quyết cuối tập xuống đĩa thất bại", "module", "tools", "action", action, "err", err)
	}
}

// consumeWriterFeedback Xóa phản hồi quy hoạch đã xử lý sau khi thao tác cấu trúc thành công.
func (t *SaveFoundationTool) consumeWriterFeedback() error {
	if err := t.store.Outline.ClearOutlineFeedback(); err != nil {
		return fmt.Errorf("clear outline feedback: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}