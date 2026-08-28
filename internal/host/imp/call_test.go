package imp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/litellm"
)

// flakyModel 前 fails 次trả về 可thử lại lỗi ，之后按 mockModel 响应。
type flakyModel struct {
	mockModel
	fails int
}

func (f *flakyModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if f.fails > 0 {
		f.fails--
		return nil, fastRetryErr{}
	}
	return f.mockModel.Generate(ctx, msgs, tools, opts...)
}

// fastRetryErr 可thử lại và退避极短（RetryAfter 命中 RetryHinter），保证kiểm tra 快速。
type fastRetryErr struct{}

func (fastRetryErr) Error() string             { return "rate limited" }
func (fastRetryErr) Retryable() bool           { return true }
func (fastRetryErr) RetryAfter() time.Duration { return time.Millisecond }

// TestCallStructuredNotifiesRetries 守护thử lại 可见性：请求退避与校验重问都phải 回显，
// 否则指数退避可静默数分钟，người dùng 会误以为nhập 卡死（截图问题：3 分钟无声后才báo lỗi）。
// 请求退避还phải 携带非零 retryAt 截止时刻——UI 倒计时依赖它；校验重问即时xảy ra ，retryAt 为零。
func TestCallStructuredNotifiesRetries(t *testing.T) {
	m := &flakyModel{mockModel: mockModel{responses: []string{"不是 JSON", `{"boundaries":[]}`}}, fails: 2}
	var notes []string
	var retries, reasks int
	prof := callProfile{notify: func(s string, retryAt time.Time) {
		notes = append(notes, s)
		if !retryAt.IsZero() {
			retries++
		}
		if strings.Contains(s, "hỏi lại") {
			reasks++
		}
	}}
	if _, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "p", 100, prof, nil); err != nil {
		t.Fatalf("最终应thành công：%v", err)
	}
	if retries != 2 || reasks != 1 {
		t.Fatalf("应回显 2 次带截止时刻的请求退避 + 1 次校验重问，得 %d/%d：%v", retries, reasks, notes)
	}
}

// TestBriefErrIncludesAdapterFacts 守护lỗi 回显的可诊断性：网关 message 可能只有一câu 
// "Provider returned error"，回显phải 补上 litellm 携带的结构化事实（分类/HTTP trạng thái/provider/model ），
// và事实在前——截断时优先保住它们；非适配器lỗi 保持原样。
func TestBriefErrIncludesAdapterFacts(t *testing.T) {
	le := &litellm.LiteLLMError{
		Type: litellm.ErrorTypeProvider, StatusCode: 502,
		Provider: "openai", Model: "gpt-x", Message: "Provider returned error",
	}
	got := briefErr(fmt.Errorf("外层包装：%w", le))
	for _, want := range []string{"Lỗi dịch vụ thượng nguồn", "HTTP 502", "openai", "gpt-x", "Provider returned error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("回显nên chứa %q，得 %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "Lỗi dịch vụ thượng nguồn") {
		t.Fatalf("结构化事实应在前，得 %q", got)
	}
	if got := briefErr(errors.New("普通lỗi")); got != "普通lỗi" {
		t.Fatalf("非适配器lỗi 应保持原样，得 %q", got)
	}
}

// TestCallStructuredCancelIsNotSemanticFailure 守护取消语义：người dùng 取消（Esc）不是语义thất bại，
// 不得包装成「N 次尝试」的 errSemantic——那会误导排查方向并多落一份误导性 failures/ 工件。
func TestCallStructuredCancelIsNotSemanticFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &mockModel{responses: []string{"垃圾输出"}}
	_, err := callStructured[boundaryBatch](ctx, m, segmentContract, "sys", "p", 100, callProfile{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("nên trả về context.Canceled，得 %v", err)
	}
	var se *errSemantic
	if errors.As(err, &se) {
		t.Fatal("取消不应被包装成语义thất bại")
	}
}

// TestCallStructuredCarriesRawOnSemanticFailure 守护 §14.2：输出层契约违约时，
// lỗi phải 携带原始响应，供 runner 统一落 failures/ thất bại工件。
func TestCallStructuredCarriesRawOnSemanticFailure(t *testing.T) {
	m := &nativeImportModel{mockModel: &mockModel{responses: []string{"垃圾输出 not json"}}}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "payload", 100, callProfile{}, nil)
	var se *errSemantic
	if !errors.As(err, &se) {
		t.Fatalf("nên trả về errSemantic，得 %T：%v", err, err)
	}
	if se.Raw != "垃圾输出 not json" || !strings.Contains(se.Error(), "vi phạm hợp đồng") {
		t.Fatalf("Raw 应携带最后一次原始响应，得 %q", se.Raw)
	}
}

func TestCallStructuredCarriesRawOnProtocolFailure(t *testing.T) {
	m := &nativeImportModel{mockModel: &mockModel{
		responses: []string{"upstream malformed output"},
		stops:     []agentcore.StopReason{agentcore.StopReasonError},
	}}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "payload", 100, callProfile{}, nil)
	var se *errSemantic
	if !errors.As(err, &se) || se.Raw != "upstream malformed output" || !strings.Contains(se.Error(), "stop_reason=error") {
		t.Fatalf("协议lỗi 应携带原始响应，得 %T：%v", err, err)
	}
}
