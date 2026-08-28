package rules

import (
	"fmt"
	"maps"
	"strings"
)

// Snapshot là điểm khôi phục quy tắc người dùng của cuốn sách này sau khi chuẩn hóa (meta/user_rules.json).
//
// Nó là nguồn sự thật duy nhất lúc chạy: khi mở sách/nhập/làm mới được gộp chuẩn hóa từ các nguồn, sau đó novel_context
// tiêm vào và commit_chapter kiểm tra đều chỉ đọc bản này, không đọc đi đọc lại file rules nữa (tránh trôi dạt và hai đầu đọc phân kỳ).
//
// Chỉ Structured + Preferences được tiêm cho model (xem Payload); Version / Status / Sources /
// Uncertain là siêu dữ liệu vận hành và chẩn đoán, không vào working_memory.user_rules.
type Snapshot struct {
	Version     int        `json:"version"`
	Status      Status     `json:"status"`
	Structured  Structured `json:"structured"`
	Preferences string     `json:"preferences"`
	Sources     []string   `json:"sources"`
	Uncertain   []string   `json:"uncertain"`
}

// Status đánh dấu điểm khôi phục chuẩn hóa thành công trọn vẹn hay không.
type Status string

const (
	// StatusReady mọi nguồn đều chuẩn hóa thành công.
	StatusReady Status = "ready"
	// StatusDegraded ít nhất một nguồn chuẩn hóa thất bại, đã hạ cấp thành raw preferences (xem chi tiết ở Uncertain / nhật ký).
	StatusDegraded Status = "degraded"
)

// SnapshotVersion là phiên bản schema của điểm khôi phục hiện tại, tiện cho việc di chuyển trong tương lai.
// v2: chapter_words rút khỏi structured (số từ là ràng buộc mềm ngữ nghĩa, đi qua preferences).
// Tương thích tải trực tiếp điểm khôi phục v1: các trường không xác định bị bỏ qua khi deserialize, lần gộp lưu tiếp theo sẽ tự động hội tụ về v2;
// Cố tình không làm "phiên bản không khớp thì xây dựng lại" —— như vậy sẽ làm mất các quy tắc không thể tái tạo được thêm vào lúc chạy bằng AddRuntimeRule.
const SnapshotVersion = 2

// Candidate là kết quả ứng viên của một nguồn sau khi chuẩn hóa.
//
// Nguồn được sắp xếp theo độ ưu tiên thấp→cao rồi giao cho BuildSnapshot gộp xác định. LLM chỉ chịu trách nhiệm biến
// ngôn ngữ tự nhiên của một nguồn thành Structured/Preferences ứng viên; độ ưu tiên và ghi đè trường do BuildSnapshot (Go) phán quyết.
type Candidate struct {
	Source      string     // Nhãn nguồn dễ đọc, đi vào Snapshot.Sources (ví dụ system_defaults / startup_prompt / global:my.md)
	Structured  Structured // Các trường cấu trúc ứng viên của nguồn này
	Preferences string     // Chính văn sở thích ngôn ngữ tự nhiên của nguồn này
	Uncertain   []string   // Các mục của nguồn này cố tình không nâng lên structured + lý do (chẩn đoán)
	Degraded    bool       // Nguồn này chuẩn hóa thất bại, đã hạ cấp thành raw preferences
}

// Payload trả về hình thái tiêm vào working_memory.user_rules: chỉ bộc lộ structured + preferences.
// Ngay cả khi đều rỗng vẫn trả về cấu trúc ổn định, tránh việc LLM thấy user_rules=null đi vào nhánh bất thường.
func (s Snapshot) Payload() map[string]any {
	return map[string]any{
		"structured":  s.Structured,
		"preferences": s.Preferences,
	}
}

// BuildSnapshot gộp xác định các ứng viên đã được xếp theo độ ưu tiên (thấp→cao) thành điểm khôi phục.
//
// Quy tắc gộp (tất cả tính xác định bên phía Go, không giao cho LLM):
//   - structured: ghi đè theo trường, nguồn ưu tiên cao ghi đè ưu tiên thấp; fatigue_words cộng dồn theo từ
//   - preferences: không ghi đè, nối theo thứ tự nguồn (ưu tiên cao ở sau), kèm tiêu đề nguồn
//   - giá trị rỗng/giá trị 0 được coi là thiếu trường, không ghi đè giá trị đã có (sanitizeStructured)
//   - Bất kỳ nguồn nào Degraded → status điểm khôi phục = degraded
func BuildSnapshot(cands []Candidate) Snapshot {
	snap := Snapshot{
		Version: SnapshotVersion,
		Status:  StatusReady,
		Sources: make([]string, 0, len(cands)),
	}
	var prefs []string
	for _, c := range cands {
		s := sanitizeStructured(c.Structured)
		if s.Genre != "" {
			snap.Structured.Genre = s.Genre
		}
		if len(s.ForbiddenChars) > 0 {
			snap.Structured.ForbiddenChars = s.ForbiddenChars
		}
		if len(s.ForbiddenPhrases) > 0 {
			snap.Structured.ForbiddenPhrases = s.ForbiddenPhrases
		}
		if len(s.FatigueWords) > 0 {
			snap.Structured.FatigueWords = mergeFatigueWords(snap.Structured.FatigueWords, s.FatigueWords)
		}

		if p := strings.TrimSpace(c.Preferences); p != "" {
			if src := strings.TrimSpace(c.Source); src != "" {
				prefs = append(prefs, fmt.Sprintf("## [%s]\n\n%s", src, p))
			} else {
				prefs = append(prefs, p)
			}
		}
		if src := strings.TrimSpace(c.Source); src != "" {
			snap.Sources = append(snap.Sources, src)
		}
		snap.Uncertain = append(snap.Uncertain, c.Uncertain...)
		if c.Degraded {
			snap.Status = StatusDegraded
		}
	}
	snap.Preferences = strings.Join(prefs, "\n\n")
	return snap
}

// OverlaySnapshot gộp một ứng viên ưu tiên cao đè lên điểm khôi phục hiện có (ứng viên thắng).
//
// Dùng cho hành động rules của Trọng tài lúc chạy: không chuẩn hóa lại mọi nguồn, chỉ ghi đè quy tắc mới vào điểm khôi phục hiện tại ——
// structured ghi đè theo trường, preferences nối thêm một đoạn, sources/uncertain cộng dồn, lan truyền hạ cấp.
func OverlaySnapshot(base Snapshot, cand Candidate) Snapshot {
	out := base
	out.Version = SnapshotVersion
	s := sanitizeStructured(cand.Structured)
	if s.Genre != "" {
		out.Structured.Genre = s.Genre
	}
	if len(s.ForbiddenChars) > 0 {
		out.Structured.ForbiddenChars = s.ForbiddenChars
	}
	if len(s.ForbiddenPhrases) > 0 {
		out.Structured.ForbiddenPhrases = s.ForbiddenPhrases
	}
	if len(s.FatigueWords) > 0 {
		out.Structured.FatigueWords = mergeFatigueWords(cloneFatigue(out.Structured.FatigueWords), s.FatigueWords)
	}
	if p := strings.TrimSpace(cand.Preferences); p != "" {
		section := p
		if src := strings.TrimSpace(cand.Source); src != "" {
			section = fmt.Sprintf("## [%s]\n\n%s", src, p)
		}
		if strings.TrimSpace(out.Preferences) == "" {
			out.Preferences = section
		} else {
			out.Preferences = out.Preferences + "\n\n" + section
		}
	}
	if src := strings.TrimSpace(cand.Source); src != "" {
		out.Sources = append(append([]string{}, out.Sources...), src)
	}
	if len(cand.Uncertain) > 0 {
		out.Uncertain = append(append([]string{}, out.Uncertain...), cand.Uncertain...)
	}
	if cand.Degraded {
		out.Status = StatusDegraded
	}
	return out
}

// mergeFatigueWords cộng dồn ngưỡng từ mệt mỏi theo từ, src ghi đè ngưỡng cùng từ trong dst (ưu tiên cái gần hơn).
// Giúp người dùng chỉ cần thêm một lượng nhỏ từ mệt mỏi, mà không cần liệt kê lại đường cơ sở tích hợp sẵn.
func mergeFatigueWords(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

func cloneFatigue(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	maps.Copy(out, m)
	return out
}

// SystemDefaults là đường cơ sở máy móc tích hợp sẵn trong mã nguồn (nguồn ưu tiên thấp nhất), không qua LLM chuẩn hóa.
//
// Giá trị gốc chuyển từ front matter của assets/rules/default.md cũ, đã bản địa hóa sang tiếng Việt.
// Căn cứ ngưỡng giữ nguyên: từ mệt mỏi đoạn sau (như một / im lặng / không nói gì / X nhịp hơi) đến từ
// thực chứng buổi chạy dài 196 chương — sau khi khuôn câu AI truyền thống bị triệt tiêu ở giai đoạn đầu,
// mô hình chuyển sang lạm dụng các "từ nhịp" này trung bình 5-7 lần mỗi chương; ngưỡng nới rộng để
// dung nạp mức sử dụng bình thường.
func SystemDefaults() Candidate {
	return Candidate{
		Source: "system_defaults",
		Structured: Structured{
			// Câu AI rập khuôn có độ dài cố định; checker khớp chuỗi con theo nghĩa đen, các mẫu có biến (không phải X mà là Y) thuộc về lớp ngữ nghĩa.
			ForbiddenPhrases: []string{"một mức độ nào đó", "đáng chú ý là", "không hiểu vì sao", "trăm mối cảm xúc"},
			FatigueWords: map[string]int{
				"không khỏi": 1, "như lại": 1, "tựa hồ": 2, "ngoài ra": 1, "tuy nhiên": 2,
				"một tia": 2, "một ánh": 2, "một làn": 2, "như thể": 1, "không nhịn được": 1,
				"như một": 3, "im lặng": 2, "không nói gì": 2, "mấy nhịp hơi": 3, "một nhịp hơi": 3, "vài nhịp": 2,
			},
		},
	}
}

// sanitizeStructured thực hiện "giá trị rỗng/giá trị 0 = thiếu trường": bộ chuẩn hóa có thể nhả ra placeholder kiểu genre:"" 
// (thực chứng nguyên mẫu), phải coi như chưa khai báo, tránh làm ô nhiễm việc gộp và kiểm tra máy móc.
func sanitizeStructured(s Structured) Structured {
	out := Structured{}
	if g := strings.TrimSpace(s.Genre); g != "" {
		out.Genre = g
	}
	out.ForbiddenChars = nonEmptyStrings(s.ForbiddenChars)
	out.ForbiddenPhrases = nonEmptyStrings(s.ForbiddenPhrases)
	out.FatigueWords = sanitizeFatigueWords(s.FatigueWords)
	return out
}

func nonEmptyStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func sanitizeFatigueWords(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for w, n := range m {
		if w = strings.TrimSpace(w); w == "" || n <= 0 {
			continue
		}
		out[w] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}