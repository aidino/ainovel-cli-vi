# Thiết kế tầng văn phong (Voice Layer)

> Trạng thái: Thiết kế đã chốt v2 (2026-07-12 tiếp thu đánh giá bên ngoài: bổ sung ngữ nghĩa ghi đè, ngữ nghĩa đường dẫn, thứ tự lắp ráp, đầu vào eval, giao thức thống kê đánh giá), **có thể triển khai**.
> Ưu tiên: Đi trước việc nâng cấp mặt phẳng điều khiển (docs/engine-arbiter.md) —— "Mùi AI" là một điểm đau (pain point) nhức nhối của người dùng.

## I. Bối cảnh và xác định vấn đề

Người dùng phản hồi nội dung được tạo có "mùi AI nặng". Kết luận sau khi điều tra: **Vấn đề không phải là kiến thức văn phong và quy trình gắn kết quá sâu, mà là vòng lặp lặp lại bị đứt gãy ở hai chỗ**:

1. **Thay đổi một lần phải biên dịch lại** —— Toàn bộ tài sản ngữ nghĩa văn phong (anti-ai-tone.md, tiêu chuẩn sáng tác của writer.md, styles/*.md) đều là `go:embed`, chỉnh sửa một từ ngữ cũng phải build và phát hành lại;
2. **Không có vòng lặp đo lường chuyên dụng cho văn phong** —— Sau khi sửa chỉ có thể dựa vào cảm giác khi đọc, không có sự đối chiếu khách quan trước và sau, việc tối ưu hóa trở thành môn học huyền bí.

## II. Kiểm kê hiện trạng (Tài sản liên quan đến văn phong tổng cộng có 5 tầng)

| Tầng | Vị trí | Hiện trạng | Người dùng có thể điều chỉnh |
|----|------|------|---------|
| Tiêu chí ngữ nghĩa | `assets/references/anti-ai-tone.md` | Writer tránh + editor đưa ra bằng chứng dùng chung, chia làm 5 loại: cấu trúc/dùng từ/miêu tả/đối thoại/nhịp điệu | ❌ Nhúng sẵn |
| Tiêu chuẩn sáng tác | `assets/prompts/writer.md` §Tiêu chuẩn sáng tác | Trộn lẫn với giao thức thực thi trong cùng một file nhúng sẵn | ❌ |
| Thiết lập phong cách (preset) | `assets/styles/*.md` (4 file) | Chọn đơn điểm qua cfg.Style, thêm vào writer prompt | ❌ và không thể thêm mới |
| Quy tắc cơ học (mechanical rules) | `internal/rules` | Từ ngữ gây mệt mỏi/từ cấm/số chữ, commit bắt buộc kiểm tra | ✅ Đã có 3 tầng ghi đè (Lưu ý: "cấp dự án" của nó gắn với **cwd**, xem 3.4) |
| Tùy chọn lúc chạy (runtime preferences) | Hành động `rules` của Arbiter | Ngôn ngữ tự nhiên → Cấu trúc hóa, có hiệu lực qua các lần khởi động lại | ✅ |

Ngoài ra còn có hai nền tảng cơ sở hạ tầng quan trọng: **stylestat** (thống kê tic câu ở cấp độ toàn bộ cuốn sách, feed lại cho writer làm "gương soi câu cửa miệng", mã thuần túy không có ảo giác) và **`OverridePrompt` của eval** (cơ sở hạ tầng prompt A/B đã tồn tại).

Kết luận: Tính linh hoạt của tầng cơ học và nguyên liệu đo lường đã sẵn sàng, lỗ hổng tập trung ở **tầng ngữ nghĩa không thể ghi đè** và **vòng lặp đo lường không nhắm đúng vào văn phong**.

## III. Thiết kế

### 3.1 Nguyên tắc cốt lõi

**Tách "viết như thế nào" (văn phong) ra khỏi "hợp tác như thế nào" (giao thức): cái trước được dữ liệu hóa, có thể ghi đè; cái sau giữ nguyên việc nhúng khi biên dịch.**

### 3.2 Tách writer.md: Điền ngược placeholder tại chỗ

Phần tiêu chuẩn sáng tác của writer.md nằm ở **giữa** file (sau giao thức thực thi, trước tính liên tục của nhân vật phụ), không thể chỉ đơn giản là nối vào đuôi. Sử dụng phương án placeholder:

- `writer.md` (giao thức, nhúng sẵn): Giữ nguyên giao thức thực thi / tiếp tục chạy từ điểm dừng / làm lại và đánh bóng / giao ước chương / giải thích cơ chế tùy chọn người dùng / **toàn bộ phần số chữ (bao gồm gợi ý cách viết)** / tính liên tục của nhân vật phụ / tham số commit; vị trí ban đầu của phần tiêu chuẩn sáng tác được thay thế bằng một placeholder duy nhất `{{VOICE}}`
- `voice.md` (văn phong, có thể ghi đè): Toàn bộ phần tiêu chuẩn sáng tác (khử mùi AI / tính đa dạng của câu / không kể lại chuyện cũ)

Gợi ý cách viết số chữ được giữ lại trong file giao thức (tiếp thu đánh giá ngày 2026-07-12): nó gắn kết chặt chẽ với việc thực thi giao ước số chữ, tách ra sẽ cần placeholder thứ hai, biến Voice thành định dạng nhiều đoạn —— không đáng để làm vậy cho một đoạn văn bản kỹ năng mà rất ít người muốn ghi đè; tùy chọn của người dùng đối với số chữ sẽ đi qua user_rules. Tên file giữ nguyên là `writer.md` không đổi (`OverridePrompt` của eval lấy tên file làm khóa, đổi tên sẽ làm tăng thêm sự rắc rối khi đấu nối).

**Thứ tự lắp ráp phải tương thích từng byte với hiện trạng**. Hiện trạng là `writer.md → simulationGuidance → style` (assets/load.go:84 + agents/build.go:247), do đó hàm lắp ráp duy nhất là:

```go
// Đầu vào duy nhất cho sản xuất, eval, kiểm thử; {{VOICE}} điền ngược tại chỗ đảm bảo việc tách không làm mất mát gì
func BuildWriterPrompt(protocolTemplate, voice, simulationGuidance, style string) string
// = replace(protocolTemplate, "{{VOICE}}", voice) + simulationGuidance + style
```

Bài học kinh nghiệm: Comment của `WithSimulationGuidance` từng ghi lại một cái hố là "baseline có bao bọc, variant không có → A/B không tương đương"; sự phân nhánh của đường dẫn lắp ráp là mầm mống của các sự cố tương tự, vì vậy cần thu gọn về một hàm duy nhất.

### 3.3 Mô hình ghi đè: Ngữ nghĩa từng tài sản (không mơ hồ)

| Tài sản | Ngữ nghĩa ghi đè | Lý do |
|------|---------|------|
| `voice.md` | **Bổ sung (Append)**: Nội dung tích hợp sẵn được giữ lại, ghi đè toàn cục/của sách được thêm vào như đoạn đánh dấu | Thay thế toàn bộ file sẽ khiến người dùng mãi kẹt lại ở phiên bản mặc định cũ; nhu cầu phổ biến là tinh chỉnh chứ không phải viết lại toàn bộ |
| `anti-ai-tone.md` | **Bổ sung** (như trên) | Nhu cầu phổ biến là bổ sung tiêu chí; người dùng muốn lật đổ các tiêu chí tích hợp sẵn thuộc nhóm thiểu số, không thiết kế cho họ |
| `styles/<name>.md` | **Thay thế toàn bộ file cùng tên**; Tên file mới tức là thêm phong cách mới | Phong cách là âm thanh tổng thể, việc kết hợp hai phong cách không có ý nghĩa |
| `genres/<name>/style-references.md` | Thay thế toàn bộ file cùng tên; Khi custom style không có reference, **cho phép bỏ trống, không lùi về default** (tham chiếu sai còn tệ hơn là không có) | Như trên |
| user_rules | Ưu tiên cao nhất lúc chạy (giữ nguyên hiện trạng) | — |

Việc lắp ráp ngữ nghĩa bổ sung đi kèm với các dấu hiệu phân định ranh giới rõ ràng:

```
## Văn phong mặc định của dự án
...
## Ghi đè văn phong toàn cục của người dùng (Các yêu cầu dưới đây ưu tiên hơn mặc định của dự án)
...
## Ghi đè văn phong của cuốn sách này (Các yêu cầu dưới đây ưu tiên hơn tất cả những điều trên)
...
```

**Ranh giới trung thực**: "Cái sau thắng" trong ngữ nghĩa bổ sung là chỉ thị ưu tiên cho LLM, không phải là sự đảm bảo cơ học —— văn phong là nội dung mang tính chất gợi ý, có thể chấp nhận được; các ràng buộc cần đảm bảo cơ học sẽ đi qua tầng rules (đó là ghi đè thực sự). Ranh giới này được ghi vào tài liệu người dùng.

`arc-templates.md` thuộc về mặt phẳng quy hoạch (định hình cấu trúc câu chuyện chứ không phải âm thanh), **không đưa vào danh sách trắng v1**, ghi lại để bàn sau.

### 3.4 Ngữ nghĩa đường dẫn: Ràng buộc outputDir ở cấp cuốn sách, không ràng buộc cwd

```
Cấp cuốn sách   <outputDir>/style/     >   Toàn cục   ~/.ainovel/style/   >   Mặc định tích hợp (dự phòng qua embed)
```

- Ràng buộc outputDir làm cho Voice **đi theo cuốn sách**: Đổi thư mục khôi phục cùng một cuốn sách thì sẽ load cùng một bản văn phong; Phân tích cú pháp đường dẫn Docker/headless/TUI nhất quán; Khi nhiều cuốn sách dùng chung cwd sẽ không bị xung đột lẫn nhau
- Chữ ký của `assets.Load` nhận rõ ràng thư mục gốc để phân tích (outputDir), **bên trong không đọc cwd**
- Lưu ý sự khác biệt với tầng rules: `./.ainovel/rules` của rules được liên kết với cwd (internal/rules/loader.go đã có quy ước, thiết kế này không động đến nó); Tài liệu người dùng nêu rõ ngữ nghĩa của cả hai là khác nhau —— rules là "cấp dự án" (project-level), còn voice là "cấp cuốn sách" (book-level)

Cấu trúc thư mục đầy đủ của người dùng:

```
<outputDir>/style/            (Cấu trúc tương tự ~/.ainovel/style/)
  voice.md                    Đoạn bổ sung
  anti-ai-tone.md             Đoạn bổ sung
  styles/
    xianxia.md                Thêm mới hoặc thay thế cùng tên
  genres/
    xianxia/
      style-references.md     Tùy chọn
```

Tên style chính là tên file, kiểm tra tính hợp lệ qua regex `[a-z0-9-]+`, từ chối các ký tự đường dẫn.

### 3.5 Tại sao mở cho người dùng là an toàn

Các bất biến của giao thức đều nằm ở **tầng sự thật (fact layer)**: draft có trước check, commit bắt buộc kiểm tra các quy tắc cơ học, chặn khi vượt quá giới hạn số chữ, checkpoint mang tính lũy đẳng (idempotent) —— những điều này không nằm trong prompt. Cho dù người dùng có sửa voice.md tệ đến mức nào, thì các chốt chặn và tiền điều kiện của công cụ vẫn có hiệu lực bình thường, kết quả tệ nhất là văn phong viết ra rất khó đọc, state machine (máy trạng thái) không thể hỏng.

### 3.6 Thời điểm có hiệu lực và đầu vào eval

- Phân tích cú pháp lúc khởi động v1, **khởi động lại sẽ có hiệu lực** (Khôi phục điểm dừng chính xác đến từng bước, chi phí khởi động lại gần như bằng không; không làm tính năng tải lại nóng - hot reload)
- eval thêm **đầu vào variant độc lập cho voice** (ví dụ: `Bundle.OverrideVoice(raw)`), bên trong đi qua cùng một đường dẫn `BuildWriterPrompt` —— Cấm làm văn phong A/B thông qua việc ghi đè toàn bộ writer.md (sẽ kéo theo cả giao thức, và giao thức của baseline/variant có thể không tương đương nhau)

## IV. Vòng lặp đo lường: Tập đánh giá văn phong

```
Sửa voice/anti-ai-tone
  → Tập đánh giá văn phong (test case cố định, eval voice-variant A/B)      ← Tính năng mới duy nhất
  → So sánh chỉ số stylestat (chỉ số cứng xác định)
  + LLM judge chấm điểm và đưa ra bằng chứng từng mục theo tiêu chí anti-ai-tone (Giai đoạn đầu chỉ báo cáo, không dùng làm hard gate)
```

Giao thức thống kê (Đầu vào cố định chỉ đảm bảo **có thể so sánh được**, không đảm bảo có thể tái hiện được):

- baseline/variant khóa cùng một model và tham số suy luận
- Mỗi test case lặp lại N≥3 lần, báo cáo giá trị trung bình, phương sai và các mẫu gốc
- judge đánh giá mù (blind eval) (không tiết lộ danh tính baseline/variant)
- Test case bao phủ Thể loại × Loại chương (Mở đầu/Thúc đẩy câu chuyện hàng ngày/Cao trào/Kết thúc)

## V. Những điều rõ ràng sẽ không làm (Ngăn ngừa thiết kế quá mức)

- Không mở prompt giao thức cho người dùng cuối (`OverridePrompt` được giữ lại như một khả năng nội bộ của eval)
- Không làm tính năng tải lại nóng (hot reload) lúc đang chạy
- Không mở regex pattern của stylestat làm cấu hình cho người dùng (Lối vào mở rộng của tầng cơ học đã có: fatigue_words/forbidden_phrases của rules)
- Không làm chợ phong cách / cơ chế chia sẻ (chỉ cần copy thư mục style là có thể chia sẻ một cách tự nhiên)
- arc-templates không được đưa vào danh sách trắng v1

## VI. Các bước thực hiện và nghiệm thu

1. Tách writer.md (placeholder `{{VOICE}}`) + Hàm lắp ráp duy nhất `BuildWriterPrompt`
2. Trình phân tích cú pháp 3 tầng: `assets.Load(outputDir, style)` + Ngữ nghĩa từng tài sản (Bảng 3.3) + Kết hợp phép liệt kê styles; Unit test bao phủ tính ưu tiên/dự phòng khi thiếu/đánh dấu ranh giới phần bổ sung
3. Đầu vào `OverrideVoice` của eval
4. Tài liệu người dùng: Cấu trúc thư mục, ngữ nghĩa từng tài sản, sự khác biệt về ngữ nghĩa đường dẫn giữa rules và voice, các ví dụ
5. Tập đánh giá văn phong (Có thể làm sau như một task độc lập)

**Tiêu chuẩn nghiệm thu**: ① Khi không có bất kỳ file ghi đè nào, đầu ra của `BuildWriterPrompt` **nhất quán từng byte** so với trước khi tách; ② Mức độ ưu tiên 3 tầng và ngữ nghĩa bổ sung/thay thế có unit test được điều khiển bằng bảng (table-driven test); ③ Sau khi thêm `styles/xianxia.md`, cấu hình `style: xianxia` có thể dùng được ngay; ④ A/B eval voice và luồng lắp ráp trong production phải cùng một đường dẫn (có test chứng minh); ⑤ Toàn bộ bộ test và sim regression đều pass.

## VII. Mối quan hệ với việc phát triển mặt phẳng điều khiển

Hoàn toàn trực giao (mặt phẳng nội dung vs mặt phẳng điều khiển), không phụ thuộc nhau khi thực hiện. Thứ tự quy ước: **Tầng văn phong → Tập đánh giá văn phong → Engine/Arbiter (Thúc đẩy theo nghị quyết §VIII trong tài liệu của nó)**.
