# Thiết kế bộ nhớ đệm prompt: Phối hợp 3 tầng litellm / agentcore / ainovel

> Bài viết này là tài liệu giải thích: Giới thiệu cách chúng tôi thiết kế bộ nhớ đệm prompt LLM (LLM prompt caching) đầu cuối (end-to-end) thông qua ba repository phối hợp, bao gồm nguyên lý thiết kế, các trường hợp khắc phục sự cố thực tế và các vị trí mã nguồn để đối chiếu.
>
> - **litellm** —— Cổng (Gateway) LLM: Phiên dịch giao thức và Khai báo khả năng
> - **agentcore** —— Framework Agent: Đặt bộ nhớ đệm và Định danh bộ nhớ đệm (cache identity)
> - **ainovel-cli** —— Tầng ứng dụng: Tích hợp với cấu hình một dòng (tương tự như codebot)

---

## 1. Tại sao đáng để làm: Mô hình chi phí và Một trường hợp thực tế

Cấu trúc request của hệ thống Agent có một đặc điểm: **Mỗi vòng request đều mang theo toàn bộ lịch sử**. Trong một vòng lặp công cụ gồm 30 lượt, body của request ở lượt thứ 30 chứa tất cả các tin nhắn từ 29 lượt trước. Nếu không dùng cache, các byte tiền tố (prefix) giống nhau sẽ bị tính phí lặp đi lặp lại.

Bảng giá cache của hai nhà cung cấp lớn (lấy Anthropic làm ví dụ):

| Hạng mục | Tương đối so với giá đầu vào thông thường |
|---|---|
| Ghi cache (TTL 5 phút) | 1.25x |
| Ghi cache (TTL 1 giờ) | 2x |
| **Đọc cache** | **0.1x (Tiết kiệm 90%)** |

Trường hợp thực tế: Một lần tạo một cuốn tiểu thuyết 33 chương đã tiêu tốn $58, phân tích `meta/usage.json` sau đó phát hiện ra rằng **Tỷ lệ trúng (hit rate) cache tổng thể chỉ có 8.5%** (coordinator chỉ 2.7%, architect là 0). Sau khi so sánh từng request trên chuỗi usage (input vs cache_read), đã định vị được ba nguyên nhân gốc rễ:

1. **Sự dao động byte (byte jitter) của tools**: Description/Schema của công cụ `subagent` ở mỗi vòng được khởi tạo trực tiếp từ Go map, thứ tự lặp (iterate) là ngẫu nhiên → body request ngay từ byte thứ 0 đã khác với vòng trước → toàn bộ prefix cache bị vô hiệu;
2. **Không có tính gắn kết định tuyến (routing affinity)**: Dòng OpenAI không truyền `prompt_cache_key`, ngay cả những request có các byte hoàn toàn giống nhau cũng có thể bị cân bằng tải sang một instance không có cache (Bằng chứng thép: trong 33 session, các request đầu tiên với byte giống hệt nhau chỉ trúng 12 cái);
3. **Claude hoàn toàn không có điểm dừng (breakpoint)**: Anthropic dùng cache tường minh (explicit), việc không đánh điểm dừng `cache_control` đồng nghĩa với việc hoàn toàn không có cache.

Ba nguyên nhân gốc rễ này lần lượt tương ứng với ba thiết kế dưới đây: **Kỷ luật ổn định tiền tố**, **Định danh bộ nhớ đệm** và **Sắp xếp điểm dừng**.

---

## 2. Kiến thức chuẩn bị: Mô hình nhận thức của hai loại giao thức cache

### 2.1 OpenAI: Cache tiền tố tự động (Ngầm định - Implicit)

- Phía server tự động cache các tiền tố **≥1024 tokens**, không yêu cầu client khai báo;
- Việc trúng cache tăng lên theo mức độ canh lề (alignment) là 128-token;
- Request có thể mang theo `prompt_cache_key` (trường chính thức) để tạo tính **gắn kết định tuyến** —— các request có cùng key sẽ cố gắng hạ cánh trên cùng một phân mảnh (shard) cache;
- Trong usage, `cached_tokens` báo cáo số lượng trúng; **việc ghi cache không bao giờ được báo cáo** (`cache_write` luôn luôn là 0, đó là hiện tượng bình thường, không phải bug).

### 2.2 Anthropic: Điểm dừng tường minh (Explicit breakpoint - cache_control)

- Client đánh điểm dừng `cache_control` trên các khối nội dung, **điểm dừng sẽ bao phủ mọi thứ trước nó**
  (Thứ tự cố định là tools → system → messages);
- Mỗi request **tối đa 4 điểm dừng**;
- Giá ghi là 1.25x (5m) / 2x (1h), giá đọc là 0.1x;
- `cache_control` **không được phép đánh trên khối thinking** (sẽ bị từ chối bằng lỗi 400).

### 2.3 Tiền đề chung

Bất kể ngầm định hay tường minh, cache chỉ nhận biết **sự bằng nhau của tiền tố ở cấp độ byte**. Do đó, nền tảng của mọi thiết kế là cùng một câu nói:

> **Sắp xếp toàn bộ request theo tần suất thay đổi từ thấp đến cao: cái nào tĩnh thì để lên trước, cái nào động thì để ở phía sau, và lịch sử đã gửi không được phép thay đổi dù chỉ một byte.**

---

## 3. Kiến trúc tổng thể: Sự phân công của ba tầng

```
┌────────────────────────────────────────────────────────┐
│ Tầng ứng dụng (ainovel-cli / codebot)                    │
│   Quyết định giá trị của "định danh cache": 1 cuốn sách - 1 base,│
│   1 vai trò - 1 tên                                    │
│   Chi phí tích hợp = mỗi agent chỉ cần 2 dòng cấu hình           │
├────────────────────────────────────────────────────────┤
│ agentcore (Framework Agent)                              │
│   Quyết định "đặt điểm dừng ở đâu, key phái sinh khi nào": │
│   Sàn system + mũi nhọn cuộn của tin nhắn cuối; spawn thêm #seq;│
│   Kiểm soát truy cập (gating) dựa trên khả năng của provider, loại bỏ im lặng nếu không hỗ trợ│
├────────────────────────────────────────────────────────┤
│ litellm (Cổng LLM)                                       │
│   Thuần phiên dịch giao thức: cache_control ↔ các trường của từng hãng,│
│   chuyền (passthrough) prompt_cache_key, Khai báo khả năng Capabilities│
│   Không thực hiện bất kỳ quyết định "có nên cache hay không" nào│
└────────────────────────────────────────────────────────┘
```

Nguyên tắc chia tách: **litellm chỉ trả lời "endpoint này hỗ trợ gì", agentcore chỉ trả lời "điểm cache đặt ở đâu", tầng ứng dụng chỉ trả lời "định danh là gì"**. Mỗi tầng có thể kiểm tra (test) độc lập, khi thay đổi một ứng dụng (codebot sử dụng lại cùng bộ agentcore/litellm) thì không cần viết lại logic cache.

---

## 4. Nền tảng: Ba kỷ luật ổn định byte tiền tố

Tiền đề để cache mang lại lợi ích là các byte tiền tố phải ổn định. Ba kỷ luật này tương ứng với những sự cố có thật.

### Kỷ luật 1: Việc tuần tự hóa (serialization) tools phải có byte xác định (deterministic)

Sự cố: Công cụ `subagent` đã nhúng danh sách agent đã đăng ký vào Description/Schema của chính nó, trong khi danh sách này được lặp từ Go map —— thứ tự gọi ngẫu nhiên mỗi lần, các byte của tools thay đổi ở mỗi vòng, tỷ lệ trúng của coordinator vì vậy chỉ có 2.7%.
(Nhóm Claude Code cũng từng dính lỗi tương tự: Toàn bộ fleet của họ từng phải trả thêm 10.2% chi phí ghi cache vì lý do này.)

Cách sửa (agentcore `subagent/subagent.go`):

```go
// sortedAgentNames trả về các tên agent đã đăng ký theo thứ tự xác định (deterministic).
// Description và Schema được build lại ở mỗi lần gọi LLM; nếu lặp (iterate) trực tiếp
// qua map, nó sẽ làm xáo trộn các byte giữa các request và phá vỡ tiền tố cache
// của provider (tools serialize thành tiền tố prompt được cache).
func (t *Tool) sortedAgentNames() []string {
	return slices.Sorted(maps.Keys(t.agents))
}
```

> Hình thức chung của bài học này: **Bất kỳ tập hợp nào đi vào body của request, đều phải được sắp xếp trước khi serialize**. Sự ngẫu nhiên khi lặp map của Go sẽ giấu bug này rất sâu —— tính năng vẫn hoạt động hoàn toàn bình thường, chỉ có hóa đơn là bất thường.

### Kỷ luật 2: Lịch sử phải là append-only (Nén phải "commit")

Sự cố: Chiến lược nén ngữ cảnh (context compression) của writer là "chiếu (projection)" (thay đổi tạm thời chế độ xem lịch sử mỗi khi gọi, nhưng không ghi lại vào đường cơ sở (baseline)). Khi vượt quá ngưỡng, **mỗi vòng đều viết lại toàn bộ tiền tố** → Miss (trượt) toàn bộ mỗi vòng.

Cách sửa: Commit (áp dụng) sau khi chiếu (`CommitOnProject: true`), để việc thay đổi chỉ diễn ra một lần, sau đó khôi phục lại trạng thái append-only (chỉ thêm vào đuôi) cho đến khi lần tiếp theo vượt ngưỡng.

> Hình thức chung: Nén ngữ cảnh là một **sự đứt gãy một lần trong kế hoạch** (đặt lại (reset) tiền tố, trả giá đầy đủ 1 lần), điều này là không có vấn đề; cái không thể chấp nhận là **đứt gãy ở mọi vòng**. Hoặc là không nén, hoặc là nén xong thì cố định (solidify) lại.

### Kỷ luật 3: Nội dung động phải đi vào phần đuôi (tail)

Những thứ thay đổi ở mỗi vòng (phong bì (envelope) trạng thái thế giới, nhắc nhở từng vòng, kết quả tool mới nhất) chỉ được phép **thêm vào phía sau tin nhắn**, tuyệt đối không được quay lại thay đổi đoạn giữa. Phong bì `novel_context` của ainovel là một thiết kế kiểu thêm-vào-đuôi (tail-append) —— nó thay đổi ở mỗi chương, nhưng sự thay đổi của nó không ảnh hưởng đến cache của hàng trăm nghìn token phía trước.

---

## 5. Định danh bộ nhớ đệm: 1 sách 1 base, 1 vai trò 1 tên, 1 session 1 key

`prompt_cache_key` của dòng OpenAI giải quyết vấn đề **định tuyến (routing)**: Các request có byte giống hệt nhau nếu bị cân bằng tải sang một instance khác, thì vẫn miss (trượt) như thường. Mục tiêu thiết kế của key là "Các request trên cùng một huyết thống (lineage) cache, luôn mang cùng một key".

Ba cấp độ định danh của chúng ta (ainovel `internal/agents/build.go`):

```go
// promptCacheBase phái sinh ra một short hash (mã băm ngắn) ổn định từ thư mục sách, dùng làm tiền tố định danh
// cho cache prompt: cùng một cuốn sách sẽ chia sẻ chung bucket định tuyến qua các lần khởi động lại (restart) tiến trình,
// và không làm rò rỉ đường dẫn cục bộ (local path) cho provider. Hậu tố vai trò (role suffix) do bên gọi ghép nối vào,
// subagent mỗi khi spawn sẽ thêm "#seq" (một session là một key).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}
```

Tích hợp ở tầng ứng dụng chỉ cần hai dòng cho mỗi agent:

```go
writer := subagent.Config{
	// ...
	CacheLastMessage: "ephemeral",                // Công tắc điểm dừng Claude (xem §6)
	PromptCacheKey:   cacheBase + "-writer",      // Định danh định tuyến OpenAI (cấp độ vai trò - role)
}
// coordinator (Agent cấp cao nhất) tương tự:
agentcore.WithCacheLastMessage("ephemeral"),
agentcore.WithPromptCacheKey(cacheBase+"-coordinator"),
```

Cấp độ thứ ba (cấp session) do agentcore tự động phái sinh —— mỗi lần spawn ra một session mới, đó là một dòng huyết thống cache mới (agentcore `subagent/subagent.go`):

```go
runSeq := t.runSeq.Add(1)

// Một session (cuộc hội thoại), một cache key: Nối hậu tố (suffix) bằng dãy số cho mỗi lần chạy (per-run sequence)
// để mỗi lần spawn hình thành một huyết thống cache riêng, thay vì dồn mọi lần chạy của agent này vào chung một routing bucket.
promptCacheKey := cfg.PromptCacheKey
if promptCacheKey != "" {
	promptCacheKey = fmt.Sprintf("%s#%d", promptCacheKey, runSeq)
}
```

Hình thái cuối cùng: `nvl-a1b2c3-writer#17` = cuốn sách này, vai trò writer, session lần spawn thứ 17.

> Tại sao không dùng một key cho toàn cục (global)? Vì các session khác nhau có tiền tố khác nhau, nếu trộn chung vào một routing bucket (nhóm định tuyến) sẽ làm loãng tỷ lệ trúng.
> Tại sao không gắn timestamp/số ngẫu nhiên? Vì key phải **ổn định qua nhiều request**, trong cùng một session mỗi vòng đều phải giống nhau.

Thiết kế tương ứng của codebot: ngữ nghĩa của key = SessionID (chuyển session = đổi huyết thống), teammate nối thêm hậu tố tên, ứng dụng máy chủ (host) khi dùng lại một instance Agent để chuyển session thì gọi `Agent.SetPromptCacheKey` để trỏ lại định danh.

---

## 6. Sắp xếp điểm dừng của Claude: Sàn (Floor) + Mũi nhọn cuộn (Rolling tip)

Anthropic không đánh điểm dừng = không có cache. Phân bổ ngân sách của chúng ta (tối đa 4 điểm dừng/request):

```
[tools][system ←Điểm dừng ① "Sàn (Floor)"][...tin nhắn lịch sử...][tin nhắn mới nhất ←Điểm dừng ② "Mũi nhọn cuộn"]
```

### 6.1 Sàn (floor): Đóng đinh tiền tố tĩnh (static)

system prompt là khối tĩnh lớn nhất. Cho nó một điểm dừng riêng, để đảm bảo **khi session mới/cache ở phần đuôi bị đào thải (evict), thì ít nhất tiền tố system+tools vẫn được đọc từ cache** (agentcore `loop.go`):

```go
} else if agentCtx.SystemPrompt != "" {
	m := SystemMsg(agentCtx.SystemPrompt)
	if config.CacheLastMessage != "" {
		// Cache floor (Sàn): Đóng đinh (pin) system prompt tĩnh (static) bằng
		// điểm dừng riêng của nó, như vậy một session mới tinh — hoặc một vòng bị đào thải (evict)
		// mục ở đuôi — thì vẫn đọc được tiền tố system+tools từ cache.
		m.Metadata = map[string]any{"cache_control": config.CacheLastMessage}
	}
	prefix = append(prefix, m)
}
```

### 6.2 Mũi nhọn cuộn (rolling tip): Đẩy phạm vi bao phủ ở mỗi vòng

Đánh một điểm dừng vào **tin nhắn không phải là system cuối cùng**. Ở vòng lặp công cụ, mỗi lần gọi LLM sẽ ghi một cache bao phủ đến tool_use+tool_result mới nhất, vòng tiếp theo đọc trực tiếp luôn mà không cần truyền lại (retransmit):

```go
// markLastMessageForCache trả về bản sao của messages với cache_control được gắn
// vào siêu dữ liệu (metadata) của tin nhắn không-phải-system cuối cùng. Các tin nhắn system bị bỏ qua
// để những nhắc nhở theo từng vòng ở đuôi (tail per-turn reminders, thứ thay đổi ở mỗi vòng)
// không vô tình mang theo điểm dừng (breakpoint).
func markLastMessageForCache(messages []Message, cacheControl string) []Message {
	idx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleSystem {
			idx = i
			break
		}
	}
	// ...
}
```

Lưu ý việc bỏ qua system reminder ở đuôi: nó thay đổi ở mỗi vòng, việc đánh điểm dừng lên nó tương đương với việc mỗi vòng ghi một cache không bao giờ được sử dụng lại.

### 6.3 Ngữ nghĩa của khối cuối (last block): Mỗi tin nhắn chỉ đốt một điểm dừng

Ngữ nghĩa `cache_control` cấp độ tin nhắn (message) là "Ghi một điểm dừng sau tin nhắn này". Khi chuyển sang cấp độ khối (block), nó chỉ được phép nằm ở **khối có thể cache (cacheable block) cuối cùng** —— Việc đánh dấu mọi khối sẽ đốt cháy hết ngân sách 4 điểm dừng; đồng thời Anthropic từ chối cho phép khối thinking mang `cache_control`, do đó quét từ đuôi và bỏ qua reasoning
(agentcore `llm/litellm.go`):

```go
if cache != nil {
	// Anthropic từ chối cache_control trên các khối thinking — hãy đặt (land)
	// điểm dừng vào khối cuối cùng có thể cache (cacheable block).
	for i := len(blocks) - 1; i >= 0; i-- {
		if _, isReasoning := blocks[i].(litellm.ReasoningBlock); isReasoning {
			continue
		}
		blocks[i] = withBlockCache(blocks[i], cache)
		break
	}
}
```

### 6.4 Đường ống TTL

Giá trị cấu hình được quy ước bằng chuỗi `"type[:ttl]"`, ví dụ `"ephemeral"` (mặc định 5m) hoặc `"ephemeral:1h"`:

```go
func cacheControlFromMetadata(metadata map[string]any) *litellm.CacheControl {
	value, _ := metadata["cache_control"].(string)
	if value == "" {
		return nil
	}
	if typ, ttl, ok := strings.Cut(value, ":"); ok {
		return &litellm.CacheControl{Type: typ, TTL: ttl}
	}
	return &litellm.CacheControl{Type: value}
}
```

Việc có nên nâng lên 1h (1 giờ) hay không phải dựa vào dữ liệu: Giá ghi tăng từ 1.25x lên 2x, chỉ khi khoảng thời gian giữa các cuộc gọi (call interval) thực tế thường xuyên vượt quá 5 phút thì mới đáng làm (khoảng thời gian trung vị (median) của coordinator được đo đạc trong thực tế của chúng tôi là 172s, nên không nâng).

---

## 7. Gửi an toàn: Kiểm soát khả năng (Capability gating) + Phân định điểm cuối chính thức (Official endpoint)

### 7.1 Kiểm soát khả năng: Các trường không hỗ trợ thì không được ra ngoài

Các provider của litellm thực hiện **kiểm tra nghiêm ngặt** `ProviderOptions` (key không xác định sẽ báo lỗi ngay lập tức), do đó agentcore sẽ kiểm soát cổng dựa trên khai báo khả năng trước khi gửi (agentcore `llm/litellm.go`):

```go
// Định danh (identity) định tuyến prompt-cache. Bị kiểm soát khả năng (Capability-gated): litellm providers
// kiểm tra tính hợp lệ của provider options một cách nghiêm ngặt, do đó một key không hỗ trợ phải
// bị drop (loại bỏ) ở đây thay vì bị reject (từ chối) ở đó.
if callCfg.PromptCacheKey != "" && caps.Cache.PromptKey == litellm.SupportYes {
	req.ProviderOptions["prompt_cache_key"] = callCfg.PromptCacheKey
}
```

### 7.2 Phân định điểm cuối chính thức (Official endpoint): Hệ sinh thái tương thích không có hợp đồng đối với trường chưa biết

`prompt_cache_key` là trường chính thức của OpenAI, nhưng hoạt động của các điểm cuối (endpoint) "Tương thích OpenAI" không có bất kỳ hợp đồng thống nhất nào.
Thực chứng từ Internet (2026-07):

- **Phía kiểm tra nghiêm ngặt trực tiếp từ chối**: Groq, Cerebras, Huoshan, Fireworks trả về 400/422 đối với trường này
  (Zed #36215, OpenClaw #48155 đều phải sửa lại thành điều kiện gửi vì lý do này);
- **Trung gian kiểu biên dịch lại (re-marshal) loại bỏ im lặng (silent drop)**: Đường dẫn không truyền suốt (non-passthrough) của one-api/new-api/sub2api phân tích body request vào cấu trúc (struct) rồi re-marshal, các trường không xác định biến mất không một tiếng động (gửi cũng như không gửi);
- **Phía linh hoạt sẽ phớt lờ (ignore)**: Ollama, vLLM phiên bản hiện tại, MiniMax.

Do đó, khai báo khả năng provider openai của litellm phải phân định một cách **động (dynamic)** dựa trên BaseURL
(litellm `provider/openai/capabilities.go`):

```go
// promptCacheParamsSupport báo cáo xem endpoint này có được tin cậy để nhận
// các prompt cache params (prompt_cache_key / prompt_cache_retention) của OpenAI hay không.
// Chỉ có endpoint chính thức mới bảo đảm hợp đồng của trường này.
func (p *Provider) promptCacheParamsSupport() litellm.Support {
	if p.cfg.PromptCacheParams || isOfficialBaseURL(p.cfg.BaseURL) {
		return litellm.SupportYes
	}
	return litellm.SupportUnknown
}

func isOfficialBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}
```

`api.openai.com` chính thức → `SupportYes` (Gửi); BaseURL của bên thứ ba → `SupportUnknown`
(cổng chặn ở §7.1 tự động không gửi, **mặc định vĩnh viễn không làm hỏng bất kỳ endpoint nào**); đối với người dùng xác nhận rằng máy chủ trung gian (relay) của mình truyền trực tiếp (passthrough) y hệt như cũ, có thể tự opt-in tường minh (explicitly) trong cấu hình của provider:

```jsonc
"my-relay": {
  "type": "openai",
  "base_url": "https://relay.example.com/v1",
  "extra": { "prompt_cache_params": true }   // Tôi xác nhận trung gian này pass-through body request
}
```

> Tại sao công tắc này lại được làm ở tầng khả năng của litellm chứ không phải tầng cấu hình ứng dụng? Vì khi dùng lệnh `/model` để đổi provider lúc đang chạy, client sẽ bị đổi theo, khai báo khả năng sẽ tự động đổi theo client; phán định tại thời điểm dựng cấu hình của ứng dụng sẽ không bao phủ được việc chuyển đổi lúc runtime.

---

## 8. Quan trắc: Phát hiện đứt gãy chuỗi cache

Cache là một "tính năng vô hình" —— nếu hỏng thì không báo lỗi, chỉ là hóa đơn bị đắt lên. Vậy nên phải có quan trắc (chúng tôi tham khảo promptCacheBreakDetection của Claude Code và làm một bản thu gọn (lightweight)).

Tiêu chí phán định (ainovel `internal/host/usage.go`):

```go
// Trong cùng một session (role+task): tiền tố không bị rút ngắn, nhưng lượng hit (trúng) lại giảm >5% so với lần trước và mức giảm ≥2000 tokens
broke := prevPrefix > 0 && prefix >= prevPrefix &&
	float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
	prevRead-u.CacheRead >= cacheBreakMinDropTokens
```

Bốn thiết kế quan trọng, mỗi cái để phòng một loại cảnh báo sai (false positive):

| Thiết kế | Ngăn chặn cảnh báo sai (false positive) |
|---|---|
| **Ngưỡng kép (Dual threshold)** (Tương đối 5% và tuyệt đối 2000) | Ngưỡng tương đối đơn lẻ sẽ bị nhấn chìm bởi nhiễu (noise) của tiền tố nhỏ; ngưỡng tuyệt đối đơn lẻ sẽ bỏ lọt sự suy thoái của tiền tố lớn |
| **Đường cơ sở theo sau session (role+task)** | Chiều đo đạc bắt buộc phải bám sát (align) với hạt (granularity) của huyết thống `prompt_cache_key` (tức là `#seq`); nếu so sánh qua nhiều session dựa trên role, nó sẽ báo sai khi "session trước rất ngắn, request đầu của session mới có tiền tố dài hơn" (Đây là một lỗ hổng thực tế mà Codex review đã bắt được) |
| **Tiền tố ngắn lại = Reset hợp lệ** | Nén ngữ cảnh (Context compression) là một sự đứt gãy nằm trong kế hoạch, việc reset đường cơ sở (baseline) sẽ không sinh ra báo động |
| **Không kiểm tra khi replay (phát lại)** | Replay lại lịch sử lúc khởi động sẽ làm những sự đứt gãy cũ năm xưa hiện lên thành báo động mới |

Khi báo động, sẽ đưa ra nhắc nhở quy kết (attribution) dựa theo khoảng thời gian giữa các cuộc gọi: >1h → Nghi ngờ TTL 1h hết hạn; >5m → Nghi ngờ TTL 5m hết hạn; thời gian rất ngắn → Nghi ngờ bị server đào thải (evict)/định tuyến bị trôi dạt (**Nguyên nhân phổ biến nhất là server trung gian xoay vòng (round-robin) gọi luân phiên nhiều tài khoản cấp trên**). Bộ đếm sẽ được ghi cố định vào `usage.json` và hiển thị ở hàng "Đứt gãy chuỗi" trong panel cache của TUI.

---

## 9. Lằn ranh đỏ khóa chặn (Latch): Nguyên tắc session đơn điệu

Một ràng buộc mang tính hiến pháp đối với các chức năng trong tương lai:

> **Tất cả các đại lượng đi vào tiền tố cache (system prompt, tools, tham số thinking, tham số sampling (lấy mẫu)), sau khi được tính toán lần đầu tiên trong session phải bị đóng băng (frozen) —— Thà để nó cũ kỹ (stale), còn hơn là phá vỡ cache.**

Ví dụ: Những tính năng như "Điều chỉnh cường độ thinking lúc runtime", nếu cường độ mới có tác dụng ngay vào session đang chạy, tương đương với việc mỗi lần điều chỉnh đều viết lại tiền tố, vô hiệu hóa toàn bộ cache. Cách làm đúng là giá trị mới chỉ có hiệu lực đối với **các session mới spawn**.
Bất kỳ buổi đánh giá nhu cầu (requirements review) nào về các tính năng "Có thể điều chỉnh X lúc runtime", câu hỏi đầu tiên luôn là: X có nằm trong tiền tố cache hay không?

---

## 10. Các phán đoán sai phổ biến và Mức trần (Ceiling)

1. **`cache_write` của OpenAI luôn là 0 là chuyện bình thường** —— API không báo cáo lượng ghi (write), đừng coi đó là bug để mà tìm.
2. **Trần của server trung gian**: Nếu server trung gian xoay vòng (round-robin) qua nhiều tài khoản (account) cấp trên, client dù có truyền byte ổn định đến mấy cũng miss (cache của tài khoản A không khả kiến (visible) đối với tài khoản B). Điều này giải thích được bí ẩn "những request giống hệt byte nhau mà chỉ trúng 12/33 cái".
   **Đây không phải là vấn đề client có thể giải quyết** —— Dữ liệu của nhóm Claude Code cũng cho thấy khoảng 90% trường hợp "client không thay đổi nhưng vẫn đứt gãy" là do phía server.
3. **Tiêu chuẩn kiểm chứng**: File JSONL của session không chứa system prompt và toàn bộ body request, **chuỗi usage của từng request (input vs cache_read) mới là tiêu chuẩn vàng của chẩn đoán**. Một chỉ báo thực tiễn: Nếu lượng trúng luôn mắc kẹt ở mức "số lượng token của system prompt được làm tròn xuống mức 128", điều đó chứng tỏ chỉ có đoạn system bị trúng, còn đoạn message đều bị miss toàn bộ.
4. **Hạch toán lợi ích**: Giá đọc (read) 0.1x, giá ghi (write) 1.25x, nghĩa là một dòng cache chỉ cần được đọc 1 lần là hoàn vốn.
   Trong cuộc hội thoại agent có nhiều lượt, điểm dừng hầu như luôn luôn mang lại lợi ích dương, do đó `CacheLastMessage` không có công tắc để bật tắt, mà là mặc định bật.

---

## 11. Hướng dẫn tích hợp nhanh

**ainovel-cli** (đã tích hợp): Mỗi agent sẽ cấu hình `CacheLastMessage: "ephemeral"` +
`PromptCacheKey: promptCacheBase(bookDir) + "-<role>"`, các phần còn lại là tự động hóa hoàn toàn.

**codebot** (đã tích hợp): key = SessionID; Khi `Reset`/`SwitchSession` thì dùng
`agent.SetPromptCacheKey(newSessionID)`; teammate dùng `sessionID + "-" + name`.

**Danh sách tối thiểu để ứng dụng mới tích hợp agentcore**:

```go
agentcore.NewAgent(
	agentcore.WithCacheLastMessage("ephemeral"),   // Điểm dừng Claude: Sàn + Mũi nhọn cuộn
	agentcore.WithPromptCacheKey(stableIdentity),  // Định tuyến OpenAI: Ổn định, duy nhất mỗi session
	// ...
)
```

Cùng với "Ba câu hỏi tự kiểm tra" (Tương ứng với Ba kỷ luật):

1. Phép serialize tools của tôi có được xác định byte không? (Các tập hợp đã được sắp xếp chưa)
2. Lịch sử của tôi có phải là append-only (chỉ thêm vào đuôi) không? (Việc nén có được commit không)
3. Nội dung thay đổi ở mỗi vòng của tôi có được để ở đuôi không?

---

## 12. Danh sách kinh nghiệm dành cho người học

- Bản chất của tối ưu hóa cache là **kỷ luật byte**, không phải là điều chỉnh tham số (parameter tuning): Hãy đảm bảo tiền tố (prefix) ổn định trước, rồi mới nói đến key và điểm dừng (breakpoint).
- Chẩn đoán bao giờ cũng phải bắt đầu từ **chuỗi usage của từng request**, đừng ngồi đoán từ code.
- Việc xáo trộn do lặp qua Go map ngẫu nhiên + serialize body request = Sát thủ cache khó lường nhất, kiểm tra tính năng thông thường (functional test) vĩnh viễn không bao giờ phát hiện được.
- "Tương thích OpenAI" là một từ tiếp thị (marketing), không phải là một hợp đồng: Trước khi gửi các trường (field) chính thức đến các endpoint của bên thứ ba, hãy tìm các bằng chứng trực tiếp (mã nguồn/issue/cách sửa đã triển khai của các client tương tự), việc suy đoán kiểu "thường thì nó sẽ lờ đi (ignore)" là rất nguy hiểm.
- Quan trắc phải đặt ưu tiên ngăn ngừa cảnh báo sai (false positive) lên hàng đầu: Chiều đo đạc bắt buộc phải được gắn kết với kích cỡ hạt (granularity) của huyết thống cache; thà báo sót còn hơn báo sai, nếu không thì báo động rất nhanh sẽ bị người ta lờ đi.
- Tiêu chuẩn kiểm nghiệm sự phân tầng: Khi dùng ứng dụng khác (codebot) tích hợp vào, sẽ không cần phải viết lại dù chỉ một dòng của logic cache.

---

### Phụ lục: Mục lục tham chiếu mã nguồn

| Chủ đề | Vị trí |
|---|---|
| Sắp xếp tools có tính xác định (deterministic) | agentcore `subagent/subagent.go` `sortedAgentNames` |
| Phái sinh key cấp độ session (#seq) | agentcore `subagent/subagent.go` `runAgent` |
| Sàn system + Mũi nhọn cuộn | agentcore `loop.go` `callLLM` / `markLastMessageForCache` |
| Điểm dừng khối cuối + Bỏ qua thinking | agentcore `llm/litellm.go` `convertAgentBlocks` |
| Phân tích (Parse) TTL ("ephemeral:1h") | agentcore `llm/litellm.go` `cacheControlFromMetadata` |
| Kiểm soát khả năng (Capability gating) | agentcore `llm/litellm.go` `applyCallConfig` |
| Phân định endpoint chính thức + opt-in | litellm `provider/openai/capabilities.go` / `provider.go Config` |
| Định danh cache (1 cuốn sách - 1 base) | ainovel `internal/agents/build.go` `promptCacheBase` |
| Phát hiện đứt gãy | ainovel `internal/host/usage.go` `noteCacheBreak` |
| Định vị kiến trúc | ainovel `docs/architecture.md` §6.6 |
