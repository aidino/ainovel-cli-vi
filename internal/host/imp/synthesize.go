package imp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// Tập đóng trạng thái câu chuyện (RFC §10.4).
const (
	storyOpen      = "open"
	storyClosed    = "closed"
	storyUncertain = "uncertain"
)

// synthesisSchemaVersion đưa vào RangeDigest / synthesis InputDigest, tăng lên khi nâng cấp khế ước tổng hợp để làm vô hiệu công kiện đã lưu.
// synthesizePromptVersion đưa vào synthesis InputDigest, tăng lên khi đổi prompt tổng hợp, nếu không synthesis cũ vẫn bị phán đoán nhầm là hợp lệ.
const (
	synthesisSchemaVersion  = 3
	synthesizePromptVersion = "synthesize-v3"
	rangePromptVersion      = "range-v2" // đưa vào rangeInputDigest, tăng lên khi đổi prompt Range, nếu không tóm tắt khoảng cũ vẫn bị phán đoán nhầm là hợp lệ
)

// ImportedArcRange / ImportedVolumeRange: Tổng hợp chỉ trả về phạm vi tập arc, không xuất lặp lại tất cả các chương (RFC §10.3).
type ImportedArcRange struct {
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	StartChapter int    `json:"start_chapter"`
	EndChapter   int    `json:"end_chapter"`
}

type ImportedVolumeRange struct {
	Title string             `json:"title"`
	Theme string             `json:"theme"`
	Arcs  []ImportedArcRange `json:"arcs"`
}

// BookSynthesis là kết quả tổng hợp cuối cùng: Sự thật toàn cục + phạm vi tập arc (RFC §10.3).
type BookSynthesis struct {
	Title        *string               `json:"title"`
	Synopsis     string                `json:"synopsis"`
	Premise      string                `json:"premise"`
	Characters   []domain.Character    `json:"characters"`
	WorldRules   []domain.WorldRule    `json:"world_rules"`
	Structure    []ImportedVolumeRange `json:"structure"`
	Compass      domain.StoryCompass   `json:"compass"`
	PlanningTier domain.PlanningTier   `json:"planning_tier"`
	StoryStatus  string                `json:"story_status"`
	StatusReason string                `json:"status_reason,omitempty"`
}

// RangeDigest là tóm tắt khoảng liên tục của giai đoạn Map cho sách dài, đầu ra chịu ràng buộc của một khoảng đơn (RFC §10.2).
type RangeDigest struct {
	StartChapter    int      `json:"start_chapter"`
	EndChapter      int      `json:"end_chapter"`
	Plot            string   `json:"plot"`
	Characters      []string `json:"characters,omitempty"`
	WorldFacts      []string `json:"world_facts,omitempty"`
	OpenedThreads   []string `json:"opened_threads,omitempty"`
	ResolvedThreads []string `json:"resolved_threads,omitempty"`
}

var validPlanningTiers = map[domain.PlanningTier]bool{
	domain.PlanningTierShort: true,
	domain.PlanningTierMid:   true,
	domain.PlanningTierLong:  true,
}

// planFactRanges chia sự thật từng chương thành các khoảng liên tục theo ngân sách byte; sách ngắn chứa được trong một lần thì tổng hợp trực tiếp bằng một khoảng đơn (RFC §10.2).
func planFactRanges(facts []ImportedChapterFacts, budgetBytes int) [][2]int {
	if len(facts) == 0 {
		return nil
	}
	if budgetBytes <= 0 {
		return [][2]int{{0, len(facts)}}
	}
	var ranges [][2]int
	start, acc := 0, 0
	for i, f := range facts {
		size := len(compactFact(f))
		if i > start && acc+size > budgetBytes {
			ranges = append(ranges, [2]int{start, i})
			start, acc = i, 0
		}
		acc += size
	}
	ranges = append(ranges, [2]int{start, len(facts)})
	return ranges
}

// compactView là góc nhìn nhỏ gọn đưa vào tổng hợp: Giữ lại các trường cần thiết để quy nạp xuyên chương, không chứa toàn văn.
// character/world evidence là quan sát được trích xuất dành riêng cho tổng hợp toàn sách khi suy luận ngược từng chương, bắt buộc phải mang vào——
// nếu không bộ tổng hợp chỉ có thể bịa ra nhân vật và quy tắc thế giới chính thức từ tóm tắt, lãng phí bằng chứng đã trích xuất (RFC §9.1/§10).
type compactView struct {
	Chapter           int                     `json:"chapter"`
	Title             string                  `json:"title"`
	CoreEvent         string                  `json:"core_event"`
	Summary           string                  `json:"summary"`
	Characters        []string                `json:"characters,omitempty"`
	CharacterEvidence []ImportedCharacterFact `json:"character_evidence,omitempty"`
	WorldEvidence     []ImportedWorldFact     `json:"world_evidence,omitempty"`
}

func toCompact(f ImportedChapterFacts) compactView {
	return compactView{
		Chapter:           f.Chapter,
		Title:             f.Title,
		CoreEvent:         f.CoreEvent,
		Summary:           f.Summary,
		Characters:        f.Characters,
		CharacterEvidence: f.CharacterEvidence,
		WorldEvidence:     f.WorldEvidence,
	}
}

func compactFact(f ImportedChapterFacts) string {
	data, _ := json.Marshal(toCompact(f))
	return string(data)
}

func compactFacts(facts []ImportedChapterFacts) string {
	views := make([]compactView, len(facts))
	for i, f := range facts {
		views[i] = toCompact(f)
	}
	data, _ := json.Marshal(views)
	return string(data)
}

// Synthesize tổng hợp phân tầng: Sách ngắn ra thẳng BookSynthesis; sách dài ra RangeDigest trước rồi mới gộp (RFC §10).
// bookPrompt mô tả khế ước BookSynthesis, rangePrompt mô tả khế ước RangeDigest——Cấu trúc đầu ra hai giai đoạn khác nhau,
// bắt buộc dùng prompt hệ thống tương ứng, nếu không model nhận chỉ thị BookSynthesis nhưng lại bị yêu cầu RangeDigest, chỉ thị tự mâu thuẫn.
func Synthesize(ctx context.Context, m callModel, bookPrompt, rangePrompt string, w *Workspace, facts []ImportedChapterFacts, budgetBytes, maxTokens int, prof callProfile) (*BookSynthesis, error) {
	ranges := planFactRanges(facts, budgetBytes)
	if len(ranges) <= 1 {
		return synthesizeBook(ctx, m, bookPrompt, compactFacts(facts), len(facts), maxTokens, prof)
	}
	digests := make([]RangeDigest, 0, len(ranges))
	for ri, r := range ranges {
		rangeFacts := facts[r[0]:r[1]]
		startCh, endCh := rangeFacts[0].Chapter, rangeFacts[len(rangeFacts)-1].Chapter
		want := rangeInputDigest(rangeFacts)
		rel := rangeDigestPath(startCh, endCh)
		// Tóm tắt khoảng đã lưu khớp InputDigest được tái sử dụng trực tiếp, bất kỳ khoảng nào của sách dài gặp sự cố sẽ không tính phí lại (RFC §6/§10.2).
		if art, err := readArtifact[RangeDigest](w, rel); err == nil && art.InputDigest == want {
			digests = append(digests, art.Payload)
			continue
		}
		prof.step(ri+1, len(ranges), "Tóm tắt khoảng %d/%d (chương %d-%d)...", ri+1, len(ranges), startCh, endCh)
		rd, err := callStructured[RangeDigest](ctx, m, rangeContract, rangePrompt, buildRangePayload(rangeFacts), maxTokens, prof, func(d *RangeDigest) error {
			return validateRangeDigest(d, startCh, endCh, "range digest")
		})
		if err != nil {
			return nil, fmt.Errorf("tổng hợp range %d-%d: %w", startCh, endCh, err)
		}
		if err := writeArtifact(w, rel, want, rd); err != nil {
			return nil, fmt.Errorf("lưu range digest: %w", err)
		}
		digests = append(digests, rd)
	}
	// Đệ quy Reduce: Tổng lượng tóm tắt khoảng vẫn có thể vượt ngân sách đầu vào tổng hợp cuối (đẩy lùi #83 từ "toàn bộ chương" sang "toàn bộ tóm tắt khoảng").
	// Gộp từng tầng cho đến khi chứa được, mới thật sự mở rộng không giới hạn (RFC §10.2).
	digests, err := reduceToFit(ctx, m, rangePrompt, digests, budgetBytes, maxTokens, prof)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(digests)
	return synthesizeBook(ctx, m, bookPrompt, string(data), len(facts), maxTokens, prof)
}

// reduceToFit lặp lại việc chia nhóm gộp tóm tắt khoảng liên tục theo ngân sách, cho đến khi tuần tự hóa xong chứa được ngân sách đầu vào BookSynthesis cuối cùng.
// Mỗi vòng giảm nghiêm ngặt số lượng tóm tắt, nên chắc chắn hội tụ; một tóm tắt dù vượt ngân sách cũng không chia nữa (tầng dưới đã là đơn vị ngữ nghĩa nhỏ nhất),
// giao cho lời gọi cuối, nếu vì thế bị cắt thì callStructured sẽ báo lỗi rõ ràng thay vì tràn tĩnh lặng.
func reduceToFit(ctx context.Context, m callModel, rangePrompt string, digests []RangeDigest, budgetBytes, maxTokens int, prof callProfile) ([]RangeDigest, error) {
	round := 0
	for len(digests) > 1 {
		if budgetBytes <= 0 {
			return digests, nil
		}
		data, _ := json.Marshal(digests)
		if len(data) <= budgetBytes {
			return digests, nil
		}
		groups := groupDigestsByBudget(digests, budgetBytes)
		if len(groups) >= len(digests) {
			return digests, nil // Không thể gộp nữa (mỗi nhóm chỉ một tóm tắt)
		}
		round++
		merged := make([]RangeDigest, 0, len(groups))
		for gi, g := range groups {
			startCh, endCh := g[0].StartChapter, g[len(g)-1].EndChapter
			prof.step(gi+1, len(groups), "Gộp tóm tắt khoảng (vòng %d %d/%d, chương %d-%d)...",
				round, gi+1, len(groups), startCh, endCh)
			rd, err := callStructured[RangeDigest](ctx, m, rangeContract, rangePrompt, buildDigestReducePayload(g), maxTokens, prof, func(d *RangeDigest) error {
				return validateRangeDigest(d, startCh, endCh, "Gộp khoảng")
			})
			if err != nil {
				return nil, fmt.Errorf("gộp khoảng %d-%d: %w", startCh, endCh, err)
			}
			merged = append(merged, rd)
		}
		digests = merged
	}
	return digests, nil
}

func validateRangeDigest(d *RangeDigest, startChapter, endChapter int, label string) error {
	if strings.TrimSpace(d.Plot) == "" {
		return fmt.Errorf("%s plot trống", label)
	}
	if d.StartChapter != startChapter || d.EndChapter != endChapter {
		return fmt.Errorf("phạm vi chương %s %d-%d không khớp với yêu cầu %d-%d", label, d.StartChapter, d.EndChapter, startChapter, endChapter)
	}
	return nil
}

// groupDigestsByBudget chia tóm tắt khoảng liên tục thành các nhóm liên tục theo ngân sách byte; một tóm tắt dù vượt ngân sách cũng thành một nhóm riêng.
func groupDigestsByBudget(digests []RangeDigest, budgetBytes int) [][]RangeDigest {
	var groups [][]RangeDigest
	var cur []RangeDigest
	acc := 0
	for _, d := range digests {
		b, _ := json.Marshal(d)
		if len(cur) > 0 && acc+len(b) > budgetBytes {
			groups = append(groups, cur)
			cur, acc = nil, 0
		}
		cur = append(cur, d)
		acc += len(b)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// buildDigestReducePayload lắp ráp đầu vào của "gộp nhiều tóm tắt khoảng tầng dưới thành một RangeDigest".
func buildDigestReducePayload(digests []RangeDigest) string {
	data, _ := json.Marshal(digests)
	return fmt.Sprintf("Hãy gộp nhiều tóm tắt khoảng tầng dưới của chương %d-%d thành một RangeDigest (tóm tắt khoảng liên tục). Tóm tắt tầng dưới:\n%s",
		digests[0].StartChapter, digests[len(digests)-1].EndChapter, string(data))
}

// rangeDigestPath trả về đường dẫn tương đối của công kiện tóm tắt khoảng liên tục.
func rangeDigestPath(startChapter, endChapter int) string {
	return fmt.Sprintf("%s/%06d-%06d.json", dirRangeDigests, startChapter, endChapter)
}

// rangeInputDigest liên kết sự thật nhỏ gọn của khoảng liên tục này với phiên bản Range prompt/schema (RFC §6.3).
func rangeInputDigest(facts []ImportedChapterFacts) string {
	return Digest([]byte(fmt.Sprintf("range\x00%s\x00v%d\x00%s", rangePromptVersion, synthesisSchemaVersion, compactFacts(facts))))
}

func synthesizeBook(ctx context.Context, m callModel, systemPrompt, payload string, n, maxTokens int, prof callProfile) (*BookSynthesis, error) {
	prof.step(0, 0, "Tạo tổng hợp toàn sách (thông tin tác phẩm/premise/characters/cấu trúc đại cương)...")
	s, err := callStructured[BookSynthesis](ctx, m, synthesisContract, systemPrompt, buildBookPayload(payload, n), maxTokens, prof, func(s *BookSynthesis) error {
		return validateSynthesis(s, n)
	})
	if err != nil {
		return nil, err
	}
	// Hiện hiểu biết toàn sách của model: Đây là đầu ra ngữ nghĩa cốt lõi nhất của việc import, xứng đáng để người dùng thấy ngay.
	prof.step(0, 0, "Model khái quát toàn sách: %s", snippet(s.Premise, 80))
	return &s, nil
}

func buildRangePayload(facts []ImportedChapterFacts) string {
	return fmt.Sprintf("Hãy tạo một RangeDigest (tóm tắt khoảng liên tục) cho chương %d-%d. Sự thật từng chương:\n%s",
		facts[0].Chapter, facts[len(facts)-1].Chapter, compactFacts(facts))
}

func buildBookPayload(inner string, n int) string {
	return fmt.Sprintf("Dưới đây là sự thật nhỏ gọn/tóm tắt khoảng của toàn sách %d chương. Hãy tạo BookSynthesis: title, synopsis, premise, characters, world_rules, phạm vi tập arc structure, compass, planning_tier, story_status.\n\n%s", n, inner)
}

// validateSynthesis xác minh ràng buộc cấu trúc của kết quả tổng hợp (miền giá trị/tập đóng/phạm vi), không phán xét lại chất lượng văn học.
func validateSynthesis(s *BookSynthesis, n int) error {
	if strings.TrimSpace(s.Synopsis) == "" {
		return fmt.Errorf("synopsis trống")
	}
	if strings.TrimSpace(s.Premise) == "" {
		return fmt.Errorf("premise trống")
	}
	if len(s.Characters) == 0 {
		return fmt.Errorf("characters trống")
	}
	if !validPlanningTiers[s.PlanningTier] {
		return fmt.Errorf("planning_tier không hợp lệ: %q", s.PlanningTier)
	}
	switch s.StoryStatus {
	case storyOpen, storyClosed, storyUncertain:
	default:
		return fmt.Errorf("story_status không hợp lệ: %q", s.StoryStatus)
	}
	if strings.TrimSpace(s.Compass.EndingDirection) == "" {
		return fmt.Errorf("compass.ending_direction trống")
	}
	return validateStructure(s.Structure, n)
}

// validateStructure xác minh phạm vi tập arc liên tục, không chồng chéo, phủ đủ 1..N (RFC §11 / Bất biến 5).
func validateStructure(structure []ImportedVolumeRange, n int) error {
	if len(structure) == 0 {
		return fmt.Errorf("structure trống")
	}
	next := 1
	for vi, v := range structure {
		if len(v.Arcs) == 0 {
			return fmt.Errorf("tập[%d] %q không có arc", vi, v.Title)
		}
		for ai, a := range v.Arcs {
			if a.StartChapter != next {
				return fmt.Errorf("tập[%d] arc[%d] điểm bắt đầu %d phải là %d (phải liên tục không có khoảng trống)", vi, ai, a.StartChapter, next)
			}
			if a.EndChapter < a.StartChapter {
				return fmt.Errorf("tập[%d] arc[%d] phạm vi bị đảo ngược %d..%d", vi, ai, a.StartChapter, a.EndChapter)
			}
			next = a.EndChapter + 1
		}
	}
	if next-1 != n {
		return fmt.Errorf("phạm vi tập arc phủ %d chương, phải là %d chương", next-1, n)
	}
	return nil
}

// synthesisInputDigest liên kết sự thật nhỏ gọn của tập hợp phân tích từng chương có thứ tự + phiên bản prompt/schema tổng hợp (RFC §6.3 / Bất biến 6).
// đưa vào phiên bản, đổi khế ước tổng hợp thì synthesis cũ tự nhiên hết hạn phải làm lại.
func synthesisInputDigest(facts []ImportedChapterFacts) string {
	var b strings.Builder
	b.WriteString("synthesize\x00")
	b.WriteString(synthesizePromptVersion)
	fmt.Fprintf(&b, "\x00v%d", synthesisSchemaVersion)
	for _, f := range facts {
		b.WriteByte(0)
		b.WriteString(compactFact(f))
	}
	return Digest([]byte(b.String()))
}

// Foundation là tập hợp đối tượng lĩnh vực chính thức được lắp ráp từ BookSynthesis + sự thật từng chương (xác minh đầy đủ trước khi đăng, RFC §11).
type Foundation struct {
	Book         domain.BookMetadata
	PlanningTier domain.PlanningTier
	Premise      string
	Characters   []domain.Character
	WorldRules   []domain.WorldRule
	Volumes      []domain.VolumeOutline
	Compass      domain.StoryCompass
	Closed       bool
}

// AssembleFoundation dùng ngữ nghĩa tổng hợp + sự thật từng chương để lắp ráp Foundation chính thức và xác minh đầy đủ.
// closed là sự thật kết thúc sau khi phán quyết story_status; fallbackName dùng làm tiêu đề suy luận khi chính văn không thể xác định tên sách.
func AssembleFoundation(s *BookSynthesis, facts []ImportedChapterFacts, closed bool, fallbackName string) (*Foundation, error) {
	n := len(facts)
	if err := validateSynthesis(s, n); err != nil {
		return nil, err
	}
	byChapter := make(map[int]ImportedChapterFacts, n)
	for _, f := range facts {
		byChapter[f.Chapter] = f
	}

	volumes := make([]domain.VolumeOutline, 0, len(s.Structure))
	for vi, v := range s.Structure {
		vol := domain.VolumeOutline{Index: vi + 1, Title: v.Title, Theme: v.Theme}
		for ai, a := range v.Arcs {
			arc := domain.ArcOutline{Index: ai + 1, Title: a.Title, Goal: a.Goal}
			for ch := a.StartChapter; ch <= a.EndChapter; ch++ {
				f, ok := byChapter[ch]
				if !ok {
					return nil, fmt.Errorf("phạm vi arc tham chiếu chương không tồn tại %d", ch)
				}
				arc.Chapters = append(arc.Chapters, domain.OutlineEntry{
					Chapter: ch, Title: f.Title, CoreEvent: f.CoreEvent, Hook: f.Hook, Scenes: f.Scenes,
				})
			}
			vol.Arcs = append(vol.Arcs, arc)
		}
		volumes = append(volumes, vol)
	}
	if closed && len(volumes) > 0 {
		volumes[len(volumes)-1].Final = true
	}

	// FlattenOutline xong số chương là N, và tiêu đề nhất trí với sự thật từng chương (RFC §11.5).
	flat := domain.FlattenOutline(volumes)
	if len(flat) != n {
		return nil, fmt.Errorf("FlattenOutline số chương %d != %d", len(flat), n)
	}
	for _, e := range flat {
		if e.Title != byChapter[e.Chapter].Title {
			return nil, fmt.Errorf("chương %d tiêu đề không nhất trí với sự thật từng chương", e.Chapter)
		}
	}

	title := ""
	if s.Title != nil {
		title = strings.TrimSpace(*s.Title)
	}
	if title == "" {
		title = importedBookTitle(fallbackName)
	}
	return &Foundation{
		Book:         (domain.BookMetadata{Title: title, Synopsis: s.Synopsis}).Normalized(),
		PlanningTier: s.PlanningTier,
		Premise:      s.Premise,
		Characters:   s.Characters,
		WorldRules:   s.WorldRules,
		Volumes:      volumes,
		Compass:      s.Compass,
		Closed:       closed,
	}, nil
}

// importedBookTitle dùng tên file nguồn khi chính văn không thể xác định tên sách, đảm bảo thông tin tác phẩm vẫn có tiêu đề rõ ràng.
func importedBookTitle(fallbackName string) string {
	name := strings.TrimSuffix(fallbackName, ".txt")
	name = strings.TrimSuffix(name, ".md")
	if name == "" {
		name = "Import chưa đặt tên"
	}
	return name
}
