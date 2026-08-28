package guard

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// subagentMaxConsecutiveBlocks 连续阻拦 N 次后升级为终止，避免弱模型死循环。
const subagentMaxConsecutiveBlocks = 3

// BlockHook 是 StopGuard 的审计回调：每次拦截/升级时同步调用。Host 用它把拦截
// 事实浮出到 TUI 事件流与离屏通知——否则拦截只进日志，用户在界面上只看到
// "卡顿+token 变快"，无从判断系统是在自愈还是在空转（issue #75）。
// 回调不参与 guard 决策。reason 取值：
//   - "blocked"    已注入催促消息，模型将继续推进
//   - "escalated"  连续空转超限，本轮 run 终止交回上层
//   - "hard_stop"  provider 拒答（safety/content_filter），立即终止
type BlockHook func(agent, reason string, consecutive int32)

// hardStopReasons 是无法用催促消息恢复的 provider 端拒答原因。注入
// "必须 commit" 对它们无效，反而每次产生一次完整 LLM 调用的 token 消耗，
// 并最终升级 escalate 后让 Engine 重跑整个 Worker 任务，叠加多倍浪费
// （实测 ch02 撞 safety 时一次写章产生 3 次重派 17 次 LLM 调用、命中率
// 从 50% 跌到 2.8%）。
//
// 注意 StopReasonError / StopReasonAborted 不需要列入：agentcore 在
// loop.go 收到这两种 stop reason 时直接终止 run，根本不会调用 StopGuard。
// 这里只列那些会真正走到 StopGuard 的 provider 拒答语义。
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard 构造一个 StopGuard：
// 在 baseline 之后若未出现指定 step 的 checkpoint，则拒绝 end_turn。
// baseline 由调用方在 factory 时刻捕获，保证 per-run 语义正确。
//
// blockMsg 接收 baseline 之后已观测到的 checkpoint step 集合，按实际进度组装
// 催促消息——静态消息在"必需工具本身持续报错"的场景下是误导（催模型去调一个
// 正在失败的工具，见 #75）。
//
// 计数语义是"有进展即重置"：两次拦截之间出现过
// 任何新 checkpoint（重新 draft / check 等）视为模型在推进，consecutive 归零；
// 只有毫无产物的连续空转才累计并升级终止。
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg func(seen map[string]struct{}) string, onBlock BlockHook) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	var lastBlockSeq atomic.Int64 // 上次拦截时观测到的最新 checkpoint Seq；-1 表示尚未拦截过
	lastBlockSeq.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// 不可恢复错误：直接升级，不浪费一次催促。
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard phát hiện treo không thể phục hồi, leo thang ngay",
				"module", "agent.guard", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			if onBlock != nil {
				onBlock(agentName, "hard_stop", consecutive.Load())
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// 倒序扫描 baseline 之后的 checkpoint，收集已出现的 step（放行判定 + 进度消息共用）。
		// 新 checkpoint 在尾部，遇到 <= baseline 即可 break。
		all := st.Checkpoints.All()
		latestSeq := baseline
		seen := make(map[string]struct{})
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if cp.Seq > latestSeq {
				latestSeq = cp.Seq
			}
			seen[cp.Step] = struct{}{}
		}
		for s := range need {
			if _, ok := seen[s]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		// 上次拦截以来有新工件落盘 = 模型在推进（如被催后重新 draft 再试探收尾），
		// 重置计数；升级只应惩罚毫无进展的空转，而不是把整个 run 的拦截攒在一起报废。
		if prev := lastBlockSeq.Load(); prev >= 0 && latestSeq > prev {
			consecutive.Store(0)
		}
		lastBlockSeq.Store(latestSeq)
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard chặn liên tiếp vượt hạn, leo thang thành chấm dứt",
				"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock(agentName, "escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard chặn end_turn",
			"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock(agentName, "blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg(seen)}
	}
}

// staticBlockMsg 把固定文案适配成 blockMsg 签名（架构/编辑器的产物是单工具落盘，
// 不存在多步进度，静态催促即够）。
func staticBlockMsg(msg string) func(map[string]struct{}) string {
	return func(map[string]struct{}) string { return msg }
}

// NewWriterStopGuard 要求 writer 本轮至少产生一次成功的 commit_chapter。
// 催促消息按已落盘的 step 进度组装：writer 是唯一有多步工具链的子代理，
// 静态的"必须调 commit_chapter"在前置步骤缺失或 commit 本身报错时是误导。
func NewWriterStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "writer", []string{"commit"}, writerBlockMsg, onBlock)
}

// writerBlockMsg 按本轮已出现的 checkpoint step 判断 writer 卡在哪一步。
// step 名与各工具落盘值对应：plan / draft / edit / consistency_check / commit。
func writerBlockMsg(seen map[string]struct{}) string {
	_, hasDraft := seen["draft"]
	_, hasEdit := seen["edit"]
	_, hasCheck := seen["consistency_check"]
	switch {
	case !hasDraft && !hasEdit:
		return "Cấm kết thúc: lượt này chưa ghi bất kỳ phần thân nào xuống đĩa. Hãy hoàn thành chương theo thứ tự plan_chapter → draft_chapter → check_consistency → commit_chapter; phần thân chỉ xuất trong chat coi như mất, bắt buộc ghi xuống đĩa qua tool và nộp."
	case !hasCheck:
		return "Cấm kết thúc: phần thân đã ghi nhưng chưa khép lại. Hãy trước hết gọi check_consistency đối chiếu tính nhất quán, rồi gọi commit_chapter nộp chương này. draft_chapter / edit_chapter chỉ là lưu bản thảo, không tính hoàn thành."
	default:
		return "Cấm kết thúc: chương này chỉ còn thiếu commit_chapter. Hãy gọi commit_chapter ngay; nếu nó trả lỗi, trước hết xử lý theo thông tin lỗi (đối chiếu số chương, bổ sung hành động tiền đề theo gợi ý) rồi thử nộp lại, đừng kết thúc khi chưa nộp."
	}
}

// NewArchitectStopGuard 要求 architect 本轮至少落盘一次规划产物。
func NewArchitectStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"book", "premise", "outline", "layered_outline", "characters", "world_rules",
			"foundation_audit", "expand_arc", "append_volume", "update_compass", "complete_book", "revise_outline", "resolve_outline_feedback",
		},
		staticBlockMsg("Bạn phải gọi save_book, save_foundation, revise_outline, resolve_outline_feedback hoặc audit_foundation để ghi sản phẩm xuống đĩa rồi mới được kết thúc. Chỉ xuất văn bản Markdown/JSON coi như mất."),
		onBlock,
	)
}

// NewEditorStopGuard yêu cầu editor lượt này phải ghi sản phẩm khớp với "nhiệm vụ" rồi mới được kết thúc.
//
// Nhận thức nhiệm vụ: khi được điều đi sinh tóm tắt, chỉ save_review (kiểm lại) không tính hoàn thành — phải sản xuất tóm tắt tương ứng.
// Nếu không, editor "được điều sinh tóm tắt arc lại đi kiểm lại trước" sẽ thỏa tiêu chí rộng cũ mà kết thúc sớm, tóm tắt arc mãi không xuống đĩa
// (cộng với khử trùng dispatcher thất bại từng gây vòng lặp arc khung trong tập, xem outline-exhaustion-livelock).
// Thoát tool trạng thái cuối cũng sẽ hỏi StopGuard (test hợp đồng TestContract_TerminalToolExitConsultsStopGuard),
// nên save_review dừng cứng trong build.go là an toàn: trong nhiệm vụ tóm tắt, editor đi kiểm lại trước thì guard này sẽ
// phủ quyết lần thoát đó và thúc giục, cho đến khi tóm tắt tương ứng được ghi xuống đĩa.
func NewEditorStopGuard(st *store.Store, task string, onBlock BlockHook) agentcore.StopGuard {
	switch {
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "tóm tắt tập") || strings.Contains(task, "卷摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			staticBlockMsg("Nhiệm vụ lần này là sinh tóm tắt tập: bạn phải gọi save_volume_summary ghi xuống đĩa rồi mới được kết thúc; save_review kiểm lại không tính là hoàn thành."), onBlock)
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "tóm tắt arc") || strings.Contains(task, "弧摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			staticBlockMsg("Nhiệm vụ lần này là sinh tóm tắt arc: bạn phải gọi save_arc_summary ghi xuống đĩa rồi mới được kết thúc; save_review kiểm lại không tính là hoàn thành."), onBlock)
	default:
		// Nhiệm vụ đọc kiểm hoặc tạm thời: bất kỳreview/summary nào ghi xuống đĩa cũng được (giữ hành vi rộng như cũ).
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			staticBlockMsg("Bạn phải gọi một trong các tool save_review / save_arc_summary / save_volume_summary để ghi kết quả xuống đĩa rồi mới được kết thúc."), onBlock)
	}
}