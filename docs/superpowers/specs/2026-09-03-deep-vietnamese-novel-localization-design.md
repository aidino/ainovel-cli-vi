# Thiết Kế Chi Tiết: Việt Hóa Sâu Hệ Thống Sáng Tác Tiểu Thuyết AI (Deep Vietnamese Novel Localization)

> **Mục tiêu:** Nâng cấp toàn diện hệ thống sáng tác tiểu thuyết của `ainovel-cli-vi` từ tầng dịch thuật bề mặt sang tầng văn học chuyên sâu. Đảm bảo tiểu thuyết sinh ra tự nhiên, loại bỏ triệt để "sáo ngữ convert" và lỗi dịch máy, kiểm soát chặt chẽ đại từ xưng hô, linh hoạt đa thể loại (từ Cổ phong / Tiên hiệp đến Đô thị / Thuần Việt), và tuân thủ các quy ước xuất bản tiếng Việt.
>
> **Nhánh Git:** `feat/deep-vi-localization` (Tách riêng biệt, tuyệt đối không merge vào `main`).

---

## 1. Bối cảnh & Vấn đề

Khi các mô hình ngôn ngữ lớn (LLMs) được giao nhiệm vụ viết tiểu thuyết bằng tiếng Việt, chúng thường gặp phải ba "căn bệnh" chí mạng:
1. **Hội chứng Sáo ngữ Convert & Cú pháp lai CJK:** Bắt chước các bản dịch convert thô của truyện mạng Trung Quốc ("hắn khóe miệng hơi nhếch lên", "trong lòng không khỏi dâng lên...", "hít vào một ngụm khí lạnh", "đáy mắt xẹt qua một tia sát ý", "sắc mặt đại biến", "nói thì chậm nhưng xảy ra rất nhanh").
2. **Loạn đại từ xưng hô (Pronoun Inconsistency):** Tiếng Việt có hệ thống đại từ nhân xưng phức tạp bậc nhất, biến thiên theo vai vế, quan hệ xã hội, tuổi tác và mức độ thân sơ. AI thường xuyên bị "nhảy ngôi" (đang xưng "tôi - anh" nhảy sang "em - anh", "ta - ngươi" nhảy sang "tôi - cậu").
3. **Cứng nhắc về quy ước trình bày:** Thiếu sự phân định rõ ràng giữa chuẩn xuất bản truyền thống (lời thoại gạch đầu dòng `- `) và chuẩn tiểu thuyết mạng hiện đại (lời thoại ngoặc kép `" "`); thiếu nhịp điệu thanh điệu hài hòa của tiếng Việt.

Thiết kế này giải quyết triệt để các vấn đề trên thông qua giải pháp **3 tầng tích hợp (Tri thức $\to$ Chỉ đạo Agent $\to$ Cấu hình & Rule Engine)**.

---

## 2. Kiến trúc 3 Tầng Chi Tiết

### Tầng 1: Tri thức nền tảng & Tiêu chuẩn Văn học (`assets/references/`)

Tầng này định nghĩa các quy chuẩn thẩm mỹ và cẩm nang sáng tác để các Agent tham chiếu.

#### 1.1. `assets/references/anti-ai-tone.md` — Sổ tay phán cứ "Vị AI" & Sáo ngữ Convert
Bổ sung danh mục chi tiết nhận diện và tiêu diệt các khuôn mẫu văn phong phi tự nhiên:
- **Bảng đối chiếu sáo ngữ Convert $\to$ Tiếng Việt tự nhiên:**
  - *Cử chỉ & Biểu cảm mặt:*
    - "khóe miệng co giật / khóe môi nhếch lên" $\to$ "nhếch mép / bật cười cay đắng / khóe môi thoáng giật".
    - "đáy mắt xẹt qua một tia [sát ý / lạnh lẽo / chế nhạo]" $\to$ "ánh mắt thoáng đanh lại / trong mắt lộ vẻ hung quang / ánh nhìn lạnh tanh".
    - "sắc mặt đại biến / cực kỳ khó coi" $\to$ "mặt biến sắc / mặt tái mét / sắc mặt sầm xuống".
  - *Phản ứng cơ thể & Tình huống:*
    - "hít vào một ngụm khí lạnh" $\to$ "rùng mình ớn lạnh / chết lặng / đứng tim".
    - "dưới chân bỗng nhiên mềm nhũn" $\to$ "khuỵu xuống / lảo đảo đứng không vững".
    - "nói thì chậm nhưng xảy ra rất nhanh" $\to$ Sử dụng câu văn ngắn, dồn dập để tái hiện tốc độ thực tế thay vì chèn câu sáo rỗng.
  - *Cảm xúc nội tâm:*
    - "trong lòng không khỏi [kinh hãi / bồn chồn]" $\to$ Thể hiện qua hành động hoặc suy nghĩ trực tiếp (VD: "Tim gã đập thình thịch, bàn tay nắm chặt chuôi kiếm ướt đẫm mồ hôi").
- **Quy chuẩn Cú pháp & Ngữ pháp tiếng Việt:**
  - Triệt tiêu câu bị động nhân tạo ("bị / được" dùng theo lối dịch máy tiếng Anh: "anh ấy bị đánh bại bởi..." $\to$ "gã đã hạ gục anh...").
  - Chống đảo ngữ vô nghĩa xuất phát từ ngữ pháp CJK (VD: "hắn ánh mắt nhìn sang" $\to$ "hắn đưa mắt nhìn sang").
  - Hạn chế các liên từ nối làm loãng nhịp điệu ("tuy nhiên", "ngoài ra", "hơn nữa", "bởi vì... cho nên...").

#### 1.2. `assets/references/dialogue-writing.md` — Cẩm nang Hội thoại & Hệ thống Xưng hô
- **Quy ước Trình bày (Dialogue Typography):**
  - *Chuẩn gạch đầu dòng (`- `):* Lời thoại xuống dòng gạch đầu dòng, lời dẫn ngắt bằng gạch ngang (VD: `- Cậu chắc chắn về việc này chứ? - Nam hỏi dồn. - Chắc chắn.`).
  - *Chuẩn ngoặc kép (`"..."`):* Lời thoại trong ngoặc kép (VD: `"Cậu chắc chắn về việc này chứ?" Nam hỏi dồn. "Chắc chắn."`).
  - Hướng dẫn áp dụng đồng nhất một chuẩn trong toàn bộ chương/cuốn sách, không pha trộn.
- **Hệ thống Đại từ Nhân xưng tiếng Việt:**
  - Hướng dẫn phân tầng đại từ:
    - *Gia đình / Thân tộc:* ông/bà, bác/chú/cô/dì, anh/chị/em, cha/mẹ - con.
    - *Xã hội / Đời thường:* tôi - anh/chị, cậu - tớ, mày - tao, gã, hắn, y, thị, ả.
    - *Cổ phong / Tiên hiệp phương Đông:* sư phụ - đồ nhi, huynh - đệ/muội, bản tọa, tại hạ, các hạ, huynh đài, tiền bối - vãn bối, trẫm, vi thần.
  - Quy tắc bất biến: Nhân vật không bao giờ nhảy đại từ xưng hô trong cùng một ngữ cảnh đối thoại nếu không có biến cố cảm xúc hoặc chủ ý chuyển tông.

#### 1.3. `assets/references/character-building.md` & `character-template.md` — Ma trận Xưng hô & Định danh
- **Bổ sung `Ma trận xưng hô (Address Matrix)` vào hồ sơ nhân vật:**
  Yêu cầu bắt buộc khi xây dựng nhân vật cốt lõi:
  ```markdown
  ### Ma trận xưng hô cốt lõi
  - Đối với [Nhân vật B]:
    - Gọi đối phương là: "Sư muội" | Tự xưng là: "Sư huynh"
    - Khi riêng tư / biến cố: "Nàng" - "Ta"
    - Ngôi thứ ba khi người dẫn chuyện kể: "chàng" / "hắn"
  ```
- **Cẩm nang Định danh theo khối Văn hóa:**
  - *Khối Cổ phong / Kỳ ảo phương Đông:* Họ tên Hán Việt trang nhã, đúng thanh vận (Tiêu Lam, Sở Mặc, Lục Thanh Vân...), tên môn phái/tổ chức có ý nghĩa triết học hoặc phong thủy.
  - *Khối Thuần Việt / Đô thị:* Họ tên người Việt tự nhiên (Họ phổ biến + Đệm + Tên chính: Nguyễn Thành Nam, Trần Thảo Linh, Lê Hữu Phước...), kết hợp biệt danh dân dã (Hùng "chó con", Tư béo...).
  - *Khối Tây phương / Khoa học viễn tưởng:* Tên La-tinh hóa hoặc phiên âm chuẩn mực (Arthur, Elena, Viktor...).

---

### Tầng 2: Tầng Chỉ đạo Agents (Prompts & Voice)

#### 2.1. `assets/voice.md` (Được tiêm vào `writer.md` qua `{{VOICE}}`)
- **Âm hưởng & Nhịp điệu Tiếng Việt:**
  - Phối hợp thanh điệu bằng/trắc để tạo nhịp điệu trầm bổng, truyền cảm cho câu văn.
  - Tận dụng vốn từ láy, từ tượng thanh, từ tượng hình của tiếng Việt để câu văn giàu hình ảnh thay vì giải thích khô khan.
  - Biến thiên nhịp câu: Cảnh chiến đấu / căng thẳng dùng câu ngắn, dồn dập, gãy gọn; cảnh hồi tưởng / nội tâm dùng câu dài, uyển chuyển.
- **Tiêu chuẩn "Không Convert":** Cấm tuyệt đối các cấu trúc câu dịch máy và sáo ngữ convert liệt kê trong `anti_ai_tone`.
- **Bám sát Ma trận Xưng hô:** Đối thoại phải tuân thủ nghiêm ngặt xưng hô định nghĩa trong `characters.md`.

#### 2.2. `assets/prompts/writer.md`
- Bổ sung hướng dẫn nhận diện và áp dụng cấu hình `dialogue_style` (`dash` hoặc `quote`).
- Bắt buộc kiểm tra ma trận xưng hô của các nhân vật tham gia cảnh trước khi viết bản thảo.
- Hướng dẫn miêu tả chiến đấu và tu luyện: Không sao chép công thức sáo rỗng ("kinh mạch đứt đoạn", "khóe miệng tràn máu tươi"), hãy miêu tả bằng cảm giác nghẹt thở, sức nặng thể xác, và tổn thương cụ thể.

#### 2.3. `assets/prompts/editor.md`
- **Zero-Convert Audit:** Quét bản thảo đối chiếu với `references.anti_ai_tone`. Nếu phát hiện sáo ngữ convert $\to$ trích dẫn nguyên văn câu văn vi phạm và bắt buộc yêu cầu sửa đổi.
- **Kiểm toán Xưng hô (Pronoun Audit):** Đọc kỹ các đoạn đối thoại; nếu có bất kỳ sự thiếu nhất quán nào về đại từ nhân xưng giữa hai nhân vật $\to$ báo lỗi `continuity` hoặc `character_voice`.
- **Kiểm toán Typography:** Đảm bảo toàn bộ lời thoại dùng chung một định dạng dấu câu.

#### 2.4. `assets/prompts/architect-long.md` & `architect-short.md`
- **Khảo sát Bối cảnh & Văn hóa:** Trong giai đoạn khởi tạo, Architect xác định bối cảnh văn hóa của cuốn sách (Thuần Việt, Cổ phong phương Đông, hay Kỳ ảo Tây phương).
- **Thiết lập Ma trận Xưng hô:** Khi sinh hồ sơ nhân vật trong `characters.md`, Architect tự động lập bảng ma trận xưng hô giữa nhân vật chính và các nhân vật phụ chủ chốt.
- **Định danh chuẩn mực:** Áp dụng cẩm nang đặt tên tương ứng với bối cảnh văn hóa đã chọn.

#### 2.5. `assets/prompts/arbiter-intervention.md` & `arbiter-failure.md`
- Định rõ nguyên tắc trọng tài: Tính tự nhiên của tiếng Việt và tính nhất quán của xưng hô là tiêu chuẩn tối cao khi phân xử tranh chấp giữa Writer và Editor.

---

### Tầng 3: Tầng Phong cách, Cấu hình & Rule Engine

#### 3.1. Bổ sung & Cập nhật Style Presets (`assets/styles/`)
- **`assets/styles/urban_vietnam.md` [MỚI]:** Phong cách Đô thị / Đời sống thuần Việt. Ngôn ngữ hiện đại, hóm hỉnh hoặc chân thực, đối thoại tự nhiên, mặc định dùng dấu gạch đầu dòng (`- `).
- **`assets/styles/oriental_cultivation.md` [MỚI]:** Phong cách Cổ phong / Tiên hiệp / Huyền huyễn thanh thoát. Giữ tinh hoa Hán Việt hào sảng, sạch bóng convert thô, mặc định dùng ngoặc kép (`"..."`).
- **Cập nhật các style hiện hữu (`default.md`, `fantasy.md`, `romance.md`, `suspense.md`):** Thấm nhuần tiêu chuẩn thẩm mỹ và nhịp điệu tiếng Việt.

#### 3.2. Cấu hình mẫu & Structured Rules (`config.example.jsonc`)
Bổ sung mẫu `user_rules.structured` chuyên dụng cho tiếng Việt:
```jsonc
{
  "user_rules": {
    "structured": {
      "forbidden_phrases": [
        "hít một ngụm khí lạnh",
        "hít vào một ngụm khí lạnh",
        "khóe miệng co giật",
        "khóe miệng nhếch lên",
        "đáy mắt xẹt qua một tia",
        "trong lòng không khỏi",
        "sắc mặt đại biến",
        "nói thì chậm nhưng xảy ra rất nhanh",
        "dưới chân bỗng nhiên mềm nhũn",
        "dưới chân mềm nhũn"
      ],
      "fatigue_words": [
        { "word": "dường như", "max_per_chapter": 2 },
        { "word": "tựa hồ", "max_per_chapter": 1 },
        { "word": "bỗng nhiên", "max_per_chapter": 3 },
        { "word": "thế nhưng", "max_per_chapter": 3 },
        { "word": "không khỏi", "max_per_chapter": 1 }
      ]
    },
    "preferences": "Ưu tiên văn phong tự nhiên, nhịp câu gãy gọn; lời thoại tuân thủ ma trận xưng hô; trình bày hội thoại dùng dấu ngoặc kép."
  }
}
```

---

## 3. Kế hoạch Kiểm thử & Xác minh

1. **Kiểm tra cú pháp & Load Assets:**
   - Chạy `go test ./assets/...` để xác minh hàm nạp asset, inject template (`{{VOICE}}`), và kiểm tra schema không bị lỗi.
2. **Kiểm tra hồi quy toàn bộ hệ thống:**
   - Chạy `go test ./...` đảm bảo không có code lỗi, không xung đột logic.
3. **Kiểm tra phần dư CJK & Encoding:**
   - Quét regex CJK trên toàn bộ các file markdown mới tạo hoặc cập nhật để đảm bảo không còn sót chữ Hán nào ngoài ý muốn.
4. **Kiểm tra nhánh Git:**
   - Xác nhận tất cả thay đổi nằm trọn vẹn trên nhánh `feat/deep-vi-localization` và nhánh `main` được giữ nguyên vẹn.
