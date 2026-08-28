package eval

import (
	"path/filepath"
	"testing"
)

// TestSmokeCasesLoad 确保仓库内置 smoke case 能被tải 器phân tích （含 DisallowUnknownFields 校验）。
func TestSmokeCasesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "evals", "cases", "smoke")
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("tải  smoke case thất bại: %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("kỳ vọng至少 3 个 smoke case，nhận được  %d", len(cases))
	}
	for _, c := range cases {
		if c.Category != "smoke" {
			t.Errorf("%s: category nên là smoke，nhận được  %s", c.ID, c.Category)
		}
		if c.Gate.MaxSeverity == "" {
			t.Errorf("%s: Validate nên điền max_severity mặc định", c.ID)
		}
		if c.Gate.MaxCostDeltaRatio == nil || *c.Gate.MaxCostDeltaRatio != 0.3 ||
			c.Gate.MaxToolCallDeltaRatio == nil || *c.Gate.MaxToolCallDeltaRatio != 0.3 {
			t.Errorf("%s: Validate nên điền delta ratio mặc định, nhận được cost=%v tool=%v",
				c.ID, c.Gate.MaxCostDeltaRatio, c.Gate.MaxToolCallDeltaRatio)
		}
		if c.Gate.StylestatRegression != "warn" {
			t.Errorf("%s: Validate nên mặc định stylestat_regression=warn, nhận được %s", c.ID, c.Gate.StylestatRegression)
		}
	}
}

func TestLoadCasesRejectsUnknownField(t *testing.T) {
	// 间接xác minh：hợp lệ  case phải 含 id+prompt；thiếu 即báo lỗi（Validate đường dẫn）。
	if _, err := LoadCases(filepath.Join("..", "..", "evals", "cases", "smoke", "writer_first_chapter.json")); err != nil {
		t.Fatalf("单tệptải 应thành công: %v", err)
	}
}

// case id 会拼进 RemoveAll 的đường dẫn，đường dẫn穿越/分隔符phải 被拒（rủi ro cao 防护）。
func TestCaseIDRejectsUnsafe(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "/abs", "..", "Up", "with space", "dot.case"} {
		c := Case{ID: bad, Prompt: "x"}
		if err := c.Validate(); err == nil {
			t.Errorf("非法 id %q nên bị từ chối", bad)
		}
	}
	for _, ok := range []string{"writer_first_chapter", "architect-long", "case1"} {
		c := Case{ID: ok, Prompt: "x"}
		if err := c.Validate(); err != nil {
			t.Errorf("hợp lệ  id %q 不nên bị từ chối: %v", ok, err)
		}
	}
}

func TestCaseRejectsInvalidGate(t *testing.T) {
	c := Case{ID: "bad_gate", Prompt: "x", Gate: Gate{StylestatRegression: "maybe"}}
	if err := c.Validate(); err == nil {
		t.Fatal("非法 stylestat_regression nên bị từ chối")
	}
	c = Case{ID: "disabled_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(-1), MaxToolCallDeltaRatio: float64Ptr(-1)}}
	if err := c.Validate(); err != nil {
		t.Fatalf("负数 delta ratio 应作为显式关闭被接受: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != -1 || *c.Gate.MaxToolCallDeltaRatio != -1 {
		t.Fatalf("显式关闭的 delta ratio 不应被默认值ghi đè: %+v", c.Gate)
	}
	c = Case{ID: "strict_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(0), MaxToolCallDeltaRatio: float64Ptr(0)}}
	if err := c.Validate(); err != nil {
		t.Fatalf("显式 0 delta ratio 应作为严格ngưỡng 被接受: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != 0 || *c.Gate.MaxToolCallDeltaRatio != 0 {
		t.Fatalf("显式 0 delta ratio 不应被默认值ghi đè: %+v", c.Gate)
	}
}
