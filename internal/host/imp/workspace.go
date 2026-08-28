package imp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// workspaceSchemaVersion là phiên bản schema tổng thể của không gian làm việc import.
// Khi không khớp yêu cầu rõ ràng dùng phiên bản khớp để tiếp tục hoặc import lại, không đoán di chuyển (RFC §6.1).
const workspaceSchemaVersion = 1

// Digest tính tóm tắt nội dung, kế thừa quy ước có sẵn của kho "sha256:"+hex (xem store/checkpoints.go).
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Artifact là danh tính thống nhất của mỗi công kiện ngữ nghĩa trong không gian làm việc: Phiên bản schema + tóm tắt đầu vào + tải trọng.
// Chỉ khi có thể tái tạo InputDigest giống nhau từ đầu vào ngữ nghĩa thực tại mới có thể tái sử dụng (RFC §6.3 / Bất biến 1).
// Không cài đặt sơ đồ phụ thuộc: LoadState so sánh dần InputDigest dọc theo đường ống tuyến tính cố định để phán đoán tái sử dụng và hết hạn, NextAction từ đó suy ra bước tiếp theo.
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}

// Manifest tương ứng với bản chụp nguồn chuẩn hóa duy nhất, là danh tính không gian làm việc chứ không phải công kiện phái sinh (RFC §6.1).
// Không lưu đường dẫn nguồn tuyệt đối, tránh rò rỉ thư mục máy và loại bỏ vấn đề khôi phục do di chuyển file.
type Manifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

// Intent lưu quyền rõ ràng của người dùng khi khởi động import, sau khi khôi phục vẫn phải tuân thủ, không suy ra từ công kiện, Runner không âm thầm sửa (RFC §6.1).
type Intent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}

// Đường dẫn tương đối công kiện chuẩn của không gian làm việc.
const (
	fileManifest     = "manifest.json"
	fileIntent       = "intent.json"
	fileSource       = "source.txt"
	fileGuidance     = "guidance.txt"
	fileSegmentation = "segmentation.json"
	fileConfirmation = "confirmation.json"
	fileSynthesis    = "synthesis.json"
	fileStoryResolve = "story-resolution.json"
	dirAnalyses      = "analyses"
	dirRangeDigests  = "range-digests"
	dirSegmentChunks = "segment-chunks"
	dirFailures      = "failures"
)

// Workspace là tay cầm đọc ghi công kiện nguyên tử của thư mục <gốc sách>/meta/import/.
type Workspace struct {
	dir string
}

// OpenWorkspace trả về tay cầm trỏ tới meta/import/ dưới gốc sách; không đảm bảo thư mục đã tồn tại, dùng Active() để phán đoán.
func OpenWorkspace(bookDir string) *Workspace {
	return &Workspace{dir: filepath.Join(bookDir, "meta", "import")}
}

// Dir trả về đường dẫn tuyệt đối không gian làm việc (dùng làm điểm rơi công kiện chẩn đoán và thất bại).
func (w *Workspace) Dir() string { return w.dir }

func (w *Workspace) path(rel string) string { return filepath.Join(w.dir, rel) }

// Active phán đoán xem có không gian làm việc hoạt động đã đăng nào không. meta/import/ không tồn tại thì không tính là hoạt động,
// thư mục bán khởi tạo tồn tại dưới dạng meta/import.init-*, sẽ không bị phán nhầm là hoạt động (RFC §6.1).
func (w *Workspace) Active() bool {
	fi, err := os.Stat(w.dir)
	return err == nil && fi.IsDir()
}

func (w *Workspace) has(rel string) bool {
	_, err := os.Stat(w.path(rel))
	return err == nil
}

// writeAtomic viết nguyên tử vào rel (tương đối với không gian làm việc) bằng "file tạm + fsync + rename".
func (w *Workspace) writeAtomic(rel string, data []byte) error {
	full := w.path(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return err
	}
	syncDir(filepath.Dir(full))
	return nil
}

// syncDir best-effort fsync mục thư mục, giúp rename vừa hoàn thành vẫn bền bỉ sau khi cúp điện.
// Nền tảng như Windows có thể không hỗ trợ Sync thư mục, bỏ qua lỗi của nó——an toàn sập tiến trình không phụ thuộc vào nó, chỉ bù cho tình huống cúp điện (RFC §12.3).
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func (w *Workspace) writeJSON(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.writeAtomic(rel, append(data, '\n'))
}

func (w *Workspace) readJSON(rel string, v any) error {
	data, err := os.ReadFile(w.path(rel))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// LoadManifest đọc danh tính bản chụp nguồn không gian làm việc.
func (w *Workspace) LoadManifest() (*Manifest, error) {
	var m Manifest
	if err := w.readJSON(fileManifest, &m); err != nil {
		return nil, err
	}
	if m.Version != workspaceSchemaVersion {
		return nil, fmt.Errorf("phiên bản schema manifest %d != %d, vui lòng dùng phiên bản khớp để tiếp tục hoặc import lại", m.Version, workspaceSchemaVersion)
	}
	return &m, nil
}

// LoadIntent đọc quyền khởi động của người dùng.
func (w *Workspace) LoadIntent() (*Intent, error) {
	var in Intent
	if err := w.readJSON(fileIntent, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// LoadSource đọc văn bản bản chụp nguồn đã chuẩn hóa.
func (w *Workspace) LoadSource() ([]byte, error) {
	return os.ReadFile(w.path(fileSource))
}

// LoadGuidance đọc hướng dẫn cắt của người dùng (RFC §18.3); thiếu tức là không có hướng dẫn.
// Hướng dẫn và source.txt đều là đầu vào ngữ nghĩa của cắt chứ không phải công kiện phái sinh, được cập nhật rõ ràng bằng --guide,
// thay đổi nội dung khiến segmentation và InputDigest hạ nguồn của nó tự nhiên không khớp.
func (w *Workspace) LoadGuidance() (string, error) {
	data, err := os.ReadFile(w.path(fileGuidance))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readBytes đọc byte gốc công kiện, dùng để liên kết InputDigest hạ nguồn.
func (w *Workspace) readBytes(rel string) ([]byte, error) {
	return os.ReadFile(w.path(rel))
}

// writeArtifact ghi công kiện ngữ nghĩa có danh tính thống nhất.
func writeArtifact[T any](w *Workspace, rel, inputDigest string, payload T) error {
	return w.writeJSON(rel, Artifact[T]{
		SchemaVersion: workspaceSchemaVersion,
		InputDigest:   inputDigest,
		Payload:       payload,
	})
}

// readArtifact đọc công kiện ngữ nghĩa và xác minh phiên bản schema; InputDigest có khớp hay không do bên gọi phán đoán theo đầu vào hiện tại.
func readArtifact[T any](w *Workspace, rel string) (*Artifact[T], error) {
	var a Artifact[T]
	if err := w.readJSON(rel, &a); err != nil {
		return nil, err
	}
	if a.SchemaVersion != workspaceSchemaVersion {
		return nil, fmt.Errorf("phiên bản schema %s %d != %d, vui lòng dùng phiên bản khớp để tiếp tục hoặc import lại", rel, a.SchemaVersion, workspaceSchemaVersion)
	}
	return &a, nil
}

// clearDir xóa một thư mục cache trung gian trong không gian làm việc. Lỗi bắt buộc giao cho bên gọi xử lý: Nuốt đi sẽ khiến câu
// văn nói dối——lần chạy lại sau vẫn tái sử dụng cache hỏng (Windows diệt virus/chiếm tay cầm là tình huống có thật, Debug-First).
func (w *Workspace) clearDir(rel string) error {
	return os.RemoveAll(w.path(rel))
}

// FailureMeta là siêu dữ liệu chẩn đoán của lần thất bại gần nhất (RFC §14.2).
type FailureMeta struct {
	Stage         string `json:"stage"`
	Detail        string `json:"detail"`
	StopReason    string `json:"stop_reason,omitempty"`
	PrefixSalvage string `json:"prefix_salvage,omitempty"` // available:N / unavailable
}

// writeFailure best-effort lưu siêu dữ liệu thất bại gần nhất và phản hồi model gốc chưa cắt vào failures/ (RFC §14.2).
// Phản hồi gốc có thể chứa chính văn, chỉ rơi vào thư mục sách của người dùng, không vào log thông thường hay xuất chẩn đoán ẩn danh.
func (w *Workspace) writeFailure(meta FailureMeta, rawResponse string) {
	_ = w.writeJSON(filepath.Join(dirFailures, "last.json"), meta)
	_ = w.writeAtomic(filepath.Join(dirFailures, "last-response.txt"), []byte(rawResponse))
}

// createWorkspace viết đủ manifest/intent/source trong thư mục tạm rồi xác minh, sau đó đăng nguyên tử bằng đổi tên thư mục thành meta/import/.
// Như vậy bộ 3 ban đầu sẽ không vào NextAction ở trạng thái bán khởi tạo, cũng không cần stage=initializing (RFC §6.1).
func createWorkspace(bookDir string, m Manifest, in Intent, normalized []byte) (*Workspace, error) {
	base := filepath.Join(bookDir, "meta")
	final := filepath.Join(base, "import")
	if fi, err := os.Stat(final); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("Không gian làm việc import đã tồn tại: %s (không có tham số /import có thể khôi phục từ đây)", final)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(base, "import.init-*")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmp)
		}
	}()

	tw := &Workspace{dir: tmp}
	if err := tw.writeAtomic(fileSource, normalized); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileManifest, m); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileIntent, in); err != nil {
		return nil, err
	}
	// Trước khi đăng, xác minh bộ 3 đọc được và bản chụp nguồn nhất trí với manifest, triệt để ngăn chặn không gian làm việc viết dở.
	got, err := tw.LoadManifest()
	if err != nil {
		return nil, fmt.Errorf("xác minh manifest ban đầu: %w", err)
	}
	src, err := tw.LoadSource()
	if err != nil {
		return nil, fmt.Errorf("xác minh bản chụp nguồn ban đầu: %w", err)
	}
	if d := Digest(src); d != got.NormalizedSHA256 {
		return nil, fmt.Errorf("tóm tắt bản chụp nguồn ban đầu không nhất trí: %s != %s", d, got.NormalizedSHA256)
	}
	if _, err := tw.LoadIntent(); err != nil {
		return nil, fmt.Errorf("xác minh intent ban đầu: %w", err)
	}

	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	syncDir(base)
	committed = true
	return &Workspace{dir: final}, nil
}
