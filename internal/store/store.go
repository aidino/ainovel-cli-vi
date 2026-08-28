package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// Store là root kết hợp của quản lý trạng thái, giữ tất cả các sub-store.
type Store struct {
	dir string

	Progress       *ProgressStore
	Book           *BookStore
	Outline        *OutlineStore
	Drafts         *DraftStore
	Summaries      *SummaryStore
	RunMeta        *RunMetaStore
	UserRules      *UserRulesStore
	Signals        *SignalStore
	Runtime        *RuntimeStore
	Characters     *CharacterStore
	Cast           *CastStore
	World          *WorldStore
	Checkpoints    *CheckpointStore
	Sessions       *SessionStore
	Usage          *UsageStore
	Simulation     *SimulationStore
	Decisions      *DecisionStore
	ChapterRecords *ChapterRecordStore
	Revisions      *RevisionStore

	crossMu sync.Mutex // Điều phối chéo miền nối tiếp; không có nghĩa là nhiều tệp có tính nguyên tử giao dịch
}

const (
	LegacyProjectFormatVersion  = 1
	CurrentProjectFormatVersion = 2
	projectFormatPath           = "meta/format.json"
)

type projectFormat struct {
	Version int `json:"version"`
}

// NewStore tạo trình quản lý trạng thái, dir là thư mục gốc đầu ra của tiểu thuyết.
func NewStore(dir string) *Store {
	io := newIO(dir)
	outline := NewOutlineStore(io)
	return &Store{
		dir:            dir,
		Progress:       NewProgressStore(newIO(dir)),
		Book:           NewBookStore(newIO(dir)),
		Outline:        outline,
		Drafts:         NewDraftStore(newIO(dir)),
		Summaries:      NewSummaryStore(newIO(dir), outline),
		RunMeta:        NewRunMetaStore(newIO(dir)),
		UserRules:      NewUserRulesStore(newIO(dir)),
		Signals:        NewSignalStore(newIO(dir)),
		Runtime:        NewRuntimeStore(newIO(dir)),
		Characters:     NewCharacterStore(newIO(dir), outline),
		Cast:           NewCastStore(newIO(dir)),
		World:          NewWorldStore(newIO(dir)),
		Checkpoints:    NewCheckpointStore(io),
		Sessions:       NewSessionStore(newIO(dir)),
		Usage:          NewUsageStore(newIO(dir)),
		Simulation:     NewSimulationStore(newIO(dir)),
		Decisions:      NewDecisionStore(newIO(dir)),
		ChapterRecords: NewChapterRecordStore(newIO(dir)),
		Revisions:      NewRevisionStore(newIO(dir)),
	}
}

// Dir trả về thư mục gốc đầu ra.
func (s *Store) Dir() string { return s.dir }

// LoadProjectFormatVersion trả về phiên bản định dạng dữ liệu của thư mục tác phẩm. Tác phẩm cũ không có tệp phiên bản,
// coi như v1, được nâng cấp thống nhất bởi di trú khởi động, mã nghiệp vụ không cần giữ nhánh định dạng cũ.
func (s *Store) LoadProjectFormatVersion() (int, error) {
	var format projectFormat
	if err := s.Progress.io.ReadJSON(projectFormatPath, &format); err != nil {
		if os.IsNotExist(err) {
			return LegacyProjectFormatVersion, nil
		}
		return 0, err
	}
	if format.Version <= 0 {
		return 0, fmt.Errorf("phiên bản định dạng dự án không hợp lệ: %d", format.Version)
	}
	return format.Version, nil
}

// SaveProjectFormatVersion cập nhật nguyên tử phiên bản định dạng dự án sau khi hoàn thành toàn bộ một đợt di trú.
func (s *Store) SaveProjectFormatVersion(version int) error {
	if version <= 0 {
		return fmt.Errorf("phiên bản định dạng dự án phải lớn hơn 0: %d", version)
	}
	return s.Progress.io.WriteJSON(projectFormatPath, projectFormat{Version: version})
}

// CheckConsistency thực hiện xác nhận tầng dữ kiện một cách nông cạn, dùng để tạo warning khi khởi động/phục hồi.
// Thuần chỉ đọc: không sửa dữ liệu, chỉ trả về mô tả vấn đề có thể đọc được. Bên gọi quyết định cách hiển thị (log / UI).
// Để tránh chi phí IO từ việc quét toàn bộ thư mục, chỉ xác nhận các điểm chính của Progress:
//   - Chương hoàn thành cuối cùng phải có bản cuối trong chapters/
//   - Dưới chế độ Layered, Volume/Arc hiện tại phải tìm thấy được trong layered_outline
func (s *Store) CheckConsistency() []string {
	var warnings []string
	progress, err := s.Progress.Load()
	if err != nil {
		return append(warnings, fmt.Sprintf("đọc progress thất bại: %v", err))
	}
	if progress == nil {
		return warnings
	}
	if n := len(progress.CompletedChapters); n > 0 {
		lastCh := progress.CompletedChapters[n-1]
		if text, err := s.Drafts.LoadChapterText(lastCh); err != nil {
			warnings = append(warnings, fmt.Sprintf("đọc bản cuối chương %d thất bại: %v", lastCh, err))
		} else if text == "" {
			warnings = append(warnings, fmt.Sprintf("progress đánh dấu chương %d đã hoàn thành, nhưng chapters/%02d.md không tồn tại hoặc trống", lastCh, lastCh))
		}
	}
	if progress.Layered && progress.CurrentVolume > 0 && progress.CurrentArc > 0 {
		volumes, err := s.Outline.LoadLayeredOutline()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("đọc đại cương phân tầng thất bại: %v", err))
		} else if len(volumes) > 0 {
			found := false
			for _, v := range volumes {
				if v.Index != progress.CurrentVolume {
					continue
				}
				for _, a := range v.Arcs {
					if a.Index == progress.CurrentArc {
						found = true
						break
					}
				}
				break
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("progress hiện tại V%d A%d không tìm thấy mục tương ứng trong đại cương phân tầng", progress.CurrentVolume, progress.CurrentArc))
			}
		}
	}
	return warnings
}

// FoundationMissing trả về thông tin tác phẩm và thiết lập cơ sở còn thiếu trong quy hoạch ban đầu, thứ tự ổn định.
// Chế độ truyện dài (đã có layered_outline) yêu cầu thêm compass. Đọc thất bại phải trả về nguyên trạng, không thể coi
// các artifact bị hỏng hoặc không có quyền đọc bị đánh giá nhầm thành "chưa được tạo", nếu không bên gọi có thể ghi đè dữ liệu thực tế.
func (s *Store) FoundationMissing() ([]string, error) {
	var missing []string
	book, err := s.Book.Load()
	if err != nil {
		return nil, fmt.Errorf("load book metadata: %w", err)
	}
	if book == nil {
		missing = append(missing, "book")
	}
	premise, err := s.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("load premise: %w", err)
	}
	if premise == "" {
		missing = append(missing, "premise")
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		return nil, fmt.Errorf("load outline: %w", err)
	}
	if len(outline) == 0 {
		missing = append(missing, "outline")
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("load characters: %w", err)
	}
	if len(characters) == 0 {
		missing = append(missing, "characters")
	}
	rules, err := s.World.LoadWorldRules()
	if err != nil {
		return nil, fmt.Errorf("load world rules: %w", err)
	}
	if len(rules) == 0 {
		missing = append(missing, "world_rules")
	}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, fmt.Errorf("load layered outline: %w", err)
	}
	if len(layered) > 0 {
		compass, err := s.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("load compass: %w", err)
		}
		if compass == nil {
			missing = append(missing, "compass")
		}
	}
	// Sách mới chỉ được phép từ quy hoạch vào viết sau khi trải qua thẩm định ngữ nghĩa rõ ràng của model đối với các artifact đã ghi đĩa.
	// PhaseWriting/Complete đại diện cho sách cũ hoặc sách mới đã thẩm định, duy trì khả năng tương thích của dự án lịch sử; bản thân việc thẩm định
	// là một hành động chứ không phải thiếu tệp, do đó chỉ được nối thêm khi các artifact khác đầy đủ.
	if len(missing) == 0 {
		progress, err := s.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w", err)
		}
		if progress == nil || (progress.Phase != domain.PhaseWriting && progress.Phase != domain.PhaseComplete) {
			missing = append(missing, "foundation_audit")
		}
	}
	return missing, nil
}

// FoundationFingerprint trả về dấu vân tay nội dung của các artifact thiết lập cơ sở hiện tại. Architect phải đưa lại
// giá trị này được đọc bởi novel_context cho công cụ thẩm định nguyên vẹn, để đảm bảo kết luận là dựa trên phiên bản thực tế ghi đĩa,
// chứ không phải nội dung chưa lưu hoặc đã hết hạn trong phiên.
func (s *Store) FoundationFingerprint() (string, error) {
	files := []string{"meta/book.json", "premise.md", "outline.json", "characters.json", "world_rules.json"}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return "", fmt.Errorf("load layered outline: %w", err)
	}
	if len(layered) > 0 {
		files = append(files, "layered_outline.json", "meta/compass.json")
	}

	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Init tạo cấu trúc thư mục con cần thiết.
func (s *Store) Init() error {
	if err := s.Checkpoints.InitError(); err != nil {
		return fmt.Errorf("load checkpoints: %w", err)
	}
	return s.Progress.io.EnsureDirs([]string{
		"chapters", "summaries", "drafts", "reviews", "meta", "meta/chapter_records", "meta/runtime", "meta/runtime/tasks", "meta/sessions", "meta/sessions/agents",
	})
}

// ── Phương thức điều phối chéo miền ──

// ExpandArc hiệu chỉnh bộ khung arc và mở rộng thành các chương chi tiết (Liên kết Outline + Progress).
func (s *Store) ExpandArc(volumeIdx, arcIdx int, expansion domain.ArcExpansion) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.expandArcUnlocked(volumeIdx, arcIdx, expansion)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	return s.Progress.saveUnlocked(p)
}

// AppendVolume nối thêm tập mới vào cuối đại cương phân tầng (Liên kết Outline + Progress).
func (s *Store) AppendVolume(vol domain.VolumeOutline) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.appendVolumeUnlocked(vol)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	return s.Progress.saveUnlocked(p)
}

// ReviseOutline thay thế đoạn đuôi kế hoạch chưa xảy ra từ fromChapter trở đi.
// Đại cương phẳng thay thế đoạn đuôi của toàn bộ sách; Đại cương phân tầng chỉ thay thế đoạn đuôi của arc chứa chương mục tiêu. Định nghĩa này cho phép tải trọng tương tự
// khi phát lại vẫn thu được cùng kết quả, đồng thời tránh liệt kê các thao tác như JSON Patch và insert/delete.
func (s *Store) ReviseOutline(fromChapter int, replacement []domain.OutlineEntry) (int, error) {
	if fromChapter <= 0 {
		return 0, fmt.Errorf("from_chapter must be > 0: %w", errs.ErrToolArgs)
	}

	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if p == nil {
		return 0, fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
	}
	if p.Phase == domain.PhaseComplete {
		return 0, fmt.Errorf("toàn sách đã hoàn thành, không cho phép sửa đại cương: %w", errs.ErrToolPrecondition)
	}
	protected := p.InProgressChapter
	if latest := p.LatestCompleted(); latest > protected {
		protected = latest
	}
	if fromChapter <= protected {
		return 0, fmt.Errorf("chương %d đã hoàn thành hoặc đang viết; sửa đại cương phải bắt đầu sau chương %d: %w",
			fromChapter, protected, errs.ErrToolPrecondition)
	}

	if p.Layered {
		volumes, err := s.Outline.reviseLayeredTailUnlocked(fromChapter, replacement)
		if err != nil {
			return 0, err
		}
		p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
		if err := s.Progress.saveUnlocked(p); err != nil {
			return 0, fmt.Errorf("save progress: %w: %w", errs.ErrStoreWrite, err)
		}
		return p.TotalChapters, nil
	}

	outline, err := s.Outline.reviseFlatTailUnlocked(fromChapter, replacement)
	if err != nil {
		return 0, err
	}
	p.TotalChapters = len(outline)
	if err := s.Progress.saveUnlocked(p); err != nil {
		return 0, fmt.Errorf("save progress: %w: %w", errs.ErrStoreWrite, err)
	}
	return p.TotalChapters, nil
}

// ClearHandledSteer xóa PendingSteer và thiết lập lại trạng thái FlowSteering cũ.
// Hai tệp không thể cấu thành giao dịch hệ thống tệp, vì vậy trước tiên ghi Progress có thể lặp lại, cuối cùng mới xóa ý định phục hồi;
// Mọi bước thất bại đều ít nhất giữ lại PendingSteer, Resume lần sau có thể phát lại an toàn.
func (s *Store) ClearHandledSteer() error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.RunMeta.io.mu.Lock()
	defer s.RunMeta.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	meta, err := s.RunMeta.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	p, err := s.Progress.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p != nil && p.Flow == domain.FlowSteering {
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		if err := s.Progress.saveUnlocked(p); err != nil {
			return err
		}
	}
	if meta != nil && meta.PendingSteer != "" {
		meta.PendingSteer = ""
		if err := s.RunMeta.saveUnlocked(*meta); err != nil {
			return err
		}
	}
	return nil
}