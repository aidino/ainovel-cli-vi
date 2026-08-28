package domain

// ChapterPlan ý tưởng sáng tác chương, do Writer tự chủ tạo ra.
// Không còn bắt buộc chia scene, Agent tự quyết định cách tổ chức nội dung.
type ChapterPlan struct {
	Chapter    int             `json:"chapter"`
	Title      string          `json:"title"`
	Goal       string          `json:"goal"`
	Conflict   string          `json:"conflict"`
	Hook       string          `json:"hook"`
	EmotionArc string          `json:"emotion_arc,omitempty"`
	Notes      string          `json:"notes,omitempty"` // Ghi chú tự do của Agent
	Contract   ChapterContract `json:"contract,omitempty"`
}

// ChapterContract là khế ước nghiệm thu chương chia sẻ giữa Writer và Editor.
// Nó định nghĩa các mục đẩy tiến bắt buộc phải hoàn thành trong chương này, các mục cấm vượt rào cũng như các điểm cần chú ý khi đọc kiểm.
type ChapterContract struct {
	RequiredBeats    []string `json:"required_beats,omitempty"`    // Các mục đẩy tiến bắt buộc phải thực hiện trong chương này
	ForbiddenMoves   []string `json:"forbidden_moves,omitempty"`   // Các mục đẩy tiến rõ ràng không được xảy ra trong chương này
	ContinuityChecks []string `json:"continuity_checks,omitempty"` // Các điểm liên tục cần đặc biệt đối chiếu trong chương này
	EvaluationFocus  []string `json:"evaluation_focus,omitempty"`  // Các điểm Editor cần trọng điểm kiểm tra
	EmotionTarget    string   `json:"emotion_target,omitempty"`    // Tùy chọn: Cảm xúc chủ yếu mà chương này muốn người đọc cảm nhận được
	PayoffPoints     []string `json:"payoff_points,omitempty"`     // Tùy chọn: Điểm tình tiết/điểm quy đổi mà chương then chốt muốn đáp lại
	HookGoal         string   `json:"hook_goal,omitempty"`         // Tùy chọn: Dục vọng đọc tiếp mà móc cuối chương muốn thúc đẩy
}

// ChapterSummary tóm tắt chương, dùng cho cửa sổ ngữ cảnh của các chương sau.
type ChapterSummary struct {
	Chapter    int      `json:"chapter"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Characters []string `json:"characters"`
	KeyEvents  []string `json:"key_events"`
}

// ArcSummary tóm tắt cấp arc, do Editor tạo ra khi kết thúc arc.
type ArcSummary struct {
	Volume    int      `json:"volume"`
	Arc       int      `json:"arc"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// VolumeSummary tóm tắt cấp tập, tạo ra khi kết thúc tập.
type VolumeSummary struct {
	Volume    int      `json:"volume"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// CharacterSnapshot ảnh chụp trạng thái nhân vật, ghi lại lúc ở ranh giới arc.
type CharacterSnapshot struct {
	Volume     int    `json:"volume"`
	Arc        int    `json:"arc"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Power      string `json:"power,omitempty"`
	Motivation string `json:"motivation"`
	Relations  string `json:"relations,omitempty"`
}

// OutlineFeedback phản hồi của Writer đối với đại cương, tùy chọn khi nộp chương.
type OutlineFeedback struct {
	Deviation  string `json:"deviation"`  // Mô tả sai lệch
	Suggestion string `json:"suggestion"` // Gợi ý điều chỉnh
}

// WritingStyleRules quy tắc sáng tác đúc kết từ các chương đã viết, do Editor tạo ra ở ranh giới arc.
// Thay thế đoạn văn bản gốc (style_anchors / voice_samples), dùng quy tắc thay vì bê nguyên văn bản gốc.
type WritingStyleRules struct {
	Volume    int              `json:"volume"`
	Arc       int              `json:"arc"`
	Prose     []string         `json:"prose"`      // 3-5 quy tắc phong cách trần thuật, mỗi quy tắc ≤50 từ
	Dialogue  []CharacterVoice `json:"dialogue"`   // Quy tắc phong cách đối thoại nhân vật
	Taboos    []string         `json:"taboos"`     // Danh sách cấm kỵ
	UpdatedAt string           `json:"updated_at"` // Timestamp ISO8601
}

// CharacterVoice quy tắc phong cách đối thoại của một nhân vật.
type CharacterVoice struct {
	Name  string   `json:"name"`
	Rules []string `json:"rules"` // 2-3 quy tắc đặc trưng ngôn ngữ, mỗi quy tắc ≤30 từ
}

// RelatedChapter chương liên quan đề xuất đọc lại.
type RelatedChapter struct {
	Chapter int    `json:"chapter"`
	Reason  string `json:"reason"`
}

// RecallItem là thông tin dài hạn được thu hồi có chọn lọc theo nhiệm vụ hiện tại.
// Nó không thay thế tạo tác chính thức, chỉ chịu trách nhiệm bơm lại lượng nhỏ thông tin lịch sử thực sự liên quan trong vòng hiện tại cho model.
type RecallItem struct {
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Chapter int    `json:"chapter,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// CommitResult là giá trị trả về có cấu trúc của công cụ commit_chapter.
// Chỉ chứa trường sự thật; "bước tiếp theo làm gì" do kênh Reminder tự tạo ra dựa trên Progress hiện tại.
type CommitResult struct {
	Chapter        int              `json:"chapter"`
	Committed      bool             `json:"committed"`
	WordCount      int              `json:"word_count"`
	NextChapter    int              `json:"next_chapter"`
	ReviewRequired bool             `json:"review_required"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	HookType       string           `json:"hook_type,omitempty"`
	DominantStrand string           `json:"dominant_strand,omitempty"`
	Feedback       *OutlineFeedback `json:"feedback,omitempty"`
	// Tín hiệu phân tầng trường thiên
	ArcEnd         bool `json:"arc_end,omitempty"`
	VolumeEnd      bool `json:"volume_end,omitempty"`
	Volume         int  `json:"volume,omitempty"`
	Arc            int  `json:"arc,omitempty"`
	NeedsExpansion bool `json:"needs_expansion,omitempty"`  // Arc tiếp theo là bộ khung, cần triển khai chương
	NeedsNewVolume bool `json:"needs_new_volume,omitempty"` // Cần Architect tạo tập tiếp theo
	NextVolume     int  `json:"next_volume,omitempty"`      // Số thứ tự arc/tập tiếp theo
	NextArc        int  `json:"next_arc,omitempty"`         // Số thứ tự arc tiếp theo
	// Sự thật trạng thái hoàn thành: sau lần commit này thì toàn bộ sách đã hoàn thành chưa
	BookComplete bool `json:"book_complete,omitempty"`
	// Ảnh chụp Progress.Flow hiện tại (writing / reviewing / rewriting / polishing)
	Flow string `json:"flow,omitempty"`
}
