# Kiến trúc thời gian chạy ainovel-cli (Runtime Architecture)

> Tầng sự kiện xác định, tầng ngữ nghĩa tự chủ: Một Engine mang tính xác định nối tiếp, ba Worker tự chủ, một vài hàm Arbiter theo yêu cầu, một tầng sự kiện hệ thống tập tin (file system fact layer).
>
> 2026-07-12 Hoàn tất thay thế mặt điều khiển: Vòng lặp dài của Coordinator LLM đã nghỉ hưu, được tiếp quản bởi Engine (vòng lặp xác định) + Arbiter (hàm phán quyết ngữ nghĩa). Quyết định thiết kế và biên bản xét duyệt xem `docs/engine-arbiter.md`, RFC xem `docs/engine-rfc.md`.

---

## 1. Mục tiêu (Theo mức ưu tiên)

1. **Độ ổn định**: Nhập một câu, viết ổn định trọn vẹn cả quyển tiểu thuyết (200~500 chương). Giữa chừng không tự ý ngắt quãng do vấn đề kiến trúc.
2. **Chất lượng có thể lặp lại**: prompt / tài liệu tham khảo / chiều đánh giá / chiến lược ngữ cảnh có thể điều chỉnh độc lập, không liên lụy đến kiến trúc.
3. **Có thể khôi phục**: Sau khi crash (sự cố), đứt mạng, tạm dừng thì có thể tiếp tục từ checkpoint gần nhất.
4. **Có thể quan sát**: Tiến độ, sản phẩm, thời gian tiêu tốn của mỗi bước mỗi chương đều có thể tra cứu.

"Ổn định" là tiền đề, "Chất lượng" là tầng trên. Mọi quyết định kiến trúc ưu tiên phục vụ độ ổn định.

---

## 2. Nguyên tắc cốt lõi

### 2.1 Quy tắc chia ba: Đưa quyết định về đúng bản chất

- **Chuyển đổi trạng thái có thể liệt kê → Mã nguồn**. "Viết xong một chương thì phái ai" là đọc sự kiện để tra bảng: `flow.Route` là hàm thuần túy + kiểm thử đặc tả vét cạn hàng vạn tổ hợp, tỷ lệ lỗi tiệm cận 0, không tốn LLM.
- **Phán quyết ngữ nghĩa có ranh giới rõ ràng → Hàm LLM (Arbiter)**. Chọn nhà quy hoạch (architect), phân loại can thiệp của người dùng, tìm lối ra cho thất bại/bế tắc: Sự kiện đầu vào, quyết định có cấu trúc đầu ra, bảo vệ bằng kiểm tra cơ học, mỗi lần phán quyết đều ghi xuống đĩa có thể phát lại.
- **Sáng tác mở → Vòng lặp LLM (Worker)**. Trong một chương, một lần đọc kiểm, một lần quy hoạch, architect/writer/editor hoàn toàn tự chủ.

Sự đối xứng giữa hai mặt phẳng là kỷ luật xuyên suốt —— bất kỳ điểm quyết định mới nào trong tương lai cũng đi theo hình thái này, không phát minh ra mô hình mới:

```text
Mặt phẳng xác định: flow.LoadState   → flow.Route     → Instruction   (Kiểm thử đặc tả vét cạn)
Mặt phẳng ngữ nghĩa: arbiter.Collect* → arbiter.Decide* → XxxDecision   (decisions.jsonl + đánh giá hồi quy)
              └── Thu thập sự kiện (IO) ──┘└── Cốt lõi (có thể phát lại) ──┘└── Engine thực thi ──┘
```

### 2.2 Công cụ là giao diện duy nhất của tầng sự kiện

Tất cả các tương tác với hệ thống tập tin, Progress, Checkpoint đều do công cụ hoàn thành. Một tập tin sử dụng thay thế nguyên tử `temp + fsync + rename`; ghi tuần tự qua nhiều tập tin không mạo danh giao dịch cơ sở dữ liệu: đệ trình (commit) chương sử dụng `PendingCommit` Saga được lưu trữ bền bỉ, ghi cấu trúc sử dụng phát lại lũy đẳng xác định và hiển thị rõ ràng thất bại. Mỗi bước đều bắt buộc kiểm tra lỗi; chỉ có quy trình đã lưu trữ ý định khôi phục mới dám cam kết khôi phục theo nguyên vẹn khối dữ liệu sau khi khởi động lại.

### 2.3 Tầng quan sát chỉ quan sát

UI, chẩn đoán, nhật ký sự kiện đều là những consumer (người tiêu dùng) thụ động được phóng chiếu từ luồng sự kiện / công cụ (artifact) chỉ đọc. Đọc sự kiện, không sinh ra sự kiện, không ảnh hưởng đến luồng điều khiển.

Dữ liệu quan sát được chia thành 3 tầng nghiêm ngặt: `agentcore.ProgressPayload` là tầng truyền tải, văn bản lỗi phải đầy đủ và không được chứa chiến lược cắt cụt của UI; `host.Event.Summary` là ngữ nghĩa hiển thị ngắn, `Detail` là chẩn đoán đầy đủ; nhật ký tập tin ưu tiên ghi toàn bộ `Detail`, TUI chỉ đọc `Summary` và khi render cuối cùng sẽ cắt cụt theo chiều rộng terminal. File logger do `Host` nắm giữ: trước tiên lấy hợp đồng độc chiếm thư mục tiểu thuyết, sau đó thiết lập phiên (session) nhật ký, rồi mới lắp ráp Store, model và Engine; điều này vừa không bỏ qua tính độc quyền của mỗi cuốn sách, vừa bao phủ toàn bộ việc lắp ráp và đóng nhật ký. Cái gọi là "nhật ký đầy đủ" có nghĩa là chuỗi lỗi, tham số không hợp lệ ban đầu và siêu dữ liệu vòng đời không bị mất, chứ không phải là sao chép lại chính văn tiểu thuyết đã sinh ra thành công vào `tui.log`; nội dung lớn vẫn do công cụ Store và `meta/sessions` đảm nhận.

**`internal/diag` là hệ thống con duy nhất về khả năng quan sát của engine** —— cơ sở hạ tầng hạng nhất, nhưng không phải là cốt lõi của sản phẩm. Nó đọc chéo qua hầu như tất cả công cụ + session + log + checkpoint, gánh vác hai chức năng: ① **Chẩn đoán chất lượng sáng tác** (Quy tắc → Finding, báo cáo trên màn hình `/diag`); ② **Khắc phục sự cố runtime + xuất ẩn danh** (Bóc tách bộ khung hành vi khỏi chính văn + tổng hợp vòng lặp → phủ sóng thành `meta/diag-export.md`).

**Kỷ luật của người quan sát (không được nới lỏng)**: diag có thể chẩn đoán, có thể đề xuất, nhưng **tuyệt đối không tự mình ra tay** —— không tự động sửa chữa, không tiếp tục chạy, không thay đổi quy trình (Bài học lịch sử xem §10 mục 5).

### 2.4 Tầng sự kiện phẳng

Chỉ có ba loại sự kiện:

- **Progress** — Chỉ số tiến độ (viết đến chương mấy, danh sách chờ viết lại)
- **Checkpoint** — Kỷ lục đẩy tiến cấp step (plan / draft / commit / review / arc_summary)
- **Artifact** — Các sản phẩm như chính văn chương, đại cương, nhân vật, tóm tắt...

Không đưa vào các trừu tượng như WorkflowInstance / TaskInstance / Command. Các sự kiện phụ trợ (bể phản hồi đại cương, hồ sơ vi phạm cơ học, kiểm toán phán quyết) cũng là jsonl phẳng, mỗi cái có duy nhất một producer (người sản xuất) và consumer.

### 2.5 Bốn kỷ luật thép

**Kỷ luật thép 1: Công cụ chỉ trả về sự kiện, không trả về chỉ lệnh điều phối chéo**. `commit_chapter` trả về các trường có cấu trúc như `arc_end` / `needs_expansion`; không kèm theo chuỗi chỉ lệnh loại `[Hệ thống]`. Trường `next_step` bên trong subagent là chỉ dẫn nội tuyến dạng trần thuật sự kiện ("Tôi vừa lưu plan, bước tiếp theo là draft"), không tính là vi phạm —— xem §6.3.

**Kỷ luật thép 2: Định tuyến quy trình do Flow Router đảm nhiệm, thực thi do Engine đảm nhiệm**. `Route(state) → *Instruction` của `internal/flow/router.go` là một hàm thuần túy (được chốt chặt bằng kiểm thử đặc tả vét cạn hàng vạn tổ hợp); Engine mỗi vòng lấy sự kiện từ store, Route suy diễn chỉ lệnh, **trực tiếp chạy Worker một cách có lập trình** (`subagent.Runner.Run`, tham số/kết quả/chuỗi lỗi được định kiểu), không có tầng chuyển tiếp công cụ LLM. Trả về nil báo hiệu cảnh ngữ nghĩa (thu dọn hoàn bản/chờ can thiệp) hoặc dừng máy tự nhiên. **Bế tắc có ranh giới rõ ràng** (RFC §5): Sau vòng trước Route vẫn sinh ra cùng `Agent+Task`, nghĩa là điều kiện hậu đề của việc định tuyến chưa được thỏa mãn; hỏi Arbiter 3 lần, ngắt mạch cứng tạm dừng 5 lần. Các checkpoint trung gian bên trong Worker không reset (đặt lại) bộ đếm, Engine mang tính xác định không cho phép chạy không tải vô hạn.

**Kỷ luật thép 3: Phán quyết ngữ nghĩa đi qua Arbiter, mỗi lần phán quyết đều ghi xuống đĩa**. Khởi động chọn nhà quy hoạch, phân loại can thiệp của người dùng, lối thoát cho thất bại/bế tắc đều do các hàm Decide theo từng cảnh (scene) của `internal/arbiter` phán quyết: sự kiện đi vào, quyết định có cấu trúc đi ra, bảo vệ bằng kiểm tra cơ học, kiểm toán decisions.jsonl (có thể phát lại ngoại tuyến). Ba Worker giữ nguyên `CheckpointDeltaGuard` của riêng chúng (hàng rào sự kiện: sản phẩm chưa ghi xuống đĩa thì không được kết thúc công việc).

**Kỷ luật thép 4: Ranh giới mã hóa cứng, không mã hóa cứng các phán quyết ngữ nghĩa không thể liệt kê**. Code chỉ cố định các bất biến có thể chứng minh được (quyền hạn, giai đoạn, thứ tự, tính lũy đẳng, tính toàn vẹn của cấu trúc) và cung cấp cho model toàn bộ sự kiện và không gian thao tác đủ lớn; sự đánh đổi trong sáng tác, phán đoán chất lượng, kế hoạch làm sao để thích ứng với chính văn và các câu hỏi mở khác phải để lại cho Worker / Arbiter. Cấm dùng từ khóa, ngưỡng điểm, làm chệch hướng liệt kê hoặc bảng quy tắc để thay thế khả năng hiểu của model, cũng cấm thu hẹp không gian quyết định hợp pháp của model vì lo sợ model mắc sai lầm. Trước khi thêm quy tắc code mới, trước tiên phải chứng minh không gian quyết định là đóng và kết quả có thể xác minh bằng máy móc; nếu không, nên cải thiện ngữ cảnh và khả năng biểu đạt của công cụ, để những lợi ích từ việc nâng cấp model có thể được hiện thực hóa mà không cần thay đổi lớp vỏ (shell).

---

## 3. Bức tranh toàn cảnh kiến trúc

```text
[Entry: TUI / headless]
        │ prompt / steer (chỉ đạo)
[Lớp vỏ Host]
   ├── observer            Tiếp sức tiến độ của Worker + Engine phát đi sự kiện → Phóng chiếu UI/Nhật ký
   ├── engine              Vòng lặp xác định: LoadState → Route → Kiểm tra trước → Chạy Worker → Ranh giới lính gác
   ├── Luồng can thiệp     Steer/Continue → Arbiter phán quyết → Thực thi hành động (tức thời/đệ trình ranh giới)
   └── usage / ngân sách / điểm dừng / quản lý model
        │ Gọi có lập trình subagent.Runner.Run (Tiến độ được tiếp sức qua ctx ToolProgress)
[architect_short/long · writer · editor] (mỗi cái có run + context + model độc lập)
        │ Gọi công cụ
[Tools]  novel_context · read_chapter · plan_chapter · draft_chapter · edit_chapter
         check_consistency · commit_chapter · save_review · save_arc_summary
         save_volume_summary · save_foundation
        │ Nguyên tử tệp đơn + phát lại lũy đẳng (commit sử dụng Saga bền vững)
[Store: Hệ thống tập tin (tmp + rename)]
   Progress · Checkpoints · Outline · Drafts · Summaries · Characters · World
   · Signals · Decisions(kiểm toán phán quyết) · Bể phản hồi · Hồ sơ vi phạm
```

| Tầng | Làm gì | Không làm gì |
|---|---|---|
| Entry | Hiển thị, nhận đầu vào | Quyết định nghiệp vụ |
| Host/Engine | Vòng đời, thực thi Route, chạy Worker, ranh giới lính gác, điều phối can thiệp | Đánh giá văn học; ghi sự kiện sáng tác (hành động trạng thái điều khiển đi qua lõi công cụ) |
| Arbiter | Phán quyết ngữ nghĩa (Quyết định có cấu trúc) | Tự mình sáng tác; thực thi hành động |
| Workers | Suy nghĩ, sáng tác, đọc kiểm | Đọc ghi Store trực tiếp (phải đi qua công cụ) |
| Tools | IO nguyên tử tệp đơn + lỗi rõ ràng + lũy đẳng; commit sử dụng Saga | Chỉ lệnh điều phối qua các subagent |
| Store | Ghi xuống đĩa hệ thống tập tin | Logic nghiệp vụ |

Phụ thuộc một chiều: `entry → host → agents/arbiter → tools → store → domain`; `flow` là package chiến lược thượng tầng (trên store, dưới host). Độc lập theo chiều ngang: `errs/` có thể được bất kỳ tầng nào tham chiếu, `diag/` đăng ký (subscribe) luồng sự kiện của host + chỉ đọc `store/`.

---

## 4. Mô hình dữ liệu

### 4.1 BookMetadata và Progress

`BookMetadata` là nguồn sự thật duy nhất về tên sách và tóm tắt (synopsis) hướng tới độc giả, được lưu bền vững vào `meta/book.json`; `book.md` chỉ là phóng chiếu dễ đọc. Premise không lưu lại tên sách, Progress cũng không mang thông tin tác phẩm.

```go
type BookMetadata struct {
    Title    string
    Synopsis string
}
```

Progress (`internal/domain/runtime.go`) chỉ ghi lại trạng thái đang chạy:

```go
type Progress struct {
    Phase             Phase           // init / premise / outline / writing / complete
    CurrentChapter    int
    TotalChapters     int
    CompletedChapters []int
    TotalWordCount    int
    ChapterWordCounts map[int]int
    InProgressChapter int             // Chương đang sáng tác
    Flow              FlowState       // writing / reviewing / rewriting / polishing / steering
    PendingRewrites   []int
    StrandHistory     []string        // Chuỗi dominant_strand
    HookHistory       []string        // Chuỗi hook_type
    CurrentVolume, CurrentArc int     // Phân tầng truyện dài
    Layered           bool
}
```

Logic điều khiển chỉ đọc các trường sự kiện trên, không phụ thuộc vào bất kỳ "dấu thời gian cập nhật" nào —— Thông tin thời gian do `OccurredAt` của checkpoint đảm nhận.

RunMeta (`meta/run.json`) đảm nhiệm **ý định chạy của người dùng** (không phải sự kiện sáng tác): PlanningTier, PlanStart (cố định phán quyết khởi động, cơ sở duy nhất để khôi phục khi sập trong giai đoạn quy hoạch), PendingSteer (bảo vệ khi sập lúc can thiệp, khe cắm duy nhất đang trên đường xử lý), AdvanceMode / AdvancePermitChapter (Chính sách nghiệm thu từng chương và giấy phép chương chính xác), AdvanceHold (Tạm dừng một lần do can thiệp ký nhận). `RunMeta.Init` giữ nguyên toàn bộ các trường ý định khi khởi động lại.

### 4.2 Checkpoint (`internal/domain/checkpoint.go`)

```go
type Scope      struct { Kind ScopeKind; Chapter, Volume, Arc int }
type Checkpoint struct {
    Seq        int64       // Tăng dần đơn điệu
    Scope      Scope       // chapter / arc / volume / global
    Step       string      // plan / draft / commit / review / arc_summary / ...
    Artifact   string
    Digest     string
    OccurredAt time.Time
}
```

Lưu trữ: `meta/checkpoints.jsonl`, chỉ ghi thêm (append-only). Việc ghi lặp lại cùng một `Scope+Step+Digest` được coi là lũy đẳng và không sinh ra dòng mới.

### 4.3 Artifact và các sự kiện phụ trợ

Artifact nằm trong `store/outline.go` `drafts.go` `summaries.go` `characters.go` `world.go`.

- **Signals**: `PendingCommit` (khôi phục gián đoạn khi commit). Đọc lúc khởi động/khôi phục, không đọc trong lúc chạy (runtime).
- **Decisions** (`meta/decisions.jsonl`): Kỷ lục kiểm toán của mỗi lần phán quyết Arbiter (facts+input+decision), có thể phát lại ngoại tuyến; **không phải là nguồn dữ liệu khôi phục** (Khôi phục chỉ phụ thuộc vào Progress/Checkpoint/RunMeta).
- **Sự kiện thế giới dạng tăng trưởng (Growth world facts)**: Dòng thời gian và sự thay đổi trạng thái nhân vật lần lượt được ghi thêm vào `timeline.jsonl` và `meta/state_changes.jsonl`; duy trì chỉ mục chống trùng lặp bên trong tiến trình, commit bình thường chỉ ghi phần delta của chương hiện tại. Các mảng JSON cũ sẽ được di chuyển theo giao thức lũy đẳng "ghi nhật ký mới dưới dạng nguyên tử trước, xóa tệp cũ sau" vào lần ghi tiếp theo, `timeline.md` là phóng chiếu có thể tái tạo cho con người đọc.
- **Bể phản hồi đại cương** (`meta/outline_feedback.jsonl`): Các phản hồi thông thường của writer được tiêu thụ trong thao tác cấu trúc tiếp theo; nếu sửa đổi chính văn từ bên ngoài có ảnh hưởng đến cốt truyện, thì ưu tiên giao cho architect trước khi tiếp tục sáng tác, xử lý xong sẽ dọn rỗng.
- **Hồ sơ vi phạm cơ học** (`meta/rule_violations.jsonl`): Kết quả kiểm tra theo user_rules lúc commit, review của editor sẽ tiêu thụ qua `novel_context(chapter=N)`; đây là siêu dữ liệu chất lượng best-effort (cố gắng hết sức), không nhất quán mạnh mẽ (strongly consistent) như với cấp độ commit.

### 4.4 Đại cương phân tầng và Thu hẹp hoàn bản (Quyển kết thúc)

Quy hoạch cuốn chiếu (điểm neo compass + khung xương của quyển + mở rộng cung arc theo nhu cầu) giải quyết được vấn đề "mở và lăn", nhưng làm cho "khi nào kết thúc" chuyển từ một con số thành một phán quyết mở ở cuối mỗi quyển —— việc thu hẹp hoàn bản phải được thiết kế tường minh, nếu không sẽ xuất hiện hai loại bế tắc: Trên sổ sách viết xong nhưng không thể kết thúc (lặp vô hạn việc viết tiếp vượt quá giới hạn, đã được sửa bằng cách có cấu trúc bảo vệ) và cốt truyện viết xong nhưng sổ sách không cho phép dừng (ước tính estimated_scale quá cao + rào cản hoàn bản từ chối thẳng thừng → bơm nước (câu chữ) hoặc ngắt mạch).

**Quyển kết thúc là khái niệm hạng nhất của việc thu hẹp**, Hoàn bản = Một lần phán quyết hướng đi + Một đoạn trượt xác định:

- **Tuyên bố (Phán quyết ngữ nghĩa LLM)**: Architect chọn 1 trong 3 ở cuối tập —— append_volume (tiếp tục) / append_volume mang `"final": true` (Quyển kết thúc) / complete_book (điều kiện hiện tại thỏa mãn tất cả). Trong phán đoán kết thúc, estimated_scale là **bằng chứng, không phải quyền phủ quyết**.
- **Thực thi (Tra bảng sự kiện bằng code)**: Sự kiện kết thúc = `domain.FinaleVolume`. Cấu trúc của quyển cuối viết xong (`layeredStructurallyComplete`) **VÀ đủ bộ ba thu dọn cuối tập (review arc/tóm tắt arc/tóm tắt tập)** thì tự động MarkComplete —— Việc kết thúc không cướp đường chạy trước rào cản chất lượng của editor. Sách chưa tuyên bố thì vẫn đi theo `layeredBookComplete` cấp độ chất lượng (chi tiết gieo mầm + đường dài đưa về 0).
- **Hủy bỏ (Suy diễn dữ liệu, không có công cụ hoàn tác)**: Sau khi tuyên bố lại thêm quyển mới chưa đánh dấu → Trạng thái thu hẹp giải trừ tự nhiên. Trạng thái luôn có thể suy ra từ layered_outline.
- **Phân phát phán đoán hoàn bản**: Cuối quyển do Route nhánh 10 phân phát architect_long đi theo danh sách kiểm tra phán đoán hoàn bản —— Quyền phán quyết hoàn bản thuộc về Architect (Một Worker), không nằm ở mặt điều khiển.

---

## 5. Đặc tả công cụ (Tools)

Công cụ là điểm tương tác duy nhất giữa tầng sự kiện và Agent.

### 5.1 Các công cụ loại đọc

`novel_context(scope)` / `read_chapter(n)` —— Bất kỳ lúc nào cũng có thể gọi, không phụ thuộc vào trạng thái tiền đề, trả về dữ liệu đủ để LLM quyết định độc lập. `novel_context(chapter=N)` tiêm thêm (inject) các vi phạm cơ học của chương đó (nếu có); nhánh architect tiêm thêm các tập đã hoàn thành/tóm tắt arc của tập hiện tại, bản chụp (snapshot) nhân vật, bể phản hồi đại cương và trạng thái foundation. Khi mở rộng arc (expand_arc), nội dung đã xảy ra là sự kiện, khung xương chỉ là kế hoạch; Architect có thể sửa đổi đồng bộ title/goal của arc mục tiêu và mở rộng các chương trong `expand_arc`.

### 5.2 Các công cụ loại ghi (Nguyên tử tệp đơn + Ngữ nghĩa khôi phục phân cấp)

Ghi tệp đơn là nguyên tử; các bước qua nhiều tệp không cam kết tính nguyên tử như cơ sở dữ liệu. Commit thông thường và commit làm lại của `commit_chapter` dùng chung `PendingCommit`, tiến hành theo trình tự "Ý định nguyên vẹn → artifact/trạng thái → Progress → checkpoint → Dọn ý định"; việc khôi phục chỉ sử dụng payload đã chuẩn hóa ghi đĩa lần đầu và bản chụp chính văn, cấm sử dụng các tham số do model sinh lại hoặc draft bị ghi đè sau khi khởi động lại. Các thao tác cấu trúc như `expand_arc` / `append_volume` không có ý định lưu bền vững, chỉ cam kết phát lại lũy đẳng cho cùng một tham số, sửa lỗi view phái sinh và trả về lỗi rõ ràng.

| Công cụ | Artifact | Step |
|---|---|---|
| `save_book` | meta/book.json + book.md | book |
| `plan_chapter` | drafts/chXX.plan.json | plan |
| `draft_chapter` | drafts/chXX.draft.md | draft |
| `edit_chapter` | drafts/chXX.draft.md | edit |
| `check_consistency` | Không có (chỉ đọc, trả về inline) | consistency_check |
| `commit_chapter` | chapters/chXX.md + Progress (+ Bể phản hồi/Hồ sơ vi phạm best-effort) | commit |
| `save_review` | reviews/chXX.json (global là chXX-global.json) | review |
| `save_arc_summary` | summaries/arc-vNNaNN.json | arc_summary |
| `save_volume_summary` | summaries/vol-vNN.json | volume_summary |
| `save_foundation` | foundation/*.json (thành công ở expand_arc/append_volume/update_compass thì tiêu thụ bể phản hồi) | premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book |

`commit_chapter` đảm nhận kiểm tra độ hoàn thành của arc/tập/toàn bộ sách, trả về sự kiện có cấu trúc; `save_review` không phán quyết các ngưỡng văn học, chỉ xác minh các sự kiện đọc kiểm và ánh xạ nguyên tử phán quyết (verdict) mà Editor đưa ra thành Flow và hàng đợi làm lại (rewrites).

`edit_chapter` là vỏ bọc mỏng của `agentcore.EditTool`, chỉ cho phép chỉnh sửa các chương đã hoàn thành và nằm trong `PendingRewrites`; bản thảo (draft) chương mới phải ghi đè nguyên chương thông qua `draft_chapter(mode="write")`.

### 5.3 Phân tầng lỗi

| Loại lỗi | Tầng xử lý | Hành động |
|---|---|---|
| Timeout mạng / EOF dạng luồng (stream) | Tools | Thử lại 3 lần |
| provider 429/503 | litellm | Chuyển lỗi (failover) sang provider dự phòng |
| Xác thực / Model không tồn tại | Tools | Ném lên terminal |
| Thiếu artifact tiền đề | Tools | Ném lỗi conflict (xung đột), LLM gọi `novel_context` xong thử lại |
| Tham số công cụ không hợp lệ | Tools | Ném lỗi validation (xác thực), LLM đổi tham số |
| retryable (stream-idle, v.v.) | Tầng subagent | MaxRetries=7 thử lại tại chỗ, không thoát ra khỏi Worker |
| Worker thất bại (nâng cấp guard/hard_stop, v.v.) | Engine | Lỗi xác định sẽ tạm dừng trực tiếp; Các lỗi khác sẽ thử lại cùng một chỉ lệnh 1 lần → Arbiter phán quyết retry/reroute/abort |
| Bế tắc (deadlock - cùng một chỉ lệnh định tuyến tái xuất hiện liên tục) | Engine | Hỏi Arbiter 3 lần, ngắt mạch cứng tạm dừng 5 lần |
| Không phản hồi dạng luồng / Suy nghĩ lâu | litellm (`StreamIdleTimeout=5min`) | Watchdog kích hoạt thử lại |

### 5.4 Tính lũy đẳng

Mỗi công cụ loại ghi trước khi thực thi sẽ kiểm tra checkpoint trước: Nếu `Step+Digest` của checkpoint mới nhất trong scope hiện tại giống hệt với lần này, trực tiếp trả về sản phẩm đã có. Việc thử lại và phân phát lại sau khi khôi phục sự cố đều an toàn —— Đây cũng là nền tảng để Engine phục hồi model (đọc store và chạy tiếp) thành lập.

---

## 6. Lắp ráp Worker (Worker Assembly)

> Về lý thuyết, có thể dùng một Prompt siêu lớn + một Agent duy nhất để chạy hết một cuốn sách, nhưng 3 điều sau sẽ làm nghẽn tính ổn định: **Bùng nổ ngữ cảnh** (200 chương dù có nén mạnh đến đâu cũng sẽ suy thoái), **Nhiễu nhiệm vụ** (Sự chặt chẽ của quy hoạch / Trí tưởng tượng khi viết / Tính phản biện lúc review pha loãng lẫn nhau trong cùng một prompt), **Mất đi cổ tức từ tính dị thể của model** (Quy hoạch/Viết/Review chọn model độc lập là không gian tối ưu đáng kể về chi phí/chất lượng). Topology đa Worker vì thế mà cần thiết.

### 6.1 Lắp ráp và Vận hành

`agents.BuildWorkers` (`internal/agents/build.go`) lắp ráp ba loại Worker thành một `subagent.Runner`: Engine trực tiếp gọi `Run(agent, task)`, mỗi lần gọi là một `agentcore.AgentLoop` hoàn chỉnh (context độc lập, model độc lập, cơ chế thử lại độc lập). Toàn bộ quá trình lắp ráp có hiệu lực ngay trong một lần: model cấp nhân vật + failover, prompt cache key (tự tăng #seq sau mỗi lần spawn), ThinkingLevel, UsageRecorder/SessionLogger (OnMessage), ContextManagerFactory của Writer (cửa sổ tự động tái tạo khi chuyển đổi /model), RestorePack, StopGuardFactory, StopAfterTools.

Việc tiếp sức tiến độ của Worker đi qua **hàm callback ToolProgress của ctx**: Engine gọi `Runner.Run` bằng `agentcore.WithToolProgress(ctx, relay)`, các sự kiện gọi công cụ/chính văn dạng luồng/thinking/retry/context của subagent sẽ đi qua relay vào observer —— cùng một hình thái ProgressPayload như thời Coordinator, tầng quan sát (observer) được tái sử dụng.

```text
Engine ── Runner.Run(agent, task) ──▶ architect_short/long · writer · editor
                                          │ Gọi công cụ
                                        Store (Môi giới hiệp đồng, các Worker không giao tiếp trực tiếp với nhau)
```

`bootstrap.ModelSet` hỗ trợ model cấp nhân vật: architect/writer/editor mỗi bên có cấu hình riêng + provider failover. Writer chạy Sonnet thay vì Opus trên tác phẩm 200 chương có thể tiết kiệm chi phí một bậc cường độ. Arbiter thống nhất sử dụng model Default (tính phí qua usageTrackedModel), hiện tại không mở cấu hình nhân vật độc lập.

### 6.2 Ba chế độ hiệp đồng

Các Worker không giao tiếp trực tiếp với nhau, mọi luồng thông tin đi qua các công cụ có cấu trúc trong Store:

**Chế độ A · Chuyển giao nối tiếp (Xương sống)**: Route phái Architect quy hoạch → Writer viết chương 1..N → Editor đọc kiểm ở cuối arc → Writer viết lại. Mỗi bước "tiếp theo phái ai" do Route suy diễn từ các sự kiện.

**Chế độ B · Vòng lặp phản hồi**: Writer báo cáo chệch hướng đại cương trong commit → Bể phản hồi ghi xuống đĩa (chỉ dành cho sách phân tầng) → Architect trong lần thao tác cấu trúc tiếp theo sẽ tham khảo qua novel_context → Thao tác thành công sẽ tiêu thụ và làm rỗng. Writer không trực tiếp gọi Architect, phản hồi được luân chuyển qua tầng sự kiện.

**Chế độ C · Mở rộng khung xương (Quy hoạch cuốn chiếu)**: Sau khi commit, sự kiện cho thấy cung arc tiếp theo vẫn là khung xương → Route (hoặc precheck của Engine) phái architect_long để mở rộng → Writer tiếp tục. Khả năng "quy hoạch cuốn chiếu" của truyện dài chính là vòng lặp khép kín này.

### 6.3 Ràng buộc bằng mã nguồn cho luồng Worker (Không dựa vào nạng prompt)

> Lúc đầu quy trình của writer dựa vào ràng buộc "tiến hành nghiêm ngặt theo trình tự sau" trong `writer.md`. LLM thường xuyên vi phạm —— bỏ qua plan để đi thẳng vào draft, chỉ viết chính văn vào cửa sổ chat mà không lưu file. **Ràng buộc quy trình bằng prompt không ổn định**, model nâng cấp có thể lại khiến nó "sáng tạo trong việc không tuân thủ".

Bốn tầng ràng buộc bằng mã (có hiệu lực đồng thời):

| Tầng | Vị trí đặt | Tác dụng |
|---|---|---|
| `StopAfterTools` / `StopAfterToolResult` | `agents/build.go` SubAgentConfig | Công cụ then chốt thành công thì thoát khỏi run của Worker (lúc thoát trạng thái cuối vẫn tham khảo StopGuard, xem hợp đồng kiểm thử). Writer hit `commit_chapter` thì dừng; Editor dùng `save_review`/`save_arc_summary`/`save_volume_summary`, Architect thu dọn arc/quyển dùng `StopAfterToolResult` |
| `CheckpointDeltaGuard` | `agents/guard/subagent_guards.go` | Lấy baseline checkpoint làm ranh giới, trước khi kết thúc vòng này phải thấy checkpoint mới của step tương ứng, nếu không sẽ từ chối `end_turn`; cản 3 lần liên tiếp thì nâng cấp terminate (Bọc lót chống chết vòng cho model yếu). Guard của Editor nhận biết task: Khi được phái tạo tóm tắt, chỉ phúc khảo (review) sẽ không được tính là xong |
| Sự kiện gợi ý `next_step` nội tuyến | Các trường trả về của công cụ | Mỗi sự kiện đều mang theo "đề xuất bước tiếp theo", LLM nhìn thấy sự kiện là biết phải làm gì tiếp |
| Quy kết nội tuyến/Kiểm tra tiền đề trong công cụ | `edit_chapter` `commit_chapter` v.v. | Cản phá vật lý ở tầng dữ liệu: Sửa draft trực tiếp, sửa chương đã hoàn thành mà chưa xếp hàng, commit rỗng đều bị cản, `ConcurrencySafe=false` ngăn cản tình trạng đua tranh (race condition) do song song |

writer.md chỉ đảm nhiệm: Giao thức thực thi, nhận thức về việc chạy tiếp từ điểm ngắt (breakpoint), diễn giải hợp đồng chương; Tiêu chuẩn viết lách nằm ở tầng văn phong (placeholder `{{VOICE}}` được điền ngược vào, người dùng có thể ghi đè, xem `docs/voice-layer.md`). **Đây chính là tiền đề để dám mở tầng văn phong cho người dùng: Các bất biến nằm ở tầng công cụ, prompt có sửa lung tung cũng không làm hỏng máy trạng thái.**

### 6.4 Phụ thuộc agentcore

`../agentcore` là thư viện Agent đa dụng nội bộ của dự án này (liên kết qua go.work). Các primitive (nguyên thủy) mà Engine sử dụng: `subagent.Runner.Run` (gọi trực tiếp có lập trình, kết quả được định kiểu và chuỗi lỗi —— phân loại như `errors.Is(err, subagent.ErrUnknownAgent)` không phụ thuộc vào văn bản lỗi), `ToolProgress` của ctx (tiếp sức sự kiện), `subagent.Config`, `StopGuard`/`StopAfterTools`. `subagent.Tool` chỉ dùng cho host cần đưa Runner cho model sử dụng qua `Runner.AsTool()`, AINovel không đi qua tầng này.

**Ranh giới sửa đổi**: Có thể đưa vào agentcore —— chiến lược ContextManager mới, bộ chuyển đổi provider mới, loại sự kiện mới; Không đưa vào agentcore —— model nghiệp vụ và công cụ nghiệp vụ. Tiêu chí phán đoán: Giả sử agentcore tương lai sẽ được sử dụng bởi coding agent / customer service agent, nếu khả năng mới đó vẫn có ý nghĩa trong ngữ cảnh đó thì mới được phép thêm vào. **Cấm viết các bản vá bọc lót (workaround) ở tầng ứng dụng** —— thiếu khả năng thì trực tiếp sửa từ phía thượng nguồn (upstream).

**Kiểm thử hợp đồng (Contract tests)** (`internal/agents/agentcore_contract_test.go`, 6 test case, toàn bộ được điều khiển qua `Runner.Run`): Ghim chặt các hành vi framework mà dự án này phụ thuộc thành các khẳng định (assertion) có thể thực thi (thoát trạng thái cuối phải hỏi StopGuard, Error/Aborted không được chạm đến guard, chuỗi lỗi Escalate có thể khớp bằng `errors.Is`, `ErrUnknownAgent` định kiểu của `Run`, tiến trình lỗi công cụ trọn vẹn và là text thuần túy). **Trước khi bump agentcore thì toàn bộ phải pass (xanh)** —— chú thích sẽ lỗi thời, kiểm thử thì không (kỷ luật này đã từng bắt được một giả thiết bị mất hiệu lực và tiết kiệm được một workaround).

### 6.5 Bộ nhớ đệm Prompt (Prompt Cache)

Đòn bẩy thứ hai cho chi phí chạy đường dài (thứ nhất là chọn model). Bản giải thích đầy đủ xem `docs/prompt-cache-design.md`. Phân công ba tầng: **litellm chỉ dịch giao thức**, **agentcore quyết định nơi đặt cache và danh tính**, **ainovel cấu hình 1 dòng là xong**.

Tiền đề của việc thu lợi từ bộ nhớ đệm là **tiền tố byte của request phải ổn định**, được đảm bảo bởi ba kỷ luật (đều ở agentcore):

1. **Tính xác định byte của tools** — Description/Schema tạo lại mỗi lần, mọi vòng lặp map đều phải sắp xếp trước
2. **Lịch sử append-only (chỉ thêm vào)** — Tin nhắn chỉ được thêm vào, không được sửa đổi; nén ngữ cảnh (context compression) là sự đánh đổi rõ ràng "chịu trả phí miss 1 lần để đổi lại cửa sổ (window)", việc phóng chiếu phải `CommitOnProject`
3. **Nội dung động đi vào phần đuôi** — Phong bì (envelope) / chỉ lệnh toàn bộ được thêm vào phần đuôi, không bao giờ ghi ngược lại các tin nhắn cũ

Cấu hình là "Một sách một gốc, Một nhân vật một tên, Một phiên một khóa (key)": Hệ OpenAI `PromptCacheKey = nvl-<Hash Sách>-<Nhân vật>#<Số thứ tự spawn>` làm tính tương thích định tuyến (affinity) (mặc định chỉ gửi cho API chính thức, qua trung gian có thể bật rõ ràng); Hệ Claude `CacheLastMessage: "ephemeral"` điểm ngắt (breakpoint) cuốn chiếu + điểm ngắt sàn (floor) system. **Đường ranh giới chốt (Latch red line)**: Mọi thứ đi vào khóa cache một khi được tính toán lần đầu trong session sẽ bị đóng băng, thà cũ (stale) còn hơn phá vỡ cache. Việc phát hiện đứt gãy (`host/usage.go noteCacheBreak`) thuần túy là quan sát chứ không sửa chữa, bộ đếm đi vào `usage.json cache_breaks` và bảng điều khiển cache trên TUI.

---

## 7. Engine và Arbiter

### 7.1 Vòng lặp Engine (`internal/host/engine.go`)

```text
for {
    Áp dụng hành động trạng thái điều khiển của can thiệp (làm rỗng; cặp hold+dispatch trước tiên thiết lập sự kiện làm lại)
    advanceGate.HandleBoundary() // Tiêu thụ hold + đối chiếu giấy phép của review
    inst := Đơn phái của can thiệp ?? Route(LoadState) ?? planStartFallback
    inst == nil → return          // Hoàn bản / Dừng máy ngữ nghĩa, đợi Continue
    precheck(inst)                // Hóa thân xác định của ToolGate cũ: Bỏ qua việc phân phát ở giai đoạn hoàn bản;
                                  // Chương mục tiêu của writer chưa được mở rộng → chuyển phái architect để mở rộng
    advanceGate.Allow(inst)       // Chỉ cản các chương mới hướng tiến chưa được cấp phép
    trackDeadlock(inst)           // Cùng một Agent+Task xuất hiện liên tiếp: Hỏi Arbiter 3 lần, ngắt mạch cứng 5 lần
    runWorker(inst)               // subagent.Runner.Run + Tiếp sức tiến độ + Sự kiện DISPATCH
    Phân loại lỗi: Lỗi xác định → tạm dừng; Lỗi đầu tiên thử lại 1 lần; Lại thất bại → Arbiter (retry/reroute/abort)
    Ranh giới chính sách: budget → advanceGate
}
```

Đơn goroutine nối tiếp (serial); `ctx` cancel = tạm dừng (checkpoint đảm bảo không mất mát). **Trạng thái điều khiển chỉ thay đổi ở ranh giới vòng lặp**: hold/reopen/dispatch của can thiệp phải xếp hàng tới ranh giới mới được đệ trình (Tổ hợp hold+dispatch sẽ thực hiện xếp hàng tạo đơn phái trước, sau đó mới cho phép Gate tiêu thụ hold); answer/rules thì thực thi tức thời. Chế độ `review` chỉ ràng buộc chương mới hướng tiến, không ngăn cản làm lại (rework), đọc kiểm, bảo trì cấu trúc và khôi phục khi commit. Arbiter trước khi thực thi đơn phái sẽ đối chiếu Expect (các trường ngữ nghĩa Phase/Flow/QueueHead; CheckpointSeq chỉ để kiểm toán chứ không đối chiếu —— khi can thiệp thì worker thường đang chạy, seq chắc chắn đổi), nếu không khớp thì vứt bỏ và gửi **đồng bộ** can thiệp gốc trở lại luồng phán quyết đầy đủ để hỏi lại.

### 7.2 Arbiter (`internal/arbiter/`)

Bốn cảnh (scene), mỗi cảnh có một cặp `Collect*Facts` (ranh giới IO) / `Decide*` (không có IO ngoại trừ gọi model do executor thống nhất quản lý, có thể phát lại ngoại tuyến) + Kiểu Decision độc quyền (hành động không khớp cảnh thì về mặt kiểu là không thể thể hiện được):

| Cảnh | Kích hoạt | Kiểu quyết định |
|---|---|---|
| `plan_start` | Khởi động sách mới | Chọn nhà quy hoạch short/long + Bổ sung nhu cầu quá ngắn |
| `intervention` | Người dùng can thiệp | Tổ hợp answer/rules/hold/reopen/dispatch (Thứ tự thực thi do Engine cố định) |
| `worker_failure` | Worker báo lỗi và các lỗi xác định không có lối thoát | retry / reroute / abort |
| `deadlock` | Cùng một chỉ lệnh không có tiến triển liên tục | retry / reroute / abort |

Luồng thất bại: Executor (người thi hành) có cấu trúc được thống nhất sẽ chọn JSON Schema nguyên sinh hoặc hợp đồng bằng prompt dựa trên khả năng; Lỗi định dạng/Schema và lỗi kiểm tra nghiệp vụ của cả hai chế độ (prompt và nguyên sinh) sẽ trả lại lý do chính xác cho model để tiếp tục sửa chữa, cho đến khi thành công hoặc kết thúc `context`, không đặt giới hạn số lần. Nếu chế độ nguyên sinh vi phạm hợp đồng, từ chối trả lời, bị cắt cụt, lỗi chấm dứt hoặc lỗi request không thể retry, sẽ lập tức trả về lỗi rõ ràng; Can thiệp không tạo ra thao tác ghi, khởi động sẽ báo lỗi rõ ràng, failure/deadlock bảo thủ tạm dừng. **Đầu ra của Arbiter cũng như đầu ra của mọi LLM đều không đáng tin** —— sau khi kiểm tra JSON Schema, `Validate` tiếp tục dùng sự kiện thực tế để kiểm tra cơ học (ràng buộc phase, reopen chỉ giới hạn ở lúc hoàn bản, chương vượt quá giới hạn). Mức sử dụng (usage) đi qua `usageTrackedModel` để tính vào ngân sách và hệ thống usage.

### 7.3 Lớp vỏ Host (`internal/host/host.go`)

Vòng đời (`StartPrepared`/`Resume`/`Continue`/`Steer`/`Abort`/`Close`), Điều phối can thiệp (FIFO nối tiếp + bảo vệ sập PendingSteer), Phóng chiếu sự kiện, Quản lý model. Kênh quan sát `Events`/`Stream`/`Done`, UI tổng hợp `Snapshot()`, Điểm mở rộng (nhập/xuất/đồng sáng tạo/phỏng viết/chuyển đổi model).

`runEnded` (callback engine.onDone) xác định trạng thái cuối dựa theo sự kiện của store: Phase=Complete → completed + tóm tắt hoàn bản xác định (không tốn lời gọi LLM); Các trường hợp khác → idle/paused. **Cấm mọi logic "tự động chạy tiếp" xuất hiện ở đây** (Bài học lịch sử §10 mục 5).

---

## 8. Khởi động, Khôi phục và Can thiệp

### 8.1 Tạo mới

```text
User: "Yêu cầu bằng một câu"
  → StartPrepared(raw)
    → Progress.Init / Checkpoints.Reset
    → StartPrompt cố định vào RunMeta (Sự kiện đầu vào được ghi lại trước khi có phán quyết)
    → Arbiter phán quyết plan_start (Chọn nhà quy hoạch + bổ sung nhu cầu) → Thất bại báo lỗi rõ ràng (kiểm toán kèm error)
    → PlanStartRecord cố định vào RunMeta (Phán quyết được ghi thành sự kiện trước, sau đó mới thực thi)
    → engine.start (Chỉ lệnh đơn phái đầu tiên)
```

Phán quyết thất bại không phải là ngõ cụt: StartPrompt đã có sẵn, bất kỳ lần khôi phục/tiếp tục nào sau này, engine sẽ phán quyết bù (xem §8.2).

### 8.2 Khôi phục (Khởi động lại sau khi sập)

```text
Khởi động tiến trình → resumeLabel (chỉ là nhãn UI) → Cảnh báo tính nhất quán → Đối chiếu AdvanceGate
  → PendingSteer tồn tại → Đi qua luồng phán quyết can thiệp đồng bộ (can thiệp có hiệu lực trước khi chạy tiếp), sau đó kéo engine lên
  → Ngược lại engine.start(nil): Chỉ khôi phục sự kiện, Route tính toán lại từ store để chạy tiếp
```

Không có session (phiên) nào cần khôi phục. Sập trong giai đoạn quy hoạch (phán quyết đã lưu đĩa, foundation đầu tiên chưa lưu) do `planStartFallback` tiếp tục phái dựa trên PlanStartRecord, không làm lại phán quyết đã có. Nếu phán quyết khởi động **chưa từng hoàn thành** (lỗi model lúc khởi động), `planStartFallback` sẽ phán quyết bù tại chỗ dựa theo StartPrompt —— đây là lần thử lại (retry) của phán quyết đầu tiên, không vi phạm nguyên tắc "khôi phục không phán quyết lại"; Phán quyết bù thất bại thì báo hiệu tạm dừng rõ ràng, không cho phép dừng máy trong im lặng. An toàn khi phân phát lặp lại được đảm bảo bởi tính lũy đẳng của công cụ (§5.4).

### 8.3 Người dùng can thiệp

`Steer`/`Continue` thống nhất đi qua luồng phán quyết của Arbiter (`doIntervention`):

```text
Lưu trữ PendingSteer bền vững (Bảo vệ khi sập) → Thu thập sự kiện (Collect facts) → Quyết định (Decide) (tính bằng giây)
  → Ghi decisions.jsonl → Echo answer (hiển thị câu trả lời) / Ghi rules tức thời
  → Xếp hàng hold/reopen/dispatch vào đệ trình ranh giới (thực thi ngay lập tức khi engine dừng và kéo engine lên tùy ý định)
  → Toàn bộ hành động thành công → Xóa PendingSteer một cách nguyên tử (ClearHandledSteer)
```

Bảo vệ khi sập (Crash protection) là **lưu trữ đơn đang trên đường (in-flight) dạng best-effort (cố gắng hết sức)**: Lần `SetPendingSteer` đầu tiên thất bại sẽ báo lỗi rõ ràng và dừng phán quyết, tuyệt đối không tiếp tục thực thi khi không có bản ghi khôi phục; Thời gian phán quyết, hành động thất bại (giữ lại để phát lại), thoát bình thường/Abort (lưu lại đơn phái còn sót lại bằng defer) đều được bảo vệ. Vẫn có hai cửa sổ rõ ràng không được đảm bảo —— Đơn phái bị giết cứng (hard kill) sau khi đã chuyển vào hàng đợi thực thi trong bộ nhớ (cấp mili-giây), và luồng nhập (input) đồng thời đang trong quá trình chờ của interMu. Người dùng có mặt tại đó có thể nhận biết được, chi phí gửi lại chỉ vài giây.

**Tầng lưu trữ bền vững cho can thiệp lâu dài**: Quy tắc văn phong/chất lượng từ hành động `rules` của phán quyết, được chuẩn hóa qua `userrules.Service` rồi đưa vào bản chụp (snapshot) quy tắc của cuốn sách này, `novel_context` tiêm `working_memory.user_rules` —— có hiệu lực xuyên suốt các lần nén (compression) và khởi động lại (xem chi tiết [Bản chụp quy tắc người dùng](user-rules-runtime.md)). Các lối ra (way out) khác vốn dĩ đã ghi vào store rồi (Độ dài/Cốt truyện → architect phái đơn, Sửa chương cũ → editor xếp hàng vào PendingRewrites, Hoàn bản làm lại → reopen).

### 8.4 Kiểm soát đẩy tiến chương

`ChapterAdvanceGate` thống nhất thực thi 2 ý định người dùng ở thang đo thời gian khác nhau:

| Ý định | Nguồn gốc | Ngữ nghĩa |
|---|---|---|
| `AdvanceMode=review` + permit chính xác | `/review on`, `/next` | Chính sách bền vững: Mỗi chương mới hướng tiến phải được thả hành (cho qua) riêng biệt |
| `AdvanceHold` | Can thiệp của Arbiter | Ý định dùng 1 lần: Tạm dừng sau khi tới ranh giới hiện tại, hoặc sau khi dọn rỗng hàng đợi làm lại, hoặc commit ổn định chương mục tiêu |

Giấy phép (permit) ràng buộc với số chương. Nó chỉ được tiêu thụ khi chương mục tiêu đã vào CompletedChapters, PendingCommit đã làm rỗng và có tồn tại commit checkpoint, vì vậy việc sập ở bất kỳ cửa sổ nào của saga commit cũng không làm cho cùng một giấy phép bị dùng cho chương tiếp theo. Các bất biến chi tiết xem [Chapter Advance Gate](chapter-advance-gate.md).

---

## 9. Cấu trúc thư mục

```text
internal/
  domain/         Dữ liệu thuần túy: Phase / FlowState / Progress / Checkpoint / Scope / Story / Plan /
                  Review / StateChange / Quy tắc chuyển đổi Phase-Flow
  store/          Lưu trữ hệ thống tập tin bền vững (tmp+rename + điều phối lũy đẳng; commit có sự kiện của giai đoạn Saga): progress / checkpoints / outline /
                  drafts / summaries / characters / world / signals / run_meta / runtime /
                  session / decisions (kiểm toán phán quyết)
  tools/          11 công cụ Agent, loại ghi là nguyên tử tệp đơn + lỗi rõ ràng + lũy đẳng; commit dùng thêm Saga bền vững
  flow/           Chiến lược định tuyến (Hàm thuần túy + Ranh giới IO): router.go (Bảng quyết định Route) + state.go (LoadState)
                  + pause.go (Phán quyết điểm dừng)
  arbiter/        Tầng phán quyết ngữ nghĩa (LLM-as-function): plan_start / intervention / failure(deadlock)
                  Cặp hàm Collect/Decide theo từng cảnh + Kiểu Decision theo từng cảnh + Kiểm tra cơ học
  agents/         build.go lắp ráp 3 Worker (subagent.Runner, Engine gọi trực tiếp có lập trình); ctxpack/ Chiến lược nén Context của Writer
    guard/        subagent_guards.go (CheckpointDeltaGuard ×3, Hàng rào sự kiện Worker)
  host/           host.go (Vòng đời/Điều phối can thiệp) + engine.go (Vòng lặp thực thi xác định) + observer*.go
                  + events.go + usage*.go + budget.go + advance_gate.go + resume.go + cocreate.go
    imp/          Nhập biên dịch ngữ nghĩa tiểu thuyết ngoại vi: ingest → segment → analyze → synthesize → publish (Suy diễn trạng thái thuần túy + LLM làm hàm)
    exp/          Xuất chương đã hoàn thành: TXT / EPUB 3; thuần chỉ đọc
  entry/          tui (Bubble Tea) / headless / startup
  bootstrap/      config + ModelSet + provider failover + Hướng dẫn setup
  eval/           Đánh giá ngoại tuyến (prompt/voice A/B, hồi quy)
  diag/ errs/ models/ notify/ rules/ userrules/ stylestat/ ...

assets/
  prompts/        arbiter-plan-start / arbiter-intervention / arbiter-failure / architect-short|long
                  / writer (Mẫu giao thức, placeholder {{VOICE}}) / editor / import-* / simulation-*
  voice.md        Tiêu chuẩn viết lách (Mặc định tích hợp ở tầng văn phong; xem 3 tầng bao phủ ở docs/voice-layer.md)
  references/     Kỹ xảo viết lách + anti-ai-tone + mẫu thể loại v.v.
  styles/         Mặc định/Kỳ ảo/Ngôn tình/Kinh dị (Người dùng có thể ghi đè/thêm mới)

../agentcore     Framework Agent đa dụng (Thư mục anh em qua go.work, có thể thêm khả năng chung, không thêm nghiệp vụ)
../litellm       LLM Gateway
```

### 9.1 Các cột mốc tiến hóa

| Thời gian | Refactor (Tái cấu trúc) | Hiệu quả ròng |
|---|---|---|
| 2026-04-10 | `internal/orchestrator/` (6342 dòng) → `host/` + `agents/` | Lõi runtime giảm 74% số dòng code |
| 2026-04-20 | Hybrid Coordinator: Tạo mới `host/flow/`, thu hồi việc định tuyến về hàm thuần túy | Tỷ lệ lỗi định tuyến tiệm cận 0 |
| 2026-05-02 | Sửa luồng stream/suy nghĩ chậm (slow thinking) của agentcore; xóa bản vá chạy tiếp `idleResumeCount` | mimo / suy nghĩ chậm dạng luồng chạy thành công |
| 2026-06-05 | Vòng lặp quy hoạch cuốn chiếu khép kín + Đẩy lùi viết tiếp bằng `/import` | Chạy thông lần đầu 200+ chương |
| 2026-07-12 | **Thay đổi mặt điều khiển Engine + Arbiter**: Coordinator vòng lặp dài và 7 bản vá sinh thái nghỉ hưu; Tầng văn phong phủ 3 lớp; Khai cố bằng 5 vòng đọc kiểm đối kháng | Tiết kiệm 1 lần chuyển tiếp LLM mỗi ranh giới; Mặt điều khiển 100% có thể test offline; Phán quyết ngữ nghĩa có thể phát lại |
| 2026-07-15 | **Đường ống biên dịch ngữ nghĩa `/import`**: Quy tắc cắt bằng hardcode nghỉ hưu, đổi sang biên dịch theo giai đoạn ingest→segment→analyze→synthesize→publish; Suy diễn trạng thái thuần túy (`NextAction(Facts)`) + Dấu vân tay đầu vào gắn liền với artifact, toàn bộ quá trình là lũy đẳng có thể khôi phục | Việc chia cắt tự nhiên mạnh lên theo khả năng model; Không bị trôi dạt liệt kê giai đoạn; Gián đoạn có thể chạy tiếp, mặt điều khiển test offline được |

Kiểm chứng thực tế: hy3-preview free 12 chương / 73 phút, mimo-v2.5-pro 10 chương / 8.4 vạn chữ, đều chạy một lần là xong; Truyện dài gpt-5.4 "Phàm Cốt" 235 chương / 1.27 triệu chữ đã chạy thông vòng lặp quy hoạch cuốn chiếu (Đây là dữ liệu thời Coordinator, dữ liệu thời Engine chờ cập nhật).

---

## 10. Những việc KIÊN QUYẾT KHÔNG LÀM

Vi phạm có nghĩa là kiến trúc đi chệch hướng.

1. **Không đưa vào khái niệm Task / Job / WorkItem**. "Nhiệm vụ hiện tại" hiển thị trên UI là hình chiếu của luồng sự kiện, không phải là sự thật (fact).
2. **Không phát minh ra bộ lập lịch thứ 2 ngoài Route**. Mọi quyết định "bước tiếp theo phái ai" bắt buộc phải đi qua bảng quyết định Route (chốt chặt bằng đặc tả vét cạn) hoặc phán quyết của Arbiter (ghi đĩa kiểm toán), không cho phép phân phát bằng if-else rải rác.
3. **Không làm cơ chế "chạy tiếp khi nhàn rỗi (idle resume)"**. Vòng lặp Engine kết thúc = Host chuyển sang trạng thái cuối; Để hệ thống chạy lại thì chỉ có người dùng gọi `Continue` hoặc khởi động lại bằng `Resume`.
4. **Không thêm răn đe hành vi vào prompt**. Nếu cần giải thích lan can bảo vệ hành vi tức là phân tầng sai rồi —— Các bất biến (invariants) phải nằm ở điều kiện tiền đề của tool, phán đoán nằm ở Arbiter, luồng quy trình nằm ở Route.
5. **Không thêm bản vá tự động chạy tiếp ở Host cho các sự cố dừng máy**. Bản vá `idleResumeCount` ngày xưa, trong lần chạy dài duy nhất mà nó được kích hoạt, đã 100% không cứu được hệ thống, ngược lại còn che lấp nguyên nhân thực sự ở tầng agentcore (xem chi tiết `feedback_no_host_resilience.md`).
6. **Không suy luận nhiệm vụ hoàn thành dựa vào "tool exec end"**. Bằng chứng duy nhất cho việc hoàn thành là ghi được checkpoint.
7. **Không làm các mô hình 4 tầng như WorkflowInstance / Command + Apply v.v**. Tầng sự kiện chỉ có Progress + Checkpoint + Artifact.
8. **Không hỗ trợ Worker chạy song song**. Một vòng lặp Engine đang hoạt động đơn lẻ, một cuốn sách tiến triển nối tiếp (serial). Muốn viết nhiều cuốn tiểu thuyết thì hãy dùng nhiều tiến trình (process).
9. **Không thực hiện lời gọi LLM ở tầng công cụ** (Trừ chính công cụ của Agent). Thuần túy là IO + Kiểm tra + Lũy đẳng.
10. **Không cho UI đọc trực tiếp từ Store**. Chỉ có thể đăng ký luồng sự kiện (subscribe to event stream) hoặc đọc `Snapshot()` của Host.
11. **Không viết máy trạng thái Flow ở phía Host**. Nhãn Flow chỉ do Tool cập nhật, Route chỉ đọc không ghi.
12. **Không viết hardcode bọc lót cho "ảo giác LLM (LLM hallucination)"**. Hãy tối ưu hóa prompt, cải thiện giá trị trả về của tool, để novel_context trình bày sự kiện một cách rõ ràng hơn.
13. **Không để diag / Tầng quan sát can thiệp vào luồng điều khiển**. Chẩn đoán chỉ là Read-Only (Chỉ đọc); tuyệt đối không làm tính năng tự động sửa / chạy tiếp / thay đổi quy trình.
14. **Ngân sách và chính sách đẩy tiến chương không được đi vào Route/Tầng công cụ**. `BudgetSentinel` / `ChapterAdvanceGate` là các thành phần chính sách nằm ở ranh giới của Engine (thực thi các chỉ lệnh đã được người dùng ký nhận trước, không đánh giá các hành vi văn học); `notify` thuần túy để quan sát.
15. **Thay đổi ở Mặt điều khiển bắt buộc phải sửa Đặc tả vét cạn trước rồi mới sửa Impl (Triển khai)**; **Trước khi bump agentcore phải pass Kiểm thử hợp đồng**.
16. **Không làm Workflow DSL đa năng, Event Sourcing, Global State Digest**. Route là 1 lĩnh vực 1 bảng, tổng quát hóa tức là thiết kế quá mức (over-design).

---

## 11. Chiến lược Xác minh

### 11.1 Danh sách tài sản kiểm thử

| Tầng | Tài sản (Assets) | Độ bao phủ |
|---|---|---|
| Đặc tả mặt điều khiển | `flow/router_exhaustive_test.go` | Vét cạn 12 vạn tổ hợp của bảng quyết định Route + Tính chất hàm thuần túy/Xác định/Bảo toàn |
| Hợp đồng Framework | `agents/agentcore_contract_test.go` | 5 giả thuyết hành vi của agentcore, được điều khiển bởi `Runner.Run` (Bắt buộc chạy trước khi nâng cấp) |
| Đầu cuối của Engine (E2E) | `host/engine_test.go` | Model giả (fake) + Công cụ thật: Viết trọn sách / Phán quyết thất bại / Phán quyết bế tắc / Chuỗi thời gian nghiệm thu làm lại / boundary hold lập tức dừng / Bảo toàn race condition lúc thoát / Một chương một giấy phép |
| Phán quyết (Arbiter) | `arbiter/arbiter_test.go` | Phân tích/Thử lại phản hồi/Ma trận kiểm tra theo từng cảnh/Thu thập sự kiện |
| Hợp đồng Ống dẫn Sự kiện | Kiểm thử của store/tools | Bể phản hồi qua các lần khởi động lại, Hồ sơ vi phạm theo quy tắc latest-wins/Làm lại bị xóa/Tiêm novel_context, PlanStart giữ lại qua Init |
| Tầng văn phong | `assets/load_test.go` | Chia tách nhất quán từng byte / Ngữ nghĩa 3 lớp bao phủ / Luồng lắp ráp eval giống nhau |
| Chất lượng ngữ nghĩa | `internal/eval` + decisions.jsonl | prompt/voice A/B, Phát lại phán quyết ngoại tuyến (Tập hồi quy đang được xây dựng) |

### 11.2 Các kịch bản ổn định

- **A. Chạy đường dài (Long Run)**: Chạy một lèo 80~200 chương, Phase=complete. Cho phép provider failover, thử lại (retry); Cấm mọi hình thức tự động chạy tiếp.
- **B. Khôi phục sau sự cố (Crash Recovery)**: Kill process sau bất kỳ step nào → Resume → Route tiếp tục chạy dựa vào sự kiện thật, không viết lại các sản phẩm đã ghi đĩa, checkpoints không có step lặp lại. Sự cố lúc đang quy hoạch sẽ đi qua PlanStartRecord.
- **C. Provider chập chờn**: 503 ngắt quãng → litellm failover, Worker không hề hay biết.
- **D. Can thiệp của người dùng**: Steer trong lúc đang chạy → Phán quyết trong tích tắc + echo, đệ trình hành động ranh giới; Steer lúc đang dừng máy → Sau khi phán quyết thì tự kéo lên theo ý định; Crash (sự cố) → PendingSteer phát lại.

### 11.3 Tính hợp quy (Có thể viết thành linter / test)

- `flow.Route` bắt buộc là hàm thuần túy: Cấm đọc Store / Bất kỳ IO nào
- Bên trong thân hàm `runEnded` không được phép xuất hiện bất kỳ lệnh gọi khởi động engine nào
- Cảnh phán quyết mới bắt buộc phải thêm từng cặp Collect/Decide + Kiểu Decision + Ghi xuống đĩa
- Mã nguồn liên quan đến khôi phục (recovery) chỉ được phép xuất hiện ở `host/resume.go` và `engine.planStartFallback`

### 11.4 Lặp lại (Iteration) để tăng chất lượng

Sửa văn phong → Sửa thư mục `<Thư mục sách>/style/` (cấp người dùng) hoặc assets/voice.md (tích hợp sẵn), tập kiểm thử văn phong A/B xác minh; Thêm chiều đọc kiểm → Sửa editor.md (save_review nhận dữ liệu có cấu trúc); Thêm tài liệu tham khảo → Đi dây rõ ràng ở 3 nơi (`tools.References` + `loadReferences` + mapping tiêm novel_context).

**Thống kê văn phong toàn sách (`internal/stylestat`)**: Host tạo một `StyleStatsIndex` duy nhất cho mỗi cuốn sách, và tiêm rõ ràng vào `novel_context` cùng `commit_chapter`. Lần khởi động đầu tiên sẽ khôi phục chỉ mục từ toàn bộ các chương đã hoàn thành, sau đó cập nhật phần gia tăng đối với các chương mới/viết lại (mô hình kiểu câu/cụm từ tần suất cao/câu lặp qua các chương/hình thái cuối chương), với cùng trạng thái sách sẽ tái sử dụng snapshot (bản chụp) và tiêm `episodic_memory.style_stats`: editor phán quyết bằng số liệu, writer dựa vào đó để tự tránh lặp lại. Đánh giá offline (eval) vẫn có thể trực tiếp gọi hàm thuần túy `Compute`. **Thống kê thuộc về mã nguồn, phán quyết thuộc về LLM**.

---

## 12. Tổng kết

> **Tầng sự kiện thì mang tính xác định, tầng ngữ nghĩa thì tự chủ.** Model được tự do ở những nơi không thể xác minh (viết cái gì, viết thế nào, đánh giá ra sao), bị ràng buộc ở những nơi có thể xác minh (thứ tự, lũy đẳng, giai đoạn).

Không có task queue (hàng đợi tác vụ), không có policy engine (động cơ chính sách), không có session (phiên) thường trú. Những gì có chỉ là:

- Một vòng lặp Engine mang tính xác định nối tiếp (~500 dòng code, chốt chặt bằng 6 đường dẫn đầu cuối E2E)
- Một Bảng quyết định Route (Hàm thuần túy, đặc tả vét cạn 12 vạn tổ hợp)
- Bốn Hàm phán quyết Arbiter (Sự kiện đầu vào, Quyết định có cấu trúc đầu ra, Ghi xuống đĩa có thể phát lại)
- Ba loại Worker chức năng (Context và Model độc lập, hàng rào sự kiện can thiệp zero)
- 11 công cụ nguyên tử tệp đơn, khôi phục lũy đẳng/báo lỗi rõ ràng qua nhiều tệp; trong đó commit dùng Saga bền vững + 1 file jsonl checkpoint

Lợi ích của việc nâng cấp model sẽ chảy về đâu, nhìn qua là rõ: Sáng tác tốt hơn (Toàn bộ đầu ra của Writer/Architect/Editor), Phán quyết chuẩn hơn (4 cảnh của Arbiter), Tóm tắt gọn hơn (ctxpack) —— Chỉ cần thay model là có tất, vỏ ngoài không cần sửa 1 dòng. Mặt điều khiển không ăn được cổ tức từ model, vì **tra bảng không cần trí tuệ**; Thứ nó cần là được chứng minh nó đúng, và điều đó đã được chứng minh rồi.

Sự cứng nhắc của quy trình là có chủ ý, được định giá, và có để chừa sẵn cửa (backdoor): Muốn thả lỏng thứ tự công cụ của writer → Nới lỏng một đoạn prompt giao thức (các bất biến sẽ được bọc lót ở tầng công cụ); Muốn phân phát theo arc → Thêm một nhánh vào Route; Muốn mở rộng khả năng phán quyết → Thêm một cặp Collect/Decide. Mỗi một lần nới lỏng đều có trọng tài (Đặc tả vét cạn, Đánh giá văn phong, Phát lại decisions) —— **Dùng bằng chứng để quyết định cho model bao nhiêu cuộn dây cương, chứ không dùng niềm tin**.

Kỷ luật duy nhất là: **Khi có người muốn thêm một điểm quyết định (decision point), trước tiên phải qua bộ lọc Quy tắc chia ba —— Có thể liệt kê thì vào Route, Ranh giới rõ ràng thì vào Arbiter, Mở thì vào Worker**. Những quyết định không thuộc cả ba loại trên, hãy suy nghĩ lại thật kỹ xem nó có thực sự tồn tại hay không.
