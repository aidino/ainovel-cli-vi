# Mẫu hồ sơ nhân vật

Dùng mẫu này làm tham chiếu khi thiết lập hồ sơ nhân vật mới.

## Trường dữ liệu

| Trường | Kiểu | Mô tả |
|---|---|---|
| `name` | string | Tên nhân vật |
| `aliases` | string[] | Biệt danh / xưng hiệu khác |
| `role` | string | Vai trò (nhân vật chính / phản diện / đạo sư / nhân vật phụ) |
| `description` | string | Mô tả tổng thể nhân vật, gồm bối cảnh, động cơ và cung phát triển chính |
| `arc` | string | Cung phát triển nhân vật dưới dạng văn bản tự do (không phải JSON) |
| `traits` | string[] | Đặc điểm tính cách (string array) |
| `tier` | string | cấp: core / important / secondary / decorative |

## Lưu ý

- Mỗi nhân vật chính và nhân vật phụ chủ chốt nên có cung nhân vật rõ ràng
- Các cung nên trải dài qua nhiều tập đối với truyện trường thiên
- Các đặc điểm (traits) phải được thể hiện qua hành vi trong truyện, không phải chỉ tuyên bố
- `tier` quyết định nhân vật sẽ nhận được bao nhiêu sự chú ý trong tường thuật
