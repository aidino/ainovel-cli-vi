package sim

import (
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func textList(description string) map[string]any {
	return schema.Array(description, schema.String(description))
}

var sourceReportContract = llmcontract.Contract{
	Name:        "simulation_source_report",
	Description: "Trích xuất phương pháp sáng tác có thể tái sử dụng và không sao chép nguyên văn từ một bài ngữ liệu",
	Schema: schema.Object(
		schema.Property("title", llmcontract.Nullable(schema.String("Tiêu đề tùy chọn; null khi không thể xác nhận"))).Required(),
		schema.Property("summary", schema.String("Khái quát giá trị cách viết của văn bản mẫu")).Required(),
		schema.Property("style_observations", textList("Góc nhìn trần thuật, cấu trúc câu và quan sát kết cấu miêu tả")).Required(),
		schema.Property("common_words", textList("Từ tần suất cao, hình ảnh và thể loại từ chuyển cảnh")).Required(),
		schema.Property("plot_patterns", textList("Mô hình đẩy tiến cốt truyện, bước ngoặt và leo thang xung đột")).Required(),
		schema.Property("hook_patterns", textList("Mô hình mở đầu, cuối chương và móc chênh lệch thông tin")).Required(),
		schema.Property("pacing_notes", textList("Mật độ cảnh và nhịp độ giải phóng thông tin")).Required(),
		schema.Property("reader_appeal", textList("Phương pháp thu hút người đọc tiếp tục đọc")).Required(),
		schema.Property("reusable_techniques", textList("Kỹ thuật cấu trúc có thể học hỏi")).Required(),
		schema.Property("warnings", textList("Rủi ro sao chép và áp dụng máy móc cần tránh")).Required(),
	),
}

var synthesisContract = llmcontract.Contract{
	Name:        "simulation_synthesis",
	Description: "Tổng hợp hình mẫu sẵn có và báo cáo ngữ liệu thành hình mẫu phương pháp mô phỏng có thể thực thi",
	Schema: schema.Object(
		schema.Property("style", schema.Object(
			schema.Property("narrative_voice", textList("Ngôi trần thuật, khoảng cách và kiểm soát thông tin")).Required(),
			schema.Property("sentence_rhythm", textList("Nhịp độ cấu trúc câu")).Required(),
			schema.Property("prose_texture", textList("Chất cảm miêu tả")).Required(),
			schema.Property("perspective", textList("Quy tắc góc nhìn")).Required(),
			schema.Property("mood", textList("Tông điệu cảm xúc")).Required(),
			schema.Property("do_not_copy", textList("Nội dung cấm sao chép")).Required(),
		)).Required(),
		schema.Property("lexicon", schema.Object(
			schema.Property("common_words", textList("Thể loại từ thường dùng")).Required(),
			schema.Property("emotion_words", textList("Thể loại từ cảm xúc")).Required(),
			schema.Property("scene_words", textList("Thể loại từ cảnh vật")).Required(),
			schema.Property("transition_words", textList("Thể loại từ chuyển cảnh")).Required(),
			schema.Property("signature_phrases", textList("Đặc điểm giọng điệu sau khi trừu tượng hóa, không gồm câu gốc")).Required(),
		)).Required(),
		schema.Property("plot_design", schema.Object(
			schema.Property("opening_patterns", textList("Cách mở đầu")).Required(),
			schema.Property("escalation_patterns", textList("Cách leo thang xung đột")).Required(),
			schema.Property("turning_point_patterns", textList("Thiết kế bước ngoặt")).Required(),
			schema.Property("payoff_patterns", textList("Cách thu hồi và thực hiện")).Required(),
		)).Required(),
		schema.Property("hook_design", schema.Object(
			schema.Property("hook_types", textList("Loại móc")).Required(),
			schema.Property("placement", textList("Vị trí móc")).Required(),
			schema.Property("cliffhanger_patterns", textList("Cách dừng lửng hồi hộp")).Required(),
			schema.Property("payoff_rules", textList("Quy tắc thực hiện móc")).Required(),
		)).Required(),
		schema.Property("pacing_density", schema.Object(
			schema.Property("scene_density", textList("Mật độ thông tin một cảnh")).Required(),
			schema.Property("information_release", textList("Nhịp độ giải phóng thông tin")).Required(),
			schema.Property("dialogue_action_ratio", textList("Tỷ lệ thoại, hành động và tâm lý")).Required(),
			schema.Property("compression_rules", textList("Quy tắc triển khai và nén nội dung")).Required(),
		)).Required(),
		schema.Property("reader_engagement", schema.Object(
			schema.Property("methods", textList("Phương pháp thu hút người đọc")).Required(),
			schema.Property("emotional_drivers", textList("Động lực cảm xúc")).Required(),
			schema.Property("progression_rewards", textList("Phần thưởng tiến triển theo giai đoạn")).Required(),
			schema.Property("anti_patterns", textList("Phản mô hình làm suy yếu sự thu hút")).Required(),
		)).Required(),
		schema.Property("role_guidance", schema.Object(
			schema.Property("architect", textList("Quy tắc Architect sử dụng hình mẫu")).Required(),
			schema.Property("writer", textList("Quy tắc Writer học hỏi nhưng không sao chép")).Required(),
			schema.Property("editor", textList("Quy tắc Editor kiểm tra hướng và rủi ro vi phạm")).Required(),
		)).Required(),
	),
}
