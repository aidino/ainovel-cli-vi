package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// BoundaryDecision là phán đoán của model về ranh giới cho một owned range đơn (RFC §8.2).
type BoundaryDecision struct {
	UnitID    string `json:"unit_id"`
	Anchor    string `json:"anchor,omitempty"`
	Kind      string `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string `json:"title,omitempty"`
	Uncertain bool   `json:"uncertain,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	kindChapter     = "chapter"
	kindGroup       = "group"
	kindFrontMatter = "front_matter"
	kindBackMatter  = "back_matter"
)

// boundaryBatch là kết quả trả về có cấu trúc của một lệnh gọi phân đoạn.
type boundaryBatch struct {
	Boundaries []BoundaryDecision `json:"boundaries"`
}

// ChapterSpan là một chương có thể gửi sau khi xác nhận cắt phân: tiêu đề + phạm vi byte văn bản chuẩn hóa (bao gồm dòng tiêu đề).
type ChapterSpan struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Start  int    `json:"start_byte"`
	End    int    `json:"end_byte"`
}

// MatterSpan là tiêu đề tập/phần hoặc khu vực phụ trợ rõ ràng.
type MatterSpan struct {
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

// Segmentation là kết quả cắt phân đã qua kiểm tra bao phủ toàn văn (thượng nguồn của confirmation và phân tích từng chương).
type Segmentation struct {
	Chapters  []ChapterSpan `json:"chapters"`
	Matter    []MatterSpan  `json:"matter,omitempty"`    // group / front / back
	Uncertain []int         `json:"uncertain,omitempty"` // Đánh dấu số chương uncertain, để nhắc nhở khi xem trước
	Notes     []string      `json:"notes,omitempty"`     // Hướng dẫn cần đối chiếu thủ công trong giai đoạn cắt phân (chẳng hạn như tiêu đề giữ chỗ đoạn văn bản trống được hợp nhất vào đoạn trước)
}

// Content trả về văn bản chuẩn hóa của chương thứ i (bao gồm dòng tiêu đề).
func (s *Segmentation) Content(normalized []byte, i int) string {
	c := s.Chapters[i]
	return string(normalized[c.Start:c.End])
}

// resolveSegmentation ánh xạ các quyết định ranh giới có thứ tự thành Segmentation đã được kiểm tra bao phủ toàn văn (RFC §8.3).
// Hàm thuần túy: đầu ra model và kiểm tra mã được tách biệt, "một dòng có phải là tiêu đề chương hay không" không do Go đánh giá lại, nhưng bất biến bao phủ phải được đảm bảo.
func resolveSegmentation(normalized []byte, units []SourceUnit, decisions []BoundaryDecision) (*Segmentation, error) {
	if len(decisions) == 0 {
		return nil, fmt.Errorf("Không nhận dạng được bất kỳ ranh giới nào")
	}
	// Hợp đồng tiền đề: units phải được sắp xếp theo thứ tự số (Line,Part) (cấm thứ tự từ điển ID).
	for i := 1; i < len(units); i++ {
		if !unitLess(units[i-1], units[i]) {
			return nil, fmt.Errorf("SourceUnit Không được sắp xếp theo thứ tự số (Line,Part): %s theo sau %s", units[i-1].ID, units[i].ID)
		}
	}
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}

	type point struct {
		byte int
		d    BoundaryDecision
	}
	points := make([]point, 0, len(decisions))
	for i, d := range decisions {
		switch d.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return nil, fmt.Errorf("Ranh giới[%d] kind không hợp lệ: %q", i, d.Kind)
		}
		b, err := resolveBoundaryByte(unitByID, d.UnitID, d.Anchor)
		if err != nil {
			return nil, err
		}
		points = append(points, point{byte: b, d: d})
	}
	// Lỗi lộn xộn và trùng lặp ngẫu nhiên của model là vấn đề kỷ luật tọa độ, Go sửa chữa mang tính xác định chứ không phủ quyết cuối cùng——sau khi tất cả các lô thành công mà vì
	// thứ tự của hai ranh giới bị đảo ngược lại hủy bỏ toàn bộ giai đoạn cắt phân thì cái giá phải trả là không thể chấp nhận được (thực tế đo lường 319 ranh giới thất bại ở 1 chỗ đảo ngược trong lô,
	// và bộ nhớ đệm lô sẽ làm cho thất bại có tính xác định lặp lại). Thứ tự giữa các lô được đảm bảo bởi khoảng owned không chồng chéo, lỗi lộn xộn chỉ có thể xảy ra trong
	// lô: sắp xếp ổn định theo byte tức là khôi phục thứ tự thực, không mất thông tin; lặp lại cùng byte giữ lại cái xuất hiện trước và ghi chú Notes
	// để xem trước xác nhận kiểm tra thủ công.
	sort.SliceStable(points, func(i, j int) bool { return points[i].byte < points[j].byte })
	var notes []string
	uniq := points[:0]
	for _, p := range points {
		if n := len(uniq); n > 0 && uniq[n-1].byte == p.byte {
			// Lặp lại giống hệt là dư thừa cơ học, xóa trùng lặp im lặng; xung đột ngữ nghĩa đồng vị (kind/tiêu đề khác nhau) trong lúc gọi
			// đã được hỏi lại, đến được đây chỉ có thể đến từ bộ đệm cũ trước khi sửa——giữ lại cái xuất hiện trước và ghi chú Notes kiểm tra thủ công.
			if prev := uniq[n-1].d; prev.Kind != p.d.Kind || boundaryLabel(prev) != boundaryLabel(p.d) {
				notes = append(notes, fmt.Sprintf("Ranh giới %q và %q trùng nhau (byte %d), đã giữ lại cái trước",
					boundaryLabel(prev), boundaryLabel(p.d), p.byte))
			}
			continue
		}
		uniq = append(uniq, p)
	}
	points = uniq
	// Văn bản không rỗng trước ranh giới đầu tiên (giới thiệu/quảng cáo đầu sách v.v., model bỏ sót báo ranh giới bắt đầu) không phủ quyết cuối cùng: Go mang tính xác định
	// bù một front_matter bọc lại [0, first), ghi Notes để xem trước xác nhận kiểm tra thủ công——bỏ sót báo đã vào bộ nhớ đệm lô,
	// phủ quyết cuối cùng sẽ làm cho việc chạy lại mà không có cuộc gọi lặp lại cùng một thất bại (cùng triết lý với việc hấp thụ chương văn bản trống, RFC §8.3.5).
	// Việc phán đoán ngữ nghĩa đã được trả lại cho model trong lúc gọi (chunkValidator.coverStart hỏi lại), dự phòng này chỉ chữa lành bộ đệm cũ.
	if head := points[0].byte; head != 0 && strings.TrimSpace(string(normalized[:head])) != "" {
		notes = append(notes, fmt.Sprintf("%d byte văn bản bắt đầu chưa được model phân bổ (%s…), đã được thu thành front_matter, vui lòng kiểm tra xem có bị sót cắt chương hay không",
			head, snippet(string(normalized[:min(head, 48)]), 24)))
		points = append([]point{{byte: 0, d: BoundaryDecision{UnitID: units[0].ID, Kind: kindFrontMatter}}}, points...)
	}

	seg := &Segmentation{Notes: notes}
	chapterNo := 0
	// absorb hợp nhất một đoạn vào span được tạo ra gần nhất (chương hoặc khu vực phụ đều được), khi không thể hợp nhất thì trả về false.
	absorb := func(end int) bool {
		ci, mi := len(seg.Chapters)-1, len(seg.Matter)-1
		switch {
		case ci >= 0 && (mi < 0 || seg.Chapters[ci].Start > seg.Matter[mi].Start):
			seg.Chapters[ci].End = end
		case mi >= 0:
			seg.Matter[mi].End = end
		default:
			return false
		}
		return true
	}
	for i, p := range points {
		start := p.byte
		if i == 0 {
			start = 0 // Đoạn đầu hấp thụ khoảng trống ở vị trí bắt đầu
		}
		end := len(normalized)
		if i+1 < len(points) {
			end = points[i+1].byte
		}
		title := strings.TrimSpace(p.d.Title)
		if title == "" {
			title = firstLine(normalized, p.byte, end)
		}
		switch p.d.Kind {
		case kindChapter:
			if strings.TrimSpace(bodyAfterTitle(normalized, p.byte, end)) == "" {
				// Nguồn tiểu thuyết mạng thực tế thường có chỗ dành riêng cho "chương đã khóa/trả phí": tiêu đề có, văn bản thiếu. Không thất bại toàn cục——
				// phủ quyết cuối cùng sẽ lãng phí toàn bộ lệnh gọi model của giai đoạn cắt phân; dòng tiêu đề được hợp nhất vào đoạn trước (không mất một chữ văn bản),
				// ghi vào Notes được hiển thị bởi xem trước xác nhận, người dùng không công nhận có thể dùng --guide để phán quyết (điểm dừng của RFC §8.4 tồn tại chính là vì việc này).
				seg.Notes = append(seg.Notes,
					fmt.Sprintf("Tiêu đề chương %q không có văn bản (byte %d..%d), đã được hợp nhất vào đoạn trước (thường gặp ở chương dành riêng đã khóa/trả phí)", title, start, end))
				if !absorb(end) {
					seg.Matter = append(seg.Matter, MatterSpan{Kind: kindFrontMatter, Title: title, Start: start, End: end})
				}
				continue
			}
			chapterNo++
			seg.Chapters = append(seg.Chapters, ChapterSpan{Number: chapterNo, Title: title, Start: start, End: end})
			if p.d.Uncertain {
				seg.Uncertain = append(seg.Uncertain, chapterNo)
			}
		default:
			seg.Matter = append(seg.Matter, MatterSpan{Kind: p.d.Kind, Title: title, Start: start, End: end})
		}
	}
	if chapterNo == 0 {
		return nil, fmt.Errorf("Cắt phân không tạo ra bất kỳ chương nào (group không tính là chương)")
	}
	// Chương cùng tên là tín hiệu xác định của "cùng chương bị cắt sai" (tên chương trong nguồn có quy ước tiêu đề không nên lặp lại), chỉ ghi Notes
	// để xem trước xác nhận kiểm tra thủ công (Notes không rỗng tức là chặn --yes)——có hợp nhất hay không không do Go phán quyết.
	titleAt := make(map[string]int, len(seg.Chapters))
	for _, c := range seg.Chapters {
		key := squashSpace(c.Title)
		if first, ok := titleAt[key]; ok && key != "" {
			seg.Notes = append(seg.Notes, fmt.Sprintf("Chương %d và chương %d có cùng tiêu đề (%q), nghi ngờ cùng chương bị cắt sai, vui lòng kiểm tra lại",
				c.Number, first, snippet(c.Title, 24)))
		} else {
			titleAt[key] = c.Number
		}
	}
	return seg, nil
}

// squashSpace loại bỏ tất cả khoảng trắng, dùng cho hiển thị tiêu đề và so sánh cùng tên——sự khác biệt về khoảng trắng/trang trí không tạo thành sự khác biệt về ngữ nghĩa.
func squashSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// firstLine trả về văn bản của dòng đầu tiên trong [start,end) sau khi loại bỏ khoảng trắng.
func firstLine(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// bodyAfterTitle trả về văn bản trong [start,end) sau khi loại bỏ dòng đầu tiên (tiêu đề).
// Tiêu đề chương nhiều dòng chiếm riêng dòng đầu tiên, văn bản ở phía sau; đoạn một dòng không có ngắt dòng (tình huống cắt phân điểm neo) toàn bộ đoạn chính là văn bản,
// lúc này trả về toàn bộ đoạn thay vì chuỗi trống——nếu không thì tiểu thuyết một dòng/nhiều chương một dòng hợp pháp sẽ bị phán đoán nhầm là "văn bản trống" và từ chối (RFC §8.3).
func bodyAfterTitle(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// planChunks cắt units thành các khoảng chỉ mục owned không chồng chéo, bao phủ hoàn chỉnh [start,end) dựa trên ngân sách byte.
// Kích thước lô được tính theo ngân sách ngữ cảnh, không theo số dòng hoặc số chương cố định (RFC §8.1).
func planChunks(units []SourceUnit, budgetBytes int) [][2]int {
	if len(units) == 0 {
		return nil
	}
	if budgetBytes <= 0 {
		return [][2]int{{0, len(units)}}
	}
	var chunks [][2]int
	start := 0
	acc := 0
	for i, u := range units {
		size := u.EndByte - u.StartByte
		if acc > 0 && acc+size > budgetBytes {
			chunks = append(chunks, [2]int{start, i})
			start = i
			acc = 0
		}
		acc += size
	}
	chunks = append(chunks, [2]int{start, len(units)})
	return chunks
}

// buildProjection lắp ráp một payload dự phóng cấu trúc của khoảng owned (bao gồm một lượng nhỏ ngữ cảnh), model chỉ trả về ranh giới cho owned.
// Đồng thời trả về tập hợp tất cả unit_id trong dự phóng (owned + khu vực ngữ cảnh), để kiểm tra đầu ra phân biệt ảo giác và vượt quá ranh giới.
func buildProjection(units []SourceUnit, owned [2]int, contextMargin, ctxBudget int, guidance string) (string, map[string]bool) {
	// Khu vực ngữ cảnh co lại theo giới hạn kép về số đơn vị và byte (chỉ theo số đơn vị khi ctxBudget<=0): đơn vị margin thường là
	// dòng bình thường, nhưng phân mảnh ảo của dòng cực dài có thể lên tới MaxUnitBytes, chỉ vài cái là có thể nuốt trọn toàn bộ ngân sách đầu vào——ngữ cảnh
	// chỉ là thông tin tham khảo, không đáng giá như vậy.
	lo, budget := owned[0], ctxBudget
	for lo > 0 && owned[0]-lo < contextMargin {
		if n := len(units[lo-1].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		lo--
	}
	hi, budget := owned[1], ctxBudget
	for hi < len(units) && hi-owned[1] < contextMargin {
		if n := len(units[hi].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		hi++
	}
	type projUnit struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	proj := struct {
		OwnedStart   string     `json:"owned_start"`
		OwnedEnd     string     `json:"owned_end"`
		Units        []projUnit `json:"units"`
		UserGuidance string     `json:"user_guidance,omitempty"`
	}{
		OwnedStart:   units[owned[0]].ID,
		OwnedEnd:     units[owned[1]-1].ID,
		UserGuidance: guidance,
	}
	ids := make(map[string]bool, hi-lo)
	for i := lo; i < hi; i++ {
		proj.Units = append(proj.Units, projUnit{ID: units[i].ID, Text: units[i].Text})
		ids[units[i].ID] = true
	}
	data, _ := json.MarshalIndent(proj, "", "  ")
	return string(data), ids
}

// segmentInputDigest bao phủ đầu vào ngữ nghĩa được tiêu thụ thực sự bởi hành động phân đoạn: nguồn chuẩn hóa, hướng dẫn người dùng, phiên bản prompt (RFC §6.3).
func segmentInputDigest(normalizedDigest, guidance, promptVersion string) string {
	return Digest([]byte(strings.Join([]string{"segment", promptVersion, normalizedDigest, guidance}, "\x00")))
}

// segmentChunkPath / segmentChunkDigest: đường dẫn và danh tính công cụ của bộ nhớ đệm ranh giới cấp lô.
// Danh tính ràng buộc danh tính cắt phân (nguồn+hướng dẫn+phiên bản prompt) và phạm vi đơn vị owned của lô——bất kỳ thay đổi nào ở thượng nguồn đều làm cho bộ nhớ đệm tự nhiên mất khớp.
func segmentChunkPath(owned [2]int) string {
	return fmt.Sprintf("%s/chunk-%06d-%06d.json", dirSegmentChunks, owned[0], owned[1])
}

func segmentChunkDigest(identity, loID, hiID string) string {
	return Digest([]byte(strings.Join([]string{"segment-chunk", identity, loID, hiID}, "\x00")))
}

// Segment thực hiện cắt phân ngữ nghĩa cho toàn bộ văn bản chuẩn hóa: gọi model nhận dạng ranh giới theo từng khoảng owned, sau đó kiểm tra bao phủ toàn văn.
// contextMargin số đơn vị ngữ cảnh, chunkBytes ngân sách byte khoảng owned, maxTokens ngân sách đầu ra một lần.
// Khi w không rỗng, ghi bộ nhớ đệm ranh giới theo từng lô vào đĩa (identity = segmentInputDigest): một lô có thể mất vài phút, bất kỳ lô nào thất bại
// không nên trả lại lệnh gọi của các lô đã hoàn thành——cùng triết lý với analyze từng chương, synthesize từng khoảng, trước đây cắt phân là
// giai đoạn đắt đỏ duy nhất không có lưu trữ nội bộ giai đoạn, một nơi thất bại là tất cả làm lại từ đầu.
func Segment(ctx context.Context, m callModel, systemPrompt string, normalized []byte, units []SourceUnit, guidance string, chunkBytes, contextMargin, maxTokens int, prof callProfile, w *Workspace, identity string) (*Segmentation, error) {
	chunks := planChunks(units, planningBudget(chunkBytes, systemPrompt, guidance))
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}
	var decisions []BoundaryDecision
	// chunk xử lý một khoảng owned: trúng bộ đệm gọi 0 lần; đầu ra bị cắt ngắn do độ dài và khoảng có thể chia nhỏ thì thu nhỏ lô một nửa
	// thử lại đệ quy (JSON ranh giới của một lượng lớn chương ngắn sẽ vượt qua đầu ra hiển thị, cùng triết lý với analyze thu nhỏ lô)——nửa lô có
	// đường dẫn bộ đệm độc lập, thành quả thử lại không trả lại; cấp đơn vị vẫn bị cắt ngắn mới là thật sự không đủ dung lượng.
	var chunk func(owned [2]int, cur, total int) ([]BoundaryDecision, error)
	chunk = func(owned [2]int, cur, total int) ([]BoundaryDecision, error) {
		lo, hi := units[owned[0]], units[owned[1]-1]
		rel, want := segmentChunkPath(owned), segmentChunkDigest(identity, lo.ID, hi.ID)
		if w != nil {
			if art, err := readArtifact[boundaryBatch](w, rel); err == nil && art.InputDigest == want {
				return art.Payload.Boundaries, nil
			}
		}
		// Một lệnh gọi model lô có thể mất vài phút, phản hồi từng lô tiến lên + tích lũy số ranh giới, bảng điều khiển mới không im lặng như treo máy.
		prof.step(cur, total, "Cắt phân lô %d/%d (%s..%s), đã nhận dạng được %d ranh giới...",
			cur, total, lo.ID, hi.ID, len(decisions))
		// Giới hạn byte khu vực ngữ cảnh lấy chunkBytes/8 nhưng không thấp hơn 4096: cái cần chặn là phân mảnh ảo của dòng cực dài
		// (một mảnh có thể lên tới MaxUnitBytes) nuốt trọn ngân sách đầu vào, chi phí margin của dòng bình thường vốn vô hại.
		payload, projIDs := buildProjection(units, owned, contextMargin, max(chunkBytes/8, 4096), guidance)
		ownedIDs := make(map[string]bool, owned[1]-owned[0])
		for i := owned[0]; i < owned[1]; i++ {
			ownedIDs[units[i].ID] = true
		}
		v := chunkValidator{projIDs: projIDs, ownedIDs: ownedIDs, unitByID: unitByID,
			normalized: normalized, coverStart: owned[0] == 0}
		batch, err := callStructured[boundaryBatch](ctx, m, segmentContract, systemPrompt, payload, maxTokens, prof, func(b *boundaryBatch) error {
			return v.validate(b.Boundaries)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) && owned[1]-owned[0] > 1 {
				mid := (owned[0] + owned[1]) / 2
				prof.step(0, 0, "Đầu ra ranh giới của lô %s..%s bị cắt ngắn (chương quá dày đặc), thu nhỏ lô một nửa để thử lại", lo.ID, hi.ID)
				prof.logger().Warn("imp cắt phân đầu ra bị cắt ngắn, thu nhỏ lô một nửa", "chunk", lo.ID+".."+hi.ID)
				left, lerr := chunk([2]int{owned[0], mid}, cur, total)
				if lerr != nil {
					return nil, lerr
				}
				right, rerr := chunk([2]int{mid, owned[1]}, cur, total)
				if rerr != nil {
					return nil, rerr
				}
				return append(left, right...), nil
			}
			return nil, fmt.Errorf("Cắt phân khoảng %s..%s: %w", lo.ID, hi.ID, err)
		}
		// Ranh giới khu vực ngữ cảnh thuộc quản lý của lô lân cận (nó sẽ báo cáo lại trong khoảng owned của chính mình), Go trực tiếp cắt bỏ:
		// Kỷ luật tọa độ do mã thực thi, thử lại ngữ nghĩa chỉ dành cho thất bại ngữ nghĩa thực sự——hành vi cũ phản hồi hỏi lại cho vượt quá ranh giới,
		// model yếu thường dùng hết 3 lần thử lại kéo sập cả lô (RFC §8.1 "Model quản lý ngữ nghĩa, Go quản lý tọa độ").
		kept := make([]BoundaryDecision, 0, len(batch.Boundaries))
		for _, bd := range batch.Boundaries {
			if ownedIDs[bd.UnitID] {
				kept = append(kept, bd)
			}
		}
		if n := len(batch.Boundaries) - len(kept); n > 0 {
			// Kỷ luật tọa độ thường lệ chứ không phải ngoại lệ, dùng tiến độ bình thường để phản hồi——màu cảnh báo sẽ làm người dùng tưởng lầm là có lỗi.
			prof.step(0, 0, "Đã cắt bỏ %d ranh giới báo thừa của khu vực ngữ cảnh (do lô lân cận tự báo cáo, không phải lỗi)", n)
		}
		// Phản hồi phán đoán ngữ nghĩa của model (tiêu đề được nhận dạng), để người dùng thấy model hiểu được gì, thay vì chỉ có đếm cơ học.
		if len(kept) > 0 {
			prof.step(0, 0, "Model nhận dạng được: %s", previewBoundaries(kept))
		}
		if w != nil {
			if err := writeArtifact(w, rel, want, boundaryBatch{Boundaries: kept}); err != nil {
				return nil, fmt.Errorf("Ghi lô cắt phân %s..%s: %w", lo.ID, hi.ID, err)
			}
		}
		return kept, nil
	}
	for ci, owned := range chunks {
		kept, err := chunk(owned, ci+1, len(chunks))
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, kept...)
	}
	seg, err := resolveSegmentation(normalized, units, decisions)
	if err != nil {
		// Bộ nhớ đệm lô không còn giá trị khi tích hợp cuối cùng thất bại: digest khớp liên tục sẽ làm cho việc chạy lại gọi 0 lần đọc lại cùng một lô ranh giới,
		// tái hiện chắc chắn cùng một thất bại. Xóa bộ đệm đổi lấy cơ hội model cho lần cắt phân lại tiếp theo; ảnh chụp nhanh quyết định thông qua errSemantic
		// được đưa thống nhất vào failures/ để kiểm tra sau này. Xóa thất bại phải báo cáo trung thực——nói dối là đã xóa sẽ làm cho người dùng
		// khi chạy lại sẽ đọc lại bộ đệm xấu một lần nữa (Debug-First).
		hint := "Bộ đệm lô đã bị xóa, chạy lại sẽ cắt phân lại"
		if w != nil {
			if cerr := w.clearDir(dirSegmentChunks); cerr != nil {
				hint = fmt.Sprintf("Xóa bộ đệm lô thất bại: %v, vui lòng xóa thủ công meta/import/segment-chunks/ trước khi chạy lại", cerr)
			}
		}
		raw, _ := json.MarshalIndent(decisions, "", "  ")
		return nil, &errSemantic{Raw: string(raw), Err: fmt.Errorf("Tích hợp cắt phân toàn bộ sách thất bại (%s): %w", hint, err)}
	}
	return seg, nil
}

// planningBudget khấu trừ chi phí cấu trúc của yêu cầu từ ngân sách đầu vào: lời nhắc hệ thống và hướng dẫn được khấu trừ theo độ dài thực tế, phần còn lại được
// tính thành 3/4 cho sự gia tăng đột biến của việc đóng gói JSON dự phóng (id/ngoặc kép/chuyển nghĩa ≈ 1/3 văn bản)——văn bản owned chỉ là một phần của yêu cầu,
// quy hoạch theo toàn bộ sẽ vượt quá ngân sách đầu vào thực tế khi có lời nhắc dài hoặc khu vực ngữ cảnh lớn. Giới hạn dưới chunkBytes/4 ngăn chặn lời nhắc cực dài đẩy ngân sách
// thành số âm; chunkBytes<=0 có nghĩa là không có ngân sách (một lô), truyền qua nguyên trạng.
func planningBudget(chunkBytes int, systemPrompt, guidance string) int {
	if chunkBytes <= 0 {
		return chunkBytes
	}
	b := (chunkBytes - len(systemPrompt) - len(guidance)) * 3 / 4
	return max(b, chunkBytes/4)
}

// boundaryLabel cung cấp cho quyết định ranh giới một định danh có thể đọc được: ưu tiên tiêu đề, không có tiêu đề lùi về kind@unit_id.
func boundaryLabel(d BoundaryDecision) string {
	if t := strings.TrimSpace(d.Title); t != "" {
		return t
	}
	return d.Kind + "@" + d.UnitID
}

// previewBoundaries nén một lô quyết định ranh giới thành một dòng xem trước tiêu đề (tối đa 3 + số đếm), dùng cho bảng điều khiển phản hồi.
func previewBoundaries(bs []BoundaryDecision) string {
	titles := make([]string, 0, 3)
	for _, b := range bs {
		titles = append(titles, snippet(boundaryLabel(b), 24))
		if len(titles) == 3 {
			break
		}
	}
	s := strings.Join(titles, " / ")
	if len(bs) > len(titles) {
		s += fmt.Sprintf(" (tổng cộng %d chỗ)", len(bs))
	}
	return s
}

// chunkValidator chứa ngữ cảnh kiểm tra trong thời gian gọi của một lệnh gọi cắt phân: unit_id ngoài dự phóng là ảo giác; khu vực owned
// ranh giới còn phải có kind hợp pháp, anchor có thể phân tích, đồng vị không xung đột ngữ nghĩa; lô đầu tiên phải có ranh giới bọc điểm bắt đầu văn bản.
// Nếu không chặn các giá trị xấu này trong lúc gọi, chúng sẽ đi theo lô vào bộ nhớ đệm——digest khớp liên tục, chạy lại 0 lần gọi đọc lại cùng một dữ liệu xấu,
// thất bại tái hiện chắc chắn (RFC §8.3). Phán đoán ngữ nghĩa (giữ lại cái nào, phần đầu là gì) sau khi hỏi lại được trả về cho model,
// Go không trả lời thay; ranh giới khu vực ngữ cảnh được định sẵn là bị kỷ luật tọa độ cắt bỏ, không hỏi lại cho nó.
type chunkValidator struct {
	projIDs, ownedIDs map[string]bool
	unitByID          map[string]SourceUnit
	normalized        []byte
	coverStart        bool // Lô đầu tiên: văn bản không rỗng trước điểm bắt đầu văn bản phải có phân bổ ranh giới
}

func (v chunkValidator) validate(bs []BoundaryDecision) error {
	seen := make(map[int]BoundaryDecision)
	first := -1
	for _, b := range bs {
		if b.UnitID == "" {
			return fmt.Errorf("Ranh giới thiếu unit_id")
		}
		if !v.projIDs[b.UnitID] {
			return fmt.Errorf("Ranh giới unit_id %q không tồn tại trong dự phóng lần này", b.UnitID)
		}
		if !v.ownedIDs[b.UnitID] {
			continue
		}
		switch b.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return fmt.Errorf("Ranh giới %s kind không hợp lệ: %q (chỉ có thể là chapter/group/front_matter/back_matter)", b.UnitID, b.Kind)
		}
		at, err := resolveBoundaryByte(v.unitByID, b.UnitID, b.Anchor)
		if err != nil {
			return err
		}
		// Hiển thị tiêu đề: tiêu đề của chapter/group phải tồn tại thực sự trong nguyên bản đơn vị ranh giới (bỏ qua sự khác biệt về khoảng trắng)——
		// tiêu đề bịa đặt bị chặn lại bởi thực tế ở đây (đo lường thực tế một nguồn 157 chương có 67 chương là ranh giới mà model tạo ra trên văn bản nối tiếp trong chương+
		// tiêu đề bịa đặt). Phán đoán ngữ nghĩa vẫn thuộc về model: nguồn thực sự không có quy ước tiêu đề có thể đặt uncertain để giữ lại tiêu đề quy nạp;
		// tiêu đề mô tả của front/back matter có rủi ro thấp, không đối chiếu.
		if (b.Kind == kindChapter || b.Kind == kindGroup) && !b.Uncertain {
			if t := squashSpace(b.Title); t != "" && !strings.Contains(squashSpace(v.unitByID[b.UnitID].Text), t) {
				return fmt.Errorf("Tiêu đề %q của ranh giới %s không tìm thấy trong nguyên bản đơn vị đó: nếu đây là phần tiếp nối của chương trước, vui lòng không thiết lập ranh giới cho nó (được phân bổ bởi ranh giới trước đó, boundaries có thể rỗng); nếu nguyên bản thực sự không có dòng tiêu đề ở đây, tiêu đề là do bạn quy nạp, vui lòng đặt uncertain=true",
					b.UnitID, snippet(b.Title, 24))
			}
		}
		// Xung đột đồng vị (kind/tiêu đề khác nhau) là vấn đề ngữ nghĩa, giữ lại cái nào không do Go phán quyết; lặp lại giống hệt
		// là dư thừa cơ học, sau khi cho qua sẽ do resolve xóa trùng lặp im lặng.
		if prev, ok := seen[at]; ok {
			if prev.Kind != b.Kind || boundaryLabel(prev) != boundaryLabel(b) {
				return fmt.Errorf("Ranh giới %q và %q rơi vào cùng một vị trí (%s), xung đột ngữ nghĩa, vui lòng chỉ giữ lại cái đúng",
					boundaryLabel(prev), boundaryLabel(b), b.UnitID)
			}
		} else {
			seen[at] = b
		}
		if first < 0 || at < first {
			first = at
		}
	}
	if v.coverStart {
		head := first
		if head < 0 {
			head = len(v.normalized) // Lô đầu tiên không báo cáo một ranh giới owned nào: toàn bộ văn bản bắt đầu chưa được phân bổ
		}
		if head > 0 && strings.TrimSpace(string(v.normalized[:head])) != "" {
			return fmt.Errorf("%d byte văn bản bắt đầu (%s…) chưa được phân bổ cho ranh giới nào, vui lòng bổ sung ranh giới cho phần đầu văn bản (front_matter/chapter/group)",
				head, snippet(string(v.normalized[:min(head, 48)]), 24))
		}
	}
	return nil
}
