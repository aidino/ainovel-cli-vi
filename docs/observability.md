# Sổ tay Quan trắc

Khi chạy tiểu thuyết dài kỳ (trường thiên), làm thế nào để biết các cơ chế khác nhau có thực sự đang hoạt động hay không?

Tài liệu này không phải là việc sao chép y nguyên các quy tắc diag, mà là hướng đến việc **vận hành thực tế**: Bạn chạy đến chương thứ N, bạn nên mở tệp nào, xem trường (field) nào, phán đoán xem nó đang khỏe mạnh hay bất thường.

---

## 1. Quy trình khắc phục sự cố chung

```
1. /diag                       # Chẩn đoán tự động, xem phần Findings
2. cd output/{novel}/meta/     # Dùng lệnh cat xem trực tiếp các tạo tác (artifacts) quan trọng
3. tail decisions.jsonl                # Xem các phán quyết gần đây của Arbiter
4. ls -lt sessions/agents/             # Định vị phiên Worker gần nhất rồi dùng tail
```

Những sự thật mà `/diag` không bao phủ được (bao gồm các mục "chẩn đoán cần bổ sung" được liệt kê trong tài liệu này) cần được kiểm tra thủ công thông qua bước 2-4.

### Báo cáo issue: Xuất chẩn đoán đã khử nhạy (Desensitized)

Mỗi lần `/diag` sẽ ghi thêm ra một tệp `output/{novel}/meta/diag-export.md` —— một bản chẩn đoán **đã được khử nhạy** (chính văn tiểu thuyết / prompt / suy nghĩ đã bị xóa bỏ, chỉ giữ lại bộ khung hành vi: tên công cụ, chuỗi lỗi, số lần lặp lại, phase/flow, bước bị kẹt, phân loại lỗi log). Khi gặp vấn đề vòng lặp vô hạn / gián đoạn, chỉ cần dán tệp này lên GitHub issue, người bảo trì sẽ dựa vào đó để định vị vấn đề mà không cần dữ liệu `output/` của người dùng.

---

## 2. Bảng tra cứu nhanh các tạo tác quan trọng

Sắp xếp theo "đường dẫn khắc phục sự cố phổ biến nhất khi xảy ra sự cố":

| Tạo tác | Đường dẫn | Xem gì | Khỏe mạnh | Không khỏe mạnh |
|---|---|---|---|---|
| Tiến độ | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | phase tiến lên đơn điệu, flow nằm trong tập hợp hợp lệ | phase thụt lùi / flow bị kẹt ở trạng thái nào đó |
| La bàn | `meta/compass.json` | Khoảng cách giữa `last_updated` và chương mới nhất | gap < 15 chương | gap > 15 chương (Hit CompassDrift) |
| Danh sách nhân vật phụ | `meta/cast_ledger.json` | Số lượng mục / tỷ lệ điền `brief_role` / tính nhất quán của tên | Xem §4 | Xem §4 |
| Sổ cái chi tiết gieo mầm | `meta/foreshadow.json` | Số chương đình trệ dài nhất của `status="planted"` | < Số chương/3 | > Số chương/3 (Hit StaleForeshadow) |
| Đại cương | `meta/layered_outline.json` | Số chương chưa viết còn lại của tập hiện tại | Đã được triển khai trước 1-2 chương | Viết đến chương hiện tại nhưng chương tiếp theo không có outline (OutlineExhausted) |
| Hồ sơ nhân vật | `meta/characters.json` | Có thể tìm thấy các nhân vật core/important trong tóm tắt N chương gần đây không | Đều có thể tìm thấy | Vắng mặt (Hit GhostCharacter) |
| Điểm khôi phục | `meta/checkpoints.jsonl` | `step` của dòng gần nhất có tương ứng với tiến độ không | Nhất quán | Không nhất quán (Phục hồi sau sự cố chưa tự chữa lành) |
| Kiểm toán phán quyết | `meta/decisions.jsonl` | facts/decision của một số phán quyết gần đây | Phân loại chẩn đoán chính xác, hành động hợp lý | Can thiệp cùng loại bị phán quyết thất bại nhiều lần |

---

## 3. Quan trắc La bàn (compass)

**Thời gian sửa lỗi**: 2026-05-08 (commit `fix: công cụ update_compass tự động điền last_updated`)

### Xem gì

```bash
cat output/{novel}/meta/compass.json
```

Ngữ nghĩa các trường:
- `ending_direction`: Hướng đi kết cục (nên nhất quán với đoạn "hướng đi kết cục" trong `premise.md`)
- `open_threads`: Các tuyến truyện dài hạn đang hoạt động (được architect thêm/bớt ở ranh giới của mỗi tập)
- `estimated_scale`: Quy mô ước tính (ví dụ "4-6 tập", cập nhật ở ranh giới mỗi tập)
- `last_updated`: **Công cụ tự động điền** là số chương đã hoàn thành lớn nhất tại thời điểm cập nhật (không còn phụ thuộc vào LLM tự điền)

### Đánh giá độ khỏe mạnh

| Tín hiệu | Đánh giá |
|---|---|
| `last_updated` nằm trong phạm vi `[latest-15, latest]` | Khỏe mạnh |
| `last_updated` tụt hậu so với latest hơn 15 chương | architect không cập nhật ở ranh giới arc/tập —— kiểm tra prompt của architect-long.md |
| `last_updated == 0` | **Dữ liệu bẩn trước lần sửa lỗi này**, lần update_compass tiếp theo sẽ tự chữa lành |
| `ending_direction` và đoạn "hướng đi kết cục" trong premise.md không khớp | architect đã lén lút thay đổi ý định của người dùng —— ghi nhận lại, quyết định xem có nên đóng băng trường này không (vấn đề thiết kế, xem todo.md) |

### Làm thế nào để xác minh bản sửa lỗi có hiệu quả

So sánh trước và sau khi chạy truyện dài:
- **Trước khi sửa**: Sau khi chạy 30+ chương, `compass.last_updated` rất có thể là `0` hoặc số chương ở giai đoạn đầu nào đó
- **Sau khi sửa**: Mỗi lần architect gọi `update_compass`, `last_updated` đều bị tầng công cụ ghi đè bằng latest hiện tại

---

## 4. Quan trắc Danh sách nhân vật phụ (cast_ledger)

**Chức năng đã được áp dụng**: 2026-05-08 (commit `feat: Bổ sung danh sách nhân vật phụ tự động theo dõi nhân vật phụ`)

### Xem gì

```bash
cat output/{novel}/meta/cast_ledger.json | jq 'length'                     # Tổng số mục
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.brief_role == "" or .brief_role == null)] | length'  # Số lượng thiếu brief_role
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.appearance_count >= 3)] | length'   # Tần suất xuất hiện nhiều (≥3 lần)
cat output/{novel}/meta/cast_ledger.json | jq 'sort_by(-.appearance_count) | .[:10]'  # 10 người xuất hiện nhiều nhất
```

### Đánh giá độ khỏe mạnh

| Khía cạnh | Khỏe mạnh | Bất thường | Cách xử lý |
|---|---|---|---|
| **Số lượng mục vs Số chương đã hoàn thành** | Số mục ledger ≈ Số chương đã hoàn thành × 0.3-0.6 | > Số chương × 0.8 (Nhân vật quần chúng bị đưa nhầm vào danh sách) | Kiểm tra xem đoạn `cast_intros` trong writer.md có đủ rõ ràng không |
| **Tỷ lệ điền brief_role** | Thiếu sót < 30% | Thiếu sót > 50% | Writer bỏ sót nghiêm trọng —— prompt hướng dẫn không đủ |
| **Độ tương đồng tên gọi** | Không bị tình trạng một nhân vật nghi ngờ có nhiều tên | Đồng thời xuất hiện "Lý X" / "Lão Lý" / "X chưởng quỹ" | LLM bị trôi dạt tên —— thêm ràng buộc "Sử dụng tên nhất quán" vào prompt hoặc thêm công cụ hợp nhất steer của người dùng |
| **Nhân vật xuất hiện thường xuyên** | Các mục có `appearance_count >= 5` rất hiếm | Một lượng lớn mục xuất hiện với tần suất cao xuyên suốt các arc | Nên cân nhắc thăng cấp vào hồ sơ cốt lõi (Giai đoạn 3 Kênh thăng cấp) |
| **Việc thu hồi có được tiêu thụ (sử dụng) không** | Khi Writer viết về nhân vật cũ, trong trường characters của commit_chapter có chứa tên đã có trong ledger | Writer sáng tạo lại cùng một cái tên (Xuất hiện "Lão Châu A" và "Lão Châu B") | Việc thu hồi (recall) recent_cast chưa được tiêu thụ —— kiểm tra đoạn "Tính liên tục của nhân vật phụ" trong writer.md |

### Xác minh luồng dữ liệu (Đầu cuối - End-to-end)

Sau khi chạy 5 chương:
1. `cat meta/cast_ledger.json` không nên rỗng (trừ phi mỗi chương đều chỉ sử dụng các nhân vật cốt lõi)
2. Nếu Writer giới thiệu "Lão Châu" ở chương 1:
   - Trong `cast_ledger` nên có mục `Lão Châu`, `appearance_count=1`
3. Nếu chương 5 lại viết về Lão Châu:
   - `Lão Châu.appearance_count=2`, `last_seen_chapter=5`
4. Giá trị trả về của công cụ novel_context cho chương 5 trong `meta/sessions/agents/writer-*.jsonl`, bạn nên nhìn thấy Lão Châu trong `episodic_memory.recent_cast`
5. Nếu ở bước trước bạn đã thấy nhưng Writer không tiêu thụ (viết ra Lão Châu không khớp với chương 1) —— Đây là vấn đề của prompt

### Hiện tại chưa có chẩn đoán tự động (nhưng snapshot đã được load)

`diag.Snapshot.CastLedger` đã được đọc bên trong `Load()` và có thể được tiêu thụ trực tiếp bởi các quy tắc —— nhưng hiện tại chưa viết bất kỳ quy tắc nào. Việc xác minh vẫn dựa vào các lệnh `jq` ở trên để kiểm tra thủ công.

Nếu muốn bổ sung các quy tắc chẩn đoán sau này (ứng cử viên):
- `CastBriefRoleMissing`: Cảnh báo khi tỷ lệ thiếu > 50%
- `CastBloat`: Cảnh báo khi số mục > số chương × 0.8
- `CastPromotionCandidate`: `appearance_count` ≥ 5 và xuyên arc → Đề xuất thăng cấp

Đừng quyết định các ngưỡng ngay lúc này —— đợi dữ liệu chạy trường thiên ra mắt, xem xét sự phân bổ thực tế rồi mới quyết định. Bản thân mã quy tắc chỉ cần 30-50 dòng.

---

## 5. Writer có làm việc như mong đợi hay không

Điều được quan tâm nhất khi chạy trường thiên là **Writer có thực sự hành xử theo prompt không**. Việc quan trắc trực tiếp nhất là log của phiên làm việc (session log):

```bash
ls output/{novel}/meta/sessions/agents/    # Mỗi subagent một bản jsonl
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

Xem một số hành vi cụ thể:

| Hành vi mong muốn | Thể hiện trong jsonl |
|---|---|
| Writer đã xem recent_cast | Trường `episodic_memory.recent_cast` trong giá trị trả về của công cụ novel_context khác rỗng |
| Writer đã điền cast_intros trong commit_chapter | Mảng `cast_intros` trong tham số tool_call khác rỗng (Chỉ trong các chương giới thiệu nhân vật mới) |
| Writer đã sử dụng các gợi ý của chương liên quan | Số lần gọi `read_chapter` > 1 (Mặc định là 1 lần, vượt quá nghĩa là đã tra cứu lại) |
| Writer không vi phạm thứ tự công cụ | Trình tự tool_call tuân thủ nghiêm ngặt: `novel_context → read_chapter → plan_chapter → draft_chapter → check_consistency → commit_chapter` |

Nếu trong jsonl bạn thấy Writer gọi "khống" (gọi nhưng không làm gì) novel_context nhiều lần, hoặc sau commit_chapter lại gọi các công cụ khác —— đó là do prompt không kiểm soát được.

---

## 6. Các "Ranh giới đỏ" trong kịch bản chạy trường thiên (long-running)

Khi chạy một bộ trường thiên hơn 100+ chương, nếu trúng phải bất kỳ điều kiện nào dưới đây thì bạn nên dừng lại để kiểm tra:

- [ ] Hit CompassDrift và kéo dài qua 2 arc mà không biến mất
- [ ] Số mục trong cast_ledger > số chương đã hoàn thành × 0.8
- [ ] Tỷ lệ điền brief_role trong cast_ledger < 30%
- [ ] Cùng một nhân vật xuất hiện sự nghi ngờ có nhiều tên ("Lão Lý" / "Lý chưởng quỹ" cùng tồn tại)
- [ ] Khi viết chương mới, Writer không đọc các nhân vật cũ đã có trong recent_cast (sáng tạo lại / tái phát minh)
- [ ] Trong Worker session xuất hiện ≥ 5 lần gọi khống novel_context liên tục
- [ ] Sau khi commit bất kỳ chương nào, `meta/checkpoints.jsonl` không có step `commit_chapter` tương ứng

4 điều kiện đầu tiên phản ánh độ khỏe mạnh của cơ chế mới lần này; 3 điều kiện sau phản ánh độ ổn định của cơ chế đã có.

---

## 7. Quy chuẩn bảo trì tài liệu

**Khi thêm một tạo tác tầng sự thật mới (Tạo mới một tệp `meta/*.json` / `meta/*.jsonl`), hãy đồng bộ hóa:**

1. Thêm một hàng tra cứu nhanh vào §2 của tài liệu này
2. Nếu tạo tác cần quan trắc chuyên biệt (không chỉ đơn giản là đánh giá "tồn tại/không tồn tại"), hãy thêm một đoạn chuyên đề §X
3. Nếu muốn chẩn đoán tự động, hãy tải nó trong `internal/diag/snapshot.go::Load` và thêm quy tắc vào `internal/diag/rules_*.go`

**Không nên:**
- Không sao chép toàn bộ quy tắc trong `internal/diag/` vào tài liệu này (đó là tham chiếu quy tắc, không phải là sổ tay quan trắc)
- Không viết quy tắc chẩn đoán cho mọi cơ chế —— việc áp đặt ngưỡng tùy tiện sẽ sai lầm, hãy quan sát trước rồi bổ sung sau
