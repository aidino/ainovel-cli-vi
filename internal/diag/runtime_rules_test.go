package diag

import "testing"

// TestRuntimeFindings_Classify 证明lặp lại 签名按形态分类、ngưỡng 升giảm cấp chính xác ，
// vàchạy 时 Finding 全部 AutoNone（观察者纪律：只诊断不产 Action）。
func TestRuntimeFindings_Classify(t *testing.T) {
	rc := RuntimeCapture{
		Repeats: []RepeatStat{
			{Sig: "writer-ch07 · err: InputValidationError", Count: 14}, // vòng lặp lỗi critical
			{Sig: "writer-ch07 · novel_context", Count: 45},             // 正常高频công cụ → 不产 Finding
			{Sig: "writer · save_plan (args invalid)", Count: 4},        // tham số không hợp lệ  warning
		},
		StuckStep:  "writing.commit_ch07",
		StuckCount: 9, // kẹt critical
		LogKinds:   map[string]int{"stream_idle": 4},
		LogErrors:  270, // 长跑累计，không nên tạo Finding đơn lẻ
	}

	fs := runtimeFindings(&rc)
	sev := map[string]Severity{}
	for _, f := range fs {
		sev[f.Rule] = f.Severity
		if f.AutoLevel != AutoNone {
			t.Errorf("%s nên là AutoNone（观察者纪律），got %s", f.Rule, f.AutoLevel)
		}
	}

	want := map[string]Severity{
		"RepeatedToolError": SevCritical,
		"ArgsInvalidLoop":   SevWarning,
		"StuckStep":         SevCritical,
		"StreamIdleStorm":   SevWarning,
	}
	for rule, w := range want {
		if sev[rule] != w {
			t.Errorf("%s: got %q want %q", rule, sev[rule], w)
		}
	}
	// 正常高频công cụ / 日志累计 error không nên tạo Finding（避免长跑误报）。
	if _, ok := sev["RepeatedToolCall"]; ok {
		t.Error("普通công cụlặp lại không nên tạo Finding")
	}
	if _, ok := sev["LogErrorBurst"]; ok {
		t.Error("日志 error 累计không nên tạo Finding đơn lẻ")
	}
}

// TestRuntimeFindings_Quiet 证明无ngoại lệ 信号时不产任何chạy 时 Finding（零误报）。
func TestRuntimeFindings_Quiet(t *testing.T) {
	rc := RuntimeCapture{
		LogKinds:  map[string]int{"stream_idle": 1}, // thấp hơn ngưỡng
		LogErrors: 2,
	}
	if fs := runtimeFindings(&rc); len(fs) != 0 {
		t.Errorf("安tĩnh không nên tạo Finding，got %d: %+v", len(fs), fs)
	}
}
