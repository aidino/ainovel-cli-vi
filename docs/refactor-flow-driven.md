# Đề xuất Refactor: Hybrid Coordinator — Định tuyến của Host × Phán quyết của LLM

> **Tài liệu lịch sử, đã bị loại bỏ.** Hybrid Coordinator đã được thay thế bởi kiến trúc Engine + Arbiter vào ngày 2026-07-12; vui lòng xem thiết kế hiện hành tại `docs/architecture.md`, `docs/engine-rfc.md`. Tài liệu này chỉ được giữ lại làm bản ghi lưu trữ quá trình tiến hóa của các quyết định, không được dùng làm cơ sở triển khai.
>
> Trạng thái cũ: **Đã được chấp nhận và triển khai** (2026-04-20)
> Thời gian khảo sát: 2026-04-20
> Tài liệu hiện hành tương ứng: `docs/architecture.md` §2 / §3 / §7 / §8 / §13 đã được cập nhật đồng bộ
>
> **Tài liệu này là bản thảo thứ hai.** Các vấn đề của phương án cấp tiến trong bản thảo đầu tiên (xóa bỏ hoàn toàn Coordinator) được trình bày chi tiết trong Phụ lục A, giữ lại phần này để tránh đi vào vết xe đổ.
>
> Kết quả triển khai:
> - Tạo mới thư mục `internal/host/flow/` (router.go / state.go / dispatcher.go / router_test.go, 15 nhánh unit test đều pass)
> - Xóa `flow.go` / `queue_guard.go` / `book_complete.go` trong `internal/host/reminder/`; giữ lại StopGuard và Guard của subagent
> - Rút gọn `assets/prompts/coordinator.md` từ 88 dòng xuống còn ~45 dòng (thu hẹp trách nhiệm thành: thực thi chỉ thị của Host + phán quyết + chọn loại planning lúc khởi động)
> - Đơn giản hóa đáng kể `internal/host/resume.go`, chỉ tạo label và prompt ngắn gọn, bước tiếp theo cụ thể sẽ do Router điều phối sau TurnEnd đầu tiên
> - Bổ sung các phương thức hỗ trợ `HasArcReview` / `HasArcSummary` / `HasVolumeSummary` / `CheckConsistency` vào thư mục `internal/store/`
> - Lỗi agent state không dừng ở 'working' trong `observer.go` cũng đã được sửa cùng lúc

---

## 1. Bối cảnh

### 1.1 Định vị dự án

```
agentcore       — Framework agent đa năng
litellm         — Cổng (Gateway) LLM đa năng
ainovel-cli     — Agent chuyên biệt (vertical) cho việc sáng tác tiểu thuyết (dự án này)
```

Không gian ra quyết định của các agent chuyên biệt (vertical agent) là **khép kín**: Lưu đồ (flowchart) cố định, các nhánh rẽ có hạn và được dẫn dắt bởi sự thật (fact-driven). Triết lý thiết kế của các agent đa năng ("đặt cược vào năng lực của model") khi áp dụng vào các kịch bản chuyên biệt có vẻ như hơi thuần túy quá mức.

### 1.2 Mục tiêu người dùng (Theo thứ tự ưu tiên)

1. **Tính ổn định** — Có thể tiếp tục viết liên tục, không bị gián đoạn do lỗi định tuyến (routing)
2. **Hưởng lợi từ việc nâng cấp LLM** — Kiến trúc không đối đầu với năng lực của model
3. **Tận dụng tối đa khả năng đa agent (multi-agent)** — Phân công chức năng rõ ràng

Đề xuất này thực hiện sự **cải tiến Pareto** (không hy sinh bất kỳ mục tiêu nào để đổi lấy mục tiêu khác) giữa 3 yếu tố trên.

---

## 2. Khảo sát hiện trạng

### 2.1 Phân loại các điểm ra quyết định của Coordinator

Trích xuất từng điểm ra quyết định trong `coordinator.md`:

| # | Điểm ra quyết định | Tính chất | Tần suất |
|---|---|---|---|
| 1 | Chọn architect_long / short khi khởi động | Phán quyết (Hiểu ngữ nghĩa) | 1 lần / cuốn sách |
| 2 | Mở rộng đầu vào (tự động bổ sung nếu <20 chữ) | Phán quyết (Tính sáng tạo) | 0-1 lần / cuốn sách |
| 3 | Vòng lặp bổ sung đại cương (quy hoạch) | Định tuyến (Dẫn dắt bởi sự thật) | 1-3 lần |
| 4 | Bước tiếp theo sau khi commit mỗi chương | **Định tuyến** | **1-2 lần / chương** |
| 5 | Thực hiện từng bước phần review cuối arc | Định tuyến | 3-5 lần / arc |
| 6 | Phân nhánh review verdict (phán quyết) | Định tuyến (Đã mã hóa thành code, xem §2.3) | 1 lần / arc |
| 7 | Xử lý can thiệp của người dùng | Phán quyết (Bắt buộc dùng LLM) | Bất kỳ lúc nào |
| 8 | Báo lỗi từ subagent và gửi lại | Định tuyến | Thỉnh thoảng |
| 9 | Xuất bản tóm tắt khi hoàn thành toàn bộ cuốn sách | Định tuyến | 1 lần |

**Kết luận**: Trong 9 điểm ra quyết định, có 6 điểm là định tuyến thuần túy (tra bảng), và 3 điểm là phán quyết thực sự cần đến LLM. **Tần suất định tuyến cao hơn nhiều so với phán quyết** (1-2 lần mỗi chương vs. vài lần cho cả cuốn sách).

### 2.2 Kênh Reminder đã là một bán thành phẩm của việc mã hóa quy trình

Các generator (bộ sinh) trong thư mục `internal/host/reminder/` sẽ sinh ra **các chỉ thị hành động cụ thể** dựa trên sự thật (facts) ở mỗi lượt:

- `flow.go` → `"flow hiện tại=writing, next_chapter=37. Vui lòng gọi trực tiếp subagent(writer, \"Viết chương 37\")..."`
- `queue_guard.go` → `"flow hiện tại=rewriting, hàng đợi chờ xử lý: [3,5]. Vui lòng gọi writer viết lại từng chương ngay lập tức..."`
- `book_complete.go` → `"Toàn bộ cuốn sách đã hoàn thành. Vui lòng xuất tóm tắt toàn bộ cuốn sách..."`

**Kiến trúc hiện tại tồn tại tính trạng double dispatch (điều phối hai lần)**:
```
Tầng quy tắc: coordinator.md định nghĩa "Nếu A thì B"
  ↓
Tầng Reminder: Cụ thể hóa quy tắc dựa trên sự thật ở mỗi lượt → Sinh ra "Bây giờ vui lòng làm B"
  ↓
Tầng LLM: Đọc reminder để sinh ra tool_call (Về cơ bản chỉ là nhắc lại reminder)
  ↓
SubAgent thực thi
```

**LLM thực tế chỉ đang "thực thi" các chỉ thị mà Reminder cung cấp cho nó**. Khâu trung gian này vừa tốn tokens, vừa mang đến sự không chắc chắn (LLM có thể không hoàn toàn tuân thủ reminder, ví dụ như lỗi định tuyến 'mid' đã từng quan sát được).

### 2.3 Tầng công cụ từng gánh vác quá nhiều phán quyết ngữ nghĩa

- Bản triển khai cũ của `save_review` từng ghi đè Editor verdict dựa trên ngưỡng điểm cố định và trạng thái hợp đồng (contract); nay đã bị xóa, phán quyết văn học thuộc về Editor, công cụ chỉ thực hiện xác thực giao thức (protocol validation) và ánh xạ trạng thái nguyên tử (atomic state mapping).
- `commit_chapter.CheckArcBoundary()`: Tính toán tức thời (on-the-fly) các giá trị `arc_end / needs_expansion / needs_new_volume`
- `commit_chapter.applyCompletion()`: Đánh giá tức thời `book_complete`
- `CommitResult` trả về 17 field sự thật

**Kết luận**: Hãy giữ lại bộ nhớ lưu trữ xác định (deterministic storage) và các bất biến của giai đoạn (phase invariants) ở tầng công cụ, và giao phó việc phán quyết văn học cũng như ngữ nghĩa cho model.

### 2.4 Chi phí thực tế của hiện trạng

Số lượt LLM của Coordinator cho mỗi chương:
- **1-2 lượt (turns) mỗi chương** (Đọc system prompt ~3000 tokens + reminder ~200 tokens + lịch sử + CommitResult ~500 tokens → Sinh ra tool_call ~50 tokens)
- Đối với trường thiên (truyện dài) 200 chương, số lượt gọi LLM của Coordinator vào khoảng **200-400 lượt**
- Trong đó **~90% là định tuyến thuần túy** (LLM nhắc lại reminder), **~10% là phán quyết**

**Khoảng ~3500-7000 tokens mỗi chương bị tiêu tốn cho việc ra quyết định của Coordinator, 95% là dư thừa** (Reminder đã tính sẵn đáp án).

---

## 3. Phương án thiết kế: Hybrid Coordinator (Coordinator lai)

### 3.1 Ý tưởng cốt lõi

**Chuyển quy trình ra quyết định từ LLM sang Host, nhưng vẫn giữ Coordinator làm điểm (node) phán quyết và kênh thực thi chỉ thị**.

```
┌──────────────────────────────────────────────────────────┐
│                   Entry (TUI / headless)                   │
└────────────────────────────────┬─────────────────────────┘
                                 │ Khởi động (Start) / Khôi phục (Resume) / Chèo lái (Steer)
┌────────────────────────────────▼─────────────────────────┐
│                            Host                            │
│                                                             │
│   ┌──────────────────────────────────────────────────┐     │
│   │  Flow Router (Bộ định tuyến luồng - Cốt lõi mới)   │     │
│   │  ───────────                                      │     │
│   │  Đăng ký (Subscribe) sự kiện Coordinator: Kích hoạt khi tool subagent trả về│     │
│   │  Hàm thuần túy (Pure function): route(Progress, Checkpoint, Boundary)│     │
│   │      → NextInstruction (Chỉ thị tiếp theo)             │     │
│   │  Có chỉ thị → coordinator.FollowUp(chỉ thị)            │     │
│   │  Không có chỉ thị (Kịch bản phán quyết) → Không can thiệp, để LLM tự quyết│     │
│   └──────────────────────────────────────────────────┘     │
│                                                             │
│   Giữ lại: API Vòng đời / Observer / Trình theo dõi (Tracker) Sử dụng│
│   Giữ lại: resume.go (Đơn giản hóa, không đổi logic cốt lõi) │
└────────────────────────────────┬─────────────────────────┘
                                 │
┌────────────────────────────────▼─────────────────────────┐
│                    Coordinator Agent (LLM)                  │
│                                                             │
│   Trách nhiệm được thu hẹp vào hai loại:                       │
│   1. Nhận chỉ thị FollowUp của Host → Sinh ra tool_call tương ứng│
│   2. Tự chủ phán quyết khi người dùng Steer (Truy vấn/Sửa đổi đánh giá)│
│                                                             │
│   coordinator.md: Từ 88 dòng → ~25 dòng                      │
│   MaxTurns: 1000 được giữ nguyên (Phản hồi steer của người dùng + thực thi chỉ thị Host)│
└────────────────────────────────┬─────────────────────────┘
                                 │
                                 ▼
         ┌──────────────────────┼───────────────────────┐
         ▼                      ▼                       ▼
    ┌────────┐             ┌────────┐             ┌────────┐
    │Architect│             │ Writer │             │ Editor │
    └────────┘             └────────┘             └────────┘
```

### 3.2 Phân chia lại trách nhiệm

| Tầng | Làm gì | Không làm gì |
|---|---|---|
| **Host / Flow Router** | Đọc sự thật → Định tuyến bằng hàm thuần túy → Chỉ thị FollowUp | Tự gọi SubAgent (Vẫn phải đi qua Coordinator) |
| **Coordinator** | Thực thi chỉ thị của Host + Phán quyết can thiệp của người dùng + Chọn nhà quy hoạch khi khởi động | Tự quyết định "bước tiếp theo làm gì" |
| **SubAgent (A/W/E)** | Thực hiện công việc chuyên môn của mình | Không thay đổi |
| **Tầng công cụ (Tool layer)** | Ghi xuống đĩa một cách nguyên tử + Trả về sự thật | Không thay đổi |

**Các bất biến quan trọng**:
- ✅ Coordinator vẫn là một agent run liên tục, giữ được khả năng "cảm nhận liên tục" cho toàn bộ cuốn sách.
- ✅ Steer của người dùng vẫn đi qua `coordinator.Inject()`, khả năng ngắt (interrupt) ngay lập tức được giữ nguyên.
- ✅ SubAgentTool vẫn do LLM gọi (đi theo luồng gốc của agentcore), luồng sự kiện (event stream) / ContextManager / việc chuyển đổi model đều không thay đổi.
- ✅ agentcore không cần phải sửa đổi bất kỳ điều gì (zero modifications).

### 3.3 Logic cụ thể của Flow Router

```go
// internal/host/flow/router.go

type NextInstruction struct {
    Agent  string   // architect_long / architect_short / writer / editor
    Task   string   // Mô tả tác vụ cho subagent
    Reason string   // Lý do để Coordinator xem (tùy chọn, để tiện gỡ lỗi)
}

type RouterState struct {
    Progress        *domain.Progress
    LatestCheckpoint *domain.Checkpoint
    // Ranh giới arc của chế độ phân tầng (tính toán khi chương trước hoàn thành)
    LastCompleted   int
    ArcBoundary     *store.ArcBoundary
    HasArcReview    bool
    HasArcSummary   bool
    // Cơ sở thiết lập (foundation) bị thiếu
    FoundationMissing []string
}

// Route trả về chỉ thị tiếp theo. Trả về nil có nghĩa là để LLM tự quyết định (kịch bản phán quyết).
func Route(s RouterState) *NextInstruction {
    p := s.Progress

    // 0. Trạng thái cuối: Để LLM xuất bản tóm tắt, không định tuyến
    if p.Phase == domain.PhaseComplete {
        return nil
    }

    // 1. Giai đoạn quy hoạch (planning): Việc phán quyết (chọn nhà quy hoạch) do LLM làm, không định tuyến
    if p.Phase != domain.PhaseWriting {
        return nil
    }

    // 2. Giai đoạn sáng tác (writing)
    // 2a. Ưu tiên hàng đợi viết lại (rewrite)/đánh bóng (polish)
    if len(p.PendingRewrites) > 0 {
        ch := p.PendingRewrites[0]
        verb := "Viết lại"
        if p.Flow == domain.FlowPolishing {
            verb = "Đánh bóng"
        }
        return &NextInstruction{
            Agent:  "writer",
            Task:   fmt.Sprintf("%s chương %d", verb, ch),
            Reason: fmt.Sprintf("Hàng đợi PendingRewrites còn lại %d chương", len(p.PendingRewrites)),
        }
    }

    // 2b. Đang duyệt (reviewing): Không định tuyến, để Coordinator đi theo nhánh verdict dựa trên kết quả save_review
    if p.Flow == domain.FlowReviewing {
        return nil
    }

    // 2c. Xử lý sau khi kết thúc arc của chế độ phân tầng
    if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
        b := s.ArcBoundary
        if !s.HasArcReview {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("Đánh giá cấp arc cho quyển (tập) %d arc %d", b.Volume, b.Arc),
                Reason: "Chưa hoàn thành review cuối arc",
            }
        }
        if !s.HasArcSummary {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("Tạo tóm tắt cho quyển %d arc %d", b.Volume, b.Arc),
                Reason: "Chưa hoàn thành tóm tắt arc",
            }
        }
        if b.NeedsExpansion {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   fmt.Sprintf("Triển khai quyển %d arc %d (save_foundation type=expand_arc)", b.NextVolume, b.NextArc),
                Reason: "Bộ khung arc tiếp theo cần được triển khai",
            }
        }
        if b.NeedsNewVolume {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   "Đánh giá và thực thi save_foundation(type=append_volume) hoặc mark_final",
                Reason: "Kết thúc quyển, cần quyết định có thêm quyển mới không",
            }
        }
    }

    // 2d. Viết tiếp bình thường
    next := p.NextChapter()
    return &NextInstruction{
        Agent:  "writer",
        Task:   fmt.Sprintf("Viết chương %d", next),
        Reason: "Viết tiếp",
    }
}
```

**Đặc điểm của hàm**:
- Hàm thuần túy (Đầu vào là RouterState, đầu ra là NextInstruction)
- Có thể test (unit test) (Cho trước trạng thái, xác nhận kết quả định tuyến)
- **Trả về nil là hợp lệ** —— Biểu thị "Đây là kịch bản phán quyết, vui lòng để LLM tự chủ"

### 3.4 Thời điểm kích hoạt (Trigger timing)

Host đăng ký sự kiện `agentcore.EventToolExecEnd`:

```go
coordinator.Subscribe(func(ev agentcore.Event) {
    if ev.Type == agentcore.EventToolExecEnd && ev.Tool == "subagent" && !ev.IsError {
        // SubAgent vừa trả về kết quả → Đọc trạng thái mới nhất → Định tuyến
        h.flowRouter.Dispatch()
    }
})
```

```go
func (r *FlowRouter) Dispatch() {
    state := r.loadState()
    instruction := Route(state)
    if instruction == nil {
        return // Kịch bản phán quyết, để LLM tự chủ
    }
    msg := formatInstruction(instruction)
    _ = r.coordinator.FollowUp(agentcore.UserMsg(msg))
}

func formatInstruction(i *NextInstruction) string {
    return fmt.Sprintf(
        "[Chỉ thị từ Host] Bước tiếp theo: Gọi subagent(%s, %q)\n"+
        "Lý do: %s\n"+
        "Đây là một chỉ thị rõ ràng từ tầng quy trình, vui lòng thực thi ngay lập tức, đừng gọi novel_context trước, đừng xuất suy luận trước.",
        i.Agent, i.Task, i.Reason,
    )
}
```

### 3.5 Khả năng đáp ứng (Responsiveness) và Đồng thời (Concurrency)

**Đường dẫn Steer của người dùng** (Không thay đổi):
```
Steer → coordinator.Inject(UserMsg("[Người dùng can thiệp] xxx"))
```

- Đang chạy: Chèn tin nhắn vào hàng đợi run hiện tại
- Rảnh (Idle): resume run
- Tạm dừng (Paused): Đưa vào hàng đợi

**Sự đồng thời của Chỉ thị định tuyến + Steer**:
- Cả hai đều đi vào hàng đợi tin nhắn của Coordinator, xử lý theo thứ tự nguyên bản của agentcore.
- Nếu Host vừa gửi `FollowUp("[Chỉ thị từ Host] Viết chương 37")`, ngay sau đó người dùng gửi Steer `"Dừng lại một chút, điều chỉnh phong cách"`
  - Coordinator sẽ xử lý chỉ thị của Host trước? Hay xử lý Steer trước?
  - **Ngữ nghĩa của `Inject` là chen ngang vào đầu hàng đợi hiện tại**, vì vậy Steer sẽ được xử lý trước.
  - Đây là hành vi mong muốn: Can thiệp của người dùng được ưu tiên cao hơn việc điều độ (dispatch) thường lệ của Host.

**Tránh xung đột giữa Chỉ thị của Host và Steer**:
- Sau khi nhận được tín hiệu "Đã inject Steer", Flow Router sẽ **tạm dừng trong chốc lát** trong vài turn (Để Coordinator xử lý xong Steer rồi mới định tuyến).
- Cảm nhận kết quả xử lý Steer thông qua việc đăng ký `agentcore.EventMessageEnd` kết hợp với việc kiểm tra sự thay đổi trạng thái của Progress.

### 3.6 Ví dụ đơn giản hóa coordinator.md

Cắt giảm từ 88 dòng xuống còn khoảng 25 dòng:

```markdown
Bạn là người điều phối tổng thể việc sáng tác tiểu thuyết.

## Chế độ làm việc của bạn

**Tuyến chính (Mainline)**: Host sẽ đưa ra thông báo `[Chỉ thị từ Host]` sau mỗi lần một subagent trả về, cho bạn biết bước tiếp theo cần gọi subagent nào và làm gì. Khi nhận được chỉ thị, hãy tạo ngay tool_call tương ứng, không gọi novel_context để suy luận trước, không nhắc lại.

**Phán quyết**: Khi gặp các tình huống sau, bạn cần tự đưa ra phán đoán (Host sẽ không đưa ra chỉ thị, bạn phải chủ động hành động):

### Khi khởi động: Chọn nhà quy hoạch (architect)

- Mặc định → `architect_long`
- Chỉ khi người dùng yêu cầu rõ ràng là truyện ngắn/một quyển (tập) duy nhất/tiểu phẩm (skit) VÀ độ dài được giới hạn trong 25 chương → `architect_short`

Nếu đầu vào của người dùng < 20 chữ, trước tiên hãy bổ sung hướng đi khác biệt, đối tượng độc giả mục tiêu và ít nhất một cái hook (móc nối) cốt truyện không theo thông lệ vào mô tả task, sau đó mới phái đi.

### Người dùng Steer (Can thiệp)

Định dạng: `[Người dùng can thiệp] xxx`

- **Loại truy vấn** (Hỏi trạng thái/thiết lập): Trực tiếp xuất câu trả lời bằng văn bản, không cần gọi công cụ nữa; Host sẽ tiếp tục phái đi (dispatch).
- **Loại sửa đổi** (Yêu cầu đổi thiết lập/viết lại/điều chỉnh phong cách): Đánh giá phạm vi ảnh hưởng:
  - Liên quan đến thay đổi thiết lập → Gọi architect_* để thực thi `save_foundation(type=...)`
  - Liên quan đến các chương đã viết → Yêu cầu công cụ tự động đưa các chương mục tiêu vào `PendingRewrites` (có thể bằng cách ghi rõ ý định viết lại khi gọi writer lần sau)
  - Chỉ ảnh hưởng đến phong cách phần sau → Mô tả ngắn gọn yêu cầu, khi nhận được chỉ thị của Host lần sau, hãy đính kèm vào mô tả task của writer.

## Công cụ (Tools)

- `subagent(agent, task)`: Gọi subagent
- `novel_context`: Chỉ sử dụng khi truy vấn của người dùng yêu cầu, đừng gọi nó trước khi chỉ thị của Host đến.

## Subagent (Tác nhân con)

- `architect_long` / `architect_short` / `writer` / `editor`

## Cấm

- Không gọi novel_context trước khi hành động lúc nhận được chỉ thị từ Host.
- Không tự quyết định bước tiếp theo khi không có Steer của người dùng và không có chỉ thị từ Host.
```

### 3.7 Kênh Reminder được thu gọn đáng kể

**Xóa**:
- `flow.go` (Host FollowUp đã đưa ra chỉ thị cụ thể, lời nhắc định tuyến của Reminder mất đi giá trị)
- `queue_guard.go` (Hàng đợi được đảm bảo bởi Host Router)
- `book_complete.go` (Khi Phase=Complete, Host sẽ gửi FollowUp với chỉ thị xuất tóm tắt)

**Giữ lại**:
- `subagent_guards.go` (StopGuard của Writer/Architect/Editor, đảm bảo subagent không kết thúc với tay trắng)
- Thêm mới một `foundation_reminder.go` gọn nhẹ: Giai đoạn quy hoạch (planning) thông báo cho Coordinator những phần thiết lập còn thiếu (Đây là **thông tin cần thiết cho việc phán quyết**, không phải là chỉ thị định tuyến)

**Giữ lại StopGuard**:
- Giữ lại StopGuard của Coordinator (Chặn `end_turn` khi `Phase != Complete` để làm bước đệm an toàn).
- Thêm mới tính năng tiêm (inject) lời nhắc khi "Nhận được chỉ thị từ Host nhưng trong lượt này chưa gọi subagent tương ứng".

### 3.8 resume.go được đơn giản hóa một chút

Hiện tại `buildResumePrompt` sinh ra chỉ thị bằng ngôn ngữ tự nhiên chính xác đến từng step dựa trên checkpoint (121 dòng).

Kiến trúc mới:
- Khi Resume, đầu tiên sẽ đọc Progress, Flow Router tính toán ra `NextInstruction`.
- Coordinator sẽ nhận được một resume prompt **rất ngắn gọn**, sau đó chờ chỉ thị FollowUp của Host.

```
[Khôi phục] Cuốn sách «xxx» hiện tại đã hoàn thành N chương, và đã bước vào giai đoạn XX.
Vui lòng chờ chỉ thị tiếp theo từ Host, hoặc xử lý sự can thiệp của người dùng có thể đã bị bỏ lại trong thời gian hệ thống dừng hoạt động.
```

Hầu như toàn bộ logic phân nhánh đã được đẩy xuống Flow Router (Bản thân Router phải định tuyến theo trạng thái, nên việc Resume không cần đường dẫn đặc biệt).

---

## 4. Đánh giá mức độ đạt được mục tiêu

### 4.1 Tính ổn định

| Rủi ro | Hiện tại | Kiến trúc mới |
|---|---|---|
| Coordinator chọn sai architect | Từng xảy ra (Lỗi định tuyến mid) | Lúc khởi động vẫn là phán quyết, nhưng prompt từ 3 mức chuyển thành nhị phân (đã làm), phạm vi sai sót giảm đi rất nhiều |
| Coordinator không tuân thủ "Chỉ nói viết chương N" | Từng xảy ra | Host đưa ra chỉ thị theo định dạng cố định, không cần LLM sinh mô tả task nữa |
| Coordinator quên kiểm tra `queue_drained` | Từng xảy ra | Host Router bắt buộc đi theo đúng thứ tự |
| Sau khi commit cuối arc, Coordinator quên gọi editor | Có thể xảy ra | Host Router phát hiện `IsArcEnd && !HasArcReview` sẽ phái đi ngay lập tức |
| Bỏ sót nhánh khôi phục sau sự cố | Lỗ hổng đã biết | Máy trạng thái (state machine) của Flow Router tự nhiên bao phủ mọi nhánh |
| StopGuard chặn liên tục 5 lần dẫn đến lỗi fatal | Tồn tại | Sau khi có chỉ thị rõ ràng từ Host, LLM rất khó để chặn liên tục (trừ khi prompt bị mất tác dụng nghiêm trọng) |

### 4.2 Lợi ích từ việc nâng cấp LLM

| Khía cạnh | Mức độ giữ lại |
|---|---|
| Nâng cấp model Writer → Chất lượng sáng tác | 100% |
| Nâng cấp model Editor → Tính chính xác của review | 100% |
| Nâng cấp model Architect → Quy hoạch tinh tế | 100% |
| **Nâng cấp model Coordinator → Phán quyết chính xác hơn** | **100%** (Các kịch bản phán quyết được giữ lại) |
| ~~Nâng cấp model Coordinator → Định tuyến chính xác hơn~~ | Bỏ qua (Tỷ lệ lỗi định tuyến vốn dĩ phải là 0, không cần LLM trở nên thông minh hơn) |

**Điểm giữ lại quan trọng**: Các kịch bản phán quyết như đánh giá can thiệp của người dùng, chọn nhà quy hoạch, phán đoán ranh giới verdict, v.v., vẫn do LLM xử lý, nên sẽ được hưởng lợi trực tiếp từ việc nâng cấp model.

### 4.3 Khả năng đa agent

- Số lượng, chức năng và cách lắp ráp SubAgent **hoàn toàn không thay đổi**.
- Cấu hình model không đồng nhất (coordinator/architect/writer/editor được cấu hình độc lập) **hoàn toàn không thay đổi**.
- Coordinator vẫn là luồng (run) liên tục, giữ lại "góc nhìn toàn bộ cuốn sách".
- Môi giới cộng tác (các sản phẩm trong Store) không thay đổi.

### 4.4 Khả năng đáp ứng

- Khả năng ngắt (interrupt) của Steer người dùng thông qua `coordinator.Inject` **hoàn toàn được giữ lại**.
- Host Router phái đi các chỉ thị khi SubAgent trả về, đi cùng một kênh tin nhắn với Steer của người dùng.
- Ưu tiên của Inject cao hơn FollowUp (Ngữ nghĩa của `Inject` là chen ngang), nên Steer sẽ không bị chỉ thị của Host đẩy ra.

### 4.5 Chi phí Token

Mỗi chương hiện tại: Coordinator ~3500-7000 tokens × 1-2 turns = 3500-14000 tokens

Kiến trúc mới mỗi chương:
- Prompt của Coordinator giảm từ ~3000 tokens xuống ~800 tokens.
- Mỗi chương vẫn cần 1 turn (Coordinator đọc chỉ thị FollowUp + sinh ra tool_call).
- Tổng cộng ~1000-1500 tokens.

**Tiết kiệm được 60-80%**. Tiểu thuyết dài 200 chương tiết kiệm được khoảng 400k-1M tokens (Không bằng con số 100% của phương án cấp tiến, nhưng không hy sinh khả năng đáp ứng và góc nhìn toàn cuốn sách).

---

## 5. Tác động đến docs/architecture.md

### 5.1 Điều chỉnh các nguyên tắc cốt lõi trong §2

**Nguyên tắc một** (Vòng lặp chính được dẫn dắt bởi LLM) → Điều chỉnh thành:
```
LLM dẫn dắt việc sáng tác và phán quyết, Host dẫn dắt việc định tuyến luồng (flow routing).

- Sáng tác và phán quyết (những quyết định cần hiểu ngữ nghĩa, đánh giá chất lượng, nhận diện ý định) vẫn dành cho LLM.
- Định tuyến quy trình (đọc sự thật → tra bảng → phát chỉ thị) được đảm nhiệm bởi code của Host.
- Host không đi vòng qua Coordinator để gọi trực tiếp SubAgent, mà thông qua FollowUp để đưa ra các chỉ thị gọi rõ ràng,
  giữ lại Coordinator như một kênh thực thi chỉ thị và điểm (node) phán quyết.
```

**Nguyên tắc hai** (Đặt cược vào năng lực của model, không đặt cược vào hard code) → Điều chỉnh thành:
```
Trong phương diện sáng tác và phán quyết, hãy đặt cược vào model (Năng lực phán quyết của Writer/Editor/Architect/Coordinator);
Trong phương diện định tuyến luồng, hãy dùng code để diễn đạt (Không gian ra quyết định của các agent chuyên biệt (vertical) là khép kín, các nhiệm vụ kiểu tra bảng sẽ không được hưởng lợi từ LLM).
```

### 5.2 Điều chỉnh danh sách cấm trong §13

- §13.13 "Không làm: Host đọc file tín hiệu → Giao diện điều khiển xác định (deterministic control plane) tiêm chỉ thị bước tiếp theo" →
  **Sửa đổi câu từ**: "Không sử dụng file tín hiệu làm IPC (chỉ cần đọc trực tiếp Progress + Checkpoint là được). Việc Host đọc sự thật sau đó thông qua `coordinator.FollowUp` để đưa ra chỉ thị gọi subagent rõ ràng là định tuyến chuyên biệt (vertical routing) hợp lý"
- §13.14 "Không hard code việc chuyển đổi Flow của máy trạng thái" →
  **Sửa đổi câu từ**: "Nhãn (label) Flow vẫn chỉ do công cụ cập nhật (không viết state machine 'Nếu A thì SetFlow(B)' trong Host), nhưng Flow Router có thể quyết định bước tiếp theo sẽ gọi ai dựa trên Flow và các sự thật khác"

### 5.3 Điều chỉnh lắp ráp Agent trong §7

- Giữ lại việc lắp ráp Coordinator.
- `coordinator.md` cắt giảm từ 88 dòng xuống còn ~25 dòng.
- Thu gọn kênh Reminder (xóa flow/queue_guard/book_complete, giữ lại foundation/subagent_guards).
- Thêm mới package `internal/host/flow/`.

---

## 6. Các điểm yếu đã biết (Trình bày trung thực)

### 6.1 Sự tiến hóa lâu dài của Flow Router

- Khi thêm kịch bản mới (trạng thái flow mới, xử lý hậu kỳ cuối arc mới), switch-case của Router sẽ dài ra.
- Cần có các ràng buộc nghiêm ngặt: **Chỉ xử lý định tuyến, không xử lý business logic (logic nghiệp vụ)**; viết unit test cho các quy tắc ra quyết định.
- Cảnh báo về `handleSubAgentDone` ở phiên bản v0.0.1 vẫn luôn có giá trị; nhưng phương án này tránh được việc trượt thành "God object" (đối tượng toàn năng) thông qua "Hàm thuần túy + Unit test + Chỉ gọi các sự thật thuần túy".

### 6.2 Tính phức tạp của can thiệp người dùng

- Thiết kế hiện tại giao phó hoàn toàn việc Steer cho phán quyết LLM của Coordinator.
- Nhưng một số Steer có thể liên quan đến nhiều danh mục (Ví dụ: "Làm rõ vài chương đầu của nhân vật A + sau này thêm cốt truyện phụ cho anh ta").
- Cần dựa vào khả năng của LLM để chia nhỏ, và prompt cần đưa ra các hướng dẫn rõ ràng.
- **Phần này sẽ được hưởng lợi trực tiếp từ việc nâng cấp model** (So với việc hardcode Enum để phân loại InterventionAgent, phán quyết linh hoạt của LLM sẽ phù hợp hơn với kịch bản thực tế).

### 6.3 Sự phụ thuộc tiền quyết vào tính nhất quán của tầng sự thật

- Router đưa ra quyết định dựa trên Progress + Checkpoint, tầng sự thật phải đáng tin cậy.
- Một tệp duy nhất với `withWriteLock` + tmp/rename đảm bảo thay thế nguyên tử; các bước trên nhiều tệp của `commit_chapter` được khôi phục nhờ tải trọng hoàn chỉnh (complete payload) của PendingCommit, ảnh chụp (snapshot) chính văn và quá trình phát lại lũy đẳng (idempotent replay) theo từng giai đoạn; các thao tác cấu trúc thì sửa chữa chế độ xem phái sinh theo cùng một bộ tham số; tất cả đều không tự xưng là transaction nguyên tử kiểu cơ sở dữ liệu.
- Tuy nhiên, nếu tầng sự thật xuất hiện sự không nhất quán (Ví dụ: Progress thông báo chương 3 đã hoàn thành nhưng trong thư mục `chapters/` lại không có), Router sẽ đưa ra quyết định sai.
- Đề xuất: Thêm một lần **kiểm tra tính nhất quán của tầng sự thật** khi khởi động (Ví dụ: Nếu phát hiện `Progress.CompletedChapters` không khớp với thư mục `chapters/`, báo cảnh báo (warning)).

### 6.4 Coordinator vẫn giữ khả năng định tuyến bằng LLM

- Dù chỉ thị rõ ràng, LLM vẫn có thể "sáng tạo" mà không thực thi (Ví dụ: Sinh ra một đoạn chữ tư duy (thinking) rồi mới gọi công cụ).
- Bước đệm StopGuard: Nếu nhận được chỉ thị của Host mà lượt này không gọi subagent thì tiêm lời nhắc.
- Đây là biện pháp đệm lót, không phải là lệnh cấm tuyệt đối —— Đôi khi model mạnh "suy nghĩ thêm một bước" cũng không hẳn là việc xấu.

### 6.5 Yêu cầu về độ bao phủ (coverage) test tăng cao

- Flow Router là hàm thuần túy, bắt buộc phải có unit test đầy đủ (bao phủ mọi tổ hợp Phase × Flow × Boundary).
- Test tích hợp (Integration test): Mô phỏng toàn bộ chuỗi "commit → router → FollowUp → coordinator phản hồi → subagent".
- Test khôi phục sau sự cố: kill tiến trình sau đó resume, assert rằng Router suy luận ra hành động tiếp theo một cách chính xác.

---

## 7. Lộ trình triển khai

### Giai đoạn 1: Tăng cường tầng sự thật (Khoảng 0.5 ngày)

- Bổ sung bước kiểm tra tính nhất quán như ở §6.3: Quét một lần khi Khởi động/Resume, sinh ra cảnh báo.
- Đảm bảo API `store.HasArcReview(vol, arc)` và `HasArcSummary(vol, arc)` có sẵn (nếu chưa có thì thêm).

### Giai đoạn 2: Giới thiệu bộ khung Flow Router (Khoảng 1 ngày)

- Tạo mới package `internal/host/flow/`:
  - `route.go` — Hàm thuần túy `Route(state) → *NextInstruction`
  - `dispatcher.go` — Đăng ký sự kiện + Phát lệnh FollowUp
  - `route_test.go` — Unit test bao phủ mọi nhánh rẽ
- Dùng config để bật tắt thông qua cờ `flow_driven: true/false`.
- Mặc định đóng (false), chạy đối chiếu trước.

### Giai đoạn 3: Kích hoạt và xác minh (Khoảng 1 ngày)

- Bật `flow_driven: true`
- Chạy một cuốn sách 30-50 chương, đối chiếu các chỉ số:
  - Số lần gọi LLM của Coordinator
  - Số lỗi định tuyến (Phải là 0)
  - Tính phản hồi (Việc ngắt steer có bình thường không)
- Sửa lỗi, điều chỉnh quy tắc Router.

### Giai đoạn 4: Đơn giản hóa coordinator.md + Thu gọn Reminder (Khoảng 0.5 ngày)

- Sửa coordinator.md theo §3.6
- Xóa `reminder/flow.go / queue_guard.go / book_complete.go`
- Giữ lại foundation reminder cần thiết
- Cập nhật StopGuard của subagent nếu cần (thường là không cần)

### Giai đoạn 5: Đơn giản hóa resume.go (Khoảng 0.5 ngày)

- Xóa hầu hết các nhánh trong `buildResumePrompt`
- Thay thế bằng thông báo ngắn gọn chung "[Khôi phục] Vui lòng chờ chỉ thị của Host"
- Sau khi Resume, Router sẽ tự suy ra thao tác cần tiếp tục.

### Giai đoạn 6: Cập nhật tài liệu kiến trúc (Khoảng 0.5 ngày)

- Sửa đổi §2 / §13 / §7 của `docs/architecture.md` theo §5
- Chuyển trạng thái của tài liệu đề xuất này thành "đã được chấp nhận", lưu trữ (archive) vào `docs/history/`

### Giai đoạn 7: Thời kỳ quan sát (2-4 tuần)

- Chạy liên tục 2-3 cuốn trường thiên (mỗi cuốn 100+ chương)
- Ghi lại mọi lỗi định tuyến (nếu có), vấn đề về phản hồi, các hành vi ngoài ý muốn của Coordinator
- Tinh chỉnh các quy tắc của Router và coordinator.md dựa trên quan sát

**Tổng cộng khoảng 4 ngày triển khai + thời kỳ quan sát**.

---

## 8. Bảng so sánh

| Khía cạnh | Kiến trúc hiện tại | Hybrid (Phương án này) | Phương án cấp tiến (Phụ lục A) |
|---|---|---|---|
| Tính ổn định | Trung bình (LLM thỉnh thoảng định tuyến sai) | **Cao** | Cao |
| Khả năng đáp ứng | Cao | **Cao** | **Thấp** (Host gọi trực tiếp SubAgent không thể ngắt được) |
| Lợi ích từ LLM | 100% | **100%** | 85% (Bỏ phương diện định tuyến) |
| Tiết kiệm Token | 0 | ~70% | ~95% |
| Góc nhìn toàn cuốn sách | Có | **Có** | Không (Mỗi SubAgent hoạt động độc lập) |
| Chi phí triển khai | - | Trung bình (Khoảng 4 ngày) | Cao (Khoảng 1 tuần + Sửa agentcore) |
| Cập nhật tài liệu | - | Nhỏ (Tinh chỉnh §2/§13) | Lớn (Viết lại nguyên tắc trong §2) |
| Cần sửa agentcore | - | Không | Có thể (Gọi trực tiếp SubAgent) |
| Độ khó khôi phục (Rollback) | - | Thấp (Có cờ tắt bật (config switch)) | Cao |

---

## 9. Các điểm cần quyết định

1. **Có chấp nhận đề xuất này (Hybrid Coordinator) không?** [ ] Chấp nhận · [ ] Chấp nhận sau khi sửa đổi · [ ] Không chấp nhận
2. Giai đoạn 3 có được đưa vào xác minh như một PR (Pull Request) độc lập trước không? [ ]
3. Việc điều chỉnh §2 / §13 của `docs/architecture.md` có được xử lý cùng lúc trong lần này không? [ ]
4. Thời lượng của giai đoạn quan sát: [ ] 2 tuần · [ ] 4 tuần · [ ] Lâu hơn

---

## Phụ lục A: Đánh giá phương án cấp tiến (Xóa bỏ hoàn toàn Coordinator)

> Phương án bản thảo đầu tiên. Đã bị hạ cấp làm tài liệu tham khảo vì sự thụt lùi của khả năng đáp ứng, tính khả thi kỹ thuật chưa rõ ràng và mất góc nhìn toàn bộ cuốn sách của Coordinator.

Cốt lõi của phương án cấp tiến: Host gọi trực tiếp `SubAgentTool.Execute`, không thông qua LLM Coordinator.

**Những vấn đề đã xác định**:

1. **Sự thụt lùi về tính đáp ứng**: `SubAgentTool.Execute` là một lệnh gọi đồng bộ (synchronous call) bị block, Steer của người dùng phải đợi SubAgent hiện tại trả về mới có thể được xử lý. Hàm `Inject` của kiến trúc hiện tại có thể ngắt lập tức.
2. **Nghi ngờ tính khả thi về mặt kỹ thuật**:
   - Host gọi trực tiếp SubAgentTool là vi phạm thông lệ sử dụng agentcore.
   - Luồng sự kiện (Các Event của `Subscribe`) có thể sẽ không nổi bong bóng (bubble up) chính xác đến observer.
   - Đường dẫn (path) gọi lại (callback) `ContextManagerFactory` / `OnMessage` của SubAgent không xác định.
   - Sẽ cần phải sửa agentcore hoặc thay đổi lớn observer.
3. **Mất góc nhìn toàn bộ cuốn sách của Coordinator**: Mỗi lần SubAgent run một cách độc lập, sẽ không còn "Người bảo vệ LLM liên tục" nữa. Việc trôi dạt phong cách, chia cắt nhân vật, v.v., trong chặng đường dài sẽ thiếu đi một lớp bảo vệ vô hình.
4. **Việc đơn giản hóa InterventionAgent quá đà**: Phương án cấp tiến dùng enum (query/modify_setting/rewrite_chapters/adjust_style/noop) để phân loại ý định người dùng, trong khi Steer thực tế có thể trải dài qua nhiều danh mục, bắt buộc schema sẽ phân loại sai.
5. **Khối lượng công việc viết lại tài liệu kiến trúc lớn**: Phải bác bỏ các nguyên tắc cốt lõi trong §2, ảnh hưởng đến 30% diễn ngôn của tài liệu.
6. **FlowDriver sẽ phình to thành God object**: Nhồi nhét toàn bộ logic định tuyến vào một vòng lặp, mỗi lần thêm kịch bản đều phải sửa, đồng hình với `handleSubAgentDone` của bản v0.0.1.

Phương án Hybrid đã tránh được 4 vấn đề đầu tiên, vấn đề thứ 5 được giảm nhẹ thành tinh chỉnh, vấn đề thứ 6 được kiểm soát nhờ "hàm thuần túy + unit test".

---

## Phụ lục B: Chi tiết triển khai các điểm ra quyết định

| Điểm ra quyết định | Vị trí hiện tại | Vị trí kiến trúc mới | Loại |
|---|---|---|---|
| Chọn nhà quy hoạch | coordinator.md dòng 26-29 | Phán quyết của LLM Coordinator (Lúc khởi động) | Phán quyết |
| Mở rộng đầu vào | coordinator.md dòng 31 | Phán quyết của LLM Coordinator (Lúc khởi động) | Phán quyết |
| Vòng lặp bổ sung quy hoạch | coordinator.md dòng 36-38 | Nhánh Phase=Premise/Outline của Host Router (trả về nil để LLM tự quyết HOẶC FollowUp architect rõ ràng) | Hỗn hợp |
| Bước tiếp theo của mỗi chương | coordinator.md dòng 46-51 + reminder/flow | **Nhánh 2d của Host Router** (FollowUp writer) | Định tuyến |
| Review cuối arc | coordinator.md dòng 78-82 | **Nhánh 2c của Host Router** (FollowUp editor/architect) | Định tuyến |
| Phân nhánh verdict | coordinator.md dòng 59-61 + công cụ save_review | Đã được code hóa ở tầng công cụ, Router chỉ đọc Flow | Định tuyến (Đã hoàn thành) |
| Can thiệp người dùng | coordinator.md dòng 67-70 | Phán quyết của LLM Coordinator (Khi nhận được tin nhắn Inject) | Phán quyết |
| Báo lỗi nhà quy hoạch và gửi lại | coordinator.md dòng 40 | Host Router phát hiện FoundationMissing không thay đổi, đếm số lần thử lại | Định tuyến |
| Tóm tắt hoàn thành sách | coordinator.md dòng 63-65 + reminder/book_complete | Host Router phát hiện Phase=Complete → FollowUp "Xuất bản tóm tắt" | Định tuyến |

---

## Phụ lục C: Vị trí tham chiếu của mã nguồn (Source Code)

- `assets/prompts/coordinator.md` — Chờ được đơn giản hóa
- `internal/host/reminder/flow.go` / `queue_guard.go` / `book_complete.go` — Chờ bị xóa
- `internal/host/reminder/subagent_guards.go` — Giữ lại
- `internal/host/reminder/stop_guard.go` — Giữ lại + Bổ sung việc kiểm tra "Phải thực thi khi nhận chỉ thị Host"
- `internal/host/resume.go` — Đơn giản hóa đáng kể
- `internal/host/observer.go` — Subscription EventToolExecEnd mới để kích hoạt Router
- `internal/host/flow/` — Package mới thêm
- `internal/tools/commit_chapter.go` dòng 220-280 — 17 field CommitResult đã hoàn thiện
- `internal/tools/save_review.go` — Ánh xạ (mapping) nguyên tử từ Editor verdict sang Flow/hàng đợi làm lại
- `internal/store/outline.go` `CheckArcBoundary` — API dữ kiện (fact) của ranh giới arc
