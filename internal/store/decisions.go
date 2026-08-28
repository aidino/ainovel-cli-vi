package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DecisionStore kiểm toán các phán quyết ngữ nghĩa của LLM trong thời gian chạy (meta/decisions.jsonl, append-only).
//
// Định vị (docs/engine-arbiter.md §4.3): Nguồn dữ liệu kiểm toán và phát lại ngoại tuyến——ghi lại "lúc đó đã thấy gì
// dữ kiện nào, đã đưa ra phán quyết gì", cung cấp cho eval hồi quy và đối chiếu A/B của Arbiter trong tương lai. Nó **không phải** là nguồn gốc sự kiện,
// cũng **không phải** là nguồn dữ liệu khôi phục (khôi phục chỉ phụ thuộc vào tầng dữ kiện Progress/Checkpoint/RunMeta).
type DecisionStore struct{ io *IO }

func NewDecisionStore(io *IO) *DecisionStore { return &DecisionStore{io: io} }

const (
	decisionSchemaVersion = 1
	decisionsFile         = "meta/decisions.jsonl"
	// maxDecisionInputBytes giới hạn trên của một input; vượt giới hạn sẽ cắt cụt và đánh dấu, tránh dán dài làm vỡ tệp kiểm toán.
	maxDecisionInputBytes = 8 << 10
)

// DecisionRecord bản ghi kiểm toán của một lần phán quyết ngữ nghĩa. facts chỉ lưu dữ kiện có cấu trúc và tham chiếu, không sao chép chính văn.
// input được giữ lại trong bản ghi (cần thiết cho phát lại ngoại tuyến); khử nhạy cảm xảy ra ở ranh giới diag export, không phải lúc ghi đĩa.
type DecisionRecord struct {
	SchemaVersion  int             `json:"schema_version"`
	ID             string          `json:"id"`
	At             string          `json:"at"`
	Kind           string          `json:"kind"`    // intervention | plan_start | volume_end | ...
	Decider        string          `json:"decider"` // arbiter | architect (đánh giá cuối tập)
	CheckpointSeq  int64           `json:"checkpoint_seq,omitempty"`
	Input          string          `json:"input,omitempty"`
	InputTruncated bool            `json:"input_truncated,omitempty"`
	Facts          json.RawMessage `json:"facts,omitempty"`
	Decision       json.RawMessage `json:"decision,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Error          string          `json:"error,omitempty"` // văn bản lỗi khi phán quyết thất bại——thất bại cũng là dữ kiện kiểm toán, không có nó thì việc khắc phục sự cố chỉ có thể dựa vào suy luận
	Model          string          `json:"model,omitempty"`
	DurationMs     int64           `json:"duration_ms,omitempty"`
}

// Append ghi đĩa một bản ghi phán quyết; SchemaVersion/At/ID được phương thức này bổ sung, input bị cắt cụt nếu vượt giới hạn.
// Trả về bản ghi đã bổ sung (ID cho bên gọi liên kết, ví dụ PlanStartRecord.DecisionID).
func (s *DecisionStore) Append(rec DecisionRecord) (DecisionRecord, error) {
	rec.SchemaVersion = decisionSchemaVersion
	if rec.At == "" {
		rec.At = time.Now().Format(time.RFC3339)
	}
	if rec.ID == "" {
		rec.ID = newDecisionID()
	}
	if len(rec.Input) > maxDecisionInputBytes {
		rec.Input = rec.Input[:maxDecisionInputBytes]
		rec.InputTruncated = true
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return rec, fmt.Errorf("marshal decision: %w", err)
	}
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	// Lần ghi thêm trước có thể đã gặp sự cố trước khi ghi ngắt dòng. Trước tiên hãy xóa phần đuôi có thể chứng minh là chưa cam kết theo giao thức, để tránh đưa JSON mới
	// ghép trực tiếp vào sau dòng tàn dư; bản ghi ngắt dòng hoàn chỉnh tuyệt đối không tự động sửa đổi.
	if _, err := s.committedDataUnlocked(); err != nil {
		return rec, fmt.Errorf("repair decision tail: %w", err)
	}
	if err := s.io.AppendLineUnlocked(decisionsFile, append(data, '\n')); err != nil {
		return rec, err
	}
	return rec, nil
}

// Recent trả về n bản ghi gần đây nhất (cũ→mới); trả về rỗng nếu tệp bị thiếu.
//
// Dòng hỏng đã cam kết phải trả về lỗi rõ ràng——Arbiter không thể tiếp tục phán quyết trên gói dữ kiện bị thiếu một phần lịch sử.
// Dòng tàn dư ở đuôi do sự cố làm gián đoạn (byte cuối cùng không phải '\n') được committedDataUnlocked cắt cụt và cảnh báo rõ ràng; đây không phải là
// sửa chữa phỏng đoán, vì giao thức tệp này quy định chỉ có bản ghi kết thúc bằng ngắt dòng mới tính là đã cam kết.
func (s *DecisionStore) Recent(n int) ([]DecisionRecord, error) {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	data, err := s.committedDataUnlocked()
	if err != nil {
		return nil, err
	}
	all, err := parseDecisionRecords(data)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// committedDataUnlocked trả về bản ghi ngắt dòng hoàn chỉnh và cắt cụt các byte tàn dư sau ngắt dòng khỏi đĩa. Bên gọi
// phải giữ khóa ghi io.mu. Việc cắt cụt là lũy đẳng, khi thất bại tệp gốc được giữ lại, lỗi được ném ra rõ ràng.
func (s *DecisionStore) committedDataUnlocked() ([]byte, error) {
	data, err := s.io.ReadFileUnlocked(decisionsFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, nil
	}
	keep := bytes.LastIndexByte(data, '\n') + 1
	if err := os.Truncate(s.io.path(decisionsFile), int64(keep)); err != nil {
		return nil, err
	}
	slog.Warn("đã sửa đuôi chưa commit của kiểm toán phán quyết",
		"module", "store", "file", decisionsFile, "discarded_bytes", len(data)-keep)
	return data[:keep], nil
}

func parseDecisionRecords(data []byte) ([]DecisionRecord, error) {
	var all []DecisionRecord
	lines := bytes.Split(data, []byte{'\n'})
	for i, raw := range lines {
		if i == len(lines)-1 && len(raw) == 0 {
			break
		}
		var rec DecisionRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", decisionsFile, i+1, err)
		}
		all = append(all, rec)
	}
	return all, nil
}

func newDecisionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dec-%d", time.Now().UnixNano())
	}
	return "dec-" + hex.EncodeToString(b[:])
}