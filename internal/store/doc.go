// Package store cung cấp lưu trữ bền bỉ dựa trên hệ thống tệp.
//
// Kiến trúc: 1 cơ sở IO + nhiều sub-store + 1 root kết hợp.
// Mỗi sub-store giữ một phiên bản IO độc lập và sync.RWMutex độc lập.
// Việc đọc ghi của các miền chính (Progress, Outline, Drafts, Summaries...) không chặn lẫn nhau;
// WorldStore kết hợp nhiều miền nhỏ tần suất thấp dùng chung một khóa.
//
// Root kết hợp Store giữ tham chiếu của tất cả các sub-store, và điều phối nối tiếp các hoạt động chéo miền
// (ExpandArc, AppendVolume, ClearHandledSteer); nhiều tệp không tạo thành cam kết nguyên tử của giao dịch,
// Lời gọi dựa vào thứ tự ghi an toàn, lỗi rõ ràng và phục hồi phát lại lũy đẳng với cùng tham số.
//
// Phân chia sub-store:
//   - ProgressStore: Trạng thái chính của tiến độ (meta/progress.json)
//   - OutlineStore: Tiền đề, đại cương (phẳng/phân tầng), la bàn
//   - DraftStore: Cấu tứ chương, bản thảo, bản cuối
//   - SummaryStore: Tóm tắt chương/arc/tập
//   - RunMetaStore: Siêu dữ liệu chạy (model, lịch sử can thiệp)
//   - SignalStore: Tệp tín hiệu một lần (phục hồi PendingCommit)
//   - CheckpointStore: Checkpoint cấp độ step (meta/checkpoints.jsonl)
//   - RuntimeStore: Hàng đợi sự kiện thời gian chạy (meta/runtime/*.jsonl)
//   - CharacterStore: Hồ sơ nhân vật, ảnh chụp trạng thái
//   - WorldStore: Dòng thời gian, chi tiết gieo mầm, quan hệ, thay đổi trạng thái, quy tắc thế giới, quy tắc phong cách, đọc kiểm
package store
