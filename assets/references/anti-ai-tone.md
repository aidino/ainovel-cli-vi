# Phán cứ "vị AI"

Bản phán cứ này dùng để đọc kiểm một bản thảo xem có chứa các khuôn mẫu cố hữu giống văn phong máy hay không. Các khuôn mẫu được chia làm năm loại:

1. **Khuôn mẫu kết cấu** — cấu trúc đoạn văn hoặc chương theo lối mòn
2. **Khuôn mẫu dụng từ** — trình kích từ lặp đi lặp lại, từ sáo rỗng, viết đầy mà không có gì
3. **Khuôn mẫu mô tả** — kể thay vì thể hiện, khái quát thay vì cụ thể
4. **Khuôn mẫu hội thoại** — trao đổi chức năng không có tính cách, phơi bày không có ẩn ý
5. **Khuôn mẫu nhịp độ** — cao trào đơn điệu, phân bổ căng thẳng máy móc

Editor dùng các tiêu chí này khi đánh giá phạm trù aesthetic trong quá trình đọc kiểm. Mỗi lần phát hiện phải được chứng minh bằng trích nguyên văn từ bản thảo, không chỉ khái quát.

## Các tiêu chí kiểm

### 1. Khuôn mẫu kết cấu
- Đoạn văn tuân theo cùng một cấu trúc ba bước: tuyên bố → chứng minh → kết luận
- Chương hoặc cảnh phân giải quá gọn gàng, không có vụn vỡ
- Mỗi tiểu kết đều được bọc trong một câu chủ đề

### 2. Khuôn mẫu dụng từ
- Lạm dụng từ nối: "tuy nhiên", "ngoài ra", "hơn nữa", "vì vậy", "trong khi đó"
- Bổ ngữ mơ hồ: "rất quan trọng", "cực kỳ mạnh", "đặc biệt sâu sắc"
- Từ viết đầy: các câu bắt đầu bằng "điều quan trọng là", "cần lưu ý rằng", "đáng chú ý là"
- Điệp ba: dùng liên tục ba cụm danh từ hoặc tính từ song song

### 3. Khuôn mẫu mô tả
- Dán nhãn cảm xúc: nhân vật "cảm thấy giận dữ" thay vì thể hiện cơn giận
- Diện mạo mẫu: mô tả như trong một bản kiểm kê ("đôi mắt xanh, mái tóc vàng, dáng người cao")
- Mô tả bối cảnh tách rời: dừng hành động để miêu tả khung cảnh
- Đặt tên cho cảm giác: nói cho độc giả biết phải cảm nhận thế nào thay vì dàn dựng

### 4. Khuôn mẫu hội thoại
- Lời của các nhân vật không thể phân biệt được nếu không có nhãn tên
- Hội thoại kể lại các sự kiện mà cả hai nhân vật đều đã biết
- Phát biểu trực tiếp thiếu ẩn ý: nhân vật nói chính xác điều họ nghĩ, không có tầng nghĩa
- Không có ngắt lời, ngập ngừng hoặc chuyển hướng trong hội thoại

### 5. Khuôn mẫu nhịp độ
- Đỉnh cảm xúc phân bố đều đặn, có thể dự đoán được
- Mỗi chương kết thúc bằng một móc theo cùng một công thức
- Các cảnh hành động ép vào cùng một kiểu độ dài, không có biến thiên
- Các đoạn giải tỏa hạ nhiệt thay vì chuyển hướng

## Các lệnh trừng phạt cụ thể

Đây là các trình kích cú pháp và cách dùng từ tần suất cao trong văn bản sinh bởi LLM. Khi chúng xuất hiện thường xuyên trong bản thảo, đó là dấu hiệu mạnh của văn phong AI:

- "không phải..., mà là..." — câu chỉnh đính (lạm dụng làm cho văn xuôi giống như đang tranh luận với chính nó)
- "dường như", "tựa hồ", "như thể" — so sánh đầu cơ (dùng để mô tả cái không chắc chắn về mặt tự sự)
- Họ từ chỉ thời gian bị lạm dụng: "mấy nhịp hơi", "vài nhịp", "chỉ trong chốc lát" (dịch thẳng từ các thành ngữ tu luyện, nhanh chóng trở nên lặp đi lặp lại)
- So sánh bằng "như": "như một...", "tựa như..." xuất hiện với tần suất cao bất thường
- "dường như... lại..." — mô tả ngập ngừng
- Dùng lặp "mà" như một pivot trong câu ghép

## Cách sử dụng bản phán cứ này

1. Đọc bản thảo và đánh dấu các câu / đoạn khớp với bất kỳ khuôn mẫu nào ở trên
2. Với mỗi lần phát hiện, trích câu gốc làm bằng chứng
3. Phân loại mức độ nghiêm trọng (một lần thì là warning, hệ thống thì là error)
4. Đề xuất cách viết lại cụ thể thay vì phê bình mơ hồ
- Không đếm các lần xuất hiện đơn lẻ như là bằng chứng quyết định — tìm kiếm các khuôn mẫu
- Một vài câu chỉnh đính là ổn; cả đoạn toàn câu chỉnh đính là không ổn
- Việc dùng từ chỉ thời gian thỉnh thoảng là bình thường; mỗi cảnh đều dùng chúng là vấn đề
