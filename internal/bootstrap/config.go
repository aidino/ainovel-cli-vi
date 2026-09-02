package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// DefaultContextWindow Kích thước cửa sổ dự phòng khi model chưa đăng ký trong registry.
const DefaultContextWindow = 200000

// CompactRatio Ngưỡng tương đối kích hoạt nén ngữ cảnh: nén khi tokens >= window * CompactRatio.
// 0.85 là giá trị kinh nghiệm, chừa 15% không gian cho "prompt lượt tới + kết quả công cụ lớn", đồng thời cho cửa sổ lớn
// Model cũng có thể chủ động nén ở 85%, tránh đợi đầy cửa sổ danh nghĩa 1M mới nén (vùng suy giảm sự chú ý).
//
// Tỷ lệ nén không hiển thị cho người dùng cấu hình; người dùng chỉ cấu hình context_window thực sự của mỗi model.
const CompactRatio = 0.85

// MinCompactReserve là giới hạn dưới của ReserveTokens. Model cửa sổ nhỏ (như 32k local qwen3:8b)
// tính reserve theo tỷ lệ 0.15 chỉ có 4800, một lần phản hồi công cụ commit_chapter có thể nhét 5-8k,
// Một chương chính văn 8-15k——sẽ xuất hiện "nén xong lại vượt ngay". 8000 dự phòng đảm bảo trong trường hợp xấu nhất vẫn còn nửa vòng đệm.
const MinCompactReserve = 8000

// CompactReserveTokens tính ngược ReserveTokens theo CompactRatio và áp dụng sàn MinCompactReserve:
//
//	threshold = window - reserve = window * CompactRatio
//	reserve   = max(MinCompactReserve, window * (1 - CompactRatio))
//
// Dành cho EngineConfig.ReserveTokens của agentcore.context.Engine.
func CompactReserveTokens(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window - int(float64(window)*CompactRatio)
	if reserve < MinCompactReserve {
		return MinCompactReserve
	}
	return reserve
}

// ProviderConfig Định nghĩa chứng chỉ của một nhà cung cấp LLM đơn lẻ.
type ProviderConfig struct {
	Type    string        `json:"type,omitempty"`     // Kiểu giao thức API (openai/anthropic/gemini), chỉ định khi dùng proxy tùy chỉnh
	API     string        `json:"api,omitempty"`      // Endpoint giao thức OpenAI: chat (mặc định) / responses
	APIKey  string        `json:"api_key,omitempty"`  // API Key
	BaseURL string        `json:"base_url,omitempty"` // API Base URL
	Models  []ModelConfig `json:"models,omitempty"`   // Danh sách model tùy chọn, để hiển thị khi chuyển đổi ở TUI
	// ExtraBody Truyền xuyên suốt các tham số bổ sung cho mỗi yêu cầu của provider này (như temperature/top_p/min_p/
	// presence_penalty, hoặc các khóa đặc thù của hãng như chat_template_kwargs mở think của nvidia).
	// Đầu tương thích OpenAI sẽ gộp nguyên văn vào thân yêu cầu (tức quy ước extra_body); người dùng tự chịu trách nhiệm về giá trị.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra Truyền xuyên suốt cho cấu hình cấp provider (litellm.ProviderConfig.Extra), dùng cho
	// các tùy chọn HTTP headers, user_agent, anthropic_beta cấp client/tầng truyền tải.
	Extra map[string]any `json:"extra,omitempty"`
	// StreamIdleTimeout Watchdog rảnh rỗi dạng stream: quá thời gian này không nhận được chunk nào sẽ ngắt stream
	// (Chuỗi Go duration, ví dụ "900s" / "15m"). Để trống mặc định là 5m——giới hạn trên hợp lý của dịch vụ đám mây;
	// Suy luận chậm tự dựng như LocalAI/ollama thì khối đầu tiên có thể vượt quá 5 phút, chỉ cần nới lỏng theo provider,
	// Không làm chậm phát hiện treo của các kênh khác (#79).
	StreamIdleTimeout string `json:"stream_idle_timeout,omitempty"`
}

// ModelConfig mô tả các model có thể chuyển đổi dưới một provider và cửa sổ ngữ cảnh tùy chọn của chúng.
// Để tương thích cấu hình cũ, vừa có thể đọc từ chuỗi JSON ("model-name"), vừa có thể đọc từ đối tượng;
// Khi ghi lại luôn chuẩn hóa thành dạng đối tượng.
type ModelConfig struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window,omitempty"`
	// JSONSchema là khai báo 3 trạng thái của đầu ra có cấu trúc nguyên bản (response_format json_schema):
	// Chưa cấu hình = phán đoán theo năng lực cấp model của provider adapter; true = người dùng khai báo endpoint/model này
	// hỗ trợ (khi yêu cầu bị từ chối sẽ hiển thị nguyên trạng, không hạ cấp ngầm); false = bắt buộc dùng prompt contract.
	// Khả năng của proxy tùy chỉnh và cổng tổng hợp dựa vào khai báo của người dùng, chương trình không dò tìm.
	JSONSchema *bool `json:"json_schema,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		m.Name = legacy
		m.ContextWindow = 0
		m.JSONSchema = nil
		return nil
	}
	type modelConfigAlias ModelConfig
	var decoded modelConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("model config must be a string or object: %w", err)
	}
	*m = ModelConfig(decoded)
	return nil
}

// ModelConfig trả về cấu hình rõ ràng của model được chỉ định.
func (pc ProviderConfig) ModelConfig(name string) (ModelConfig, bool) {
	name = strings.TrimSpace(name)
	for _, model := range pc.Models {
		if strings.TrimSpace(model.Name) == name {
			return model, true
		}
	}
	return ModelConfig{}, false
}

// ModelJSONSchema trả về khai báo 3 trạng thái json_schema của model; khi chưa được đưa vào models hoặc chưa cấu hình
// trả về nil (phán đoán theo năng lực adapter).
func (c Config) ModelJSONSchema(provider, model string) *bool {
	if pc, ok := c.Providers[provider]; ok {
		if mc, ok := pc.ModelConfig(model); ok {
			return mc.JSONSchema
		}
	}
	return nil
}

// defaultStreamIdleTimeout: trong kịch bản đầu ra dài + ctx dài, reasoning-aware provider
// (mimo / deepseek-r1 v.v.) ở giai đoạn suy nghĩ nếu server không phát reasoning delta dạng stream,
// toàn bộ đoạn SSE sẽ giữ im lặng. Watchdog mặc định của litellm là 2 phút, với chương viết 8000 chữ thường
// kích hoạt giết nhầm; 5 phút bao phủ hầu hết các trường hợp thực tế (xem thống kê thời gian suy nghĩ plan→draft ở tasks/todo.md).
const defaultStreamIdleTimeout = 5 * time.Minute

// StreamIdleTimeoutValue phân tích timeout nhàn rỗi stream của provider; để trống lùi về giá trị mặc định.
func (pc ProviderConfig) StreamIdleTimeoutValue() (time.Duration, error) {
	s := strings.TrimSpace(pc.StreamIdleTimeout)
	if s == "" {
		return defaultStreamIdleTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use Go duration like \"900s\" / \"15m\")", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive, got %q", s)
	}
	return d, nil
}

// RequiresAPIKey trả về provider này có bắt buộc cấu hình api_key hay không.
// Quy ước:
// 1. ollama / bedrock cho phép không có key;
// 2. Cấu hình chỉ định Type rõ ràng được xem là proxy tùy chỉnh, cho phép không có key;
// 3. Các provider khác mặc định yêu cầu key, giữ nguyên kiểm tra thận trọng với các API lưu trữ chính thức.
func (pc ProviderConfig) RequiresAPIKey(name string) bool {
	switch name {
	case "ollama", "bedrock":
		return false
	}
	return pc.Type == ""
}

// ProviderType trả về loại giao thức API hợp lệ.
// Ưu tiên dùng Type chỉ định rõ; nếu không yêu cầu bản thân tên provider đã có trong registry litellm.
func (pc ProviderConfig) ProviderType(name string) (string, error) {
	if pc.Type != "" {
		return pc.Type, nil
	}
	if llm.IsProviderRegistered(name) {
		return name, nil
	}
	return "", fmt.Errorf("provider %q thiếu type, và không nằm trong danh sách provider đã biết của litellm: %w", name, errs.ErrConfig)
}

// ModelRef đại diện cho một tổ hợp provider/model.
type ModelRef struct {
	Provider string `json:"provider"` // tên provider (khóa trong map Providers)
	Model    string `json:"model"`    // tên model (truyền nguyên trạng, không parse gì cả)
}

// RoleConfig định nghĩa model ghi đè cho một vai trò đơn lẻ.
type RoleConfig struct {
	Provider  string     `json:"provider"`            // tên provider chính (khóa trong map Providers)
	Model     string     `json:"model"`               // tên model chính (truyền nguyên trạng, không parse gì cả)
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` // danh sách provider/model dự phòng rõ ràng
	// Cường độ suy luận của vai trò này (off/low/medium/high/xhigh/max), rỗng = kế thừa mặc định cấp trên cùng.
	// Do agents.ParseThinkingLevel kiểm tra rồi áp dụng, giá trị vượt cấp xem như rỗng.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// knownRoles tên vai trò có thể cấu hình được hỗ trợ. Arbiter hiện không mở cấu hình cấp vai trò,
// thống nhất dùng model mặc định cấp trên cùng (host.arbiterModel dùng models.Default).
// import_* là núm xoay cấp model của hàm ngữ nghĩa import (docs/import-pipeline.md §13.1):
// khi chưa cấu hình rơi vào architect, cấu hình xong có thể trỏ các hàm có tính máy móc cao hơn vào cấp rẻ hơn.
var knownRoles = map[string]bool{
	"architect":         true,
	"writer":            true,
	"editor":            true,
	"import_segment":    true,
	"import_analyze":    true,
	"import_synthesize": true,
}

// Config cấu hình ứng dụng tiểu thuyết.
type Config struct {
	// Trường lúc chạy (không serialize vào JSON)
	OutputDir string `json:"-"` // Thư mục gốc đầu ra

	// Cấu hình LLM mặc định
	Provider  string `json:"provider"` // provider mặc định (khóa trong map Providers)
	ModelName string `json:"model"`    // Tên model mặc định
	// ReasoningEffort Cường độ suy luận mặc định cấp trên cùng (off/low/medium/high/xhigh/max), rỗng = không ghi đè (dùng mặc định của model/provider).
	// Khi vai trò không cấu hình reasoning_effort riêng thì lùi về giá trị này.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Kho chứng chỉ Provider
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Model ghi đè cấp vai trò
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	// Tham số sáng tác
	Style string `json:"style,omitempty"`

	// ContextWindow là cửa sổ ngữ cảnh toàn cục bản cũ, giữ lại làm
	// dự phòng tương thích sau context_window đặc thù của model. Chỉ ảnh hưởng ngưỡng nén, không đổi độ dài yêu cầu thực của LLM API.
	ContextWindow int `json:"context_window,omitempty"`

	// Budget chính sách ngân sách chi phí cho một cuốn sách; book_usd > 0 mới bật.
	Budget BudgetConfig `json:"budget,omitzero"`

	// Notify cấu hình cảnh báo không người trực; mặc định bật (kênh system làm dự phòng).
	Notify NotifyConfig `json:"notify,omitzero"`

	// DisableUpdateCheck tắt nhắc nhở kiểm tra phiên bản mới khi khởi động (mặc định bật). Kiểm tra chỉ đọc
	// giao diện công khai GitHub Releases, kết quả cache trong thư mục cấu hình cục bộ, không gửi bất kỳ dữ liệu nào.
	DisableUpdateCheck bool `json:"disable_update_check,omitempty"`
}

// BudgetConfig là tuyên bố chính sách của người dùng đối với ví tiền của một cuốn sách. Dừng máy khi vượt vạch tương đương với việc người dùng
// Abort thủ công ngay lúc đó——Host chỉ thực thi thay, không đánh giá hành vi model (Kiến trúc §10 Ranh giới hợp hiến).
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // Bắt buộc mới bật; 0/mặc định = không giới hạn
	WarnRatio float64 `json:"warn_ratio,omitempty"` // Mức cảnh báo, mặc định 0.8
	HardStop  bool    `json:"hard_stop,omitempty"`  // true=vượt vạch dừng ngay; mặc định đợi task subagent hiện tại kết thúc
}

// Enabled trả về chính sách ngân sách có đang bật hay không.
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig cấu hình kênh cảnh báo không người trực.
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` // Mặc định true (kênh system không cần cấu hình cũng dùng được)
	Command string   `json:"command,omitempty"` // Tùy chọn, cấu hình xong sẽ thay thế kênh system (thông báo đẩy lên điện thoại qua đây)
	Events  []string `json:"events,omitempty"`  // Tùy chọn, lọc theo notify.Kinds; mặc định mở tất cả
}

// IsEnabled trả về cảnh báo có đang bật không (mặc định true).
func (n NotifyConfig) IsEnabled() bool { return n.Enabled == nil || *n.Enabled }

// ValidateBase kiểm tra cấu hình cơ bản.
func (c *Config) ValidateBase() error {
	if err := validateConfigText("provider", c.Provider); err != nil {
		return err
	}
	if err := validateConfigText("model", c.ModelName); err != nil {
		return err
	}

	if c.Provider == "" {
		return fmt.Errorf("provider is required: %w", errs.ErrConfig)
	}
	if c.ModelName == "" {
		return fmt.Errorf("model is required: %w", errs.ErrConfig)
	}

	// provider mặc định phải có chứng chỉ
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q chưa cấu hình chứng thực trong providers; nếu đã ghi đè provider trong ./.ainovel/config.json, phải đồng thời khai báo providers.%s (gồm api_key/base_url), không thể chỉ sửa provider tầng trên: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q has no api_key configured: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	if err := c.validateProviderAPI("default", c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
		if err := c.validateProviderAPI(fmt.Sprintf("provider %q", name), name, provider); err != nil {
			return err
		}
	}

	// Kiểm tra ghi đè vai trò
	for role, rc := range c.Roles {
		if err := validateConfigText("role name", role); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q provider", role), rc.Provider); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q model", role), rc.Model); err != nil {
			return err
		}
		if !knownRoles[role] {
			return fmt.Errorf("unknown role %q in roles config (valid: architect/writer/editor/import_segment/import_analyze/import_synthesize): %w", role, errs.ErrConfig)
		}
		if rc.Provider == "" || rc.Model == "" {
			return fmt.Errorf("role %q must have both provider and model: %w", role, errs.ErrConfig)
		}
		if err := c.validateModelRef(
			fmt.Sprintf("role %q", role),
			ModelRef{Provider: rc.Provider, Model: rc.Model},
		); err != nil {
			return err
		}
		for i, fallback := range rc.Fallbacks {
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] provider", role, i), fallback.Provider); err != nil {
				return err
			}
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] model", role, i), fallback.Model); err != nil {
				return err
			}
			if err := c.validateModelRef(
				fmt.Sprintf("role %q fallback[%d]", role, i),
				fallback,
			); err != nil {
				return err
			}
		}
	}

	// Kiểm tra chính sách ngân sách
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd must be >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio must be in (0, 1): %w", errs.ErrConfig)
	}

	// Kiểm tra cấu hình cảnh báo
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !notify.IsKnownKind(ev) {
			return fmt.Errorf("unknown notify event %q (valid: %s): %w", ev, strings.Join(notify.Kinds(), "/"), errs.ErrConfig)
		}
	}

	return nil
}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("provider %q type", name), value: pc.Type},
		{label: fmt.Sprintf("provider %q api", name), value: pc.API},
		{label: fmt.Sprintf("provider %q api_key", name), value: pc.APIKey},
		{label: fmt.Sprintf("provider %q base_url", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	seenModels := make(map[string]bool, len(pc.Models))
	for i, model := range pc.Models {
		modelName := strings.TrimSpace(model.Name)
		if err := validateConfigText(fmt.Sprintf("provider %q models[%d].name", name, i), model.Name); err != nil {
			return err
		}
		if modelName == "" {
			return fmt.Errorf("provider %q models[%d].name is required: %w", name, i, errs.ErrConfig)
		}
		if seenModels[modelName] {
			return fmt.Errorf("provider %q has duplicate model %q: %w", name, modelName, errs.ErrConfig)
		}
		seenModels[modelName] = true
		if model.ContextWindow < 0 {
			return fmt.Errorf("provider %q model %q context_window must be >= 0: %w", name, modelName, errs.ErrConfig)
		}
	}
	switch pc.API {
	case "", "chat", "responses":
	default:
		return fmt.Errorf("provider %q api must be chat or responses: %w", name, errs.ErrConfig)
	}
	if _, err := pc.StreamIdleTimeoutValue(); err != nil {
		return fmt.Errorf("provider %q stream_idle_timeout: %w: %w", name, err, errs.ErrConfig)
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s contains control character: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig Trả về cấu hình chứng chỉ của provider mặc định.
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults Điền giá trị mặc định.
func (c *Config) FillDefaults() {
	if c.OutputDir == "" {
		c.OutputDir = filepath.Join("output", "novel")
	}
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	if c.Roles == nil {
		c.Roles = make(map[string]RoleConfig)
	}
	if c.Style == "" {
		c.Style = "default"
	}
	if c.Budget.Enabled() && c.Budget.WarnRatio == 0 {
		c.Budget.WarnRatio = 0.8
	}
}

// ContextWindowSource Đánh dấu nguồn gốc lấy giá trị cửa sổ, dùng cho log/chẩn đoán.
type ContextWindowSource string

const (
	CtxWindowModelConfig ContextWindowSource = "model_config" // chỉ định rõ ràng ở mục model của provider
	CtxWindowConfig      ContextWindowSource = "config"       // chỉ định rõ ràng ở context_window cấp cao nhất cũ
	CtxWindowRegistry    ContextWindowSource = "registry"     // hit đường cơ sở OpenRouter
	CtxWindowDefault     ContextWindowSource = "default"      // Dự phòng (proxy tùy chỉnh/model không nhận diện được)
)

// ResolveContextWindow Phân giải cửa sổ hữu hiệu dùng để nén ngữ cảnh, theo thứ tự ưu tiên:
//  1. providers.<provider>.models[].context_window
//  2. ContextWindow cấp cao nhất cũ (tương thích cấu hình có sẵn)
//  3. models.DefaultRegistry tra cứu theo tên model (cơ sở OpenRouter + làm mới 24h)
//  4. Dự phòng DefaultContextWindow (proxy tùy chỉnh / model không nhận diện được)
//
// Lưu ý: giá trị trả về chỉ dùng để tính ngưỡng nén, không thu hẹp độ dài yêu cầu thực sự gửi đến LLM API.
func (c Config) ResolveContextWindow(provider, modelName string) (int, ContextWindowSource) {
	if pc, ok := c.Providers[strings.TrimSpace(provider)]; ok {
		if model, found := pc.ModelConfig(modelName); found && model.ContextWindow > 0 {
			return model.ContextWindow, CtxWindowModelConfig
		}
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveReasoningEffort Trả về chuỗi gốc của cường độ suy luận có hiệu lực cho vai trò nào đó (off/low/medium/high/xhigh/max hoặc rỗng).
// Ưu tiên: Roles[role].ReasoningEffort cấp vai trò → ReasoningEffort mặc định cấp trên cùng → "" (không ghi đè, dùng mặc định của model/provider).
// Khi role rỗng hoặc "default" thì lấy mặc định cấp trên cùng. Tính hợp lệ của giá trị do agents.ParseThinkingLevel kiểm tra.
func (c Config) ResolveReasoningEffort(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.ReasoningEffort != "" {
			return rc.ReasoningEffort
		}
	}
	return c.ReasoningEffort
}

// LogContextWindowChoice In quyết định cửa sổ của vai trò nào đó. Khi source=default thì phát Warn cảnh báo
// model này chưa hit trong registry (OpenRouter cũng chưa thu thập), sau này nén ngữ cảnh sẽ theo cửa sổ dự phòng
// ——nếu cửa sổ thực tế của model lớn hơn, có thể chỉ định rõ qua context_window trong file cấu hình để tránh nén sớm, mất lịch sử.
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowModelConfig:
		slog.Info("cửa sổ ngữ cảnh (từ cấu hình model của provider)", attrs...)
	case CtxWindowDefault:
		slog.Warn("model không nhận dạng được, dùng cửa sổ dự phòng (có thể chỉ định rõ trong providers.<name>.models[].context_window)", attrs...)
	case CtxWindowConfig:
		slog.Info("cửa sổ ngữ cảnh (từ context_window trong file cấu hình)", attrs...)
	default:
		slog.Info("cửa sổ ngữ cảnh", attrs...)
	}
}

// CandidateModels Trả về danh sách model có thể chuyển đổi dưới một provider nào đó.
// Ưu tiên dùng models do provider khai báo rõ ràng; đồng thời bổ sung các model provider đó đã xuất hiện trong cấu hình hiện tại.
func (c Config) CandidateModels(provider string) []string {
	if provider == "" {
		return nil
	}

	seen := make(map[string]bool)
	models := make([]string, 0, 4)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		models = append(models, model)
	}

	if pc, ok := c.Providers[provider]; ok {
		for _, model := range pc.Models {
			add(model.Name)
		}
	}
	if c.Provider == provider {
		add(c.ModelName)
	}
	for _, rc := range c.Roles {
		if rc.Provider == provider {
			add(rc.Model)
		}
		for _, fallback := range rc.Fallbacks {
			if fallback.Provider == provider {
				add(fallback.Model)
			}
		}
	}
	return models
}

func (c Config) validateModelRef(owner string, ref ModelRef) error {
	if ref.Provider == "" || ref.Model == "" {
		return fmt.Errorf("%s must have both provider and model: %w", owner, errs.ErrConfig)
	}

	pc, ok := c.Providers[ref.Provider]
	if !ok {
		return fmt.Errorf("%s references provider %q which is not configured: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(ref.Provider) && pc.APIKey == "" {
		return fmt.Errorf("%s references provider %q which has no api_key: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if err := c.validateProviderAPI(owner, ref.Provider, pc); err != nil {
		return err
	}
	return nil
}

func (c Config) validateProviderAPI(owner, providerName string, pc ProviderConfig) error {
	if pc.API == "" {
		return nil
	}
	providerType, err := pc.ProviderType(providerName)
	if err != nil {
		return fmt.Errorf("%s provider %q không phân tích được kiểu giao thức từ cấu hình api: %w", owner, providerName, err)
	}
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return fmt.Errorf("%s api của provider %q chỉ hỗ trợ provider kiểu giao thức OpenAI: %w", owner, providerName, errs.ErrConfig)
	}
	return nil
}
