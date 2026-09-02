# Kế Hoạch Triển Khai Việt Hóa Sâu Hệ Thống Sáng Tác Tiểu Thuyết AI (Deep Vietnamese Novel Localization Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Nâng cấp toàn diện chất lượng sáng tác văn học tiếng Việt cho `ainovel-cli-vi`, loại bỏ triệt để sáo ngữ convert/dịch máy, kiểm soát ma trận xưng hô, linh hoạt đa thể loại và quy cách trình bày hội thoại.

**Architecture:** Triển khai theo mô hình 3 tầng tích hợp (Tri thức nền tảng $\to$ Chỉ đạo Agent $\to$ Style & Cấu hình/Rule Engine).

**Tech Stack:** Go 1.24, Bubbletea, Litellm/Agentcore, Markdown Prompts/Assets.

**Spec:** [docs/superpowers/specs/2026-09-03-deep-vietnamese-novel-localization-design.md](file:///Users/ngohongthai/Documents/ainovel-cli-vi/docs/superpowers/specs/2026-09-03-deep-vietnamese-novel-localization-design.md)

## Global Constraints

- **Nhánh Git:** Toàn bộ công việc thực hiện trên nhánh `feat/deep-vi-localization`. Tuyệt đối KHÔNG merge vào nhánh `main`.
- **Độ tương thích Code Go:** Không làm vỡ bất kỳ struct, tool contract, hay parser nào (`assets/load.go` nạp assets phải pass 100%).
- **Kiểm thử liên tục:** Mỗi task đều chạy `go test ./assets/...` và `go test ./...` để đảm bảo không có regression.

---

### Task 1: Nâng cấp Sổ tay phán cứ "Vị AI" & Sáo ngữ Convert (`assets/references/anti-ai-tone.md`)

**Files:**
- Modify: `assets/references/anti-ai-tone.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Cấu trúc hiện có của `anti-ai-tone.md`
- Produces: Bản phán cứ hoàn chỉnh với danh mục sáo ngữ convert, bảng đối chiếu sửa đổi và quy chuẩn ngữ pháp tiếng Việt.

- [ ] **Step 1: Cập nhật nội dung `assets/references/anti-ai-tone.md`**
Bổ sung bảng đối chiếu sáo ngữ Convert (cử chỉ, biểu cảm mặt, phản ứng cơ thể, cảm xúc nội tâm), các lỗi ngữ pháp dịch máy (lạm dụng bị động, đảo ngữ CJK, lạm dụng liên từ nối) và hướng dẫn viết lại câu tự nhiên.

- [ ] **Step 2: Chạy unit test kiểm tra nạp asset**
Run: `go test -v ./assets -run TestLoadReferences`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add assets/references/anti-ai-tone.md
git commit -m "feat(assets): nâng cấp sổ tay phán cứ chống sáo ngữ convert và vị AI tiếng Việt"
```

---

### Task 2: Nâng cấp Cẩm nang Hội thoại & Hệ thống Xưng hô (`assets/references/dialogue-writing.md`)

**Files:**
- Modify: `assets/references/dialogue-writing.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Quy phạm viết hội thoại hiện có
- Produces: Hướng dẫn chi tiết 2 chuẩn typography hội thoại (gạch đầu dòng `- ` và ngoặc kép `" "`) cùng cẩm nang hệ thống đại từ nhân xưng tiếng Việt đa tầng bậc.

- [ ] **Step 1: Cập nhật nội dung `assets/references/dialogue-writing.md`**
Bổ sung mục quy chuẩn trình bày dấu câu hội thoại (gạch đầu dòng vs ngoặc kép), quy tắc phân tầng đại từ nhân xưng (gia đình, xã hội, cổ phong/tiên hiệp), và nguyên tắc chống "nhảy ngôi".

- [ ] **Step 2: Chạy unit test kiểm tra nạp asset**
Run: `go test -v ./assets -run TestLoadReferences`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add assets/references/dialogue-writing.md
git commit -m "feat(assets): chuẩn hóa quy cách hội thoại và cẩm nang xưng hô tiếng Việt"
```

---

### Task 3: Quy chuẩn Ma trận Xưng hô & Cẩm nang Định danh (`assets/references/character-building.md` và `character-template.md`)

**Files:**
- Modify: `assets/references/character-building.md`
- Modify: `assets/references/character-template.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Hồ sơ nhân vật và mẫu character template hiện có
- Produces: Trường `Ma trận xưng hô (Address Matrix)` bắt buộc cho nhân vật cốt lõi và cẩm nang định danh theo 3 khối văn hóa (Cổ phong, Thuần Việt, Tây phương).

- [ ] **Step 1: Cập nhật `assets/references/character-building.md` và `character-template.md`**
Thêm mục `Ma trận xưng hô cốt lõi` vào template nhân vật và viết cẩm nang định danh văn hóa chi tiết.

- [ ] **Step 2: Chạy unit test kiểm tra nạp asset**
Run: `go test -v ./assets -run TestLoadReferences`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add assets/references/character-building.md assets/references/character-template.md
git commit -m "feat(assets): bổ sung ma trận xưng hô và cẩm nang định danh văn hóa"
```

---

### Task 4: Nâng cấp Âm hưởng ngòi bút & Chỉ đạo Writer (`assets/voice.md` & `assets/prompts/writer.md`)

**Files:**
- Modify: `assets/voice.md`
- Modify: `assets/prompts/writer.md`
- Modify: `assets/testdata/writer-golden.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Template `{{VOICE}}` và prompt `writer.md`
- Produces: Chỉ thị bắt buộc về nhịp điệu thanh điệu tiếng Việt, tuân thủ ma trận xưng hô, kiểm soát dấu câu hội thoại, và triệt tiêu công thức convert.

- [ ] **Step 1: Cập nhật `assets/voice.md`**
Bổ sung các chuẩn mực âm điệu bằng trắc, từ láy/tượng thanh, cấm sáo ngữ convert và mệnh lệnh neo giữ xưng hô.

- [ ] **Step 2: Cập nhật `assets/prompts/writer.md`**
Bổ sung hướng dẫn đọc cấu hình `dialogue_style`, tra cứu ma trận xưng hô trước khi viết cảnh, và miêu tả hành động/nội tâm tự nhiên.

- [ ] **Step 3: Cập nhật `assets/testdata/writer-golden.md` để đồng bộ snapshot**
Thay thế nội dung golden file bằng kết quả ghép nối của `writer.md` (thay placeholder `{{VOICE}}` bằng nội dung `voice.md`) để test `TestBuildWriterPrompt_ByteIdenticalToPreSplit` pass.

- [ ] **Step 4: Chạy test kiểm tra template injection và golden snapshot**
Run: `go test -v ./assets -run TestBuildWriterPrompt`
Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add assets/voice.md assets/prompts/writer.md assets/testdata/writer-golden.md
git commit -m "feat(prompts): nâng cấp voice và writer prompt theo tiêu chuẩn văn học Việt"
```

---

### Task 5: Nâng cấp Đọc kiểm Editor & Trọng tài Arbiter (`assets/prompts/editor.md` & `assets/prompts/arbiter-*.md`)

**Files:**
- Modify: `assets/prompts/editor.md`
- Modify: `assets/prompts/arbiter-intervention.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Quy trình đọc kiểm và phân xử hiện có
- Produces: Tiêu chí kiểm toán Zero-Convert Gate, Pronoun Audit, Typography Consistency trong Editor; nguyên tắc phân xử tiếng Việt trong Arbiter.

- [ ] **Step 1: Cập nhật `assets/prompts/editor.md`**
Bổ sung các hạng mục kiểm tra: sáo ngữ convert (trích câu gốc yêu cầu sửa), sai lệch xưng hô so với ma trận, và sự nhất quán của dấu câu hội thoại.

- [ ] **Step 2: Cập nhật `assets/prompts/arbiter-intervention.md`**
Bổ sung chuẩn tiếng Việt tự nhiên và ma trận xưng hô làm căn cứ phân xử tối cao.

- [ ] **Step 3: Chạy test kiểm tra prompts**
Run: `go test -v ./assets -run TestLoadPrompts`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add assets/prompts/editor.md assets/prompts/arbiter-intervention.md
git commit -m "feat(prompts): tích hợp bộ lọc zero-convert và kiểm toán xưng hô vào editor và arbiter"
```

---

### Task 6: Nâng cấp Thiết kế Cốt truyện Architect (`assets/prompts/architect-long.md` & `architect-short.md`)

**Files:**
- Modify: `assets/prompts/architect-long.md`
- Modify: `assets/prompts/architect-short.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Quy trình kiến tạo đại cương và nhân vật hiện có
- Produces: Khảo sát bối cảnh văn hóa khi khởi tạo, tự động sinh ma trận xưng hô trong `characters.md`, định danh chuẩn theo cẩm nang.

- [ ] **Step 1: Cập nhật `assets/prompts/architect-long.md` và `architect-short.md`**
Bổ sung bước xác định bối cảnh văn hóa, yêu cầu sinh ma trận xưng hô cho nhân vật nòng cốt, và áp dụng quy tắc đặt tên.

- [ ] **Step 2: Chạy test kiểm tra prompts**
Run: `go test -v ./assets -run TestLoadPrompts`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add assets/prompts/architect-long.md assets/prompts/architect-short.md
git commit -m "feat(prompts): nâng cấp architect prompt hỗ trợ khảo sát văn hóa và ma trận xưng hô"
```

---

### Task 7: Bổ sung Style Presets Mới & Cập nhật Style Hiện có (`assets/styles/`)

**Files:**
- Create: `assets/styles/urban_vietnam.md`
- Create: `assets/styles/oriental_cultivation.md`
- Modify: `assets/styles/default.md`
- Modify: `assets/styles/fantasy.md`
- Modify: `assets/styles/romance.md`
- Modify: `assets/styles/suspense.md`
- Test: `assets/load_test.go`

**Interfaces:**
- Consumes: Hệ thống nạp styles của `assets/load.go`
- Produces: 2 preset phong cách mới (`urban_vietnam`, `oriental_cultivation`) và các preset hiện có được chuẩn hóa văn phong tiếng Việt.

- [ ] **Step 1: Tạo `assets/styles/urban_vietnam.md` và `assets/styles/oriental_cultivation.md`**
Xây dựng phong cách Đô thị thuần Việt (ngôn ngữ đời sống, gạch đầu dòng `- `) và Cổ phong mượt mà (Hán Việt trang nhã, sạch convert, ngoặc kép `" "`).

- [ ] **Step 2: Cập nhật `default.md`, `fantasy.md`, `romance.md`, `suspense.md`**
Đưa các chỉ dẫn nhịp câu tiếng Việt vào các style hiện có.

- [ ] **Step 3: Cập nhật `assets/load.go` và `assets/load_test.go` (nếu có enum style hoặc test kiểm tra danh sách styles)**
Kiểm tra xem `assets/load.go` có giới hạn cứng danh sách styles không. Đảm bảo test nạp styles pass.
Run: `go test -v ./assets -run TestLoadStyles`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add assets/styles/ assets/load.go assets/load_test.go
git commit -m "feat(styles): thêm style urban_vietnam, oriental_cultivation và tinh chỉnh các styles hiện hữu"
```

---

### Task 8: Thiết lập Cấu hình mẫu & Structured Rule Engine (`config.example.jsonc`)

**Files:**
- Modify: `config.example.jsonc`
- Test: `internal/bootstrap/configfile_test.go`

**Interfaces:**
- Consumes: Cấu hình `config.example.jsonc`
- Produces: Cấu hình mẫu tích hợp sẵn `forbidden_phrases` chống sáo ngữ convert, `fatigue_words` tần suất cao, và tùy chọn `dialogue_style`.

- [ ] **Step 1: Cập nhật `config.example.jsonc`**
Thêm phần mẫu `user_rules.structured` và chú thích hướng dẫn cấu hình chi tiết cho tiếng Việt.

- [ ] **Step 2: Chạy test kiểm tra config**
Run: `go test -v ./internal/bootstrap -run TestConfig`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add config.example.jsonc
git commit -m "feat(config): bổ sung mẫu forbidden_phrases và fatigue_words tiếng Việt vào config.example.jsonc"
```

---

### Task 9: Kiểm thử Toàn diện, Quét CJK Residual & Nghiệm thu

**Files:**
- Test: Toàn bộ test suite trong repo

- [ ] **Step 1: Chạy toàn bộ test suite Go**
Run: `go test ./...`
Expected: Toàn bộ pass không có lỗi.

- [ ] **Step 2: Quét dư lượng CJK trong các file assets mới chỉnh sửa**
Run: `grep -P '[\x{4e00}-\x{9fff}]' assets/references/anti-ai-tone.md assets/references/dialogue-writing.md assets/references/character-building.md assets/voice.md assets/styles/urban_vietnam.md assets/styles/oriental_cultivation.md || echo "No CJK found"`
Expected: Không có ký tự CJK nào sót lại.

- [ ] **Step 3: Kiểm tra trạng thái git branch**
Run: `git branch --show-current && git status`
Expected: Đang ở nhánh `feat/deep-vi-localization` và working tree sạch.

- [ ] **Step 4: Commit tổng kết nếu có chỉnh sửa phụ**
```bash
git status
```
