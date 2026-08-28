package domain

// CastEntry là một bản ghi nhân vật phụ trong danh sách nhân vật phụ.
//
// Tách biệt khỏi Character (characters.json, hồ sơ cốt lõi do Architect bảo trì):
//   - CastEntry được tích lũy tự động bởi công cụ commit_chapter, ghi lại "các vai phụ có tên đã từng xuất hiện"
//   - Character được Architect thiết kế rõ ràng, ghi lại nhân vật chính và nhân vật phụ quan trọng với vòng cung nhân cách/đặc điểm/tier
//
// Khi trùng tên thì lấy Character làm chuẩn (nhân vật cốt lõi không vào cast_ledger), tránh lặp lại.
type CastEntry struct {
	Name string `json:"name"`
	// Aliases hiện chưa có kênh ghi; để dành cho công cụ "người dùng steer gộp bí danh" trong tương lai
	// (ví dụ: khai báo 'Lý chưởng quỹ' và 'lão Lý' là cùng một người). MergeAppearances đã hỗ trợ tìm kiếm bí danh.
	Aliases          []string `json:"aliases,omitempty"`
	BriefRole        string   `json:"brief_role,omitempty"` // Định vị một câu (lần đầu xuất hiện do Writer điền, có thể bổ sung sau; không bị ghi đè)
	FirstSeenChapter int      `json:"first_seen_chapter"`
	LastSeenChapter  int      `json:"last_seen_chapter"`
	// AppearanceCount dẫn xuất từ len(AppearanceChapters), khi merge giữ đồng bộ.
	// Giữ trường tường minh để tiện cho UI/JSON đọc trực tiếp, không cần tính lại mỗi lần.
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
	// Promoted đánh dấu mục này đã thăng cấp lên characters.json. RecentActive sẽ bỏ qua các mục này,
	// tránh thu hồi lặp lại với hồ sơ cốt lõi. Kênh thăng cấp hiện tại chưa thực hiện, trường này là hook dự trữ.
	Promoted bool `json:"promoted,omitempty"`
}

// CastIntro là khai báo tóm tắt của Writer khi commit_chapter đối với nhân vật mới xuất hiện.
// Chỉ được sử dụng khi tên này xuất hiện lần đầu hoặc BriefRole trong ledger vẫn trống.
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}
