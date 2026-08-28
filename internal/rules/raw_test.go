package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRawFileSources_ScansAllMarkdownInOrder xác minhthư mục下多个 .md 都被扫到，
// 按tệp名từ điển 序trả về ；非 .md tệp被bỏ qua ；原文原样giữ lại 。
func TestRawFileSources_ScansAllMarkdownInOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.md", "# B 偏好")
	write("a.md", "# A 偏好")
	write("ignore.txt", "not a rule")
	write("empty.md", "   ") // trống白tệp应bỏ qua 

	srcs := RawFileSources(LoadOptions{HomeRulesDir: dir})
	if len(srcs) != 2 {
		t.Fatalf("应扫到 a.md / b.md 两个nguồn （.txt 与空白bỏ qua ），nhận được  %d：%+v", len(srcs), srcs)
	}
	// từ điển 序：a 在前 b 在后
	if srcs[0].Label != "global:a.md" || srcs[1].Label != "global:b.md" {
		t.Errorf("应按từ điển 序trả về ，nhận được  %q, %q", srcs[0].Label, srcs[1].Label)
	}
	for _, s := range srcs {
		if s.Kind != SourceGlobal {
			t.Errorf("HomeRulesDir nguồn nên là SourceGlobal，nhận được  %v", s.Kind)
		}
	}
}

// TestRawFileSources_DirMissing xác minhthư mụckhông tồn tại时静默bỏ qua （trả về  nil）。
func TestRawFileSources_DirMissing(t *testing.T) {
	srcs := RawFileSources(LoadOptions{HomeRulesDir: filepath.Join(t.TempDir(), "nope")})
	if len(srcs) != 0 {
		t.Errorf("thiếu thư mụcnên trả về 0 nguồn ，nhận được  %d", len(srcs))
	}
	if len(RawFileSources(LoadOptions{})) != 0 {
		t.Error("空 LoadOptions nên trả về 0 nguồn ")
	}
}

// TestRawFileSources_IgnoresHiddenAndSubdirs 锁死：ẩn /biên tập viên 器临时tệp（. 开头）被bỏ qua 、
// 子thư mục不递归——防止脏tệp二进制nội dung 当偏好chính văn注入 LLM。
func TestRawFileSources_IgnoresHiddenAndSubdirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("# real"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dirty := range []string{"._real.md", ".#lock.md", ".hidden.md"} {
		if err := os.WriteFile(filepath.Join(dir, dirty), []byte("\x00binary garbage\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.md"), []byte("# nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcs := RawFileSources(LoadOptions{HomeRulesDir: dir})
	if len(srcs) != 1 || srcs[0].Label != "global:real.md" {
		t.Fatalf("应只扫到 real.md（ẩn /脏/子thư mụcbỏ qua ），nhận được  %+v", srcs)
	}
}

// TestRawFileSources_GlobalThenProject xác minhtoàn cục nguồn 在前、项目nguồn 在后。
func TestRawFileSources_GlobalThenProject(t *testing.T) {
	base := t.TempDir()
	global := filepath.Join(base, "global")
	project := filepath.Join(base, "project")
	for _, d := range []string{global, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(global, "g.md"), []byte("# toàn cục "), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "p.md"), []byte("# 本书"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcs := RawFileSources(LoadOptions{HomeRulesDir: global, ProjectRulesDir: project})
	if len(srcs) != 2 || srcs[0].Kind != SourceGlobal || srcs[1].Kind != SourceProject {
		t.Fatalf("应先toàn cục 后项目，nhận được  %+v", srcs)
	}
}
