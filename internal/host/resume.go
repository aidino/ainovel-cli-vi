package host

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/revision"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// upgradeProject nâng dữ liệu dự án cũ lên định dạng hiện tại, đồng thời truyền cùng root error cho cả giao diện và log.
func upgradeProject(st *storepkg.Store) error {
	if err := runProjectUpgrades(st); err != nil {
		slog.Error("nâng cấp dữ liệu dự án thất bại", "module", "migration", "err", err)
		return err
	}
	return nil
}

func runProjectUpgrades(st *storepkg.Store) error {
	version, err := st.LoadProjectFormatVersion()
	if err != nil {
		return fmt.Errorf("đọc phiên bản định dạng dự án: %w", err)
	}
	if version > storepkg.CurrentProjectFormatVersion {
		return fmt.Errorf("phiên bản định dạng dự án v%d cao hơn v%d mà chương trình hiện tại hỗ trợ, vui lòng nâng cấp ainovel-cli", version, storepkg.CurrentProjectFormatVersion)
	}
	for version < storepkg.CurrentProjectFormatVersion {
		next := version + 1
		switch version {
		case storepkg.LegacyProjectFormatVersion:
			if err := migrateLegacyBook(st); err != nil {
				return fmt.Errorf("nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
		case storepkg.ChapterRecordProjectFormatVersion:
			// v3 bổ sung bản ghi nghiệm thu mà v2 có thể bỏ sót; bản ghi đã có được hàm di chuyển giữ nguyên.
			if err := revision.MigrateLegacyBaseline(st); err != nil {
				return fmt.Errorf("nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
		default:
			return fmt.Errorf("không hỗ trợ nâng cấp từ định dạng dự án v%d", version)
		}
		if err := st.SaveProjectFormatVersion(next); err != nil {
			return fmt.Errorf("lưu phiên bản định dạng dự án v%d: %w", next, err)
		}
		slog.Info("hoàn tất nâng cấp dữ liệu dự án", "module", "migration", "from", version, "to", next)
		version = next
	}
	return nil
}

func migrateLegacyBook(st *storepkg.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		book, err = loadLegacyBook(st)
		if err != nil || book == nil {
			return err
		}
	}
	if err := st.Book.Save(*book); err != nil {
		return fmt.Errorf("lưu thông tin tác phẩm cũ: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("ghi lại thông tin tác phẩm cũ: %w", err)
	}
	return nil
}

func loadLegacyBook(st *storepkg.Store) (*domain.BookMetadata, error) {
	data, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "progress.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("đọc tiến độ tác phẩm cũ: %w", err)
	}
	var legacy struct {
		NovelName string `json:"novel_name"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("phân tích tiến độ tác phẩm cũ: %w", err)
	}
	legacy.NovelName = strings.TrimSpace(legacy.NovelName)
	if legacy.NovelName == "" {
		return nil, nil
	}
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("đọc tiền đề câu chuyện cũ: %w", err)
	}
	title := legacyPremiseTitle(premise)
	if title == "" {
		return nil, fmt.Errorf("tiền đề câu chuyện cũ thiếu tiêu đề sách")
	}
	if title != legacy.NovelName {
		return nil, fmt.Errorf("trùng lặp tên sách tác phẩm cũ: progress=%q, premise=%q", legacy.NovelName, title)
	}
	synopsis := legacyPremiseSection(premise, "Xung đột cốt lõi")
	if synopsis == "" {
		return nil, fmt.Errorf("tiền đề câu chuyện cũ thiếu \"Xung đột cốt lõi\", không thể tạo tóm tắt tác phẩm")
	}
	return &domain.BookMetadata{Title: title, Synopsis: synopsis}, nil
}

func legacyPremiseTitle(premise string) string {
	for _, line := range strings.Split(premise, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "# ")), "《》\"")
		}
	}
	return ""
}

func legacyPremiseSection(premise, heading string) string {
	var body []string
	matched := false
	for _, line := range strings.Split(premise, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if matched {
				break
			}
			matched = strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")) == heading
			continue
		}
		if matched {
			body = append(body, line)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// resumeLabel dựa trên thực tế để sinh ra nhãn UI cho Resume.
// label rỗng biểu thị không có trạng thái có thể khôi phục (nên đi theo tạo mới). Bản thân việc khôi phục không cần bất kỳ prompt nào——
// Engine chỉ khôi phục thực tế: từ store tính toán lại định tuyến để chạy tiếp (docs/engine-rfc.md §6).
func resumeLabel(store *storepkg.Store) (string, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return "", nil
	}
	return describeResume(store, progress)
}

// describeResume sinh ra nhãn khôi phục mà con người có thể đọc; không ảnh hưởng đến định tuyến của Engine.
// Mọi định tuyến thực thi đều do Flow Router suy luận dựa trên thực tế; ở đây chỉ hướng tới UI "Khôi phục: xxx".
func describeResume(store *storepkg.Store, progress *domain.Progress) (string, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("Khôi phục: Giai đoạn quy hoạch (%s)", progress.Phase), nil
	case domain.PhaseWriting:
		// Độ ưu tiên khớp với độ ưu tiên quyết định của Router, để label đồng bộ với chỉ thị sắp phân phối.
		pending, err := store.Signals.LoadPendingCommit()
		if err != nil {
			return "", fmt.Errorf("đọc commit đợi khôi phục: %w", err)
		}
		if pending != nil {
			return fmt.Sprintf("Khôi phục: Chương %d commit bị gián đoạn", pending.Chapter), nil
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "Làm lại"
			if progress.Flow == domain.FlowPolishing {
				verb = "Đánh bóng"
			}
			return fmt.Sprintf("Khôi phục %s: %d chương đang chờ xử lý", verb, len(progress.PendingRewrites)), nil
		}
		if progress.Flow == domain.FlowReviewing {
			return "Khôi phục: Đọc kiểm gián đoạn", nil
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("Khôi phục: Chương %d đang tiến hành", progress.InProgressChapter), nil
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return "", err
		}
		if label != "" {
			return label, nil
		}
		return fmt.Sprintf("Khôi phục: Tiếp tục từ chương %d", progress.NextChapter()), nil
	}
	return "Khôi phục", nil
}

// describeArcEndLabel tạo nhãn thân thiện với UI cho nhiều trạng thái trung gian ở cuối arc/cuối tập.
// Giữ nguyên thứ tự nhánh cuối arc của flow.Route, đảm bảo label khớp với chỉ thị đầu tiên của Router.
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (string, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return "", nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return "", fmt.Errorf("kiểm tra ranh giới arc: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return "", nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	hasArcReview, err := store.World.HasArcReview(lastCh)
	if err != nil {
		return "", fmt.Errorf("đọc review arc: %w", err)
	}
	hasArcSummary, err := store.Summaries.HasArcSummary(vol, arc)
	if err != nil {
		return "", fmt.Errorf("đọc tóm tắt arc: %w", err)
	}
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary, err = store.Summaries.HasVolumeSummary(vol)
		if err != nil {
			return "", fmt.Errorf("đọc tóm tắt tập: %w", err)
		}
	}
	switch {
	case !hasArcReview:
		return fmt.Sprintf("Khôi phục: Đợi review cuối arc (V%d A%d)", vol, arc), nil
	case !hasArcSummary:
		return fmt.Sprintf("Khôi phục: Đợi tạo tóm tắt arc (V%d A%d)", vol, arc), nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return fmt.Sprintf("Khôi phục: Đợi tạo tóm tắt tập (V%d)", vol), nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("Khôi phục: Đợi mở rộng arc tiếp theo (V%d A%d)", boundary.NextVolume, boundary.NextArc), nil
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("Khôi phục: Đợi quyết định tập tiếp theo (Cuối V%d)", vol), nil
	}
	return "", nil
}
