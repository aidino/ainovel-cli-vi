package domain

import (
	"fmt"
	"strings"
)

// BookMetadata là thông tin tác phẩm hướng đến độc giả và nhà xuất bản.
// Thiết lập sáng tác thuộc về Foundation, tiến độ chạy thuộc về Progress, cả hai đều không chứa dữ liệu này.
type BookMetadata struct {
	Title    string `json:"title"`
	Synopsis string `json:"synopsis"`
}

// Normalized trả về giá trị chuẩn hóa có thể cố định và có thể so sánh.
func (b BookMetadata) Normalized() BookMetadata {
	b.Title = strings.TrimSpace(b.Title)
	b.Synopsis = strings.TrimSpace(b.Synopsis)
	return b
}

// Validate kiểm tra các trường bắt buộc của thông tin tác phẩm.
func (b BookMetadata) Validate() error {
	b = b.Normalized()
	if b.Title == "" {
		return fmt.Errorf("tên sách là bắt buộc")
	}
	if b.Synopsis == "" {
		return fmt.Errorf("tóm tắt sách là bắt buộc")
	}
	return nil
}

// OutlineEntry mục đại cương, tương ứng một chương.
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character hồ sơ nhân vật.
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // Bí danh/danh hiệu/biệt danh (ví dụ: "Thiếu niên rác rưởi", "Viêm ca")
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative (mặc định important)
}

// VolumeOutline đại cương cấp tập (chế độ trường thiên phân tầng).
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"`           // Xung đột/chủ đề cốt lõi của tập này
	Final bool         `json:"final,omitempty"` // Tập thu quan: Toàn bộ sách thu thập ở tập này (Architect tuyên bố khi append_volume)
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded đánh giá xem tập đã được triển khai chưa (có cấu trúc cấp arc).
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// FinaleVolume trả về số thứ tự tập thu quan đã tuyên bố, chưa tuyên bố trả về 0.
// Sự thật thu quan = "Tập cuối có cờ Final": Sau khi tuyên bố, toàn bộ sách chuyển sang trạng thái thu thập (quy hoạch thu dây, cấu trúc tập cuối viết xong là hoàn kết); nếu sau đó lại thêm tập mới không có cờ, tập mới trở thành tập cuối, trạng thái thu thập tự nhiên được giải trừ——
// Do đó không cần công cụ hoàn tác, trạng thái luôn có thể suy luận từ dữ liệu đại cương.
func FinaleVolume(volumes []VolumeOutline) int {
	if n := len(volumes); n > 0 && volumes[n-1].Final {
		return volumes[n-1].Index
	}
	return 0
}

// StoryCompass la bàn hướng kết cục, thay thế danh sách tập bộ khung cố định.
// Architect có thể cập nhật ở mỗi biên tập, cho phép hướng câu chuyện tiến hóa theo sáng tác.
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // Hướng kết cục (mô tả tính chủ đề)
	OpenThreads     []string `json:"open_threads,omitempty"`    // Tuyến dài đang hoạt động (cần thu thập mới kết thúc được)
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // Quy mô mờ (ví dụ: "Dự kiến 4-6 tập")
	LastUpdated     int      `json:"last_updated,omitempty"`    // Số chương đã hoàn thành lúc cập nhật
}

// ArcOutline đại cương cấp arc.
type ArcOutline struct {
	Index             int            `json:"index"` // Số thứ tự arc trong tập
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // Mục tiêu arc (khởi thừa chuyển hợp)
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // Số chương dự kiến của arc bộ khung (thanh toán về 0 sau khi triển khai)
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded đánh giá xem arc đã triển khai chưa (có chương chi tiết).
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// ArcExpansion là quy hoạch hoàn chỉnh của Architect đối với một arc chưa viết ở biên cấu trúc.
// Title/Goal không phải là bản sao cơ học của bộ khung: model có thể dựa trên chính văn đã hoàn thành để sửa đổi kế hoạch chưa diễn ra.
type ArcExpansion struct {
	Title    string         `json:"title"`
	Goal     string         `json:"goal"`
	Chapters []OutlineEntry `json:"chapters"`
}

// EstimatedChapterCapacity tính toán ước tính dung lượng bên trong của đại cương phân tầng: arc đã triển khai tính theo số chương thực tế, arc bộ khung tính theo EstimatedChapters. Nó chỉ dùng cho chiến lược ngữ cảnh, không phải tổng số chương toàn bộ sách; các chương thực sự đã chi tiết hóa và có thể viết luôn đến từ FlattenOutline, cấm tiết lộ giá trị này cho người dùng hoặc model.
func EstimatedChapterCapacity(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline triển khai đại cương phân tầng thành danh sách chương phẳng, giữ cho số chương toàn cục liên tục.
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule mục quy tắc thế giới quan.
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // Mô tả quy tắc
	Boundary string `json:"boundary"` // Ranh giới không thể vi phạm
}
