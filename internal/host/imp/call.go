package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/llmretry"
	"github.com/voocel/litellm"
)

// callModel là sự phụ thuộc tối thiểu của lõi vào model, thuận tiện cho việc tiêm mock để test.
type callModel interface {
	Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error)
}

// errTruncated chỉ ra rằng model dừng do độ dài (lỗi dung lượng). Mang theo văn bản gốc để bên gọi quyết định thất bại hay trục vớt tiền tố (§9.5).
type errTruncated struct {
	Raw string
}

func (e *errTruncated) Error() string { return "Đầu ra model bị cắt ngắn theo độ dài (stop=length)" }

// errSemantic chỉ ra lỗi tầng đầu ra không thể sửa bằng cách hỏi lại, mang theo phản hồi gốc,
// để runner thống nhất đưa vào công cụ thất bại failures/ (§14.2), tất cả các hàm ngữ nghĩa đều dùng chung.
type errSemantic struct {
	Raw string
	Err error
}

func (e *errSemantic) Error() string { return e.Err.Error() }
func (e *errSemantic) Unwrap() error { return e.Err }

// callProfile chứa các tùy chọn thinking và khả năng quan sát, dẫn xuất từ ModelRuntime được Host thăm dò.
// Giao thức có cấu trúc được callStructured lựa chọn độc lập dựa trên sự thật model và Contract tĩnh.
type callProfile struct {
	thinking agentcore.ThinkingLevel
	// notify tùy chọn: phản hồi lùi lại yêu cầu thử lại/hỏi lại kiểm tra lên giao diện; khi nil là im lặng (§14.1).
	// retryAt khác 0 = thời gian hết hạn cho lần thử lại tiếp theo, UI dựa vào đây để render đếm ngược từng giây (sự kiện chỉ mang điểm hết hạn, thời gian còn lại sẽ được tính khi render).
	notify func(msg string, retryAt time.Time)
	// progress tùy chọn: phản hồi tiến độ bên trong của giai đoạn dài (cắt phân lô thứ N/M, tóm tắt khoảng N/M); khi nil là im lặng.
	// Cắt phân/tổng hợp gọi model theo từng lô/từng khoảng trong hàm, một lô có thể kéo dài vài phút, không có nó thì bảng điều khiển im lặng như treo máy (§14.1).
	progress func(current, total int, msg string)
	// log tùy chọn: Log dành riêng cho import (logs/import.log); nil lùi về logger mặc định.
	log *slog.Logger
}

func (p callProfile) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

// step phản hồi một tiến độ thông thường (tiến độ bên trong của giai đoạn dài).
func (p callProfile) step(current, total int, format string, args ...any) {
	if p.progress != nil {
		p.progress(current, total, fmt.Sprintf(format, args...))
	}
}

// say phản hồi một trạng thái gọi trong thời gian dài. Thử lại có thể im lặng trong vài phút (lùi lại theo số mũ tích lũy trên 2 phút),
// không phản hồi sẽ làm người dùng tưởng nhầm là treo máy.
func (p callProfile) say(format string, args ...any) {
	p.sayRetry(time.Time{}, format, args...)
}

// sayRetry phản hồi một trạng thái có mang thời gian hết hạn thử lại, để UI đếm ngược.
func (p callProfile) sayRetry(retryAt time.Time, format string, args ...any) {
	if p.notify != nil {
		p.notify(fmt.Sprintf(format, args...), retryAt)
	}
}

// snippet nén văn bản nhiều dòng thành tóm tắt ngắn một dòng để giao diện phản hồi: hợp nhất khoảng trắng, cắt đến max rune.
func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// briefErr nén lỗi thành văn bản ngắn một dòng để giao diện phản hồi (chuỗi lỗi đầy đủ vẫn đi qua nhật ký và công cụ thất bại).
// Sự thật có cấu trúc của adapter đặt lên trước: khi cắt ngắn ưu tiên giữ "loại lỗi nào, mã trạng thái là gì", message của gateway có thể hi sinh.
func briefErr(err error) string {
	s := err.Error()
	if d := modelErrDetail(err); d != "" {
		s = d + "：" + s
	}
	return snippet(s, 100)
}

// errTypeLabels dịch phân loại lỗi của litellm thành thẻ ngắn tiếng Trung dễ đọc.
var errTypeLabels = map[litellm.ErrorType]string{
	litellm.ErrorTypeAuth:            "Xác thực thất bại",
	litellm.ErrorTypeRateLimit:       "Giới hạn luồng",
	litellm.ErrorTypeNetwork:         "Lỗi mạng",
	litellm.ErrorTypeValidation:      "Tham số yêu cầu không hợp lệ",
	litellm.ErrorTypeProvider:        "Lỗi dịch vụ thượng nguồn",
	litellm.ErrorTypeTimeout:         "Quá thời gian",
	litellm.ErrorTypeQuota:           "Không đủ hạn ngạch",
	litellm.ErrorTypeModel:           "Model không khả dụng",
	litellm.ErrorTypeInternal:        "Lỗi nội bộ",
	litellm.ErrorTypeContextOverflow: "Ngữ cảnh vượt quá giới hạn",
	litellm.ErrorTypeOverloaded:      "Thượng nguồn quá tải",
	litellm.ErrorTypeContentFilter:   "Bị lọc nội dung chặn",
}

// modelErrDetail trích xuất sự thật cấu trúc của adapter từ chuỗi lỗi (phân loại lỗi, trạng thái HTTP, provider, model).
// message của gateway thường chỉ có một câu chung chung "Provider returned error", chỉ dựa vào đó không thể phán đoán là sai cấu hình,
// sự cố thượng nguồn hay giới hạn luồng; những sự thật này litellm luôn mang theo, chỉ là không vào câu văn Error(). adapter của agentcore
// Unwrap cho phép bên gọi biết litellm dùng errors.As lấy lỗi gốc một cách rõ ràng. Lỗi gọi phi model trả về chuỗi rỗng.
func modelErrDetail(err error) string {
	var le *litellm.LiteLLMError
	if !errors.As(err, &le) {
		return ""
	}
	parts := make([]string, 0, 4)
	if label := errTypeLabels[le.Type]; label != "" {
		parts = append(parts, label)
	}
	if le.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", le.StatusCode))
	}
	if le.Provider != "" {
		parts = append(parts, le.Provider)
	}
	if le.Model != "" {
		parts = append(parts, le.Model)
	}
	return strings.Join(parts, ", ")
}

// callOptions lắp ráp CallOption của lần gọi này: luôn có giới hạn đầu ra; thinking có thể chọn theo năng lực.
// thinking chỉ gửi khi không Auto——việc gửi bất kỳ cấp độ nào (kể cả off) cho model không hỗ trợ thinking đều là tham số không hợp lệ (cùng chiến lược với arbiter).
func (p callProfile) callOptions(maxTokens int) []agentcore.CallOption {
	opts := []agentcore.CallOption{agentcore.WithMaxTokens(maxTokens)}
	if p.thinking != agentcore.ThinkingAuto {
		opts = append(opts, agentcore.WithThinking(p.thinking))
	}
	return opts
}

// callStructured là bộ thực thi cấu trúc thống nhất được điều chỉnh cho tầng import, và ánh xạ thất bại chung thành ngữ nghĩa công kiện import.
func callStructured[T any](ctx context.Context, m callModel, contract llmcontract.Contract, systemPrompt, payload string, maxTokens int, prof callProfile, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, m, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      prof.callOptions(maxTokens),
		Validate:     validate,
		Agent:        "import",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				prof.logger().Debug("Lựa chọn giao thức có cấu trúc imp",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			RequestRetry: func(ev llmretry.Event) {
				prof.sayRetry(time.Now().Add(ev.Delay), "Yêu cầu model thất bại (%s), tiến hành thử lại lần thứ %d", briefErr(ev.Err), ev.Attempt)
				prof.logger().Warn("Thử lại yêu cầu model imp", "attempt", ev.Attempt, "delay", ev.Delay, "err", ev.Err)
			},
			Correction: func(ev llmcontract.Correction) {
				prof.say("Xác minh đầu ra không qua (%s), mang phản hồi lỗi để hỏi lại lần thứ %d", briefErr(ev.Err), ev.Attempt+1)
				prof.logger().Warn("Đầu ra có cấu trúc imp tự chữa lành", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var failure *llmcontract.Failure
	if !errors.As(err, &failure) {
		return out, fmt.Errorf("imp: %w", err)
	}
	switch failure.Kind {
	case llmcontract.FailureLength:
		return out, &errTruncated{Raw: failure.Raw}
	case llmcontract.FailureSafety, llmcontract.FailureContract, llmcontract.FailureProtocol:
		if failure.Raw != "" {
			return out, &errSemantic{Raw: failure.Raw, Err: fmt.Errorf("imp: %w", failure)}
		}
	case llmcontract.FailureRequest:
		if detail := modelErrDetail(failure); detail != "" {
			return out, fmt.Errorf("imp: Gọi model thất bại (%s): %w", detail, failure)
		}
	}
	return out, fmt.Errorf("imp: %w", failure)
}
