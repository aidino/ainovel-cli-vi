// Package arbiter là tầng phán quyết ngữ nghĩa: LLM-as-function đánh thức theo nhu cầu.
//
// Hai mặt phẳng đối xứng (docs/engine-arbiter.md §Hai):
//
//	Mặt phẳng tất định: flow.LoadState   → flow.Route     → Instruction
//	Mặt phẳng ngữ nghĩa: arbiter.Collect* → arbiter.Decide* → XxxDecision
//
// Kỷ luật: Collect tập trung IO (đọc đủ sự kiện từ store); Decide ngoài yêu cầu model do trình thực thi thống nhất quản lý thì không có IO,
// có thể dùng facts lịch sử phát lại ngoại tuyến; thực thi thuộc về Engine. Mỗi kịch bản có một cặp hàm + loại Decision độc quyền,
// hành động không khớp kịch bản sẽ không thể biểu đạt trên kiểu dữ liệu; tính hợp pháp còn lại do Validate của từng loại từ chối——
// Đầu ra của Arbiter giống như mọi đầu ra LLM khác là không đáng tin, kiểm tra sự kiện là cửa ải cuối cùng.
package arbiter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// decideMaxTokens giới hạn đầu ra cho một lần phán quyết; JSON phán quyết rất nhỏ, phần lớn dành cho ngân sách suy nghĩ của model suy luận
// (Cùng lý do với userrules.normalizeMaxTokens).
const decideMaxTokens = 8192

// decide giao nộp hợp đồng kịch bản và kiểm tra nghiệp vụ cho trình thực thi có cấu trúc thống nhất. Không có IO ngoài việc gọi model.
func decide[T any](ctx context.Context, model agentcore.ChatModel, contract llmcontract.Contract, systemPrompt, payload string, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, model, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(decideMaxTokens)},
		Validate:     validate,
		Agent:        "arbiter",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("chọn giao thức phán quyết", "module", "arbiter",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("tự sửa đầu ra phán quyết", "module", "arbiter", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("arbiter: %w", err)
	}
	return out, nil
}

// DispatchOp là hành động điều phát chia sẻ chung cho các kịch bản.
type DispatchOp struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

// workerNames là mục tiêu điều phát hợp pháp (Khớp với đăng ký của agents.BuildWorkers). Lát cắt có thứ tự:
// Vừa làm schema enum (đảm bảo thứ tự giữ fingerprint ổn định) vừa làm danh sách trắng kiểm tra.
var workerNames = []string{"architect_long", "architect_short", "writer", "editor"}

func (d *DispatchOp) validate() error {
	if d == nil {
		return nil
	}
	if !slices.Contains(workerNames, d.Agent) {
		return fmt.Errorf("dispatch.agent không hợp lệ: %q", d.Agent)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("dispatch.task không được rỗng")
	}
	return nil
}

// dispatchSchema là schema có thể null cho DispatchOp: chỉ hành động cần điều phát mới cấp object,
// những trường hợp khác là null (chế độ strict thì tất cả field đều required, ngữ nghĩa optional thể hiện qua null).
func dispatchSchema(desc string) map[string]any {
	return llmcontract.Nullable(schema.Object(
		schema.Property("agent", schema.Enum(desc, workerNames...)).Required(),
		schema.Property("task", schema.String("mô tả nhiệm vụ đầy đủ giao cho worker đó")).Required(),
	))
}

// marshalPayload serialize gói sự kiện; thất bại là lỗi chương trình, bắt buộc phải báo lỗi——âm thầm làm giả sự kiện trống
// sẽ làm cho model phán đoán sai dựa trên input giả.
func marshalPayload(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("arbiter: serialize gói dữ kiện thất bại: %w", err)
	}
	return string(data), nil
}