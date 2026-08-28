# ainovel-cli

Engine sáng tác tiểu thuyết dài tập tự động hoàn toàn bằng AI. Engine xác định (deterministic) sẽ chạy toàn bộ cuốn sách, model được sử dụng chính xác ở mỗi vị trí cần đánh giá: Engine điều khiển ba tác nhân sáng tác tự chủ Architect / Writer / Editor dựa trên định tuyến sự thật (fact route), phán quyết ngữ nghĩa sẽ đánh thức Arbiter khi cần thiết. Từ một câu yêu cầu đến tiểu thuyết hoàn chỉnh, toàn bộ quá trình không cần sự can thiệp của con người.

<p align="center">
  <img src="scripts/sample.gif" alt="ainovel-cli demo" width="800">
  <img src="scripts/novel.png" alt="ainovel-cli bg" width="800">
</p>

## Đặc điểm

- **Engine xác định + Hợp tác đa tác nhân (Multi-agent)** — Engine điều phối ba tác nhân sáng tác tự chủ Architect / Writer / Editor dựa trên bảng quyết định sự thật, vòng lặp chính không tốn chi phí LLM, hành vi có thể kiểm thử toàn diện
- **Phán quyết ngữ nghĩa có thể kiểm toán** — Các đánh giá như chọn nhà quy hoạch, phân loại can thiệp, lối thoát thất bại được Arbiter hoàn thành bằng một lần gọi, mỗi lần phán quyết được lưu trữ và có thể phát lại. Càng đơn giản càng ổn định, từ chối việc điều phối phức tạp
- **Khôi phục checkpoint cấp độ Step** — Sau khi mỗi công cụ thực thi thành công sẽ ghi vào checkpoint, sau khi sập có thể khôi phục chính xác đến cấp độ bước plan/draft/check/commit
- **Quy hoạch cuộn hai lớp tập-arc** — Truyện dài không còn quy hoạch toàn bộ các chương trong một lần. Ban đầu chỉ quy hoạch bộ khung 2 tập + chi tiết các chương của arc đầu tiên, các arc/tập tiếp theo sẽ được Architect triển khai khi quá trình sáng tác đẩy tiến đến đó, mỗi lần triển khai đều tham khảo tóm tắt phần trước và trạng thái nhân vật, quy hoạch dài hạn không bị sáo rỗng
- **Gợi ý thông minh chương liên quan** — Khi sáng tác mỗi chương, tự động gợi ý các chương lịch sử liên quan từ bốn chiều: chi tiết gieo mầm, sự xuất hiện của nhân vật, sự thay đổi trạng thái và các mối quan hệ, kết hợp với báo trước của chương tiếp theo để đảm bảo tính liên tục của tiểu thuyết dài hơn 500 chương
- **Chiến lược ngữ cảnh thích ứng** — Tự động chuyển đổi giữa toàn bộ / cửa sổ trượt (sliding window) / tóm tắt phân tầng dựa trên tổng số chương, hỗ trợ truyện dài hơn 500 chương
- **Đánh giá chất lượng 7 chiều** — Editor đọc kiểm từ 7 chiều: tính nhất quán của thiết lập, hành vi nhân vật, nhịp độ, tính mạch lạc của tự sự, chi tiết gieo mầm, móc, và chất lượng thẩm mỹ. Chiều thẩm mỹ chia nhỏ thành 5 mục: cảm giác miêu tả/thủ pháp tự sự/mức độ phân biệt đối thoại/chất lượng dùng từ/sức lay động cảm xúc, mỗi mục bắt buộc phải trích dẫn văn bản gốc làm bằng chứng
- **Người dùng can thiệp theo thời gian thực** — Bất cứ lúc nào trong quá trình sáng tác đều có thể nhập ý kiến sửa đổi vào hộp nhập liệu (không cần tạm dừng), hệ thống sẽ tự động đánh giá phạm vi ảnh hưởng và viết lại các chương bị ảnh hưởng
- **Nghiệm thu từng chương tùy chọn** — Mặc định vẫn tự động hoàn toàn; khi cần kiểm soát chi tiết có thể dùng `/review on`, mỗi lần `/next` chỉ cho qua một chương mới, làm lại và khôi phục sự cố sẽ không tiêu hao giấy phép nhầm
- **Cổng vào kép TUI + Headless** — Vừa có thể quan sát và can thiệp theo thời gian thực trên giao diện tương tác, vừa có thể chạy liên tục không giao diện trên server, NAS hoặc CI
- **Hỗ trợ nhiều LLM** — OpenRouter / Anthropic / Gemini / OpenAI v.v. chuyển đổi tùy ý

## Kiến trúc

Thiết kế cốt lõi: **Lớp sự thật xác định, lớp ngữ nghĩa tự chủ**. Sự chuyển đổi trạng thái có thể đếm được sẽ do mã xác định thực thi (Engine + Route); các đánh giá có ranh giới rõ ràng sẽ tư vấn hàm LLM theo nhu cầu (Arbiter); sáng tác mở được giao cho vòng lặp LLM tự chủ (Workers). Tóm lại trong một câu: Một Engine xác định tuần tự, ba Worker tự chủ, một vài hàm Arbiter theo nhu cầu, một lớp sự thật hệ thống tệp.

```
┌─────────────────────────────────────────────────┐
│              Host / Engine (Xác định)             │
│  Đọc Store → Route → Chạy trực tiếp Worker → Lặp  │
│  Khởi động phán quyết / Phân loại can thiệp / Bế tắc thất bại → Tư vấn Arbiter theo nhu cầu │
└────┬──────────┬──────────┬─────────────┬────────┘
     │          │          │             │
 ┌───▼────┐ ┌───▼───┐ ┌────▼────┐   ┌────▼────┐
 │Architect│ │Writer │ │ Editor  │   │ Arbiter │
 │(Lặp LLM)│ │(Lặp LLM)│ │(Lặp LLM)│   │(Hàm LLM)│
 └───┬────┘ └───┬───┘ └────┬────┘   └─────────┘
     └──────────┼──────────┘
                │ Gọi công cụ (IO + checkpoint)
┌───────────────▼─────────────────────────────────┐
│                   Store                         │
│  Progress / Checkpoint / Outline / Drafts / ... │
└─────────────────────────────────────────────────┘
```

- **Engine** — Mỗi vòng đọc sự thật từ Store, phái Worker theo bảng quyết định Route, thi hành quyết định, không tham gia đánh giá văn học; phục hồi sau sự cố = đọc store chạy tiếp, không có phiên hội thoại nào để phục hồi
- **Arbiter** — Phán quyết ngữ nghĩa được đánh thức theo nhu cầu (chọn nhà quy hoạch, phân loại can thiệp của người dùng, lối thoát thất bại/bế tắc), đầu vào là sự thật, đầu ra là quyết định có cấu trúc, mỗi lần phán quyết đều lưu lại để kiểm toán và phát lại
- **Workers** — Vòng lặp sáng tác tự chủ của Architect / Writer / Editor với ngữ cảnh độc lập, hợp tác thông qua các tạo tác trong Store
- **Tools** — IO nguyên tử tệp đơn + phát lại idempotent; nộp chương sử dụng Saga bền vững + checkpoint, chỉ trả về JSON sự thật, không kèm theo chỉ thị

### Trách nhiệm của các Tác nhân

| Vai trò | Trách nhiệm | Công cụ |
|--------|------|------|
| **Arbiter** | Phán quyết ngữ nghĩa: khởi động chọn nhà quy hoạch, phân loại can thiệp của người dùng, lối thoát thất bại/bế tắc | Không (gọi LLM 1 lần, xuất ra quyết định có cấu trúc) |
| **Architect** | Tạo tên sách, tóm tắt tiểu thuyết, tiền đề, đại cương, hồ sơ nhân vật, quy tắc thế giới | `novel_context` `save_book` `save_foundation` |
| **Writer** | Tự chủ hoàn thành cấu tứ, sáng tác, tự kiểm và nộp của một chương | `novel_context` `read_chapter` `plan_chapter` `draft_chapter` `check_consistency` `commit_chapter` |
| **Editor** | Đọc văn bản gốc, đọc kiểm từ hai khía cạnh cấu trúc và thẩm mỹ | `novel_context` `read_chapter` `save_review` `save_arc_summary` `save_volume_summary` |

### Quy trình sáng tác

```
Yêu cầu của người dùng → Arbiter chọn nhà quy hoạch → Architect quy hoạch bộ khung+arc đầu → Writer viết từng chương → Editor đọc kiểm cấp arc
               (Phán quyết lưu lại)                                  ↑                   │
                                                             ├── Viết lại/Mài giũa ◄──────┘
                                                             │
                                                      Architect triển khai arc/tập tiếp theo
                                                     (Tham khảo tóm tắt trước + ảnh chụp nhân vật)
```

Mỗi bước "tiếp theo phái ai" được Engine suy luận qua bảng quyết định Route theo sự thật của Store (kiểm thử toàn diện vạn cấp tổ hợp cố định), không tiêu hao bất kỳ cuộc gọi LLM nào.

Writer hoàn thành mỗi chương theo thứ tự cố định (nội dung sáng tác hoàn toàn tự chủ, thứ tự gọi công cụ nghiêm ngặt):

1. `novel_context` — Tải ngữ cảnh (tóm tắt nội dung trước, chi tiết gieo mầm, trạng thái nhân vật, quy tắc phong cách, gợi ý chương liên quan)
2. `read_chapter` — Đọc lại phần trước để tìm lại giọng điệu và nhịp điệu
3. `plan_chapter` — Cấu tứ mục tiêu, xung đột, đường cong cảm xúc của chương này
4. `draft_chapter` — Viết toàn bộ chính văn của chương
5. `check_consistency` — Kiểm tra tính nhất quán dựa trên dữ liệu trạng thái (bắt buộc phải sau draft)
6. `commit_chapter` — Nộp bản thảo cuối, lưu các trường sự thật (`arc_end` / `next_chapter` / kho phản hồi v.v.), bước tiếp theo do Engine suy luận theo bảng quyết định Route

### Quy tắc chuyển đổi trạng thái

Hệ thống nội bộ chia trạng thái hoạt động thành hai lớp:

- **Phase** — Giai đoạn lớn, biểu thị tác phẩm hiện đang ở thời kỳ thiết lập, thời kỳ sáng tác hay đã hoàn kết
- **Flow** — Quy trình hoạt động hiện tại, biểu thị hệ thống lúc này đang sáng tác bình thường, đọc kiểm, làm lại, mài giũa hay xử lý can thiệp của người dùng

#### Phase

`Phase` áp dụng quy tắc "chỉ tiến không lùi":

```text
init -> premise -> outline -> writing -> complete
  \-------> outline ------^
  \--------------> writing
```

Ý nghĩa:

- `init` — Nhiệm vụ đã tạo, chưa hình thành thiết lập ổn định
- `premise` — Đã lưu tiền đề câu chuyện
- `outline` — Đã lưu đại cương, có thể bước vào sáng tác chính thức
- `writing` — Đã bước vào thời kỳ sáng tác chương
- `complete` — Quy trình toàn bộ sách kết thúc

Giải thích quy tắc:

- Cho phép cập nhật đồng cấu, ví dụ `writing -> writing`
- Cho phép tiến lên, ví dụ `outline -> writing`
- Không cho phép lùi lại, ví dụ `writing -> premise`、`complete -> writing`

#### Flow

`Flow` chỉ mô tả quy trình hoạt động trong thời kỳ sáng tác, cho phép chuyển đổi giữa một vài luồng công việc:

```text
writing   -> reviewing / rewriting / polishing / steering / writing
reviewing -> writing / rewriting / polishing / steering / reviewing
rewriting -> writing / steering / rewriting
polishing -> writing / steering / polishing
steering  -> writing / reviewing / rewriting / polishing / steering
```

Ý nghĩa:

- `writing` — Bình thường đẩy tiến chương tiếp theo
- `reviewing` — Editor đang đọc kiểm
- `rewriting` — Xử lý các chương bắt buộc làm lại
- `polishing` — Xử lý các chương chỉ cần mài giũa
- `steering` — Đang đánh giá và xử lý can thiệp của người dùng

Giải thích quy tắc:

- Cho phép `writing -> reviewing`, ví dụ sau khi nộp chương sẽ kích hoạt đọc kiểm
- Cho phép `reviewing -> rewriting/polishing/writing`, quyết định bởi kết quả đọc kiểm
- Cho phép `steering -> writing/reviewing/rewriting/polishing`, quyết định bởi phạm vi ảnh hưởng của can thiệp
- Không cho phép bước nhảy bất thường rõ ràng, ví dụ `rewriting -> reviewing`

Những quy tắc này hiện được ràng buộc thống nhất bởi xác thực nhẹ trong mã, tránh lùi trạng thái hoặc nhảy sang nhánh quy trình không hợp lý.

### Quy hoạch cuộn truyện dài

Phương án truyền thống quy hoạch toàn bộ các chương trong một lần, khi trên 300+ chương thì đại cương sáo rỗng, nhịp điệu giống như chạy đua tiến độ. Hệ thống này áp dụng **la bàn + quy hoạch cuộn tầm nhìn**, mô phỏng quy trình sáng tác thực tế của tác giả:

```
Quy hoạch ban đầu                 Khi arc kết thúc                 Khi tập kết thúc
┌────────────────────┐    ┌─────────────────────┐    ┌─────────────────────┐
│ Hướng đi cuối (La bàn)│    │ Editor đọc kiểm cấp arc│    │ Editor đọc kiểm cấp tập│
│ Bắt đầu 2 tập, sau tùy│    │ Tóm tắt arc + chụp NV  │    │ Tóm tắt tập          │
│ Chi tiết chương arc 1 │ →  │ Architect triển khai arc │ → │ Architect tự chủ tạo  │
│ Nhân vật + TG quan    │    │ Writer tiếp tục viết   │    │ Tập tiếp + cập nhật la bàn│
└────────────────────┘    └─────────────────────┘    └─────────────────────┘
```

- **La bàn (Compass)** — Hướng đi cuối cùng + tuyến dài hạn đang hoạt động + ước tính quy mô, mỗi ranh giới tập sẽ được Architect cập nhật, hướng đi của câu chuyện có thể tiến hóa theo sáng tác
- **Tạo theo nhu cầu** — Sau khi tập hiện tại viết xong, Architect dựa trên nội dung đã viết để tự chủ tạo tập tiếp theo. Quy hoạch ban đầu tạo 2 tập làm khởi đầu, các tập sau tạo theo nhu cầu
- **Arc bộ khung** — Chỉ có goal (mục tiêu) + ước tính số chương, khi đến nơi mới triển khai chi tiết các chương
- **Làm mịn dần** — Mỗi lần triển khai đều tham khảo tóm tắt phần trước, ảnh chụp nhân vật, quy tắc phong cách, càng viết về sau càng chính xác
- **Mẫu nhịp điệu chung** — Arc trưởng thành đột phá / arc thi đấu đối kháng / arc khám phá phát hiện / arc ân oán xung đột / arc thường ngày chuyển tiếp, mỗi loại arc có mật độ tham khảo và ánh xạ đề tài phù hợp

### Quản lý ngữ cảnh truyện dài

Tiểu thuyết 500+ chương sử dụng tóm tắt 3 cấp + ống nén 4 cấp + gợi ý thông minh:

```
Tập (Volume)→ Tóm tắt tập
└── Arc (Arc)→ Tóm tắt arc + Ảnh chụp nhân vật + Quy tắc phong cách
    └── Chương (Chapter)→ Tóm tắt chương (cửa sổ trượt 3 chương gần nhất)
```

- **Tóm tắt phân tầng** — Gần dùng tóm tắt chương, khoảng cách trung bình dùng tóm tắt arc, xa dùng tóm tắt tập, nén từng tầng không mất thông tin
- **Gợi ý thông minh chương liên quan** — Khi sáng tác mỗi chương sẽ tra ngược các chương lịch sử từ bốn chiều: chi tiết gieo mầm, xuất hiện nhân vật, thay đổi trạng thái, mối quan hệ, gợi ý Writer đọc lại theo nhu cầu
- **Báo trước chương tiếp theo** — Tải đại cương chương tiếp theo, giúp Writer thiết kế móc cuối chương và nối tiếp chi tiết gieo mầm
- **Phát hiện ranh giới arc** — Tự động nhận diện kết thúc arc/tập, kích hoạt đọc kiểm, tạo tóm tắt và triển khai arc/tập tiếp theo

#### Ống nén ngữ cảnh

Khi hội thoại vượt quá cửa sổ ngữ cảnh của model, sẽ nén theo từng cấp từ chi phí thấp đến cao:

```
ToolResultMicrocompact → LightTrim → StoreSummaryCompact → FullSummary
   Dọn kết quả công cụ cũ    Cắt bớt văn bản dài   Store nén 0 LLM       LLM tóm tắt chốt chặn
```

- **StoreSummaryCompact** — Dành riêng cho Writer, dùng tóm tắt chương, ảnh chụp nhân vật, sổ ghi chép chi tiết gieo mầm đã có trong store để thay thế trực tiếp tin nhắn cũ, chi phí 0 LLM
- **FullSummary tùy chỉnh cho tiểu thuyết** — Writer sử dụng prompt tóm tắt hướng tới tính liên tục của tự sự, yêu cầu rõ ràng giữ lại trạng thái nhân vật, manh mối chi tiết gieo mầm, các mục cần sửa từ bản thảo, mỏ neo phong cách
- **Gói khôi phục sau khi nén** — Sau FullSummary sẽ tự động đưa vào kế hoạch chương hiện tại, đại cương và ảnh chụp nhân vật, ngăn chặn Writer "mất trí nhớ" sau khi nén
- **Cầu chì (Circuit breaker)** — Khi nén thất bại liên tục sẽ tự động bỏ qua và cảnh báo rõ ràng, áp dụng chế độ mở một nửa, vòng sau tự động thử lại
- **Ước tính Token CJK** — Tiếng Trung `runes × 1.5`, sẽ không bị trễ kích hoạt nén do đánh giá thấp của `bytes/4`
- **Chuyển màu sức khỏe TUI** — Chiếm dụng ngữ cảnh xanh(<70%)→vàng(70-85%)→đỏ(>85%) hiển thị theo thời gian thực

## Bắt đầu nhanh

```bash
# Cài đặt một chạm (macOS / Linux, không cần Go)
curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/main/scripts/install.sh | sh

# Cài đặt phiên bản chỉ định
curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/v1.2.3/scripts/install.sh | sh -s -- v1.2.3

# Hoặc cài đặt qua Go
go install github.com/voocel/ainovel-cli/cmd/ainovel-cli@latest

# Xem phiên bản / Cập nhật lên phiên bản mới nhất
ainovel-cli --version
ainovel-cli update

# Lần chạy đầu tiên, tự động vào quy trình hướng dẫn (Chọn Provider → Nhập API Key → Base URL → Tên model)
ainovel-cli
```

> Windows hoặc cài đặt thủ công: Truy cập [Releases](https://github.com/voocel/ainovel-cli/releases/latest) để tải gói cho nền tảng tương ứng.
> Script cài đặt sẽ tải danh sách SHA256 từ cùng một GitHub Release, sau khi xác minh thông qua mới giải nén và cài đặt file nhị phân.

### Chế độ Headless

`--headless` không cần TUI, phù hợp để chạy liên tục trên server, NAS, CI hoặc task nền. Nó không cung cấp hướng dẫn cấu hình lần đầu, vui lòng chạy `ainovel-cli` một lần trước để hoàn tất cấu hình, hoặc tạo thủ công `~/.ainovel/config.json`.

```bash
# Sử dụng nhu cầu một câu để bắt đầu nhiệm vụ mới
ainovel-cli --headless --prompt "Viết một bộ tiểu thuyết huyền huyễn phương Đông dài tập, nhân vật chính bắt đầu từ một thị trấn nhỏ ở vùng biên giới"

# Đọc nhu cầu từ file
ainovel-cli --headless --prompt-file prompt.txt

# Khôi phục nhiệm vụ chưa hoàn thành trong cùng thư mục
ainovel-cli --headless
```

`--prompt` và `--prompt-file` chỉ có thể được sử dụng trong chế độ Headless, và không thể chỉ định cùng lúc. Đầu ra luồng của model được ghi vào stdout, sự kiện chạy được ghi vào stderr, nhật ký chạy đầy đủ được lưu trong `logs/headless.log` của thư mục tác phẩm.

### Docker

Image Docker phù hợp để chạy các tác vụ dài headless trên server/NAS, cũng có thể dùng `-it` để vào TUI. Khuyến nghị mount thư mục cấu hình và tác phẩm vào máy chủ:

```bash
mkdir -p config workspace

# TUI
docker run --rm -it \
  -v "$PWD/config:/root/.ainovel" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/voocel/ainovel-cli:latest

# Headless
docker run --rm \
  -v "$PWD/config:/root/.ainovel" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/voocel/ainovel-cli:latest \
  --headless --prompt "Viết một bộ tiểu thuyết huyền huyễn phương Đông dài tập, nhân vật chính bắt đầu từ một thị trấn nhỏ ở vùng biên giới"
```

Cũng có thể dùng Compose:

```bash
docker compose run --rm ainovel
docker compose run --rm ainovel --headless --prompt "Viết một truyện ngắn hồi hộp"
```

Sau khi vào TUI, giai đoạn khởi động hỗ trợ hai kiểu tương tác tiền trạm:

- `Bắt đầu nhanh`: Một câu đi thẳng vào sáng tác
- `Đồng sáng tạo quy hoạch`: Trò chuyện nhiều vòng với AI để làm rõ nhu cầu, **bên phải đồng bộ hóa theo thời gian thực bản thảo chỉ thị sáng tác được hệ thống sắp xếp**; Mỗi vòng AI chủ động cung cấp 1-3 gợi ý dẫn dắt, có thể nhấn liên tục phím số để điền vào, chỉnh sửa rồi gửi, nhấn `Ctrl+S` để vào sáng tác chính thức

Cả hai chế độ cuối cùng đều sẽ hội tụ thành cùng một bản chỉ thị sáng tác, sau đó đi vào cùng một engine sáng tác.

Khi đã có thiết lập thế giới hoặc đại cương câu chuyện tương đối dài, có thể trực tiếp tạo sách mới từ file ở trang chào mừng:

```text
/start ./outline.md
```

`/start` sẽ lấy toàn bộ văn bản của file làm yêu cầu sáng tác ban đầu, giao cho Architect để sắp xếp thành thiết lập nội bộ và đại cương động, không coi nội dung file là các chương đã hoàn thành. Nhập tiểu thuyết đã có và viết tiếp vẫn dùng `/import`.

### Quản lý nhiều tiểu thuyết

Mỗi cuốn tiểu thuyết gắn với thư mục khởi động, sản phẩm nằm ở `{cwd}/output/novel/`. Đổi thư mục khởi động = đổi sách, `cd` về lại để khởi động = tự động khôi phục từ checkpoint gần nhất. Cấu hình `~/.ainovel/config.json` chia sẻ toàn cục, không cần sao chép.

### File cấu hình

Khi chạy lần đầu sẽ tự động hướng dẫn tạo file cấu hình `~/.ainovel/config.json`. Sau khi vào TUI có thể nhập `/config` để thêm hoặc chỉnh sửa Provider, lưu nhiều model và thiết lập cửa sổ ngữ cảnh cho mỗi model; sau khi lưu có hiệu lực ngay. `/model` dùng để chuyển đổi giữa các model đã lưu.

Cũng có thể tạo file cấu hình thủ công, tham khảo `config.example.jsonc` ở thư mục gốc của repository. Hướng dẫn lần đầu cũng sẽ sao chép một bản vào `~/.ainovel/config.example.jsonc` để tiện xem offline trên máy.

```jsonc
{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "reasoning_effort": "medium",
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx",
      "base_url": "https://openrouter.ai/api/v1",
      "models": [
        { "name": "google/gemini-2.5-flash", "context_window": 200000 },
        { "name": "google/gemini-2.5-pro", "context_window": 1000000 }
      ],
      "extra": {
        "user_agent": "my-client/1.0",
        "headers": { "X-Custom-Client": "my-client" }
      }
    }
  },
  "style": "default"
}
```

#### Thứ tự tìm kiếm file cấu hình (cái sau ghi đè cái trước)

1. `~/.ainovel/config.json` — Cấu hình toàn cục
2. `./.ainovel/config.json` — Ghi đè cấp độ dự án (tùy chọn)

> `.ainovel/` cấp dự án là bản sao của `~/.ainovel/` toàn cục: cùng cấu trúc, chỉ là thư mục gốc chuyển từ thư mục home sang dự án hiện tại. Cấu hình để ở `./.ainovel/config.json`, quy tắc sáng tác để ở `./.ainovel/rules/*.md` (xem chi tiết phần "Khử mùi AI và Quy tắc tùy chỉnh" bên dưới). Thư mục này chứa khóa bí mật, đã được mặc định thêm vào `.gitignore`.

Giải thích quy tắc ghi đè:

- Các trường vô hướng (scalar) ghi đè theo thứ tự sau lên trước, ví dụ `provider`, `model`, `reasoning_effort`, `style`
- `providers` và `roles` hợp nhất theo key, trong cùng một mục thì ghi đè theo trường
- Các trường chưa điền sẽ kế thừa cấu hình lớp trên, ví dụ cấu hình cấp dự án chỉ ghi `base_url` sẽ giữ nguyên `api_key` trong cấu hình toàn cục
- Không hỗ trợ dùng chuỗi rỗng để xóa giá trị đã có của lớp trên; nếu cần xóa, vui lòng sửa trực tiếp file cấu hình có độ ưu tiên cao hơn

> ⚠️ Giá trị của `provider` (và `roles.*.provider`) là **tên key** trong `providers`——một con trỏ, không phải tên giao thức. Nếu cấp dự án chuyển `provider` sang một tài khoản không tồn tại trong `providers` toàn cục, bắt buộc phải bổ sung chứng chỉ của tài khoản đó (`api_key` / `base_url`) tại cấp dự án, nếu không khi khởi động sẽ báo "chưa cấu hình chứng chỉ".

`providers.<name>.models` là danh sách đối tượng model tùy chọn: `name` là tên model truyền cho Provider, `context_window` là cửa sổ nén ngữ cảnh dành riêng cho model, `json_schema` là lớp phủ ba trạng thái cho đầu ra có cấu trúc gốc (`true` xác nhận hỗ trợ, `false` xác nhận không hỗ trợ, bỏ qua thì dùng khả năng của adapter). Với proxy tùy chỉnh hoặc khi năng lực phụ thuộc vào model cụ thể thì nên điền rõ. Mảng chuỗi phiên bản cũ vẫn đọc được, lần tiếp theo lưu qua `/config` sẽ được chuẩn hóa thành danh sách đối tượng. Nếu không cấu hình, hệ thống sẽ thoái lui về model cùng Provider đã từng xuất hiện trong cấu hình.

Cửa sổ ngữ cảnh phân giải theo thứ tự "giá trị riêng của model → `context_window` cấp cao nhất (cũ) → registry model → chốt chặn 200K". Nó chỉ ảnh hưởng đến thời điểm nén ngữ cảnh cục bộ, không thay đổi giới hạn yêu cầu thực sự của API từ xa.

`/config` chỉ dùng để **chỉnh sửa định nghĩa của Provider** (giao thức / API Key / Base URL / kho model), không phụ trách "hiện tại dùng model nào"——để chuyển đổi model và cường độ suy luận vui lòng dùng `/model`. Danh sách model hỗ trợ `↑↓` chọn dòng, `←→` chọn trường, `Enter` chỉnh sửa tại chỗ ID model hoặc cửa sổ ngữ cảnh, `Delete` để xóa; ở cuối có thể thêm trực tiếp model mới, không vào trang chi tiết nhiều lớp nữa. Cửa sổ có thể nhập số nguyên, `128K`, `1M`, để trống biểu thị tự động phân giải. Lưu sẽ **ghi ngược lại file cấu hình đang có hiệu lực ở gần nhất**——thư mục dự án có `./.ainovel/config.json` thì ghi vào đó, không thì ghi vào `~/.ainovel/config.json` toàn cục——và áp dụng nóng ngay lập tức. Sửa đổi thông thường chỉ bổ sung đoạn Provider tương ứng; khi sửa đổi rõ ràng ID model, sẽ đồng bộ di chuyển cấp cao nhất, role và tham chiếu fallback trong cùng một lần ghi nguyên tử. Không thể xóa trực tiếp model đang được tham chiếu, cần phải chuyển đi bằng `/model` trước. API Key khi nhập vào luôn bị ẩn.

API Key và Base URL trong chi tiết Provider hỗ trợ chỉnh sửa tại chỗ, Key đã có chỉ hiển thị đầu cuối bị che dấu; "Kiểm tra kết nối" sẽ dùng bản thảo hiện tại và model đã chọn để gửi một yêu cầu thực tế nhỏ nhất, có thể tốn một ít dung lượng API, nhưng kết quả test sẽ không ngăn cản việc lưu hoặc kích hoạt hạ cấp tự động. Bất kỳ cấu hình nâng cao nào như `extra`, `extra_body`, `stream_idle_timeout` vẫn được duy trì trong file cấu hình thực tế hiển thị trên giao diện.

`reasoning_effort` là cường độ suy luận mặc định, các giá trị tùy chọn là `off` / `low` / `medium` / `high` / `xhigh` / `max`; bỏ qua hoặc chuỗi rỗng biểu thị dùng mặc định của model/provider. `roles.<role>.reasoning_effort` có thể ghi đè theo vai trò, khi không cấu hình sẽ kế thừa `reasoning_effort` ở trên cùng. Cường độ suy luận có hiệu lực theo "ý định × khả năng": Trong cấu hình lưu **ý định gốc** bạn chọn, khi truyền xuống thực tế sẽ bị giới hạn bởi **khả năng của model hiện tại** của vai trò đó——đổi sang model có khả năng thấp hơn chỉ làm giảm giá trị có hiệu lực của lần đó, ý định lưu không đổi, đổi lại model mạnh sẽ tự động phục hồi. Sau khi bảng `/model` của TUI chuyển đổi provider, model hoặc cường độ suy luận, sẽ ghi ngược lại cấu hình đang có hiệu lực (giống với `/config`: cấp dự án tồn tại thì ghi vào dự án, không thì ghi toàn cục).

`providers.<name>.api` chỉ có hiệu lực với `type: "openai"` hoặc `openai` cài sẵn, dùng để chọn endpoint của giao thức OpenAI: `chat` (mặc định, `base_url + /chat/completions`) hoặc `responses` (`base_url + /responses`). Nếu `base_url` đã bao gồm đường dẫn (ví dụ `/api/v3` của Huoshan Ark), đường dẫn đó sẽ được giữ nguyên; khi chỉ điền tên miền thì mặc định dùng `/v1` của OpenAI. Các proxy loại Codex thường cần cấu hình thành `responses`.

`providers.<name>.extra` là cấu hình cấp provider, sẽ truyền cho client HTTP bên dưới, thích hợp cấu hình `user_agent`, `headers`, `anthropic_beta` và các trường nhận diện proxy; `providers.<name>.extra_body` mới là tham số mở rộng cho phần thân (body) của yêu cầu, không nên nhầm lẫn hai cái này.

## Báo cáo chẩn đoán

Trong TUI nhập `/diag` có thể tiến hành phân tích chẩn đoán cho các sản phẩm output của cuốn tiểu thuyết hiện tại, tạo ra các phát hiện và gợi ý cải thiện có thể thực thi.

Chẩn đoán bao phủ bốn chiều:

- **Quy trình** — Vòng lặp làm lại bị kẹt, chỉ thị chuyển hướng chưa được tiêu thụ, trạng thái giai đoạn/quy trình bất thường, nhảy số chương
- **Chất lượng** — Điểm đánh giá các chiều liên tục thấp, tỷ lệ thực hiện hợp đồng, tỷ lệ làm lại, số chữ của chương bất thường
- **Quy hoạch** — Chi tiết gieo mầm đình trệ, la bàn lỗi thời, đại cương cạn kiệt, thiếu tóm tắt
- **Ngữ cảnh** — Nhân vật biến mất, khoảng trống dòng thời gian, dữ liệu mối quan hệ đình trệ

Mỗi phát hiện bao gồm: Mô tả vấn đề, bằng chứng dữ liệu, gợi ý cải thiện (trỏ tới prompt/flow/config cụ thể).

`/diag` đồng thời sẽ xuất ra một bản `meta/diag-export.md` **đã được làm mờ** (loại bỏ chính văn tiểu thuyết, chỉ giữ lại khung hành vi như gọi công cụ, chuỗi lỗi, số lần lặp). Khi gặp các vấn đề vòng lặp vô hạn / gián đoạn, hãy dán nó vào GitHub issue, tiện cho người bảo trì định vị khi không lấy được dữ liệu cục bộ.

## Chân dung phỏng viết

Đặt các bài văn tham khảo vào thư mục `simulate/` của thư mục khởi động hiện tại, sau đó nhập `/simulate` trong TUI. Hệ thống sẽ đọc đệ quy các file `.txt`, `.md`, `.markdown`, dùng model architect phân tích ngữ liệu, và ghi vào:

```text
output/novel/meta/simulation_profile.json
```

Khi chạy lại `/simulate`, sẽ bỏ qua các file không thay đổi dựa trên `relative_path + sha256`; nếu không có nội dung mới hoặc thay đổi, sẽ nhắc "Chân dung đã là mới nhất" và sẽ không gọi LLM. Nếu đã có chân dung và `simulate/` có thêm hoặc sửa bài văn, hệ thống sẽ tiếp tục tổng hợp dựa trên chân dung cũ.

Cũng có thể nhập chân dung đã tạo trước đó, tránh phân tích lặp lại cùng một lô bài văn:

```text
/simulate
/importsim ./profile.json
```

`/importsim` chỉ nhận JSON `simulation_profile.v1` được tạo bởi chức năng này, và hợp nhất theo dấu vân tay ngữ liệu, nguồn trùng lặp sẽ bị bỏ qua. Chỉ nhập file chân dung từ nguồn đáng tin cậy; nội dung nhập vào sẽ trở thành tham chiếu ngữ cảnh cho các Agent tiếp theo. Chân dung sẽ được bơm vào `novel_context` dưới dạng compact, Architect, Writer, Editor đều có thể đọc; mỗi Agent chỉ tham khảo cấu trúc, nhịp điệu, móc và thủ pháp thu hút độc giả, không sao chép cách diễn đạt hoặc thiết lập riêng biệt của văn bản gốc.

## Tiếp nhận sửa đổi thủ công

Có thể chỉnh sửa trực tiếp các chương đã hoàn thành trong `output/novel/chapters/*.md`. Hệ thống nhận diện sự thay đổi dựa trên SHA-256 của chính văn đã tiếp nhận, không phụ thuộc vào thời gian sửa đổi file:

```text
/sync --check   # Chỉ liệt kê các chương có thay đổi, không gọi model
/sync           # Tiếp nhận sửa đổi, xây dựng lại tóm tắt, dòng thời gian, chi tiết gieo mầm, mối quan hệ, trạng thái và ký ức phong cách
```

Khi phát hiện có sửa đổi chưa tiếp nhận, các hành động như khôi phục sáng tác, tiếp tục nhập và `/next` đều sẽ yêu cầu rõ ràng phải chạy `/sync` trước, tránh dùng sự thật cũ tiếp tục điều khiển các chương mới. `/sync` không viết lại chính văn của người dùng; model chỉ phụ trách trích xuất lại sự thật toàn chương và sở thích phong cách có thể tái sử dụng từ chính văn mới, các vấn đề về phiên bản file, phóng chiếu trạng thái và khôi phục sự cố đều do chương trình xử lý xác định. Các phần đọc kiểm, tóm tắt arc/tập và ảnh chụp nhân vật bị ảnh hưởng sẽ mất hiệu lực và được Editor xây dựng lại; thay đổi cốt truyện sẽ được giao cho Architect cập nhật kế hoạch tiếp theo trước khi viết tiếp, khi xác nhận kế hoạch cũ vẫn áp dụng được cũng sẽ lưu lại rõ ràng.

## Nhập (Import)

Trong TUI nhập `/import <đường dẫn file>` có thể **biên dịch ngữ nghĩa** một cuốn tiểu thuyết đã có vào dự án. Mỗi lần khởi động liên kết với một cuốn sách (`output/novel` dưới thư mục khởi động), vì vậy việc nhập thường được bắt đầu trực tiếp từ **trang chào mừng sau khi khởi động trong thư mục mới**——nó cùng hàng với "Nhập yêu cầu để bắt đầu sách mới", "Đồng sáng tạo bắt đầu sách mới", là cách thứ ba để bắt đầu một cuốn sách; khi engine đang sáng tác thì lệnh này sẽ bị từ chối. Ống nén đẩy tiến theo từng giai đoạn: Snapshot file nguồn (ingest) → LLM nhận diện ranh giới chương (segment) → Xác nhận chia tách → Trích xuất sự thật từng chương (analyze) → Tổng hợp tiền đề toàn sách / nhân vật / thế giới quan / đại cương phân tầng / la bàn theo tầng (synthesize) → Phát hành Foundation chính thức và lưu từng chương (publish). Ranh giới chương được model phán quyết theo ngữ nghĩa, không phụ thuộc vào quy tắc tiêu đề code cứng; phần Go chỉ quản lý tọa độ, xác minh ghi đè, tính idempotent và thứ tự.

Quy trình điển hình chỉ có ba bước——nhập, đối chiếu, chờ hoàn thành:

```text
/import ~/tieuthuyetcuatoi.txt   # ① Khởi động: bảng hiển thị tiến độ theo thời gian thực, sau khi chia tách xong sẽ dừng lại
                                  # ② Đối chiếu toàn bộ tiêu đề chương được liệt kê trên bảng: nhấn y xác nhận tiếp tục
                                  # ③ Tự động chạy xong Phân tích→Tổng hợp→Phát hành, hoàn thành xong dừng ở nghiệm thu, xác nhận không có vấn đề là có thể tiếp tục sáng tác
```

Chia tách không đúng? Nhấn Esc đóng bảng, dùng ngôn ngữ tự nhiên giải thích rồi nhận diện lại (sẽ lại dừng lại để đối chiếu):

```text
/import --guide=Màn giao thời·X cũng là chương độc lập     # Văn bản hướng dẫn có thể chứa khoảng trắng, đặt ở cuối lệnh
```

Toàn bộ tùy chọn (ba tùy chọn đầu sẽ được lưu lâu dài, sau khi khôi phục sự cố vẫn tuân thủ):

```text
/import ~/tieuthuyetcuatoi.txt --yes           # Không người trực: tự động chấp nhận chia tách và chạy hết tiến trình
/import ~/tieuthuyetcuatoi.txt --story=closed  # Trả lời trước "trạng thái câu chuyện còn nghi ngờ": xử lý theo hoàn kết (closed) / chưa hoàn (open)
/import ~/tieuthuyetcuatoi.txt --continue      # Sau khi nhập xong trực tiếp nối tiếp viết tiếp, không dừng ở nghiệm thu
/import                                        # Không tham số: khôi phục quá trình nhập chưa hoàn thành từ chỗ bị gián đoạn
```

Tiền đề và khôi phục:

- Chỉ có thể nhập vào **sách trống** (không có chương nào đã hoàn thành), không hỗ trợ ghép một cuốn sách khác vào tác phẩm đã có; file nguồn hỗ trợ `txt`/`md`, mã hóa UTF-8 / GB18030 (tự động nhận diện, nếu không thể giải mã tin cậy sẽ báo lỗi rõ ràng).
- Sản phẩm của mỗi giai đoạn nằm ở workspace `meta/import/` và liên kết theo vân tay đầu vào: Sau khi gián đoạn hoặc thất bại chạy lại `/import` chỉ bù làm phần thiếu, không gọi lại model, không cần nhớ "nhập đến chương mấy rồi". Khi có bản nhập chưa hoàn thành, trang chào mừng sau khi khởi động lại sẽ chủ động nhắc nhở tiến độ (ví dụ "Đã phân tích 210/300 chương"); trước khi khôi phục hoàn tất, engine bị chặn bởi cổng, sẽ không coi bán thành phẩm là một cuốn sách hoàn chỉnh để viết tiếp. Phản hồi gốc của model khi xuất ra thất bại được lưu tại `meta/import/failures/` để kiểm tra.
- Khi trạng thái câu chuyện được đánh giá tổng hợp là `uncertain` (không chắc chắn), quá trình sẽ dừng, dùng `--story=open|closed` để làm rõ rồi chạy lại là được.
- Mặc định sau khi phát hành xong sẽ thiết lập một lần nghiệm thu Hold, đợi bạn xác nhận rồi mới viết tiếp; `--continue` bỏ qua Hold này (ở chế độ review vẫn cần `/next`).
- Ba hàm ngữ nghĩa của quá trình nhập có thể chỉ định cấp model độc lập trong cấu hình `roles` (xem [Sử dụng model khác nhau theo vai trò](#Sử dụng-model-khác-nhau-theo-vai-trò)).

> Nguyên văn sẽ được lưu từng chữ thành chương đã hoàn thành, vì vậy việc nhập phù hợp với "viết tiếp cùng một cuốn sách". Nếu chỉ muốn mượn thiết lập để sáng tác hoàn toàn mới, vui lòng dùng cách thông thường để bắt đầu sách mới, miêu tả phong cách thiết lập mong muốn trong phần yêu cầu.

## Xuất (Export)

Trong TUI nhập `/export` có thể gộp các chương đã hoàn thành để xuất, mặc định là TXT, ghi vào `{thư_mục_tiểu_thuyết}/{tên_sách}.txt`. Xuất là thao tác chỉ đọc, giữa chừng sáng tác cũng có thể lấy "thành phẩm giai đoạn hiện tại" bất cứ lúc nào, không ảnh hưởng đến việc chạy của engine.

Định dạng do **hậu tố đường dẫn đầu ra** quyết định (`.txt` / `.epub`):

```text
/export                            # Mặc định TXT, {thư_mục_tiểu_thuyết}/{tên_sách}.txt
/export ~/Vuetsang.txt             # Hậu tố .txt → TXT
/export ~/Vuetsang.epub            # Hậu tố .epub → EPUB (Apple Books / WeChat Read / Trình đọc Kindle đọc được)
/export from=10 to=30 --overwrite  # Khoảng chương + ghi đè
/export from=10 ~/x.epub --overwrite
```

- **TXT** — `《Tên sách》` → Dấu phân cách tập → Chính văn chương (chế độ phân tầng truyện dài tự động thêm phân cách tập). Hai loại dữ liệu nội bộ **không vào bản xuất**: premise (bản thiết kế sáng tác, gồm độc giả mục tiêu / vùng cấm viết v.v. thông tin hậu trường, viết cho tác giả và engine xem), phân cách arc (dưới góc nhìn độc giả, arc là cấu trúc nội bộ quá chi tiết). Trình xuất tự động tạo "Chương N Tiêu đề", các tiêu đề lặp lại do writer tự mang trong chính văn (`# Chương N...` hoặc `# Tên chương`) sẽ bị lột bỏ.
- **EPUB** — Container chuẩn EPUB 3, gồm tên sách, metadata tóm tắt tiểu thuyết, trang bìa, mục lục và XHTML chia theo chương, định danh phái sinh ổn định dựa trên nội dung (khi xuất lại cùng một sách, trình đọc sẽ nhận diện là phiên bản cập nhật). Không kèm ảnh bìa.

Các chương chưa hoàn thành trong phạm vi sẽ bị bỏ qua và hiển thị trong kết quả, không tính là lỗi.

#### Sử dụng model khác nhau theo vai trò

Thông qua trường `roles` để phân bổ model khác nhau cho các tác nhân khác nhau, vai trò không được cấu hình sẽ dùng model mặc định:

```jsonc
{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "reasoning_effort": "medium",
  "providers": {
    "openrouter": { "api_key": "sk-or-v1-xxx", "base_url": "https://openrouter.ai/api/v1" },
    "anthropic": { "api_key": "sk-ant-xxx" }
  },
  "roles": {
    "writer": { "provider": "anthropic", "model": "claude-sonnet-4", "reasoning_effort": "high" },
    "architect": { "provider": "openrouter", "model": "google/gemini-2.5-pro", "reasoning_effort": "low" }
  }
}
```

Các vai trò có thể cấu hình: `architect` / `writer` / `editor`, cùng với ba cấp độ hàm ngữ nghĩa của quy trình nhập `import_segment` / `import_analyze` / `import_synthesize` (khi không cấu hình sẽ rơi vào architect; có thể chỉ định chia tách mang tính cơ học hơn sang model rẻ hơn để tiết kiệm chi phí). Phán quyết ngữ nghĩa Arbiter thống nhất sử dụng model default, hiện không mở cấu hình vai trò độc lập.

#### Proxy tùy chỉnh

Sau khi chọn bất kỳ Provider nào, chỉ cần điền địa chỉ proxy là được, hoặc dùng Custom Proxy và chỉ định loại giao thức API. `api_key` của proxy tùy chỉnh là tùy chọn; nếu proxy của bạn không cần xác thực, có thể bỏ qua:

```jsonc
{
  "provider": "my-proxy",
  "model": "gpt-4o",
  "providers": {
    "my-proxy": {
      "type": "openai",
      "base_url": "https://proxy.example.com/v1",
      "extra": {
        "user_agent": "my-client/1.0",
        "headers": { "X-Custom-Client": "my-client" }
      }
    }
  }
}
```

Provider hỗ trợ: `openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` / `ollama` / `bedrock` và bất kỳ proxy tùy chỉnh nào.

Nếu proxy là giao thức Anthropic, và giới hạn chỉ cho phép client Claude Code truy cập, `type` nên đặt thành `anthropic`, `anthropic_beta` đặt ở tầng cao nhất của `extra`, các header HTTP như Stainless đặt trong `extra.headers`:

```jsonc
{
  "provider": "claude-code-proxy",
  "model": "claude-sonnet-4-6",
  "providers": {
    "claude-code-proxy": {
      "type": "anthropic",
      "api_key": "sk-xxx",
      "base_url": "https://proxy.example.com",
      "extra": {
        "user_agent": "claude-code/2.1.183",
        "anthropic_beta": "claude-code-20250219",
        "headers": {
          "X-Stainless-Lang": "js",
          "X-Stainless-Package-Version": "0.94.0",
          "X-Stainless-Runtime": "node"
        }
      }
    }
  }
}
```

Nếu proxy là giao thức OpenAI/NewAPI, và giới hạn chỉ cho phép client Codex truy cập, `type` nên đặt thành `openai`, dùng `extra.user_agent` để ghi đè mặc định `litellm-go/0.1`, và truyền các header nhận diện Codex trong `extra.headers`. `Session_id` và `X-Codex-Turn-Metadata` trong ví dụ nên được thay bằng giá trị ngẫu nhiên ổn định; chúng đồng thời tương thích với mẫu truyền qua Codex của New API và kiểm tra dấu vân tay `x-codex-*` của sub2api:

```jsonc
{
  "provider": "codex-proxy",
  "model": "gpt-5.4",
  "providers": {
    "codex-proxy": {
      "type": "openai",
      "api_key": "sk-xxx",
      "base_url": "https://proxy.example.com/v1",
      "models": [
        { "name": "gpt-5.4", "context_window": 400000 },
        { "name": "gpt-5.4-mini" },
        { "name": "MiniMax-M3", "context_window": 1000000 }
      ],
      "api": "responses",
      "extra": {
        "user_agent": "codex-tui/0.142.3 (Mac OS 26.5.1; arm64) Apple_Terminal/470.2 (codex-tui; 0.142.3)",
        "headers": {
          "Originator": "codex-tui",
          "Session_id": "replace-with-random-session-id",
          "X-Codex-Turn-Metadata": "replace-with-random-turn-metadata"
        }
      }
    }
  }
}
```

Về `api_key`:

- `openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` các loại API được host này thường cần điền `api_key`
- `ollama` và `bedrock` cho phép không điền `api_key`; Bedrock cần cấu hình `region`, `access_key_id`, `secret_access_key` (tùy chọn `session_token`) trong `extra`
- Proxy tùy chỉnh đã chỉ định rõ `type` cho phép không điền `api_key`

Ví dụ cấu hình `ollama` nội bộ:

```jsonc
{
  "provider": "ollama",
  "model": "qwen3:latest",
  "providers": {
    "ollama": {
      "base_url": "http://localhost:11434/v1"
    }
  }
}
```

### Phong cách sáng tác

Thay đổi qua trường `style` của file cấu hình:

- `default` — Phong cách chung
- `suspense` — Suy luận hồi hộp
- `fantasy` — Kỳ ảo tiên hiệp
- `romance` — Ngôn tình

### Khử mùi AI và Quy tắc tùy chỉnh

Tích hợp sẵn một baseline (cơ sở) khử mùi AI (mặc định của nhà sản xuất): Danh sách đen cơ học (câu sáo rỗng / từ mệt mỏi, code tích hợp sẵn `rules.SystemDefaults()`, kiểm tra xác định khi commit) + tiêu chí đánh giá ngữ nghĩa `assets/references/anti-ai-tone.md` (truyền vào writer / editor để tránh và lấy dẫn chứng).

Muốn chồng thêm sở thích của mình **không cần sửa mã nguồn**: Trong thư mục `~/.ainovel/rules/` (toàn cục, để bất kỳ file `.md` nào, hợp nhất theo thứ tự từ điển tên file) hoặc thư mục `./.ainovel/rules/` (của cuốn sách này, cũng để bất kỳ `.md` nào, định dạng giống với toàn cục), **dùng ngôn ngữ bình thường để viết sở thích là được** (ví dụ "Nhân vật chính đừng viết thành thánh mẫu", "Dùng nhiều cảm nhận cơ thể", "Mỗi chương khoảng 3000 chữ", "Không được xuất hiện 'ở một mức độ nào đó'")——không định dạng, không YAML. Hệ thống sẽ dùng model để chuẩn hóa các yêu cầu ngôn ngữ tự nhiên này thành snapshot quy tắc sách (các ràng buộc có cấu trúc như phạm vi số chữ / từ cấm / ngưỡng từ mệt mỏi + sở thích phong cách), tự động tuân theo khi sáng tác, tự động kiểm tra cơ học khi nộp; baseline cơ học của các câu sáo rỗng và từ mệt mỏi AI phổ biến đã được tích hợp sẵn, không viết cũng dùng được, ghi đè tại chỗ gần nhất, có hiệu lực cộng dồn với baseline tích hợp.

### Văn phong tùy chỉnh (Voice Layer)

Tiêu chuẩn sáng tác và tiêu chí khử mùi AI cũng có thể được ghi đè trực tiếp, tương tự **không cần sửa mã nguồn, không cần biên dịch lại**. Thư mục ghi đè hai cấp: `<Thư_mục_đầu_ra>/style/` (sách hiện tại, đi theo sách——đổi máy khôi phục cùng một sách tải cùng một văn phong) > `~/.ainovel/style/` (toàn cục), cấu trúc thư mục:

```
style/
├── voice.md                          # Đoạn thêm tiêu chuẩn sáng tác (tích hợp được giữ lại, yêu cầu của bạn thêm vào sau, độ ưu tiên cao hơn)
├── anti-ai-tone.md                   # Đoạn thêm tiêu chí khử mùi AI (như trên)
├── styles/
│   └── xianxia.md                    # Thêm phong cách tùy chỉnh mới (tên file là tên phong cách, config ghi style: xianxia là dùng)
│                                     # (Nếu trùng tên file tích hợp ví dụ fantasy.md thì thay thế toàn bộ)
└── genres/
    └── xianxia/
        └── style-references.md       # Tham khảo đề tài của phong cách này (thay thế toàn file)
```

Ghi nhớ nhanh ngữ nghĩa: **Văn bản chỉ đạo (voice / anti-ai-tone) thì thêm vào cuối, thiết lập trước phong cách (styles / genres) thì thay thế toàn file**. Độ ưu tiên của phần thêm vào là chỉ thị cho model; các ràng buộc cần bắt buộc cơ học (từ cấm, số chữ) vui lòng viết trong thư mục rules ở trên. Thay đổi có hiệu lực sau khi khởi động lại (khôi phục checkpoint chính xác đến bước, khởi động lại không tốn chi phí). Các prompt loại thỏa thuận thực thi không mở cho phép ghi đè——các bất biến hợp tác được bảo vệ bởi lớp công cụ, đây cũng là lý do bạn có thể yên tâm sửa văn phong mà không làm hỏng hệ thống. Chi tiết thiết kế xem `docs/voice-layer.md`.

## Cấu trúc đầu ra

Tất cả dữ liệu sáng tác (chương, đại cương, nhân vật, tiến độ v.v.) được lưu trong thư mục output. Chạy lại sau khi bị gián đoạn sẽ tự động viết tiếp từ tiến độ lần trước. Xóa thư mục output sẽ bắt đầu sáng tác lại.

```
output/{tên_tiểu_thuyết}/
├── book.md             # Tên sách và tóm tắt tiểu thuyết (phóng chiếu có thể đọc)
├── chapters/           # Bản thảo cuối (Markdown)
│   ├── 01.md
│   └── ...
├── summaries/          # Tóm tắt chương (JSON)
├── drafts/             # Bản thảo chương
├── reviews/            # Báo cáo đọc kiểm
├── timeline.jsonl      # Sự thật dòng thời gian (nhật ký thêm mới)
├── timeline.md         # Phóng chiếu có thể đọc của dòng thời gian
├── premise.md          # Tiền đề câu chuyện
├── outline.json        # Đại cương chương phẳng (chỉ chứa các chương đã triển khai)
├── layered_outline.json # Đại cương phân tầng (chế độ truyện dài)
├── characters.json     # Hồ sơ nhân vật
├── world_rules.json    # Quy tắc thế giới
├── meta/
│   ├── book.json       # Nguồn sự thật duy nhất của thông tin tác phẩm
│   ├── compass.json    # La bàn hướng đi cuối cùng (chế độ truyện dài)
│   ├── progress.json   # Trạng thái tiến độ
│   ├── foreshadow.json # Sổ ghi chép chi tiết gieo mầm
│   ├── state_changes.jsonl # Nhật ký thêm mới sự thay đổi trạng thái nhân vật
│   ├── style_rules.json# Quy tắc phong cách sáng tác (chắt lọc khi ranh giới arc)
│   ├── snapshots/      # Ảnh chụp trạng thái nhân vật (truyện dài)
│   └── checkpoints.jsonl # Checkpoint cấp Step (thêm mới sau khi mỗi công cụ thành công)
```

## Khôi phục checkpoint (Breakpoint)

Viết một cuốn tiểu thuyết dài tập có thể mất vài giờ thậm chí vài ngày, việc sập giữa chừng, rớt mạng, Ctrl+C đều là những tình huống thường gặp. Hệ thống sẽ **tự động khôi phục khi chạy lại trong cùng thư mục**, không cần thao tác thủ công.

### Các kịch bản khôi phục

| Thời điểm gián đoạn | Hành vi khôi phục |
|---|---|
| Giai đoạn quy hoạch (đang xây dựng thế giới quan/đại cương) | Kiểm tra các thiết lập đã lưu, tự động bổ sung các mục còn thiếu |
| Đang viết một chương nào đó (có bản thảo chưa nộp) | Viết tiếp từ chương đó, đọc bản thảo đã có để tiếp tục |
| Đang trong quá trình đọc kiểm | Kích hoạt lại Editor đọc kiểm |
| Hàng đợi làm lại/mài giũa chưa dọn sạch | Tiếp tục xử lý các chương chờ làm lại |
| Gián đoạn triển khai arc/tập (đã đọc kiểm xong nhưng chưa triển khai arc tiếp theo) | Tự động nhận diện arc/tập bộ khung, kích hoạt Architect triển khai |
| Can thiệp của người dùng chưa hoàn thành | Bơm lại chỉ thị can thiệp của lần trước |
| Gián đoạn sáng tác bình thường | Tiếp tục từ chương tiếp theo |

### Nguyên lý hoạt động

Tất cả sản phẩm sáng tác được lưu trữ bền vững trong thư mục `output/`. Sau khi mỗi công cụ thực thi thành công sẽ ghi vào checkpoint (`meta/checkpoints.jsonl`). Khi khởi động lại:

1. Đọc `progress.json` + checkpoint gần nhất + tín hiệu chờ xử lý
2. Tạo chỉ thị khôi phục chính xác đến cấp step (ví dụ "draft Chương 7 đã lưu, vui lòng tiếp tục check_consistency")
3. Engine trực tiếp tính toán lại định tuyến từ store để chạy tiếp——không có phiên hội thoại nào cần khôi phục, tính idempotent của checkpoint đảm bảo an toàn khi phái lại

> Ghi file sử dụng các thao tác nguyên tử temp + fsync + rename, dù mất điện trong quá trình ghi cũng không làm hỏng dữ liệu đã có.

## Nghiệm thu từng chương

Hệ thống mặc định dùng chế độ `auto` để tiếp tục sáng tác tự chủ. Khi cần đọc kiểm từng chương, tránh khoảng thời gian đọc kiểm mà tiếp tục viết chương mới, có thể bật cổng nghiệm thu xác định:

```text
/review on   # Bật nghiệm thu từng chương; sau khi hoàn thành công việc hiện tại, sẽ đợi trước khi tạo chương mới thuận chiều
/next        # Chỉ cho qua chương tiếp theo; các hoạt động đọc kiểm và bảo trì cấu trúc arc/tập cần thiết vẫn tự động hoàn thành
/review off  # Khôi phục đẩy tiến tự động; nếu hiện tại đang tạm dừng, nhập tiếp chỉ thị này sẽ khởi động Engine
```

Giấy phép liên kết với số chương cụ thể. Chương chỉ tiêu hao giấy phép sau khi trạng thái khôi phục đệ trình được dọn sạch và commit checkpoint đã lưu, vì vậy tiến trình sập giữa chừng khi đệ trình cũng không lỡ viết thêm chương tiếp theo. Làm lại, mài giũa, đọc kiểm và bảo trì cấu trúc không thuộc về "chương mới", sẽ không bị cổng chặn lại.

## Can thiệp theo thời gian thực (Steer)

Trong quá trình sáng tác có thể bơm ý kiến sửa đổi bất cứ lúc nào qua hộp nhập liệu, **không cần tạm dừng hoặc khởi động lại**.

### Chế độ TUI

Sau khi sáng tác bắt đầu, hộp nhập liệu dưới cùng tự động chuyển sang chế độ can thiệp:

```
❯ Đẩy tuyến tình cảm lên chương 4, thêm cảnh diễn chung của nam nữ chính
```

Sau khi nhập, nhấn Enter, hệ thống tự động:
1. Ghi lại chỉ thị can thiệp vào `run.json` (dùng để khôi phục sự cố)
2. Arbiter lập tức phán quyết (truy vấn phản hồi cấp độ giây; các hành động điều khiển được nộp an toàn tại ranh giới chương)
3. Thực thi theo phán quyết: Sửa thiết lập chuyển cho Architect, viết lại chương đã có chuyển cho Editor xếp hàng, quy tắc sáng tác lưu ngay lập tức——mỗi lần kiểm toán phán quyết đều có thể phát lại

### Ví dụ can thiệp

| Chỉ thị can thiệp | Phản hồi có thể có của hệ thống |
|---|---|
| "Đổi nhân vật chính thành nữ" | Sửa thiết lập nhân vật, đánh giá xem các chương đã viết có cần làm lại không |
| "Đẩy tuyến tình cảm lên chương 4" | Điều chỉnh đại cương, có thể làm lại chương 4 và sau đó |
| "Thêm một nhân vật phản diện" | Cập nhật hồ sơ nhân vật và quy tắc thế giới, đưa vào ở các chương sau |
| "Nhịp độ chậm quá, đẩy nhanh tiến độ" | Điều chỉnh mật độ đại cương của các chương sau |
| "Viết đến chương 20" | Sáng tác liên tục đến chương 20, sau khi đệ trình ổn định thì tạm dừng |

## Triết lý thiết kế

> **Lớp sự thật xác định, lớp ngữ nghĩa tự chủ.** Model tự do ở những nơi không thể xác minh (viết cái gì, viết thế nào), bị ràng buộc ở những nơi có thể xác minh (thứ tự, tính idempotent, giai đoạn).

### Phương pháp chia ba, càng đơn giản càng ổn định

- **Chuyển đổi có thể đếm được thuộc về Code** — "Tiếp theo phái ai" là đọc sự thật tra bảng (`flow.Route` pure function, kiểm thử toàn diện vạn cấp tổ hợp), tỷ lệ lỗi tiến tới 0, chi phí LLM bằng 0
- **Đánh giá có ranh giới rõ ràng thuộc về Arbiter** — Chọn nhà quy hoạch, phân loại can thiệp, lối thoát thất bại: Đầu vào là sự thật, đầu ra là quyết định có cấu trúc, kiểm tra cơ học làm chốt chặn, mỗi phán quyết lưu lại có thể phát lại
- **Sáng tác mở thuộc về Worker** — Trong một chương Writer hoàn toàn tự chủ; khi công cụ thất bại sẽ trả về lỗi có cấu trúc và gợi ý lối thoát, do LLM tự sửa
- **Ranh giới code cứng, không code cứng đánh giá** — Code chỉ giữ các bất biến có thể chứng minh; sự đánh đổi trong sáng tác không thể đếm được giao cho model, không dùng từ khóa, ngưỡng điểm hoặc bảng quy tắc để giả mạo sự hiểu biết
- **Công cụ chỉ trả về sự thật** — IO nguyên tử tệp đơn + lỗi hiển thị + phát lại idempotent; nộp chương bằng Saga bền vững + checkpoint, giá trị trả về là các trường sự thật JSON (`final_verdict` / `pending_rewrites` / `arc_end`), không kèm theo bất kỳ chuỗi chỉ thị nào
- **Hàng rào sự thật, không phải hàng rào hành vi** — CheckpointDeltaGuard của Worker chỉ nhận sản phẩm đã lưu: chưa nộp mà muốn nghỉ việc sẽ bị chặn lại; hàng rào không tốn chi phí khi hành vi của model đúng đắn
- **Từ chối điều phối phức tạp** — Không có task queue, không có policy engine. Một vòng lặp tuần tự + một bảng quyết định + vài hàm phán quyết là toàn bộ luồng điều khiển
- **Model càng mạnh lợi ích càng lớn** — Chất lượng sáng tác và phán quyết hưởng lợi tuyến tính theo việc nâng cấp model; vỏ bọc xác định không cần sửa một dòng nào

### Chu trình kín tự động hoàn toàn

Nhập một câu, xuất tiểu thuyết hoàn chỉnh:

```
"Viết một bộ tiểu thuyết hồi hộp" → Xây dựng thế giới quan → Thiết kế nhân vật → Quy hoạch đại cương
                → Sáng tác từng chương → Đọc kiểm chất lượng → Tự động làm lại
                → Tóm tắt cấp arc → Ảnh chụp nhân vật → Hoàn bản đầy đủ
```

- **Engine điều phối xác định** — Mỗi vòng đọc lớp sự thật phân phái theo bảng quyết định, không phiên hội thoại, không chuyển tiếp; khôi phục sự cố = đọc store chạy tiếp
- **Writer sáng tác tự chủ** — Mỗi chương hoàn thành độc lập chu trình kín plan → draft → check → commit
- **Editor đọc kiểm tự chủ** — Phân tích các vấn đề cấu trúc qua nhiều chương, xuất ra phán quyết và phạm vi ảnh hưởng
- **Architect xây dựng tự chủ** — Suy luận từ một câu yêu cầu ra thiết lập hoàn chỉnh, tự chủ triển khai quy hoạch tiếp theo khi đến ranh giới arc/tập (tham khảo kho phản hồi đại cương do Writer lưu)
- **Tự động quản lý chi tiết gieo mầm** — Gài gắm, đẩy tiến, thu hồi toàn bộ quá trình do Agent tự theo dõi
- **Tự động điều chỉnh nhịp độ** — Theo dõi lịch sử tuyến tự sự và các loại móc, tránh các chương liên tiếp bị trùng lặp cấu trúc

### Tách rời Sự thật và Chỉ thị

Công cụ chỉ trả về sự thật, "bước tiếp theo" do Engine tính toán lại từ lớp sự thật trong mỗi vòng:

- `commit_chapter` / `save_review` lưu sự thật có cấu trúc (`final_verdict` / `pending_rewrites` / `arc_end` / kho phản hồi đại cương), không kèm theo bất kỳ chuỗi `[Hệ thống]` nào
- `flow.Route` đọc `Progress` + `Outline` v.v. sự thật để suy luận chỉ thị tiếp theo; mỗi lần sửa bảng quyết định bắt buộc phải sửa đặc tả (spec) toàn diện trước rồi mới sửa triển khai
- Quyết định ngữ nghĩa (phán quyết) toàn bộ lưu ở `meta/decisions.jsonl`: kiểm toán, phát lại offline, A/B regression (hồi quy)

Làm như vậy chỉ thị sẽ không bị gọi chuỗi nuốt mất, cũng không bị trôi dạt trong sản phẩm của công cụ. Sửa lỗi luồng chỉ cần sửa một nhánh + một spec.

## Tech Stack

- **Go 1.25** — Ngôn ngữ chính
- **[agentcore](https://github.com/voocel/agentcore)** — Nhân Agent cực giản (tool-calling + streaming)
- **[litellm](https://github.com/voocel/litellm)** — Giao diện LLM thống nhất
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** — Framework TUI terminal

## License

MIT

Dự án này tích cực tham gia và công nhận [cộng đồng linux.do](https://linux.do/).
