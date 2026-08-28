package rules

import (
	"strings"
	"testing"
)

func TestBuildSnapshot_FieldOverridePrecedence(t *testing.T) {
	// 低→高：defaults 设 修仙，project ghi đè为 都市；高优先级胜出。
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{Genre: "修仙"}},
		{Source: "project:a.md", Structured: Structured{Genre: "都市"}},
	})
	if snap.Structured.Genre != "都市" {
		t.Fatalf("kỳ vọng project ghi đè defaults，nhận được  %q", snap.Structured.Genre)
	}
	if snap.Status != StatusReady {
		t.Fatalf("kỳ vọng ready，nhận được  %s", snap.Status)
	}
	if snap.Version != SnapshotVersion {
		t.Fatalf("version nên là %d，nhận được  %d", SnapshotVersion, snap.Version)
	}
}

func TestBuildSnapshot_EmptyAndZeroAreAbsent(t *testing.T) {
	// 归一化器吐占位：genre:""、空串元素——都phải 当thiếu ，不ghi đè低优先级真值。
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{
			Genre: "修仙",
		}},
		{Source: "startup_prompt", Structured: Structured{
			Genre:            "",                 // 占位空串 → 不ghi đè
			ForbiddenPhrases: []string{"", "  "}, // 全空 → 丢弃
		}},
	})
	if snap.Structured.Genre != "修仙" {
		t.Fatalf("空 genre 不应ghi đè，kỳ vọng 修仙，nhận được  %q", snap.Structured.Genre)
	}
	if len(snap.Structured.ForbiddenPhrases) != 0 {
		t.Fatalf("全空 forbidden_phrases 应被丢弃，nhận được  %v", snap.Structured.ForbiddenPhrases)
	}
}

func TestBuildSnapshot_PreferencesPrecedenceOrder(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "global:g.md", Preferences: "toàn cục 偏好"},
		{Source: "project:p.md", Preferences: "项目偏好"},
	})
	gi := strings.Index(snap.Preferences, "toàn cục 偏好")
	pi := strings.Index(snap.Preferences, "项目偏好")
	if gi < 0 || pi < 0 || gi > pi {
		t.Fatalf("preferences 应按优先级低→高拼接（项目在后），nhận được :\n%s", snap.Preferences)
	}
	if !strings.Contains(snap.Preferences, "## [global:g.md]") {
		t.Fatalf("preferences 应带nguồn tiêu đề ，nhận được :\n%s", snap.Preferences)
	}
}

func TestBuildSnapshot_FatigueWordsMergeByWord(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{FatigueWords: map[string]int{"竟然": 1, "仿佛": 2}}},
		{Source: "project:p.md", Structured: Structured{FatigueWords: map[string]int{"仿佛": 5}}},
	})
	if snap.Structured.FatigueWords["竟然"] != 1 {
		t.Fatalf("竟然 应giữ lại  defaults ngưỡng  1，nhận được  %d", snap.Structured.FatigueWords["竟然"])
	}
	if snap.Structured.FatigueWords["仿佛"] != 5 {
		t.Fatalf("仿佛 应被 project ghi đè为 5，nhận được  %d", snap.Structured.FatigueWords["仿佛"])
	}
}

func TestBuildSnapshot_DegradedPropagates(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{FatigueWords: map[string]int{"竟然": 1}}},
		{Source: "project:bad.md", Preferences: "原文giảm cấp ", Degraded: true},
	})
	if snap.Status != StatusDegraded {
		t.Fatalf("任一nguồn giảm cấp 则 status=degraded，nhận được  %s", snap.Status)
	}
	// giảm cấp nguồn 仍以 raw preferences 进入，不阻断；其它nguồn  structured 照常。
	if len(snap.Structured.FatigueWords) == 0 {
		t.Fatalf("giảm cấp 不应影响其它nguồn 的 structured")
	}
	if !strings.Contains(snap.Preferences, "原文giảm cấp ") {
		t.Fatalf("giảm cấp nguồn 应作为 raw preferences giữ lại ")
	}
}

func TestSystemDefaults_MatchesLegacyDefaultMD(t *testing.T) {
	d := SystemDefaults().Structured
	if len(d.ForbiddenPhrases) != 4 {
		t.Fatalf("默认禁语nên là 4 条，nhận được  %d", len(d.ForbiddenPhrases))
	}
	if len(d.FatigueWords) != 16 {
		t.Fatalf("默认疲劳từ nên là 16 条，nhận được  %d", len(d.FatigueWords))
	}
}
