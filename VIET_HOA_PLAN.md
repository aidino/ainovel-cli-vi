# Kế hoạch Việt hóa ainovel-cli-vi

> Mục tiêu: dịch toàn bộ source code sang tiếng Việt — code, prompt, tài liệu — để engine sinh tiểu thuyết **tiếng Việt** từ đầu đến cuối, đồng thời giữ nguyên toàn bộ các định dạng dữ liệu trên đĩa để không phá vỡ store/checkpoint/compat.

## 1. Hiện trạng (đã khảo sát ngày 2025)

Project Go (bubbletea TUI + litellm/agentcore), fork từ `voocel/ainovel-cli` gốc tiếng Trung. **Chưa có bất kỳ text tiếng Việt nào** — Việt hóa chưa bắt đầu.

Tổng kê chữ Hán (CJK) còn lại:

| Hạng mục | Quy mô | Ghi chú |
|---|---|---|
| Go source (299/316 file) | ~10.056 dòng CJK | host 3.336 · tools 1.575 · entry(TUI) 1.236 · store 805 · còn lại rải rác |
| — trong đó string literal | ~5.400 dòng | UI, lỗi, log, mô tả schema gửi LLM |
| — trong đó test (106 file) | ~3.778 dòng | phải dịch đồng bộ với literal |
| — còn lại là comment | ~1.000+ dòng | không ảnh hưởng runtime |
| assets/*.md (prompt, reference, style, voice) | ~2.059 dòng | prompts ~500 · references ~1.400 · styles ~33 |
| docs/ + README.md (14 file) | ~3.000 dòng | |
| config.example.jsonc | 52 dòng comment | |
| evals/cases (3 JSON) | nhỏ | prompt + expectation tiếng Trung |
| scripts/check_chapter_wordcount.py | nhỏ | **đếm ký tự Hán — không dùng được cho tiếng Việt** |

Điểm tốt: gần như **không có logic rẽ nhánh theo chuỗi Hán** (chỉ 1 chỗ: `internal/entry/tui/panels_sidebar.go` so sánh `detail == "待命"`) → rủi ro parser thấp, dịch chuỗi hiển thị an toàn.

## 2. Nguyên tắc vàng (chính sách dịch)

### 2.1. KHÔNG dịch (giữ nguyên 100%)
- Identifier: tên biến, hàm, struct, package, file
- JSON key lưu trên đĩa (store schema), giá trị enum được persist
- Tên tool LLM: `novel_context`, `draft_chapter`, `commit_chapter`… và tên field trong contract
- Tên checkpoint (`chapter:1:plan`…), CLI flags, đường dẫn file
- **Lý do:** không phá format dữ liệu cũ, không phá contract LLM-tool, test vẫn khớp.

### 2.2. DỊCH
- Comment + doc comment trong Go
- String literal: TUI, lỗi, log, message sự kiện, mô tả schema JSON gửi LLM
- Toàn bộ assets (prompt, style, reference, voice) — **dịch tái diễn đạt, không dịch máy**; thêm chỉ thị rõ "viết正文 tiếng Việt"
- Docs, README, config comment, Dockerfile, install.sh, eval cases, script Python

### 2.3. Bảng thuật ngữ chuẩn (làm ở GĐ0, bắt buộc dùng xuyên suốt)
Khởi tạo: 章=chương · 卷=tập · 弧=arc · 伏笔=chi tiết gieo mầm · 钩子=móc · 大纲=đại cương · 草稿=bản thảo · 审阅=đọc kiểm · 裁定=phán quyết · checkpoint=điểm khôi phục … → lưu `docs/vi-glossary.md`.

### 2.4. Quy tắc đồng bộ chuỗi
Chuỗi A được so sánh/regex/đối chiếu bởi chuỗi B (kể cả trong test) → **dịch trong cùng một commit**. Rà bằng grep trước khi dịch mỗi gói.

## 3. Các giai đoạn

### GĐ0 — Chính sách & công cụ (nhỏ)
- Tạo `docs/vi-glossary.md` (bảng thuật ngữ) + checklist "cấm dịch".
- Viết script kiểm tra phần dư: `scripts/check_no_cjk.sh` (grep CJK, cho phép loại trừ testdata cố ý giữ tiếng Trung — phục vụ import tiểu thuyết gốc).
- Tạo nhánh `upstream-zh` giữ bản gốc làm tham chiếu (sau khi dịch toàn bộ thì không còn merge upstream được nữa — đây là hard fork).

### GĐ1 — Assets LLM: prompt + style + reference + voice ⭐ (quan trọng nhất)
Đây là phần quyết định **ngôn ngữ và chất lượng tiểu thuyết sinh ra**. Thứ tự: `prompts/writer.md` → `editor.md` → `architect-*.md` → `arbiter-*.md` → `import-*.md` → `revision-analyze.md` → `simulation-*.md` → `styles/` → `references/` (21 file) → `voice.md`.
- Giữ nguyên tên tool, tên field JSON trong ví dụ; dịch phần diễn giải.
- `import-*.md`: prompt tiếng Việt nhưng phải xử lý văn bản nguồn **bất kỳ ngôn ngữ nào** (giữ khả năng nhập tiểu thuyết tiếng Trung/Anh).
- Nghiệm thu: `go build ./…` + chạy `evals/cases/smoke` (nếu có API key), hoặc tối thiểu unit test load assets.

### GĐ2 — Thống kê tiếng Việt (sửa logic, không chỉ dịch chữ)
- Thay đếm ký tự Hán bằng **đếm từ** (uax29 / `golang.org/x/text` đã có trong deps) cho `ChapterWordCounts`, `TotalWords`, diag `WordCountAnomaly`, gate số chữ tối thiểu.
- Quy đổi ngưỡng: 3.000 chữ Hán ≈ **1.800–2.200 từ Việt** (xác định bằng đo thực tế).
- Viết lại `scripts/check_chapter_wordcount.py` theo đếm từ.
- Dịch template persisted trong `internal/store/book.go` (`《%s》`, `## 简介`) — quyết định format tiếng Việt cho **sách mới** (ví dụ `# %s` + `## Tóm tắt`); ghi chú compat với `migration.go`.

### GĐ3 — Chuỗi Go theo gói (thứ tự rủi ro tăng dần)
`errs` `logger` `version` → `notify` `utils` `models` `userrules` → `rules` `flow` `domain` `chapterfacts` `llmcontract` `arbiter` → `eval` `stylestat` → `revision` `diag` → `bootstrap` → `store` (cẩn thận mục GĐ2) → `tools` (mô tả schema gửi LLM — đồng bộ với GĐ1) → `host` → `entry` (TUI: kiểm tra độ rộng layout, tiếng Việt là single-width nên an toàn) → `cmd`.
- **Sau mỗi gói:** `go build ./… && go vet ./… && go test ./…` phải xanh.
- Test của gói đó dịch **cùng commit** với literal.

### GĐ4 — Comment Go (khối lượng lớn, rủi ro = 0)
299 file, chia batch 20–30 file, chỉ sửa comment — chạy song song được (subagent theo batch), verify bằng build + diff chỉ nằm trong comment.

### GĐ5 — Test dọn đọng
3.778 dòng CJK trong 106 file test chưa kịp đồng bộ ở GĐ3 — dịch nốt, toàn bộ `go test` phải xanh.

### GĐ6 — Tài liệu & ngoại vi
`docs/` (13 file) → `README.md` → `config.example.jsonc` → `assets/README.md` → `Dockerfile`, `docker-compose.yml`, `install.sh`, `.github`.

### GĐ7 — Eval & nghiệm thu cuối
- Dịch `evals/cases/*.json` (prompt yêu cầu viết tiểu thuyết Việt).
- Tạo golden file tiếng Việt mới thay `assets/testdata/writer-golden.md`.
- Chạy end-to-end headless 1 chương với model thật (cần API key) — soi kết quả: đề mục, tóm tắt, chương, thống kê từ.
- Gate cuối: `scripts/check_no_cjk.sh` = 0 lỗi trên phạm vi mục tiêu.

## 4. Chiến lược xác minh tổng thể
1. Mỗi commit: `go build ./…` + `go test ./…` xanh.
2. Gate CJK-dư sau mỗi giai đoạn.
3. Bảng thuật ngữ: review chéo trước khi hoàn thành mỗi giai đoạn.
4. Smoke thực tế cuối GĐ1 và cuối GĐ7.

## 5. Rủi ro & đối sách
| Rủi ro | Đối sách |
|---|---|
| Mất khả năng merge upstream | Chấp nhận hard fork; giữ nhánh `upstream-zh` tham chiếu |
| Prompt dịch máy → văn phong AI dở | GĐ1 do người/dịch giả review, tác bút viết lại tự nhiên |
| Đếm từ sai làm gate chất lượng sai | GĐ2 tách riêng, đo chuẩn bằng corpus đối chiếu |
| Chuỗi so sánh chéo (kiểu "待命") | Quy tắc 2.4 + grep rà trước mỗi gói |
| Format store cũ không đọc được | Không đổi JSON key/enum (mục 2.1); chỉ đổi template văn bản hiển thị cho sách mới |

## 6. Khối lượng ước tính & phân bổ
- Tổng ~13.000 dòng CJK. GĐ1 ~2.000 · GĐ2 ~300 (có logic) · GĐ3+GĐ5 ~9.200 · GĐ4 ~1.000 (song song) · GĐ6 ~3.100.
- GĐ4, GĐ6: delegate batch cho subagent chạy nền. GĐ1–GĐ3: làm trực tiếp, kiểm từng gói.
