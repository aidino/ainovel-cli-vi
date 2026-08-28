package host

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// buildStoryStateSummary lắp ráp một đoạn tóm tắt ngắn gọn về hiện trạng câu chuyện, để trợ lý đồng sáng tạo theo giai đoạn hiểu "đã viết những gì".
// Tái sử dụng điểm truy cập store, chỉ lấy các sự kiện cấp cao cần thiết cho hướng quy hoạch (tiến độ / la bàn / tập gần nhất / nhân vật chính / chi tiết gieo mầm đang hoạt động);
// không kéo chính văn, không mớm toàn bộ JSON của novel_context - đồng sáng tạo là hội thoại, cái cần là cái nhìn tổng quan dễ đọc, không phải ngữ cảnh sáng tác.
// Bất kỳ mục nào thiếu đều bỏ qua (best-effort), trả về chuỗi rỗng nghĩa là chưa có tiến độ khả dụng.
func buildStoryStateSummary(s *store.Store) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	var warnings []string
	warn := func(scope string, err error) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s đọc thất bại: %v", scope, err))
		}
	}

	if book, err := s.Book.Load(); book != nil {
		fmt.Fprintf(&b, "- Tên sách: 《%s》\n", book.Title)
	} else {
		warn("book", err)
	}

	if progress, err := s.Progress.Load(); progress != nil {
		fmt.Fprintf(&b, "- Tiến độ: đã hoàn thành %d chương", len(progress.CompletedChapters))
		if progress.Layered {
			outline, outlineErr := s.Outline.LoadOutline()
			if outlineErr != nil {
				warn("outline", outlineErr)
			} else if len(outline) > 0 {
				fmt.Fprintf(&b, " / hiện đã chi tiết hóa %d chương (sau này quy hoạch động theo arc)", len(outline))
			}
		} else if progress.TotalChapters > 0 {
			fmt.Fprintf(&b, " / quy hoạch %d chương", progress.TotalChapters)
		}
		fmt.Fprintf(&b, ", khoảng %d chữ, chương tiếp theo là chương %d\n", progress.TotalWordCount, progress.NextChapter())
		if progress.Layered && progress.CurrentVolume > 0 {
			fmt.Fprintf(&b, "- Vị trí hiện tại: Tập %d Arc %d\n", progress.CurrentVolume, progress.CurrentArc)
		}
	} else {
		warn("progress", err)
	}

	if compass, err := s.Outline.LoadCompass(); compass != nil {
		if dir := strings.TrimSpace(compass.EndingDirection); dir != "" {
			fmt.Fprintf(&b, "- Hướng đi chung cuộc: %s\n", dir)
		}
		if compass.EstimatedScale != "" {
			fmt.Fprintf(&b, "- Quy mô ước tính: %s\n", compass.EstimatedScale)
		}
		if len(compass.OpenThreads) > 0 {
			fmt.Fprintf(&b, "- Tuyến dài đang hoạt động: %s\n", strings.Join(compass.OpenThreads, "；"))
		}
	} else {
		warn("story_compass", err)
	}

	// Tóm tắt tập gần nhất, để trợ lý biết câu chuyện vừa đi đến đâu
	if vols, err := s.Summaries.LoadAllVolumeSummaries(); len(vols) > 0 {
		last := vols[len(vols)-1]
		fmt.Fprintf(&b, "- Gần đây 《%s》: %s\n", last.Title, truncate(last.Summary, 200))
	} else {
		warn("volume_summaries", err)
	}

	// Nhân vật chính (core/important), tối đa 8 người
	if chars, err := s.Characters.Load(); len(chars) > 0 {
		var names []string
		for _, c := range chars {
			if c.Tier == "secondary" || c.Tier == "decorative" {
				continue
			}
			line := c.Name
			if role := strings.TrimSpace(c.Role); role != "" {
				line += "（" + role + "）"
			}
			names = append(names, line)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "- Nhân vật chính: %s\n", strings.Join(names, "、"))
		}
	} else {
		warn("characters", err)
	}

	// Chi tiết gieo mầm chưa thu, tối đa 6 cái
	if fs, err := s.World.LoadActiveForeshadow(); len(fs) > 0 {
		var items []string
		for _, f := range fs {
			items = append(items, truncate(f.Description, 40))
			if len(items) >= 6 {
				break
			}
		}
		fmt.Fprintf(&b, "- Chi tiết gieo mầm chưa thu: %s\n", strings.Join(items, "；"))
	} else {
		warn("foreshadow", err)
	}

	if len(warnings) > 0 {
		fmt.Fprintf(&b, "- Cảnh báo dữ liệu: %s\n", strings.Join(warnings, "；"))
	}

	return strings.TrimSpace(b.String())
}

// stageSystemPrompt lắp ráp hệ thống nhắc nhở hoàn chỉnh cho đồng sáng tạo theo giai đoạn: prompt giai đoạn + tóm tắt trạng thái câu chuyện hiện tại.
// Tóm tắt được treo ở cuối như phụ lục dữ liệu (ngăn cách bằng dòng gạch ngang và quy phạm định dạng), hô ứng với chỉ dẫn "tiến độ xem bên dưới" trong prompt.
func stageSystemPrompt(s *store.Store) string {
	prompt := stageCoCreateSystemPrompt
	if summary := buildStoryStateSummary(s); summary != "" {
		prompt += "\n\n---\n## Trạng thái câu chuyện hiện tại\n(Dưới đây là tóm tắt khách quan của nội dung đã viết, để bạn tham khảo khi quy hoạch phần tiếp theo, đừng chép y nguyên vào <draft>)\n" + summary
	}
	return prompt
}
