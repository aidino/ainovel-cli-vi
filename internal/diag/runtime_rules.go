package diag

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// 运行时检测阈值。
const (
	repeatCritical = 8 // 近端重复达到此次数升为 critical
	streamIdleWarn = 3 // stream_idle 累计告警阈值
)

// RuntimeRuleFunc 是运行时诊断规则的统一签名（对应创作侧的 RuleFunc）。
// 入参是脱敏聚合后的 RuntimeCapture，产出报告型 Finding——全部 AutoNone，
// 只诊断、不产 Action（观察者纪律，见 architecture.md §2.3）。
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings 跑全部运行时规则。
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose 是 /diag 的完整诊断入口：创作诊断 + 运行时信号 + 运行时检测，
// 返回合并后的 Report 与原始 RuntimeCapture（供导出复用，避免重复抓取）。
// 运行时 Finding 仅并入 Findings 供展示，不改 Actions——保持纯观察。
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors 只把"近端反复出现的错误 / 参数无效"判成 Finding。
// 不碰普通工具重复——subagent/novel_context/read_chapter 等在长跑里天然
// 高频，累计次数不是循环信号；真正的"反复而不推进"由 stuckStep 兜住。
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
			continue // 普通工具重复不产 Finding
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

// stuckStep 检测 checkpoint 连续停在同一 step。
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

// streamIdleStorm 检测流式中断频发（#32）。
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