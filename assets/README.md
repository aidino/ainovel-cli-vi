# Bản đồ nội dung assets

Trước khi thêm "một đoạn văn / một tư liệu / một quy tắc" vào hệ thống, hãy tra bảng dưới đây xác định chỗ đặt, rồi xem cách nối dây.

| Thư mục | Chứa gì | Ai tiêu thụ | Cách nối dây |
|---|---|---|---|
| `prompts/` | System prompt của Worker (writer / editor / architect×2), prompt phán quyết của Arbiter và prompt nhiệm vụ một lần (import / simulation / revision) | `agents/build.go`, `internal/arbiter`, runner imp / sim / revision | Trường Prompts của `load.go`. Lưu ý: simulation_guidance được `load.go` bơm vào lúc tải, trong file md không thấy |
| `references/` | Tư liệu kiến thức viết không phụ thuộc thể loại. Không vào system prompt, do novel_context cắt theo vai trò / chương rồi bơm vào `reference_pack` | writer / editor / architect | **Nối ba chỗ**: thêm trường vào `tools.References` + loadReferences của `load.go` đọc + `novel_context.go` bơm qua writerReferences / architectReferences. Bỏ vào thư mục sẽ không tự động được tải |
| `references/genres/<style>/` | Kiến thức riêng thể loại (style-references / arc-templates) | Như trên, tải khi `style != default` | loadReferences của `load.go` |
| `rules/` | Thư mục quy tắc nội bộ cũ đã bỏ; cơ sở máy móc đã dời vào code, quy tắc người dùng đến từ ảnh chụp ngôn ngữ tự nhiên của `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` | `userrules.Service` chuẩn hóa thành `meta/user_rules.json`; `novel_context` bơm vào; `commit_chapter` kiểm tra | Cơ sở nội bộ xem `SystemDefaults()` trong `internal/rules/snapshot.go`; file .md của người dùng không định dạng, không YAML, chuẩn hóa theo ngôn ngữ tự nhiên |
| `styles/<style>.md` | Chỉ thị phong cách viết theo thể loại | Ghép vào system prompt của **writer** (`agents/build.go`) | Tên file chính là giá trị của `config.style`. Cùng một khái niệm thể loại với `references/genres/<style>/` nhưng hai loại tải khác nhau: cái trước là chỉ thị phong cách, cái sau là tư liệu kiến thức |

## Phán đoán chỗ đặt nội dung mới (năm câu hỏi)

1. Luồng này bắt buộc phải được **bảo đảm**? → Không viết prompt, viết ràng buộc bằng code (StopAfterTools / guard tool / Flow Router)
2. Đây là phán cứ phán quyết? → Luồng kiểu tra bảng viết vào `internal/flow/router.go`; phán đoán ngữ nghĩa viết vào `prompts/arbiter-*.md`
3. Đây là chuẩn thẩm mỹ / thực thi của một vai trò? → `prompts/<role>.md`
4. Đây là quy tắc mặc định liệt kê được bằng máy (từ cấm / ngưỡng)? → `SystemDefaults()` trong `internal/rules/snapshot.go`; quy tắc tùy chỉnh của người dùng viết vào `.ainovel/rules/*.md`, do ảnh chụp chuẩn hóa tiêu thụ (số từ / độ dài là ràng buộc mềm ngữ nghĩa, đi preferences, không làm quy tắc máy móc)
5. Đây là tư liệu kiến thức viết? → `references/` (nhớ nối ba chỗ)

## Bảo đảm tính nhất quán

Đường dẫn phong bì mà prompt tham chiếu (`working_memory.*` v.v.) phải nhất quán với `novel_context`. Hình dạng tham số tool chỉ định nghĩa trong Schema của tool; prompt chỉ bổ sung ngữ nghĩa nghiệp vụ mà Schema không diễn đạt được, không copy lại danh sách tham số JSON và ví dụ hình dạng.

prompt có thể mô tả phương pháp thực thi của một Worker riêng, nhưng định tuyến toàn cục, chuyển dịch trạng thái và logic khôi phục chỉ lấy code làm chuẩn. Các bước có thể xác định từ dữ kiện Store đặt vào Router/Tool; những phán đoán cần hiểu nội dung tiểu thuyết hoặc ý định người dùng mới dành cho mô hình.