# Đường ống nhập ngữ nghĩa tiểu thuyết ngoại vi (Import Pipeline)

> Trạng thái: Đã triển khai (v1, `internal/host/imp`; vớt vát tiền tố bị cắt cụt bao gồm giai đoạn 3·bổ sung)
> Ngày: 2026-07-15
> Mục tiêu: Làm cho việc nhập tiểu thuyết ngoại vi vừa liên tục nhận được lợi ích từ việc nâng cấp khả năng của model, vừa có sự đảm bảo về mặt kỹ thuật như: không mất mát chính văn, có thể chẩn đoán khi thất bại, có thể khôi phục khi sập, và có thể xác minh khi phát hành.
> Sửa đổi: Thứ tự SourceUnit dựa theo giá trị `(Line, Part)` (§7.3/§8.3); Vớt vát tiền tố bị cắt cụt giáng cấp thành tối ưu hóa hiệu suất có thể đặt ở phía sau và yêu cầu có thể quan sát được (§9.5/§13.3/§19); Các mức của model hàm ngữ nghĩa mở thành các núm xoay (knobs) (§13.1/§17).
> Sửa đổi 2026-07-16: Núm xoay mức độ model được triển khai vào cấu hình roles `import_segment/import_analyze/import_synthesize` (§13.1); Việc chia cắt lại bằng ngôn ngữ tự nhiên được triển khai thành `--guide` và đầu vào ngữ nghĩa `guidance.txt` trong không gian làm việc (§18.3); Các thất bại về mặt ngữ nghĩa thống nhất lưu phản hồi gốc vào failures/ (§14.2); Xác nhận chia cắt hỗ trợ nhấn `y` trong bảng điều khiển để thả hành một lần (§8.4); Việc nhập chưa hoàn thành sẽ chủ động nhắc nhở khi khởi động (§18.2). Chế độ JSON Schema (§13.2 cấp 1) tạm thời chưa triển khai, đánh dấu TODO chờ cải tạo thống nhất với các điểm gọi model khác trong toàn kho.

## 1. Tóm tắt trong một câu

Nhập (import) không phải là "dùng biểu thức chính quy cắt văn bản, rồi để model nhả toàn bộ cuốn sách ra JSON trong một lần", cũng không phải là một Import Agent chạy tự do; nó là một **đường ống biên dịch ngữ nghĩa chia theo giai đoạn (phased semantic compilation pipeline)**:

> Model chịu trách nhiệm hiểu ngữ nghĩa mở, mã nguồn chịu trách nhiệm về tọa độ, độ bao phủ, kiểu, hash, trình tự và tính lũy đẳng (idempotent); toàn bộ các sản phẩm ngữ nghĩa sau khi xác minh xong trong không gian làm việc độc lập, mới được phát hành thành trạng thái sách chính thức.

```text
Văn bản ngoại vi
  → Đọc và chuẩn hóa xác định
  → LLM nhận diện ranh giới chương/tập/văn bản phụ trợ
  → Mã nguồn xác minh độ bao phủ toàn văn bản
  → Người dùng xác nhận chia cắt (có thể ủy quyền rõ ràng để tự động chấp nhận)
  → LLM trích xuất sự kiện từng chương theo lô chương liên tục
  → LLM tổng hợp ngữ nghĩa toàn sách phân tầng
  → Mã nguồn lắp ráp và xác minh Foundation
  → Phát hành lũy đẳng Foundation và các chương
  → Mặc định tạm dừng một lần; phải truyền rõ ràng --continue mới tiếp sức theo cổng chặn bình thường
```

## 2. Tại sao phải tái cấu trúc

Triển khai hiện tại là:

```text
Cắt chương bằng biểu thức chính quy (Regex)
  → Toàn bộ chính văn các chương cùng lúc đưa vào ReverseFoundation
  → Model xuất ra premise / characters / world_rules / đại cương toàn bộ chương / compass trong một lần
  → Lập tức ghi vào Foundation chính thức
  → Sau đó lại đọc từng chương cùng một chính văn, phân tích và commit
```

Nó có 4 vấn đề mang tính cấu trúc.

### 2.1 Việc cắt chương đang cố gắng liệt kê ngữ nghĩa mở

Tiêu đề chương không có cú pháp đóng. Việc tiếp tục thêm các regex như "Chương N", "Tập N", "Chapter N" chỉ có thể bao phủ những định dạng đã thấy, không thể bao phủ các tiêu đề tự định nghĩa của tác giả, dàn trang hỗn hợp, phân cấp tập-chương và các định dạng tương lai.

Nghiêm trọng hơn, việc chia cắt hiện tại sẽ khiến các ranh giới không trúng đích trực tiếp biến mất khỏi kết quả, và có thể âm thầm vứt bỏ văn bản trước tiêu đề đầu tiên, chương rỗng và nội dung bị phán đoán là nhiễu phần đuôi. Mã nguồn không thể chứng minh những nội dung này đáng bị vứt bỏ.

### 2.2 Đầu vào và đầu ra khi gọi Foundation đều tăng tuyến tính theo số chương

`ReverseFoundation` đồng thời đảm nhận việc hiểu toàn sách và tạo đại cương cho toàn bộ chương: Đầu vào chứa toàn bộ chính văn, đầu ra chứa cấu trúc chi tiết của mỗi chương. 54 chương đã có thể bị cắt cụt JSON; tăng `max_tokens` chỉ đẩy điểm thất bại về phía những cuốn sách dài hơn.

### 2.3 Đã sửa trạng thái chính thức trước khi thất bại

Foundation và chương vừa được phân tích vừa được phát hành. Khi các bước sau thất bại, thứ người dùng nhận được là một trạng thái sách chính thức hoàn thành nhập một nửa, một nửa chưa phân tích. Tùy chọn `from=N` hiện tại chỉ giả định rằng người dùng biết khôi phục từ đâu, không thể chứng minh file nguồn, kết quả chia cắt và các chương đã có vẫn nhất quán.

### 2.4 Nhiều kết luận ngữ nghĩa bị hardcode (mã hóa cứng)

Phương án hiện tại còn cố định:

- Chính văn nhập vào chỉ có thể là một tập;
- Chỉ có thể chia thành 1~3 arc;
- Chọn short/mid/long dựa trên ngưỡng 25/80 chương đã nhập;
- Để cho phép viết tiếp nên có xu hướng gượng ép tạo ra `open_threads`;
- Mỗi chương bắt buộc phải có nhân vật, số lượng sự kiện cố định và loại móc nối (hook) cố định.

Những điều này không phải là sự kiện có thể chứng minh máy móc từ định dạng file, mà nên do model phán đoán dựa trên chính văn, hoặc do người dùng bày tỏ ý định rõ ràng.

## 3. Mục tiêu và phi mục tiêu

### 3.1 Mục tiêu

1. **Hiểu được định dạng mở**: Không yêu cầu người dùng đổi tiểu thuyết thành định dạng tiêu đề tích hợp sẵn, cũng không yêu cầu người dùng viết regex.
2. **Toàn văn bản phải rõ ràng**: Mỗi đoạn văn bản nguồn không rỗng phải thuộc về một chương rõ ràng hoặc khu vực phụ trợ, cấm vứt bỏ âm thầm.
3. **Quy mô có thể kiểm soát**: Không còn việc dùng một lần gọi để đọc toàn bộ chính văn và xuất ra toàn bộ đối tượng chương; việc phân đoạn, chia lô chương theo ngân sách kép và tổng hợp khoảng đều có ranh giới đầu vào đầu ra cục bộ, đầu ra toàn cục chỉ tăng theo độ phức tạp ngữ nghĩa thực sự như nhân vật, tập arc.
4. **Thất bại không làm ô nhiễm**: Trước khi phân tích ngữ nghĩa và xác minh Foundation hoàn tất, không ghi trạng thái sáng tác chính thức.
5. **Khôi phục chính xác**: Khôi phục dựa vào bản chụp nguồn và `InputDigest` của công cụ, không phụ thuộc vào `from=N` hoặc trí nhớ người dùng.
6. **Lợi tức model đến trực tiếp**: Model mạnh hơn trực tiếp cải thiện việc nhận diện ranh giới, trích xuất sự kiện, chia tập arc và phán đoán viết tiếp, không cần thêm quy tắc Go.
7. **Tái sử dụng ngữ nghĩa commit chính thức**: Việc phát hành chương tiếp tục sử dụng các khả năng lũy đẳng như PendingCommit, checkpoint và digest của `commit_chapter`.
8. **Hoàn toàn có thể quan sát**: Tiến độ, danh tính model, mức sử dụng (usage), phản hồi lỗi gốc và lỗi cuối cùng đều có nơi ghi rõ ràng.
9. **Tương tác và tự động hóa cùng tồn tại**: Mặc định để người dùng xác nhận ranh giới ngữ nghĩa rủi ro cao, đồng thời cung cấp ủy quyền không người trực rõ ràng; luồng tự động không phụ thuộc vào việc đoán mò âm thầm.

### 3.2 Phi mục tiêu

- Không xây dựng Coordinator hoặc vòng lặp dài Agent đa dụng.
- Không xây dựng framework Workflow/PolicyEngine/đồ thị nhiệm vụ đa dụng.
- Không tự động sửa chữa hoặc viết lại nguyên bản của người dùng.
- Không triển khai cơ sở dữ liệu, truy xuất vector hay song song phân tán cho việc nhập.
- Không hỗ trợ trộn mờ một cuốn tiểu thuyết khác vào cuốn sách đã có.
- Không triển khai việc di chuyển trạng thái `from=N` cũ hoặc tương thích song song.
- Không mở rộng EPUB/PDF trong RFC này; phiên bản đầu tiên vẫn chỉ nhận txt/md, tầng đọc giữ nguyên cục bộ, tương lai có thể thay thế mà không đổi hợp đồng phía sau.

## 4. Ranh giới Trách nhiệm

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Giải mã byte, chuẩn hóa ngắt dòng | Go | Định dạng file và chuyển đổi xác định |
| Vị trí nguồn nào là tiêu đề chương, tiêu đề tập hoặc văn bản phụ trợ | LLM | Ngữ nghĩa mở, không thể liệt kê |
| Tiêu đề tương ứng với vị trí nguồn ổn định nào | Go | SourceUnit, điểm neo nguyên bản và phạm vi byte có thể xác minh máy móc |
| Chuyện gì đã xảy ra trong một chương | LLM | Hiểu ngữ nghĩa văn học |
| Nhân vật, quy tắc thế giới, chi tiết gieo mầm và mối quan hệ được quy nạp ra sao | LLM | Quy nạp ngữ nghĩa liên chương |
| Ranh giới tập arc, câu chuyện đã thu hẹp chưa, cấp độ quy hoạch | LLM | Phụ thuộc vào hình dạng tự sự, không phụ thuộc vào ngưỡng cố định |
| Phạm vi chương có tăng dần, không chồng chéo, bao phủ toàn bộ không | Go | Các bất biến có thể chứng minh |
| Kiểu JSON, liệt kê tập đóng, số chương tham chiếu có hợp lệ không | Go | Hợp đồng được định kiểu |
| Có thể tái sử dụng phân tích đã có không | Host/Workspace | Đầu vào ngữ nghĩa thật tái tạo được cùng `InputDigest` thì mới được tái sử dụng |
| Khi nào ghi trạng thái sách chính thức | Host/Store | Giao thức phát hành và khôi phục sự cố |
| Có ủy quyền tiếp tục cắt theo hiện tại không | Người dùng/Intent | Tương tác xác nhận hoặc `--yes` rõ ràng, không do mã nguồn đoán trộm |

Các lệnh gọi LLM ở đây không phải là mặt điều khiển Arbiter, cũng không phải vòng lặp sáng tác Worker. Chúng là các **hàm ngữ nghĩa** có ranh giới rõ ràng: Sự kiện định kiểu đi vào, kết quả ngữ nghĩa định kiểu đi ra, sau khi Host xác minh thì thực thi.

## 5. Kiến trúc Tổng thể

```text
[TUI / Headless]
       │ /import <path> / Ủy quyền tự động / Xác nhận / Hủy
[Host]
       │ Độc chiếm vòng đời nhập, sự kiện, thời gian chạy model
[imp.Runner]
       ├── LoadState → NextAction (Chỉ suy diễn từ sự kiện không gian làm việc)
       ├── Source     Đọc, giải mã, chuẩn hóa, bản chụp
       ├── Segment    Phóng chiếu cấu trúc → LLM nhận diện ranh giới → Xác minh độ bao phủ
       ├── Analyze    Lô liên tục ngân sách kép → Lưu tạm sự kiện từng chương
       ├── Synthesize Quy nạp phân tầng → BookSynthesis
       ├── Validate   Lắp ráp và xác minh Foundation hoàn chỉnh
       └── Publish    Foundation chính thức → commit_chapter
               │
[Không gian làm việc meta/import]    [Store chính thức]
Kết quả bản chụp nguồn/cắt/phân tích/tổng hợp      Progress/Checkpoint/Artifact/PendingCommit
```

Runner là sự điều phối theo giai đoạn có tính xác định thông thường, không sở hữu khả năng quyết định tự do. Mỗi lần nó chỉ thực thi một hành động do `NextAction` suy ra, hành động hoàn thành thì đọc lại sự kiện.

## 6. Không gian làm việc và Suy diễn Trạng thái

Các sự kiện trong quá trình nhập được lưu trong thư mục sách:

```text
meta/import/
├── manifest.json
├── intent.json
├── source.txt
├── guidance.txt          # Khi tồn tại: Hướng dẫn cắt bằng ngôn ngữ tự nhiên của người dùng (--guide), là đầu vào ngữ nghĩa của segmentation
├── segmentation.json
├── confirmation.json
├── analyses/
│   ├── 000001.json
│   ├── 000002.json
│   └── ...
├── range-digests/
│   ├── 000001-000050.json
│   └── ...
├── synthesis.json
├── story-resolution.json
└── failures/
    ├── last.json
    └── last-response.txt
```

Phiên bản đầu tiên giữ lại không gian làm việc. Nó vừa là căn cứ khôi phục, vừa là hồ sơ kiểm toán nhập; không thêm cơ chế tự động dọn dẹp và lưu trữ lịch sử.

`intent.json` lưu ủy quyền rõ ràng của người dùng khi khởi động nhập (tự động xác nhận, chọn trước trạng thái câu chuyện uncertain, có bỏ qua Hold hoàn thành không). Đây là những ý định của người dùng phải tuân thủ cả sau khi khôi phục, không phải trạng thái giai đoạn đoán được từ các công cụ; sau khi tạo thì không bị Runner âm thầm ghi đè.

### 6.1 Manifest

```go
type ImportManifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

type ImportIntent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}
```

- `source.txt` là bản chụp cục bộ sau khi chuẩn hóa, việc khôi phục không còn phụ thuộc vào việc đường dẫn gốc có tồn tại hay không;
- Manifest không lưu đường dẫn nguồn tuyệt đối, tránh lộ thư mục máy và loại bỏ vấn đề khôi phục do di chuyển file;
- Intent chỉ nhận giá trị tập đóng, lưu chính xác ủy quyền của người dùng trong lệnh khởi động; khi khôi phục không suy ngược ý định cũ từ advance mode hiện tại;
- Khi phiên bản schema không khớp, yêu cầu rõ ràng phải dùng phiên bản khớp để tiếp tục hoặc nhập lại, không đoán mò di chuyển.

Khi tạo lần đầu, trước tiên ghi đủ và xác minh manifest, intent, source vào thư mục tạm cùng cấp, sau đó mới dùng rename thư mục để phát hành thành `meta/import/`; nếu `meta/import/` không tồn tại thì không tính là không gian làm việc hoạt động. Như vậy bộ 3 khởi tạo sẽ không đi vào `NextAction` với hình thái bán khởi tạo, và cũng không cần thêm `stage=initializing` cho quá trình tạo. Khi khởi động phát hiện thư mục khởi tạo còn sót lại thì phải nhắc nhở rõ ràng và giữ lại thông tin chẩn đoán, không tự động coi là không gian làm việc thành công, cũng không âm thầm xóa đi.

### 6.2 Không lưu các liệt kê giai đoạn dễ bị trôi dạt

Trạng thái bền vững không ghi các trường điều khiển như `stage=analyzing`, `current=37`. Hành động tiếp theo suy ra từ các công cụ:

```text
Không có manifest/intent/source   → ingest
Không có segmentation            → segment
Không có confirmation khớp với tóm tắt đầu vào segmentation → await_confirmation / auto_confirm
Tồn tại phân tích chương thiếu hoặc tóm tắt đầu vào không khớp → analyze_first_missing
Thiếu RangeDigest hoặc synthesis khớp với đầu vào      → synthesize_first_missing
story_status=uncertain và không có lựa chọn khớp của người dùng → await_story_resolution
Công cụ chính thức và synthesis không nhất quán                 → publish
Toàn bộ công cụ chính thức nhất quán                            → done
```

`Stage` trong sự kiện chỉ dùng để hiển thị UI, không phải nguồn sự kiện khôi phục.

### 6.3 Danh tính Công cụ Thống nhất

Không triển khai đồ thị phụ thuộc (dependency graph). Mỗi công cụ ngữ nghĩa trong không gian làm việc thống nhất dùng một quy tắc danh tính:

```go
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}
```

`InputDigest` bao phủ toàn bộ **đầu vào ngữ nghĩa** mà hành động đó thực sự tiêu thụ, mã hóa theo thứ tự cố định rồi tính toán:

- segmentation: Nội dung nguồn chuẩn hóa, phóng chiếu SourceUnit, hướng dẫn của người dùng và phiên bản prompt/schema phân đoạn;
- confirmation: Nội dung segmentation và phương thức xác nhận;
- Phân tích chương: Phạm vi chương của lô và chính văn, sổ cái (ledger) liên tục trước khi vào lô, phiên bản prompt/schema và hướng dẫn của người dùng;
- RangeDigest/BookSynthesis: Nội dung phân tích có thứ tự hoặc digest tầng dưới mà mỗi cái tiêu thụ, phiên bản prompt/schema tổng hợp;
- story resolution: Nội dung synthesis và lựa chọn của người dùng;
- Phát hành (publish): Nội dung chuẩn hóa của các đối tượng lĩnh vực chờ phát hành.

Các sự kiện thực thi như provider/model, usage, thinking được ghi vào provenance/session, không tự động làm vô hiệu hóa phân tích đã thành công do thay đổi cấu hình model; khi người dùng yêu cầu phân tích lại, hãy xóa rõ ràng công cụ tương ứng. Phán đoán tái sử dụng cache chỉ xem hành động hiện tại có thể tạo lại cùng `InputDigest` hay không.

`NextAction` đi dọc theo đường ống tuyến tính cố định để tìm công cụ đầu tiên bị thiếu, phân tích thất bại hoặc `InputDigest` không khớp. Khi cắt lại, sửa hướng dẫn người dùng hoặc thay đổi sự kiện thượng nguồn, hạ nguồn sẽ tự nhiên mất khớp (mismatch); không viết quy tắc vô hiệu hóa "khi cắt thay đổi thì xóa thủ công những file nào".

Khi phát hành, công cụ chính thức và kết quả tổng hợp được so sánh từng mục; nếu giống nhau thì bỏ qua theo tính lũy đẳng, nếu khác thì báo cáo xung đột, không đoán mò ghi đè. Do đó xóa bỏ `ResumeFrom`. Việc khôi phục chỉ cần thực thi lại `/import`; Runner sẽ tiếp tục từ sự kiện thiếu đầu tiên.

## 7. Đọc File Nguồn

### 7.1 Giải mã

Phiên bản đầu tiên hỗ trợ:

- UTF-8 / UTF-8 BOM;
- GB18030 (Bao phủ văn bản tiểu thuyết GBK phổ biến).

Kết quả giải mã bắt buộc phải trả về encoding đã chọn, và ghi vào Manifest cùng sự kiện tiến độ. Không được phép giấu việc "thử GB18030" thành phương án bọc lót không tiếng động. Nếu không thể giải mã đáng tin cậy hoặc xuất hiện ký tự thay thế không thể chấp nhận thì thất bại trực tiếp, lỗi phải chứa kết quả kiểm tra.

### 7.2 Chuẩn hóa

Chỉ thực hiện các chuyển đổi không làm thay đổi nội dung văn học:

- Xóa BOM;
- Thống nhất CRLF/CR thành LF;
- Giữ lại dòng trống, thụt lề, dòng tiêu đề và ký tự chính văn;
- Không xóa văn bản phần đầu, chương rỗng, quảng cáo, thông tin bản quyền hay cái gọi là nhiễu phần đuôi.

Mọi quyết định loại trừ đều để lại cho kết quả ngữ nghĩa phân đoạn và hiển thị trong bản xem trước.

### 7.3 Tọa độ Ổn định

Văn bản đã chuẩn hóa sẽ thiết lập một bảng `SourceUnit` thống nhất:

```go
type SourceUnit struct {
	ID        string // L1257; Dòng vượt ngân sách tách thành L1257.1, L1257.2
	Line      int
	Part      int
	StartByte int
	EndByte   int
	Text      string
}
```

- `ID` chỉ dùng để hiển thị và model tham chiếu; mọi phán đoán thứ tự, bao hàm và tăng dần bắt buộc so sánh theo tuple giá trị `(Line, Part)`, cấm so sánh từ điển với chuỗi ID (`"L900"` từ điển sẽ lớn hơn `"L1000"`); phóng chiếu JSON giữ chuỗi id, Go phân tích thành `(Line, Part)` rồi mới so sánh;
- Dòng bình thường tương ứng một unit, đường dẫn chung vẫn là mô hình tọa độ số dòng trực quan;
- Khi dòng đơn vượt quá ngân sách phóng chiếu cấu trúc, Go chỉ tạo ra nhiều **unit ảo** ở các ranh giới ký tự UTF-8;
- Phân mảnh ảo không ghi ngược lại `source.txt`, không chèn ngắt dòng mềm (soft wrap), không làm đổi bất kỳ ký tự nguồn nào;
- Khi có ranh giới bên trong cùng một unit, model trả về ID của unit và đoạn điểm neo (anchor) nguyên bản chép nguyên xi; Go yêu cầu điểm neo phải duy nhất trong unit đó, sau đó ánh xạ thành vị trí byte chính xác;
- Khi điểm neo không tồn tại hoặc không duy nhất, phản hồi lỗi cụ thể lại cho model, cấm đoán mò offset, cắt cụt văn bản hoặc bắt người dùng phải tự đi sửa bản gốc trước.

Do đó, văn bản chia chương bình thường duy trì mô hình số dòng, toàn bộ đoạn không ngắt dòng, cùng một dòng chứa nhiều chương hoặc dòng dài bất thường đều xử lý bằng cùng một bộ tọa độ.

## 8. Cắt Ngữ nghĩa

### 8.1 Phóng chiếu Cấu trúc

Model nhìn thấy phóng chiếu cấu trúc được chia khối theo ngân sách ngữ cảnh:

```json
{
  "owned_units": {"start": "L1200", "end": "L1800"},
  "context_units": {"start": "L1180", "end": "L1820"},
  "units": [
    {"id": "L1200", "line": 1200, "text": "Gió từ ngoài cổng thành thổi tới.", "blank_before": true},
    {"id": "L1257", "line": 1257, "text": "Tập 2 · Bắc Cảnh", "blank_before": true, "blank_after": true}
  ],
  "user_guidance": ""
}
```

Vùng ngữ cảnh (context_units) có thể chồng chéo, nhưng mỗi lần gọi chỉ có thể trả kết quả cho vùng `owned_units`, nên không tồn tại việc gộp xung đột hay bỏ phiếu khối chồng chéo. Kỷ luật tọa độ do Go thi hành (Sửa đổi 2026-07-16): Model trả về ranh giới nằm trong vùng ngữ cảnh sẽ không kích hoạt việc hỏi lại ngữ nghĩa —— ranh giới đó thuộc quyền quản lý của khối lân cận (nó sẽ được báo cáo lại một lần nữa trong vùng owned của chính nó), code trực tiếp cắt bỏ và hiển thị lại giải thích; việc thử lại ngữ nghĩa chỉ dành cho những lỗi ngữ nghĩa thực sự (ID ảo giác ngoài phóng chiếu, kind không hợp lệ, v.v.). Hành vi cũ hỏi lại khi phản hồi vượt ranh giới, các model yếu thường tiêu sạch 3 lần thử làm sập cả khối.

Kích thước khối tính theo context window của model architect hiện tại và ngân sách dự trữ, không cắt khối theo số dòng hay số chương cố định. Khi ngữ cảnh model rộng ra, số lần gọi tự nhiên giảm xuống. Ngân sách quy hoạch không dùng hết (Sửa đổi 2026-07-16): phần chính văn owned chỉ là một phần request, khi quy hoạch phải trừ độ dài thực của system prompt và hướng dẫn, rồi nhân tỷ lệ 3/4 do gói JSON phình ra; Vùng ngữ cảnh có giới hạn byte riêng (chunkBytes/8, sàn là 4096), chặn các phân mảnh ảo của dòng quá dài nuốt mất ngân sách đầu vào. Đầu ra bọc lót đối xứng: Biên giới khối đơn dạng JSON bị cắt do độ dài (do có quá nhiều chương ngắn) thì sẽ chia đôi khối và thử lại đệ quy —— khối chia nửa có luồng cache độc lập, thành quả thử lại không bắt trả tiền lại; chia tới cấp đơn vị (unit) mà vẫn bị cắt cụt thì mới thực sự hết dung lượng.

Mỗi quyết định ranh giới khối sẽ ghi đĩa dưới dạng công cụ (`segment-chunks/chunk-*.json`, danh tính = danh tính cắt + MaxUnitBytes + phạm vi owned của khối —— bảng unit được xác định duy nhất bởi "nguồn chuẩn hóa + MaxUnitBytes", khi đổi số (mức model) và tạo lại phân mảnh dòng siêu dài, cache tự nhiên mất khớp, sẽ không tái sử dụng ranh giới cũ bị sai lệch), bất kỳ khối nào thất bại hay gián đoạn, khi chạy lại các khối đã xong sẽ tái sử dụng với 0 lần gọi —— cùng một triết lý với analyze từng chương, synthesize từng khoảng; sau khi segmentation cuối cùng ghi đĩa, cache cấp khối sẽ bị xóa. Khi tích hợp chót (resolve) thất bại, cũng xóa cache khối và ghi bản chụp quyết định vào `failures/`: lúc này digest của cache luôn khớp, nếu giữ nó lại sẽ khiến chạy lại báo 0 lần gọi và lặp lại y nguyên đám ranh giới đó, làm lỗi tái hiện y chang. Ranh giới chương có chính văn rỗng (Tiêu đề chiếm chỗ "Chương đã khóa/trả phí" rất phổ biến ở nguồn web truyện) không làm toàn khối thất bại: Gộp vào đoạn trước (không mất 1 chữ nào), ghi chú vào `Segmentation.Notes` để bản xem trước xác nhận hiển thị, nếu người dùng không ưng có thể dùng `--guide` để phán quyết.

### 8.2 Đầu ra của Model

```go
type BoundaryDecision struct {
	UnitID    string   `json:"unit_id"`
	Anchor    string   `json:"anchor,omitempty"`
	Kind      string   `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string   `json:"title,omitempty"`
	Uncertain bool     `json:"uncertain,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
```

- `chapter` là đơn vị chính văn có thể đệ trình, bao gồm phán đoán ngữ nghĩa xem lời bạt, chương đệm, ngoại truyện có được tính là chương hay không;
- `group` là bằng chứng cấu trúc tầng trên như quyển, bộ, phần, không dùng trực tiếp làm chương;
- `front_matter` / `back_matter` đánh dấu các khu vực phụ trợ rõ ràng không đi vào chính văn của chương;
- `anchor` bắt buộc từng chữ phải đến từ unit tương ứng; có thể bỏ qua nếu ranh giới nằm ở đầu unit;
- `uncertain` chỉ dùng cho nhắc nhở xem trước (preview), mã nguồn không thiết lập ngưỡng độ tin cậy.

Không để model sinh ra regex. Regex vẫn sẽ ép ngữ nghĩa mở về một cú pháp hữu hạn, và đưa vào các giả định về thoát ký tự (escape), khớp cục bộ và định dạng thống nhất.

### 8.3 Mã nguồn Kiểm tra (Validation)

Go chỉ kiểm tra:

1. Tất cả unit ID tồn tại và rơi vào khoảng phóng chiếu được gọi (owned + vùng ngữ cảnh; ngoài phóng chiếu là ảo giác, sẽ có feedback bắt hỏi lại);
2. Của ranh giới vùng owned: kind thuộc tập đóng, anchor không rỗng là duy nhất trong unit đó và ánh xạ được tới ranh giới byte UTF-8, không xung đột ngữ nghĩa ở cùng vị trí (kind/title khác nhau thì giữ cái nào Go không quyết, hỏi lại giao cho model; lặp lại y hệt là dư thừa cơ học, cho qua rồi âm thầm lọc trùng), khối đầu tiên bắt buộc phải có ranh giới hứng điểm bắt đầu của văn bản (đoạn đầu có phải là lời mở đầu không do model phán đoán, Go không trả lời hộ) —— đều kiểm tra lúc gọi (Sửa đổi 2026-07-16): Giá trị lỗi lọt vào cache khối đến cuối cùng mới phát hiện, digest luôn khớp sẽ làm lỗi chắc chắn tái hiện; ranh giới vùng ngữ cảnh định sẵn bị cắt bỏ nên không hỏi lại;
2a. **Echo Title (Hiển thị lại tiêu đề)** (Sửa đổi 2026-07-16, seg-v2): title của ranh giới chapter/group sau khi chuẩn hóa (bỏ khoảng trắng) bắt buộc phải tồn tại thật trong unit ranh giới bản gốc, nếu không sẽ hỏi lại ngay lúc gọi —— thực nghiệm trên một nguồn phân trang với 157 chương, có 67 chương model tự bịa ranh giới và tiêu đề giữa phần chính văn nối tiếp (do kỷ luật che phủ bắt buộc mỗi khối có ranh giới đầu khối), toàn bộ đều bị bước đối chiếu sự thật này chặn lại. Ngữ nghĩa vẫn giao cho model: Nguồn thực sự không có quy ước tiêu đề thì gán `uncertain=true` có thể giữ lại tiêu đề quy nạp (hiển thị nghi vấn lúc preview); tiêu đề mô tả của front/back matter có rủi ro thấp, không đối chiếu. Đồng bộ siết chặt prompt: Ranh giới chỉ đặt ở chỗ ngăn cách cấu trúc thật sự, nếu đầu khối là chính văn nối tiếp chương trước thì trả về boundaries rỗng là đầu ra đúng (trừ việc che phủ đoạn đầu của khối đầu tiên);
3. Thứ tự và trùng lặp của ranh giới được Go sửa chữa có tính xác định chứ không phủ quyết (Sửa đổi 2026-07-16): Dựa theo sắp xếp byte ổn định sau phân tích để khôi phục thứ tự thật —— thứ tự giữa các khối được đảm bảo do vùng owned không chồng chéo, lộn xộn chỉ có thể xảy ra trong khối, sắp xếp lại không mất thông tin; trùng lặp ở cùng byte thì giữ cái xuất hiện trước và ghi vào `Notes`. Hành vi cũ yêu cầu tăng dần nghiêm ngặt nếu không sẽ hỏng toàn bộ, thực nghiệm 319 ranh giới chỉ gục ở 1 chỗ ngược thứ tự trong khối, và cache khối sẽ làm lỗi đó chắc chắn tái hiện. Phán đoán thứ tự nhất loạt so sánh theo giá trị `(Line, Part)`, không so sánh từ điển với ID;
4. Phạm vi chính văn mỗi chương xuất ra không rỗng (Tiêu đề chiếm chỗ có chính văn rỗng thì nhập vào đoạn trước, xem §8.1);
5. Mọi văn bản nguồn không rỗng thuộc về đúng một chương, một tiêu đề group, hoặc front/back matter rõ ràng (Văn bản không rỗng chưa phân bổ ở đoạn đầu —— như tóm tắt sách/quảng cáo đầu truyện bị bỏ sót ranh giới —— Go sẽ xác định gom lại thành front_matter và ghi `Notes` giao cho bước preview xác nhận, không phủ quyết ở giai đoạn cuối);
5a. Chương trùng tên (title sau khi bỏ khoảng trắng giống nhau) được ghi vào `Notes` giao cho con người đối chiếu (Sửa đổi 2026-07-16) —— Trong nguồn có quy ước tiêu đề thì tên chương không được lặp lại, lặp lại là tín hiệu xác định của việc "cùng một chương bị cắt nhầm"; Có gộp lại hay không Go không quyết, Notes khác rỗng tức là chặn `--yes`;
6. Không có phạm vi chồng chéo, vượt ranh giới hay chưa phân bổ;
7. group không bị tính nhầm vào tổng số chương.

"L1257 về mặt ngữ nghĩa có phải là tiêu đề chương không" không do Go phán xử.

### 8.4 Người dùng Xác nhận

Trong chế độ tương tác, trước khi xác nhận sẽ không gọi phân tích chương, cũng không ghi vào Store chính thức. Preview (Xem trước) ít nhất hiển thị:

- Số tập/group và số chương;
- Toàn bộ tiêu đề chương, có thể cuộn để xem;
- Phạm vi và tóm tắt của văn bản phụ trợ ở đầu/cuối;
- Ranh giới chương rỗng, chương quá dài và ranh giới mà model đánh dấu uncertain;
- Dòng bắt đầu/kết thúc mỗi chương, tiện cho người dùng đối chiếu bản gốc.

Người dùng có thể:

- Xác nhận (Trong bảng preview TUI nhấn `y`, nội bộ sẽ chạy lại với AcceptSegmentation; thả hành một lần cho việc chia cắt hiện tại, không ghi vào intent, confirmation ghi `method=user_confirmed`);
- Nhập giải thích bằng ngôn ngữ tự nhiên sau đó nhận diện lại, ví dụ `/import --guide=Giữa màn·X cũng là chương độc lập`;
- Hủy và giữ lại không gian làm việc (Esc).

`/import <path> --yes` là ủy quyền không người trực rõ ràng: Sau khi vượt qua việc kiểm tra độ bao phủ, Runner sẽ ghi công cụ confirmation y hệt, ghi chú `method=auto_authorized`, sau đó tiếp tục phân tích. `--yes` mặc dù có ranh giới uncertain vẫn biểu thị người dùng chọn tin tưởng lần chia cắt này, nhưng uncertain vẫn lưu lại trong công cụ và nhật ký. **Ngoại lệ (Sửa đổi 2026-07-16)**: Khi chia cắt có kèm theo giải thích dung sai (`Notes` khác rỗng —— hấp thụ chương rỗng, bọc lót đoạn đầu, xóa trùng lặp từng xảy ra) thì `--yes` không tự động thả hành, vẫn dừng lại ở preview xác nhận —— Cấu trúc đã bị sửa đổi có tính xác định, việc ủy quyền mù quáng khi chưa xem preview không nên nuốt mất điều đó; Đã xem preview rồi nhấn `y` (AcceptSegmentation) thì không bị hạn chế này.

`--yes` chỉ bỏ qua xác nhận chia cắt, không quyết định thay người dùng `story_status=uncertain`, cũng không bỏ qua bước Hold (tạm dừng) lúc nhập hoàn tất. Người dùng không cần viết regex hay điền thủ công `from=N`.

## 9. Trích xuất sự kiện từng chương theo lô liên tục

Sau khi xác nhận, bắt đầu từ bản phân tích bị thiếu đầu tiên, ghép các chương liên tiếp thành các lô (batch) dựa theo **ngân sách kép đầu vào và đầu ra** của model hiện tại. Trong phiên bản đầu tiên, giữa các lô là xử lý nối tiếp (serial), không làm song song giữa các cửa sổ: ID chi tiết gieo mầm, bí danh nhân vật và thay đổi trạng thái có tính chất thời gian, sổ cái (ledger) cô đọng tạo ra từ lô trước là đầu vào của lô sau.

Nối tiếp chỉ ràng buộc chiến lược thực thi của bản đầu tiên, không phải hạn chế kiến trúc vĩnh viễn; công cụ phân tích vẫn ghi đĩa độc lập theo từng chương, nếu tương lai có bằng chứng cho thấy việc quy nạp song song có thể giữ nguyên chất lượng ngữ nghĩa, thì có thể chỉ cần thay thế phần điều phối lô (batch scheduling).

### 9.1 Đầu ra Lô, Công cụ Từng Chương

Loại bỏ vỏ bọc (envelope) hỗn hợp `=== TAG ===`. Mỗi lệnh gọi trả về một đối tượng lô có cấu trúc, mỗi phần tử mảng vẫn là sự kiện của 1 chương:

```go
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook"`
	Scenes              []string                   `json:"scenes"`
	Characters          []string                   `json:"characters"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent      `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}
```

Mỗi `analyses/NNNNNN.json` là `Artifact[ChapterAnalysisPayload]`. Các chương ghi đĩa trong cùng một lô sẽ ghi nhận `BatchStart/BatchEnd` giống nhau; `InputDigest` của nó dùng kiểu **ràng buộc từng chương (per-chapter binding)**: Danh tính chia cắt (của `InputDigest` công cụ segmentation) + phiên bản prompt/schema + số chương + chính văn chương lẻ. Sở dĩ gán danh tính theo từng chương chứ không chia theo lô là vì ranh giới lô thay đổi theo khả năng vào/ra của model (đổi model mạnh hơn lô tự nhiên to ra); nếu viết việc chia lô vào danh tính, khi đổi model, các phân tích đã thành công sẽ bị lệch hoàn toàn, buộc phải tính toán lại và trả phí lặp lại. Việc gán với danh tính chia cắt thì đảm bảo "khi cắt lại, đổi phiên bản prompt/schema, đổi nguồn" thì các phân tích hạ nguồn sẽ mất khớp tự nhiên, còn nếu chỉ đơn thuần đổi model thì không bị vạ lây —— đó mới là ngữ nghĩa vô hiệu hóa (invalidation semantics) mà việc khôi phục thực sự cần.

`ImportedCharacterFact` và `ImportedWorldFact` là các quan sát cô đọng dùng cho việc tổng hợp toàn sách, không ghi trực tiếp thành nhân vật hay quy tắc thế giới chính thức. Chúng ít nhất mang theo số chương, giúp cho kết quả tổng hợp có nguồn gốc ổn định.

### 9.2 Lập Lô Ngân Sách Kép

Quy hoạch lô phải thỏa mãn đồng thời:

```text
Đầu vào dự kiến + system/prompt/ledger + Dự phòng suy luận + Đầu ra thấy được dự kiến ≤ context window
Đầu ra thấy được dự kiến ≤ Giới hạn completion khả dụng của provider/model
```

- Ước tính đầu vào bao gồm tiêu đề, chính văn mỗi chương và ledger trước lô đó;
- Ước tính đầu ra cấu thành từ chi phí cấu trúc cố định của analyzer schema và khoản dự phòng sự kiện bảo thủ của mỗi chương, nó chỉ quyết định lần này nạp bao nhiêu chương, không cắt bớt bất kỳ trường nào;
- reasoning token và JSON thấy được dùng chung completion budget của model, bắt buộc phải trừ đi phần dự phòng suy luận trước;
- Năng lực đầu ra provider/model càng mạnh, lô sẽ tự nhiên lớn hơn; không viết quy tắc cố định kiểu "mỗi lô 10/20 chương";
- Bản thân đầu vào của một chương đã không thể nhét vừa context, hoặc cấu trúc đầu ra nhỏ nhất của 1 chương không thể nhét vừa completion, sẽ báo cáo rõ ràng chương đó và sức chứa của model, không cắt bớt chính văn hay ngụy tạo thành công rút gọn.

Do đó sự tăng trưởng của tổng số chương chỉ làm tăng số lô, không làm cho bất kỳ một phản hồi nào bị phình ra vô hạn theo quy mô sách nữa; đồng thời cũng không đem #83 từ mức độ cả sách chuyển sang một mức độ lô mà không chịu sự ràng buộc của đầu ra.

### 9.3 Ngữ Cảnh của Lô

Một lệnh gọi lô (batch) duy nhất chỉ bao gồm:

- Nguyên bản và tiêu đề của khoảng chương liên tục hiện tại;
- Bảng bí danh nhân vật cô đọng phái sinh từ các chương trước;
- ID chi tiết gieo mầm (foreshadow) đang hoạt động và trạng thái một câu của nó;
- Các tóm tắt trạng thái gần đây cần thiết.

Model xử lý các chương trong lô theo thứ tự của mảng, có thể tiếp nối bí danh, chi tiết gieo mầm và trạng thái bên trong lô; sau khi lô kết thúc, Go cập nhật ledger cô đọng theo trình tự sự kiện đã được xác minh. Nó không phụ thuộc vào Premise của toàn sách vốn chưa được sinh ra, cũng không đọc lại toàn bộ phần truyện phía trước một lần nữa. Sự kiện chương là đầu vào của Foundation, chứ không phải ngược lại để tạo thành sự phụ thuộc vòng tròn.

### 9.4 Xác Minh Toàn Bộ Phản Hồi

Mã nguồn chia làm 2 tầng để xác minh cấu trúc, miền giá trị và tính tham chiếu, không hardcode chất lượng văn học:

- Cấp độ Lô: Mảng chapters phải liên tục theo số chương kỳ vọng, không lặp, không hổng, phạm vi lô, `InputDigest` và schema version phải khớp;
- Cấp độ Chương: chapter/title nhất quán với phân đoạn nguồn, summary/core_event không rỗng, hook type, strand... là các trường tập đóng hợp lệ của domain chính thức, các trường dòng thời gian, foreshadow và trạng thái thay đổi phải hợp lệ về kiểu (type).

Mã nguồn không yêu cầu "Bắt buộc 3~6 sự kiện", "Bắt buộc có nhân vật xuất hiện", "Bắt buộc có 3 cảnh (scene)". Chương tĩnh lặng, thư từ, chương bối cảnh môi trường hay chương nhân vật vô danh đều là hình dáng văn học hợp lệ.

Khi phản hồi hoàn chỉnh xuất hiện lỗi JSON hoặc xác minh ngữ nghĩa, không đệ trình (commit) bất kỳ chương mới nào trong đó; phản hồi lỗi cụ thể lại cho cùng một model, đi theo luồng thử lại phía đầu ra của §13.3. Model có thể sửa lại và viết đè các đối tượng phía trước, do đó nếu việc kiểm tra thông thường thất bại thì không được tự ý lưu một phần mảng.

### 9.5 Tiền Tố Liên Tục Khi Cắt Cụt Độ Dài

> Định vị triển khai: Phần này là **Tối ưu hóa token trên đường dẫn lỗi**, không phải là phụ thuộc vào độ chính xác khi khôi phục. v1 (giai đoạn ba) cắt cụt tức là "thất bại + thu hẹp và tổ hợp lại lô", tự nó đã đúng và có thể khôi phục; việc vớt vát tiền tố liên tục được thực hiện trong một giai đoạn con độc lập (giai đoạn ba·bổ sung), có thể đóng mở riêng biệt, nghiệm thu riêng biệt.

Chỉ khi phản hồi có cờ `StopReasonLength` rõ ràng VÀ trả về được một phần văn bản có thể phân tích cú pháp (parse), mới được phép lưu **tiền tố hợp lệ liên tục lớn nhất** từ phản hồi thất bại:

1. Sử dụng stream JSON decoder để đi vào mảng `chapters` trên cùng;
2. Bắt đầu từ chương đầu tiên của lô, đọc từng đối tượng JSON đã đóng hoàn chỉnh;
3. Mỗi đối tượng độc lập vượt qua xác minh từng chương của §9.4, và kết hợp với các đối tượng trước đó tạo thành một chuỗi liên tục kể từ chương đầu của lô, thì lập tức ghi nguyên tử vào công cụ phân tích chương tương ứng;
4. Gặp phải đối tượng đầu tiên không hoàn chỉnh, không hợp lệ, nhảy cóc số hay trùng lặp thì lập tức dừng lại, các byte phía sau tuyệt đối không được diễn giải;
5. Cấm bù ngoặc, viết tiếp nửa cái JSON, đoán trường bị thiếu hoặc vớt các đối tượng không liên tục ở vị trí sau;
6. Phản hồi gốc, StopReason, phạm vi tiền tố đã lưu và chương thất bại đầu tiên, tất cả đều ghi vào failure artifact, sự kiện và nhật ký;
7. `NextAction` tiếp tục nhóm lô từ bản phân tích bị thiếu đầu tiên, không làm lại tiền tố hợp lệ đã đệ trình.

typed-call bắt buộc phải ghi lại xem lần này có lấy được phần văn bản có thể dùng được hay không: Chế độ có cấu trúc phi luồng như JSON Schema có thể không đưa ra được tiền tố nào parse được khi nó dừng lại vì độ dài. Nếu provider không trả về một phần văn bản, không thể chứng minh rõ ràng là bị cắt cụt độ dài, hoặc không có lấy một đối tượng hợp lệ nào hoàn thành, thì không lưu bất cứ kết quả nào, phát ra sự kiện/nhật ký `prefix_salvage=unavailable` và lui về "Thất bại + thu hẹp tổ hợp lô", chứ không phải chạy không tải trong im lặng. Nếu lô 1 chương mà vẫn bị cắt cụt thì trực tiếp báo cáo dung lượng đầu ra của model không đủ, không cắt giảm vòng lặp hay chế tạo ra sự kiện rỗng.

Cắt cụt độ dài là lỗi dung lượng, không đi vào vòng lặp tự sửa chữa ngữ nghĩa "đưa lỗi xác minh cho cùng model", cũng không thử lại y chang cùng một lô.

### 9.6 Khôi Phục

Mỗi chương phân tích thành công lập tức ghi nguyên tử `analyses/NNNNNN.json`. Sau khi sập:

- Bản phân tích có `InputDigest` khớp sẽ tái sử dụng luôn, không tính phí lần hai;
- Bản phân tích bị thiếu hoặc không khớp đầu tiên sẽ trở thành điểm bắt đầu của lô tiếp theo;
- Sau khi đầu vào ngữ nghĩa thượng nguồn thay đổi, không thể tái tạo cùng `InputDigest` thì bản phân tích sẽ vô hiệu hóa tự nhiên;
- Các tiền tố hợp lệ liên tục đã nộp trong lúc bị cắt cụt độ dài và các công cụ hoàn thành bình thường sử dụng bộ quy tắc khôi phục hoàn toàn giống nhau;
- Không cho phép người dùng vượt qua một chương thất bại để tiếp tục sinh ra sự kiện ngữ nghĩa đoạn sau một cách đứt đoạn.

## 10. Tổng Hợp Phân Tầng (Layered Synthesis)

### 10.1 Tại sao không thể làm đầu ra một lần cho cả cuốn sách nữa

Tổng hợp toàn sách cần sự hiểu biết xuyên suốt các chương, nhưng không cần đọc lại toàn bộ chính văn một lần nữa, cũng không nên xuất ra các đối tượng chi tiết của từng chương. Sự kiện từng chương đã bao hàm ngữ nghĩa cấp chương; Tổng hợp chỉ xử lý các sự kiện cô đọng này.

### 10.2 Hình dáng Map/Reduce

```text
ImportedChapterFacts × N
        ↓ Chia các khoảng liên tục theo context window hiện tại
RangeDigest × M
        ↓ Nếu cần thì tiếp tục gộp lại
BookSynthesis
```

Nếu sách ngắn mà một lần có thể chứa hết tất cả sự kiện chương, trực tiếp tạo ra `BookSynthesis`; Sách dài mới tạo `RangeDigest`. Có phân tầng hay không do ngân sách token quyết định cơ học, không do ngưỡng số chương quyết định.

`RangeDigest` chứa nội dung đẩy tiến tình tiết, diễn biến nhân vật, sự kiện thế giới, chi tiết gieo mầm đã mở/đã thu, và các ranh giới cấu trúc dự bị của dải liên tục đó. Độ lớn đầu ra của nó bị giới hạn bởi một dải đơn; tổng hợp cuối cùng không xuất lặp lại N bản đối tượng chi tiết từng chương nữa, chỉ xuất ra các sự kiện toàn cục và phạm vi tập arc (volume/arc range).

### 10.3 Kết quả Tổng Hợp Cuối Cùng

```go
type BookSynthesis struct {
	Title         *string                `json:"title"`    // Khi chính văn không xác định được thì null, lấy từ tên file
	Synopsis      string                 `json:"synopsis"` // Tóm tắt không chứa tiết lộ nội dung hướng tới độc giả
	Premise       string                 `json:"premise"`
	Characters    []domain.Character     `json:"characters"`
	WorldRules    []domain.WorldRule     `json:"world_rules"`
	Structure     []ImportedVolumeRange  `json:"structure"`
	Compass       domain.StoryCompass    `json:"compass"`
	PlanningTier  domain.PlanningTier    `json:"planning_tier"`
	StoryStatus   string                 `json:"story_status"` // open / closed / uncertain
	StatusReason  string                 `json:"status_reason"`
}
```

Cấu trúc chỉ trả về phạm vi, không xuất lặp lại tất cả các chương:

```go
type ImportedVolumeRange struct {
	Title string             `json:"title"`
	Theme string             `json:"theme"`
	Arcs  []ImportedArcRange `json:"arcs"`
}

type ImportedArcRange struct {
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	StartChapter int    `json:"start_chapter"`
	EndChapter   int    `json:"end_chapter"`
}
```

Model tự quyết định số tập và số arc, có thể tham khảo các tiêu đề group trong file nguồn, nhưng không bị giới hạn bởi "Một tập" hay "1~3 arc". Go sử dụng title/core_event/hook/scenes của `ImportedChapterFacts` để lắp ráp `OutlineEntry` chính thức.

### 10.4 Trạng thái Câu chuyện

Import chỉ tạo lại các sự kiện của chính văn, không vì muốn Engine chạy tiếp mà ngụy tạo các đường dài (long line) chưa hội tụ:

- `open`: Chính văn có mục tiêu chưa thu hẹp hoặc tình huống căng thẳng thật sự, sinh ra Compass bình thường;
- `closed`: Phát hành theo tác phẩm đã hoàn kết, tập cuối đánh dấu Final; Khi cần viết phần tiếp theo do người dùng rõ ràng reopen và đưa ra hướng đi mới;
- `uncertain`: Trước khi phát hành yêu cầu người dùng chọn xử lý theo kiểu chưa xong hay đã hoàn kết; Nếu Intent đã lưu thông qua `--story=open|closed` thì dùng luôn, nếu không sẽ vào chờ tương tác. Lựa chọn được lưu thành công cụ `story-resolution.json` lấy synthesis hiện tại làm đầu vào.

Mã nguồn không lén lút đoán ý người dùng thông qua việc xem `open_threads` có rỗng hay không.

## 11. Lắp Ráp và Xác Minh Foundation

Model sinh ra ngữ nghĩa tổng hợp, Go chịu trách nhiệm lắp ráp thành đối tượng domain chính thức. Trước khi phát hành bắt buộc thỏa mãn:

1. Premise có tiêu đề sách hợp lệ; khi chính văn không xác định được tên sách thì dùng basename của file nguồn, đồng thời đánh dấu nguồn gốc là filename, không để model tự xưng đó là "Tên sách thật";
2. Tất cả các phạm vi tập và arc liên tục theo thứ tự;
3. Phạm vi đầu tiên bắt đầu từ Chương 1, phạm vi cuối cùng kết thúc ở Chương N;
4. Mỗi chương đúng bằng một phần của một arc;
5. Sau khi `FlattenOutline` (làm phẳng đại cương), số lượng chương là N, tiêu đề và sự kiện từng chương nhất quán;
6. Tên nhân vật, quy tắc thế giới và Compass thỏa mãn các ràng buộc kiểu domain hiện có;
7. PlanningTier là giá trị tập đóng hợp lệ, nhưng lý do lựa chọn là từ model chứ không phải ngưỡng số chương;
8. Trạng thái closed/open thống nhất với hình dạng phát hành của Final, Compass;
9. `InputDigest` của công cụ Synthesis có thể được tái tạo từ tập hợp phân tích có thứ tự hiện tại.

Khi vi phạm cấu trúc, trả lỗi cụ thể lại cho model để sinh ra lại, kéo dài cho đến khi thành công hoặc context bị hủy; không ghi đĩa nửa chừng.

## 12. Phát Hành Chính Thức

### 12.1 Tiền đề Phát hành

Lần nhập mới chỉ được phép đi vào:

- Không có chương nào đã hoàn thành;
- Không có chương đang xử lý dở hay PendingCommit;
- Không có không gian làm việc nhập phi đồng nguồn nào khác;
- Foundation chính thức đang rỗng, hoặc hoàn toàn giống với digest đã phát hành của không gian làm việc hiện tại.

Ngữ nghĩa gộp giữa tiểu thuyết đã có với văn bản bên ngoài vẫn chưa rõ ràng, phiên bản đầu tiên từ chối thẳng thừng, không đoán mò ghi đè hay nối đuôi.

### 12.2 Phát hành Foundation

Phát hành theo thứ tự phụ thuộc chính thức:

```text
planning tier
→ premise
→ characters
→ world rules
→ layered outline + flat outline
→ compass
→ progress đối chiếu
```

Mỗi bước:

1. Tính toán digest nội dung chờ phát hành;
2. Nếu công cụ chính thức chưa có thì ghi nguyên tử và gắn checkpoint;
3. Nếu đã tồn tại và digest bằng nhau thì skip theo tính lũy đẳng;
4. Đã tồn tại nhưng khác nhau thì trả về lỗi xung đột, không ghi đè.

Nửa chừng sập sau đó bắt đầu lại đối chiếu từ mục đầu tiên, không cần transaction xuyên file hoặc máy trạng thái Pending của Foundation.

### 12.3 Phát hành Chương

Tái sử dụng quy trình hiện có theo thứ tự chương:

```text
Lưu draft
→ Progress.StartChapter
→ commit_chapter(sự kiện từng chương)
```

`commit_chapter` đã có saga PendingCommit, checkpoint và kiểm tra lũy đẳng chương đã hoàn thành. Việc nhập không tạo ra bộ logic đệ trình thứ 2.

Cửa sổ sập:

| Cửa sổ | Hành vi khôi phục |
|---|---|
| Trước khi draft | Lưu lại cùng chính văn |
| Sau draft, trước StartChapter | Đối chiếu digest xong thì tiếp tục |
| Sau StartChapter, trước PendingCommit | Thực thi lại commit cùng chương |
| Đang PendingCommit | Khôi phục bằng saga đệ trình hiện có |
| Sau khi chapter complete | Nếu digest/checkpoint nhất quán thì skip |
| Nội dung chính thức xung đột digest nguồn | Dừng hẳn, báo cáo chương xung đột |

### 12.4 Ranh giới Hoàn Thành Nhập

Sau khi toàn bộ chương đệ trình ổn định sẽ thiết lập một lần `AdvanceHoldAtBoundary`, lý do ghi rõ là "Nhập tiểu thuyết ngoại vi đã hoàn tất, chờ nghiệm thu rồi viết tiếp". Nó chỉ bảo vệ lần chuyển đổi qua hệ thống này, không đổi chế độ `auto/review` lâu dài của người dùng.

`--yes` chỉ ủy quyền nhận phần chia cắt tự động, không được ngầm skip cái Hold này. Trừ phi người dùng cố tình truyền tham số `--continue` riêng biệt, Runner mới không tạo Hold dành riêng cho import; sau đó vẫn tuân thủ advance mode bình thường: `auto` có thể viết tiếp luôn, `review` vẫn đợi `/next`.

Mặc định TUI không còn tiếp sức không có thông báo nữa. Người dùng kiểm tra Foundation và trạng thái chương, rồi dùng điểm truy cập (entry) continue hiện tại để khôi phục sáng tác.

**Điểm rơi khi đóng bảng điều khiển**: Việc nhập thành công bắt đầu từ trang chào mừng (welcome page), Esc đóng bảng điều khiển sẽ chạy bù `Resume()` một lần (Cổng khôi phục bootstrap chỉ chạy một lần lúc khởi động), người dùng sẽ rơi vào thẳng bàn làm việc (workbench), bị chặn lại ở ranh giới chương tiếp theo đợi nghiệm thu bởi cái Hold hoàn thành nhập —— thay vì kẹt ở cái trang chào mừng nhấn lộn Enter là "mở sách mới". Trạng thái cuối khi bị lỗi và các kịch bản của workbench thì chỉ đóng panel.

**Tuyến phòng thủ tạo mới**: `PrepareUserRules` / `StartPrepared` sẽ từ chức tạo mới khi trong thư mục sách đã có chương thành hình (`CompletedChapters` không rỗng) —— StartPrepared ngay phần đầu sẽ reset checkpoints và progress, thao tác nhầm sẽ âm thầm dọn sạch cả quyển (gồm toàn bộ các chương vừa mới nhập). Phế liệu quy hoạch chưa thành chương sẽ được cho qua, giữ lại luồng tự chữa lành là lưu đồng sáng tạo Ctrl+S cùng phiên rồi thử lại (retry) kèm phán quyết bù (fallback).

### 12.5 Cổng chặn Phát hành Xuyên khởi động

Khi không gian làm việc tồn tại và `NextAction != done`, `Host.New/Resume` phải nhận diện được cuốn sách là đang nhập dở:

- Cho phép xem, chẩn đoán và thực thi `/import` khôi phục;
- Cấm Engine khởi động bình thường, Continue hoặc phái Writer;
- Hiển thị rõ hành động khôi phục hiện tại, không coi một Foundation/chương phát hành dở dang là một cuốn sách có thể viết tiếp với ngữ nghĩa trọn vẹn.

Cổng chặn đọc trực tiếp từ không gian làm việc và Store chính thức để suy diễn, không thêm `published bool`. Như vậy phát hành ở bất cứ cửa sổ nào bị sập, cũng sẽ không để trạng thái bán phát hành bị quy trình sáng tác bình thường tiêu thụ khi Runner chưa khôi phục xong.

## 13. Nhân (Kernel) Gọi Model

Bên trong `imp` giữ lại một helper typed-call nhỏ gọn chuyên dụng, không dựng framework luồng công việc LLM đa năng.

### 13.1 Lựa chọn Model

- Mặc định dùng model vai trò architect;
- Mức độ của hàm ngữ nghĩa là núm xoay mở: segment/analyze/synthesize có thể khai báo mức (tier) riêng, mặc định rớt xuống architect, tầng cấu hình có thể chuyển segment nặng tính máy móc sang mức rẻ hơn. Đây là cấu hình lời gọi, không đổi hợp đồng ngữ nghĩa, cũng không lấy "một nhân vật đơn" làm tiền đề kiến trúc —— mục đích là để chi phí có được từ "model mức giá rẻ trở nên mạnh hơn" cũng được hưởng lợi;
  - Điểm triển khai: cấu hình roles hỗ trợ ba key `import_segment` / `import_analyze` / `import_synthesize`; khi chưa cấu hình thì rơi vào architect. Ngân sách kép và thinking/tùy chọn có cấu trúc của từng hàm sẽ phái sinh độc lập dựa theo năng lực thực tế của mức độ đó (cửa sổ nhỏ của mức nhỏ chỉ ràng buộc hàm của chính nó), dùng bao nhiêu tính tiền vào vai trò thực tế bấy nhiêu;
- Truy cập failover đã được cấu hình cho vai trò được chọn;
- Sử dụng reasoning effort của vai trò được chọn, và quyết định gửi tham số thinking hay không thông qua thăm dò khả năng;
- Ghi siêu dữ liệu session và usage theo đúng provider/model thật;
- Bỏ vào lính gác ngân sách hiện có (đưa vào thực tế từ 2026-07-16): Trước khi bắt đầu thì qua `Refuse()` tuân theo cùng một kỷ luật với Start/Resume/Continue; Nếu đang chạy mà ngân sách bị ngưng cứng (hard stop) thì qua `abortWithEvent` để cancel ngữ cảnh nhập của riêng nó (Host ghi danh job độc quyền vào cancel, không còn chỉ pause Engine trong khi Engine chưa được chạy).

### 13.2 Khả năng Đầu ra Có Cấu Trúc

4 loại sản phẩm nhập cùng chia sẻ `llmcontract.Execute`: Khi model hoặc người dùng cấu hình hỗ trợ tường minh sẽ gửi JSON Schema nguyên sinh; Khả năng chưa biết hoặc ghi rõ không hỗ trợ thì tự sinh ra Prompt Contract từ cùng một schema. Chế độ nguyên sinh xác minh phản hồi hoàn chỉnh, chế độ tương thích mới đi trích xuất đối tượng JSON cân bằng; cả hai đường đều thực thi cùng một JSON Schema để kiểm tra trước, sau đó mới giải mã DTO và thực thi kiểm tra nghiệp vụ (business validation). Request bị lỗi không được âm thầm xóa schema để thử lại, phán đoán năng lực bị sai hoặc Provider từ chối đều phải được phơi bày.

### 13.3 Tách biệt Thất bại Yêu cầu, Ngữ nghĩa và Dung lượng

- Lỗi tầng Request: Chỉ thử lại các lỗi quá giờ, bị giới hạn, lỗi mạng mà adapter đánh dấu rõ là retryable, tiếp tục dùng ngữ nghĩa backoff hiện có và kiên trì đến khi thành công hoặc bị hủy context;
- Lỗi tầng Đầu ra: Báo lỗi cụ thể của JSON parse hay Validate lại cho cùng model, tự sửa chữa liên tục đến khi thành công hoặc ngữ cảnh bị người dùng/hệ thống ngân sách hủy bỏ; Hợp đồng Schema nguyên sinh nếu bị vi phạm, từ chối trả lời và bị cắt cụt sẽ không mù quáng hỏi lại.
- Thử lại (retry) không được im lặng: Tầng request mỗi lần backoff ("Đang tiến hành thử lại lần thứ N · Sau Xs thử lại") và tầng đầu ra mỗi lần hỏi lại đều hiện ra sự kiện tiến độ để echo lên bảng điều khiển import —— Nếu không có echo, người dùng sẽ hiểu lầm là bị treo (stuck). Sự kiện backoff chỉ mang theo mốc thời gian chót (`RetryAt`), số giây còn lại do tầng render (hiển thị) tính theo tick hình thành đồng hồ đếm ngược thời gian thực (dùng chung cơ chế với bảng sự kiện của bàn làm việc); Trong lúc chạy bảng sẽ thường trú một spinner trên cùng + thời gian đã dùng, ở phần đuôi nhật ký cũng có luồng con trỏ sao tương tự.
- Lỗi echo không được mông lung: Message của gateway thường chỉ có một câu "Provider returned error"; Echo và văn bản thất bại thống nhất mang theo sự kiện có cấu trúc (phân loại lỗi/Trạng thái HTTP/provider/model, `modelErrDetail` được trích xuất từ chuỗi lỗi của litellm qua errors.As) của adapter, sự kiện đưa lên trước, cắt cụt thì ưu tiên giữ lại.
- Giai đoạn thời gian dài không được im lặng: Cắt từng khối, tổng hợp từng khoảng đều gọi model trong nội bộ hàm (Một khối có thể mất vài phút), thông qua `callProfile.step` để echo đẩy tiến lên cho từng khối/khoảng ("Đang cắt khối thứ N/M, đã nhận diện K ranh giới"). Key sự kiện chỉ cấp cho backoff request (trạng thái chớp nhoáng trong cùng cuộc gọi, nhảy tại chỗ); Hỏi lại kiểm tra là sự kiện ngữ nghĩa xuyên nhiều lần gọi, cái nào cũng thành một hàng để giữ lịch sử —— Dùng chung Key sẽ làm khối sau ghi đè khối trước, mất sạch dấu vết gỡ lỗi.
- Sao chép toàn bộ nhật ký (Full log transcription): Mỗi sự kiện tiến độ (bao gồm các dòng retry bị đè tại chỗ ở panel) đều được ghi vào **nhật ký dành riêng cho import** `<Root sách>/logs/import.log` (không trộn chung với tui.log, đọc cả cuốn sách 1 file là xem trọn vẹn toàn bộ); Backoff request và Hỏi lại ngữ nghĩa ngoài ra còn ghi một chuỗi lỗi hoàn chỉnh xuống chung một nhật ký này.
- Echo lại ngữ nghĩa của model chứ không chỉ là đếm số máy móc: Cắt từng khối echo lại tiêu đề model nhận diện ra ("Model đã nhận ra: Chương mười hai Đêm mưa tuyết / … (Tổng cộng N chỗ)"), phân tích từng chương echo sự kiện cốt lõi ("Chương 12 <Đêm mưa tuyết>: ……"), tổng hợp hoàn tất thì echo tóm quát toàn sách (Tóm tắt premise) —— Người dùng nên thấy model đã đọc hiểu những gì.
- Lỗi dung lượng: `StopReasonLength` không thử lại y chang, cũng không vào vòng lặp tự sửa ngữ nghĩa; khi một phần văn bản của analysis batch parse được thì lưu tiền tố hợp lệ liên tục theo §9.5, ngược lại thì ghi `prefix_salvage=unavailable` và thu hẹp tổ hợp lô; các hàm ngữ nghĩa khác trực tiếp báo lỗi rõ ràng và giữ lại phản hồi gốc.

Xác thực (Auth), quyền, model không hỗ trợ và xung đột trạng thái lập tức gây thất bại. Không có chuyện mô phỏng thành công, tạo đối tượng rỗng đắp vào hay bỏ qua chương bị lỗi.

### 13.4 Ngân sách Đầu vào và Đầu ra

Mỗi loại hàm ngữ nghĩa có schema, ngân sách đầu vào, dự phòng suy luận và ngân sách đầu ra thấy được (visible output budget) độc lập:

- Phân đoạn đầu ra (Segment output) chỉ bao gồm ranh giới của owned range hiện tại;
- Lô phân tích (analysis batch) đồng thời chịu sự ràng buộc của context window và completion upper bound, đầu ra là sự kiện từng chương của phạm vi liên tục hữu hạn;
- RangeDigest chỉ chứa một vùng liên tục;
- BookSynthesis chỉ chứa sự kiện toàn cục và các khoảng (range) của tập arc, không nhắc lại chi tiết đối tượng các chương.

Mỗi khi request, trước khi gửi đều ghi lại đầu vào ước tính, dự phòng suy luận, lượng max tokens đã yêu cầu và đầu ra thấy được dự kiến. Việc ước lượng chỉ để quyết định chia khối/chia lô, chứ không xóa bỏ chính văn hay trường sự kiện. Thế nên, không có cấu trúc kiểu "Tổng số chương càng nhiều, thì thể nào có một lần nào đấy kích cỡ phản hồi càng to", cũng không được vì thấy phần đầu vào vừa đủ mà phớt lờ rủi ro bị cắt cụt đầu ra.

## 14. Sự Kiện, Nhật Ký và Chẩn Đoán

### 14.1 Các Giai đoạn Sự Kiện (Stage)

```go
const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)
```

Mỗi sự kiện gồm action, chương/khoảng hiện tại, tổng số, thời gian dùng và lỗi đi kèm (optional). Sự kiện analysis batch có thêm thông tin về phạm vi lô, ngân sách ước tính, StopReason và phạm vi tiền tố đã đệ trình. Event là thứ phóng chiếu ra (projection), không tham gia vào việc khôi phục.

### 14.2 Lỗi Bắt Buộc Cùng Lúc Đến 3 Nơi

1. Bảng TUI import: Tự động xuống dòng, giữ lại nguyên chuỗi lỗi;
2. `tui.log`: Ghi chép có cấu trúc về stage, chapter/range, model, attempt và error;
3. `meta/import/failures/`: Giữ lại siêu dữ liệu lỗi của lần thất bại cuối cùng và cả phần trả lời nguyên xi chưa bị cắt gọt của model.

Chính văn tiểu thuyết gốc không xuất hiện trong nhật ký thông thường, cũng không đi vào bộ xuất chẩn đoán ẩn danh (desensitized diagnostic export) mặc định. Phản hồi hỏng nằm trong chính thư mục sách của người dùng, báo lỗi sẽ chỉ đích danh đường dẫn cụ thể.

### 14.3 Session và Usage

Mỗi lần gọi ngữ nghĩa ghi lại:

- Tên task ổn định, như `import/segment/0003`, `import/analyze/0054-0061`;
- Câu trả lời gốc của assistant;
- provider/model và usage;
- structured mode, thinking level và kết quả kiểm tra xác thực đầu ra.

Usage thống nhất quy vào vai trò architect, ngân sách nhìn thấy được chi phí dành cho nhập (import).

## 15. Vòng Đời và Đồng Thời (Concurrency)

- Việc nhập loại trừ lẫn nhau (mutually exclusive) với thao tác ghi của Engine, đồng sáng tạo theo giai đoạn, và simulation;
- Trong khi đang nhập, một cuốn sách chỉ được phép có duy nhất một Runner;
- Người dùng Hủy (cancel) sẽ hủy các lệnh gọi model đang dang dở, các sự kiện trong không gian làm việc đã được ghi nguyên tử xuống đĩa thì được giữ lại;
- Hủy trước khi xác nhận sẽ không thay đổi Store chính thức;
- Hủy sau khi bắt đầu phát hành sẽ không thực hiện rollback (quay lui) mang tính đoán mò, lần sau chỉ có thể khôi phục việc phát hành một cách chính xác;
- Ở phiên bản đầu tiên, giữa các analysis batch (lô phân tích) là xử lý nối tiếp, bên trong một lô thì model trả về sự kiện theo thứ tự chương trong một lần gọi; phát hành chính thức cũng là nối tiếp theo chương;
- Khi gọi `Host.New/Resume` sẽ phát hiện thấy trạng thái nhập dở dang (được giải thích ở phần trước).
