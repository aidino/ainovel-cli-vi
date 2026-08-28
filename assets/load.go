package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

//go:embed voice.md
var voiceFS embed.FS

// Prompts biểu thị tập hợp prompt nhúng.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // Mẫu hợp đồng, chứa placeholder {{VOICE}}; bản cuối được BuildWriterPrompt lắp ráp
	Editor           string
	ImportSegment    string // Cắt phân ngữ nghĩa: nhận diện ranh giới chương/tập/văn bản phụ
	ImportAnalyze    string // Trích xuất sự thật từng chương theo lô liên tục
	ImportSynthesize string // Tổng hợp phân tầng và chia phân đoạn/tập (Toàn thư BookSynthesis)
	ImportRange      string // Tóm tắt khoảng liên tục giai đoạn Map của sách dài (RangeDigest)
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Prompt phán quyết của Trọng tài (LLM-as-function, không có bọc simulation guidance).
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle biểu thị tập hợp tài nguyên tĩnh cần thiết để chạy.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // Tiêu chuẩn viết (lớp văn phong), đã lắp ráp theo ba lớp ghi đè; xem docs/voice-layer.md
}

// LoadOptions khai báo nguồn ghi đè của lớp văn phong. Thư mục rỗng = bỏ qua lớp đó (eval truyền giá trị 0 để lấy
// baseline xác định thuần tích hợp, không bị ô nhiễm bởi ghi đè trên máy người dùng).
//
// Ngữ nghĩa đường dẫn: BookStyleDir liên kết với thư mục sách (outputDir) chứ không phải cwd —— văn phong đi theo sách, đổi thư mục
// khôi phục cùng một sách vẫn tải cùng một văn phong. Chú ý khác với lớp rules (cấp độ dự án của rules liên kết với cwd).
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions cấu trúc nguồn ghi đè môi trường sản xuất dựa trên thư mục sách.
func DefaultLoadOptions(outputDir string) LoadOptions {
	var opts LoadOptions
	if outputDir != "" {
		opts.BookStyleDir = filepath.Join(outputDir, "style")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		opts.HomeStyleDir = filepath.Join(home, ".ainovel", "style")
	}
	return opts
}

// Load trả về tập hợp tài nguyên tương ứng với phong cách chỉ định. Tài sản văn phong (voice / anti-ai-tone / styles /
// style-references đề tài) ghi đè 3 lớp theo opts: tích hợp < toàn cục < sách này.
func Load(style string, opts LoadOptions) Bundle {
	return Bundle{
		References: loadReferences(style, opts),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(opts),
		Voice:      resolveAppendable(mustRead(voiceFS, "voice.md"), "voice.md", opts),
	}
}

// voicePlaceholder là điểm chèn tại chỗ của đoạn văn phong trong mẫu hợp đồng writer.
const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt là lối vào duy nhất lắp ráp prompt hệ thống của writer, dùng chung cho sản xuất / eval / test,
// đảm bảo hai nhánh A/B đi cùng một đường (bài học tiền lệ xem WithSimulationGuidance).
// writerPrompt là mẫu hợp đồng chứa placeholder (có thể đã mang hậu tố simulation guidance, placeholder nằm trong
// tiền tố, thay thế không bị ảnh hưởng); không nối thêm nếu style rỗng.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// OverrideVoice dùng raw thay thế toàn bộ đoạn văn phong đã lắp ráp (dùng cho eval làm voice A/B).
// variant và baseline vẫn đi qua cùng đường lắp ráp BuildWriterPrompt.
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

// resolveAppendable lắp ráp 3 lớp nối thêm ngữ nghĩa: giữ nguyên tích hợp, toàn cục/sách này nối thêm làm đoạn đánh dấu.
// Khi không có ghi đè trả về nguyên văn tích hợp (từng byte không đổi —— một trong các tiêu chuẩn nghiệm thu lớp văn phong).
// "Cái sau ưu tiên" là chỉ thị ưu tiên cho LLM chứ không phải bảo đảm bằng máy; ràng buộc cần máy móc bảo đảm thì đi theo lớp rules.
func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong toàn cục người dùng (yêu cầu dưới đây ưu tiên hơn mặc định dự án)\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong sách này (yêu cầu dưới đây ưu tiên hơn tất cả những thứ trên)\n\n" + s
	}
	return out
}

// readOverride đọc một file trong thư mục ghi đè; thư mục rỗng, file không tồn tại hoặc trắng đều trả về "".
func readOverride(dir, name string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// styleNameRe kiểm tra tên file style tự định nghĩa của người dùng (không gồm đuôi), từ chối ký tự đường dẫn.
var styleNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadReferences(style string, opts LoadOptions) tools.References {
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:      mustRead(referencesFS, "references/chapter-guide.md"),
		HookTechniques:    mustRead(referencesFS, "references/hook-techniques.md"),
		QualityChecklist:  mustRead(referencesFS, "references/quality-checklist.md"),
		OutlineTemplate:   mustRead(referencesFS, "references/outline-template.md"),
		CharacterTemplate: mustRead(referencesFS, "references/character-template.md"),
		ChapterTemplate:   mustRead(referencesFS, "references/chapter-template.md"),
		Consistency:       mustRead(referencesFS, "references/consistency.md"),
		ContentExpansion:  mustRead(referencesFS, "references/content-expansion.md"),
		DialogueWriting:   mustRead(referencesFS, "references/dialogue-writing.md"),
		LongformPlanning:  mustRead(referencesFS, "references/longform-planning.md"),
		Differentiation:   mustRead(referencesFS, "references/differentiation.md"),
		AntiAITone:        resolveAppendable(mustRead(referencesFS, "references/anti-ai-tone.md"), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
		// Tham khảo phong cách đề tài: thay thế toàn bộ file cùng tên (sách này > toàn cục); khi style tự định nghĩa không có tham khảo tích hợp
		// cho phép chỉ cung cấp bằng ghi đè, không lùi về default (tham chiếu sai còn tệ hơn không có).
		relPath := filepath.Join("genres", style, "style-references.md")
		for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
			if s := readOverride(dir, relPath); s != "" {
				refs.StyleReference = s
			}
		}
	}
	return refs
}

func loadPrompts() Prompts {
	return Prompts{
		ArchitectShort:   WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Writer:           WithSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Editor:           WithSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		ImportSegment:    mustRead(promptsFS, "prompts/import-segment.md"),
		ImportAnalyze:    mustRead(promptsFS, "prompts/import-analyze.md"),
		ImportSynthesize: mustRead(promptsFS, "prompts/import-synthesize.md"),
		ImportRange:      mustRead(promptsFS, "prompts/import-range.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
		RevisionAnalyze:  mustRead(promptsFS, "prompts/revision-analyze.md"),

		ArbiterPlanStart:    mustRead(promptsFS, "prompts/arbiter-plan-start.md"),
		ArbiterIntervention: mustRead(promptsFS, "prompts/arbiter-intervention.md"),
		ArbiterFailure:      mustRead(promptsFS, "prompts/arbiter-failure.md"),
	}
}

// WithSimulationGuidance nối thêm chỉ dẫn chân dung mô phỏng viết vào prompt cốt lõi. Xuất ra cho eval và các tình huống bên ngoài
// tái sử dụng khi ghi đè variant, đảm bảo prompt sau ghi đè tương đương với baseline mà Load tạo ra (cùng một đường dẫn bọc).
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

// OverridePrompt dùng raw ghi đè prompt vai trò tương ứng với file prompt chỉ định trong bundle, và đi cùng đường bọc WithSimulationGuidance
// giống hệt Load —— khi eval làm A/B chỉ cần gọi nó, không cần sao chép logic bọc,
// nếu không baseline mang hậu tố chân dung mô phỏng viết, variant không mang, A/B không tương đương. file là tên file prompt.
// Chú ý: khi ghi đè writer.md thì raw phải mang theo placeholder {{VOICE}} (ngữ nghĩa mẫu hợp đồng); nếu chỉ muốn A/B văn phong
// thì dùng OverrideVoice.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("Không hỗ trợ ghi đè file prompt: %s (chỉ prompt cốt lõi mới có thể ghi đè)", file)
	}
	wrapped := WithSimulationGuidance(raw, role)
	switch file {
	case "architect-short.md":
		b.Prompts.ArchitectShort = wrapped
	case "architect-long.md":
		b.Prompts.ArchitectLong = wrapped
	case "writer.md":
		b.Prompts.Writer = wrapped
	case "editor.md":
		b.Prompts.Editor = wrapped
	}
	return nil
}

// promptRole ánh xạ tên file prompt cốt lõi với placeholder vai trò của simulation guidance.
var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## Chân dung mô phỏng viết

Khi planning_memory hoặc working_memory của novel_context tồn tại simulation_profile, phải coi nó là ràng buộc hướng mô phỏng viết của tác phẩm hiện tại. {{role}} nên đọc style, lexicon, plot_design, hook_design, pacing_density, reader_engagement và role_guidance trong đó.

Nguyên tắc sử dụng: tham khảo kết cấu, nhịp điệu, móc câu, cách nhả thông tin và thủ pháp thu hút người đọc; không sao chép nguyên văn câu, nhân vật, địa danh, thiết lập độc quyền hay phân đoạn cố định. Nếu simulation_profile xung đột với yêu cầu rõ ràng của người dùng, ưu tiên tuân theo yêu cầu của người dùng.`

// loadStyles liệt kê cài đặt trước phong cách tích hợp, sau đó theo thứ tự toàn cục → sách này ghi đè styles/*.md trong thư mục ghi đè
// (thay thế toàn bộ file cùng tên, tên file mới tức là phong cách mới; phong cách là âm thanh tổng thể, không gộp).
func loadStyles(opts LoadOptions) map[string]string {
	styles := make(map[string]string)
	entries, err := stylesFS.ReadDir("styles")
	if err != nil {
		return styles
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
	for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
		overlayStyles(styles, dir)
	}
	return styles
}

// overlayStyles chồng <dir>/styles/*.md vào tập hợp styles; tên file không hợp lệ thì bỏ qua và cảnh báo.
func overlayStyles(styles map[string]string, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(dir, "styles"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !styleNameRe.MatchString(name) {
			slog.Warn("Bỏ qua tên file phong cách không hợp lệ", "module", "assets", "dir", dir, "file", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "styles", e.Name()))
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
