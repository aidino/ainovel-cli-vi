# Thiết kế thống nhất quy tắc người dùng

## Tóm tắt trong một câu

Tất cả các quy tắc sáng tác dài hạn đều được chuẩn hóa vào cùng một snapshot quy tắc của cuốn sách; lúc chạy (runtime), snapshot này chỉ được tiêm (inject) thông qua `novel_context`, không còn việc nhồi nhét lặp đi lặp lại văn bản quy tắc gốc vào prompt nữa.

```text
Prompt khởi động / File rules của người dùng / Yêu cầu dài hạn trong quá trình chạy
        ↓
LLM chuẩn hóa ngữ nghĩa (theo nguồn)
        ↓
Go kết hợp xác định (theo độ ưu tiên)  ←  Quy tắc mặc định của hệ thống (tích hợp trong code, đưa trực tiếp vào kết hợp, không qua LLM)
        ↓
output/novel/meta/user_rules.json
        ↓
Tiêm qua novel_context
        ↓
Dùng chung cho Architect / Writer / Editor / kiểm tra khi commit
```

## Trạng thái triển khai (2026-07-19, đã áp dụng + đã sửa lỗi sau khi review)

Thiết kế này đã được triển khai, `go build` / `go vet` / `go test` của 24 package đều pass. Sau một vòng code review, đã vá 4 lỗ hổng (tất cả đều đã được sửa): ① Quy tắc prompt khởi động chỉ được gắn vào phương thức chết `Host.Start` mà bỏ sót việc tạo snapshot vì lối vào thực sự lại đi qua `StartPrepared` —— đã truyền thẳng prompt gốc từ hai lối vào quick/cocreate để gọi chung `Host.PrepareUserRules`; ② Bỏ qua việc lưu snapshot xuống đĩa bị lỗi —— `PrepareUserRules` đổi thành trả về error và hủy bỏ việc tạo sách nếu không thể lưu xuống đĩa (đường dẫn resume vẫn giữ nguyên best-effort để tránh tạo ra kiểu thất bại mới cho sách cũ); ③ Lỗi đọc file rules bị bỏ qua trong im lặng —— `raw.go` ghi log cho các lỗi không phải là "không tồn tại" (quyền hạn, v.v.); ④ README vẫn hướng dẫn dùng YAML/front matter cũ và liên kết đến các file đã bị xóa —— đã viết lại.

Việc triển khai về cơ bản giống với tài liệu này, các lựa chọn triển khai sau khi nâng cấp structured output như sau:

1. **Chuẩn hóa chỉ có một `Contract.Schema`, không bảo trì hai bộ prompt.**
   Khi model khai báo hỗ trợ, sẽ hạ lệnh JSON Schema nguyên bản; khi không hỗ trợ hoặc chưa biết khả năng, tầng hợp đồng thống nhất sẽ tiêm cùng một Schema đó vào prompt.
   Cả hai chế độ đều sẽ kiểm tra lại (review) Schema ở phía Go, sau đó thực thi việc kiểm tra (validate) miền giá trị và nghiệp vụ xuyên các field.
2. **Khi một giá trị field bị lỗi, sẽ hạ cấp xuống "thiếu field đó" thay vì hạ cấp toàn bộ nguồn.**
   Ví dụ: Nếu một field là chuỗi giữ chỗ trống (placeholder) hoặc có kiểu không hợp lệ, sanitize sẽ loại bỏ field đó (coi như chưa được khai báo), giữ lại các field hợp lệ khác từ cùng một nguồn;
   Chỉ khi "chuẩn hóa toàn bộ thất bại" (mạng/model/JSON không hợp lệ/phân tích cú pháp thất bại) thì mới hạ cấp toàn bộ nguồn đó thành raw preferences,
   và đặt `status=degraded`. Như vậy một field hỏng sẽ không làm liên lụy đến các quy tắc hợp lệ khác từ cùng một nguồn. Các lỗi đầu ra mà model có thể sửa sẽ mang theo
   nguyên nhân chính xác để tiếp tục tự chữa lành, vòng đời do `context` kiểm soát; các lỗi chấm dứt rõ ràng sẽ được ghi log và hạ cấp theo nguồn.

Vị trí code: `internal/rules` (thuần dữ liệu + kết hợp xác định: snapshot.go / raw.go / types.go), `internal/userrules`
(LLM chuẩn hóa + điều phối + lưu xuống đĩa: normalize.go / service.go), `internal/store/user_rules.go` (lưu trữ snapshot),
`internal/userrules/service.go` (lưu quy tắc xuống đĩa lúc chạy), `assets/prompts/arbiter-intervention.md` (phân luồng làm 3 loại).
Đường cơ sở cơ học mặc định của hệ thống đã được chuyển từ `assets/rules/default.md` sang tích hợp trong code `rules.SystemDefaults()`, các đường dẫn phân tích cú pháp YAML và
phụ thuộc yaml.v3 đã bị xóa. **Chưa kiểm chứng**: Luồng toàn bộ hành động rules của Arbiter / LLM mở sách thực tế trong khi chạy (nguyên mẫu normalizer ngoại tuyến đã kiểm chứng 10/10).

## Tại sao

Mỗi chương Writer không nhận được prompt hoàn chỉnh ban đầu của người dùng một cách ổn định. Nó chủ yếu phụ thuộc vào nhiệm vụ của chương này và `novel_context(chapter=N)`.

Vì vậy, các quy tắc dài hạn không thể dựa vào bộ nhớ lịch sử hội thoại, cũng không nên dựa vào regex để đoán ngầm từ ngôn ngữ tự nhiên. Cách làm đúng là: Chuẩn hóa rõ ràng các quy tắc dài hạn thành trạng thái (state), sau đó phân phối thống nhất thông qua `novel_context`.

Việc "chuẩn hóa" ở đây phải tận dụng khả năng hiểu ngôn ngữ tự nhiên của model lớn (LLM), chứ không phải là liệt kê các cách diễn đạt trong Go. Chương trình chỉ định nghĩa một số lượng nhỏ các field có thể kiểm tra cơ học, chịu trách nhiệm về schema, kết hợp xác định, kiểm tra, lưu xuống đĩa và kiểm tra commit; các cách diễn đạt như "khoảng một nghìn rưỡi mỗi chương", "đừng vượt quá hai nghìn mỗi chương", "đừng viết những câu như bánh răng vận mệnh nữa" sẽ do LLM hiểu ngữ nghĩa.

## Trạng thái thống nhất

Trong khi chạy sách, chỉ duy trì một nguồn sự thật (fact source) duy nhất cho quy tắc người dùng:

```text
output/novel/meta/user_rules.json
```

Cấu trúc được giữ đơn giản:

```json
{
  "version": 1,
  "status": "ready",
  "structured": {
    "genre": "Tu tiên",
    "forbidden_chars": [],
    "forbidden_phrases": ["ở một mức độ nào đó"],
    "fatigue_words": {}
  },
  "preferences": "Nhân vật chính bình tĩnh điềm đạm; ít giải thích, dùng hành động và đối thoại nhiều hơn.",
  "sources": [
    "startup_prompt",
    ".ainovel/rules/style.md"
  ],
  "uncertain": [
    "Dùng ít phép ẩn dụ: Không có ngưỡng rõ ràng, xử lý theo sở thích phong cách"
  ]
}
```

Ranh giới các field:

- `version`: Phiên bản schema của snapshot, thuận tiện cho việc migrate trong tương lai.
- `status`: `ready` / `degraded`, đánh dấu việc chuẩn hóa có thành công hoàn toàn hay không; chỉ dùng để phản hồi (echo) và chẩn đoán, không đưa vào việc phán đoán sáng tác.
- `structured`: Các quy tắc mà code có thể kiểm tra cơ học hoặc tiêu thụ một cách ổn định.
- `preferences`: Những sở thích bằng ngôn ngữ tự nhiên không thể kiểm tra cơ học, nhưng có hiệu lực lâu dài đối với việc sáng tác.
- `sources`: Kiểm toán nguồn gốc, không đưa vào phán đoán sáng tác.
- `uncertain`: Chẩn đoán chuẩn hóa, chỉ dùng để phản hồi và khắc phục sự cố, không đưa vào phán đoán sáng tác.

Những gì được tiêm vào model chỉ là `structured` và `preferences`; `version` / `status` / `sources` / `uncertain` là siêu dữ liệu vận hành và chẩn đoán, không được đưa vào `working_memory.user_rules`. Các lỗi kỹ thuật không được đưa vào snapshot, chỉ được ghi vào log (xem §Thất bại và hạ cấp).

## Nguồn đầu vào

Các quy tắc dài hạn có bốn nguồn đầu vào:

1. **Prompt khởi động**: Các yêu cầu dài hạn mà người dùng viết khi mở sách.
2. **File rules của người dùng**: Các sở thích dài hạn cấp dự án hoặc toàn cục, được đọc dưới dạng ngôn ngữ tự nhiên thông thường.
3. **Quy tắc mặc định của hệ thống**: Đường cơ sở cơ học (mechanical baseline) được tích hợp trong code.
4. **Yêu cầu dài hạn trong quá trình chạy**: Người dùng nói giữa chừng "sau này sẽ như thế nào", Arbiter trích xuất hành động `rules`, Host gọi `AddRuntimeRule`.

Các nguồn đầu vào này không trực tiếp đi vào prompt của Writer, cũng không được đọc đi đọc lại lúc chạy (runtime). Chúng chỉ tham gia vào việc chuẩn hóa khi tạo hoặc cập nhật snapshot, và kết quả được kết hợp (merge) vào `meta/user_rules.json`.

## File rules

File rules là một prompt dài hạn thông thường, không phải là runtime prompt, cũng không phải là file cấu hình. Nó chỉ làm đầu vào chuẩn hóa, không hỗ trợ YAML:

```md
# Sở thích sáng tác

Mỗi chương 1200-1600 chữ.
Nhân vật chính bình tĩnh, điềm đạm, đừng thánh mẫu.
Ít giải thích, thúc đẩy bằng hành động và đối thoại nhiều hơn.
Không xuất hiện từ "ở một mức độ nào đó".
```

Sau khi hệ thống đọc sẽ được chuẩn hóa thành:

```json
{
  "structured": {
    "forbidden_phrases": ["ở một mức độ nào đó"]
  },
  "preferences": "Mỗi chương 1200-1600 chữ; Nhân vật chính bình tĩnh, điềm đạm, đừng thánh mẫu; Ít giải thích, thúc đẩy bằng hành động và đối thoại nhiều hơn."
}
```

Nếu trong file xuất hiện YAML front matter, nó cũng sẽ được xử lý như văn bản thông thường, không đóng vai trò là khai báo có cấu trúc. Kết quả có cấu trúc chỉ đến từ quy trình chuẩn hóa thống nhất.

Sau khi khởi động, nếu người dùng sửa đổi file rules, cuốn sách hiện tại sẽ không tự động thay đổi; cần phải tạo lại snapshot. Như vậy, các cuốn sách cũ sẽ không bị trôi dạt hành vi do sự thay đổi của file rules toàn cục.

## Chuẩn hóa ngữ nghĩa

Chuẩn hóa là một lời gọi LLM độc lập bị ràng buộc bởi schema —— mỗi nguồn được chuẩn hóa riêng biệt một lần, không trộn lẫn trong quá trình tạo nội dung sáng tác, cũng không dựa vào regex hoặc hardcode bảng từ khóa để phân tích cú pháp.

Đầu vào:

- Văn bản gốc của một nguồn duy nhất (prompt khởi động / một file rules / một yêu cầu lúc chạy)
- Hướng dẫn cho các field `structured` mà hệ thống hiện hỗ trợ

Các quy tắc mặc định của hệ thống không thuộc loại này —— chúng là các quy tắc có cấu trúc đã biên dịch (compiled) được tích hợp trong code, đi thẳng vào §Kết hợp quy tắc, không qua normalizer.

Đầu ra:

- Ứng cử viên `structured` của nguồn đó
- Ứng cử viên `preferences` của nguồn đó
- `sources`
- `uncertain`

Trách nhiệm của phía Go:

- Cung cấp schema.
- Kiểm tra kiểu dữ liệu của field và miền giá trị (value range).
- Theo độ ưu tiên của §Kết hợp quy tắc, kết hợp xác định các nguồn (LLM không phán quyết độ ưu tiên của nguồn).
- Lưu snapshot.
- Tiêm snapshot vào `novel_context`.
- Dùng cùng một snapshot để kiểm tra cơ học trong `commit_chapter`.

Trách nhiệm của phía LLM:

- Hiểu các quy tắc bằng ngôn ngữ tự nhiên của một nguồn duy nhất.
- Nâng cấp (promote) các quy tắc rõ ràng, có thể kiểm tra cơ học lên `structured`.
- Giữ lại thẩm mỹ, phong cách, sở thích về nhân vật thành `preferences`.
- Thận trọng với các nội dung không chắc chắn, không tự ý sáng chế ra ngưỡng (threshold).

### Nâng cấp một cách thận trọng (Conservative promotion)

`structured` là một quy tắc cứng hoặc tham số ổn định, không phải là "khu vực dự đoán của model". Việc nâng cấp quy tắc phải thận trọng:

- Chỉ khi người dùng diễn đạt rõ ràng, không mơ hồ thì mới ghi vào `structured`.
- `forbidden_chars` / `forbidden_phrases` là các trường cấp độ lỗi (error-level), phải đặc biệt thận trọng; chỉ nâng cấp khi có lệnh cấm rõ ràng như "không xuất hiện X", "cấm dùng X", "đừng viết X".
- `fatigue_words` chỉ được nâng cấp khi người dùng cung cấp rõ ràng từ và ngưỡng; những yêu cầu không có ngưỡng như "dùng ít phép ẩn dụ", "đừng quá văn vở", "giảm câu cửa miệng" sẽ được đưa vào `preferences`.
- Ý muốn về số chữ/độ dài ("mỗi chương 3000 chữ", "ngắn một chút") nhất loạt đưa vào `preferences`: Độ dài ngắn của chương là sự phán xét ngữ nghĩa về nhịp điệu kể chuyện, không làm kiểm tra cơ học —— việc số hóa thành ranh giới cứng sẽ xúi giục model bơm nước (viết lan man) để vượt quá ranh giới.
- Các yêu cầu không thể cơ học hóa, không có ngưỡng rõ ràng, phụ thuộc vào đánh giá ngữ cảnh sẽ đi vào `preferences`.

Nguyên tắc:

```text
Thà để lọt (bỏ sót) vào structured, bị hạ cấp thành sở thích mềm (soft preference);
Còn hơn đưa nhầm vào structured, tạo ra các báo cáo lỗi cứng mỗi chương.
```

Cái giá của việc bỏ sót chiết xuất là sở thích về phong cách sẽ yếu hơn một chút; cái giá của việc chiết xuất sai là tạo ra dữ kiện quy tắc sai lầm cho mỗi chương.

## Thất bại và hạ cấp

Việc chuẩn hóa là một đường dẫn tăng cường (enhancement path), không phải là điều kiện tiên quyết cho việc sáng tác chính. Việc model hiểu sai tuyệt đối không được cản trở việc viết sách.

- **Hạ cấp theo nguồn**: Một nguồn nào đó bị lỗi khi chuẩn hóa (mạng / model / JSON không hợp lệ / lỗi schema), nguồn đó sẽ bị hạ cấp thành raw preferences, không tạo ra `structured`; các nguồn thành công khác vẫn đóng góp `structured` như bình thường.
- **Tự chữa lành do ngữ cảnh (context) kiểm soát**: Lỗi request có thể retry, lỗi định dạng/Schema ở chế độ prompt và lỗi validate nghiệp vụ sẽ tiếp tục tự chữa lành cho đến khi thành công hoặc `context` kết thúc; không thiết lập số lần cố định. Vi phạm hợp đồng nguyên bản (native contract), từ chối trả lời, bị cắt bớt, kết thúc do lỗi và các lỗi request không thể retry sẽ ngay lập tức được hiển thị và hạ cấp theo nguồn.
- **Lỗi kỹ thuật ghi vào log**: Các lỗi kỹ thuật như JSON / schema / mạng v.v. được ghi vào log, không đi vào `working_memory.user_rules`, không làm đầu vào cho sáng tác.
- **Đánh dấu snapshot**: Khi bất kỳ nguồn nào bị hạ cấp, snapshot sẽ có `status=degraded`.
- **Có thể lưu xuống đĩa thì tiếp tục**: Chừng nào còn có thể ghi vào `meta/user_rules.json`, việc sáng tác chính bắt buộc phải được tiếp tục.
- **Chỉ dừng khi không lưu được xuống đĩa**: Chỉ bị hủy bỏ khi snapshot không thể lưu vào đĩa, vì lần chạy tiếp theo sẽ không có nguồn sự thật ổn định.

Giao ước của `AddRuntimeRule` (lúc chạy): Khi normalizer thất bại, lưu lại snapshot bị degraded,
không tiêm các lỗi chuẩn hóa như JSON/schema/mạng vào luồng sáng tác; chỉ trả về error khi lưu xuống đĩa thất bại.

## Quy tắc mặc định của hệ thống

`System defaults` là một đường cơ sở cơ học được tích hợp trong code, không phải là file rules của người dùng, cũng không sử dụng YAML.

Nó không đi qua việc chuẩn hóa của LLM —— nó đã ở dạng cấu trúc sẵn, được trực tiếp coi là nguồn có ưu tiên thấp nhất để tiến vào bước kết hợp Go trong phần §Kết hợp quy tắc. Như vậy, các quy tắc mặc định sẽ không gặp phải vấn đề thất bại, trôi dạt (drift) hay chi phí của LLM.

Các quy tắc cơ học mặc định của hệ thống trước đây được lưu tạm thời trong `assets/rules/default.md` (chi tiết triển khai cũ, nhằm tương thích với YAML của người dùng); khi thiết kế này được áp dụng, nó đã được chuyển sang dạng tích hợp trong code `rules.SystemDefaults()`, đường dẫn phân tích cú pháp YAML đã bị xóa (xem §Trạng thái triển khai).

Khi di chuyển (migrate), hãy giữ lại các chú thích cần thiết để giải thích nguồn gốc của các ngưỡng (threshold), ví dụ một số ngưỡng của fatigue_words đến từ thực nghiệm khi tạo ra các tác phẩm dài kỳ. Việc này không phải để tương thích với YAML cũ, mà là để những người bảo trì trong tương lai biết tại sao các ngưỡng mặc định lại tồn tại và khi nào nên điều chỉnh chúng.

## Kết hợp quy tắc

Thứ tự kết hợp tuân theo "càng cụ thể càng được ưu tiên":

```text
System defaults
→ Kết quả biên dịch Global rules
→ Kết quả biên dịch Project rules
→ Kết quả biên dịch Startup prompt
→ Runtime user update
```

Các nguồn có độ ưu tiên cao hơn sẽ ghi đè các nguồn thấp hơn.

Việc kết hợp được thực hiện một cách xác định (deterministically) bởi Go: LLM chỉ chuẩn hóa ngôn ngữ tự nhiên của một nguồn duy nhất thành ứng cử viên `structured`/`preferences`, Go sẽ thực hiện ghi đè trường và ghép văn bản theo trình tự trên, thứ tự ưu tiên không giao cho LLM phán quyết.

- `structured`: Ghi đè theo field, field cùng tên của nguồn đến sau sẽ ghi đè nguồn trước đó.
- `preferences`: Không ghi đè lẫn nhau, được ghép thành văn bản dễ đọc theo thứ tự ưu tiên (nguồn ưu tiên cao xếp sau), để LLM có thể thấy được thứ tự của các nguồn.

Giới hạn đã biết: `preferences` được sắp xếp theo thứ tự ưu tiên, nhưng Go không giải quyết xung đột. Nếu người dùng đưa ra các sở thích mềm mâu thuẫn nhau trong một quãng chạy dài (ví dụ: trước đó bảo "bình tĩnh điềm đạm", sau đó lại bảo "lắm mồm"), cả hai dòng sẽ được giữ lại trong văn bản và do LLM cân nhắc dựa trên thứ tự và ngữ cảnh; những yêu cầu cần được ghi đè cứng (hard override) một cách xác định thì nên được biểu diễn dưới dạng trường `structured` có thể cơ học hóa.

## Lối vào lưu xuống đĩa (Persist entry)

Chuẩn hóa, kết hợp, lưu xuống đĩa là cùng một logic, nhưng có hai đối tượng gọi, phải được phân biệt rõ ràng, nếu không sẽ làm xáo trộn việc chuẩn bị khởi động với ngữ cảnh sáng tác chính:

- **Mở sách / Làm mới (Phía khởi động, mang tính xác định)**: Host / Luồng khởi động trực tiếp gọi logic này để tạo ra snapshot ban đầu, không đi vào vòng lặp (loop) sáng tác chính. Đây là một tác vụ chuẩn bị khởi động mang tính xác định.
- **Cập nhật trong lúc chạy (Hành động phán quyết can thiệp)**: Hành động `rules` do Arbiter phân loại được Host gọi trực tiếp qua `userrules.Service.AddRuntimeRule`, tái sử dụng cùng logic kiểm tra / kết hợp / lưu xuống đĩa, kết hợp quy tắc mới (không có điểm xuất phát tiến độ) vào snapshot với tư cách là `Runtime user update`.

(Về mặt thực thi, nên thu gọn logic này thành một service nội bộ để hai đối tượng gọi dùng chung; việc đặt tên cụ thể để lại cho bên triển khai.)

Dù đối tượng gọi là ai, cuối cùng đều ghi vào cùng một tệp `meta/user_rules.json`. Logic lưu xuống đĩa chỉ làm 3 việc:

1. Kiểm tra (validate) các trường có cấu trúc.
2. Kết hợp vào snapshot hiện tại của cuốn sách theo độ ưu tiên ở phần §Kết hợp quy tắc.
3. Trả về sự thật (fact) về quy tắc hoàn chỉnh sau khi đã được lưu.

Không làm:

- Không điều phối subagent.
- Không sửa đổi đại cương (outline).
- Không âm thầm nuốt đi các trường không hợp lệ (ghi lại và hạ cấp, xem §Thất bại và hạ cấp).
- Không trực tiếp lấy văn bản gốc làm prompt cuối cùng để tiêm vào.

Ví dụ về cập nhật lúc chạy: Người dùng nói "sau này sẽ như thế nào" (không có điểm xuất phát tiến độ) → Arbiter phán quyết đó là hành động `rules` → Host thông qua `AddRuntimeRule` chuẩn hóa mục đó → Được kết hợp vào snapshot với tư cách là `Runtime user update` với ưu tiên cao nhất → Luồng sự kiện (event stream) phản hồi lại.

## Phản hồi (Echo)

Mỗi lần tạo hoặc cập nhật snapshot `user_rules`, bắt buộc phải phản hồi lại kết quả chuẩn hóa cho người dùng:

```text
Đã tạo snapshot quy tắc của sách:
- Quy tắc cơ học: Mỗi chương 1200-1600 chữ; cấm dùng cụm từ "ở một mức độ nào đó"
- Sở thích phong cách: Nhân vật chính bình tĩnh điềm đạm; ít giải thích, dùng hành động và đối thoại nhiều hơn
- Chưa nâng cấp thành quy tắc cơ học: Dùng ít phép ẩn dụ (Không có ngưỡng rõ ràng, xử lý theo sở thích phong cách)
```

- Khởi động / Làm mới: Sử dụng lại khả năng in log quy tắc khởi động hiện có để in snapshot, không thêm cơ chế mới; trong trường hợp đồng sáng tác (cocreate), có thể gộp phản hồi vào bước xác nhận đồng sáng tác.
- Lúc chạy: Sau khi `AddRuntimeRule` thành công, sẽ phản hồi thông qua event stream ("Quy tắc sáng tác đã được cập nhật và lưu trữ").
- Hạ cấp: Khi `status=degraded`, phần phản hồi sẽ nói rõ nguồn nào không thể phân tích cú pháp, hệ thống hiện đang chạy theo raw preferences và có thể tạo lại snapshot.

Phản hồi không phải là chốt chặn phê duyệt lần 2; tác dụng của nó là cho người dùng biết hệ thống đã hiểu ra sao, để có thể tạo lại snapshot nếu phát hiện sai sót.

## Cách Agent tiêu thụ (tiếp nhận)

Tất cả các agent chỉ xem:

```json
working_memory.user_rules
```

Phân chia trách nhiệm:

- Architect: Dựa theo ý muốn về số chữ trong `preferences` để điều chỉnh mật độ cốt truyện và số lượng chương được chia nhỏ của mỗi chương.
- Writer: Sáng tác dựa theo các quy tắc cứng của `structured` và điều chỉnh phong cách dựa theo `preferences`.
- Editor: Đọc duyệt dựa trên cùng một bộ quy tắc.
- `commit_chapter`: Dùng `structured` để thực hiện kiểm tra cơ học và trả về các vi phạm (violations).

Writer không đọc lại để hiểu lại prompt khởi động ban đầu, cũng không đọc các tệp rules gốc.

## Phân loại can thiệp: Hướng đi của ba loại

Can thiệp trong lúc chạy được chia làm 3 loại dựa trên "muốn sửa cái gì":

- **Viết như thế nào** (Bút pháp / Phong cách / Chất lượng: Số chữ, dùng từ, từ cấm, cấu trúc câu, tỷ lệ đối thoại, định dạng tiêu đề, v.v.) → Hành động `rules` của Arbiter, được chuẩn hóa và kết hợp vào `meta/user_rules.json`. Ví dụ: "Mỗi chương 1500 chữ", "Tiêu đề chỉ dùng tiếng Trung", "Nhân vật chính nhìn chung là bình tĩnh điềm đạm", "Tỷ lệ đối thoại cao hơn một chút".
- **Viết cái gì** (Cốt truyện / Cấu trúc / Hướng phát triển nhân vật / Độ dài) → architect, đi vào compass / outline / hồ sơ nhân vật. Ví dụ: "Tập này viết nhiều về tuyến chiến đấu", "Từ chương 30 trở đi, giọng điệu nhân vật chính trở nên lạnh lùng", "Tăng lên 40 chương".
- **Sửa những gì đã viết** (Làm lại / Sửa đổi chương được chỉ định) → editor, đưa vào hàng đợi PendingRewrites.

Tiêu chí: **"Viết như thế nào" → rules; "Viết cái gì" → architect; "Sửa những gì đã viết" → editor**.

## Các bước triển khai

1. Thêm mới store `meta/user_rules.json`.
2. Thêm mới pass chuẩn hóa LLM độc lập (theo nguồn), dùng schema ràng buộc để xuất các ứng cử viên `structured/preferences/sources/uncertain`.
3. Thêm mới phần kết hợp mang tính xác định ở phía Go: Dựa theo độ ưu tiên để thực hiện ghi đè trường và ghép văn bản đối với các nguồn, tạo ra snapshot.
4. Thu gọn chuẩn hóa / kết hợp / lưu xuống đĩa thành một bộ logic để hai đối tượng gọi dùng chung: Phía khởi động gọi trực tiếp để tạo snapshot ban đầu; lúc chạy, hành động `rules` do can thiệp phán quyết sẽ tái sử dụng thông qua `AddRuntimeRule`. Khi thất bại, xử lý theo §Thất bại và hạ cấp: nguồn bị hạ cấp xuống raw preferences, snapshot `status=degraded`, quá trình sáng tác chính tiếp tục.
5. Chuyển các quy tắc cơ học mặc định của hệ thống trong `assets/rules/default.md` hiện tại vào struct được tích hợp trong code hoặc JSON asset, giữ lại các chú thích về nguồn gốc của ngưỡng; xóa đường dẫn phân tích cú pháp YAML của user rules, không làm tầng tương thích.
6. Sau khi đọc file rules, sẽ không tiêm trực tiếp chính văn vào làm prompt nữa, mà sẽ chuẩn hóa nó rồi kết hợp vào snapshot `user_rules`.
7. `novel_context` chỉ tiêm `working_memory.user_rules` nằm trong `meta/user_rules.json`.
8. `commit_chapter` dùng chung `user_rules.structured` để kiểm tra.
9. Phân luồng can thiệp (hiện do Arbiter đảm nhận, arbiter-intervention.md) được làm rõ thành 3 luồng dựa theo "muốn sửa cái gì": Yêu cầu dài hạn về phong cách / chất lượng sáng tác sẽ đi qua hành động `rules` để lưu vào snapshot; cốt truyện / cấu trúc / nhân vật / độ dài sẽ đi vào architect; các chương đã viết cần làm lại sẽ đi vào editor (chi tiết xem §Phân loại can thiệp: Hướng đi của ba loại).

## Tiêu chuẩn nghiệm thu

- Người dùng viết "Mỗi chương 1200-1600 chữ" trong prompt khởi động, `novel_context` của Writer ở chương đầu tiên có thể nhìn thấy đoạn văn bản nguyên gốc mong muốn này trong `preferences`.
- File rules chỉ viết bằng ngôn ngữ tự nhiên cũng có thể được chuẩn hóa vào cùng một `user_rules` khi tạo snapshot.
- File rules không cần và không hỗ trợ YAML; toàn bộ được chuẩn hóa như quy tắc ngôn ngữ tự nhiên.
- Lúc chạy không đọc file rules nữa; chỉ đọc `meta/user_rules.json`.
- Quy tắc cơ học mặc định không còn lấy từ file YAML rules nữa, và user rules cũng không có tầng tương thích YAML.
- Việc chuẩn hóa không dùng regex/hardcode từ khóa; việc hiểu ngôn ngữ tự nhiên được thực hiện bởi LLM.
- Các quy tắc mơ hồ sẽ không được nâng cấp thành các trường `structured` cấp độ error.
- Quy tắc mặc định của hệ thống không đi qua LLM mà tiến thẳng vào bước kết hợp của Go.
- Độ ưu tiên của nguồn và việc ghi đè trường được Go thực thi một cách xác định, cùng đầu vào sẽ tạo ra cùng snapshot.
- Trong lúc chạy, người dùng nói "sau này sẽ như thế nào", thông qua hành động rules của Arbiter sẽ được kết hợp vào snapshot, `novel_context` của các chương tiếp theo có thể nhìn thấy bản cập nhật.
- Lỗi chuẩn hóa không cản trở việc viết sách: Nguồn bị lỗi sẽ bị hạ cấp xuống raw preferences, snapshot `status=degraded`, quá trình sáng tác chính vẫn tiếp tục; chỉ khi snapshot không thể lưu xuống đĩa thì mới hủy bỏ.
- Khi chuẩn hóa thất bại sẽ trả về `status=degraded`, không đẩy lỗi kỹ thuật lên trên làm ô nhiễm luồng chính.
- Sau khi tạo hoặc cập nhật snapshot, sẽ phản hồi (echo) lại `structured` / `preferences` / các mục chưa được nâng cấp; khi hạ cấp, phần phản hồi sẽ ghi rõ nguồn bị hạ cấp.
- Việc mở một cuốn sách mới sẽ không kế thừa `user_rules` của cuốn sách trước.
- Các trường có cấu trúc không hợp lệ sẽ không bị bỏ qua một cách im lặng: Hệ thống sẽ ghi nhận và hạ cấp nguồn đó, không cản trở luồng chính.

## Nói rõ những gì sẽ không làm (Nhận định là không cần thiết, không phải chia giai đoạn)

Các khả năng sau không mang lại lợi ích gì trong nhu cầu hiện tại, vì vậy không được đưa vào thiết kế để tránh thiết kế quá mức:

- Các ngữ nghĩa xóa / hoàn tác ở cấp độ trường (field) như `clear_fields`.
- Tự động làm mới khi nghe thấy sự thay đổi của file rules (Chỉ cần tạo lại snapshot một cách rõ ràng nếu có sửa file).
- Thời điểm neo / giải quyết ghi đè của `preferences` (Nếu cần ghi đè cứng (hard override), hãy dùng `structured`).
- Lưu trữ liên tục mảng `diagnostics` trong snapshot (Các lỗi kỹ thuật chỉ cần ghi vào log, snapshot chỉ giữ `status`).
- Tự động tạo mô tả cho trường schema từ kiểu dữ liệu Go (Chỉ cần duy trì thủ công một bản mô tả ngắn gọn là được).

Nguyên tắc thiết kế không đổi: LLM chịu trách nhiệm hiểu ngôn ngữ tự nhiên, Go chịu trách nhiệm kết hợp xác định, kiểm tra tính hợp lệ, lưu xuống đĩa và check.
