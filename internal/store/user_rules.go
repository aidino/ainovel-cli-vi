package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/rules"
)

// UserRulesStore quản lý ảnh chụp quy tắc người dùng sau khi chuẩn hóa của sách này (meta/user_rules.json).
//
// Nguồn dữ kiện duy nhất trong thời gian chạy: tiêm novel_context và kiểm tra commit_chapter đều chỉ đọc bản này,
// không đọc lại tệp rules nhiều lần (tránh trôi dạt và phân kỳ người đọc kép). Ảnh chụp được tạo chuẩn hóa khi mở sách/nhập/làm mới.
type UserRulesStore struct{ io *IO }

func NewUserRulesStore(io *IO) *UserRulesStore { return &UserRulesStore{io: io} }

// Load đọc meta/user_rules.json. Trả về nil khi không tồn tại (bên gọi dựa vào đó tạo lười).
func (s *UserRulesStore) Load() (*rules.Snapshot, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	var snap rules.Snapshot
	if err := s.io.ReadJSONUnlocked("meta/user_rules.json", &snap); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &snap, nil
}

// Save lưu ảnh chụp.
func (s *UserRulesStore) Save(snap *rules.Snapshot) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.io.WriteJSONUnlocked("meta/user_rules.json", snap)
}
