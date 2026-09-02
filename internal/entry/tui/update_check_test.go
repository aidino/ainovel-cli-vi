package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

func TestUpdateNotesPreviewSanitizesAndTruncates(t *testing.T) {
	notes := "\x1b[31m## Cập nhật quan trọng\x1b[0m\x00\n" + strings.Repeat("nội dung tiếp theo", 40)
	got := updateNotesPreview(notes)
	if got != "Cập nhật quan trọng" {
		t.Fatalf("tóm tắt chưa xử lý đúng Markdown/ANSI/ký tự điều khiển: %q", got)
	}

	got = updateNotesPreview(strings.Repeat("cập nhật", 100))
	if lipgloss.Width(got) > updateNotesPreviewWidth {
		t.Fatalf("chiều rộng tóm tắt vượt giới hạn: width=%d text=%q", lipgloss.Width(got), got)
	}
}

func TestFormatUpdateNoticeIncludesSafePreview(t *testing.T) {
	got := formatUpdateNotice(&buildversion.CheckResult{
		Latest: "v1.2.4",
		Notes:  "## Sửa lỗi khởi động",
	})
	for _, want := range []string{"v1.2.4", "Sửa lỗi khởi động", "ainovel-cli update"} {
		if !strings.Contains(got, want) {
			t.Fatalf("thông báo cập nhật %q thiếu %q", got, want)
		}
	}
}
