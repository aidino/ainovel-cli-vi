package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"maps"
	"os"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// analysisSchemaVersion là phiên bản schema sự thật từng chương, được đưa vào InputDigest.
const analysisSchemaVersion = 2

// ImportedCharacterFact / ImportedWorldFact là các quan sát nhỏ gọn dùng để tổng hợp toàn bộ sách, không trực tiếp viết thành vai trò chính thức hoặc quy tắc thế giới.
// Ít nhất mang theo số chương, giúp kết quả tổng hợp có nguồn ổn định (RFC §9.1).
type ImportedCharacterFact struct {
	Chapter int    `json:"chapter"`
	Name    string `json:"name"`
	Note    string `json:"note,omitempty"`
}

type ImportedWorldFact struct {
	Chapter  int    `json:"chapter"`
	Category string `json:"category,omitempty"`
	Fact     string `json:"fact"`
}

// ImportedChapterFacts là sản phẩm có cấu trúc được suy luận ngược từ một chương (RFC §9.1).
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook,omitempty"`
	Scenes              []string                   `json:"scenes,omitempty"`
	Characters          []string                   `json:"characters,omitempty"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent     `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

// AnalysisBatchResult là kết quả trả về có cấu trúc của một lệnh gọi lô, mỗi phần tử là một chương sự thật.
type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

// ChapterAnalysisPayload là tải trọng công cụ phân tích từng chương; các chương cùng lô ghi cùng BatchStart/BatchEnd.
type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}

// AnalyzeBudget là ngân sách kép đầu vào/đầu ra của phân tích từng chương (RFC §9.2).
// Đầu vào lấy byte để xấp xỉ context window; đầu ra lấy mức dự trữ sự thật bảo thủ mỗi chương để xấp xỉ giới hạn completion.
type AnalyzeBudget struct {
	ContextBytes     int // Ngân sách đầu vào (văn bản + ledger + overhead)
	MaxOutputTokens  int // Ngân sách đầu ra hiển thị (giới hạn completion)
	PerChapterOutput int // Dự trữ đầu ra bảo thủ mỗi chương
	PromptOverhead   int // Chi phí đầu vào cố định của system/ledger (byte)
}

func analysisPath(chapter int) string {
	return fmt.Sprintf("%s/%06d.json", dirAnalyses, chapter)
}

// analyzedChapters trả về số lượng công cụ phân tích liên tục từ chương 1 và có InputDigest khớp với danh tính cắt phân/phiên bản/văn bản hiện tại (RFC §9.6).
// Thiếu, phân tích thất bại hoặc digest không khớp đều bị cắt ngắn ở đây, làm cho những thay đổi ở thượng nguồn (cắt lại, đổi phiên bản prompt/schema) tự nhiên làm vô hiệu hóa phân tích ở hạ lưu.
func analyzedChapters(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) int {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n
}

// analyzedChaptersStrict có ngữ nghĩa độ tươi giống với analyzedChapters, nhưng sẽ phơi bày các
// công cụ đã có bị hỏng hoặc không thể đọc được. Phục hồi trạng thái dùng phiên bản nghiêm ngặt, để tránh nhầm lẫn lỗi đọc thực sự thành "chưa phân tích" rồi ghi đè làm lại.
func analyzedChaptersStrict(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) (int, error) {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("Đọc công cụ phân tích chương %d: %w", c, err)
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n, nil
}

// discardAnalysesAfter xóa công cụ phân tích từng chương có số chương > keep, để đảm bảo "phân tích lại một chương sẽ làm vô hiệu hóa toàn bộ phân tích sau đó" (#4a).
// Trong phân tích tiến tới bình thường, vốn không có công cụ nào sau keep, là hành động không đổi; chỉ dọn dẹp phần đuôi cũ khi phân tích lại giữa chừng (vượt qua phần đầu tươi mới).
// Thất bại xóa phải được lan truyền: đây là điểm thực thi duy nhất của bất biến này, nuốt lỗi sẽ làm cho phần đuôi cũ (digest từng chương luôn khớp)
// được dùng lại như phần đầu tươi mới, tổng hợp sẽ tiêu thụ sự thật trộn lẫn mới cũ và không có bất kỳ báo lỗi nào.
func discardAnalysesAfter(w *Workspace, keep, total int) error {
	for c := keep + 1; c <= total; c++ {
		if err := os.Remove(w.path(analysisPath(c))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Xóa công cụ phân tích cũ %s: %w", analysisPath(c), err)
		}
	}
	return nil
}

// loadPriorFacts đọc sự thật của chương 1..count đã ghi đĩa, để xây dựng ledger.
func loadPriorFacts(w *Workspace, count int) []ImportedChapterFacts {
	var out []ImportedChapterFacts
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		out = append(out, a.Payload.Facts)
	}
	return out
}

func loadPriorFactsStrict(w *Workspace, count int) ([]ImportedChapterFacts, error) {
	out := make([]ImportedChapterFacts, 0, count)
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			return out, fmt.Errorf("Đọc sự thật phân tích chương %d: %w", c, err)
		}
		out = append(out, a.Payload.Facts)
	}
	return out, nil
}

// buildLedger dẫn xuất ngữ cảnh liên tục nhỏ gọn từ các chương đã phân tích: bí danh nhân vật + ID chi tiết gieo mầm đang hoạt động + trạng thái gần nhất.
func buildLedger(prior []ImportedChapterFacts) string {
	if len(prior) == 0 {
		return ""
	}
	names := map[string]bool{}
	active := map[string]string{} // foreshadow id -> desc
	var recent []string
	for _, f := range prior {
		for _, c := range f.Characters {
			names[c] = true
		}
		for _, fu := range f.ForeshadowUpdates {
			switch fu.Action {
			case "plant", "advance":
				if fu.Description != "" {
					active[fu.ID] = fu.Description
				} else if _, ok := active[fu.ID]; !ok {
					active[fu.ID] = ""
				}
			case "resolve":
				delete(active, fu.ID)
			}
		}
	}
	if len(prior) > 0 {
		last := prior[len(prior)-1]
		for _, sc := range last.StateChanges {
			recent = append(recent, fmt.Sprintf("%s.%s=%s", sc.Entity, sc.Field, sc.NewValue))
		}
	}
	var b strings.Builder
	if len(names) > 0 {
		b.WriteString("Nhân vật đã biết: ")
		b.WriteString(strings.Join(slices.Sorted(maps.Keys(names)), ", "))
		b.WriteString("\n")
	}
	if len(active) > 0 {
		b.WriteString("Chi tiết gieo mầm hoạt động (tái sử dụng ID, đừng tạo mới):\n")
		for _, id := range slices.Sorted(maps.Keys(active)) {
			fmt.Fprintf(&b, "- %s：%s\n", id, active[id])
		}
	}
	if len(recent) > 0 {
		b.WriteString("Trạng thái gần nhất: ")
		b.WriteString(strings.Join(recent, "; "))
		b.WriteString("\n")
	}
	return b.String()
}

// planBatch tính từ chương start, trả về điểm cuối end của lô liên tục theo ngân sách kép đầu vào/đầu ra ([start,end), chỉ số chương tính từ 0).
// Ít nhất 1 chương; một chương dù vượt ngân sách cũng thành một lô riêng, do bên thực thi báo cáo thiếu dung lượng khi cắt (RFC §9.2).
func planBatch(chapters []ChapterSpan, start, ledgerBytes int, b AnalyzeBudget) int {
	end := start + 1
	if b.ContextBytes <= 0 || b.MaxOutputTokens <= 0 || b.PerChapterOutput <= 0 {
		return end // Ngân sách không được cấu hình: từng chương
	}
	inAcc := ledgerBytes + b.PromptOverhead + chapterBytes(chapters, start)
	outAcc := b.PerChapterOutput
	for end < len(chapters) {
		cb := chapterBytes(chapters, end)
		if inAcc+cb > b.ContextBytes {
			break
		}
		if outAcc+b.PerChapterOutput > b.MaxOutputTokens {
			break
		}
		inAcc += cb
		outAcc += b.PerChapterOutput
		end++
	}
	return end
}

func chapterBytes(chapters []ChapterSpan, i int) int {
	return chapters[i].End - chapters[i].Start
}

// chapterInputDigest liên kết danh tính công kiện phân tích từng chương: Danh tính cắt + phiên bản prompt/schema + số chương + chính văn một chương.
// Liên kết từng chương thay vì cấp lô——chia lô là chi tiết thực thi thay đổi theo năng lực model, không nên để đổi model xong thì toàn bộ chương đã phân tích hết hạn; 
// Liên kết segIdentity (InputDigest của công kiện segmentation) đảm bảo sau khi cắt lại thì mọi phân tích tự nhiên không khớp (RFC §9.1/§6.3).
func chapterInputDigest(segIdentity, promptVersion string, seg *Segmentation, normalized []byte, i int) string {
	var b strings.Builder
	b.WriteString("analyze\x00")
	b.WriteString(promptVersion)
	fmt.Fprintf(&b, "\x00v%d\x00", analysisSchemaVersion)
	b.WriteString(segIdentity)
	fmt.Fprintf(&b, "\x00ch%d\x00", seg.Chapters[i].Number)
	b.WriteString(seg.Content(normalized, i))
	return Digest([]byte(b.String()))
}

// validateBatch kiểm tra 2 lớp: cấp lô liên tục không thiếu không lặp, cấp chương vùng giá trị và tham chiếu (RFC §9.4).
func validateBatch(r *AnalysisBatchResult, seg *Segmentation, start, end int) error {
	want := end - start
	if len(r.Chapters) != want {
		return fmt.Errorf("Số chương của lô %d != kỳ vọng %d", len(r.Chapters), want)
	}
	for i, f := range r.Chapters {
		want := seg.Chapters[start+i]
		if f.Chapter != want.Number {
			return fmt.Errorf("Số chương mục thứ %d của lô %d != %d", i, f.Chapter, want.Number)
		}
		if strings.TrimSpace(f.Summary) == "" || strings.TrimSpace(f.CoreEvent) == "" {
			return fmt.Errorf("Chương %d summary/core_event không thể để trống", f.Chapter)
		}
		if !domain.ValidHookType(strings.ToLower(f.HookType)) {
			return fmt.Errorf("Chương %d hook_type không hợp lệ: %q", f.Chapter, f.HookType)
		}
		if !domain.ValidDominantStrand(strings.ToLower(f.DominantStrand)) {
			return fmt.Errorf("Chương %d dominant_strand không hợp lệ: %q", f.Chapter, f.DominantStrand)
		}
		for j, fu := range f.ForeshadowUpdates {
			if fu.Action == "plant" && strings.TrimSpace(fu.Description) == "" {
				return fmt.Errorf("Chương %d foreshadow[%d] plant cần description", f.Chapter, j)
			}
		}
		// Enum được kiểm tra chữ thường thì ghi đĩa theo chữ thường: commit_chapter không kiểm tra lại enum, biến thể chữ hoa chữ thường sẽ trực tiếp vào trạng thái chính thức
		// (HookHistory v.v. được tiêu thụ theo chuỗi chính xác, biến thể được coi là loại không xác định), xác nhận thành công tức là chuẩn hóa.
		r.Chapters[i].HookType = strings.ToLower(f.HookType)
		r.Chapters[i].DominantStrand = strings.ToLower(f.DominantStrand)
	}
	return nil
}

// AnalyzeNext gom một lô từ phân tích thiếu đầu tiên và lưu nguyên tử, trả về số chương commit lần này.
// Cắt nghĩa là "thất bại + thu nhỏ lô tổ hợp lại" (mặc định, §9.5); lô đã thu đến một chương mà vẫn bị cắt thì báo cáo rõ thiếu dung lượng.
func AnalyzeNext(ctx context.Context, m callModel, systemPrompt string, w *Workspace, normalized []byte, seg *Segmentation, segIdentity, promptVersion string, budget AnalyzeBudget, prof callProfile) (int, error) {
	total := len(seg.Chapters)
	start := analyzedChapters(w, seg, normalized, segIdentity, promptVersion)
	if start >= total {
		return 0, nil
	}
	ledger := buildLedger(loadPriorFacts(w, start))
	end := planBatch(seg.Chapters, start, len(ledger), budget)

	for {
		payload := buildAnalyzePayload(normalized, seg, ledger, start, end)
		res, err := callStructured[AnalysisBatchResult](ctx, m, analysisContract, systemPrompt, payload, budget.MaxOutputTokens, prof, func(r *AnalysisBatchResult) error {
			return validateBatch(r, seg, start, end)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) {
				// Khi bị cắt ngắn ưu tiên trục vớt tiền tố hợp lệ liên tục lớn nhất từ chương đầu tiên của lô, phần đã gửi không làm lại (§9.5).
				if salvaged := salvagePrefix(tr.Raw, seg, start); len(salvaged) > 0 {
					for i, f := range salvaged {
						ch := start + i + 1
						digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
						art := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
						if werr := writeArtifact(w, analysisPath(ch), digest, art); werr != nil {
							return i, fmt.Errorf("Ghi đĩa chương trục vớt %d: %w", ch, werr)
						}
					}
					w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("Độ dài lô %d-%d bị cắt ngắn", start+1, end),
						StopReason: "length", PrefixSalvage: fmt.Sprintf("available:%d", len(salvaged))}, tr.Raw)
					prof.logger().Info("imp phân tích cắt ngắn, trục vớt tiền tố liên tục", "batch_start", start+1, "salvaged", len(salvaged))
					echoChapterFacts(prof, salvaged)
					return len(salvaged), nil
				}
				// Không có tiền tố để trục vớt: ghi lại không khả dụng và "thất bại + thu nhỏ tổ hợp lại lô", một chương vẫn bị cắt ngắn thì báo thiếu dung lượng.
				w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("Độ dài lô %d-%d bị cắt ngắn，không có tiền tố có thể cứu vãn", start+1, end),
					StopReason: "length", PrefixSalvage: "unavailable"}, tr.Raw)
				if end-start > 1 {
					prof.logger().Warn("imp phân tích cắt ngắn, thu nhỏ tổ hợp lại lô", "batch", fmt.Sprintf("%d-%d", start+1, end), "prefix_salvage", "unavailable")
					end = start + (end-start)/2
					// Dòng tiến độ không có Key: vừa giúp người dùng thấy hành động thu nhỏ lô, vừa ngăn cách dòng lùi lại của hai lần gọi
					// độc lập trước sau bị nhầm lẫn hợp nhất theo cùng một Key (hợp đồng Key chỉ bao gồm lùi lại chớp nhoáng trong cùng một lần gọi).
					prof.step(0, 0, "Đầu ra bị cắt ngắn theo độ dài và không có tiền tố để trục vớt, thu nhỏ lô thành chương %d-%d để thử lại", start+1, end)
					continue
				}
				return 0, fmt.Errorf("Chương %d lô một chương vẫn bị cắt ngắn theo độ dài, khả năng đầu ra hiển thị của model không đủ", start+1)
			}
			return 0, err
		}
		for i, f := range res.Chapters {
			ch := start + i + 1
			digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
			payloadArt := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
			if err := writeArtifact(w, analysisPath(ch), digest, payloadArt); err != nil {
				return i, fmt.Errorf("Ghi đĩa phân tích chương %d: %w", ch, err)
			}
		}
		echoChapterFacts(prof, res.Chapters)
		return end - start, nil
	}
}

// echoChapterFacts phản hồi sự hiểu biết cốt lõi của model về từng chương lên bảng điều khiển——người dùng nên nhìn thấy model đã hiểu được gì,
// chứ không phải chỉ là đếm lô cơ học (§14.1).
func echoChapterFacts(prof callProfile, facts []ImportedChapterFacts) {
	for _, f := range facts {
		prof.step(0, 0, "Chương %d <%s>: %s", f.Chapter, snippet(f.Title, 24), snippet(f.CoreEvent, 60))
	}
}

// buildAnalyzePayload lắp ráp đầu vào của lô: nguyên bản chương liên tục + ledger trước lô.
func buildAnalyzePayload(normalized []byte, seg *Segmentation, ledger string, start, end int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hãy phân tích chương %d-%d, trả về {\"chapters\":[mỗi chương một đối tượng sự thật]}, thứ tự mảng nhất trí với số chương.\n\n", start+1, end)
	if ledger != "" {
		b.WriteString("## ledger tính liên tục (tham khảo)\n\n")
		b.WriteString(ledger)
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		c := seg.Chapters[i]
		fmt.Fprintf(&b, "## Chương %d: %s\n\n", c.Number, c.Title)
		b.WriteString(seg.Content(normalized, i))
		b.WriteString("\n\n---\n\n")
	}
	return b.String()
}

// salvagePrefix phân tích tiền tố hợp lệ liên tục lớn nhất từ phản hồi lô bị cắt độ dài (RFC §9.5).
// Chỉ lưu đối tượng liên tục từ chương đầu lô, qua xác minh từng chương; gặp lỗi không trọn vẹn/không hợp lệ/nhảy số đầu tiên là dừng, các byte sau không diễn giải.
// Hàm thuần túy, được AnalyzeNext ưu tiên gọi khi bị cắt dung lượng, tránh vứt bỏ chương tiền tố đã tạo trọn vẹn.
func salvagePrefix(raw string, seg *Segmentation, start int) []ImportedChapterFacts {
	arr := extractChaptersArray(raw)
	if arr == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(arr))
	if _, err := dec.Token(); err != nil { // Tiêu thụ '['
		return nil
	}
	var out []ImportedChapterFacts
	for dec.More() {
		var f ImportedChapterFacts
		if err := dec.Decode(&f); err != nil {
			break // Đối tượng không hoàn chỉnh đầu tiên, dừng lại
		}
		idx := start + len(out)
		if idx >= len(seg.Chapters) || f.Chapter != seg.Chapters[idx].Number {
			break // Nhảy số/vượt quá giới hạn
		}
		one := AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}
		if err := validateBatch(&one, seg, idx, idx+1); err != nil {
			break
		}
		out = append(out, one.Chapters[0]) // validateBatch đã chuẩn hóa enum tại chỗ, lấy giá trị sau khi kiểm tra
	}
	return out
}

// extractChaptersArray lấy văn bản mảng JSON sau "chapters" (có thể bị cắt ngắn ở đuôi).
func extractChaptersArray(raw string) string {
	i := strings.Index(raw, "\"chapters\"")
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(raw[i:], '[')
	if j < 0 {
		return ""
	}
	return raw[i+j:]
}
