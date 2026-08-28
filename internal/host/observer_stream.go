package host

import (
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// handleSubagentDelta phân luồng văn bản của subagent và tham số gọi công cụ:
// - DeltaText xuất trực tiếp dưới dạng markdown
// - DeltaToolCall chỉ trích xuất các trường xuất ra cho các công cụ có nội dung dài đã biết (như draft_chapter.content); JSON tham số của các công cụ khác sẽ bị vứt bỏ toàn bộ
func (o *observer) handleSubagentDelta(p *agentcore.ProgressPayload) {
	if p.DeltaKind != agentcore.DeltaToolCall {
		o.emitStreamDelta(p.Delta, false)
		return
	}
	if p.Tool == "" {
		return // Tên công cụ chưa sẵn sàng, đợi delta tiếp theo thử lại
	}

	// Khi nhận dạng luồng stream được tên công cụ, phát trước sự kiện TOOL đang diễn ra, để spinner bao phủ toàn bộ khoảng thời gian LLM sinh ra
	// (nếu không, "đang diễn ra" của các công cụ như draft_chapter chỉ hiển thị trong vài chục mili giây Execute thực tế).
	// Khi ProgressToolStart thực sự đến, nhận ra toolStarts đã có ghi nhận, sẽ chỉ bổ sung summary.
	o.ensureSubagentToolStarted(p.Agent, p.Tool)
	o.updateToolCallSummaryFromDelta(p.Agent, p.Tool, p.Delta)

	cur, ok := o.streamExtractors[p.Agent]
	// Sau khi args của cùng một lần gọi công cụ đã đóng (trúng ngoặc } cấp cao nhất), vẫn có thể nhận được trailing delta:
	// một số provider (deepseek-v4-flash thực tế đo được) sẽ tách một lần args thành nhiều chunk,
	// chunk cuối cùng sau `}` còn theo sau bởi khoảng trắng hoặc ký tự lặp lại. Lúc này nếu xử lý theo "khớp tên công cụ +
	// Done thì tạo lại", extractor mới sẽ lại emit một lần ✻ header và coi token đuôi
	// là args mới để parse. Các delta này là phần đuôi dư thừa, vứt bỏ là được.
	if ok && cur.tool == p.Tool && cur.ext.Done() {
		return
	}
	// Tên công cụ đã thay đổi hoặc chưa từng được tạo: tạo mới.
	if !ok || cur.tool != p.Tool {
		ext := newToolExtractor(p.Tool)
		if ext == nil {
			delete(o.streamExtractors, p.Agent)
			return
		}
		cur = &agentExtractor{tool: p.Tool, ext: ext}
		o.streamExtractors[p.Agent] = cur
	}
	if emitted := cur.ext.Feed(p.Delta); emitted != "" {
		if !cur.emittedAny {
			cur.emittedAny = true
			// streamClear để ✻ header của extractor rơi vào điểm bắt đầu round mới, kết hợp
			// với HasPrefix("✻") của renderStreamContent để đi vào luồng highlight renderAgentBlock;
			// nếu dùng ensureStreamParagraphBreak chỉ chèn dòng trống không mở round, ✻ vẫn sẽ bị
			// đoạn thinking/chính văn phía trước bao bọc, rớt xuống renderChapterBlock và bị vẽ bằng màu mặc định.
			o.streamClear()
			// streamClear đã dọn sạch phòng thủ streamExtractors. cur hiện tại còn phải tiếp tục Feed
			// các delta tiếp theo của lệnh gọi công cụ này, phải đăng ký lại nó ngay lập tức; nếu không khi đoạn delta
			// tiếp theo đến sẽ tạo extractor mới, bắt đầu parse từ giữa args (vào psBeforeKey tại `{`
			// trong object lồng nhau), coi timeline_events.time / foreshadow_updates.id
			// v.v. thành trường cấp cao nhất, ✻ header sẽ xuất hiện lặp lại trên TUI.
			o.streamExtractors[p.Agent] = cur
		}
		o.emitStreamDelta(emitted, false)
	}
}

func (o *observer) emitStreamDelta(delta string, thinking bool) {
	if delta == "" {
		return
	}
	if thinking != o.streamThinking {
		o.emitD(utils.ThinkingSep)
		o.streamThinking = thinking
	}
	o.emitD(delta)
	o.streamHasContent = true
	o.streamLastByte = delta[len(delta)-1]
}

// ensureSubagentToolStarted khi nhận diện tool_call lần đầu trong luồng stream, đăng ký trước một lệnh gọi TOOL đang diễn ra cho agent đó,
// để spinner của luồng sự kiện bao phủ khoảng thời gian "LLM sinh ra tham số tool_call" này (thường chiếm 99% tổng thời gian gọi).
// args lúc này chưa đầy đủ, tạm dùng tên công cụ thuần túy làm summary; khi ProgressToolStart thực sự đến sẽ bổ sung summary có chứa tham số.
func (o *observer) ensureSubagentToolStarted(agent, tool string) {
	if agent == "" || tool == "" {
		return
	}
	if _, ok := o.toolStarts[agent]; ok {
		return // Đã có lệnh gọi đang diễn ra, idempotent
	}
	o.resetStreamArgLabel(agent, tool)
	id := nextEventID()
	o.toolStarts[agent] = &activeCall{
		id:      id,
		start:   time.Now(),
		summary: tool, // Dùng tên công cụ thuần trước, khi ProgressToolStart đến có thể cập nhật thành tool(chương N)
		depth:   1,
	}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "TOOL",
		Agent:    agent,
		Summary:  tool,
		Level:    "info",
		Depth:    1,
	})
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = tool
	})
	o.emitFallbackStreamHeader(tool)
}

func (o *observer) resetStreamArgLabel(agent, tool string) {
	key := streamArgKey(agent, tool)
	delete(o.streamArgPrefixes, key)
	delete(o.streamArgLabels, key)
}

// emitFallbackStreamHeader bổ sung một dòng tiêu đề ✻ vào panel luồng cho công cụ chưa cấu hình extractor.
// Cả hai luồng đều phải gọi để đảm bảo tính nhất quán:
//  1. ensureSubagentToolStarted —— subagent tool args luồng stream (DeltaToolCall)
//  2. handleToolUpdate ProgressToolStart —— subagent tool args không stream
//
// Thiếu bất kỳ luồng nào, tiêu đề công cụ của model stream và không stream sẽ hiển thị không nhất quán.
func (o *observer) emitFallbackStreamHeader(tool string) {
	if _, has := toolDisplays[tool]; has {
		return // Có extractor, header do extractor tự xuất ra
	}
	o.streamClear()
	o.emitStreamDelta(streamHeaderFallback(tool)+"\n", false)
}

// streamHeaderFallback tạo văn bản header stream cho công cụ chưa cấu hình extractor,
// để người dùng có thể thấy "đang gọi cái gì" ngay cả đối với các công cụ đọc nhẹ.
//
// Tiền tố "✻ " là đánh dấu "khối điều phối agent" theo quy ước — renderStreamContent của TUI khi thấy
// tiền tố này sẽ đi theo đường renderAgentBlock (icon + label highlight + dòng phân cách),
// nếu không sẽ rơi vào đường khối chính văn dùng màu mặc định của terminal, header trông như chính văn bình thường không nổi bật.
func streamHeaderFallback(tool string) string {
	return "✻ " + tool
}

// streamClear thông báo TUI bắt đầu một streamRound mới, đồng thời reset trạng thái liên quan đến ngắt đoạn.
// Về mặt logic, round mới là "stream rỗng", nếu không emit đầu tiên của extractor lần sau sẽ bù nhầm dòng trống dẫn đầu.
//
// streamThinking cũng phải được reset cùng: emitStreamDelta dùng streamThinking để theo dõi xuyên suốt
// xem đoạn trước có phải là thinking không. Trong round mới chưa xuất nội dung nào, lần emit(thinking=false) tiếp theo
// không nên chèn thêm ThinkingSep nữa. Nếu không fallback header (như ✻ đọc chương) sẽ bị \x02
// chiếm đầu trước, HasPrefix("✻") của renderStreamContent bị sai lệch, cả đoạn rơi vào đường chính văn
// rồi bị ThinkingSep cắt thành đoạn thinking, màu của title bị vẽ thành màu thinking.
func (o *observer) streamClear() {
	o.emitC()
	o.streamHasContent = false
	o.streamLastByte = 0
	o.streamThinking = false
	// ProgressToolEnd trước khi kết thúc subagent của vòng trước đã bị delete, dọn sạch phòng thủ ở đây.
	if len(o.streamExtractors) > 0 {
		o.streamExtractors = make(map[string]*agentExtractor)
	}
}
