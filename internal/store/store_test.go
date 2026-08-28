package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestSummaryTitleCacheTracksSave(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "旧tiêu đề "}); err != nil {
		t.Fatal(err)
	}
	if title, err := st.Summaries.LoadSummaryTitle(1); err != nil || title != "旧tiêu đề " {
		t.Fatalf("首次đọc tiêu đề : title=%q err=%v", title, err)
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "新tiêu đề "}); err != nil {
		t.Fatal(err)
	}
	if title, err := st.Summaries.LoadSummaryTitle(1); err != nil || title != "新tiêu đề " {
		t.Fatalf("lưu 后缓存未cập nhật : title=%q err=%v", title, err)
	}
}

func TestProjectFormatDefaultsToLegacyAndPersistsUpgrade(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if version, err := st.LoadProjectFormatVersion(); err != nil || version != LegacyProjectFormatVersion {
		t.Fatalf("无phiên bản tệp应识别为旧định dạng : version=%d err=%v", version, err)
	}
	if err := st.SaveProjectFormatVersion(CurrentProjectFormatVersion); err != nil {
		t.Fatal(err)
	}
	if version, err := st.LoadProjectFormatVersion(); err != nil || version != CurrentProjectFormatVersion {
		t.Fatalf("định dạng phiên bản 未持久化: version=%d err=%v", version, err)
	}
}

func TestFoundationMissingReturnsReadError(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FoundationMissing(); err == nil {
		t.Fatal("bị hỏngđại cươngphải trả về đọc lỗi ，不能giảm cấp 成thiếu 项")
	}
}

func TestClearHandledSteerKeepsIntentWhenProgressReadFails(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.RunMeta.Init("default", "test", "model"); err != nil {
		t.Fatalf("RunMeta.Init: %v", err)
	}
	if err := st.RunMeta.SetPendingSteer("giữ lại 这条干预"); err != nil {
		t.Fatalf("SetPendingSteer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearHandledSteer(); err == nil {
		t.Fatal("corrupt progress should make ClearHandledSteer fail")
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatalf("RunMeta.Load: %v", err)
	}
	if meta == nil || meta.PendingSteer != "giữ lại 这条干预" {
		t.Fatalf("recovery intent was lost after partial clear: %+v", meta)
	}
}
