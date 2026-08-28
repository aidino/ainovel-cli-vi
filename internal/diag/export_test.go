package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// sentinel 是一段绝不该xuất hiện 在xuất 里的"tiểu thuyết chính văn"。
const sentinel = "雪夜里主角揭穿了反派的惊天阴谋这是机密chính văn"

// writeSession 把若干tin nhắn 按 sessions/*.jsonl 的định dạng 写到临时 output thư mục。
func writeSession(t *testing.T, rel string, msgs []agentcore.Message) string {
	t.Helper()
	dir := t.TempDir()
	writeSessionAt(t, dir, rel, msgs)
	return dir
}

func writeSessionAt(t *testing.T, dir, rel string, msgs []agentcore.Message) {
	t.Helper()
	path := filepath.Join(dir, "meta", "sessions", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func commitCall(chapterRaw string) agentcore.Message {
	args := json.RawMessage(`{"chapter":` + chapterRaw + `,"content":"` + sentinel + sentinel + `"}`)
	return agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{Name: "commit_chapter", Args: args})},
	}
}

func errResult(msg string) agentcore.Message {
	return agentcore.Message{
		Role:     agentcore.RoleTool,
		Content:  []agentcore.ContentBlock{agentcore.TextBlock(msg)},
		Metadata: map[string]any{"is_error": true},
	}
}

// TestExport_DeathLoopShape 端到端复现 #34：model 把 commit_chapter 的 chapter
// chữ 符串化导致校验vòng lặp 。khẳng địnhxuất 能定位、vàtiểu thuyết chính văn零出包。
func TestExport_DeathLoopShape(t *testing.T) {
	var msgs []agentcore.Message
	// 一段 Worker 输出的裸chính văn（<4KB，绕过 session_compact），phải 被打码。
	msgs = append(msgs, agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	// 14 轮 commit_chapter(chapter:"7") + InputValidationError。
	for range 14 {
		msgs = append(msgs, commitCall(`"7"`))
		msgs = append(msgs, errResult("InputValidationError: chapter must be int"))
	}

	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), msgs)
	s := store.NewStore(dir)
	rep, rc := Diagnose(s)
	out := string(RenderExport(rep, rc))

	if strings.Contains(out, sentinel) {
		t.Fatalf("tiểu thuyết chính văn出包了！xuất chứa  sentinel:\n%s", out)
	}
	if !strings.Contains(out, `chapter: "7"`) {
		t.Errorf("缺类型ngoại lệ 信号 chapter: \"7\"（#34 根因）\n%s", out)
	}
	if !strings.Contains(out, "InputValidationError") {
		t.Errorf("lỗi 串未giữ lại \n%s", out)
	}
	if !strings.Contains(out, "×14") {
		t.Errorf("lặp lại 聚合未列出 ×14\n%s", out)
	}
	// Phase 2：chạy 时检测应把这个vòng lặp 判成 critical 的 RepeatedToolError。
	if !strings.Contains(out, "tool lặp lại cùng một lỗi") {
		t.Errorf("phát hiện runtime không sản xuất RepeatedToolError\n%s", out)
	}
	if !strings.Contains(out, "[critical]") {
		t.Errorf("14 次lặp lại 应升为 critical\n%s", out)
	}
}

// TestExport_NumberVsStringArg 证明标量与chữ 符串投影能区分类型：
// chapter:7（数chữ ）giữ lại 为 7，chapter:"7"（chữ 符串）giữ lại 为 "7"。
func TestExport_NumberVsStringArg(t *testing.T) {
	intDir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`7`)})
	si := store.NewStore(intDir)
	repInt, rcInt := Diagnose(si)
	outInt := string(RenderExport(repInt, rcInt))
	if !strings.Contains(outInt, "chapter: 7") || strings.Contains(outInt, `chapter: "7"`) {
		t.Errorf("数chữ tham số 应渲染为 chapter: 7（không mang 引号）\n%s", outInt)
	}
}

// TestProjectValue_ProseArgRedacted 守护脱敏边界：标识符型短值giữ lại 、
// 中文/带空格的短值（如 dispatch task、chapter title）一律打码。
func TestProjectValue_ProseArgRedacted(t *testing.T) {
	keep := map[string]string{
		`"7"`:       `"7"`,       // chữ 符串化数chữ （#34 信号）
		`"premise"`: `"premise"`, // 枚举
		`"writer"`:  `"writer"`,  // nhân vật名
		`7`:         `7`,         // 数chữ 标量
		`true`:      `true`,      // bool 标量
	}
	for in, want := range keep {
		if got := projectValue([]byte(in)); got != want {
			t.Errorf("应giữ lại  %s：got %q want %q", in, got, want)
		}
	}
	// 含中文 / 空格 → phải 打码，và不含原文。
	prose := []string{`"第7chương  雪夜的真相"`, `"雪夜杀机"`, `"主角揭穿阴谋"`}
	for _, in := range prose {
		got := projectValue([]byte(in))
		if !strings.HasPrefix(got, "<redacted") {
			t.Errorf("中文/带空格短值应打码：%s → %q", in, got)
		}
		if strings.Contains(got, "雪夜") || strings.Contains(got, "主角") {
			t.Errorf("打码后仍含chính văn：%s → %q", in, got)
		}
	}
}

// TestWriteExport_WritesFile 证明纯函数đường dẫn：不依赖 TUI，写出固定相对đường dẫn。
func TestWriteExport_WritesFile(t *testing.T) {
	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`"7"`), errResult("boom")})
	s := store.NewStore(dir)

	rep, rc := Diagnose(s)
	path, err := WriteExport(s, rep, rc)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if want := filepath.Join(dir, filepath.FromSlash(ExportRelPath)); path != want {
		t.Errorf("đường dẫn不对：got %s want %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "diag-export") {
		t.Errorf("tệpnội dung ngoại lệ \n%s", data)
	}
	if strings.Contains(string(data), sentinel) {
		t.Errorf("写出的tệp夹带chính văn")
	}
}

// TestRedactMessage_DupSha 证明同一段文本反复xuất hiện 产生同 sha（vòng lặp 信号）。
func TestRedactMessage_DupSha(t *testing.T) {
	a := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	b := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	if a.TextSha == "" || a.TextSha != b.TextSha {
		t.Errorf("giống nhau chính văn应得giống nhau  sha：%q vs %q", a.TextSha, b.TextSha)
	}
	if a.Redacted != 1 {
		t.Errorf("应打码 1 个文本块，got %d", a.Redacted)
	}
}