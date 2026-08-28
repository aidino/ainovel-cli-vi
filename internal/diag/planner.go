package diag

import "fmt"

// PlanActions 根据高置信 Finding 生成可执行动作。
// 只有 Confidence==high && AutoLevel==safe 的 Finding 才会产出 Action。
func PlanActions(findings []Finding) []Action {
	var actions []Action
	seen := make(map[string]struct{})

	for _, f := range findings {
		if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
			continue
		}
		if _, ok := seen[f.Rule]; ok {
			continue
		}
		seen[f.Rule] = struct{}{}

		actions = append(actions, planRule(f)...)
	}
	return actions
}

func planRule(f Finding) []Action {
	key := findingFingerprint(f)

	switch f.Rule {
	case "PhaseFlowMismatch":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEmitNotice, Severity: f.Severity, Summary: f.Title, Message: f.Title, Fingerprint: key},
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "sửa bất thường máy trạng thái", Message: "Bất thường máy trạng thái: " + f.Evidence + ". Vui lòng kiểm tra và sửa trạng thái phase/flow của progress trước rồi chạy tiếp.", Fingerprint: key},
		}
	case "OutlineExhausted":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "xử lý đại cương cạn", Message: "Số chương hoàn thành đã đạt giới hạn quy hoạch. Vui lòng ưu tiên gọi Architect triển khai arc kế tiếp hoặc thêm tập mới rồi viết tiếp.", Fingerprint: key},
		}
	case "OrphanedSteer":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "tiêu thụ can thiệp người dùng chưa xử lý", Message: "Tồn tại chỉ lệnh can thiệp người dùng chưa tiêu thụ, vui lòng ưu tiên xử lý pending steer rồi tiếp tục nhiệm vụ hiện tại.", Fingerprint: key},
		}
	default:
		return nil
	}
}

func findingFingerprint(f Finding) string {
	return fmt.Sprintf("%s|%s|%s|%s", f.Rule, f.Target, f.Title, f.Evidence)
}