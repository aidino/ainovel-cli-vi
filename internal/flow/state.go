package flow

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// LoadState đọc toàn bộ sự thật mà Route cần từ Store.
// Đây là "Biên giới IO" của định tuyến: tất cả việc đọc tập trung ở đây, Route giữ tính thuần túy.
// Bất kỳ lỗi đọc nào đều trả về lỗi; công cụ bị hỏng và "chưa được tạo" là hai sự thật khác nhau, Router không được
// tiếp tục điều phối trên snapshot không hoàn chỉnh.
func LoadState(store *storepkg.Store) (State, error) {
	var s State
	missing, err := store.FoundationMissing()
	if err != nil {
		return s, fmt.Errorf("load foundation state: %w", err)
	}
	s.FoundationMissing = missing
	// Cấp độ quy hoạch: ghi vào RunMeta khi save_foundation lưu scale, nhánh bổ sung dựa vào đó suy ra nhà quy hoạch.
	// Lỗi đọc xử lý như không xác định (tier rỗng → bổ sung giao LLM phán quyết), nhất quán với mặc định bảo thủ của các sự thật khác.
	meta, err := store.RunMeta.Load()
	if err != nil {
		return s, fmt.Errorf("load run meta: %w", err)
	}
	if meta != nil {
		s.PlanningTier = meta.PlanningTier
	}
	progress, err := store.Progress.Load()
	if err != nil {
		return s, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return s, nil
	}
	s.Progress = progress
	feedback, err := store.Outline.LoadPendingOutlineFeedback()
	if err != nil {
		return s, fmt.Errorf("load outline feedback: %w", err)
	}
	for _, item := range feedback {
		if item.RequiresImmediateReview() {
			s.ImmediateFeedbackCount++
		}
	}

	s.LastCompleted = progress.LatestCompleted()

	// Ranh giới arc chỉ tính ở chế độ phân tầng và khi có chương đã hoàn thành
	if progress.Layered && s.LastCompleted > 0 {
		boundaries, err := store.Outline.CompletedArcBoundaries(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("load completed arc boundaries: %w", err)
		}
		for i := range boundaries {
			boundary := &boundaries[i]
			hasReview, err := store.World.HasArcReview(boundary.EndChapter)
			if err != nil {
				return s, fmt.Errorf("load arc review: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = aggregateRefresh(AggregateArcReview, boundary)
				break
			}
			hasArcSummary, err := store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
			if err != nil {
				return s, fmt.Errorf("load arc summary: %w", err)
			}
			if !hasArcSummary {
				s.AggregateRefresh = aggregateRefresh(AggregateArcSummary, boundary)
				break
			}
			if boundary.IsVolumeEnd {
				hasVolumeSummary, err := store.Summaries.HasVolumeSummary(boundary.Volume)
				if err != nil {
					return s, fmt.Errorf("load volume summary: %w", err)
				}
				if !hasVolumeSummary {
					s.AggregateRefresh = aggregateRefresh(AggregateVolumeSummary, boundary)
					break
				}
			}
		}

		boundary, err := store.Outline.CheckArcBoundary(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("check arc boundary: %w", err)
		}
		if boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview, err = store.World.HasArcReview(s.LastCompleted)
				if err != nil {
					return s, fmt.Errorf("load arc review: %w", err)
				}
				s.HasArcSummary, err = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if err != nil {
					return s, fmt.Errorf("load arc summary: %w", err)
				}
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary, err = store.Summaries.HasVolumeSummary(boundary.Volume)
					if err != nil {
						return s, fmt.Errorf("load volume summary: %w", err)
					}
				}
			}
		}
	}

	// Sự thật đọc kiểm toàn cục phi phân tầng: chỉ đọc đĩa tại điểm kích hoạt (các kết hợp Route khác không tiêu thụ trường này).
	if !progress.Layered && s.LastCompleted > 0 {
		for completed := domain.ReviewInterval; completed <= len(progress.CompletedChapters); completed += domain.ReviewInterval {
			chapter := progress.CompletedChapters[completed-1]
			hasReview, err := store.World.HasGlobalReview(chapter)
			if err != nil {
				return s, fmt.Errorf("load global review: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = &AggregateRefresh{Kind: AggregateGlobalReview, EndChapter: chapter}
				break
			}
		}
		if due, _ := domain.ShouldReview(len(progress.CompletedChapters)); due {
			s.HasGlobalReview, err = store.World.HasGlobalReview(s.LastCompleted)
			if err != nil {
				return s, fmt.Errorf("load global review: %w", err)
			}
		}
	}

	return s, nil
}

func aggregateRefresh(kind AggregateKind, boundary *storepkg.ArcBoundary) *AggregateRefresh {
	return &AggregateRefresh{
		Kind: kind, Volume: boundary.Volume, Arc: boundary.Arc,
		StartChapter: boundary.StartChapter, EndChapter: boundary.EndChapter,
	}
}
