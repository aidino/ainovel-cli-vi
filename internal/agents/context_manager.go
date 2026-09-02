package agents

import (
	"log/slog"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// contextManagerConfig tập hợp toàn bộ tham số cấu hình của ContextManager.
type contextManagerConfig struct {
	Model            agentcore.ChatModel
	ContextWindow    int
	ReserveTokens    int
	Agent            string
	CommitProjected  bool
	Summary          *corecontext.FullSummaryConfig
	ToolMicrocompact *corecontext.ToolResultMicrocompactConfig
	ExtraStrategies  []corecontext.Strategy
}

func newContextManager(cfg contextManagerConfig) *corecontext.ContextEngine {
	var sc corecontext.FullSummaryConfig
	if cfg.Summary != nil {
		sc = *cfg.Summary
	}
	sc.Model = cfg.Model

	var tc corecontext.ToolResultMicrocompactConfig
	if cfg.ToolMicrocompact != nil {
		tc = *cfg.ToolMicrocompact
	}

	strategies := []corecontext.Strategy{
		corecontext.NewToolResultMicrocompact(tc),
	}
	strategies = append(strategies, cfg.ExtraStrategies...)
	strategies = append(strategies, corecontext.NewFullSummary(sc))

	var commitStrategies []string
	if cfg.CommitProjected {
		commitStrategies = make([]string, len(strategies))
		for i, strategy := range strategies {
			commitStrategies[i] = strategy.Name()
		}
	}

	engine := corecontext.NewEngine(corecontext.EngineConfig{
		ContextWindow:    cfg.ContextWindow,
		ReserveTokens:    cfg.ReserveTokens,
		CommitStrategies: commitStrategies,
		Strategies:       strategies,
	})

	callback := contextRewriteCallback(cfg.Agent)
	engine.SetProjectHook(callback)
	engine.SetRecoverHook(callback)
	return engine
}

// roleContextProfile mô tả hồ sơ nén ngữ cảnh cho các Worker kiểu "một nhiệm vụ,
// nhiều lần đọc" như Architect / Editor: chỉ dọn kết quả novel_context cũ (dữ liệu
// đã lưu đĩa có thể đọc lại bất cứ lúc nào), giữ kết quả tool ghi và nguyên văn chương;
// nếu vẫn vượt giới hạn thì dùng prompt chuyên biệt theo vai trò để tóm tắt toàn phần.
type roleContextProfile struct {
	Agent           string
	KeepRecentReads int // giữ lại bao nhiêu kết quả novel_context gần nhất không dọn
	Summary         corecontext.FullSummaryConfig
}

// newRoleContextManager xây dựng ContextManager cho hồ sơ theo cửa sổ model hiện tại.
func newRoleContextManager(p roleContextProfile, model agentcore.ChatModel, window int, contextToolName string) *corecontext.ContextEngine {
	summary := p.Summary
	return newContextManager(contextManagerConfig{
		Model:           model,
		ContextWindow:   window,
		ReserveTokens:   bootstrap.CompactReserveTokens(window),
		Agent:           p.Agent,
		CommitProjected: true,
		ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
			KeepRecent:      p.KeepRecentReads,
			MinResultTokens: 200,
			Classifier:      func(toolName string) bool { return toolName == contextToolName },
		},
		Summary: &summary,
	})
}

// contextRewriteCallback tạo callback log cho việc viết lại ngữ cảnh.
// Kiến trúc mới được đơn giản hóa chỉ ghi slog, không ghi runtime queue và UIEvent nữa.
func contextRewriteCallback(agent string) func(corecontext.RewriteEvent) {
	return func(ev corecontext.RewriteEvent) {
		attrs := []any{
			"module", "context",
			"agent", agent,
			"reason", ev.Reason,
			"strategy", ev.Strategy,
			"committed", ev.Committed,
			"tokens_before", ev.TokensBefore,
			"tokens_after", ev.TokensAfter,
		}
		if info := ev.Info; info != nil {
			attrs = append(attrs,
				"msgs_before", info.MessagesBefore,
				"msgs_after", info.MessagesAfter,
				"compacted", info.CompactedCount,
				"kept", info.KeptCount,
				"duration_ms", info.Duration.Milliseconds(),
			)
		}
		slog.Warn("viết lại ngữ cảnh", attrs...)
	}
}
