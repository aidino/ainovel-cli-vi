# Hệ thống đánh giá ainovel-cli (Eval System)

> Đánh giá không phải là tạo ra một bộ kịch bản kiểm tra mới, mà là biến **bộ chẩn đoán sự kiện (`diag`), bộ thống kê văn phong toàn sách (`stylestat`), và bộ đọc kiểm nguyên bản 7 chiều (`ReviewEntry`)** vốn có của dự án thành các trình đánh giá (evaluator), bọc thêm một lớp khai thác (harness) xử lý hàng loạt ngoại tuyến. Một định nghĩa sự kiện duy nhất, hai nơi không còn trôi dạt.

---

## 0. Tại sao cần thiết kế lại

Tính ổn định đã được thông suốt: Truyện dài 235 chương / 1.27 triệu chữ viết xong trong một lần, vòng lặp quy hoạch cuốn chiếu (rolling plan) đã được thiết lập (xem `architecture.md` §9.1). Nút thắt cổ chai giờ đã chuyển sang —— **Chất lượng có thể lặp lại (Quality iteration)**:

- Sau khi sửa một prompt, quy trình có còn ổn định không? Chuỗi công cụ, đẩy tiến trạng thái, sự kiện lưu trữ có còn chính xác không?
- Chất lượng của chính văn, đại cương, đọc kiểm có thực sự được cải thiện, hay chỉ là lần này ngẫu nhiên rút được kết quả tốt?
- Trong truyện dài, nhân vật, dòng thời gian, chi tiết gieo mầm (foreshadow), ngữ cảnh có liên tục đáng tin cậy không?
- **Sự cố định văn phong ở cấp độ toàn sách** (mô hình câu cú lặp lại hàng chục lần mỗi chương, hình thái cuối chương lặp lại, lặp lại y hệt từng chữ qua các chương) có tốt lên hay xấu đi? Đây là thủ phạm thực sự đằng sau thực chứng 196 chương đạt 6.5/10, mà việc đọc kiểm từng chương (single-chapter review) tự nhiên sẽ mù mờ trước điều này.

Hiện tại những phán đoán này dựa vào "cảm giác + đọc mẫu thủ công". Hệ thống đánh giá phải biến việc sửa prompt từ cảm giác thành một **quy trình kỹ thuật có hồi quy, có bằng chứng, có đọc mẫu thủ công**.

Nhưng dự án này không cần, và cũng không nên sao chép nền tảng eval dùng chung của ngành (dataset / experiment / scorer / database / Web UI). Lý do rất đơn giản: **Cốt lõi của những khả năng này —— kiểm tra xác định và tín hiệu chất lượng —— đã tồn tại trong dự án, được viết bằng Go, và chia sẻ cùng một mô hình sự kiện với thời gian chạy (runtime).**

---

## 1. Luận điểm cốt lõi: Trình đánh giá đã tồn tại

Trong 4 loại trình đánh giá của hệ thống đánh giá, 3 loại đã được triển khai trong kho mã, chỉ là chưa bao giờ được gọi là "trình đánh giá":

| Trình đánh giá | Khả năng sẵn có trong dự án | Điểm vào | Đầu ra |
|---|---|---|---|
| **Chẩn đoán sự kiện xác định** | Một tập hợp các quy tắc công cụ (artifact) + quy tắc runtime của `internal/diag` | `diag.Diagnose(store)` | `Report{Stats, Findings}`, Finding mang Severity/Evidence |
| **Hồi quy văn phong cấp toàn sách** | `internal/stylestat` | `stylestat.Compute(input)` | Số lần xuất hiện kiểu câu/chương, câu lặp qua các chương, tỷ lệ câu ngắn cuối chương, nhầm lẫn tiêu đề |
| **Phán quyết chất lượng (rubric)** | Rubric được phiên bản hóa (ban đầu bắt nguồn từ 7 chiều `editor.md`) | LLM Judge (Làm A/B bằng thước đo cố định) | consistency/character/pacing/continuity/foreshadow/hook/aesthetic |
| **Xuất ẩn danh hành vi** | Xuất của `internal/diag` | `diag.WriteExport(store, rep, rc)` | Bộ khung hành vi, phục vụ đọc mẫu thủ công và lưu trữ |

`diag.Analyze(s *store.Store)` nhận vào một Store là có thể xuất ra `Report` hoàn chỉnh —— **Bản thân nó đã có thể chạy ngoại tuyến (offline) trên bất kỳ thư mục đầu ra nào**. `stylestat.Compute` là một hàm thuần túy. Điều này có nghĩa là hệ thống đánh giá không phải triển khai lại "chương đã được ghi xuống đĩa chưa, progress đã đẩy tiến chưa, checkpoint có tồn tại không, có pending tồn đọng không, quy trình có bị lặp vô hạn không" —— những điều này diag đã làm hết rồi, và mỗi quy tắc đều tương ứng với một hố sâu có thật từng gặp phải (`PhaseFlowMismatch`、`OrphanedSteer`、`OutlineExhausted`、`repeatedErrors`/`stuckStep` tương ứng với idleResume / cạn kiệt đại cương dẫn đến livelock / gọi công cụ bị in ra dưới dạng văn bản,...).

> **Công việc của hệ thống đánh giá không phải là tạo ra kiểm tra, mà là: Dẫn động hàng loạt + Chạy các trình đánh giá có sẵn trên đầu ra + Ánh xạ Finding/thống kê thành cổng chặn (gate) + Tổng hợp báo cáo.**

---

## 2. Nguyên tắc thiết kế

### 2.1 Trình đánh giá chính là trình chẩn đoán, tuyệt đối không tạo lại kiểm tra xác định

Kiểm tra xác định chỉ gọi `diag.Diagnose`, không phân tích lại `progress.json` / `checkpoints.jsonl` / `sessions/*.jsonl` ở tầng đánh giá. Lý do là quy tắc sắt DRY của dự án này: **"Thế nào là một trạng thái hợp lệ" chỉ có thể có một định nghĩa.** Nếu tầng đánh giá dùng Python để phân tích lại checkpoint và đánh giá xem commit có thiếu không, thì sẽ có hai định nghĩa về "commit hoàn thành", khi runtime sửa quy tắc diag mà tầng đánh giá không sửa theo, cổng chặn sẽ ngay lập tức bị sai lệch.

→ Harness của tầng đánh giá dùng **Go**, gọi in-process tới `diag` và `stylestat`, chia sẻ `internal/domain` và `internal/store` với runtime. Đây là sự khác biệt căn bản nhất giữa thiết kế này và phiên bản trước.

### 2.2 Hồi quy văn phong toàn sách là tín hiệu chất lượng số một

LLM Judge từng chương nhìn chương nào cũng "bình thường", nhưng nút thắt cổ chai lại chính là sự cố định qua các chương. Do đó xương sống xác định của việc hồi quy chất lượng **là `stylestat`, không phải LLM Judge**.

**Tiền đề: `stylestat.Compute` với ít hơn 5 chương sẽ trả về nil trực tiếp** (`stylestat.go` `minChapters=5`, mẫu quá nhỏ nên tần suất không có ý nghĩa). Vì vậy hồi quy văn phong **chỉ có hiệu lực ở tầng Quality / Longform với ≥5 chương**, Smoke với 1 chương sẽ không nhận được tín hiệu văn phong —— điều này quyết định chi phí và chiến lược mặc định bên dưới. Các chỉ số bao gồm:

- Số lần xuất hiện kiểu câu/chương của variant so với baseline (`patterns[].per_chapter`)
- Tỷ lệ câu ngắn kết thúc ở cuối chương (`ending.short_ratio` tiến gần đến 1 là bệnh)
- Số lượng câu lặp lại y hệt từng chữ qua các chương (`repeated_sentences`)
- Sử dụng nhầm lẫn định dạng tiêu đề (`title_formats`)
- Tỷ lệ từ thời gian ở phần mở đầu (`opening_time_rate`)

Đây là những chỉ số có chi phí LLM bằng 0, mang tính xác định, và đánh trúng vào nút thắt chất lượng. **LLM Judge là phần bổ sung, delta của stylestat là luồng chính.**

### 2.3 LLM Judge căn chỉnh theo rubric nguyên bản 7 chiều, không làm bộ mới

Judge không phát minh ra chiều đánh giá (dimension) mới —— các chiều này hoàn toàn tương đương với 7 mục của `domain.DimensionScore`, để so sánh baseline/variant.

**Nhưng rubric bắt buộc phải được phiên bản hóa, có thể cố định**, lưu dưới dạng bản chụp (snapshot) trong `evals/rubrics/*.json`, chứ không phải đọc trực tiếp `editor.md` lúc runtime. Lý do: khi đối tượng bị kiểm tra chính là `editor.md`, nếu trọng tài thay đổi cùng với `editor.md`, cơ sở đánh giá sẽ bị trôi —— trọng tài và đối tượng cùng chung nguồn gốc sẽ khiến việc đánh giá "sửa editor là tốt hay xấu" không thể thực hiện được. Vì vậy, rubric ban đầu **được phái sinh** từ 7 chiều của editor (đảm bảo đồng nhất tiêu chuẩn), sau đó **tiến hóa độc lập, tăng (bump) phiên bản một cách tường minh**; trong report có ghi lại đã dùng phiên bản rubric nào.

### 2.4 Finding xác định quyết định cổng chặn, LLM và con người chỉ phán quyết chất lượng

Căn chỉnh với quy tắc sắt kiến trúc "thống kê quy về mã nguồn, phán quyết quy về LLM":

- **Chỉ bằng chứng xác định mới có thể chặn (block) việc merge (hợp nhất)**: Finding `SevCritical` của `diag`, hoặc thất bại (fail) của các xác nhận hợp đồng (contract assertion) khai báo trong case.
- **LLM Judge và con người đọc mẫu chỉ đưa ra cảnh báo (warning) và manh mối để sắp xếp (sorting)**, không tự mình quyết định việc merge.
- Nói gọn: `Finding.Severity` ánh xạ trực tiếp sang cấp độ của cổng chặn, không đưa thêm phương pháp phân loại mức độ nghiêm trọng mới.

### 2.5 Đánh giá chỉ quan sát, không can thiệp luồng điều khiển

Đánh giá tái sử dụng `diag`, nhưng **loại bỏ `Action` và `Planner` của diag** —— đó là những thứ thuộc về luồng điều khiển runtime. Trong ngữ cảnh đánh giá, `diag.Report` chỉ lấy `Stats` và `Findings`, Action hoàn toàn bị bỏ qua. Đánh giá không tự động sửa prompt, không tự động rollback (quay lui), không tiếp tục chạy. Đây là phần mở rộng của quy luật người quan sát (`architecture.md` §2.3) trong ngữ cảnh đánh giá.

### 2.6 Bộc lộ rõ ràng thất bại

Không mock (giả lập) thành công, không nuốt (swallow) lỗi, không dùng template giả vờ là đã pass (vượt qua). Bất kỳ thất bại nào ở model, công cụ, cấu hình, hệ thống file, phân tích, hay judge, báo cáo đều ghi lại lý do rõ ràng. **Bản thân thất bại cũng là một kết quả đánh giá** —— một case chạy bị crash, cổng chặn sẽ là FAIL, chứ không phải "bỏ qua" (skip).

### 2.7 Mỗi lần chỉ xác minh một biến

Ràng buộc cứng của A/B: Cùng yêu cầu, cùng cấu hình, cùng model/provider, cùng phong cách, thư mục đầu ra bị cách ly. Baseline = prompt chính thức hiện tại, Variant = chỉ thay thế tệp prompt cần xác minh lần này. Không sửa đổi đồng thời Writer/Architect/Editor/Arbiter trong một lần thử nghiệm.

---

## 3. Bức tranh toàn cảnh kiến trúc

```text
[Cases]  evals/cases/*.json —— Tập hợp xác nhận tầng sự kiện, không phải các dòng dataset thông thường
   │
[Runner]  internal/eval —— host driver lắp ráp in-process (dừng khi đạt giới hạn số chương), bundle.Prompts ghi đè bộ nhớ để làm variant
   │       baseline run ┐
   │       variant  run ┘  mỗi cái dùng một thư mục output cách ly
   ▼
[Collectors]  Thu thập trên mỗi thư mục đầu ra:
   ├── diag.Diagnose(store)      → Report{Stats, Findings}      (Sự kiện + runtime)
   ├── stylestat.Compute(input)  → Thống kê văn phong toàn sách (Xương sống hồi quy chất lượng)
   ├── xác nhận hợp đồng của case→ Checkpoint/phase/hợp đồng công cụ dự kiến (những phần diag không bao phủ)
   ├── usage / cost / token      → Đọc từ meta/usage.json
   └── tool_calls                → Đếm số lần gọi công cụ thực tế từ meta/sessions/*.jsonl
   ▼
[Graders]
   ├── Cổng chặn xác định: Finding.Severity + Xác nhận hợp đồng → hard_fail / regression
   ├── stylestat delta: Chênh lệch chỉ số văn phong variant vs baseline
   ├── LLM Judge (tùy chọn): So sánh A/B bằng rubric 7 chiều
   └── Human: Con người đọc công cụ (artifact) baseline/variant
   ▼
[Report]  report.json (máy đọc) + report.md (người đọc) + Xuất ẩn danh hành vi
   └── Gate: PASS / WARN / FAIL
```

Hướng phụ thuộc: `eval → host → agents → tools → store → domain`, dùng chung theo chiều ngang `diag` / `stylestat`. Tầng đánh giá **không phụ thuộc ngược** vào luồng điều khiển runtime, chỉ đọc Store và các trình đánh giá chỉ đọc (read-only).

> **Triển khai hiện tại bao phủ luồng chính xác định**: Không có `--variant` thì là `mode=single`; truyền `--variant` thì là `mode=ab`, chạy cách ly baseline và variant cho cùng một case, đồng thời sinh ra delta. Các Collector đã kết nối với `diag.Diagnose`, hợp đồng của case, `stylestat.Compute`, `meta/usage.json`, và bộ đếm tool call của session; Các Grader đã kết nối với cổng chặn xác định, baseline/variant diag delta, cost/token/tool call delta, stylestat delta. Runner lắp ráp trực tiếp bằng `host.New` và đi kèm điểm dừng khi đạt giới hạn số chương, **không tái sử dụng `headless.Run` không có giới hạn chương**. LLM Judge và Human vẫn là các tầng tùy chọn tiếp theo, không tham gia vào cổng chặn xác định hiện tại.

---

## 4. Tại sao lại là Go in-process, không phải shell + Python

| Chiều | shell copy mã nguồn + Python phân tích (cách cũ) | Go in-process (Thiết kế này) |
|---|---|---|
| Kiểm tra xác định | Python phân tích lại JSON, hai định nghĩa với quy tắc diag | Gọi trực tiếp `diag.Diagnose(store)`, một định nghĩa |
| Chuyển đổi variant | Copy toàn bộ cây mã nguồn + `go build` lại thành hai file nhị phân | `bundle.OverridePrompt(...)` ghi đè bộ nhớ rồi lắp ráp host, không copy, không biên dịch lại |
| Hồi quy văn phong | Phải viết lại logic phân câu tiếng Trung của stylestat bằng Python | Trực tiếp gọi `stylestat.Compute` |
| Rubric cho Judge | Các chiều phân tán trong Python | Tái sử dụng `domain.DimensionScore`, đồng nguồn với online |
| Rủi ro trôi dạt (drift) | Cao: Khi runtime đổi mô hình sự kiện, eval không theo kịp | Thấp: Lỗi đổi trường sẽ bị bộc lộ ngay lúc biên dịch |

Sở dĩ `prompt_ab.sh` cũ phải copy mã nguồn và biên dịch lại là vì prompt được nhúng vào file nhị phân (`go:embed`). Nhưng `assets.Bundle.Prompts` là một struct thông thường, **runner chỉ cần thay đổi một trường trong bộ nhớ là làm được variant**, hoàn toàn không cần copy mã nguồn. Đây là sự đơn giản hóa lớn nhất khi viết harness bằng Go.

> **Ràng buộc triển khai**: `assets.Load` đi qua `loadPrompts` để cấp cho Worker prompt (architect/writer/editor) một hậu tố `WithSimulationGuidance` thống nhất. Nếu variant chỉ nhét văn bản thô vào `bundle.Prompts.Writer`, sẽ mất đi hậu tố chân dung mô phỏng mà baseline có, A/B sẽ không công bằng.
>
> Cách đúng là ghi đè thông qua `assets.OverridePrompt`, bên trong đi theo logic đóng gói y hệt như `Load`; eval không sao chép logic đóng gói.

> Tài liệu phiên bản trước giữ lại `prompt_ab.sh` / `prompt_ab_report.py` và "từng bước trích xuất khả năng". Thiết kế này từ bỏ con đường đó: Các vấn đề chúng giải quyết (chạy cách ly + tổng hợp chỉ số) chỉ là một tập con trong Go harness in-process, nếu miễn cưỡng tái sử dụng sẽ phải gánh thêm lớp keo giao tiếp (glue) giữa 3 ngôn ngữ shell/Python/Go. **Go harness là con đường chính duy nhất**; Go harness hiện tại đã bao phủ việc chạy cách ly baseline/variant, tổng hợp repeat và tính delta xác định. Các script cũ (`scripts/prompt_ab.sh`、`scripts/prompt_ab_report.py`) cùng với sổ tay thao tác của nó `docs/prompt-ab.md` đã bị xóa ngay khi thiết kế này ra đời, không còn được giữ lại.

---

## 5. Case Manifest

Case là đơn vị tối thiểu của đầu vào đánh giá, cũng là một bộ **xác nhận ở tầng sự kiện**. Dùng JSON để mô tả, tránh việc các quy tắc nằm rải rác trong các tham số command line.

```json
{
  "id": "writer_first_chapter_xianxia",
  "category": "smoke",
  "role": "writer",
  "description": "Xác minh chất lượng chính văn chương đầu của Writer và tính ổn định của chuỗi công cụ",
  "prompt": "Viết một truyện tiên hiệp dài tập, nhân vật chính đi lên từ một tạp dịch ở biên thành, nhờ trí nhớ bất thường phá được án cũ của tông môn và bị cuốn vào ván cờ trường sinh.",
  "style": "fantasy",
  "max_chapters": 1,
  "target_prompts": ["writer.md"],
  "rubric": "writer_chapter",

  "expect": {
    "phase": "writing",
    "min_completed_chapters": 1,
    "required_checkpoints": ["chapter:1:plan", "chapter:1:draft", "chapter:1:commit"],
    "no_pending": ["pending_commit", "pending_steer"]
  },

  "gate": {
    "max_severity": "warning",
    "max_cost_delta_ratio": 0.3,
    "max_tool_call_delta_ratio": 0.3,
    "stylestat_regression": "warn"
  }
}
```

**Ngữ nghĩa của các trường**:

- `expect`: Xác nhận hợp đồng cấp độ case, **chỉ khai báo những kỳ vọng mà các quy tắc diag chung không bao phủ được, và có liên quan chặt chẽ đến case này** (ví dụ: "case smoke này phải sinh ra đúng chapter:1:commit"). Những thứ chung chung như "không có pending tồn đọng / phase-flow nhất quán / không có khoảng trống chương" thì giao cho diag, không khai báo lại trong case.
- `category`: Tầng đánh giá ∈ `smoke` / `workflow` / `quality` / `longform` / `recovery` / `steering`. Quyết định chạy bộ cổng chặn nào và mặc định có bật stylestat/Judge hay không.
- `role`: Vai trò bị kiểm tra ∈ `writer` / `architect` / `editor`. Trực giao với `category` —— tầng quyết định "kiểm tra sâu đến mức nào", vai trò quyết định "kiểm tra Worker nào". Tầng Workflow chọn bộ xác nhận theo `role`.
- `max_severity`: Mức độ nghiêm trọng tối đa cho phép của diag Finding. Vượt qua là hard fail.
- `gate.max_cost_delta_ratio` / `gate.max_tool_call_delta_ratio`: Ngưỡng tăng trưởng về chi phí và số lần gọi công cụ của variant so với baseline; nếu bỏ qua thì mặc định là `0.3`, rõ ràng ghi `0` là không cho phép tăng, số âm là tắt cổng chặn delta đó.
- `rubric`: Bật phiên bản bảng điểm LLM Judge nào. Để trống thì không chạy Judge.
- `gate.stylestat_regression`: `block` / `warn` / `off`, điều khiển việc hồi quy văn phong có chặn hay không (chỉ có tác dụng với case ≥5 chương).

---

## 6. Phân tầng đánh giá

Mỗi tầng làm rõ **dùng trình đánh giá có sẵn nào**, tránh việc "tầng đánh giá tự viết lại phán đoán một lần nữa".

### 6.1 Smoke (Lần đổi prompt nào cũng phải chạy, tập tối thiểu)

Chỉ phán đoán hệ thống có còn chạy ổn định không, không đánh giá văn phong. 1 chương / giai đoạn quy hoạch là đủ bộc lộ.

| case | Mục tiêu | Trình đánh giá chính |
|---|---|---|
| `writer_first_chapter` | Writer hoàn thành chương đầu và commit | `expect.required_checkpoints` + diag |
| `architect_short` | Quy hoạch truyện ngắn lưu đủ premise/outline/characters/world_rules | Kiểm tra foundation đồng nguồn với diag `MissingSummaries` + `expect` |
| `architect_long` | Quy hoạch truyện dài lưu layered_outline/compass, mở rộng arc đầu | diag `OutlineExhausted`/`CompassDrift` + `expect` |
| `editor_review` | Đến điểm đọc kiểm Editor lưu review (đủ 7 chiều) | Xác nhận trường `ReviewEntry` |

Chi phí: 1 chương × baseline+variant, từ tính bằng giây đến vài phút, không bật Judge, không chạy stylestat (không đủ 5 chương, `Compute` trả về nil). CI mặc định chỉ chạy tầng này.

### 6.2 Workflow (Xác minh hành vi Agent tuân thủ hợp đồng kiến trúc)

**Kỷ luật then chốt: Xác nhận hợp đồng, không xác nhận trình tự công cụ chính xác.** Kiến trúc đặt cược vào quyết định tự chủ luồng của LLM (`architecture.md` §2.1), viết cứng (hardcode) thứ tự công cụ ở tầng đánh giá sẽ đưa lại sự "viết mã cứng cho hành vi LLM" vốn đã bị từ chối ở §10.13. Do đó ở đây chỉ xác nhận **sự kiện tất yếu**:

- Writer: checkpoint `chapter:N:commit` tồn tại; sau khi commit, tiểu đại lý kết thúc lượt này (không có chính văn quá dài nối đuôi); checkpoint draft có trước commit. **Không** xác nhận "bắt buộc phải theo đúng trình tự này: novel_context→read_chapter→plan→draft→check→commit".
- Architect: trong giai đoạn sáng tác, outline chỉ thêm vào không ghi đè toàn bộ (checkpoint của `expand_arc`/`append_volume`, không có lần ghi toàn bộ `layered_outline` thứ hai); sau khi mở rộng, outline phẳng (flat) có số chương khớp với layered.
- Editor: `ReviewEntry.Verdict` hợp lệ (accept/polish/rewrite); rewrite/polish bắt buộc phải sinh ra affected chapters; cuối arc có checkpoint `arc_summary`, cuối tập có `volume_summary`.
- Phân phát Engine: Chỉ lệnh Route khớp với Worker chạy thực tế (đọc từ session trace, diag `repeatedErrors` lo vòng lặp); phán quyết ngữ nghĩa đối chiếu với `meta/decisions.jsonl`.

Phần lớn có thể bao phủ trực tiếp bằng quy tắc diag + xác nhận checkpoint, một số ít (sau commit còn nối đuôi chính văn) cần thêm một kiểm tra trace nhẹ nhàng trong collector.

### 6.3 Quality (Luồng pass rồi mới chạy, đánh giá chất lượng nội dung)

Đi bằng hai chân:

1. **delta stylestat (Xác định, luồng chính)**: Chênh lệch chỉ số văn phong của variant vs baseline. Đây là bằng chứng cứng cho hồi quy chất lượng. **Yêu cầu case phải chạy đủ ≥5 chương** (nếu không `Compute` trả về nil, mục này đánh dấu `insufficient_sample`), vậy nên case Quality thuần 1 chương không thu được hồi quy văn phong, cần đặt `max_chapters` từ 5 trở lên.
2. **LLM Judge (Phụ trợ)**: Rubric 7 chiều A/B (xem §8).

Chỉ những case qua được §6.1/§6.2 mới vào Quality —— quy trình còn sai, bàn chất lượng vô nghĩa.

### 6.4 Longform & Recovery (Sửa đổi lớn / chạy ban đêm - nightly)

Không cần lần nào cũng chạy. Bao phủ độ ổn định của truyện dài và khả năng khôi phục, chính là sân nhà của quy tắc ngữ cảnh (context) và quy tắc runtime của diag:

- Viết liên tục 3 chương / 5 chương đầu → diag `GhostCharacter`/`TimelineGaps`/`RelationshipStagnation`/`ChapterGaps` + stylestat lặp qua các chương.
- Đọc kiểm cuối arc + mở rộng arc tiếp theo → `OutlineExhausted`/`StaleForeshadow`/`CompassDrift`.
- Người dùng can thiệp giữa chừng (steering case) → user_rules có ghi vào `meta/user_rules.json` không, các chương sau có tuân theo không.
- Khôi phục sau sự cố: Viết đến draft chương N rồi kill → Resume → diag xác nhận `checkpoints.jsonl` không có step trùng lặp, không ghi đè draft đã ghi đĩa, `pending_commit` cuối cùng về 0.
- Lạm dụng gọi công cụ / Bất thường chi phí → diag `repeatedErrors`/`stuckStep`/`streamIdleStorm` + usage delta.

---

## 7. Cổng chặn xác định

Cấp độ cổng chặn được phái sinh trực tiếp từ **Severity của diag Finding** + **Xác nhận hợp đồng của case**, không lập cách phân loại riêng.

### 7.1 Hard Fail (Chặn merge)

- Tiến trình panic / headless trả về error.
- diag sinh ra Finding `SevCritical` (như `InvalidPendingRewrites` / `PhaseFlowMismatch` v.v.).
- Xác nhận hợp đồng `expect` của case thất bại: thiếu checkpoint commit, phase chưa đạt kỳ vọng, khai báo pending chưa về 0.
- Số lượng lỗi / số lượng Finding critical của variant nhiều hơn baseline (Hồi quy về mức tệ hơn).

### 7.2 Regression (Mặc định là warning, có chặn hay không do case gate quyết định)

- diag thêm Finding `SevWarning` mới (variant nhiều hơn baseline).
- tool calls / cost / input token / output token có mức tăng vượt quá ngưỡng của case (mặc định 30%).
- **Hồi quy stylestat**: Tần suất số kiểu câu trên mỗi chương tăng, tỷ lệ câu ngắn cuối chương tăng, số câu lặp qua các chương tăng, xuất hiện nhầm lẫn tiêu đề —— dựa vào `gate.stylestat_regression` để quyết định warn/block.
- Số chữ trong chương thấp hơn 60% hoặc cao hơn 180% so với baseline (ngưỡng đồng nguồn với diag `WordCountAnomaly`).

### 7.3 Quality Gate (Con người làm chốt chặn cuối)

- LLM Judge chỉ làm phụ trợ và sắp xếp.
- Judge phán variant kém hơn rõ rệt → Phải có người đọc mẫu xác nhận.
- Con người đọc mẫu nhận định là suy thoái (degrade) → Chặn.
- Judge phán variant tốt hơn nhưng mặt xác định lại có hard fail → Vẫn chặn.

### 7.4 Điều kiện merge đề xuất

Sửa prompt thường ngày: Smoke pass hết + Workflow của vai trò mục tiêu pass hết (Smoke 1 chương không chứa hồi quy văn phong; nếu chạy case Quality ≥5 chương thì stylestat không có hồi quy rõ rệt).
Sửa đổi lớn: Thêm 2-3 case Quality + 1-2 case Longform + đọc mẫu thủ công.

---

## 8. LLM Judge

Judge hỗ trợ về chất lượng, bản chất là **dùng rubric đã được phiên bản hóa (ban đầu phái sinh từ 7 chiều của editor.md) để so sánh ngoại tuyến baseline/variant**. Rubric là thước đo cố định, tiến hóa độc lập với `editor.md` online (lý do xem §2.3), report ghi lại phiên bản rubric đã dùng.

### 8.1 Đầu vào (Kiểm soát dung lượng, tuyệt đối không nhét cả cuốn sách)

- Yêu cầu ban đầu của người dùng + đại cương/hợp đồng của chương hiện tại.
- Chính văn của **cùng một chương** ở baseline và variant.
- Tóm tắt 1-2 chương gần nhất + tóm tắt trạng thái nhân vật (đọc từ store).
- Lát cắt liên quan của stylestat cho chương đó (cho Judge thấy sự kiện "câu này đã bị lặp lại 7 lần trong toàn bộ quyển sách").

### 8.2 Đầu ra (Có cấu trúc, căn chỉnh theo 7 chiều)

```json
{
  "scores": {
    "consistency": 8, "character": 7, "pacing": 8, "continuity": 8,
    "foreshadow": 7, "hook": 7, "aesthetic": 6
  },
  "winner": "variant",
  "confidence": "medium",
  "reasons": ["Hành động của variant đẩy tiến tập trung hơn", "Baseline kể lể lại tình tiết cũ quá nhiều"],
  "risks": ["Variant đệm lót cho động cơ của nhân vật phụ hơi ít"]
}
```

- Các chiều đánh giá nghiêm ngặt bằng 7 mục của `domain.DimensionScore`, mỗi mục từ 0-10.
- `winner` ∈ baseline/variant/tie; `confidence` ∈ low/medium/high.
- Mỗi điều `reasons`/`risks` ≤ 80 chữ, trích dẫn bản gốc phải ngắn gọn.

### 8.3 Ranh giới

Judge **không thể**: Quyết định luồng có pass hay không, sửa đổi công cụ sinh ra, tự động sửa prompt, làm căn cứ duy nhất để merge, sinh ra trích đoạn truyện dài.
Judge **có thể**: Xếp hạng cho việc đánh giá thủ công, đánh dấu các suy thoái rõ ràng, tóm tắt sự khác biệt A/B, bộc lộ tác dụng phụ của việc sửa prompt.

---

## 9. Báo cáo

Mỗi lần thử nghiệm tạo ra `report.json` (máy đọc, có thể tái sinh thành markdown) + `report.md` (người đọc) + `artifacts/{case_id}/{baseline,variant}/` (công cụ nguyên thủy sinh ra). Khi có `--repeat N`, đường dẫn sẽ là `artifacts/{case_id}/rN/{baseline,variant}/`.

### 9.1 Chỉ số delta

Báo cáo hiển thị chênh lệch của variant so với baseline, giá trị tuyệt đối và tỷ lệ được đặt song song:

```text
completed: baseline=5 variant=5   ← ≥5 chương, chỉ số văn phong mới có ý nghĩa
tool_calls: baseline=12 variant=16  +4 (+33.3%)
cost_usd: baseline=0.42 variant=0.55  +0.13 (+31.0%)
output_tokens: baseline=8200 variant=9100  +900 (+11.0%)
critical_findings: baseline=0 variant=0
warning_findings: baseline=1 variant=2  +1
stylestat.pattern_top_per_chapter: baseline=3.1 variant=5.4  +2.3   ← Hồi quy văn phong
stylestat.ending_short_ratio: baseline=0.42 variant=0.71  +0.29     ← Tình trạng đồng hình ở cuối chương nặng thêm
```

### 9.2 Tổng hợp Repeat

Khi `--repeat N` không chỉ nhìn lần cuối cùng, triển khai hiện tại hiển thị tỷ lệ pass, số lần hard fail, số lần warning, và min/avg/max của cost/tool_calls. Sau khi kết nối Judge sẽ bổ sung thêm phân phối winner, tránh việc trộn lẫn nhiễu của trọng tài model vào báo cáo xác định mặc định.

```text
writer_first_chapter_xianxia repeat=3
- pass_rate: 3/3
- cost_usd: avg=0.41 min=0.38 max=0.44
- tool_calls: avg=13 min=12 max=15
- stylestat.pattern_top_per_chapter: avg delta=+0.4 (Không có hồi quy đáng kể)
```

### 9.3 Báo cáo khả thi tối thiểu (MVP)

```text
Gate: FAIL

Hard Fail:
- writer_first_chapter_xianxia: missing checkpoint chapter:1:commit

Warnings:
- writer_dialogue_density: tool_calls +35%
- writer_anti_ai_tone: ending_short_ratio +0.28 (Hồi quy văn phong)

Quality:
- writer_anti_ai_tone: judge prefers variant, confidence=medium

Artifacts:
- workspace/evals/20260629-120000/report.json
```

---

## 10. Cấu trúc thư mục và Lệnh

```text
internal/eval/
  case.go        Cấu trúc Case manifest + Load
  eval.go        Điều phối CLI: single / A/B / repeat
  runner.go      Lắp ráp host driver (dừng khi đủ chương + drain đến Done), ghi đè bộ nhớ bundle.OverridePrompt
  collect.go     Chạy diag.Diagnose + stylestat.Compute + usage/tool_calls + xác nhận hợp đồng trên thư mục đầu ra
  grade.go       Ánh xạ Finding→Gate + baseline/variant delta + quyết định stylestat gate
  report.go      report.json + report.md

cmd/ainovel-cli  Điểm vào của lệnh con eval

evals/
  cases/         smoke/ workflow/ quality/ longform/ recovery/ steering/
  rubrics/       writer_chapter.json / architect_outline.json / editor_review.json
  variants/      writer-anti-ai-tone/writer.md v.v. (Mỗi thư mục chỉ chứa prompt cần thay)
  reports/       Lưu trữ báo cáo lịch sử
```

Lệnh:

```bash
# Lô nhiều case (CI mặc định chỉ chạy smoke, không bật judge)
ainovel-cli eval --cases evals/cases/smoke \
  --variant evals/variants/writer-anti-ai-tone \
  --out workspace/evals/writer-anti-ai-tone --ci
```

**Các tham số đã triển khai trong giai đoạn này**: `--cases` (Thư mục hoặc một manifest lẻ), `--variant` (Thư mục prompt biến thể; nếu có sẽ tự động chạy baseline+variant A/B), `--repeat N` (Chạy lặp lại mỗi case N lần), `--config`, `--out`, `--max-chapters N` (Ghi đè mặc định của case), `--timeout` (Giới hạn đồng hồ treo (wall clock) cho một case), `--ci` (Chặn các đầu ra cho từng sự kiện; mã thoát khác 0 nghĩa là hard fail, không dùng tham số này vẫn có hiệu lực).

**Đang được quy hoạch (chưa triển khai, không dùng trong command line kẻo báo lỗi chưa định nghĩa cờ)**: `--judge`/`--no-judge` (LLM Judge của Phase 3). Những thay đổi lớn của prompt hiện tại có thể dùng A/B xác định + repeat trước:

```bash
# Thay đổi prompt lớn: A/B + repeat để giảm tính ngẫu nhiên
ainovel-cli eval --cases evals/cases/quality \
  --variant evals/variants/writer-anti-ai-tone \
  --repeat 3 --ci
```

---

## 11. Việc rõ ràng không làm

Vi phạm đồng nghĩa với việc đánh giá đi chệch khỏi định vị.

1. **Không sao chép logic chẩn đoán chung của diag vào tầng đánh giá** —— Các phán đoán chung (pending tồn đọng, phase/flow nhất quán, khoảng trống chương, lặp vô hạn) đều đi qua `diag`, phán đoán sự kiện chỉ có một định nghĩa duy nhất. Các xác nhận hợp đồng cấp case (như `expect.required_checkpoints`) được phép đọc trực tiếp `store`/checkpoint API, nhưng chỉ thực hiện các **xác nhận mỏng (thin assertion)** —— xác minh kỳ vọng cụ thể liên quan chặt chẽ đến case đó, tuyệt đối không viết lại các quy tắc chung đã có của diag.
2. **Không tái triển khai các quy tắc xác định** —— diag đã có sẵn một bộ quy tắc công cụ (artifact) + quy tắc runtime. Thiếu quy tắc thì thêm vào diag, tầng đánh giá chỉ tiêu thụ.
3. **Không viết lại logic văn phong tiếng Trung của stylestat bằng Python** —— Gọi trực tiếp gói Go.
4. **Không để LLM Judge quyết định quy trình có pass hay không** —— Cổng chặn chỉ nhận bằng chứng xác định.
5. **Không để đánh giá can thiệp vào luồng điều khiển** —— Bỏ qua Action/Planner của diag, không tự động sửa prompt, không quay lui (rollback), không chạy tiếp, không phát hành.
6. **Không xác nhận chính xác một trình tự công cụ gọi ra** —— Chỉ xác nhận hợp đồng (có commit, checkpoint tồn tại), bảo vệ việc đặt cược vào "LLM dẫn động quy trình".
7. **Không đưa vào cơ sở dữ liệu / Web UI / Nền tảng đánh giá online** —— Ở giai đoạn hiện tại, thứ cần thiết là khả năng hồi quy cục bộ (local regression) có thể lặp lại, có thể áp dụng ngay, chi phí thấp.
8. **Không sao chép mã nguồn và biên dịch lại để tạo variant** —— Ghi đè lên `bundle.Prompts` trong bộ nhớ.
9. **Không mock thành công, không nuốt lỗi** —— Bất kỳ mắt xích nào thất bại cũng phải ghi lại rõ ràng, case crash nghĩa là FAIL.
10. **Case không thay đổi thường xuyên theo prompt** —— Case là tập kiểm thử ổn định; sửa case để cho variant pass là gian lận.

---

## 12. Triển khai theo từng giai đoạn

### Phase 1 · Runner + Cổng chặn xác định (MVP, Chứng minh giả thuyết trước)

- `internal/eval`: Cấu trúc Case + runner (in-process headless + ghi đè bundle) + collect (gọi `diag.Diagnose`) + grade (Finding→Gate + hợp đồng `expect`).
- Đặt 3-4 case vào `evals/cases/smoke/`.
- Báo cáo sinh ra trước `report.json` + markdown tối thiểu.

**Nghiệm thu**: Một lệnh chạy xong smoke; Writer bỏ qua commit, tồn đọng pending, thiếu checkpoint, phase không khớp **đều có thể bị cổng chặn chặn lại** (diag vốn đã kiểm tra được những cái này, ở đây xác minh harness đã kết nối đúng).

### Phase 2 · A/B + repeat + Hồi quy stylestat (Đã triển khai)

- `--variant` tự động chạy baseline và variant, xuất ra artifacts cách ly.
- `--repeat N` tổng hợp pass rate, hard fail runs, warning runs, min/avg/max của cost/tool_calls.
- collect thêm `stylestat.Compute`, grade thêm văn phong delta.
- Báo cáo hiển thị so sánh baseline-variant về: số lần lặp kiểu câu trên mỗi chương / tỷ lệ câu ngắn cuối chương / câu lặp qua các chương / nhầm lẫn tiêu đề.

**Nghiệm thu**: Dùng một case ≥5 chương + một variant "mắc chứng lặp lại kiểu câu trầm trọng", có thể bị hồi quy văn phong đánh dấu warning; các case chưa đủ số chương sẽ hiển thị rõ `insufficient_sample` chứ không bị tính nhầm là pass.

### Phase 3 · LLM Judge

- `evals/rubrics/` + `judge.go`, rubric 7 chiều A/B.
- Judge thất bại (JSON không hợp lệ) → Báo cáo ghi nhận thất bại, không ảnh hưởng đến kết quả xác định.

**Nghiệm thu**: Đầu ra của Judge vào json+md, và không làm ô nhiễm cổng chặn xác định.

### Phase 4 · Longform & Recovery

- Các case: 3-5 chương liên tục / đọc kiểm cuối arc / người dùng can thiệp / replay pending_commit / nén ngữ cảnh.
- Tái sử dụng ngữ cảnh (context) + quy tắc runtime của diag.

**Nghiệm thu**: Có thể phát hiện dòng thời gian bị lặp, tồn đọng pending, thiếu tóm tắt cuối arc, vòng lặp công cụ.

---

## 13. Quy chuẩn bảo trì Case

- **Hạn chế số lượng**: Smoke 3-5, Workflow mỗi vai trò 3-5, Quality 2-4, Longform/Recovery mỗi cái 2-3. Nhiều quá sẽ không ai muốn chạy.
- **Case tốt**: Đầu vào ngắn và rõ ràng, bao phủ rủi ro có thật, ít chương là bộc lộ được vấn đề, không phụ thuộc vào việc model tạo ra những câu cố định, không mô tả sở thích phong cách quá chi tiết.
- **Case tồi**: Đầu vào quá dài, đa mục tiêu cùng lúc, phải chạy mấy chục chương mới phán đoán được, chỉ có thể dựa vào cảm nhận chủ quan.
- **Đặt tên Variant**: `writer-anti-ai-tone` / `architect-rolling-outline` / `editor-strict-review`, mỗi thư mục chỉ chứa prompt cần thay.

---

## 14. Rủi ro và Ranh giới

- **Tính ngẫu nhiên của model**: Cùng prompt chạy nhiều lần cũng sẽ đổi. Sửa đổi quan trọng thì chạy `--repeat 3` để xem xu hướng.
- **Chi phí**: Judge và longform đốt tiền. Mặc định ở máy cá nhân (local) chỉ chạy **smoke** (1 chương × baseline+variant, cổng chặn diag xác định, không bật Judge, không chạy stylestat); **stylestat chỉ kích hoạt ở Quality/Longform có ≥5 chương** (smoke thiếu chương, `Compute` trả về nil, báo cáo đánh dấu `insufficient_sample`); bộ suite đầy đủ (complete suite) chỉ dành cho những sửa đổi lớn.
- **Độ lệch của Judge**: Judge cũng là model, thích những văn bản có tính giải thích gọn gàng, chưa chắc đã phải là tiểu thuyết hay —— vì vậy chỉ dùng làm phụ trợ, stylestat mới là luồng chính xác định.
- **Lạm dụng chỉ số**: Số chữ / số lần dùng công cụ / chi phí / thống kê văn phong đều là tín hiệu chứ không phải mục tiêu. Số liệu stylestat có thành "bệnh" hay không do con người dựa vào thể loại để phán quyết, **không viết cứng (hardcode) các ngưỡng** (giữ nguyên với editor.md).
- **Không tự động rollback online**: Đây là công cụ hồi quy ngoại tuyến (offline), không chịu trách nhiệm tự động sửa prompt / phát hành trên môi trường online.

---

## 15. Tổng kết

Giá trị của hệ thống đánh giá này không phải là tự động phán đoán chất lượng văn học, mà là biến việc sửa đổi prompt từ "dựa vào cảm giác" thành "có hồi quy, có bằng chứng, có con người đọc mẫu".

Điểm khác biệt căn bản nhất giữa nó và thiết kế trước chỉ gói gọn trong một câu: **Các trình đánh giá đã nằm sẵn trong kho mã rồi.** `diag` là trình chẩn đoán sự kiện xác định, `stylestat` là trình hồi quy văn phong toàn sách, 7 chiều của `ReviewEntry` là rubric gốc. Việc hệ thống đánh giá cần làm là một lớp Go harness mỏng manh —— dẫn động hàng loạt, thu thập, ánh xạ Finding và thống kê thành cổng chặn, tổng hợp báo cáo —— chứ không phải dùng ngôn ngữ khác để viết lại những phán đoán sự kiện này một lần nữa.

Một định nghĩa sự kiện, vĩnh viễn không trôi dạt (drift). Đây chính là kỷ luật được duy trì nhất quán từ kiến trúc cho đến đánh giá của dự án này: **Harness tối thiểu, tái sử dụng tối đa, tính xác định quy về mã nguồn, phán quyết quy về LLM và con người.**

---

## 16. Tham khảo

Cấu trúc chung của các LLM eval trong ngành (dataset / experiment / scorer / trace / regression gate) là nguồn tư tưởng cho thiết kế này, nhưng **chủ ý không bê nguyên xi** —— "scorer" của dự án này là `diag`/`stylestat` đã có sẵn, "trace" là tầng sự kiện checkpoint/session đã có sẵn, "dataset" là các case có chứa xác nhận ở tầng sự kiện.

- OpenAI Evals · https://developers.openai.com/api/docs/guides/evals (Ghi chú: Nền tảng Evals được host của họ đã công bố lộ trình nghỉ hưu, ở đây chỉ mượn **tư tưởng** của kiểm thử có cấu trúc/chấm điểm tự động/hiệu chuẩn thủ công, không làm phần phụ thuộc trong tương lai)
- Braintrust · https://www.braintrust.dev/foundations/what-is-an-eval
- LangSmith · https://docs.langchain.com/langsmith/evaluation-concepts
