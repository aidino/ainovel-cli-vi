package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// UsageStore bền bỉ hóa lượng token / chi phí tích lũy vào meta/usage.json.
// Việc ghi đi qua nguyên tử ghi (tmp + rename) của IO, đường dẫn Save ghi đè toàn bộ trạng thái mỗi lần.
type UsageStore struct{ io *IO }

func NewUsageStore(io *IO) *UsageStore { return &UsageStore{io: io} }

// Load đọc usage.json. Trả về (nil, nil) khi tệp không tồn tại hoặc phiên bản schema không khớp,
// do bên gọi quyết định có thực hiện session replay lấp đầy một lần hay không.
func (s *UsageStore) Load() (*domain.UsageState, error) {
	var state domain.UsageState
	if err := s.io.ReadJSON("meta/usage.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if state.Schema != domain.UsageSchemaVersion {
		return nil, nil
	}
	return &state, nil
}

// Save ghi đè toàn bộ state xuống đĩa. Bên gọi chịu trách nhiệm debounce / tiết lưu.
func (s *UsageStore) Save(state domain.UsageState) error {
	state.Schema = domain.UsageSchemaVersion
	return s.io.WriteJSON("meta/usage.json", state)
}
