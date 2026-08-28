package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureRulesDirAt xác minh备好thư mục + README.txt：ghi giải thích 、始终ghi đè为最新模板，
// và README.txt（非 .md）不会被扫描当成quy tắc 。
func TestEnsureRulesDirAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.txt")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("README.txt should be written: %v", err)
	}
	// 砍 YAML 后引导改讲"大白话 + 自动归一化"，不再教 front matter。
	if !strings.Contains(string(data), "chuẩn hóa") {
		t.Errorf("README.txt phải giải thích ngôn ngữ tự nhiên sẽ được chuẩn hóa, got %q", data)
	}
	if strings.Contains(string(data), "front matter") {
		t.Errorf("README.txt không nên dạy YAML front matter nữa, got %q", data)
	}

	// 始终ghi đè为最新模板：旧phiên bản 写的过时文案再次 ensure 时被刷新
	if err := os.WriteFile(readme, []byte("旧phiên bản 写的过时文案"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(readme); string(again) != homeRulesReadme {
		t.Errorf("README.txt should be refreshed to latest template, got %q", again)
	}

	// README.txt 不被当quy tắc （扫描只认 .md）
	if srcs := RawFileSources(LoadOptions{HomeRulesDir: dir}); len(srcs) != 0 {
		t.Errorf("README.txt must not be scanned as a rule, got %d sources", len(srcs))
	}
}

// TestDefaultProjectRulesDir 锁死项目级quy tắc thư mục镜像toàn cục ：./.ainovel/rules/。
func TestDefaultProjectRulesDir(t *testing.T) {
	proj := filepath.Join("/tmp", "demo-book")
	want := filepath.Join(proj, ".ainovel", "rules")
	if got := DefaultProjectRulesDir(proj); got != want {
		t.Errorf("DefaultProjectRulesDir=%q, want %q", got, want)
	}
	if got := DefaultProjectRulesDir(""); got != "" {
		t.Errorf("空项目根nên trả về空串，nhận được  %q", got)
	}
}

// TestDefaultOptions_ScansProjectRulesFromDotAinovel 端到端xác minh：
// DefaultOptions 把 cwd 下的 ./.ainovel/rules/ 接进 SourceProject nguồn 。
func TestDefaultOptions_ScansProjectRulesFromDotAinovel(t *testing.T) {
	proj := t.TempDir()
	t.Chdir(proj)
	rulesDir := filepath.Join(proj, ".ainovel", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "book.md"), []byte("# 本书偏好\n每chương  4000 chữ "), 0o644); err != nil {
		t.Fatal(err)
	}

	srcs := RawFileSources(DefaultOptions())
	var got *RawSource
	for i := range srcs {
		if srcs[i].Kind == SourceProject {
			got = &srcs[i]
		}
	}
	if got == nil {
		t.Fatalf("应从 ./.ainovel/rules/ 扫到项目quy tắc nguồn ，nhận được  %+v", srcs)
	}
	if !strings.Contains(got.Text, "本书偏好") {
		t.Errorf("项目quy tắc 原文应被原样trả về ，nhận được  %q", got.Text)
	}
	if got.Label != "project:book.md" {
		t.Errorf("nguồn 标签nên là project:book.md，nhận được  %q", got.Label)
	}
}