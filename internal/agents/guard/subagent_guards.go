package guard

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// subagentMaxConsecutiveBlocks chặn liên tiếp N lần sẽ nâng cấp thành chấm dứt, tránh model yếu rơi vào vòng lặp vô hạn.
const subagentMaxConsecutiveBlocks = 3

// BlockHook là callback kiểm toán của StopGuard: được gọi đồng bộ mỗi lần chặn/nâng cấp. Host dùng nó để đưa thông tin
// chặn lên luồng sự kiện TUI và thông báo ngoài màn hình——nếu không thì sự việc chặn chỉ vào log, người dùng trên giao diện chỉ thấy
// "giật lag + token tăng nhanh", không biết hệ thống đang tự phục hồi hay đang chạy không tải (issue #75).
// Callback không tham gia vào quyết định guard. Giá trị reason gồm:
//   - "blocked"    đã tiêm tin nhắn thúc giục, model sẽ tiếp tục tiến hành
//   - "escalated"  chạy không tải liên tục vượt giới hạn, vòng run này chấm dứt trả về tầng trên
//   - "hard_stop"  provider từ chối trả lời (safety/content_filter), chấm dứt ngay
type BlockHook func(agent, reason string, consecutive int32)

// hardStopReasons là những lý do từ chối trả lời từ phía provider mà không thể phục hồi bằng tin nhắn thúc giục. Tiêm
// "bắt buộc commit" không có tác dụng với chúng, ngược lại mỗi lần tạo ra tiêu hao token của một lần gọi LLM đầy đủ,
// và cuối cùng nâng cấp escalate xong lại khiến Engine chạy lại toàn bộ task Worker, cộng dồn lãng phí nhiều lần
// (thực tế ch02 đụng safety khi viết một chương tạo ra 3 lần gửi lại 17 lần gọi LLM, tỷ lệ trúng
// giảm từ 50% xuống 2.8%).
//
// Lưu ý StopReasonError / StopReasonAborted không cần đưa vào: agentcore trong
// loop.go nhận được 2 stop reason này sẽ trực tiếp chấm dứt run, hoàn toàn không gọi StopGuard.
// Ở đây chỉ liệt kê những ngữ nghĩa từ chối trả lời của provider mà thực sự đi đến StopGuard.
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard cấu tạo một StopGuard:
// sau baseline nếu không xuất hiện checkpoint của step được chỉ định, thì từ chối end_turn.
// baseline do bên gọi bắt tại thời điểm factory, đảm bảo ngữ nghĩa per-run chính xác.
//
// blockMsg nhận tập hợp checkpoint step đã quan sát được sau baseline, ghép thành theo tiến độ thực tế
// tin nhắn thúc giục——tin nhắn tĩnh trong kịch bản "công cụ thiết yếu liên tục báo lỗi" là đánh lạc hướng (thúc giục model gọi một
// công cụ đang thất bại, xem #75).
//
// Ngữ nghĩa đếm là "có tiến triển thì reset": giữa hai lần chặn từng xuất hiện
// bất kỳ checkpoint mới nào (draft lại / check v.v.) thì xem như model đang tiến hành, consecutive về 0;
// Chỉ có chạy không tải liên tục không sinh ra kết quả gì mới được tích lũy và nâng cấp chấm dứt.
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
	var lastBlockSeq atomic.Int64 // Seq của checkpoint mới nhất quan sát được ở lần chặn trước; -1 nghĩa là chưa từng chặn
	lastBlockSeq.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// Lỗi không thể phục hồi: leo thang trực tiếp, không lãng phí một lần thúc giục.
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard phát hiện treo không thể phục hồi, leo thang ngay",
				"module", "agent.guard", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			if onBlock != nil {
				onBlock(agentName, "hard_stop", consecutive.Load())
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// Quét ngược checkpoint sau baseline, thu thập các step đã xuất hiện (dùng chung cho phán đoán thả hành và thông báo tiến độ).
		// Checkpoint mới ở đuôi, gặp <= baseline là có thể break.
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
		// Từ lần chặn trước có công cụ ghi xuống đĩa = model đang đẩy tiến (như bị thúc giục rồi làm lại draft thử thu gọn),
		// reset bộ đếm; leo thang chỉ nên trừng phạt việc chạy không tiến triển, chứ không phải gom hết chặn của toàn run để báo phế.
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

// staticBlockMsg chuyển đổi văn bản cố định thành chữ ký blockMsg (sản phẩm của quy hoạch sư/người đọc kiểm là gọi công cụ một lần ghi đĩa,
// không tồn tại tiến độ nhiều bước, thúc giục tĩnh là đủ).
func staticBlockMsg(msg string) func(map[string]struct{}) string {
	return func(map[string]struct{}) string { return msg }
}

// NewWriterStopGuard yêu cầu writer trong lượt này phải có ít nhất một commit_chapter thành công.
// Tin nhắn thúc giục lắp ráp theo tiến độ step đã ghi đĩa: writer là subagent duy nhất có chuỗi công cụ nhiều bước,
// Việc yêu cầu tĩnh "phải gọi commit_chapter" khi thiếu bước tiền đề hoặc commit gặp lỗi sẽ gây hiểu nhầm.
func NewWriterStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "writer", []string{"commit"}, writerBlockMsg, onBlock)
}

// writerBlockMsg phán đoán writer đang bị kẹt ở bước nào dựa vào step checkpoint đã xuất hiện trong lượt này.
// Tên step tương ứng với giá trị công cụ ghi đĩa: plan / draft / edit / consistency_check / commit.
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

// NewArchitectStopGuard yêu cầu architect lượt này ít nhất ghi đĩa sản phẩm quy hoạch một lần.
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
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "tóm tắt tập") || strings.Contains(task, "tóm tắt tập"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			staticBlockMsg("Nhiệm vụ lần này là sinh tóm tắt tập: bạn phải gọi save_volume_summary ghi xuống đĩa rồi mới được kết thúc; save_review kiểm lại không tính là hoàn thành."), onBlock)
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "tóm tắt arc") || strings.Contains(task, "tóm tắt arc"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			staticBlockMsg("Nhiệm vụ lần này là sinh tóm tắt arc: bạn phải gọi save_arc_summary ghi xuống đĩa rồi mới được kết thúc; save_review kiểm lại không tính là hoàn thành."), onBlock)
	default:
		// Nhiệm vụ đọc kiểm hoặc tạm thời: bất kỳreview/summary nào ghi xuống đĩa cũng được (giữ hành vi rộng như cũ).
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			staticBlockMsg("Bạn phải gọi một trong các tool save_review / save_arc_summary / save_volume_summary để ghi kết quả xuống đĩa rồi mới được kết thúc."), onBlock)
	}
}