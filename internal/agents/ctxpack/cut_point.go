package ctxpack

// Polyfill cho corecontext.FindCutPoint / corecontext.CutResult.
// Upstream (voocel/ainovel-cli) đã dùng API export mới của agentcore nhưng
// phiên bản agentcore v1.8.2 hiện tại chỉ có hàm private findCutPoint.
// File này cung cấp bản sao tương đương để code sync lại biên dịch được.
// Khi agentcore cập nhật, xóa file này và đổi import lại.

import (
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

// CutResult giữ kết quả cắt điểm nén, bao gồm thông tin chia lượt.
type CutResult struct {
	// FirstKeptIndex là chỉ số tin nhắn đầu tiên được giữ lại.
	FirstKeptIndex int
	// IsSplitTurn là true khi điểm cắt rơi giữa một lượt hội thoại.
	IsSplitTurn bool
}

// FindCutPoint đi ngược từ cuối, tích lũy token cho đến khi đạt keepTokens.
// Trả về kết quả cắt có nhận thức lượt hội thoại.
func FindCutPoint(msgs []agentcore.AgentMessage, keepTokens int) CutResult {
	if len(msgs) == 0 {
		return CutResult{}
	}

	accumulated := 0
	cutIndex := len(msgs)

	for i := len(msgs) - 1; i >= 0; i-- {
		accumulated += corecontext.EstimateTokens(msgs[i])
		if accumulated >= keepTokens {
			cutIndex = i
			break
		}
	}

	if cutIndex >= len(msgs) {
		return CutResult{}
	}

	rawCut := cutIndex // ghi lại vị trí cắt thô để fallback

	// Căn chỉnh đến điểm cắt hợp lệ: đi tới tìm biên giới tin nhắn user.
	// Không bao giờ cắt cặp tool (assistant có toolCalls + kết quả tool phía sau).
	for cutIndex < len(msgs) {
		msg := msgs[cutIndex]
		m, ok := msg.(agentcore.Message)
		if !ok {
			break
		}
		if m.Role == agentcore.RoleTool {
			cutIndex++
			continue
		}
		if m.Role == agentcore.RoleUser {
			break
		}
		if m.Role == agentcore.RoleAssistant && m.HasToolCalls() {
			cutIndex++
			for cutIndex < len(msgs) {
				next, ok := msgs[cutIndex].(agentcore.Message)
				if ok && next.Role == agentcore.RoleTool {
					cutIndex++
				} else {
					break
				}
			}
			continue
		}
		break
	}

	// Fallback: nếu forward alignment vượt quá end (ví dụ toàn tool groups),
	// đi ngược từ rawCut tìm đầu tool group gần nhất (biên assistant có toolCalls).
	if cutIndex >= len(msgs) {
		cutIndex = -1
		for i := rawCut; i >= 1; i-- {
			m, ok := msgs[i].(agentcore.Message)
			if ok && m.Role == agentcore.RoleAssistant && m.HasToolCalls() {
				cutIndex = i
				break
			}
		}
		if cutIndex <= 0 {
			return CutResult{}
		}
	}

	// Phát hiện lượt bị chia: nếu điểm cắt không ở tin nhắn user, tìm đầu lượt.
	isSplitTurn := false
	if m, ok := msgs[cutIndex].(agentcore.Message); !ok || m.Role != agentcore.RoleUser {
		for i := cutIndex - 1; i >= 0; i-- {
			if um, ok := msgs[i].(agentcore.Message); ok && um.Role == agentcore.RoleUser {
				isSplitTurn = true
				break
			}
		}
	}

	return CutResult{
		FirstKeptIndex: cutIndex,
		IsSplitTurn:    isSplitTurn,
	}
}
