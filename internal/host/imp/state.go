package imp

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Action là hành động xác định tiếp theo được NextAction suy ra từ sự thật không gian làm việc.
// Trạng thái bền bỉ không ghi enum giai đoạn sẽ bị trôi dạt; hành động tiếp theo chỉ được suy ra từ công kiện (RFC §6.2).
type Action string

const (
	ActionIngest               Action = "ingest"
	ActionSegment              Action = "segment"
	ActionAwaitConfirmation    Action = "await_confirmation"
	ActionAnalyze              Action = "analyze"
	ActionSynthesize           Action = "synthesize"
	ActionAwaitStoryResolution Action = "await_story_resolution"
	ActionPublish              Action = "publish"
	ActionDone                 Action = "done"
)

// Facts là bản chụp sự thật tối thiểu đọc từ không gian làm việc, cần để quyết định hành động tiếp theo.
// Tách quyết định thuần túy (NextAction) và IO (LoadState): NextAction không đổi đối với cùng một Facts (RFC §20.1).
type Facts struct {
	WorkspaceReady   bool // manifest + intent + source đủ bộ 3
	Segmented        bool
	Confirmed        bool
	ExpectedChapters int // tổng số chương đã xác nhận cắt (điền từ giai đoạn hai)
	AnalyzedChapters int // số phân tích liên tục từ chương 1, khớp InputDigest (điền từ giai đoạn ba)
	Synthesized      bool
	StoryUncertain   bool
	StoryResolved    bool
	Published        bool // công kiện chính thức khớp hoàn toàn với synthesis (điền từ giai đoạn năm)
}

// NextAction chạy dọc theo đường ống tuyến tính cố định, trả về hành động thiếu hoặc chưa thỏa mãn đầu tiên. Hàm thuần túy, không IO.
func NextAction(f Facts) Action {
	switch {
	case f.Published:
		// Đăng là trạng thái cuối: Đối soát kho chính thức đã nhất trí toàn bộ, không gian làm việc chỉ là lưu trữ kiểm toán. Công kiện thượng nguồn do
		// phiên bản prompt / nâng cấp hướng dẫn bị lỗi thời sẽ không yêu cầu làm lại——nếu không việc nâng cấp phiên bản sẽ đẩy sách đã đăng
		// bị phán ngược về nửa chừng, cổng kiểm soát Engine qua lần khởi động lại sẽ khóa vĩnh viễn.
		return ActionDone
	case !f.WorkspaceReady:
		return ActionIngest
	case !f.Segmented:
		return ActionSegment
	case !f.Confirmed:
		return ActionAwaitConfirmation
	case f.AnalyzedChapters < f.ExpectedChapters:
		return ActionAnalyze
	case !f.Synthesized:
		return ActionSynthesize
	case f.StoryUncertain && !f.StoryResolved:
		return ActionAwaitStoryResolution
	default:
		return ActionPublish
	}
}

// artifactFresh phán đoán công kiện tồn tại và InputDigest của nó bằng với want cần tái tạo hiện tại;
// thiếu, phân tích lỗi, schema hoặc digest không khớp đều coi là không mới (cần làm lại).
func artifactFresh[T any](w *Workspace, rel, want string) (bool, error) {
	a, err := readArtifact[T](w, rel)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.InputDigest == want, nil
}

// LoadState đọc bản chụp sự thật hiện tại từ không gian làm việc (chỉ không gian làm việc, không bao gồm Store chính thức).
// Đoản mạch tuyến tính: Mỗi bước đều xác minh InputDigest của công kiện nhất trí với tóm tắt có thể tái tạo ở thượng nguồn hiện tại, bất kỳ bước nào không khớp sẽ coi bước đó chưa hoàn thành,
// sự thật hạ nguồn giữ false, giao cho NextAction làm lại từ đây——như vậy mới khiến "đổi cắt/phiên bản prompt/nguồn" tự nhiên làm vô hiệu hạ nguồn (RFC §6.2/§6.3 / Bất biến 1).
// Published được bên gọi bổ sung theo đối soát đăng chính thức (thống nhất đi qua CollectFacts).
func LoadState(w *Workspace) (Facts, error) {
	var f Facts
	if !w.Active() {
		return f, nil
	}
	if !(w.has(fileManifest) && w.has(fileIntent) && w.has(fileSource)) {
		return f, nil
	}
	src, err := w.LoadSource()
	if err != nil {
		return f, fmt.Errorf("đọc bản chụp nguồn import: %w", err)
	}
	f.WorkspaceReady = true
	guidance, err := w.LoadGuidance()
	if err != nil {
		return f, fmt.Errorf("đọc hướng dẫn cắt: %w", err)
	}

	// segmentation: Liên kết nguồn đã chuẩn hóa + hướng dẫn của người dùng + phiên bản prompt cắt. Hướng dẫn thay đổi (--guide nhận diện lại) tự nhiên làm vô hiệu việc cắt cũ.
	segArt, err := readArtifact[Segmentation](w, fileSegmentation)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("đọc công kiện cắt: %w", err)
	}
	if segArt.InputDigest != segmentInputDigest(Digest(src), guidance, segmentPromptVersion) {
		return f, nil
	}
	f.Segmented = true
	seg := &segArt.Payload
	f.ExpectedChapters = len(seg.Chapters)

	// confirmation: Liên kết byte gốc của công kiện segmentation.
	segRaw, err := w.readBytes(fileSegmentation)
	if err != nil {
		return f, fmt.Errorf("đọc bản gốc công kiện cắt: %w", err)
	}
	confirmed, err := artifactFresh[Confirmation](w, fileConfirmation, Digest(segRaw))
	if err != nil {
		return f, fmt.Errorf("đọc xác nhận cắt: %w", err)
	}
	if !confirmed {
		return f, nil
	}
	f.Confirmed = true

	// Phân tích từng chương: Số lượng InputDigest từng chương khớp với danh tính/phiên bản/chính văn cắt liên tục.
	f.AnalyzedChapters, err = analyzedChaptersStrict(w, seg, src, segArt.InputDigest, analyzePromptVersion)
	if err != nil {
		return f, err
	}
	if f.AnalyzedChapters < f.ExpectedChapters {
		return f, nil
	}

	// synthesis: Liên kết sự thật từng chương có thứ tự.
	facts, err := loadPriorFactsStrict(w, f.ExpectedChapters)
	if err != nil {
		return f, err
	}
	synArt, err := readArtifact[BookSynthesis](w, fileSynthesis)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("đọc công kiện tổng hợp toàn sách: %w", err)
	}
	if synArt.InputDigest != synthesisInputDigest(facts) {
		return f, nil
	}
	f.Synthesized = true
	f.StoryUncertain = synArt.Payload.StoryStatus == storyUncertain

	// story resolution: Liên kết byte gốc của công kiện synthesis khi uncertain, hoặc được chọn trước bởi intent.
	synRaw, err := w.readBytes(fileSynthesis)
	if err != nil {
		return f, fmt.Errorf("đọc bản gốc công kiện tổng hợp toàn sách: %w", err)
	}
	resolved, err := artifactFresh[StoryResolution](w, fileStoryResolve, Digest(synRaw))
	if err != nil {
		return f, fmt.Errorf("đọc phán quyết trạng thái câu chuyện: %w", err)
	}
	if resolved {
		f.StoryResolved = true
	} else if in, iErr := w.LoadIntent(); iErr != nil {
		return f, fmt.Errorf("đọc intent import: %w", iErr)
	} else if in.StoryResolution != "" {
		f.StoryResolved = true
	}
	return f, nil
}

// CollectFacts kết hợp sự thật không gian làm việc và đối soát đăng chính thức, là đầu vào sự thật thống nhất của ResumeStatus/ResumeSummary/runner
// . Số chương kỳ vọng để đối soát đăng ưu tiên lấy từ cắt mới; nếu cắt vì phiên bản prompt / nâng cấp hướng dẫn
// mà không khớp, lùi về số chương đã xác nhận lúc đó trong công kiện——chương chính thức của sách đã đăng chính là được lưu kho theo bản cắt đó,
// dùng phiên bản hiện tại để tính lại digest đối soát ngược lại sẽ không khớp với bất cứ thứ gì.
func CollectFacts(st *store.Store, w *Workspace) (Facts, error) {
	f, err := LoadState(w)
	if err != nil {
		return f, err
	}
	expected := f.ExpectedChapters
	if expected == 0 {
		if segArt, err := readArtifact[Segmentation](w, fileSegmentation); err == nil {
			expected = len(segArt.Payload.Chapters)
		}
	}
	f.Published, err = isPublished(st, expected)
	return f, err
}

// ResumeStatus báo cáo xem có không gian làm việc import hoạt động hay không, và nó đã hoàn thành triệt để chưa (bao gồm đối soát đăng chính thức).
// Dành cho cổng kiểm soát Engine qua lần khởi động lại (RFC §12.5): Cấm quy trình sáng tác thông thường tiêu thụ trạng thái nửa đăng khi active && !done.
func ResumeStatus(st *store.Store) (active, done bool, err error) {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return false, false, nil
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return true, false, err
	}
	return true, NextAction(f) == ActionDone, nil
}

// ResumeSummary tạo một dòng nhắc nhở import chưa hoàn thành (RFC §18.2); trả về chuỗi rỗng nếu không có import chưa hoàn thành.
// Dành cho máy chủ chủ động thông báo ở giao diện khởi động/chào mừng, tránh việc người dùng chỉ khi bị cổng kiểm soát sáng tác từ chối mới phát hiện sách này đang dừng nửa chừng lúc import.
func ResumeSummary(st *store.Store) string {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return ""
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return "Phát hiện đọc trạng thái import bất thường:" + err.Error() + "; vui lòng chạy /import để xem và sửa"
	}
	var state string
	switch NextAction(f) {
	case ActionDone:
		return ""
	case ActionIngest, ActionSegment:
		state = "Chưa hoàn thành cắt"
	case ActionAwaitConfirmation:
		state = fmt.Sprintf("Đã cắt %d chương, chờ đối chiếu xác nhận", f.ExpectedChapters)
	case ActionAnalyze:
		state = fmt.Sprintf("Đã phân tích %d/%d chương", f.AnalyzedChapters, f.ExpectedChapters)
	case ActionSynthesize:
		state = "Phân tích từng chương hoàn thành, chờ tổng hợp toàn sách"
	case ActionAwaitStoryResolution:
		state = "Chờ làm rõ trạng thái câu chuyện (--story=open|closed)"
	case ActionPublish:
		state = "Tổng hợp hoàn thành, chờ đăng trạng thái chính thức"
	}
	return "Phát hiện import chưa hoàn thành (" + state + "), nhập /import để tiếp tục từ điểm dừng"
}

// checkImportPreconditions xác minh điều kiện tiền đề import mới (RFC §12.1):
// Không có thông tin tác phẩm sẵn có, chương đã hoàn thành và PendingCommit đang trên đường. Ngữ nghĩa gộp tiểu thuyết đã có với văn bản bên ngoài mới không rõ ràng, phiên bản đầu tiên từ chối dứt khoát.
func checkImportPreconditions(st *store.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc thông tin tác phẩm: %w", err)
	}
	if book != nil {
		return fmt.Errorf("Đã có tác phẩm \"%s\", từ chối gộp tiểu thuyết bên ngoài vào sách không rỗng", book.Title)
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("đọc tiến độ: %w", err)
	}
	if prog != nil && len(prog.CompletedChapters) > 0 {
		return fmt.Errorf("Đã có %d chương hoàn thành, từ chối gộp tiểu thuyết bên ngoài vào sách không rỗng", len(prog.CompletedChapters))
	}
	pending, err := st.Signals.LoadPendingCommit()
	if err != nil {
		return fmt.Errorf("đọc commit đang trên đường: %w", err)
	}
	if pending != nil {
		return fmt.Errorf("Tồn tại commit chương đang trên đường, vui lòng hoàn thành hoặc dọn dẹp trước khi import")
	}
	return nil
}
