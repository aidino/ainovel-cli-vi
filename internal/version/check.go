package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultRepo là repo upstream mặc định dùng để kiểm tra phiên bản và tự cập nhật.
const DefaultRepo = "aidino/ainovel-cli-vi"

// DefaultCheckInterval là khoảng cách tối thiểu giữa hai lần kiểm tra qua mạng.
// GitHub API ẩn danh giới hạn 60 req/h, và nhịp phát hành là theo ngày,
// kiểm tra thường xuyên hơn chỉ gây phiền mà không có lợi.
const DefaultCheckInterval = 24 * time.Hour

// CheckOptions là đầu vào cho một lần kiểm tra phiên bản. CachePath do caller
// truyền vào (thường là update-check.json trong thư mục cấu hình); package này
// không phụ thuộc vào bootstrap, giữ version là package lá trong cây phụ thuộc.
type CheckOptions struct {
	Repo           string
	CurrentVersion string
	Client         *http.Client
	CachePath      string        // rỗng = không lưu đĩa, mỗi lần gọi đều liên mạng
	MaxAge         time.Duration // thời hạn cache; <=0 thì dùng DefaultCheckInterval
}

// CheckResult là kết luận của lần kiểm tra phiên bản. Notes chứa nội dung
// release gốc (markdown), caller phải làm sạch trước khi hiển thị theo
// phương tiện đầu ra của mình; việc nâng cấp do người dùng quyết định.
type CheckResult struct {
	Latest          string
	Current         string
	Notes           string
	UpdateAvailable bool
	FromCache       bool
}

// checkCache là cấu trúc lưu xuống đĩa tại CachePath.
type checkCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
	Notes     string    `json:"notes"`
}

// CheckUpdate truy vấn release mới nhất từ upstream và xác định có cần nhắc
// nâng cấp hay không. Chỉ đọc, không ghi bất kỳ binary nào; việc nâng cấp
// hay không, khi nào nâng cấp hoàn toàn do người dùng quyết định qua
// `ainovel-cli update`. Cache hoặc lỗi mạng đều được trả qua error;
// lỗi ghi cache vẫn trả kết quả kiểm tra đã thu được.
func CheckUpdate(ctx context.Context, opts CheckOptions) (*CheckResult, error) {
	current := Normalize(opts.CurrentVersion)
	if current == "dev" {
		// Bản build cục bộ không có ngữ nghĩa phiên bản để so sánh,
		// bỏ qua kiểm tra để tránh báo nhầm bản build bất kỳ là "có thể nâng cấp".
		return &CheckResult{Current: current}, nil
	}
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCheckInterval
	}

	var cacheErr error
	if opts.CachePath != "" {
		c, err := loadCache(opts.CachePath)
		switch {
		case err == nil:
			age := time.Since(c.LastCheck)
			if age < 0 {
				cacheErr = fmt.Errorf("thời gian cache kiểm tra cập nhật nằm trong tương lai: %s", c.LastCheck.Format(time.RFC3339))
			} else if age <= maxAge {
				result, resultErr := c.result(current)
				if resultErr == nil {
					return result, nil
				}
				cacheErr = fmt.Errorf("xác thực cache kiểm tra cập nhật: %w", resultErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// Lần kiểm tra đầu tiên không có cache là trạng thái bình thường.
		default:
			cacheErr = fmt.Errorf("đọc cache kiểm tra cập nhật: %w", err)
		}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	rel, err := fetchRelease(ctx, client, repo, "latest")
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if rel.TagName == "" {
		return nil, errors.Join(cacheErr, fmt.Errorf("release thiếu tag_name"))
	}

	result, err := newCheckResult(rel.TagName, current, rel.Body, false)
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if err := writeCache(opts.CachePath, rel); err != nil {
		cacheErr = errors.Join(cacheErr, fmt.Errorf("ghi cache kiểm tra cập nhật: %w", err))
	}
	// Lỗi cache trả kèm như lỗi đồng hành, caller nên ghi log nhưng vẫn dùng được kết quả kiểm tra.
	return result, cacheErr
}

func newCheckResult(latest, current, notes string, fromCache bool) (*CheckResult, error) {
	updateAvailable, err := isNewer(latest, current)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Latest:          latest,
		Current:         current,
		Notes:           notes,
		UpdateAvailable: updateAvailable,
		FromCache:       fromCache,
	}, nil
}

// loadCache đọc và kiểm tra cache; file không tồn tại, hỏng hoặc thiếu trường
// do caller xử lý riêng.
func loadCache(path string) (*checkCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("giải mã cache: %w", err)
	}
	if c.Latest == "" || c.LastCheck.IsZero() {
		return nil, fmt.Errorf("cache thiếu latest hoặc last_check")
	}
	return &c, nil
}

// result chuyển nội dung cache thành kết luận kiểm tra (FromCache=true).
func (c *checkCache) result(current string) (*CheckResult, error) {
	return newCheckResult(c.Latest, current, c.Notes, true)
}

// writeCache ghi kết quả kiểm tra xuống đĩa một cách nguyên tử. Tạo thư mục nếu chưa có.
func writeCache(path string, rel *release) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.Marshal(checkCache{LastCheck: time.Now(), Latest: rel.TagName, Notes: rel.Body})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
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

// isNewer dùng quy tắc SemVer đầy đủ để so sánh phiên bản; phiên bản
// không hợp lệ trả lỗi rõ ràng, tránh bỏ sót thông báo.
func isNewer(latest, current string) (bool, error) {
	latest = Normalize(latest)
	current = Normalize(current)
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("phiên bản mới nhất không hợp lệ %q", latest)
	}
	if !semver.IsValid(current) {
		return false, fmt.Errorf("phiên bản hiện tại không hợp lệ %q", current)
	}
	return semver.Compare(latest, current) > 0, nil
}
