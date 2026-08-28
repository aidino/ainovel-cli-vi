package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildWriterPrompt_ByteIdenticalToPreSplit 是文风层验收标准 ①:
// 不放任何ghi đètệp时,组装产物与拆分前的 writer.md 管线逐chữ 节nhất quán 。
// golden 是拆分前 writer.md 的原始快照(testdata/writer-golden.md)。
func TestBuildWriterPrompt_ByteIdenticalToPreSplit(t *testing.T) {
	golden, err := os.ReadFile("testdata/writer-golden.md")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	protocol := mustRead(promptsFS, "prompts/writer.md")
	voice := mustRead(voiceFS, "voice.md")

	// tệp级:占位符回填 == 拆分前原文
	if got := strings.Replace(protocol, voicePlaceholder, strings.TrimSpace(voice), 1); got != string(golden) {
		t.Fatalf("占位符回填与拆分前không nhất quán:\n--- 长度 golden=%d got=%d", len(golden), len(got))
	}

	// 管线级:新组装 == 旧管线(writer.md → simGuidance → style)
	const style = "## 某风格\n\n- kiểm tra "
	old := WithSimulationGuidance(string(golden), "writer") + "\n\n" + style
	got := BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, style)
	if got != old {
		t.Fatal("组装管线与拆分前不等价")
	}

	// 无风格thêm vào 时也等价
	if BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, "") != WithSimulationGuidance(string(golden), "writer") {
		t.Fatal("无 style 时组装管线与拆分前不等价")
	}
}

// TestLoad_NoOverrides 零ghi đè时 Voice/AntiAITone 与内置逐chữ 节nhất quán 。
func TestLoad_NoOverrides(t *testing.T) {
	b := Load("default", LoadOptions{})
	if b.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("无ghi đè时 Voice 应与内置逐chữ 节nhất quán ")
	}
	if b.References.AntiAITone != mustRead(referencesFS, "references/anti-ai-tone.md") {
		t.Fatal("无ghi đè时 AntiAITone 应与内置逐chữ 节nhất quán ")
	}
	if _, ok := b.Styles["default"]; !ok {
		t.Fatal("内置风格集应含 default")
	}
}

func TestInterventionPromptsKeepScopeContract(t *testing.T) {
	prompts := loadPrompts()
	for _, phrase := range []string{"ngữ cảnh không đồng nghĩa quyền sửa đổi", "phạm vi tối thiểu vừa đủ", "phạm vi phân tích không đồng nghĩa phạm vi sửa"} {
		if !strings.Contains(prompts.ArbiterIntervention, phrase) {
			t.Fatalf("Arbiter 干预nhắc nhở 缺少范围契约 %q", phrase)
		}
	}
	for _, phrase := range []string{"yêu cầu can thiệp gốc của người dùng", "phạm vi phân tích không đồng nghĩa phạm vi sửa", "tập chương tối thiểu vừa đủ"} {
		if !strings.Contains(prompts.Editor, phrase) {
			t.Fatalf("Editor nhắc nhở 缺少范围契约 %q", phrase)
		}
	}
}

func TestStructuredArbiterPromptsContainOnlySemantics(t *testing.T) {
	prompts := loadPrompts()
	for name, prompt := range map[string]string{
		"plan_start": prompts.ArbiterPlanStart,
		"failure":    prompts.ArbiterFailure,
	} {
		for _, duplicate := range []string{"```json", "不要 Markdown", "输出一个 JSON 对象"} {
			if strings.Contains(prompt, duplicate) {
				t.Fatalf("%s nhắc nhở từ 仍lặp lại 维护输出định dạng  %q", name, duplicate)
			}
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLoad_ThreeTierAppendAndReplace ghi đè三层优先级与逐资产语义(验收标准 ②)。
func TestLoad_ThreeTierAppendAndReplace(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	opts := LoadOptions{HomeStyleDir: home, BookStyleDir: book}

	// voice / anti-ai-tone:thêm vào 语义,toàn cục 在前、本书在后,带边界标记
	writeFile(t, filepath.Join(home, "voice.md"), "toàn cục :少用成语")
	writeFile(t, filepath.Join(book, "voice.md"), "本书:多写对话")
	writeFile(t, filepath.Join(book, "anti-ai-tone.md"), "本书判据:禁排比")

	// styles:同名整tệp替换 + 新名新增;非法名bỏ qua 
	writeFile(t, filepath.Join(home, "styles", "fantasy.md"), "toàn cục 改写的奇幻")
	writeFile(t, filepath.Join(book, "styles", "xianxia.md"), "自定义仙侠")
	writeFile(t, filepath.Join(book, "styles", "Bad Name!.md"), "非法")

	// 题材参考:同名整tệp替换,本书 > toàn cục 
	writeFile(t, filepath.Join(home, "genres", "fantasy", "style-references.md"), "toàn cục 参考")
	writeFile(t, filepath.Join(book, "genres", "fantasy", "style-references.md"), "本书参考")

	b := Load("fantasy", opts)

	builtinVoice := mustRead(voiceFS, "voice.md")
	if !strings.HasPrefix(b.Voice, builtinVoice) {
		t.Fatal("thêm vào 语义phải giữ lại 内置原文为tiền tố ")
	}
	giIdx := strings.Index(b.Voice, "## Ghi đè văn phong toàn cục người dùng")
	bkIdx := strings.Index(b.Voice, "## Ghi đè văn phong sách này")
	if giIdx < 0 || bkIdx < 0 || giIdx > bkIdx {
		t.Fatalf("Lỗi thứ tự thêm vào: global=%d book=%d", giIdx, bkIdx)
	}
	if !strings.Contains(b.Voice, "toàn cục :少用成语") || !strings.Contains(b.Voice, "本书:多写对话") {
		t.Fatal("ghi đènội dung thiếu ")
	}
	if !strings.Contains(b.References.AntiAITone, "本书判据:禁排比") {
		t.Fatal("anti-ai-tone 本书thêm vào thiếu ")
	}

	if b.Styles["fantasy"] != "toàn cục 改写的奇幻" {
		t.Fatal("styles 同名应整tệp替换")
	}
	if b.Styles["xianxia"] != "自定义仙侠" {
		t.Fatal("新增自定义风格应即放即用")
	}
	if _, ok := b.Styles["Bad Name!"]; ok {
		t.Fatal("非法风格名phải 被bỏ qua ")
	}

	if b.References.StyleReference != "本书参考" {
		t.Fatalf("题材参考nên là本书ghi đè优先,got %q", b.References.StyleReference)
	}
}

// TestLoad_BookOverridesHomeOnStyles 本书 styles ghi đètoàn cục 同名。
func TestLoad_BookOverridesHomeOnStyles(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	writeFile(t, filepath.Join(home, "styles", "romance.md"), "toàn cục 版")
	writeFile(t, filepath.Join(book, "styles", "romance.md"), "本书版")
	b := Load("default", LoadOptions{HomeStyleDir: home, BookStyleDir: book})
	if b.Styles["romance"] != "本书版" {
		t.Fatalf("本书应ghi đètoàn cục ,got %q", b.Styles["romance"])
	}
}

// TestOverrideVoice_SharesAssemblyPath eval 的 voice A/B 与生产同组装đường dẫn(验收标准 ④)。
func TestOverrideVoice_SharesAssemblyPath(t *testing.T) {
	b := Load("default", LoadOptions{})
	b.OverrideVoice("## 实验文风\n\n- 一câu 话")
	got := BuildWriterPrompt(b.Prompts.Writer, b.Voice, "")
	if !strings.Contains(got, "## 实验文风") {
		t.Fatal("OverrideVoice 未生效")
	}
	if strings.Contains(got, voicePlaceholder) {
		t.Fatal("占位符phải 被消耗")
	}
	// 协议部分不受 voice ghi đè影响
	if !strings.Contains(got, "## Giao thức thực thi") {
		t.Fatal("协议模板不得被 voice ghi đè破坏")
	}
}