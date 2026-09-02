# Mẫu hồ sơ nhân vật & Ma trận xưng hô

Dùng mẫu này làm tham chiếu khi thiết lập hồ sơ nhân vật mới (`characters.md`).

## Trường dữ liệu

| Trường | Kiểu | Mô tả |
|---|---|---|
| `name` | string | Tên nhân vật (tuân thủ cẩm nang định danh theo bối cảnh văn hóa) |
| `aliases` | string[] | Biệt danh / xưng hiệu / đạo hiệu khác |
| `role` | string | Vai trò (nhân vật chính / phản diện / đạo sư / đồng minh / nhân vật phụ) |
| `tier` | string | Cấp độ: `core` (nòng cốt) / `important` (quan trọng) / `secondary` (phụ) / `decorative` (quần chúng) |
| `description` | string | Mô tả tổng thể nhân vật: xuất thân, tính cách, động cơ và xung đột cốt lõi |
| `arc` | string | Cung phát triển nhân vật qua các giai đoạn/tập truyện |
| `traits` | string[] | Đặc điểm tính cách nổi bật (thể hiện qua hành vi cụ thể, không dán nhãn) |
| `address_matrix` | object | Ma trận xưng hô chi tiết đối với các nhân vật cốt lõi khác |

## Cấu trúc Ma trận Xưng hô (`address_matrix`)

Đối với các nhân vật thuộc nhóm `core` và `important`, **bắt buộc phải khai báo ma trận xưng hô hai chiều** để Writer và Editor đối chiếu:

```markdown
### Ma trận xưng hô đối với các nhân vật khác
- Với [Tên Nhân vật B]:
  - Xưng hô thường ngày: xưng [X], gọi đối phương là [Y] (VD: xưng "Ta", gọi "Sư muội")
  - Khi bộc phát cảm xúc / biến cố: xưng [X'], gọi [Y'] (VD: xưng "Huynh", gọi "Muội")
  - Nhân vật B gọi lại: xưng [Y_self], gọi nhân vật này là [X_call] (VD: xưng "Đồ nhi", gọi "Sư tôn")
  - Ngôi thứ ba người kể chuyện dùng: "hắn" / "chàng" / "y" / "gã"
```

## Lưu ý

- Mọi nhân vật `core` đều phải có cung nhân vật (character arc) rõ ràng và ma trận xưng hô với các nhân vật tương tác chính.
- `tier` quyết định mức độ chú ý trong mạch kể:
  - `core`: Nhân vật trung tâm, luôn bám sát ma trận xưng hô và chiều sâu nội tâm.
  - `important`: Nhân vật ảnh hưởng mạch truyện chính, có xưng hô cố định với nhóm core.
  - `secondary` / `decorative`: Nhân vật chức năng, áp dụng quy tắc suy luận xưng hô tiếng Việt theo ngữ cảnh xã hội/tuổi tác.
