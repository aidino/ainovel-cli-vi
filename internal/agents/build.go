package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/agents/guard"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// agentToRole chuẩn hóa subagent name thành role name mà ModelSet nhận diện được.
// architect_short / architect_long đều dùng chung một cấu hình architect role.
// Đồng nghĩa với host.agentRoleName, vì build và host không phụ thuộc lẫn nhau nên mỗi bên giữ một bản.
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// promptCacheBase dẫn xuất mã băm ngắn ổn định từ thư mục sách, làm tiền tố danh tính cache prompt: cùng một cuốn sách
// khởi động lại qua nhiều tiến trình sẽ chia sẻ bucket định tuyến, và không tiết lộ đường dẫn cục bộ cho provider. Hậu tố vai trò do caller ghép,
// subagent mỗi lần spawn sẽ nối thêm "#seq" (một phiên một khóa).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}

// subagentMaxRetries là giới hạn LLM retry của tất cả Worker.
// Chiến lược lùi lại: lùi lại theo hàm mũ (bị giới hạn bởi maxDelay), ưu tiên tuân theo Retry-After của server.
// Công cụ chỉ khởi động sau khi tin nhắn Assistant hoàn chỉnh được gửi, nên stream-idle / 503 /
// giật lag mạng ngắn có thể retry an toàn trong Worker, sẽ không phát lại tác dụng phụ của công cụ.
const subagentMaxRetries = 7

// UsageRecorder là callback lượng sử dụng tùy chọn của BuildWorkers; chữ ký giống OnMessage,
// mỗi tin nhắn agent sẽ gọi một lần, do tầng Host chịu trách nhiệm tổng hợp. task là văn bản nhiệm vụ của lần spawn này
// dùng làm danh tính phiên, để phát hiện đứt chuỗi cache và reset lại cơ sở theo phiên.
// nil nghĩa là không theo dõi.
type UsageRecorder func(agentName, task string, msg agentcore.AgentMessage)

// ApplyThinking áp dụng cường độ suy luận của vai trò cụ thể vào Worker (dùng để điều chỉnh /model lúc chạy).
// architect → hai subagent architect_*; writer/editor → subagent tương ứng.
// level rỗng = dùng mặc định của model/provider. Bỏ qua các tên role khác.
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel chuyển chuỗi cấu hình thành agentcore.ThinkingLevel.
// "" hợp pháp (= không ghi đè/kế thừa); các giá trị khác phải là off/low/medium/high/xhigh/max,
// nếu không trả về error (hạ cấp thành rỗng và warn khi khởi động, hiển thị lỗi cho người dùng lúc chạy).
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax:
		return lv, nil
	default:
		return "", fmt.Errorf("cường độ suy luận không hợp lệ %q (chọn: off/low/medium/high/xhigh/max)", s)
	}
}

func ResolveThinkingForModel(model agentcore.ChatModel, level agentcore.ThinkingLevel) (agentcore.ThinkingLevel, bool) {
	level = agentcore.NormalizeThinkingLevel(level)
	// Đối với model chat thường không hỗ trợ thinking, chỉ định rõ off không phải là no-op, mà là tham số bất hợp pháp.
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return agentcore.ThinkingAuto, level == agentcore.ThinkingAuto
	}
	return llm.ThinkingPolicyFor(model).Resolve(level)
}

func AvailableThinkingForModel(model agentcore.ChatModel) []agentcore.ThinkingLevel {
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return []agentcore.ThinkingLevel{agentcore.ThinkingAuto}
	}
	return llm.ThinkingPolicyFor(model).Available
}

// roleThinking phân giải cường độ suy luận có hiệu lực cho vai trò; giá trị không hợp lệ hạ cấp thành rỗng (không ghi đè) và warn.
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveReasoningEffort(role))
	if err != nil {
		slog.Warn("bỏ qua cấu hình cường độ suy luận không hợp lệ", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

func resolvedRoleThinking(model agentcore.ChatModel, cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	resolved, _ := ResolveThinkingForModel(model, roleThinking(cfg, role))
	return resolved
}

// BuildWorkers lắp ráp 3 Worker(architect_short/long, writer, editor) thành bộ
// subagent.Runner gọi lập trình được. Engine gọi trực tiếp lối vào có định kiểu của nó, không có tầng công cụ LLM
// (docs/engine-rfc.md §1).
// Trả về Runner, WriterRestorePack và ApplyThinking (liên kết cường độ suy luận các vai trò với /model lúc chạy;
// ContextManager của từng Worker do factory tự động tạo lại).
// onGuardBlock tùy chọn (nil an toàn): callback kiểm toán chặn/nâng cấp của StopGuard từng Worker.
func BuildWorkers(
	cfg bootstrap.Config,
	store *store.Store,
	styleStats *tools.StyleStatsIndex,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
	onGuardBlock guard.BlockHook,
) (*subagent.Runner, *ctxpack.WriterRestorePack, ApplyThinking) {
	// Công cụ dùng chung
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, styleStats)
	readChapter := tools.NewReadChapterTool(store)

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveBookTool(store),
		tools.NewSaveFoundationTool(store),
		tools.NewReviseOutlineTool(store),
		tools.NewResolveOutlineFeedbackTool(store),
		tools.NewAuditFoundationTool(store),
	}
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store, styleStats),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover chỉ ghi log, không thông báo host
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("chuyển provider",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := models.ForRoleWithFailover("writer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)

	// ContextManager do factory tạo lại mỗi lần gọi, cửa sổ bám theo swap model tự động (xem factory bên dưới).
	architectProvider, architectModelName, _ := models.CurrentSelection("architect")
	architectContextWindow, architectSource := cfg.ResolveContextWindow(architectProvider, architectModelName)
	bootstrap.LogContextWindowChoice("architect", architectModelName, architectContextWindow, architectSource)

	writerProvider, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerProvider, writerModelName)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	editorProvider, editorModelName, _ := models.CurrentSelection("editor")
	editorContextWindow, editorSource := cfg.ResolveContextWindow(editorProvider, editorModelName)
	bootstrap.LogContextWindowChoice("editor", editorModelName, editorContextWindow, editorSource)

	// modelLookup khi ghi vào session sẽ đính kèm _meta:{provider,model} cho mỗi tin nhắn assistant,
	// giúp replay không còn phụ thuộc "ModelSet hiện tại" để tính lại chi phí lịch sử, chuyển đổi model trong lúc chạy cũng tính chính xác.
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, task, msg)
		}
	}

	// Bộ đệm prompt: một truyện một gốc, một nhân vật một tên, một phiên một khóa (subagent spawn thêm #seq).
	// Dòng OpenAI dùng prompt_cache_key làm ái lực định tuyến; Dòng Claude dùng cache_control cuộn điểm ngắt
	// (sàn system + mũi tin nhắn cuối). Khi provider không hỗ trợ thì agentcore sẽ âm thầm bỏ qua theo năng lực,
	// dưới phiên nhiều lượt đọc bộ đệm lợi ích luôn dương, nên không đặt công tắc.
	cacheBase := promptCacheBase(store.Dir())

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return guard.NewArchitectStopGuard(store, onGuardBlock)
	}
	// ContextManager của Architect / Editor mỗi lần chạy đều tạo lại theo model hiện tại, cửa sổ bám theo swap model.
	roleContextFactory := func(profile roleContextProfile) func(agentcore.ChatModel) agentcore.ContextManager {
		return func(model agentcore.ChatModel) agentcore.ContextManager {
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newRoleContextManager(profile, model, window, contextTool.Name())
		}
	}
	architectThinking, _ := ResolveThinkingForModel(architectModel, roleThinking(cfg, "architect"))
	architectShort := subagent.Config{
		Name:                  "architect_short",
		Description:           "Quy hoạch sư truyện ngắn: sinh thiết lập đặc chắc và đại cương phẳng cho truyện một tập, một xung đột, mật độ cao",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectShort,
		Tools:                 architectTools,
		MaxTurns:              15,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_short",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			return foundationReadyResult(toolName, result)
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:                  "architect_long",
		Description:           "Quy hoạch sư trường thiên: sinh thiết lập phân tầng và đại cương tập arc cho truyện dài kỳ nâng cấp bền vững",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectLong,
		Tools:                 architectTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_long",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult:   architectLongShouldStopAfterToolResult,
		StopGuardFactory:      architectStopGuardFactory,
	}

	// Tuyến đường lắp ráp duy nhất: mẫu giao thức {{VOICE}} điền lại đoạn văn phong tại chỗ, sau đó nối thêm preset phong cách.
	// voice A/B của eval đi chung một hàm, đảm bảo hai nhánh tương đương (docs/voice-layer.md §3.2).
	writerPrompt := assets.BuildWriterPrompt(bundle.Prompts.Writer, bundle.Voice, bundle.Styles[cfg.Style])

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:             "writer",
		Description:      "Người sáng tác: tự chủ hoàn thành thiết tưởng, viết, tự kiểm và nộp một chương",
		Model:            writerModel,
		SystemPrompt:     writerPrompt,
		Tools:            writerTools,
		MaxTurns:         30,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    resolvedRoleThinking(writerModel, cfg, "writer"),
		StopAfterTools:   []string{"commit_chapter"},
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-writer",
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return guard.NewWriterStopGuard(store, onGuardBlock)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// Mỗi chương xây lại trình quản lý ngữ cảnh theo model writer hiện tại.
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:         model,
				ContextWindow: window,
				ReserveTokens: bootstrap.CompactReserveTokens(window),
				Agent:         "writer",
				// Cam kết chiếu hình, tránh các lượt sau liên tục sửa tiền tố yêu cầu.
				CommitProjected: true,
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					MinResultTokens: 200,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:                  "editor",
		Description:           "Người đọc kiểm: đọc nguyên văn, phát hiện vấn đề ở hai tầng kết cấu và thẩm mỹ",
		Model:                 editorModel,
		SystemPrompt:          bundle.Prompts.Editor,
		Tools:                 editorTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         resolvedRoleThinking(editorModel, cfg, "editor"),
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-editor",
		ContextManagerFactory: roleContextFactory(editorContextProfile),
		// Trúng sản phẩm trạng thái cuối thì dừng ngay. Thoát trạng thái cuối vẫn sẽ tham vấn StopGuard (test hợp đồng TestContract_
		// TerminalToolExitConsultsStopGuard), NewEditorStopGuard nhận biết nhiệm vụ sẽ chịu trách nhiệm
		// phủ quyết việc thoát sớm "bị phái đi sinh tóm tắt nhưng chỉ làm đọc kiểm lại", nên save_review có thể dừng cứng an toàn.
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(store, task, onGuardBlock)
		},
	}

	runner := subagent.NewRunner(architectShort, architectLong, writer, editor)

	// Lúc chạy liên kết cường độ suy luận của các vai (dành cho /model điều chỉnh).
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "architect":
			level, _ = ResolveThinkingForModel(models.ForRole("architect"), level)
			runner.SetThinkingLevel("architect_short", level)
			runner.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			level, _ = ResolveThinkingForModel(models.ForRole(role), level)
			runner.SetThinkingLevel(role, level)
		}
	}

	return runner, restore, applyThinking
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}

func architectLongShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if foundationReadyResult(toolName, result) {
		return true
	}
	r := decodeSaveFoundationResult(toolName, result)
	switch r.Type {
	case "expand_arc", "complete_book":
		return true
	default:
		return false
	}
}

func foundationReadyResult(toolName string, result json.RawMessage) bool {
	if toolName != "audit_foundation" {
		return false
	}
	var r struct {
		FoundationReady bool `json:"foundation_ready"`
	}
	return json.Unmarshal(result, &r) == nil && r.FoundationReady
}