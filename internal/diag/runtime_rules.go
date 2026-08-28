package diag

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Ngưỡng phát hiện runtime.
const (
	repeatCritical = 8 // Lặp lại gần đạt số lần này sẽ nâng lên critical
	streamIdleWarn = 3 // Ngưỡng cảnh báo lũy kế stream_idle
)

// RuntimeRuleFunc là chữ ký thống nhất của quy tắc chẩn đoán runtime (tương ứng RuleFunc bên sáng tác).
// Tham số đầu vào là RuntimeCapture sau khi làm nhạy và tổng hợp, xuất ra Finding dạng báo cáo - tất cả là AutoNone,
// Chỉ chẩn đoán, không tạo Action (kỷ luật quan sát viên, xem architecture.md §2.3).
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings chạy toàn bộ quy tắc runtime.
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose là điểm vào chẩn đoán hoàn chỉnh của /diag: chẩn đoán sáng tác + tín hiệu runtime + phát hiện runtime,
// trả về Report đã gộp và RuntimeCapture gốc (dùng để xuất tái sử dụng, tránh lấy lại).
// Finding runtime chỉ gộp vào Findings để hiển thị, không đổi Actions - giữ nguyên việc quan sát thuần túy.
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors chỉ đánh giá "lỗi / tham số không hợp lệ xuất hiện lặp lại gần đây" thành Finding.
// Không chạm vào việc lặp lại công cụ bình thường - subagent/novel_context/read_chapter v.v. trong chạy dài tự nhiên
// tần suất cao, số lần lũy kế không phải tín hiệu vòng lặp; "lặp lại mà không tiến" thực sự được stuckStep bắt lại.
func repeatedErrors(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, r := range rc.Repeats {
		var rule, title, sugg string
		switch {
		case strings.Contains(r.Sig, " · err: "):
			rule = "RepeatedToolError"
			title = "tool lặp lại cùng một lỗi"
			sugg = "Cùng một tool gần đây lặp lại trả về cùng lỗi, phần nhiều do tham số model không hợp lệ hoặc không khớp hợp đồng tool; kiểm tra việc kiểm tra tool của agentcore / quy ước tham số prompt (xem #34)."
		case strings.Contains(r.Sig, "(args invalid)"):
			rule = "ArgsInvalidLoop"
			title = "tham số lặp lại không phân tích được"
			sugg = "Tham số model gửi đến không phân tích được nhưng cứ thử lại; xem agentcore có ép kiểu lỏng lẻo cho kiểu này không (xem #34)."
		default:
			continue // Lặp lại công cụ bình thường không tạo Finding
		}
		sev := SevWarning
		if r.Count >= repeatCritical {
			sev = SevCritical
		}
		out = append(out, Finding{
			Rule:       rule,
			Category:   CatFlow,
			Severity:   sev,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      title,
			Evidence:   fmt.Sprintf("`%s` ×%d", r.Sig, r.Count),
			Suggestion: sugg,
		})
	}
	return out
}

// stuckStep phát hiện checkpoint dừng liên tục tại cùng một step.
func stuckStep(rc *RuntimeCapture) []Finding {
	if rc.StuckStep == "" {
		return nil
	}
	sev := SevWarning
	if rc.StuckCount >= repeatCritical {
		sev = SevCritical
	}
	return []Finding{{
		Rule:       "StuckStep",
		Category:   CatFlow,
		Severity:   sev,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      "checkpoint dừng đờ tại cùng một step",
		Evidence:   fmt.Sprintf("dừng liên tiếp tại `%s` ×%d", rc.StuckStep, rc.StuckCount),
		Suggestion: "Cùng một step được ghi lặp lại mà không tiến; kết hợp chữ ký lặp phía trên để định vị subagent nào bị kẹt.",
	}}
}

// streamIdleStorm phát hiện gián đoạn streaming xảy ra thường xuyên (#32).
func streamIdleStorm(rc *RuntimeCapture) []Finding {
	n := rc.LogKinds["stream_idle"]
	if n < streamIdleWarn {
		return nil
	}
	return []Finding{{
		Rule:       "StreamIdleStorm",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.provider",
		Title:      "Gián đoạn streaming thường xuyên (stream_idle)",
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: "Thượng nguồn lâu không nhả token nên bị watchdog giết nhầm; với model suy nghĩ chậm, tăng streamIdleTimeout, hoặc rà độ ổn định kết nối provider (xem #32).",
	}}
}