package flow

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestStartsForwardChapter(t *testing.T) {
	base := &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}}
	tests := []struct {
		name    string
		inst    *Instruction
		p       *domain.Progress
		pending *domain.PendingCommit
		want    bool
	}{
		{"正常下一chương ", &Instruction{Agent: "writer", Chapter: 2}, base, nil, true},
		{"零chương按事实推导", &Instruction{Agent: "writer"}, base, nil, true},
		{"文案不参与", &Instruction{Agent: "writer", Chapter: 2, Task: "任意", Reason: "任意"}, base, nil, true},
		{"làm lại  Writer", &Instruction{Agent: "writer", Chapter: 1}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}, PendingRewrites: []int{1}}, nil, false},
		{"chươngkhôi phục ", &Instruction{Agent: "writer", Chapter: 2}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}, InProgressChapter: 2}, nil, false},
		{"提交khôi phục ", &Instruction{Agent: "writer", Chapter: 2}, base, &domain.PendingCommit{Chapter: 2}, false},
		{"非下一chương ", &Instruction{Agent: "writer", Chapter: 3}, base, nil, false},
		{"Editor", &Instruction{Agent: "editor"}, base, nil, false},
		{"空lệnh ", nil, base, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StartsForwardChapter(tc.inst, tc.p, tc.pending); got != tc.want {
				t.Fatalf("StartsForwardChapter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveAdvanceHold(t *testing.T) {
	tests := []struct {
		name    string
		hold    *domain.AdvanceHold
		p       *domain.Progress
		want    AdvanceHoldResolution
		wantErr bool
	}{
		{"无 hold", nil, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, false},
		{"边界tạm dừng ", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "停"}, &domain.Progress{Phase: domain.PhaseWriting, PendingRewrites: []int{1}}, AdvanceHoldConsumeAndStop, false},
		{"làm lại 未排空", &domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "验收"}, &domain.Progress{Phase: domain.PhaseWriting, PendingRewrites: []int{1}}, AdvanceHoldKeep, false},
		{"làm lại 已排空", &domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "验收"}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldConsumeAndStop, false},
		{"目标chương未完成", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 3, Reason: "写到第3chương "}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1, 2}}, AdvanceHoldKeep, false},
		{"目标chương已完成", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 3, Reason: "写到第3chương "}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1, 2, 3}}, AdvanceHoldConsumeAndStop, false},
		{"目标chươngthiếu ", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, Reason: "写到目标chương "}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, true},
		{"完本只消费", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "停"}, &domain.Progress{Phase: domain.PhaseComplete}, AdvanceHoldConsume, false},
		{"chưa biết điều kiện ", &domain.AdvanceHold{After: "unknown", Reason: "停"}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, true},
		{"缺少进度", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "停"}, nil, AdvanceHoldKeep, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAdvanceHold(tc.hold, tc.p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("resolution = %v, want %v", got, tc.want)
			}
		})
	}
}
