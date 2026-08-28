// Package userrules là lớp dịch vụ chuẩn hóa quy tắc người dùng: đưa quy tắc ngôn ngữ tự nhiên từ các nguồn qua LLM có cấu trúc
// chuẩn hóa thành các trường cấu trúc ứng viên, sau đó do rules.BuildSnapshot gộp xác định thành điểm khôi phục của sách.
//
// Trách nhiệm các lớp:
//   - gói rules: dữ liệu thuần + gộp xác định (Snapshot / Candidate / BuildSnapshot / SystemDefaults)
//   - gói này: LLM chuẩn hóa + biên đạo + ghi đĩa (phụ thuộc agentcore + store + rules)
//
// Chuẩn hóa là đường dẫn tăng cường, không phải là điều kiện tiên quyết của sáng tác chính: bất kỳ nguồn nào thất bại đều hạ cấp thành raw preferences, sáng tác chính vẫn phải tiếp tục.
package userrules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// normalizeMaxTokens giới hạn đầu ra của một lần chuẩn hóa (token suy nghĩ và đầu ra JSON chia sẻ ngân sách này).
// JSON chuẩn hóa vốn rất nhỏ (thường <1k), để phần lớn ở đây là ngân sách suy nghĩ cho "model lý luận không tắt được suy nghĩ" —
// Nếu để hẹp quá, suy nghĩ sẽ chèn ép JSON dẫn đến bị cắt cụt, phân tích thất bại. max_tokens là giới hạn trên chứ không phải lượng tính phí, tăng lên không làm tăng chi phí.
const normalizeMaxTokens = 8192

// normalizeContract sát biên giới DTO: tất cả trường required, fatigue_words dùng mảng đối tượng
// (chế độ strict cấm map có key động), cả hai chế độ dùng chung một quy ước DTO.
var normalizeContract = llmcontract.Contract{
	Name:        "userrules_normalize",
	Description: "Chuẩn hóa quy tắc viết bằng ngôn ngữ tự nhiên của người dùng thành trường có cấu trúc",
	Schema: schema.Object(
		schema.Property("structured", schema.Object(
			schema.Property("genre", schema.String("thể loại; không có thì chuỗi rỗng")).Required(),
			schema.Property("forbidden_chars", schema.Array("ký tự cấm xuất hiện", schema.String("ký tự"))).Required(),
			schema.Property("forbidden_phrases", schema.Array("cụm từ cấm xuất hiện (khớp chính xác theo nghĩa đen)", schema.String("cụm từ"))).Required(),
			schema.Property("fatigue_words", schema.Array("từ mệt mỏi và trần xuất hiện mỗi chương", schema.Object(
				schema.Property("word", schema.String("từ mệt mỏi")).Required(),
				schema.Property("max_per_chapter", schema.Int("trần số lần xuất hiện mỗi chương (số nguyên dương)")).Required(),
			))).Required(),
		)).Required(),
		schema.Property("preferences", schema.String("sở thích văn phong/nhân vật/thẩm mỹ bằng ngôn ngữ tự nhiên; không có thì chuỗi rỗng")).Required(),
		schema.Property("uncertain", schema.Array("mục cố tình không nâng lên structured + lý do", schema.String("mục"))).Required(),
	),
}

// Normalizer chuẩn hóa quy tắc ngôn ngữ tự nhiên của một nguồn thành rules.Candidate.
type Normalizer struct {
	model agentcore.ChatModel
}

// NewNormalizer cấu trúc bộ chuẩn hóa bằng một ChatModel. Chuẩn hóa là công cụ khởi động một lần,
// nên truyền vào model có năng lực mạnh (ví dụ model mặc định của ModelSet), không cần theo model yếu của việc sáng tác.
//
// Chuẩn hóa không ghi đè thinking: off rõ ràng vốn cũng chỉ là tham số suy luận được một số model hỗ trợ,
// model chat thông thường sẽ từ chối nó. Dùng mặc định của provider/model, do normalizeMaxTokens
// để lại ngân sách đầu ra cho các model không thể tắt suy nghĩ.
func NewNormalizer(model agentcore.ChatModel) *Normalizer {
	return &Normalizer{model: model}
}

// Normalize chuẩn hóa một nguồn. Thất bại trả về error (gồm lý do thực sự), do phía gọi quyết định hạ cấp
// (Service.normalizeOrDegrade rớt xuống ứng viên degraded) —— lỗi kỹ thuật không còn ngụy trang thành kết quả bình thường,
// lỗi chấm dứt (xác thực/quyền hạn v.v.) không thử lại.
func (n *Normalizer) Normalize(ctx context.Context, source, text string) (rules.Candidate, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return rules.Candidate{Source: source}, nil
	}
	if n == nil || n.model == nil {
		return rules.Candidate{}, fmt.Errorf("model chuẩn hóa chưa cấu hình")
	}

	out, err := llmcontract.Execute(ctx, n.model, llmcontract.Request[normalizerOutput]{
		Contract:     normalizeContract,
		SystemPrompt: normalizerSystemPrompt,
		Payload:      text,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(normalizeMaxTokens)},
		Validate: func(out *normalizerOutput) error {
			_, err := out.toCandidate(source)
			return err
		},
		Agent: "rules",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("chọn giao thức chuẩn hóa quy tắc", "module", "rules", "source", source,
					"contract", normalizeContract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider, "model", res.Model,
					"schema_fingerprint", normalizeContract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("tự sửa đầu ra chuẩn hóa quy tắc", "module", "rules", "source", source,
					"attempt", ev.Attempt, "layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return rules.Candidate{}, fmt.Errorf("chuẩn hóa thất bại: %w", err)
	}
	return out.toCandidate(source)
}

// degraded cấu trúc một ứng viên hạ cấp: khi chuẩn hóa thất bại thì coi nguyên văn như sở thích văn phong, không tinh chế bất kỳ quy tắc máy móc nào.
// uncertain đánh dấu nguồn (tiện hiển thị lại "nguồn nào chưa phân tích được"), nhưng không kèm chi tiết lỗi kỹ thuật — lỗi kỹ thuật chỉ vào log.
func degraded(source, text string) rules.Candidate {
	return rules.Candidate{
		Source:      source,
		Preferences: text,
		Uncertain:   []string{source + ": chuẩn hóa thất bại, đã xử lý theo nguyên văn như sở thích văn phong (chưa chưng cất quy tắc cơ khí)"},
		Degraded:    true,
	}
}

// normalizerOutput là DTO ranh giới do bộ chuẩn hóa quy ước (dùng chung cho hai chế độ): uncertain cố định
// là mảng chuỗi, fatigue_words cố định là mảng đối tượng —— hình thái bị đóng đinh bởi hợp đồng, không phỏng đoán nhiều hình thái nữa.
type normalizerOutput struct {
	Structured  normalizerStructured `json:"structured"`
	Preferences string               `json:"preferences"`
	Uncertain   []string             `json:"uncertain"`
}

type normalizerStructured struct {
	Genre            string             `json:"genre"`
	ForbiddenChars   []string           `json:"forbidden_chars"`
	ForbiddenPhrases []string           `json:"forbidden_phrases"`
	FatigueWords     []fatigueWordEntry `json:"fatigue_words"`
}

type fatigueWordEntry struct {
	Word          string `json:"word"`
	MaxPerChapter int    `json:"max_per_chapter"`
}

// toCandidate kiểm tra DTO ranh giới và chuyển thành ứng viên lĩnh vực: mục fatigue phải có từ không rỗng, giới hạn trên là số nguyên dương
// (lỗi kiểm tra có thể phản hồi cho model sửa), phía lĩnh vực vẫn là map[string]int.
func (o normalizerOutput) toCandidate(source string) (rules.Candidate, error) {
	var fatigue map[string]int
	for _, e := range o.Structured.FatigueWords {
		word := strings.TrimSpace(e.Word)
		if word == "" {
			return rules.Candidate{}, fmt.Errorf("fatigue_words chứa mục từ rỗng")
		}
		if e.MaxPerChapter < 1 {
			return rules.Candidate{}, fmt.Errorf("fatigue_words[%q].max_per_chapter phải là số nguyên dương, got %d", word, e.MaxPerChapter)
		}
		if fatigue == nil {
			fatigue = make(map[string]int, len(o.Structured.FatigueWords))
		}
		fatigue[word] = e.MaxPerChapter
	}
	return rules.Candidate{
		Source: source,
		Structured: rules.Structured{
			Genre:            strings.TrimSpace(o.Structured.Genre),
			ForbiddenChars:   nonEmpty(o.Structured.ForbiddenChars),
			ForbiddenPhrases: nonEmpty(o.Structured.ForbiddenPhrases),
			FatigueWords:     fatigue,
		},
		Preferences: strings.TrimSpace(o.Preferences),
		Uncertain:   nonEmpty(o.Uncertain),
	}, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizerSystemPrompt chỉ mô tả ngữ nghĩa chuẩn hóa; cấu trúc đầu ra do normalizeContract duy trì tập trung.
// Đã xác minh với 10 ví dụ thực (bao gồm bẫy tự phát minh ngưỡng) — bảo thủ nâng cấp đạt 10/10.
const normalizerSystemPrompt = `Bạn là «bộ chuẩn hóa quy tắc» của hệ thống viết tiểu thuyết AI. Bạn đọc các quy tắc viết dài hạn của người dùng từ một nguồn (ngôn ngữ tự nhiên), nâng những quy tắc rõ ràng và có thể kiểm tra máy móc lên structured, phần còn lại đưa vào preferences hoặc uncertain.

【Nâng cấp bảo thủ — QUAN TRỌNG NHẤT】
- Chỉ ghi vào structured khi người dùng nói rõ ràng, không mơ hồ.
- forbidden_chars/forbidden_phrases ở mức error: chỉ nâng cấp khi có lệnh cấm rõ ràng kiểu "không được xuất hiện X / cấm dùng X / đừng viết X".
- fatigue_words: chỉ nâng cấp khi đồng thời có «từ cụ thể» VÀ «ngưỡng số lần cụ thể»; "hạn chế dùng X / đừng lạm dụng X" mà không cho con số thì đưa vào preferences, tuyệt đối không tự phát minh ngưỡng.
- Ý muốn về số từ/độ dài (ví dụ "mỗi chương 3000 từ", "viết ngắn hơn") luôn đưa vào preferences: độ dài chương là vấn đề nhịp tường thuật, do sáng tác tự nhiên nắm bắt, không kiểm tra máy móc.
- Những gì không thể kiểm tra máy móc, không có ngưỡng rõ ràng, hoặc phụ thuộc ngữ cảnh, luôn đưa vào preferences.
- Nguyên tắc: thà bỏ sót khỏi structured còn hơn nâng cấp sai (vì nâng sai sẽ báo lỗi mỗi chương).

preferences dùng một đoạn ngôn ngữ tự nhiên dễ đọc để lưu giữ phong cách, nhân vật và sở thích thẩm mỹ.
uncertain giải thích những mục bạn cố ý không nâng lên structured và lý do.`