# Mẫu đại cương quy hoạch

Vai trò của mẫu này không phải là ép mọi tác phẩm vào một độ dài cố định, mà là giúp trước hết phán đoán cấp độ tác phẩm, rồi chọn độ hạt đại cương phù hợp.

## Bước một: Trước hết phán đoán cấp độ dài của tác phẩm

### Truyện ngắn / truyện một tập

- Áp dụng: một xung đột, một mục tiêu, ít nhân vật, kết truyện tập trung
- Độ dài tham khảo: 8-25 chương
- Định dạng khuyến nghị: outline phẳng

### Truyện trung / truyện nhiều giai đoạn

- Áp dụng: có nâng cấp theo giai đoạn, vài tuyến nhánh, quan hệ nhân vật sẽ thay đổi
- Độ dài tham khảo: 25-60 chương
- Định dạng khuyến nghị: outline phẳng hoặc phân tầng nhẹ

### Truyện trường thiên dài kỳ / kiểu web novel

- Áp dụng: thể loại vốn có sẵn không gian nâng cấp liên tục, độ căng quan hệ dài hạn, nhiều mục tiêu giai đoạn, thế giới có thể mở rộng, bí ẩn dài hạn hoặc trục trưởng thành dài hạn
- Độ dài tham khảo: 80-200+ chương
- Định dạng khuyến nghị: phân tầng `layered_outline`

## Bước hai: Phán đoán có bắt buộc dùng đại cương phân tầng không

Chỉ cần thỏa mãn 2 điều bất kỳ sau, ưu tiên dùng `layered_outline`:

- Thế giới quan cần triển khai dần dần, không phải nói hết một lần
- Trưởng thành nhân vật chính không phải một bước nhảy, mà là nâng cấp nhiều giai đoạn
- Quan hệ nhân vật sẽ thay đổi liên tục qua nhiều giai đoạn
- Giữa và cuối truyện tồn tại các loại mâu thuẫn chính khác nhau
- Cần nhiều lần chuyển đổi bản đồ / thế lực / thân phận / mục tiêu
- Thể loại rõ ràng giống tiểu thuyết thương mại dài kỳ hơn là truyện một tập

## Bước ba: Khi là trường thiên, đừng trực tiếp làm "bản kê chương toàn tuyến tính"

Thứ tự quy hoạch trường thiên khuyến nghị là:

1. Điểm mạnh và phân biệt tác phẩm
2. Động cơ truyện dài hạn
3. Chủ đề và nâng cấp cấp tập
4. Mục tiêu cấp arc và bước ngoặt giai đoạn
5. Sự kiện và móc cấp chương

Cách làm sai:

- Viết trước tóm tắt 20 chương, rồi cố kéo dài ra
- Mỗi tập đều lặp "gặp địch — mạnh lên — đổi bản đồ"
- Chỉ có nâng cấp tuyến chính, không có nâng cấp quan hệ
- Đầu truyện dùng hết mọi bí mật lớn, nửa sau chỉ còn lặp khuôn

## Mẫu đại cương phẳng (truyện ngắn / trung)

```json
[
  {
    "chapter": 1,
    "title": "tiêu đề chương",
    "core_event": "sự kiện cốt lõi của chương",
    "hook": "móc cuối chương",
    "scenes": ["cảnh 1", "cảnh 2", "cảnh 3"]
  }
]
```

## Mẫu đại cương phân tầng (trường thiên — triển khai lăn hai tầng tập + arc)

Quy hoạch ban đầu dùng triển khai lăn hai tầng: 2 tập đầu có khung arc, các tập còn lại là tập khung xương; arc đầu có chương chi tiết.

```json
[
  {
    "index": 1,
    "title": "tiêu đề tập một",
    "theme": "mâu thuẫn / chủ đề cốt lõi mới thêm vào của tập này",
    "arcs": [
      {
        "index": 1,
        "title": "arc đầu (đã triển khai)",
        "goal": "mục tiêu cục bộ, trở lực và bước ngoặt",
        "chapters": [
          {"chapter": 1, "title": "tiêu đề chương", "core_event": "sự kiện cốt lõi", "hook": "móc cuối chương", "scenes": ["cảnh 1", "cảnh 2"]}
        ]
      },
      {
        "index": 2,
        "title": "arc hai (arc khung xương)",
        "goal": "tóm lược mục tiêu của arc này",
        "estimated_chapters": 12,
        "chapters": []
      }
    ]
  },
  {
    "index": 2,
    "title": "tiêu đề tập hai",
    "theme": "chủ đề tập hai",
    "arcs": [
      {"index": 1, "title": "tiêu đề arc", "goal": "mục tiêu arc", "estimated_chapters": 15, "chapters": []},
      {"index": 2, "title": "tiêu đề arc", "goal": "mục tiêu arc", "estimated_chapters": 10, "chapters": []}
    ]
  },
  {
    "index": 3,
    "title": "tiêu đề tập ba (tập khung xương)",
    "theme": "hướng chủ đề tập ba",
    "estimated_chapters": 60,
    "arcs": []
  }
]
```

- Triển khai cấp arc: khi viết đến arc khung xương, Architect triển khai chương chi tiết của arc đó
- Triển khai cấp tập: khi viết đến tập khung xương, Architect triển khai cấu trúc arc của tập đó + chương arc đầu

## Bảng kiểm cấp tập cho trường thiên

Mỗi tập đều phải trả lời:

- Tập này thêm mới thông tin thế giới gì?
- Tập này leo thang mâu thuẫn cốt lõi nào?
- Tập này khiến nhân vật chính nhận được gì, cũng mất đi gì?
- Tập này thay đổi quan hệ nhân vật chính thế nào?
- Sau khi tập này kết thúc, vì sao truyện bắt buộc phải bước vào tập tiếp theo?

## Bảng kiểm cấp arc cho trường thiên

Mỗi arc đều phải trả lời:

- Mục tiêu rõ ràng của arc này là gì?
- Trở lực đến từ ai, quy tắc gì, cái giá gì?
- Bước ngoặt là gì?
- Sau khi arc này kết thúc, những trạng thái nào đã thay đổi không thể đảo ngược?

## Bảng kiểm cấp chương

- Mỗi chương phải phục vụ mục tiêu của arc chứa nó
- Mỗi chương phải chứa một bước đẩy sự kiện không thể xóa bỏ
- Móc phải đa dạng, đừng toàn bộ dựa vào một khuôn mẫu "phát hiện bí mật"
- Các chương đầu không thể chỉ "giới thiệu thế giới", phải đồng thời đẩy nhân vật và xung đột
