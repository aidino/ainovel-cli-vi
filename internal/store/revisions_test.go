package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestInvalidateChapterAggregatesRemovesAffectedArtifacts(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "第一chương "}, {Chapter: 2, Title: "第二chương "},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Summary: "旧arc 摘要"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveVolumeSummary(domain.VolumeSummary{Volume: 1, Summary: "旧tập摘要"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.SaveSnapshots(1, 1, []domain.CharacterSnapshot{{Volume: 1, Arc: 1, Name: "林墨"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveStyleRules(domain.WritingStyleRules{Volume: 1, Arc: 1, Prose: []string{"旧quy tắc "}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "旧đọc kiểm "}); err != nil {
		t.Fatal(err)
	}

	if err := st.InvalidateChapterAggregates(1); err != nil {
		t.Fatal(err)
	}
	if sum, _ := st.Summaries.LoadArcSummary(1, 1); sum != nil {
		t.Fatalf("arc 摘要未失效: %+v", sum)
	}
	if sum, _ := st.Summaries.LoadVolumeSummary(1); sum != nil {
		t.Fatalf("tập摘要未失效: %+v", sum)
	}
	if snapshots, _ := st.Characters.LoadSnapshots(1, 1); len(snapshots) != 0 {
		t.Fatalf("nhân vật快照未失效: %+v", snapshots)
	}
	if rules, _ := st.World.LoadStyleRules(); rules != nil {
		t.Fatalf("sáng tác quy tắc 未失效: %+v", rules)
	}
	if review, _ := st.World.LoadReview(2); review != nil {
		t.Fatalf("đọc kiểm 未失效: %+v", review)
	}
}
