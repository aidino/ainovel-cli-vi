# Giải thích về Quản lý Ngữ cảnh (Context Management)

Tài liệu này giải thích hệ thống quản lý ngữ cảnh hiện tại của `ainovel-cli`, bao gồm:

- Tại sao cần quản lý ngữ cảnh
- Ngữ cảnh đến từ đâu
- Làm thế nào để nén, khôi phục, bàn giao trong lúc chạy (runtime)
- Giá trị, điều kiện kích hoạt và bối cảnh áp dụng của mỗi chiến lược
- Khi có vấn đề nên kiểm tra ở đâu trước

Mục tiêu không phải là giới thiệu các khái niệm trừu tượng, mà là để những người bảo trì sau này khi mở tài liệu này ra, có thể nhanh chóng hiểu được cách thức hoạt động hiện tại và điểm vào để gỡ lỗi.

## 1. Mục tiêu Thiết kế

Quản lý ngữ cảnh của dự án này không dành cho các đoạn chat thông thường, mà là hướng tới ngữ cảnh sáng tác tiểu thuyết. Nó phải đồng thời giải quyết một số vấn đề:

1. Cuộc hội thoại dài sẽ vượt quá cửa sổ ngữ cảnh (context window) của model.
2. Sáng tác tiểu thuyết không cần giữ lại "bản thân lịch sử chat", mà là bộ nhớ tự sự có cấu trúc.
3. Sau khi nén, Writer không được làm mất trạng thái nhân vật, chi tiết gieo mầm (foreshadow), kế hoạch chương, ràng buộc văn phong, và các mục chờ sửa từ bản đọc kiểm (review).
4. Khi khôi phục việc viết lách, không thể giả định rằng model vẫn "nhớ những gì đã nói trước đó", mà phải ưu tiên phụ thuộc vào các công cụ (artifact) đã được lưu trữ bền vững.

Vì vậy, chúng tôi áp dụng một giải pháp "bộ nhớ phân tầng":

- Bộ nhớ ngắn hạn: Phần đuôi của các tin nhắn được giữ lại gần đây
- Bộ nhớ trung hạn: `ContextSummary` sinh ra từ việc nén
- Bộ nhớ dài hạn: Các công cụ có cấu trúc trong store của dự án
- Bộ nhớ khôi phục: handoff / restore pack / novel_context

## 2. Kiến trúc Tổng thể

### 2.1 Các Tầng Chính

Hiện tại quản lý ngữ cảnh được chia thành 4 tầng:

1. `agentcore/context`
   Chịu trách nhiệm về ngân sách ngữ cảnh chung, đường ống (pipeline) chiến lược, framework nén/khôi phục.

2. `internal/tools/novel_context`
   Chịu trách nhiệm lắp ráp dữ liệu có cấu trúc trong dự án tiểu thuyết thành ngữ cảnh có thể dùng cho vòng lặp hiện tại.

3. `internal/orchestrator/store_summary_*`
   Chịu trách nhiệm nén nhanh dựa trên store (store-based) dành riêng cho Writer.

4. `internal/orchestrator/writer_restore.go`
   Chịu trách nhiệm thêm một gói khôi phục (restore pack) sau khi nén bằng `FullSummary`, đảm bảo Writer có thể tiếp tục viết.

### 2.2 Luồng Dữ liệu

Trong lúc chạy, có 2 luồng ngữ cảnh chính:

1. Luồng hoạt động bình thường
   - Agent gọi `novel_context`
   - `novel_context` đọc các dữ liệu như tóm tắt chương, kế hoạch, nhân vật, dòng thời gian từ store
   - Những dữ liệu này đi vào prompt của vòng lặp hiện tại

2. Luồng khi ngữ cảnh quá dài
   - `ContextManager` phát hiện áp lực về token
   - Nén theo thứ tự của chiến lược
   - Ưu tiên thử nén nhẹ và nén dựa trên store
   - Khi vẫn chưa đủ thì mới đi qua `FullSummary` của LLM
   - Sau `FullSummary` thì tiêm (inject) restore pack

## 3. Các Tệp Quan Trọng

### 3.1 Engine Ngữ cảnh Chung

- `../agentcore/context/strategy.go`
- `../agentcore/context/engine.go`
- `../agentcore/context/strategy_tool.go`
- `../agentcore/context/strategy_trim.go`
- `../agentcore/context/strategy_summary.go`
- `../agentcore/context/message.go`
- `../agentcore/context/summary_run.go`

Tác dụng:

- Định nghĩa `Strategy` / `ForceCompactionStrategy`
- Chịu trách nhiệm thực thi chuỗi chiến lược dựa trên ngân sách
- Chịu trách nhiệm biểu diễn `ContextSummary` và chuyển đổi bằng LLM
- Chịu trách nhiệm nén tóm tắt LLM của `FullSummary`

### 3.2 Điểm Kết nối phía Dự án

- `internal/orchestrator/agents.go`

Tác dụng:

- Lắp ráp `ContextManager` của Writer (Coordinator đã nghỉ hưu vào ngày 2026-07-12, xem docs/engine-arbiter.md)
- Tiêm thêm `StoreSummaryCompact` cho Writer
- Cấu hình prompt `FullSummary` tùy chỉnh cho tiểu thuyết cho Writer
- Cấu hình `writerRestorePack` cho Writer

### 3.3 Nén và Khôi phục phía Dự án

- `internal/orchestrator/store_summary_strategy.go`
- `internal/orchestrator/store_summary_builder.go`
- `internal/orchestrator/writer_restore.go`

Tác dụng:

- Trước khi dùng tóm tắt của LLM, ưu tiên dùng dữ liệu store để nén nhanh
- Xây dựng ngữ cảnh có cấu trúc cần thiết cho việc nén và khôi phục của Writer một cách thống nhất
- Sau `FullSummary`, thêm một tin nhắn khôi phục (restore message) hoàn toàn trên bộ nhớ

### 3.4 Lắp ráp Ngữ cảnh có Cấu trúc

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`
- `internal/domain/runtime.go`

Tác dụng:

- Định nghĩa `ContextProfile` / `MemoryPolicy`
- Quyết định tải bao nhiêu tóm tắt chương, bao nhiêu dòng thời gian, có bật tóm tắt phân tầng không
- Lắp ráp các phần như chương, nhân vật, chi tiết gieo mầm, dòng thời gian, kinh nghiệm đọc kiểm từ store

### 3.5 Bàn giao (Handoff) và Khôi phục

- `internal/orchestrator/handoff_policy.go`
- `internal/orchestrator/recovery_engine.go`

Tác dụng:

- Ở giai đoạn truyện dài/làm lại/đọc kiểm, ưu tiên phụ thuộc vào handoff
- Khi khôi phục, ghép gói bàn giao có cấu trúc vào prompt

### 3.6 Khả năng Quan sát

- `internal/orchestrator/run.go`
- `internal/orchestrator/runtime.go`
- `internal/entry/tui/panels.go`

Tác dụng:

- Ghi lại các sự kiện ghi lại ngữ cảnh (context rewrite)
- In ra tên chiến lược, thay đổi về token, dung lượng tin nhắn được giữ lại
- Cho phép TUI nhìn thấy ngữ cảnh hiện tại là `projected` hay `compacted`

## 4. ContextManager Được Lắp Ráp Như Thế Nào

Writer đi qua `newContextManager` (được tạo lại bởi factory theo kích thước cửa sổ của model hiện tại sau mỗi lần spawn). Trước khi Coordinator nghỉ hưu, nó cũng đi qua cùng một factory, cấu hình của nó được giữ lại trong bảng dưới để đối chiếu lịch sử.

Các tham số quan trọng của `contextManagerConfig` hiện tại:

- `ContextWindow`
  Tổng cửa sổ ngữ cảnh của model.

- `ReserveTokens`
  Số token dự trữ cho đầu ra của model.

- `KeepRecentTokens`
  Ngân sách của phần đuôi tin nhắn gần đây cố gắng giữ lại khi nén.

- `ToolMicrocompact`
  Cấu hình nén vi mô cho kết quả công cụ.

- `ExtraStrategies`
  Các chiến lược nén bổ sung phía dự án. Hiện tại Writer dùng để gắn `StoreSummaryCompact`.

- `Summary`
  Cấu hình của `FullSummary`, bao gồm prompt tùy chỉnh và post-summary hook.

Giá trị cấu hình thực tế hiện tại:

| Tham số | Writer | Coordinator (Đã nghỉ hưu, để đối chiếu) |
|------|--------|-------------|
| ReserveTokens | 16,384 | 32,000 |
| KeepRecentTokens | 20,000 | 30,000 |
| CommitOnProject | false | true |
| IdleThreshold | 5min | Không có |
| ExtraStrategies | StoreSummaryCompact | Không có |
| Tùy chỉnh Summary Prompt | Bản tự sự tiểu thuyết | Mặc định (Bản trợ lý code) |

Ngưỡng kích hoạt nén = `ContextWindow - ReserveTokens`. Ví dụ khi cửa sổ là 128K, Writer sẽ kích hoạt ở khoảng 112K.

Thứ tự đường ống chiến lược hiện tại của Writer là:

1. `ToolResultMicrocompact`
2. `LightTrim`
3. `StoreSummaryCompact`
4. `FullSummary`

Thứ tự này có ý nghĩa rõ ràng:

- Trước tiên dùng cách rẻ nhất để dọn dẹp nhiễu công cụ
- Sau đó cắt xén các khối văn bản quá dài
- Nếu dữ liệu store đủ, trực tiếp làm nén có cấu trúc với LLM = 0
- Cuối cùng mới lùi về tóm tắt bằng LLM

## 5. Tác Dụng Của Mỗi Chiến Lược

### 5.1 ToolResultMicrocompact

Vị trí triển khai:

- `../agentcore/context/strategy_tool.go`

Tác dụng:

- Dọn dẹp `tool_result` lịch sử
- Thay thế kết quả công cụ cũ bằng đoạn văn bản giữ chỗ ngắn gọn

Giá trị:

- Nội dung trả về của công cụ thường có dung lượng lớn, mật độ thông tin thấp
- Nhiều kết quả công cụ cũ chỉ là "nhiễu quá trình", không phải ký ức của tiểu thuyết

Đặc điểm cấu hình hiện tại của Writer:

- Đã thiết lập `IdleThreshold = 5m`

Điều này có nghĩa là:

- Nếu tin nhắn assistant gần nhất đã rảnh rỗi (idle) vượt quá ngưỡng
- Sẽ quyết liệt hơn trong việc giảm số lượng kết quả công cụ cũ được giữ lại

Bối cảnh áp dụng:

- Nhiều vòng `novel_context`
- Sau nhiều vòng gọi công cụ read / check / draft

### 5.2 LightTrim

Vị trí triển khai:

- `../agentcore/context/strategy_trim.go`

Tác dụng:

- Cắt cụt các khối văn bản quá dài
- Giữ lại phần đầu và phần đuôi, ở giữa thay thế bằng placeholder

Giá trị:

- Giữ nguyên cấu trúc tin nhắn
- Chi phí thấp
- Rất phù hợp để xử lý văn bản gốc của chương quá dài hoặc đoạn đầu ra lớn

Bối cảnh áp dụng:

- Một tin nhắn quá dài, nhưng chưa cần làm summary cho toàn bộ lịch sử

### 5.3 StoreSummaryCompact

Vị trí triển khai:

- `internal/orchestrator/store_summary_strategy.go`
- `internal/orchestrator/store_summary_builder.go`

Tác dụng:

- Khi ngữ cảnh của Writer quá dài
- Ưu tiên sử dụng ký ức có cấu trúc trong store đã được lưu trữ bền vững để thay thế tin nhắn cũ
- Không gọi LLM

Nó không phải là tóm tắt đoạn chat, mà là "thay thế ký ức có cấu trúc".

Dữ liệu cốt lõi hiện được giữ lại bao gồm:

- Tiến độ hiện tại
- Tóm tắt chương gần đây
- Kế hoạch chương hiện tại
- Đại cương chương hiện tại
- Tóm tắt arc hiện tại
- Tóm tắt tập (volume) hiện tại
- Bản chụp (snapshot) nhân vật
- Chi tiết gieo mầm (foreshadow) đang hoạt động
- Các vấn đề đọc kiểm đang chờ sửa
- Dòng thời gian gần đây
- Quy tắc văn phong

Tiền đề kích hoạt:

- Chương hiện tại lớn hơn 1
- Đã có đủ tóm tắt lịch sử trong store
- Và chương hiện tại có ít nhất dữ liệu trạng thái làm việc (working state)
  - `chapter_plan` hoặc `current_outline`

Giá trị:

- Giảm số lần nén bằng LLM
- Tránh thông tin quan trọng của tiểu thuyết bị trôi dạt (drift) khi tóm tắt
- Để ký ức dài hạn ưu tiên phụ thuộc vào sự kiện đã ghi xuống đĩa, chứ không phải lịch sử chat

Tại sao chỉ dành cho Writer:

- Đây là chiến lược nghiệp vụ tiểu thuyết, không phải chiến lược framework chung
- Mô hình ngữ cảnh của Editor / Architect khác nhau (Nhiệm vụ đơn lẻ, áp lực cửa sổ thấp)
- Việc xác minh trước trên Writer - nơi cần ký ức sáng tác liên tục nhất - là hợp lý nhất

### 5.4 FullSummary

Vị trí triển khai:

- `../agentcore/context/strategy_summary.go`
- `../agentcore/context/summary_run.go`

Tác dụng:

- Khi các tầng trên chưa đủ, sử dụng model để tạo ra `ContextSummary`
- Giữ lại phần đuôi tin nhắn gần đây
- Biến các ngữ cảnh cũ hơn thành checkpoint có cấu trúc

Điểm khác biệt của Writer so với trợ lý code mặc định:

- Writer sử dụng prompt tóm tắt tùy chỉnh
- Nội dung tóm tắt yêu cầu rõ ràng phải giữ lại:
  - Tiến độ hiện tại
  - Trạng thái tức thời của nhân vật
  - Chi tiết gieo mầm và manh mối đang hoạt động
  - Phản hồi đọc kiểm và vấn đề đang chờ sửa
  - Văn phong và nhịp điệu
  - Các quyết định quan trọng
  - Bước tiếp theo
  - Ngữ cảnh quan trọng

Giá trị:

- Là chiến lược bọc lót cuối cùng
- Ngay cả khi dữ liệu store không đủ, vẫn có thể duy trì tính liên tục thông qua LLM

### 5.5 Cầu dao (Circuit Breaker)

Vị trí triển khai:

- `../agentcore/context/engine.go`

Tác dụng:

- Khi quá trình nén thất bại liên tiếp đạt đến ngưỡng (mặc định là 3 lần), sẽ bỏ qua việc nén của vòng hiện tại
- Khi bỏ qua vẫn phát ra `RewriteEvent` (`Reason = "circuit_breaker"`)
- TUI sẽ hiển thị scope là "Bỏ qua do ngắt mạch (Skip due to circuit break)"
- Áp dụng mô hình half-open: Sau khi bỏ qua một vòng, lần tới sẽ thử lại, thành công thì reset, thất bại tiếp thì lại bỏ qua

Tại sao cần thiết:

- Việc tóm tắt bằng LLM có thể thất bại liên tiếp do mạng, model từ chối,...
- Nếu không có cầu dao, mỗi vòng Project đều sẽ cố gắng và thất bại, gây lãng phí API call
- Sự lãng phí này sẽ tích lũy trong các phiên sáng tác truyện dài

Gỡ lỗi (Troubleshooting):

- Nếu TUI liên tục hiển thị "Bỏ qua do ngắt mạch", tức là luồng tóm tắt LLM đang có vấn đề
- Kiểm tra các sự kiện context rewrite có `reason=circuit_breaker` trong slog
- Việc ngắt mạch không ảnh hưởng đến `StoreSummaryCompact` (Nó không gọi LLM)

### 5.6 Ước tính Token (Nhận biết CJK)

Vị trí triển khai:

- `../agentcore/context/usage.go`

Tác dụng:

- Mọi việc kiểm soát ngân sách, thời điểm kích hoạt nén đều phụ thuộc vào ước tính token
- `estimateTextTokens` tự động phát hiện xem văn bản có chủ yếu là ký tự CJK (Trung, Nhật, Hàn) hay không
- Văn bản chủ đạo là CJK: `runes × 1.5`
- Văn bản chủ đạo là ASCII: `bytes / 4`

Tại sao không thể dùng `bytes/4` tiêu chuẩn:

- Một chữ tiếng Trung ở chuẩn UTF-8 = 3 bytes
- `bytes/4` sẽ ước tính một chữ Hán là 0.75 token, trong khi thực tế là khoảng 1.5 token
- Đánh giá thấp đi 2 lần sẽ khiến việc kích hoạt nén bị trễ nghiêm trọng

Phạm vi ảnh hưởng:

- `EstimateTokens` (Tin nhắn đơn)
- `EstimateTotal` (Danh sách tin nhắn)
- `EstimateContextTokens` (Ước tính hỗn hợp: LLM báo cáo Usage + Ước tính các tin nhắn ở đuôi)
- Việc cắt gọt ngân sách trong `store_summary_builder.go`

Lưu ý: Tham số (args) của ToolCall là JSON (Chủ đạo là ASCII), nên vẫn sử dụng `bytes/4`, không bị ảnh hưởng bởi điều chỉnh CJK.

## 6. Tại Sao Writer Có Hai Bộ "Ký Ức Sau Khi Nén"

Hiện tại Writer có hai chuỗi liên kết tưởng chừng giống nhau, nhưng chức năng lại khác nhau:

### 6.1 StoreSummaryCompact

Chức năng:

- Trực tiếp thay thế các tin nhắn cũ trong quá trình nén

Đặc điểm:

- Diễn ra trước `FullSummary`
- 0 LLM
- Dùng store để thay thế lịch sử cũ hơn

### 6.2 writerRestorePack

Vị trí triển khai:

- `internal/orchestrator/writer_restore.go`

Chức năng:

- Thêm một restore message (tin nhắn khôi phục) sau `FullSummary`

Đặc điểm:

- Diễn ra sau khi LLM nén
- Được tiêm vào thông qua `PostSummaryHook`
- Dùng để bổ sung các thông tin có cấu trúc mà Writer bắt buộc phải thấy khi khôi phục để viết tiếp

Tại sao cần cả hai:

- `StoreSummaryCompact` không phải lúc nào cũng trúng
  - Ví dụ như ở chương 1 hoặc khi dữ liệu store không đủ
- `FullSummary` dù có làm tốt đến đâu cũng có thể bỏ sót các thông tin chính xác trong store
- Nên restore pack đóng vai trò như chốt bảo hiểm cuối cùng

Hiện tại cả hai đã dùng chung `store_summary_builder.go` để tránh việc lệch pha về chuẩn dữ liệu.

## 7. Tác Dụng Của novel_context

Vị trí triển khai:

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`

`novel_context` không phải là chiến lược nén, nó là "bộ lắp ráp ngữ cảnh có cấu trúc" lúc runtime.

Nó chia dữ liệu trong store thành các loại:

- `working_memory` (Bộ nhớ làm việc)
  - Kế hoạch chương hiện tại
  - Đại cương chương hiện tại
  - Tóm tắt chương gần đây
  - Dòng thời gian
  - checkpoint
  - previous tail (đuôi của phần trước)

- `episodic_memory` (Bộ nhớ theo tập)
  - Trạng thái nhân vật
  - Trạng thái quan hệ
  - Thay đổi trạng thái gần đây
  - Chi tiết gieo mầm (Foreshadow)

- `reference_pack` (Gói tham khảo)
  - Dữ liệu tham chiếu và thiết lập ổn định hơn

- `selected_memory` (Bộ nhớ được chọn lọc)
  - Một lượng nhỏ ký ức quan trọng được chọn ra theo nhiệm vụ hiện tại

Giá trị:

- Nó quyết định những ngữ cảnh tiểu thuyết có cấu trúc nào thực sự được "đút cho model" ở mỗi vòng
- `StoreSummaryCompact` không trực tiếp gọi nó, nhưng tái sử dụng cùng nguồn dữ liệu và tư duy lắp ráp với nó

## 8. ContextProfile Và MemoryPolicy

Vị trí triển khai:

- `internal/domain/runtime.go`

### 8.1 ContextProfile

Tác dụng:

- Quyết định kích thước cửa sổ cần tải dựa trên tổng số chương

Quy tắc hiện tại:

- `<= 15` chương
  - 10 tóm tắt chương gần nhất
  - 10 dòng thời gian gần nhất

- `<= 50` chương
  - 5 tóm tắt chương gần nhất
  - 8 dòng thời gian gần nhất

- `> 50` chương
  - 3 tóm tắt chương gần nhất
  - 5 dòng thời gian gần nhất
  - Bật tóm tắt phân tầng (Layered summaries)

Giá trị:

- Kiểm soát quy mô ngữ cảnh
- Tránh việc nhét tất cả lịch sử vào prompt khi viết truyện dài

### 8.2 MemoryPolicy

Tác dụng:

- Ghi rõ ràng chiến lược sử dụng ngữ cảnh hiện tại
- Cung cấp cho đầu ra của `novel_context`
- Cung cấp cho các logic handoff / reminder / chuẩn đoán

Các trường quan trọng:

- `SummaryWindow`
- `TimelineWindow`
- `LayeredSummaries`
- `SummaryStrategy`
- `HandoffPreferred`
- `ReadOnlyThreshold`

Giá trị:

- Biến "hệ thống hiện tại nên sử dụng ký ức như thế nào" từ logic ngầm thành chiến lược runtime tường minh

## 9. Tác Dụng Của handoff (Bàn giao)

Vị trí triển khai:

- `internal/orchestrator/handoff_policy.go`

Khi tác phẩm bước vào giai đoạn dài hơn, phức tạp hơn, và phụ thuộc nhiều hơn vào các công cụ có cấu trúc, hệ thống sẽ thiên về handoff.

Gói handoff (handoff pack) sẽ ghi lại:

- Giai đoạn và flow hiện tại
- Vị trí chương tiếp theo
- Lần đệ trình (commit) gần nhất
- Lần đọc kiểm (review) gần nhất
- Tóm tắt gần nhất
- Memory policy hiện tại
- Lời hướng dẫn khôi phục

Giá trị:

- Khi khôi phục sau gián đoạn sẽ không phụ thuộc vào lịch sử chat
- Ưu tiên phụ thuộc vào công cụ có cấu trúc trong các ngữ cảnh như làm lại, đọc kiểm, truyện dài

## 10. Khả Năng Quan Sát Và Gỡ Lỗi

### 10.1 Sự kiện ghi lại ngữ cảnh (Context Rewrite Event)

Vị trí triển khai:

- `internal/orchestrator/run.go`

Mỗi lần ghi lại ngữ cảnh đều sẽ in ra thông qua `contextRewriteCallback`:

- `reason`
- `strategy`
- `committed`
- `tokens_before`
- `tokens_after`
- `messages_before`
- `messages_after`
- `compacted_count`
- `kept_count`
- `split_turn`
- `incremental`
- `summary_runes`
- `duration_ms`

Những thông tin này sẽ đồng thời đi vào:

- `slog`
- Hàng đợi ranh giới của runtime (runtime boundary queue)
- Sự kiện `COMPACT` của TUI

### 10.2 TUI có thể nhìn thấy gì

TUI sẽ hiển thị:

- Số token ngữ cảnh hiện tại (với màu gradient chỉ sức khỏe)
- context window
- Scope ngữ cảnh hiện tại (có cả "Bỏ qua do ngắt mạch")
- Tên chiến lược cuối cùng hiện tại
- Số lượng summary

Ý nghĩa màu sắc phần trăm của ngữ cảnh (Triển khai trong `internal/entry/tui/layout.go`):

| Màu sắc | Điều kiện | Ý nghĩa |
|------|------|------|
| Xanh lá | < 70% | Dư dả, cách xa ngưỡng nén |
| Vàng | 70-85% | Đang tiến gần tới ngưỡng nén |
| Đỏ | > 85% | Sắp hoặc đang nén |

Nhãn tiếng Trung của Scope:

| Scope | Hiển thị | Ý nghĩa |
|-------|------|------|
| baseline | Cơ sở (Baseline) | Trạng thái bình thường |
| projected | Phóng chiếu (Projected) | Xem trước nén tạm thời |
| compacted | Đã đệ trình (Compacted) | Nén đã có hiệu lực |
| recovered | Khôi phục (Recovered) | Khôi phục sau khi tràn |
| skipped | Bỏ qua do ngắt mạch (Skipped) | Nén bị cầu dao bỏ qua |

Giá trị:

- Có thể nhanh chóng phán đoán mức độ khỏe mạnh của ngữ cảnh hiện tại
- Khi thấy màu Vàng/Đỏ có thể đoán trước việc nén sắp xảy ra
- Thấy "Bỏ qua do ngắt mạch" là biết đường ống tóm tắt LLM đang có vấn đề

### 10.3 Khi có vấn đề kiểm tra ở đâu trước

#### Tình huống 1: Sau khi nén Writer làm mất kế hoạch chương

Kiểm tra trước:

- `novel_context` có tiêm `chapter_plan` ổn định không
- `store_summary_builder.go` có lấy được `chapterPlan` không
- `writerRestorePack` có refresh (làm mới) không

Tệp cần chú ý:

- `internal/tools/novel_context_builders.go`
- `internal/orchestrator/store_summary_builder.go`
- `internal/orchestrator/session.go`

#### Tình huống 2: Sau khi nén làm mất trạng thái nhân vật/chi tiết gieo mầm

Kiểm tra trước:

- `LoadLatestSnapshots`
- `LoadActiveForeshadow`
- `store_summary_builder.go`
- Summary prompt của Writer có bị ghi đè không

#### Tình huống 3: Nén thường xuyên nhưng không bao giờ trúng store_summary

Kiểm tra trước:

- Chương hiện tại có phải là `<= 1` không
- Đã có recent summaries / arc / volume summary chưa
- Có tồn tại `chapter_plan` hay `current_outline` không
- Cái cuối cùng mà `writer.Context.Strategy` ghi lại có phải là `full_summary` không

#### Tình huống 4: Sau khi khôi phục không đủ ngữ cảnh

Kiểm tra trước:

- handoff có sinh ra không
- restore pack có refresh không
- recovery prompt có tiêm handoff vào không

#### Tình huống 5: Kết quả công cụ quá nhiều làm phình ngữ cảnh

Kiểm tra trước:

- `ToolResultMicrocompact` có trúng (hit) không
- `IdleThreshold` có hiệu lực không

## 11. Sự Đánh Đổi Của Triển Khai Hiện Tại

### Định hướng đã giữ vững rõ ràng

1. Không nhét logic nghiệp vụ tiểu thuyết vào `agentcore`
2. Ưu tiên dựa vào store có cấu trúc, thay vì lịch sử trò chuyện
3. Writer sử dụng prompt tóm tắt dành riêng cho tiểu thuyết
4. Nén và khôi phục cố gắng dùng chung builder, tránh bị lệch pha về chuẩn

### Những hạn chế chủ ý giữ lại hiện tại

1. `StoreSummaryCompact` chỉ cho Writer dùng
2. Chương đầu tiên sẽ không trúng nén dựa trên store
3. Khi dữ liệu store không đủ thì vẫn lui về `FullSummary`
4. `writerRestorePack` là một dạng bù đắp bổ sung, không thay thế `FullSummary`

Những hạn chế này không phải là lỗi (defect), mà là ranh giới được vạch ra để kiểm soát độ phức tạp trong giai đoạn hiện tại.

## 12. Tóm tắt trong một câu

Quản lý ngữ cảnh của dự án này không đơn giản là "nén đoạn hội thoại dài cho ngắn lại", mà là:

`Ưu tiên dùng ký ức tiểu thuyết có cấu trúc để duy trì tính liên tục, chỉ khi cần thiết mới để LLM đi tóm tắt hội thoại; và ở cả 3 khâu nén, khôi phục, bàn giao đều cố gắng dùng chung một bộ công cụ đã lưu bền vững.`

Nếu sau này bạn cần sửa hệ thống này, hãy ưu tiên giữ vững 3 điều sau:

1. Đừng bao giờ để ký ức quan trọng của Writer lại một lần nữa chỉ dựa vào lịch sử trò chuyện.
2. Đừng bao giờ để chuẩn của `store_summary` và `writer_restore` bị lệch nhau.
3. Khi gặp vấn đề về tính liên tục, trước tiên hãy tra xem công cụ có cấu trúc đã đi vào ngữ cảnh chưa, rồi hẵng quyết định có sửa prompt hay không.
