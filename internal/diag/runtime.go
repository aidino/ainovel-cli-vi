package diag

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	logTailCap   = 200 << 10 // Log chỉ lấy phần đuôi 200KB (vòng lặp là hiện tượng gần)
	sessionTail  = 80        // Số đoạn đuôi bộ xương (xem thứ tự phân phối)
	repeatWindow = 150       // Tổng hợp lặp lại chỉ xem bấy nhiêu sự kiện gần - trong chạy dài, công cụ bình thường tích lũy hàng trăm lần,
	// Vòng lặp thực sự là tập trung cao độ ở gần; dùng cửa sổ thay vì tích lũy, tránh đánh giá sai "đẩy tiến bình thường" thành "vòng lặp chết".
	recentAgents = 2  // Số phiên subagent hoạt động gần đây quét thêm
	repeatMin    = 3  // Lặp lại đạt mấy lần mới tính là "tín hiệu tần suất cao"
	repeatTopN   = 12 // Chữ ký lặp lại liệt kê tối đa bao nhiêu mục
)

// RuntimeCapture là kết quả làm nhạy của một lần bắt runtime. Chỉ mang tín hiệu runtime;
// phase/flow/chương v.v. trạng thái sáng tác được mang bởi Report.Stats, không lặp lại ở đây.
type RuntimeCapture struct {
	GoOS, GoArch  string
	Models        []RoleModel  // provider/model thực sự có hiệu lực của mỗi phiên (thu thập từ _meta)
	CurrentStep   string       // checkpoint mới nhất: scope.step
	StuckStep     string       // Phần đuôi liên tục cùng step; "" = không kẹt
	StuckCount    int          // Số lần liên tục
	Repeats       []RepeatStat // Chữ ký lặp lại top-N (tín hiệu vòng lặp)
	DupContent    []DupStat    // Văn bản cùng sha xuất hiện lặp lại (tạo lại cùng một đoạn)
	LogKinds      map[string]int
	LogErrors     int
	LogWarns      int
	StopGuard     int
	Tail          []SkelEvent // N bộ xương cuối (xem thứ tự)
	RedactedTexts int         // Tổng số khối văn bản bị che (tự kiểm tra làm nhạy)
	Sources       []string    // Nguồn thực tế đọc được (tự kiểm tra)
}

// RoleModel ghi lại provider/model thực tế dùng của một phiên.
type RoleModel struct {
	Agent, Provider, Model string
}

// RepeatStat là một chữ ký lặp lại và số lần của nó.
type RepeatStat struct {
	Sig   string
	Count int
}

// DupStat là số lần xuất hiện lặp lại của cùng một đoạn văn bản đã làm nhạy.
type DupStat struct {
	Sha   string
	Count int
}

// sessionLine phân tích một dòng của sessions/*.jsonl: nhúng agentcore.Message + _meta tùy chọn.
type sessionLine struct {
	agentcore.Message
	Meta *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"_meta"`
}

var kindRe = regexp.MustCompile(`kind=(\S+)`)

// CaptureRuntime lấy tín hiệu runtime chỉ đọc từ thư mục output và tổng hợp làm nhạy.
// Bất kỳ nguồn nào thiếu đều hạ cấp an toàn (không báo lỗi), cố gắng hết sức.
func CaptureRuntime(s *store.Store) RuntimeCapture {
	rc := RuntimeCapture{GoOS: runtime.GOOS, GoArch: runtime.GOARCH, LogKinds: map[string]int{}}

	rc.CurrentStep, rc.StuckStep, rc.StuckCount = analyzeCheckpoints(s.Checkpoints.All())
	captureSessions(s.Dir(), &rc)
	captureLog(s.Dir(), &rc)
	return rc
}

// analyzeCheckpoints lấy step mới nhất, và tính phần đuôi liên tục cùng step (tín hiệu kẹt).
func analyzeCheckpoints(cps []domain.Checkpoint) (current, stuck string, count int) {
	if len(cps) == 0 {
		return "", "", 0
	}
	key := func(c domain.Checkpoint) string { return fmt.Sprintf("%s.%s", c.Scope, c.Step) }
	current = key(cps[len(cps)-1])
	n := 1
	for i := len(cps) - 2; i >= 0; i-- {
		if key(cps[i]) == current {
			n++
		} else {
			break
		}
	}
	if n >= repeatMin {
		stuck, count = current, n
	}
	return current, stuck, count
}

// captureSessions quét các phiên Worker hoạt động gần đây, tổng hợp làm nhạy.
func captureSessions(dir string, rc *RuntimeCapture) {
	sessDir := filepath.Join(dir, "meta", "sessions")
	files := sessionFiles(sessDir)

	repeats := map[string]int{}
	dups := map[string]int{}
	models := map[string]RoleModel{}

	for _, f := range files {
		evs := scanSession(filepath.Join(sessDir, f.path), f.agent, rc, models)
		// Tổng hợp chỉ xem cửa sổ gần: trong chạy dài subagent/novel_context tích lũy hàng trăm lần là đẩy tiến bình thường,
		// không phải vòng lặp; vòng lặp chết thực sự là tập trung cao độ ở gần.
		aggregateRepeats(f.agent, tailEvents(evs, repeatWindow), repeats, dups)
		// files sắp xếp giảm dần theo thời gian hoạt động; lấy phiên không rỗng đầu tiên làm hiện trường.
		if len(rc.Tail) == 0 && len(evs) > 0 {
			rc.Tail = tailEvents(evs, sessionTail)
		}
		rc.Sources = append(rc.Sources, "sessions/"+f.path)
	}

	rc.Repeats = topRepeats(repeats)
	rc.DupContent = topDups(dups)
	rc.Models = sortedModels(models)
}

type sessionFile struct {
	path  string // Tương đối với sessDir
	agent string
}

// sessionFiles trả về phiên Worker hoạt động gần đây.
func sessionFiles(sessDir string) []sessionFile {
	agentsDir := filepath.Join(sessDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil
	}
	type withTime struct {
		name string
		mod  int64
	}
	var agents []withTime
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			agents = append(agents, withTime{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].mod > agents[j].mod })
	out := make([]sessionFile, 0, min(len(agents), recentAgents))
	for i, a := range agents {
		if i >= recentAgents {
			break
		}
		stem := strings.TrimSuffix(a.name, ".jsonl")
		out = append(out, sessionFile{path: filepath.Join("agents", a.name), agent: stem})
	}
	return out
}

// scanSession đọc một file phiên, làm nhạy từng dòng, thu thập chuỗi sự kiện và model per-agent.
// Tổng hợp lặp lại/cùng đoạn không làm ở đây - giao cho aggregateRepeats tính trên cửa sổ gần.
func scanSession(path, agent string, rc *RuntimeCapture, models map[string]RoleModel) []SkelEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var evs []SkelEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var sl sessionLine
		if json.Unmarshal(sc.Bytes(), &sl) != nil {
			continue
		}
		ev := redactMessage(agent, sl.Message)
		evs = append(evs, ev)
		rc.RedactedTexts += ev.Redacted
		if sl.Meta != nil && (sl.Meta.Provider != "" || sl.Meta.Model != "") {
			models[agent] = RoleModel{Agent: agent, Provider: sl.Meta.Provider, Model: sl.Meta.Model}
		}
	}
	return evs
}

// aggregateRepeats tích lũy chữ ký lặp lại và đoạn văn bản giống nhau trên cửa sổ sự kiện cho trước.
func aggregateRepeats(agent string, evs []SkelEvent, repeats, dups map[string]int) {
	for _, ev := range evs {
		for _, t := range ev.Tools {
			sig := agent + " · " + t.Name
			if t.Invalid {
				sig += " (args invalid)"
			}
			repeats[sig]++
		}
		if ev.ErrClass != "" {
			repeats[agent+" · err: "+ev.ErrClass]++
		}
		if ev.TextSha != "" {
			dups[ev.TextSha]++
		}
	}
}

func tailEvents(evs []SkelEvent, n int) []SkelEvent {
	if len(evs) <= n {
		return evs
	}
	return evs[len(evs)-n:]
}

// captureLog đọc phần đuôi log, chỉ tổng hợp tín hiệu cấu trúc (kind/error/warn/stop_guard),
// Không đóng gói dòng log gốc - Detail có thể kẹp theo chính văn.
func captureLog(dir string, rc *RuntimeCapture) {
	path := filepath.Join(dir, "logs", "tui.log")
	tail, ok := readTail(path)
	if !ok {
		path = filepath.Join(dir, "logs", "headless.log")
		tail, ok = readTail(path)
	}
	if !ok {
		return
	}
	rc.Sources = append(rc.Sources, "logs/"+filepath.Base(path)+" (phần đuôi)")

	sc := bufio.NewScanner(bytes.NewReader(tail))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "level=ERROR"):
			rc.LogErrors++
		case strings.Contains(line, "level=WARN"):
			rc.LogWarns++
		}
		if m := kindRe.FindStringSubmatch(line); m != nil {
			rc.LogKinds[m[1]]++
		}
		if strings.Contains(line, "stop_guard") {
			rc.StopGuard++
		}
	}
}

// readTail đọc logTailCap byte phần đuôi file, và vứt bỏ nửa dòng đầu tiên có thể bị cắt đứt.
func readTail(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	var off int64
	if size > logTailCap {
		off = size - logTailCap
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	if off > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return data, true
}

func topRepeats(m map[string]int) []RepeatStat {
	var out []RepeatStat
	for sig, c := range m {
		if c >= repeatMin {
			out = append(out, RepeatStat{Sig: sig, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Sig < out[j].Sig
	})
	if len(out) > repeatTopN {
		out = out[:repeatTopN]
	}
	return out
}

func topDups(m map[string]int) []DupStat {
	var out []DupStat
	for sha, c := range m {
		if c >= repeatMin {
			out = append(out, DupStat{Sha: sha, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Sha < out[j].Sha
	})
	return out
}

func sortedModels(m map[string]RoleModel) []RoleModel {
	out := make([]RoleModel, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}