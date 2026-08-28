# Tái cấu trúc kiến trúc Engine + Arbiter

> Trạng thái: Đã hoàn thành (2026-07-12)

Đây là đợt tái cấu trúc triệt để mặt điều khiển (control plane) cốt lõi của ainovel-cli. Chúng tôi đã cho nghỉ hưu vòng lặp Coordinator dựa trên model lớn, chuyển sang sử dụng Engine mã nguồn thuần túy kết hợp với hàm phán quyết ngữ nghĩa (Arbiter) để tiếp quản quy trình.

## 1. Tại sao cần tái cấu trúc

Mô hình Coordinator khi đối mặt với truyện dài hơn 200 chương đã bộc lộ một số vấn đề không thể giải quyết bằng prompt:
1. **Quyết định không đáng tin cậy**: Model lớn không thể suy diễn chính xác "Bây giờ nên phái ai làm gì" trong một ngữ cảnh cực dài.
2. **Trôi dạt máy trạng thái**: Model lớn có xu hướng bỏ qua kế hoạch và bắt đầu viết trực tiếp, hoặc quên dọn dẹp bể phản hồi đại cương.
3. **Gỡ lỗi hộp đen**: Khi model lớn đưa ra quyết định sai, ngoài việc sửa prompt và cầu nguyện, không có biện pháp khắc phục xác định nào.
4. **Chi phí đắt đỏ**: Mỗi quyết định định tuyến (route) đều tiêu tốn hàng trăm nghìn token của toàn bộ ngữ cảnh.

## 2. Kiến trúc mới: Quy tắc chia ba

Chúng tôi đã chia trách nhiệm của mặt điều khiển thành ba tầng, đưa về đúng vị trí dựa trên bản chất của việc "quyết định làm như thế nào":

### 2.1 Mặt phẳng xác định (Engine + Route)
Xử lý **các chuyển đổi trạng thái có thể liệt kê**.
Viết xong một chương thì nên phái ai? Đại cương chệch hướng thì ai xử lý? Những vấn đề này về bản chất là tra bảng.
Chúng tôi mã hóa cứng (hardcode) tất cả máy trạng thái của luồng điều khiển vào `internal/flow/router.go`. Nó là một hàm thuần túy: đọc vào tất cả các sự kiện tiến độ và công cụ (artifact) hiện tại, trả về chính xác chỉ lệnh bước tiếp theo.
- **Lợi ích**: Tỷ lệ lỗi định tuyến giảm xuống 0. Hoàn toàn có thể xác minh thông qua các ca kiểm thử vét cạn (12 vạn tổ hợp). Không cần tiêu tốn bất kỳ token nào.

### 2.2 Mặt phẳng ngữ nghĩa (Arbiter)
Xử lý **các phán quyết ngữ nghĩa có ranh giới rõ ràng**.
"Người dùng nói muốn viết lại chương vừa rồi, tôi nên phái ai?" "Nhà quy hoạch (architect) nói bị mắc kẹt, giải quyết thế nào?" Những vấn đề này cần khả năng hiểu ngôn ngữ, nhưng không cần sáng tác mở.
Chúng tôi gom chúng lại thành một số ít các hàm Arbiter (như `intervention`, `plan_start`, `deadlock`). Chúng nhận đầu vào là các sự kiện xác định, trả về các hành động có cấu trúc (như hold, dispatch).
- **Lợi ích**: Sự không đáng tin cậy của model lớn bị giới hạn nghiêm ngặt ở một vài điểm ngữ nghĩa cụ thể, và mỗi lần phán quyết đều được ghi xuống đĩa, có thể phát lại ngoại tuyến và đánh giá.

### 2.3 Mặt phẳng sáng tác (Worker)
Xử lý **sáng tác mở**.
Architect (nhà quy hoạch), Writer (tác giả), Editor (biên tập viên) giữ lại hoàn toàn tính tự chủ của họ. Nhưng dưới kiến trúc mới, họ không cần phải vắt óc suy nghĩ "bước tiếp theo luân chuyển thế nào" nữa, chỉ cần chuyên tâm hoàn thành nhiệm vụ sáng tác đang có trong tay.

## 3. Cơ chế cốt lõi

### 3.1 Lan can sự kiện (Delta Guards)
Để ngăn chặn Worker (đặc biệt là Writer) "giả vờ như đã viết xong" (ví dụ: nói đã viết xong trong cửa sổ chat, nhưng thực tế không gọi công cụ ghi xuống đĩa), chúng tôi đã thêm các lan can sự kiện cơ học xung quanh Worker:
Nếu `commit_chapter` không tạo ra checkpoint mới, Worker không được phép kết thúc vòng làm việc hiện tại; nếu thất bại liên tục sẽ nâng cấp thành ngắt mạch cứng (hard stop).

### 3.2 Thu hẹp hoàn bản
Trước đây model lớn thường không chịu hoàn kết do ước tính số chữ chưa đạt. Bây giờ:
- **Quyền phán quyết thuộc về Architect**: Quyết định rõ ràng thông qua một trong ba lựa chọn `append_volume` ở cuối tập.
- **Quyền thực thi thuộc về Engine**: Khi cấu trúc đã hoàn kết, cộng với các tóm tắt và đọc kiểm (review) liên quan đã đầy đủ, Engine sẽ tự động cắt vòng lặp, không còn phụ thuộc vào sự "cho phép" của model lớn.

## 4. Tổng kết
**"Tra bảng không cần trí tuệ, nhưng nó cần được chứng minh là đúng."** 
Bằng cách tách biệt những nơi cần trí tuệ (sáng tác, hiểu) và những nơi cần sự chính xác (quy trình, trạng thái), hệ thống đã đạt được tính ổn định và khả năng kiểm thử chưa từng có trước đây.
