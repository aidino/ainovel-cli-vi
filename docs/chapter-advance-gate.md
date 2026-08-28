# Chapter Advance Gate

> Trạng thái: Đã thực hiện  
> Ngày: 2026-07-14  
> Giải quyết: Nghiệm thu từng chương, tạm dừng an toàn sau can thiệp, cấp phép chương chính xác dưới khôi phục sự cố

## 1. Tại sao cần nó

Rủi ro cốt lõi của việc tự động sáng tác truyện dài không phải là tốn thêm một lần gọi API, mà là trong lúc người dùng đang đọc kiểm (review), hệ thống tiếp tục viết chính văn chương mới, đồng thời nhúng các tóm tắt, trạng thái nhân vật và phản hồi đại cương dựa trên cốt truyện cũ vào nguồn sự kiện tiếp theo. Xóa một chương viết thừa không thể tự động hoàn tác các trạng thái phái sinh này, và người dùng sẽ mất niềm tin vào quá trình sáng tác.

Dự án vẫn định vị mặc định là "đưa ra mục tiêu rồi tiếp tục tự chủ hoàn thành", vì vậy không biến việc xác nhận mỗi chương thành mặc định toàn cục. Hệ thống cung cấp hai chính sách rõ ràng:

- `auto`: Chế độ mặc định, liên tục tự chủ đẩy tiến;
- `review`: Chế độ nghiệm thu từng chương do người dùng chủ động chọn, mỗi chương mới hướng tiến đều cần một lần cấp phép chính xác.

Đây không phải là trả lại luồng công việc cho Coordinator LLM. Khi nào cần người dùng xác nhận là chính sách của người dùng; quy trình xác định bước tiếp theo vẫn do Route suy diễn; việc có cần dừng lại một lần để nghiệm thu kết quả can thiệp hay không mới do Arbiter phán quyết ngữ nghĩa.

## 2. Phân chia ranh giới

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Hiện tại có phải là chế độ nghiệm thu từng chương không | RunMeta / Host | Ý định chạy bền bỉ của người dùng |
| Chương nào đã được cấp phép | RunMeta / Gate | Sự kiện cơ học có thể xác minh, có thể khôi phục |
| Bước tiếp theo chạy Worker nào | `flow.Route` | Suy diễn từ hàm thuần túy của sự kiện sáng tác |
| Chỉ lệnh có bắt đầu một chương mới hướng tiến không | `flow.StartsForwardChapter` | Đánh giá cơ học được định kiểu |
| "Sửa xong cho tôi xem" có cần tạm dừng không | Arbiter | Phán quyết ngữ nghĩa bằng ngôn ngữ tự nhiên |
| Khi nào kích hoạt tạm dừng | `ChapterAdvanceGate` | Thực thi có tính xác định đối với ý định dùng một lần |
| Ngân sách có cho phép tiếp tục không | `BudgetSentinel` | Chính sách Host độc lập |

`AdvanceMode`, cấp phép chương và hold dùng một lần không đi vào bảng quyết định Route, cũng không cho phép model sửa đổi. Trạng thái sáng tác của Route và chính sách nghiệm thu từng chương duy trì tính trực giao.

## 3. Mô hình trạng thái tối thiểu

Trong `meta/run.json` chỉ thêm ba ý định chạy:

```go
type RunMeta struct {
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"`
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`
}

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}
```

Không có PolicyEngine dùng chung, mảng điều kiện, hàng đợi cấp phép, thời gian hết hạn hoặc phiên bản chiến lược. Việc đẩy tiến chương chỉ giữ lại một chế độ bền bỉ, một cấp phép chính xác và một hold dùng một lần được định kiểu.

### 3.1 Bất biến

1. `AdvanceMode` chỉ có thể là `auto` hoặc `review`; giá trị không xác định sẽ trả về `UnsupportedAdvanceModeError`.
2. Chế độ không xác định không được khởi động Host, cũng không được ghi đè RunMeta.
3. Dưới `auto`, cấp phép phải là `0`.
4. Dưới `review`, cấp phép chỉ có thể là `0` hoặc một số chương nguyên dương.
5. Cấp phép lặp lại cho cùng mục tiêu là lũy đẳng, mục tiêu khác nhau không được ghi đè cấp phép đang chờ xử lý.
6. Cấp phép chỉ ràng buộc "bắt đầu một chương mới hướng tiến chưa hoàn thành"; quy hoạch, đọc kiểm, làm lại, trau chuốt và khôi phục cam kết không bị ngăn cản.
7. Cấp phép gắn liền với số chương, không gắn với một lần chạy tiến trình hoặc gọi Worker nào đó.
8. Chỉ khi chương mục tiêu đã vào `CompletedChapters`, `PendingCommit` tương ứng đã bị xóa, và tồn tại checkpoint `commit` của chương đó, cấp phép mới được coi là tiêu thụ ổn định.
9. Chương mục tiêu đã hoàn thành nhưng thiếu checkpoint commit là trạng thái hỏng: báo lỗi rõ ràng và tạm dừng, không đoán để sửa.
10. Cấp phép chưa hoàn thành phải bằng `Progress.NextChapter()`. `PendingRewrites` không làm thay đổi `NextChapter()`, vì vậy việc làm lại và cấp phép hướng tiến đang chờ có thể cùng tồn tại cơ học.
11. `AdvanceHold` chỉ có thể sử dụng `boundary`, `rewrites_drained` hoặc `chapter`, và phải mang theo lý do không rỗng; `chapter` phải mang theo chương mục tiêu số dương, các điều kiện khác cấm mang theo.
12. hold và cấp phép sử dụng compare-and-clear; khi trạng thái bị hành động mới thay thế không được xóa nhầm.
13. hold chương mục tiêu dưới `review` tạo thành ủy quyền khoảng thời gian dùng một lần; sau khi tạm dừng, chính sách nghiệm thu từng chương ban đầu vẫn giữ nguyên.

## 4. Store API

RunMetaStore cung cấp các thao tác nguyên tử hẹp và được định kiểu:

```go
SetAdvanceMode(mode domain.ChapterAdvanceMode) error
GrantAdvancePermit(chapter int) error
ClearAdvancePermit(chapter int) error
SetAdvanceHold(hold domain.AdvanceHold) error
ClearAdvanceHold(expected domain.AdvanceHold) error
```

- Khi chuyển về `auto`, xóa cấp phép chương trong cùng một khóa ghi, nhưng không xóa một hold khác sinh ra do can thiệp của người dùng;
- Ủy quyền chỉ hợp lệ dưới `review`;
- Thao tác xóa chỉ tiêu thụ cùng một mục tiêu mà bên gọi vừa đọc;
- Khi khởi tạo RunMeta, chế độ mặc định là `auto`, và giữ lại chế độ, cấp phép và hold đã ghi xuống đĩa.

Dự án hiện tại không có dữ liệu lịch sử cần di chuyển, do đó việc triển khai không bao gồm đọc trường cũ, ghi song song (double-write) hoặc nhánh hạ cấp.

## 5. Ngữ nghĩa hàm thuần túy

### 5.1 Nhận diện chương mới hướng tiến

```go
func StartsForwardChapter(
	inst *Instruction,
	progress *domain.Progress,
	pending *domain.PendingCommit,
) bool
```

Chỉ khi các điều kiện sau đồng thời thỏa mãn mới trả về true:

- Worker là `writer`;
- phase là `writing`;
- Không có `PendingCommit`;
- Không có hàng đợi làm lại;
- Không có `InProgressChapter`;
- Chương mục tiêu bằng `NextChapter()`.

Việc phán đoán chỉ đọc các trường được định kiểu, không phân tích văn bản Task hoặc Reason.

### 5.2 hold dùng một lần

`ResolveAdvanceHold` dựa trên hold và Progress trả về:

- `keep`: Điều kiện chưa được thỏa mãn;
- `consume`: Trạng thái hoàn bản chỉ cần dọn dẹp ý định;
- `consume-and-stop`: Dọn dẹp ý định và tạm dừng.

`boundary` kích hoạt ở ranh giới Worker hiện tại; `rewrites_drained` kích hoạt sau khi hàng đợi làm lại đã xử lý hết; `chapter` kích hoạt sau khi chương mục tiêu vào danh sách hoàn thành, `PendingCommit` đã xóa và checkpoint commit tồn tại. Điều kiện không xác định hoặc thiếu sự kiện sẽ trực tiếp báo lỗi.

## 6. ChapterAdvanceGate

Gate là thành phần chính sách đẩy tiến sáng tác duy nhất ngoại trừ ngân sách, chỉ có hai trách nhiệm:

1. Ở ranh giới vòng lặp phân tích và tiêu thụ hold dùng một lần;
2. Trước khi phân phát writer kiểm tra cấp phép từng chương, và ở ranh giới đối chiếu xem cấp phép có được tiêu thụ ổn định không.

Thứ tự của Engine là:

```text
Gửi can thiệp đang chờ xử lý
→ Gate kiểm tra ranh giới
→ Route / Lấy chỉ lệnh từ Arbiter
→ precheck
→ Gate kiểm tra cấp phép phân phát
→ Worker
→ Budget kiểm tra ranh giới
→ Gate kiểm tra ranh giới
→ Vòng tiếp theo
```

Khi `auto && hold == nil`, kiểm tra ranh giới đọc RunMeta xong sẽ trả về ngay, không đọc Progress, PendingCommit hoặc checkpoint.

### 6.1 hold + dispatch

Arbiter có thể phán quyết "Viết lại chương 3, sửa xong cho tôi xem" thành:

```json
{
  "hold": {
    "after": "rewrites_drained",
    "reason": "Chờ người dùng nghiệm thu sau khi viết lại xong"
  },
  "dispatch": {
    "agent": "editor",
    "task": "Đọc kiểm lại chương 3 và lập hàng đợi làm lại dựa trên kết quả"
  }
}
```

Nhóm hành động này phải ưu tiên thực thi phân phát ghép cặp (dispatch), để Editor tạo ra sự kiện làm lại, sau đó Gate mới phán đoán xem hàng đợi đã làm rỗng chưa. Engine sẽ liên kết "lần phân phát này lùi sau Gate" với chỉ lệnh bộ nhớ đó, khi lấy chỉ lệnh sẽ xóa đi cùng lúc; chỉ lệnh phân phát Arbiter thông thường không thể bỏ qua Gate.

### 6.2 permit và làm lại

`reopen` hoàn bản chỉ có thể xảy ra ở `complete`, còn `/next` chỉ có thể xảy ra ở `writing`, hai cái này loại trừ nhau về mặt cơ học. `PendingRewrites` đã tồn tại trong giai đoạn sáng tác không thay đổi số chương lớn nhất đã hoàn thành, do đó cấp phép vẫn căn chỉnh với cùng một `NextChapter()`; Worker làm lại có thể chạy, nhưng sẽ không tiêu thụ cấp phép hướng tiến.

## 7. Khôi phục sự cố

Commit chương là một saga nhiều bước, cấp phép không thể dùng giá trị boolean "lần run tiếp theo có thể viết một chương" để biểu diễn. Khi khôi phục Gate dựa trên ba loại sự kiện để đối chiếu:

| Cửa sổ sự kiện | Hành vi của Gate |
|---|---|
| Chương mục tiêu chưa hoàn thành, không có PendingCommit | Giữ lại cấp phép, cho phép bắt đầu/khôi phục chương đó |
| PendingCommit thuộc về chương mục tiêu | Giữ lại cấp phép, để việc khôi phục commit hoàn tất |
| Chương mục tiêu đã hoàn thành, PendingCommit đã xóa, checkpoint commit tồn tại | Tiêu thụ cấp phép |
| Chương mục tiêu đã hoàn thành nhưng thiếu checkpoint | Báo lỗi và tạm dừng |
| Cấp phép trỏ đến chương chưa hoàn thành không phải NextChapter | Báo lỗi và tạm dừng |

Do đó tiến trình bị sập ở bất kỳ cửa sổ nào như bản thảo, ghi trạng thái, đánh dấu tiến độ hoặc ghi tín hiệu, cũng sẽ không lấy nhầm cùng một cấp phép dùng cho chương tiếp theo.

## 8. Arbiter

Schema can thiệp sử dụng `AdvanceHoldOp`:

```go
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}
```

Quy tắc:

- Yêu cầu rõ ràng "dừng lại một chút trước" sử dụng `boundary`;
- Dưới `auto` yêu cầu "sửa chương đã viết, sửa xong để tôi nghiệm thu" sử dụng `rewrites_drained`;
- "Viết đến chương N" sử dụng `chapter`, khác biệt nghiêm ngặt với việc điều chỉnh độ dài "cả sách có tổng cộng N chương";
- `review` vốn dĩ đã dừng từng chương, không tạo lại hold đồng nghĩa lặp lại;
- hold chương mục tiêu dưới `review` là một lần ủy quyền hàng loạt do người dùng ký nhận rõ ràng;
- "Tiếp tục" có thể hủy hold hiện tại, nhưng không thể cấp phép chương;
- Chuyển chế độ chỉ có thể sử dụng `/review on|off`, cho qua (pass) chỉ có thể sử dụng `/next`.

Engine gọi trực tiếp RunMetaStore để áp dụng hành động cấu trúc, không ngụy trang nó thành LLM Tool.

## 9. Giao diện người dùng

### 9.1 `/review on|off`

- `/review on`: Lập tức lưu chính sách nghiệm thu từng chương; nếu Worker đang chạy, sau khi công việc hiện tại hoàn thành sẽ dừng lại trước chương mới hướng tiến tiếp theo;
- `/review off`: Chuyển về tự động đẩy tiến và xóa nguyên tử cấp phép; không tự động khởi động Engine đang tạm dừng, sự kiện sẽ nhắc nhở người dùng nhập lệnh tiếp tục.

### 9.2 `/next`

Chỉ khả dụng khi các điều kiện sau đồng thời thỏa mãn:

- Engine chưa chạy;
- Không phải giai đoạn đồng sáng tạo (cocreate);
- Chế độ là `review`;
- Không có hold đang chờ xử lý;
- Ngân sách cho phép;
- phase là `writing`.

Lệnh sẽ cấp phép chính xác cho `NextChapter()` và khởi động Engine. Thông báo sẽ nói rõ: sau khi chương này commit, các công việc đọc kiểm và bảo trì cấu trúc tập/arc cần thiết vẫn sẽ hoàn thành, sau đó lại chờ cho qua (pass).

### 9.3 Hiển thị trạng thái

`UISnapshot` là nguồn sự kiện duy nhất của TUI, bao gồm:

- `AdvanceMode`;
- `AdvancePermitChapter`;
- `HasAdvanceHold`;
- `AdvanceHoldReason`.

Thanh bên hiển thị trạng thái tự động/nghiệm thu từng chương và chương đã cho qua; khi chờ đợi ô nhập liệu nhắc "Nhập ý kiến sửa đổi, hoặc `/next` cho qua chương tiếp theo". Sự kiện có kind là `advance_gate`.

## 10. Xác minh

Độ bao phủ của kiểm thử (Test):

- Chuyển đổi trạng thái nguyên tử và compare-and-clear của chế độ, cấp phép, hold trong RunMeta;
- Chế độ không xác định thất bại rõ ràng và không ghi đè RunMeta;
- Nhận diện hàm thuần túy của chương mới hướng tiến và làm lại/khôi phục;
- Ngữ nghĩa của hold với boundary, làm lại chưa rỗng, làm lại rỗng và hoàn bản;
- Ngăn cản khi không có cấp phép, cho qua với cấp phép chính xác, báo lỗi với cấp phép sai chương;
- Giữ lại cấp phép trong thời gian PendingCommit, tiêu thụ sau khi commit ổn định;
- Tạm dừng khi đánh dấu hoàn thành và checkpoint xung đột;
- permit và PendingRewrites xen kẽ không báo lỗi sai;
- Chứng minh end-to-end của Engine rằng một cấp phép vừa đúng chỉ làm ổn định một chương mới;
- Khi Gate đã đánh dấu tạm dừng nhưng goroutine Engine cũ vẫn đang thoát, `/next` từ chối tái nhập (re-entry) rõ ràng, thử lại sau sẽ khôi phục lũy đẳng bằng cấp phép cùng chương;
- Hồi quy đối với hold-only, hold+dispatch và race condition khi thoát.

## 11. Rõ ràng không làm

- Không để model quyết định chế độ chạy hoặc cấp phát cấp phép;
- Không sửa đổi Route để tương thích chiến lược xác nhận của người dùng;
- Không biến việc làm lại, quy hoạch, đọc kiểm và bảo trì cấu trúc đều thành xác nhận từng bước;
- Không thêm PolicyEngine chung, danh sách StopCondition hoặc DSL chiến lược;
- Không cung cấp ủy quyền trước nhiều chương hoặc hàng đợi cấp phép;
- Không giữ lại mô hình tạm dừng cũ, trường tương thích, DTO di chuyển hoặc liên kết ghi song song (double-write);
- Không âm thầm hạ cấp cho chế độ tương lai chưa biết.

Trong tương lai nếu xuất hiện yêu cầu ranh giới tự trị mới, sẽ lặp lại việc mở rộng chế độ dựa trên bằng chứng; chi phí hối hận thấp hiện tại chính là tính tương thích cho tương lai.
