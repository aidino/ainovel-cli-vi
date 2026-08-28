// Package llmcontract là hợp đồng thống nhất và lớp thực thi cho trả về có cấu trúc trực tiếp: Contract tĩnh
// là nguồn chân lý duy nhất cho cấu trúc, Execute hoàn thành việc chọn khả năng, chuẩn bị prompt, thử lại yêu cầu,
// giải mã Schema/DTO và tự phục hồi qua phản hồi.
// Giao thức được xác định trước khi yêu cầu được gửi; khi yêu cầu gốc bị từ chối hoặc vi phạm hợp đồng, nó được phơi bày nguyên trạng, cấm việc âm thầm bỏ schema để gửi lại.
package llmcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// Contract là hợp đồng tĩnh của một lần trả về có cấu trúc trực tiếp, nằm sát với các định nghĩa DTO ở ranh giới.
type Contract struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Mode là giao thức có cấu trúc được sử dụng trong lần gọi này.
type Mode string

const (
	ModeNativeJSONSchema Mode = "native_json_schema"
	ModePromptContract   Mode = "prompt_contract"
)

// Source là nguồn căn cứ để phán đoán khả năng.
type Source string

const (
	SourceConfig  Source = "config"  // Người dùng khai báo rõ ràng trong ModelConfig.json_schema
	SourceAdapter Source = "adapter" // Bảng khả năng cấp model của provider adapter
	SourceUnknown Source = "unknown" // Không khai báo và chưa rõ khả năng, bảo thủ dùng prompt contract
)

// Resolution là kết quả lựa chọn giao thức xác định trước khi gửi yêu cầu, dùng cho rẽ nhánh và log ở phía gọi.
type Resolution struct {
	Mode     Mode
	Source   Source
	Strict   bool // Khi ở native có mang theo strict hay không
	Provider string
	Model    string
}

// jsonSchemaOverrider được thực hiện bởi model wrapper có mang theo ghi đè config 3 trạng thái
// (bootstrap.SwappableModel và lớp wrapper truyền nó đi).
type jsonSchemaOverrider interface {
	JSONSchemaOverride() *bool
}

type modelInfoProvider interface {
	Info() llm.ModelInfo
}

// ModelFacts là bản chụp nhanh (snapshot) tại cùng một thời điểm cần thiết để phân tích khả năng. Wrapper đổi nóng thực thi interface này,
// tránh việc Resolve đọc khả năng, cấu hình ghi đè và danh tính model một cách riêng lẻ rồi bị lẫn lộn trạng thái giữa hai lần chuyển đổi.
type ModelFacts struct {
	Capabilities       llm.Capabilities
	Info               llm.ModelInfo
	JSONSchemaOverride *bool
}

type modelFactsProvider interface {
	StructuredOutputFacts() ModelFacts
}

// Resolve luôn đọc sự thật hiện tại của model mỗi lần gọi (sau khi đổi nóng, lần gọi tiếp theo dùng giá trị mới ngay):
// Cấu hình 3 trạng thái ưu tiên cao nhất, sau đó là bảng khả năng cấp model của adapter, không rõ thì mặc định prompt contract.
func Resolve(model any) Resolution {
	res := Resolution{Mode: ModePromptContract, Source: SourceUnknown}

	var caps llm.Capabilities
	var info llm.ModelInfo
	var override *bool
	if fp, ok := model.(modelFactsProvider); ok {
		facts := fp.StructuredOutputFacts()
		caps, info, override = facts.Capabilities, facts.Info, facts.JSONSchemaOverride
	} else {
		if cp, ok := model.(llm.CapabilityProvider); ok {
			caps = cp.Capabilities()
		}
		if ip, ok := model.(modelInfoProvider); ok {
			info = ip.Info()
		}
		if o, ok := model.(jsonSchemaOverrider); ok {
			override = o.JSONSchemaOverride()
		}
	}
	res.Provider, res.Model = caps.Provider, caps.Model
	if res.Provider == "" {
		res.Provider = info.Provider
	}
	if res.Model == "" {
		res.Model = info.Name
	}

	if override != nil {
		res.Source = SourceConfig
		if *override {
			res.Mode = ModeNativeJSONSchema
			// Người dùng khai báo endpoint tuân thủ hợp đồng Structured Outputs tức là ngầm định strict;
			// Chỉ khi adapter nói rõ không hỗ trợ strict thì mới gửi schema mà không kèm strict.
			res.Strict = caps.Structured.Strict != llm.SupportNo
		}
		return res
	}

	switch caps.Structured.JSONSchema {
	case llm.SupportYes:
		res.Mode = ModeNativeJSONSchema
		res.Source = SourceAdapter
		res.Strict = caps.Structured.Strict == llm.SupportYes
	case llm.SupportNo:
		res.Source = SourceAdapter
	}
	return res
}

// Plan phân tích giao thức và sinh các tùy chọn gọi nếu ở chế độ native; chế độ prompt contract trả về opts nil.
func Plan(model any, c Contract) ([]agentcore.CallOption, Resolution) {
	res := Resolve(model)
	if res.Mode != ModeNativeJSONSchema {
		return nil, res
	}
	return []agentcore.CallOption{
		agentcore.WithJSONSchema(c.Name, c.Description, c.Schema, res.Strict),
	}, res
}

// PreparePrompt giữ prompt ngữ nghĩa nghiệp vụ chỉ duy nhất một bản: chế độ native trả về bản gốc trực tiếp; chế độ prompt
// contract tự động sinh hậu tố định dạng từ cùng một bản Schema. Bên gọi không cần duy trì template thứ hai, thay đổi trường
// cũng không làm cho prompt và response_format bị lệch nhau.
func PreparePrompt(base string, c Contract, res Resolution) (string, error) {
	if res.Mode != ModePromptContract {
		return base, nil
	}
	schemaJSON, err := json.Marshal(c.Schema)
	if err != nil {
		return "", fmt.Errorf("llmcontract: marshal %s prompt schema: %w", c.Name, err)
	}
	contract := "## Hợp đồng đầu ra\n\n" +
		"Chỉ xuất ra một đối tượng JSON đúng với JSON Schema dưới đây, không xuất giải thích, khối Markdown hay chính nhãn.\n\n" +
		"<output-json-schema>\n" + string(schemaJSON) + "\n</output-json-schema>"
	if strings.TrimSpace(base) == "" {
		return contract, nil
	}
	return strings.TrimSpace(base) + "\n\n" + contract, nil
}

// Nullable mở rộng type của schema thành union cho phép null (["<t>","null"]), dùng cho chế độ strict
// để biểu đạt "mọi trường required, ngữ nghĩa tùy chọn dùng null". Trả về bản sao, không sửa map truyền vào.
func Nullable(s map[string]any) map[string]any {
	out := maps.Clone(s)
	if t, ok := out["type"].(string); ok {
		out["type"] = []string{t, "null"}
	}
	switch values := out["enum"].(type) {
	case []string:
		enum := make([]any, 0, len(values)+1)
		for _, value := range values {
			enum = append(enum, value)
		}
		out["enum"] = append(enum, nil)
	case []any:
		enum := slices.Clone(values)
		for _, value := range enum {
			if value == nil {
				return out
			}
		}
		out["enum"] = append(enum, nil)
	}
	return out
}

// ValidateStrictReady đệ quy kiểm tra schema thỏa mãn điều kiện cấu trúc tập con strict của OpenAI:
// mọi thuộc tính của object đều phải được liệt kê trong required (ngữ nghĩa tùy chọn dùng null union để biểu đạt). litellm
// cũng kiểm tra tương tự ở giai đoạn gửi yêu cầu và tự động bổ sung additionalProperties:false; hàm này
// được dùng trong test hợp đồng để khẳng định sớm (RFC §11.1), không để các vấn đề cấu trúc nảy sinh lúc runtime.
func ValidateStrictReady(s map[string]any) error {
	return validateStrictReady(s, "$")
}

func validateStrictReady(s map[string]any, path string) error {
	if typeIncludes(s["type"], "object") {
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]string)
		for name, sub := range props {
			if !slices.Contains(required, name) {
				return fmt.Errorf("%s.%s không nằm trong required (strict yêu cầu mọi thuộc tính phải required)", path, name)
			}
			if subMap, ok := sub.(map[string]any); ok {
				if err := validateStrictReady(subMap, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		return validateStrictReady(items, path+"[]")
	}
	return nil
}

func typeIncludes(t any, want string) bool {
	switch v := t.(type) {
	case string:
		return v == want
	case []string:
		return slices.Contains(v, want)
	}
	return false
}

// Fingerprint trả về 12 ký tự hex đầu tiên của SHA256 cho JSON chuẩn hóa của schema, dùng liên kết log;
// encoding/json đã sắp xếp các khóa map, nên cùng hợp đồng sẽ tự nhiên ổn định.
func (c Contract) Fingerprint() string {
	data, err := json.Marshal(c.Schema)
	if err != nil {
		return "unmarshalable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}