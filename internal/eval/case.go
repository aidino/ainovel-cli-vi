// Package eval là harness đánh giá ngoại tuyến của ainovel-cli.
//
// Điểm xuất phát thiết kế: Các trình đánh giá (chẩn đoán xác định diag, phong cách toàn sách stylestat, tiêu chí 7 chiều rubric) đã
// tồn tại trong dự án, eval chỉ đóng vai trò là một lớp mỏng——điều khiển case hàng loạt, thu thập kết quả, ánh xạ diag Finding với hợp đồng case
// thành cổng chặn, tổng hợp báo cáo. Một định nghĩa thực tế, không viết lại phán đoán ở lớp đánh giá. Xem chi tiết tại docs/evaluation-system.md.
//
// Hiện tại đã bao phủ luồng chính xác định: cổng chặn đơn, baseline/variant A/B delta, tổng hợp repeat và hồi quy stylestat.
// LLM Judge vẫn là một lớp tiếp theo tùy chọn, không được làm ô nhiễm cổng chặn xác định.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// caseIDPattern giới hạn case id là các ký tự an toàn: id sẽ được ghép vào thư mục đầu ra và được RunCase dọn dẹp bằng RemoveAll,
// cấm các ký tự đường dẫn như . / v.v., chấm dứt việc xuyên thấu đường dẫn "../" xóa ra ngoài không gian làm việc.
var caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const defaultDeltaRatio = 0.3

// Case là một mẫu đánh giá: một đoạn yêu cầu sáng tác + một tập hợp các khẳng định ở lớp sự thật.
type Case struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`       // Lớp đánh giá: smoke/workflow/quality/longform/recovery/steering
	Role          string   `json:"role,omitempty"` // Vai trò được kiểm tra: writer/architect/editor (trực giao với Category)
	Description   string   `json:"description,omitempty"`
	Prompt        string   `json:"prompt"`                   // Yêu cầu sáng tác của người dùng
	Style         string   `json:"style,omitempty"`          // Ghi đè phong cách cấu hình
	MaxChapters   int      `json:"max_chapters"`             // Giới hạn số chương; 0 nghĩa là chỉ chạy đến khi hoàn thành quy hoạch (vào writing)
	TargetPrompts []string `json:"target_prompts,omitempty"` // Các file prompt chủ yếu được xác minh bởi case này (mang tính thông tin)
	Rubric        string   `json:"rubric,omitempty"`         // Bảng điểm LLM Judge (Kích hoạt ở Phase 3)
	Expect        Expect   `json:"expect"`
	Gate          Gate     `json:"gate"`
}

// Expect là khẳng định hợp đồng cấp case——chỉ khai báo các kỳ vọng mà quy tắc chung của diag không bao phủ được, liên quan chặt chẽ đến case này.
type Expect struct {
	Phase                string   `json:"phase,omitempty"`                  // Phase cuối cùng kỳ vọng
	MinCompletedChapters int      `json:"min_completed_chapters,omitempty"` // Số chương hoàn thành tối thiểu
	RequiredCheckpoints  []string `json:"required_checkpoints,omitempty"`   // Có dạng "chapter:1:commit" / "arc:1:1:arc_summary" / "global:layered_outline"
	NoPending            []string `json:"no_pending,omitempty"`             // Tín hiệu cần dọn sạch khi kết thúc: pending_commit/pending_steer/last_commit/last_review
}

// Gate là ngưỡng cổng chặn của case này. Phiên bản này chỉ dùng MaxSeverity; các trường còn lại dành riêng cho giai đoạn A/B (regression),
// được phân tích nhưng không tham gia cổng chặn——giữ lại để file case có thể viết theo schema đầy đủ của docs/evaluation-system.md.
type Gate struct {
	MaxSeverity string `json:"max_severity,omitempty"` // Mức độ nghiêm trọng tối đa cho phép của diag Finding (mặc định: warning): vượt quá là hard fail

	MaxCostDeltaRatio     *float64 `json:"max_cost_delta_ratio,omitempty"`
	MaxToolCallDeltaRatio *float64 `json:"max_tool_call_delta_ratio,omitempty"`
	StylestatRegression   string   `json:"stylestat_regression,omitempty"`
}

// Validate kiểm tra các trường bắt buộc của case.
func (c *Case) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case thiếu id")
	}
	if !caseIDPattern.MatchString(c.ID) {
		return fmt.Errorf("case id không hợp lệ %q: chỉ cho chữ thường/số/gạch dưới/gạch ngang, không chứa ký tự đường dẫn", c.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("case %q thiếu prompt", c.ID)
	}
	if c.Gate.MaxSeverity == "" {
		c.Gate.MaxSeverity = "warning"
	}
	if !validSeverity(c.Gate.MaxSeverity) {
		return fmt.Errorf("gate.max_severity của case %q không hợp lệ: %s", c.ID, c.Gate.MaxSeverity)
	}
	if c.Gate.MaxCostDeltaRatio == nil {
		c.Gate.MaxCostDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.MaxToolCallDeltaRatio == nil {
		c.Gate.MaxToolCallDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.StylestatRegression == "" {
		c.Gate.StylestatRegression = "warn"
	}
	if !validStylestatGate(c.Gate.StylestatRegression) {
		return fmt.Errorf("gate.stylestat_regression của case %q không hợp lệ: %s", c.ID, c.Gate.StylestatRegression)
	}
	return nil
}

func float64Ptr(v float64) *float64 { return &v }

func validStylestatGate(s string) bool {
	switch s {
	case "warn", "block", "off":
		return true
	default:
		return false
	}
}

// LoadCases tải case từ một file .json đơn hoặc một thư mục. Đệ quy tải tất cả *.json trong thư mục, sắp xếp theo id.
func LoadCases(path string) ([]Case, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".json") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}

	var cases []Case
	seen := map[string]string{}
	for _, f := range files {
		c, err := loadCaseFile(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[c.ID]; dup {
			return nil, fmt.Errorf("case id trùng lặp: %q (%s và %s)", c.ID, prev, f)
		}
		seen[c.ID] = f
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("không tìm thấy case nào: %s", path)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func loadCaseFile(path string) (Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Case{}, err
	}
	var c Case
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // Báo lỗi trực tiếp nếu sai trường, tránh bỏ qua im lặng
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf("phân tích case %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Case{}, err
	}
	return c, nil
}