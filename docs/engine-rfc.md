# Step 2 RFC: Engine trực tiếp chạy Worker (Lời giải cho 7 câu hỏi bắt buộc)

> Trạng thái: Đã chốt (2026-07-12). Dựa trên việc trinh sát mã nguồn của host/observer/subagent/usage/cocreate.
> Kết luận: Toàn bộ 7 câu hỏi đều có lời giải rủi ro thấp, đưa vào triển khai. Liên kết: docs/engine-arbiter.md.

## 1. Mặt thực thi Worker: Gọi lập trình subagent.Runner

> Hậu ký (2026-07-22): agentcore đã tách rời việc thực thi định kiểu và giao thức công cụ model. `Runner.Run` là điểm vào của host;
> `Runner.AsTool()` chỉ dành cho host cần để LLM tự chủ phân phát subagent. AINovel chỉ phụ thuộc vào Runner.

`Runner.Run(agent, task)` mỗi lần khởi động một `agentcore.AgentLoop` hoàn chỉnh. Engine gọi trực tiếp nó, 
**toàn bộ lắp ráp của build.go có hiệu lực nguyên trạng**: model nhân vật + failover, prompt cache key (#seq tự tăng mỗi lần), ThinkingLevel,
UsageRecorder/SessionLogger (OnMessage), Writer ContextManagerFactory, RestorePack, StopGuardFactory,
StopAfterTools. Kết quả định kiểu và chuỗi lỗi được trả về trực tiếp, không đi qua mã hóa/giải mã JSON hoặc đánh hơi kết quả công cụ.

**Chiếu sự kiện (Event projection)**: Tiến độ của subagent đọc từ **hàm callback ToolProgress trong ctx** (`agentcore.ReportToolProgress(ctx,...)`).
Engine gọi `Runner.Run` với `agentcore.WithToolProgress(ctx, relay)`, bộ chuyển tiếp (relay) hoạt động bình thường; relay tổng hợp ProgressPayload thành
`EventToolExecUpdate` rồi đưa cho `observer.handleToolUpdate` hiện có —— xử lý phía worker của observer (dòng TOOL/chính văn dạng luồng/
thinking/retry/context) **tỷ lệ tái sử dụng ~95%**. Dòng DISPATCH chuyển sang do Engine trực tiếp khởi xướng/thu dọn (thêm 2 điểm vào trong observer).
Dòng tường thuật cột trái của Coordinator biến mất, được thay thế bằng sự kiện tường thuật của Engine.

**/model và cường độ suy luận**: Chuyển đổi model qua ModelSet swap (configs giữ failover wrapper, cơ chế cũ); cường độ suy luận qua
`runner.SetThinkingLevel` (giữ lại applyThinking, xóa nhánh coordinator).

## 2. Vòng đời Engine

Vòng lặp nối tiếp (serial) đơn goroutine; cancel `ctx` = tạm dừng/dừng hẳn (lan truyền vào worker loop, checkpoint đảm bảo không mất mát);
Resume/Continue = khởi động vòng lặp mới. Tính nối tiếp của một Worker được đảm bảo tự nhiên nhờ cấu trúc vòng lặp. Ngân sách/lính gác điểm dừng ở mỗi ranh giới vòng lặp do
Engine trực tiếp gọi (thay thế cho đăng ký sự kiện và FlowBoundaryHook).

## 3. Giao thức đệ trình trạng thái → Tính nối tiếp khiến nó gần như biến mất

Trước khi sinh ra (spawn) ở mỗi vòng lặp, Engine mới `LoadState+Route`, chỉ lệnh luôn dựa trên sự kiện mới nhất —— Chỉ lệnh Route không có TOCTOU (Time-of-check to time-of-use), không cần đối chiếu Expect.
Bản chụp Expect (Expect snapshot) chỉ dùng cho **dispatch của quyết định Arbiter** (giữa tư vấn và thực thi cách nhau một lần chạy worker): trước khi thực thi ở ranh giới, so sánh
{Phase, QueueHead}, không khớp → loại bỏ + hỏi lại bằng sự kiện mới. Kiểm tra tiền đề (trách nhiệm cũ của Gate) trở thành mã Engine thông thường:
phase=complete không phân phát; chương mục tiêu của writer chưa được mở rộng → chuyển sang phái architect_long expand (xác định, không cần văn bản hướng dẫn).
Hành động trạng thái điều khiển (hold/reopen/dispatch) của can thiệp đi vào hàng đợi Engine để đệ trình ở ranh giới; answer/rules thực thi tức thì.

## 4. Phân loại lỗi (Xác định đi trước)

- retryable (mạng/giới hạn luồng/stream-idle): MaxRetries=7 bên trong subagent đã tự tiêu hóa tại chỗ, không thoát ra khỏi vòng lặp
- worker trả về error (escalate/hard_stop/lỗi cứng công cụ): Cùng một chỉ lệnh Engine thử lại 1 lần → vẫn thất bại → Arbiter
  tư vấn `worker_failure` (retry/reroute/abort) → abort hoặc bản thân Arbiter thất bại → tạm dừng + notify
- Lỗi tham số/agent không xác định hoặc các lỗi xác định khác: Tạm dừng trực tiếp + notify (bug code, thử lại vô nghĩa)

## 5. Giao thức bế tắc (Deadlock)

Mỗi vòng lặp ghi lại khóa chỉ lệnh `Agent+Task`. Sau khi thực thi ở vòng trước, Route vẫn tạo ra cùng khóa, chứng tỏ điều kiện hậu đề của nhiệm vụ chưa được thỏa mãn, `repeat++`; chỉ lệnh thay đổi thì xóa về 0. Các checkpoint trung gian `plan/draft/edit` bên trong Worker không được tính là tiến triển ở cấp độ Engine.
repeat==3 → Arbiter tư vấn `deadlock`; Arbiter đề xuất retry **không xóa về 0**; repeat==5 → ngắt mạch cứng: tạm dừng + notify.
(Thời Coordinator "không đặt ngưỡng" dựa vào tính tự chủ của nó; Engine xác định bắt buộc phải có giới hạn.)

## 6. Ngữ nghĩa sự cố → Miễn phí

Không cần phán đoán "Worker trước có tạo ra sự kiện hợp lệ không": checkpoint+digest ở tầng công cụ có tính lũy đẳng, Route tính toán lại từ store,
việc phân phát lặp lại là an toàn. Việc thử lại luồng model của agentcore sẽ không vượt qua ranh giới thực thi công cụ. Khôi phục = trực tiếp đi vào vòng lặp. PendingSteer trước khi vòng lặp khởi động đóng vai trò là
can thiệp để đi qua Arbiter.

## 7. Nghiệm thu nguyên mẫu

Kiểm thử tích hợp đầu cuối (end-to-end) (fake ChatModel): quy hoạch → bổ sung → viết chương → đọc kiểm/tóm tắt cuối arc → mở rộng → hoàn bản Toàn bộ chuỗi; phân loại can thiệp ghi vào store;
tạm dừng/khôi phục; ngắt mạch do bế tắc; ghi nhận usage; hình dạng sự kiện của observer (dòng DISPATCH/TOOL, delta luồng). Cộng thêm các lưới hồi quy hiện có:
đặc tả Route 60k, hợp đồng agentcore, kiểm thử luồng editor.

## Tổng kết giai đoạn hoàn thành (Quyết định thiết kế)

Tóm tắt hoàn bản chuyển sang **tạo lập xác định**: store đã có toàn bộ sự kiện (tóm tắt chương/nhân vật/sổ chi tiết chi tiết gieo mầm/số chữ), Engine trực tiếp kết xuất (render) báo cáo,
không tốn thêm một lần gọi LLM để tạo ra văn bản mang tính nghi thức nữa. LLM tổng kết của coordinator cũ bị hủy bỏ (engine-arbiter.md §3: Tổng kết không phải phán quyết).
