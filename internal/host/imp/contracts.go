package imp

import (
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func nullableString(description string) map[string]any {
	return llmcontract.Nullable(schema.String(description))
}

func stringList(description string) map[string]any {
	return schema.Array(description, schema.String(description))
}

var segmentContract = llmcontract.Contract{
	Name:        "import_segment",
	Description: "Nhận dạng ranh giới chương, tập truyện và văn bản phụ trong văn bản được nạp",
	Schema: schema.Object(
		schema.Property("boundaries", schema.Array("Ranh giới được sắp xếp theo thứ tự nguyên bản", schema.Object(
			schema.Property("unit_id", schema.String("unit id trong khoảng owned")).Required(),
			schema.Property("anchor", nullableString("Đoạn định vị nguyên bản khi có nhiều ranh giới trong cùng một unit; nếu không là null")).Required(),
			schema.Property("kind", schema.Enum("Loại ranh giới", kindChapter, kindGroup, kindFrontMatter, kindBackMatter)).Required(),
			schema.Property("title", nullableString("Nguyên bản tiêu đề; khi không có tiêu đề là null")).Required(),
			schema.Property("uncertain", schema.Bool("Có cần người dùng xác nhận hay không")).Required(),
			schema.Property("reason", nullableString("Lý do không chắc chắn; khi không cần giải thích là null")).Required(),
		))).Required(),
	),
}

var analysisContract = llmcontract.Contract{
	Name:        "import_chapter_analysis",
	Description: "Trích xuất sự thật câu chuyện có thể truy nguyên của các chương liên tục",
	Schema: schema.Object(
		schema.Property("chapters", schema.Array("Sự thật từng chương nhất quán với thứ tự số chương nhập vào", chapterFactsSchema())).Required(),
	),
}

func chapterFactsSchema() map[string]any {
	characterEvidence := schema.Object(
		schema.Property("chapter", schema.Int("Chương chứa chứng cứ")).Required(),
		schema.Property("name", schema.String("Tên nhân vật")).Required(),
		schema.Property("note", nullableString("Sự thật nhân vật; không có thì là null")).Required(),
	)
	worldEvidence := schema.Object(
		schema.Property("chapter", schema.Int("Chương chứa chứng cứ")).Required(),
		schema.Property("category", nullableString("Thể loại sự thật thế giới; khi không thể phân loại thì là null")).Required(),
		schema.Property("fact", schema.String("Sự thật thế giới được tiết lộ rõ ràng trong chính văn")).Required(),
	)
	timelineEvent := schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("time", schema.String("Thời gian trong câu chuyện")).Required(),
		schema.Property("event", schema.String("Sự kiện")).Required(),
		schema.Property("characters", stringList("Nhân vật liên quan")).Required(),
	)
	foreshadow := schema.Object(
		schema.Property("id", schema.String("Tái sử dụng ID chi tiết gieo mầm trong ledger")).Required(),
		schema.Property("action", schema.Enum("Hành động của chi tiết gieo mầm", "plant", "advance", "resolve")).Required(),
		schema.Property("description", nullableString("Mô tả chi tiết gieo mầm lúc plant; trường hợp khác có thể là null")).Required(),
	)
	relationship := schema.Object(
		schema.Property("character_a", schema.String("Nhân vật A")).Required(),
		schema.Property("character_b", schema.String("Nhân vật B")).Required(),
		schema.Property("relation", schema.String("Mối quan hệ thay đổi")).Required(),
		schema.Property("chapter", schema.Int("Số chương")).Required(),
	)
	stateChange := schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("entity", schema.String("Nhân vật hoặc thực thể")).Required(),
		schema.Property("field", schema.String("Thuộc tính thay đổi")).Required(),
		schema.Property("old_value", nullableString("Trạng thái trước khi thay đổi; lần đầu xuất hiện là null")).Required(),
		schema.Property("new_value", schema.String("Trạng thái sau khi thay đổi")).Required(),
		schema.Property("reason", nullableString("Lý do thay đổi; khi chính văn không giải thích thì là null")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("title", schema.String("Tiêu đề chương")).Required(),
		schema.Property("summary", schema.String("Tóm tắt chương này")).Required(),
		schema.Property("key_events", stringList("Sự kiện then chốt")).Required(),
		schema.Property("core_event", schema.String("Sự việc then chốt nhất chương này")).Required(),
		schema.Property("hook", nullableString("Móc cuối chương; không có thì là null")).Required(),
		schema.Property("scenes", stringList("Trình tự bối cảnh")).Required(),
		schema.Property("characters", stringList("Nhân vật xuất hiện")).Required(),
		schema.Property("character_evidence", schema.Array("Chứng cứ nhân vật", characterEvidence)).Required(),
		schema.Property("world_evidence", schema.Array("Chứng cứ sự thật thế giới", worldEvidence)).Required(),
		schema.Property("timeline_events", schema.Array("Sự kiện dòng thời gian", timelineEvent)).Required(),
		schema.Property("foreshadow_updates", schema.Array("Gia tăng chi tiết gieo mầm", foreshadow)).Required(),
		schema.Property("relationship_changes", schema.Array("Mối quan hệ thay đổi", relationship)).Required(),
		schema.Property("state_changes", schema.Array("Thay đổi trạng thái", stateChange)).Required(),
		schema.Property("hook_type", schema.Enum("Loại móc cuối chương", domain.HookTypes()...)).Required(),
		schema.Property("dominant_strand", schema.Enum("Đường dẫn tự sự chủ đạo", domain.DominantStrands()...)).Required(),
	)
}

var rangeContract = llmcontract.Contract{
	Name:        "import_range_digest",
	Description: "Tóm tắt cốt truyện và sự thật của một khoảng chương liên tục",
	Schema: schema.Object(
		schema.Property("start_chapter", schema.Int("Chương đầu của khoảng")).Required(),
		schema.Property("end_chapter", schema.Int("Chương cuối của khoảng")).Required(),
		schema.Property("plot", schema.String("Tiến triển cốt truyện chính qua các chương")).Required(),
		schema.Property("characters", stringList("Những nhân vật có tiến triển đáng kể")).Required(),
		schema.Property("world_facts", stringList("Những sự thật thế giới đã được thiết lập")).Required(),
		schema.Property("opened_threads", stringList("Đường dây mới được mở trong khoảng này")).Required(),
		schema.Property("resolved_threads", stringList("Đường dây đã được giải quyết trong khoảng này")).Required(),
	),
}

var synthesisContract = llmcontract.Contract{
	Name:        "import_book_synthesis",
	Description: "Tổng hợp sự thật toàn bộ sách và đưa ra khoảng tập arc liên tục hoàn chỉnh",
	Schema: schema.Object(
		schema.Property("title", nullableString("Tên sách chính thức trong chính văn; khi không thể xác nhận thì là null")).Required(),
		schema.Property("synopsis", schema.String("Tóm tắt tiểu thuyết không spoil dành cho độc giả")).Required(),
		schema.Property("premise", schema.String("Mô tả Markdown của tiền đề câu chuyện")).Required(),
		schema.Property("characters", schema.Array("Nhân vật chính", schema.Object(
			schema.Property("name", schema.String("Tên nhân vật")).Required(),
			schema.Property("aliases", stringList("Bí danh và danh hiệu")).Required(),
			schema.Property("role", schema.String("Vai trò tự sự")).Required(),
			schema.Property("description", schema.String("Mô tả nhân vật")).Required(),
			schema.Property("arc", schema.String("Arc nhân vật")).Required(),
			schema.Property("traits", stringList("Đặc điểm nhân vật")).Required(),
			schema.Property("tier", nullableString("Cấp bậc nhân vật; khi không thể đánh giá thì là null")).Required(),
		))).Required(),
		schema.Property("world_rules", schema.Array("Quy tắc thế giới đã được thiết lập trong chính văn", schema.Object(
			schema.Property("category", schema.String("Hạng mục quy tắc")).Required(),
			schema.Property("rule", schema.String("Mô tả quy tắc")).Required(),
			schema.Property("boundary", schema.String("Ranh giới không thể vi phạm")).Required(),
		))).Required(),
		schema.Property("structure", schema.Array("Khoảng chương liên tục của tập và arc", schema.Object(
			schema.Property("title", schema.String("Tiêu đề tập")).Required(),
			schema.Property("theme", schema.String("Xung đột cốt lõi hoặc chủ đề tập")).Required(),
			schema.Property("arcs", schema.Array("Story arc trong tập", schema.Object(
				schema.Property("title", schema.String("Tiêu đề arc")).Required(),
				schema.Property("goal", schema.String("Mục tiêu arc")).Required(),
				schema.Property("start_chapter", schema.Int("Chương bắt đầu")).Required(),
				schema.Property("end_chapter", schema.Int("Chương kết thúc")).Required(),
			))).Required(),
		))).Required(),
		schema.Property("compass", schema.Object(
			schema.Property("ending_direction", schema.String("Hướng đi kết thúc")).Required(),
			schema.Property("open_threads", stringList("Dây chuyền dài vẫn chưa kết thúc")).Required(),
			schema.Property("estimated_scale", nullableString("Quy mô mờ nhạt; khi không thể đánh giá thì là null")).Required(),
			schema.Property("last_updated", llmcontract.Nullable(schema.Int("Số chương mới nhất dựa vào; null khi không cần điền"))).Required(),
		)).Required(),
		schema.Property("planning_tier", schema.Enum("Cấp bậc quy hoạch", "short", "mid", "long")).Required(),
		schema.Property("story_status", schema.Enum("Câu chuyện đã hoàn kết hay chưa", storyOpen, storyClosed, storyUncertain)).Required(),
		schema.Property("status_reason", nullableString("Lý do phán đoán trạng thái")).Required(),
	),
}
