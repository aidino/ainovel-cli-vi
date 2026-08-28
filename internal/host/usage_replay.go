package host

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/agentcore"
)

// sessionRecord là dạng phân tích cú pháp nhẹ cho bản ghi đơn lẻ trong meta/sessions/*.jsonl — chỉ lấy các trường cần thiết cho việc tích lũy usage.
// Các trường lớn như Content được bỏ qua để tiết kiệm IO trong giai đoạn khởi động.
//
// Phân cấp dự phòng cho mô hình:
//  1. Usage.Provider/Model — Mô hình phản hồi thực tế được truyền qua từ agentcore/litellm (ưu tiên).
//  2. Meta(_meta)          — Mô hình "có hiệu lực tại thời điểm đó" do ModelLookup bổ sung khi ghi vào nếu phía thượng nguồn không truyền qua.
//  3. Không có gì cả        — Khi replay, quay về effectiveModel, dùng ModelSet hiện tại để suy ngược lại (độ chính xác bị giảm).
type sessionRecord struct {
	Role  agentcore.Role     `json:"role"`
	Usage *agentcore.Usage   `json:"usage,omitempty"`
	Meta  *sessionRecordMeta `json:"_meta,omitempty"`
}

type sessionRecordMeta struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// UsageReplay khôi phục lượng dùng của phiên (Token và chi phí) từ bản ghi phiên JSONL đã được lưu trữ bền vững.
// Do cấu hình model có thể bị cập nhật nóng (ví dụ đổi sang model đắt hơn), để đảm bảo sau khi khôi phục từ điểm dừng
// "chi phí đã dùng" hiển thị trên giao diện khớp với hóa đơn, bắt buộc phải áp dụng nguyên tắc "ghi lại chi phí lúc đó, không tính lại theo giá hiện tại".
//
// Ràng buộc gọi: Chỉ gọi một lần để khôi phục khi tệp meta/usage.json bị thiếu.
// Việc duy trì dữ liệu hàng ngày được quản lý độc lập với việc ghi log, chúng ta chỉ cần phát lại cộng dồn, cuối cùng SaveNow đè lên state.json của store.
// Chỉ khi thực sự xảy ra quá trình nạp lại (đọc được bản ghi token hợp lệ từ phiên) mới ghi đĩa, tránh trường hợp chạy mới hoàn toàn
// và phiên rỗng thì lại cưỡng chế đè số 0 (mặc dù không sao nhưng tiết kiệm một lần IO).
//
// Độ chính xác phụ thuộc vào phân cấp trong chú thích sessionRecord—cấp 3 (thiếu cả Usage và _meta)
// sẽ chỉ kích hoạt trong các nhật ký cũ hơn hoặc khi thượng nguồn xảy ra lỗi.
func (t *UsageTracker) ReplaySessions(rootDir string) (int, error) {
	if t == nil {
		return 0, nil
	}
	sessionsDir := filepath.Join(rootDir, "meta", "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	total := 0
	agentsDir := filepath.Join(sessionsDir, "agents")
	walkErr := filepath.WalkDir(agentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		agentName := parseAgentNameFromFile(name)
		if agentName == "" {
			return nil
		}
		n, fileErr := t.replayFile(path, agentName)
		if fileErr != nil {
			slog.Warn("replay agent session failed", "module", "usage", "file", name, "err", fileErr)
			return nil
		}
		total += n
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return total, walkErr
	}
	return total, nil
}

// replayFile quét một tệp jsonl duy nhất, đưa tất cả các tin nhắn assistant có chứa Usage vào accumulate.
// agentName được bên gọi phân tích cú pháp từ tên tệp phiên Worker.
func (t *UsageTracker) replayFile(path, agentName string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	role := agentRoleName(agentName)
	count := 0
	scanner := bufio.NewScanner(f)
	// Một dòng có thể rất dài (tin nhắn assistant + tool args v.v. đều được làm phẳng), nới lỏng lên 4MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Role != agentcore.RoleAssistant || rec.Usage == nil {
			continue
		}
		provider, modelName := usageActualModel(rec.Usage)
		if rec.Meta != nil {
			if provider == "" {
				provider = rec.Meta.Provider
			}
			if modelName == "" {
				modelName = rec.Meta.Model
			}
		}
		t.accumulate(role, provider, modelName, *rec.Usage)
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan %s: %w", path, err)
	}
	return count, nil
}

// parseAgentNameFromFile trích xuất tên agent từ "writer-ch01.jsonl" / "architect_short-001.jsonl"
// (phần trước dấu "-"). Quy ước đặt tên xem tại store/session.go::subAgentPath:
// agentName không chứa dấu gạch ngang, hậu tố là ch<n> hoặc số thứ tự tăng dần.
func parseAgentNameFromFile(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	if i := strings.Index(base, "-"); i > 0 {
		return base[:i]
	}
	return ""
}
