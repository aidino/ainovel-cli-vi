package domain

// TimelineEvent sự kiện dòng thời gian.
type TimelineEvent struct {
	Chapter    int      `json:"chapter"`
	Time       string   `json:"time"`
	Event      string   `json:"event"`
	Characters []string `json:"characters,omitempty"`
}

// ForeshadowEntry mục chi tiết gieo mầm.
type ForeshadowEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	PlantedAt   int    `json:"planted_at"`
	Status      string `json:"status"` // planted / advanced / resolved
	ResolvedAt  int    `json:"resolved_at,omitempty"`
}

// ForeshadowUpdate thao tác thay đổi chi tiết gieo mầm.
type ForeshadowUpdate struct {
	ID          string `json:"id"`
	Action      string `json:"action"` // plant / advance / resolve
	Description string `json:"description,omitempty"`
}

// RestoreOwnPlants đưa các chi tiết gieo mầm plant đã gieo trong chương ở bản ghi cũ nhưng bản ghi mới chưa khai báo lại về đầu hàng.
// Trong một chương đã gieo những chi tiết gieo mầm nào là sự thật lịch sử của chính nó, viết lại chính văn không thay đổi điểm này; nếu bỏ đi,
// khi phát lại toàn bộ bản ghi chương, advance/resolve của chương này và các chương sau sẽ không tìm thấy plant tiền quyết, toàn bộ chuỗi sẽ báo lỗi.
func RestoreOwnPlants(prev, next []ForeshadowUpdate) []ForeshadowUpdate {
	declared := make(map[string]struct{}, len(next))
	for _, u := range next {
		if u.Action == "plant" {
			declared[u.ID] = struct{}{}
		}
	}
	var restored []ForeshadowUpdate
	for _, u := range prev {
		if u.Action != "plant" {
			continue
		}
		if _, ok := declared[u.ID]; ok {
			continue
		}
		declared[u.ID] = struct{}{}
		restored = append(restored, u)
	}
	if len(restored) == 0 {
		return next
	}
	// plant phải xếp trước advance/resolve của cùng chương, khi phát lại mới có thể tạo mục trước.
	return append(restored, next...)
}

// RelationshipEntry mục quan hệ nhân vật.
type RelationshipEntry struct {
	CharacterA string `json:"character_a"`
	CharacterB string `json:"character_b"`
	Relation   string `json:"relation"`
	Chapter    int    `json:"chapter"`
}

// ConsistencyIssue vấn đề nhất quán.
type ConsistencyIssue struct {
	Type           string `json:"type"`     // Chiều vấn đề cụ thể do model đưa ra dựa trên rubric
	Severity       string `json:"severity"` // critical / error / warning
	Description    string `json:"description"`
	Evidence       string `json:"evidence,omitempty"` // Bằng chứng: đoạn văn bản gốc, tình tiết cụ thể hoặc dữ liệu trạng thái
	Suggestion     string `json:"suggestion,omitempty"`
	Chapters       []int  `json:"chapters,omitempty"` // Bằng chứng thực tế rơi vào các chương nào
	RequiresChange bool   `json:"requires_change"`    // Có nên lập tức đưa vào hàng đợi làm lại hay không, do Editor phán đoán ngữ nghĩa
}

// DimensionScore điểm đánh giá theo chiều đơn lẻ.
type DimensionScore struct {
	Dimension string `json:"dimension"`         // Xác định bởi rubric đánh giá, có thể mở rộng theo task
	Score     int    `json:"score"`             // 0-100
	Verdict   string `json:"verdict,omitempty"` // Tương thích với đọc kiểm cũ; runtime không dùng ngưỡng để đè lên phán đoán của model nữa
	Comment   string `json:"comment,omitempty"` // Kết luận ngắn gọn về chiều này
}

// ReviewEntry mục đọc kiểm của Editor.
type ReviewEntry struct {
	Chapter          int                `json:"chapter"`
	Scope            string             `json:"scope"` // chapter / global / arc
	Issues           []ConsistencyIssue `json:"issues"`
	Dimensions       []DimensionScore   `json:"dimensions,omitempty"`      // Điểm theo các chiều
	ContractStatus   string             `json:"contract_status,omitempty"` // met / partial / missed
	ContractMisses   []string           `json:"contract_misses,omitempty"` // Các mục contract chưa đạt được
	ContractNotes    string             `json:"contract_notes,omitempty"`  // Mô tả ngắn gọn tình hình thực hiện contract
	Verdict          string             `json:"verdict"`                   // accept / polish / rewrite
	Summary          string             `json:"summary"`
	AffectedChapters []int              `json:"affected_chapters,omitempty"` // Số chương cần viết lại/đánh bóng
}

// CriticalCount trả về số lượng vấn đề cấp critical.
func (r *ReviewEntry) CriticalCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "critical" {
			n++
		}
	}
	return n
}

// ErrorCount trả về số lượng vấn đề cấp error.
func (r *ReviewEntry) ErrorCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			n++
		}
	}
	return n
}

// Dimension trả về điểm của chiều được chỉ định; nếu không tồn tại thì trả về nil.
func (r *ReviewEntry) Dimension(name string) *DimensionScore {
	if r == nil {
		return nil
	}
	for i := range r.Dimensions {
		if r.Dimensions[i].Dimension == name {
			return &r.Dimensions[i]
		}
	}
	return nil
}
