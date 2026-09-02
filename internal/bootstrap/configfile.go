package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const configDirName = ".ainovel"

// DefaultConfigPath trả về đường dẫn file cấu hình toàn cục ~/.ainovel/config.json.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName, "config.json")
}

// DefaultConfigDir Trả về đường dẫn thư mục ~/.ainovel; trả về chuỗi rỗng khi không lấy được thư mục home.
// Chỉ dùng để đọc/ghi file không bắt buộc tồn tại (như cache model), sẽ không tự động tạo thư mục.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// configDir Trả về đường dẫn thư mục ~/.ainovel, tạo mới nếu không tồn tại.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// projectConfigPath trả về đường dẫn tương đối của file cấu hình cấp dự án ./.ainovel/config.json.
// dotdir cấp dự án mirror ~/.ainovel/ toàn cục, dùng chung một configDirName; phân giải tương đối so với cwd.
func projectConfigPath() string {
	return filepath.Join(configDirName, "config.json")
}

// EffectiveConfigPath Trả về file cấu hình mà những thay đổi từ TUI (/config, /model) nên ghi lại:
// thư mục dự án có ./.ainovel/config.json thì ghi vào đó——giống với chiều ghi đè toàn cục của tầng dự án khi đọc,
// đảm bảo "sửa bản hiện đang có hiệu lực", sửa xong là tác dụng ngay; nếu không thì ghi vào bản toàn cục ~/.ainovel/config.json.
// Chỉ sửa cấu hình dự án đã tồn tại, không tạo từ hư vô (việc tạo ghi đè dự án là hành động người dùng chủ động đặt file).
func EffectiveConfigPath() string {
	rel := projectConfigPath()
	if _, err := os.Stat(rel); err == nil {
		if abs, err := filepath.Abs(rel); err == nil {
			return abs
		}
		return rel
	}
	return DefaultConfigPath()
}

// LoadConfig tải và gộp cấu hình theo thứ tự ưu tiên:
//  1. ~/.ainovel/config.json (toàn cục)
//  2. ./.ainovel/config.json (ghi đè cấp dự án)
func LoadConfig() (Config, error) {
	var cfg Config

	// 1. Cấu hình toàn cục. Là lớp cơ sở có ưu tiên thấp nhất, file hỏng sẽ hạ cấp thành cảnh báo thay vì chặn——có thể bị dự án ghi đè;
	//    thất bại cứng sẽ chặn người dùng có "file toàn cục hỏng + cấu hình dự án hợp lệ" ở ngoài.
	if p := DefaultConfigPath(); p != "" {
		global, found, err := loadOptionalJSON(p)
		switch {
		case err != nil:
			slog.Warn("phân tích cấu hình toàn cục thất bại, đã bỏ qua (có thể bị cấp dự án ghi đè)", "module", "config", "path", p, "err", err)
		case found:
			cfg = global
		}
	}

	// 2. Ghi đè cấp dự án. Lỗi file fail loud: người dùng chủ động đặt cấu hình ở thư mục hiện tại, nuốt lỗi im lặng sẽ làm cho
	//    "đã cấu hình nhưng không hiệu lực" không biết đâu mà lần (issue #37).
	project, found, err := loadOptionalJSON(projectConfigPath())
	if err != nil {
		return cfg, fmt.Errorf("phân tích cấu hình cấp dự án ./.ainovel/config.json thất bại (vui lòng kiểm tra cú pháp JSON): %w", err)
	}
	if found {
		cfg = mergeConfig(cfg, project)
	}

	return cfg, nil
}

// loadOptionalJSON Đọc một file cấu hình tùy chọn:
//   - file không tồn tại → (zero, false, nil), do caller quyết định dùng giá trị mặc định/lớp trên
//   - file tồn tại nhưng parse thất bại → trả về lỗi (không nuốt lỗi im lặng nữa——nếu không cấu hình của người dùng "đã cấu hình nhưng không hiệu lực"
//     lại không biết đâu mà lần, chính là nguyên nhân gốc rễ của issue #37)
func loadOptionalJSON(path string) (Config, bool, error) {
	cfg, err := loadJSONFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfigFile Đọc một file cấu hình JSON, hỗ trợ // chú thích dòng.
// Không làm thao tác gộp nào cả, chỉ trả về cấu hình của chính file đó. Trả về lỗi khi file không tồn tại.
func LoadConfigFile(path string) (Config, error) {
	return loadJSONFile(path)
}

// loadJSONFile Đọc file cấu hình JSON, hỗ trợ // chú thích dòng.
// Trả về lỗi khi file không tồn tại (do caller quyết định có bỏ qua hay không).
func loadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cleaned := stripJSONComments(data)
	var cfg Config
	if err := json.Unmarshal(cleaned, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// mergeConfig Gộp overlay vào base. Các trường giá trị khác không thì ghi đè, map gộp theo khóa.
func mergeConfig(base, overlay Config) Config {
	if overlay.Provider != "" {
		base.Provider = overlay.Provider
	}
	if overlay.ModelName != "" {
		base.ModelName = overlay.ModelName
	}
	if overlay.ReasoningEffort != "" {
		base.ReasoningEffort = overlay.ReasoningEffort
	}
	if overlay.Style != "" {
		base.Style = overlay.Style
	}
	if overlay.ContextWindow > 0 {
		base.ContextWindow = overlay.ContextWindow
	}

	// Providers: khóa của overlay ghi đè khóa cùng tên của base
	if len(overlay.Providers) > 0 {
		if base.Providers == nil {
			base.Providers = make(map[string]ProviderConfig)
		}
		for k, v := range overlay.Providers {
			existing := base.Providers[k]
			if v.Type != "" {
				existing.Type = v.Type
			}
			if v.API != "" {
				existing.API = v.API
			}
			if v.APIKey != "" {
				existing.APIKey = v.APIKey
			}
			if v.BaseURL != "" {
				existing.BaseURL = v.BaseURL
			}
			if len(v.Models) > 0 {
				existing.Models = append([]ModelConfig(nil), v.Models...)
			}
			if len(v.ExtraBody) > 0 {
				existing.ExtraBody = cloneMap(v.ExtraBody)
			}
			if len(v.Extra) > 0 {
				existing.Extra = cloneMap(v.Extra)
			}
			base.Providers[k] = existing
		}
	}

	// Roles: khóa của overlay ghi đè khóa cùng tên của base
	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = make(map[string]RoleConfig)
		}
		for k, v := range overlay.Roles {
			existing := base.Roles[k]
			if v.Provider != "" {
				existing.Provider = v.Provider
			}
			if v.Model != "" {
				existing.Model = v.Model
			}
			if len(v.Fallbacks) > 0 {
				existing.Fallbacks = append([]ModelRef(nil), v.Fallbacks...)
			}
			if v.ReasoningEffort != "" {
				existing.ReasoningEffort = v.ReasoningEffort
			}
			base.Roles[k] = existing
		}
	}

	// Budget / Notify：Ghi đè toàn bộ khối (ngân sách/cảnh báo cấp dự án là tuyên bố chính sách độc lập, không nối ghép từng field với cấu hình toàn cục)
	if overlay.Budget != (BudgetConfig{}) {
		base.Budget = overlay.Budget
	}
	if overlay.Notify.Enabled != nil || overlay.Notify.Command != "" || len(overlay.Notify.Events) > 0 {
		base.Notify = overlay.Notify
	}
	// kiểm tra cập nhật là tùy chọn riêng tư: sau khi bất kỳ tầng cấu hình nào tắt tường minh, tầng cao hơn không nên bật lại ngầm.
	if overlay.DisableUpdateCheck {
		base.DisableUpdateCheck = true
	}

	return base
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// CloneConfig Copy sâu các map/slice sẽ bị sửa đổi lúc chạy trong cấu hình, tránh để cấu hình ứng viên làm bẩn cấu hình hiện tại.
func CloneConfig(cfg Config) Config {
	clone := cfg
	clone.Providers = make(map[string]ProviderConfig, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		pc.Models = append([]ModelConfig(nil), pc.Models...)
		pc.Extra = cloneMap(pc.Extra)
		pc.ExtraBody = cloneMap(pc.ExtraBody)
		clone.Providers[name] = pc
	}
	clone.Roles = make(map[string]RoleConfig, len(cfg.Roles))
	for role, rc := range cfg.Roles {
		rc.Fallbacks = append([]ModelRef(nil), rc.Fallbacks...)
		clone.Roles[role] = rc
	}
	clone.Notify.Events = append([]string(nil), cfg.Notify.Events...)
	return clone
}

// SaveProviderConfig Cập nhật kiểu vá chứng chỉ và kho model của một provider đơn lẻ trong tầng cấu hình đích.
// Chỉ động vào khối providers, tuyệt đối không chạm vào lựa chọn provider/model cấp trên cùng——"hiện đang dùng cái nào" thuộc về /model.
// Tạo cấu hình tối giản khi mục tiêu không tồn tại; từ chối ghi đè khi mục tiêu hỏng.
func SaveProviderConfig(path string, provider string, pc ProviderConfig) error {
	target, found, err := loadOptionalJSON(path)
	if err != nil {
		return err
	}
	if !found {
		target = Config{}
	}
	if target.Providers == nil {
		target.Providers = make(map[string]ProviderConfig)
	}
	target.Providers[provider] = pc
	return SaveConfig(path, target)
}

// stripJSONComments Loại bỏ // chú thích dòng trong JSON, theo dõi trạng thái dấu nháy kép để tránh xóa nhầm nội dung chuỗi.
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if escaped {
			out = append(out, b)
			escaped = false
			continue
		}

		if inString {
			out = append(out, b)
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// Không nằm trong chuỗi
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		// Phát hiện chú thích //
		if b == '/' && i+1 < len(data) && data[i+1] == '/' {
			// Nhảy đến cuối dòng
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			continue
		}

		out = append(out, b)
	}

	return out
}

// WriteStartupError Ghi nối lỗi chí mạng thời kỳ khởi động vào ~/.ainovel/last-error.log, và trả về
// đường dẫn file đó (nỗ lực tối đa, khi thất bại thì trả về chuỗi rỗng). Khi nhấp đúp khởi động thì cửa sổ console sẽ
// đóng ngay khi tiến trình thoát, lỗi lướt qua cực nhanh, nên ghi đĩa là con đường duy nhất để người dùng như vậy truy xuất sự việc sau đó.
func WriteStartupError(msg string) string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "last-error.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg); err != nil {
		return ""
	}
	return path
}

// SaveConfig Ghi cấu hình vào đường dẫn được chỉ định (định dạng JSON, có thụt lề làm đẹp).
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
