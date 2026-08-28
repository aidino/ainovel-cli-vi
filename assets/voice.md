## Chuẩn sáng tác

Đây là các tiêu chí chất lượng, đừng điểm danh cứng nhắc từng điều. Chương trước hết phải đứng vững một cách tự nhiên, rồi mới đến đủ hạng mục kiểm tra.

- Mở đầu nhanh chóng lập xung đột, hồi hộp, khát vọng hoặc cảm giác bất thường, ít dùng hồi tưởng trừu tượng.
- Dùng hành động, hội thoại, chi tiết giác quan để đẩy tình tiết, ít dùng khái quát và tổng kết.
- Hội thoại nhân vật phải có khác biệt thân phận, ngầm ý và mục đích hành động, đừng thuyết giáo.
- Cảm xúc thể hiện qua phản ứng cơ thể và lựa chọn, không dán nhãn trực tiếp.
- Thay đổi quan hệ phải có sự kiện khơi mào, đừng trong một chương nhảy từ xa lạ lên tin tưởng tuyệt đối.
- Bí mật hé lộ theo từng đợt, không giải thích trước câu đố lớn mà đại cương chưa yêu cầu.
- Móc cuối chương có thể là khủng hoảng, lựa chọn, dư ba cảm xúc, thay đổi quan hệ hoặc mục tiêu chưa hoàn tất, không cần chương nào cũng dựng hồi hộp cường điệu.
- **Tránh "vị AI"**: khi viết tránh toàn bộ khuôn mẫu liệt kê trong `reference_pack.references.anti_ai_tone` (năm loại: kết cấu / dụng từ / miêu tả / hội thoại / nhịp độ). Trong đó, ngưỡng từ mệt mỏi và khuôn câu có thể liệt kê cơ khí xem `working_memory.user_rules.structured`, bị kiểm bắt buộc lúc commit.
- **Đa dạng câu thức**: `episodic_memory.style_stats` (nếu có) là thống kê của code về phần thân bạn đã viết — tấm gương về khuôn câu quen miệng của chính bạn. Chương này chủ động giảm các hạng mục tần suất cao trong đó; nguồn cố hữu phổ biến nhất là câu chỉnh đính ("không phải… mà là…"), lượng từ chỉ thời gian đơn nhất ("mấy nhịp hơi / vài nhịp") và liên tiếp ẩn dụ cùng loại. Hình thái khép lại cuối chương (chém bằng câu ngắn / dư âm hội thoại / tàn ảnh bối cảnh / câu hỏi hồi hộp) luân phiên với các chương gần đây, mở đầu tránh kiểu mỗi chương đều bắt đầu bằng từ thời gian "đêm / sáng sớm / tỉnh dậy".
- **Không nhắc lại tình tiết trước**: tóm tắt, chi tiết gieo mầm, trạng thái trong `episodic_memory` là bản ghi nhớ về phần thân đã viết, dùng để đối chiếu nối tiếp, không phải tài liệu chờ viết của chương này; thông tin chương trước đã kể, chương mới chỉ chạm khi tình tiết cần bằng góc nhìn mới, cấm viết lại kiểu tóm tắt tình tiết trước (đọc lặp nguyên văn xuyên chương sẽ bị ghi vào repeated_sentences của style_stats).
