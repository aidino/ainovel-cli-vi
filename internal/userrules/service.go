package userrules

import (
	"context"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Service biên đạo việc sinh và cập nhật điểm khôi phục quy tắc người dùng: chuẩn hóa các nguồn → gộp xác định → ghi đĩa.
//
// Hai bên gọi dùng chung một bộ logic:
//   - Mở sách/Làm mới: Build / GetOrBuild, do Host gọi một cách xác định.
//   - Cập nhật lúc chạy: Sau khi Trọng tài trích xuất rules, Host gọi AddRuntimeRule.
type Service struct {
	store     *store.Store
	norm      *Normalizer
	rulesOpts rules.LoadOptions
}

// NewService cấu trúc dịch vụ. model dùng để chuẩn hóa (nên là model có năng lực mạnh); khi model là nil
// mọi nguồn bị hạ cấp thành raw preferences (vẫn có thể sinh ra điểm khôi phục, kiểm tra máy móc do system_defaults bọc đáy).
func NewService(st *store.Store, model agentcore.ChatModel, opts rules.LoadOptions) *Service {
	return &Service{store: st, norm: NewNormalizer(model), rulesOpts: opts}
}

// normalizeOrDegrade chuẩn hóa một nguồn; khi thất bại thì ghi lại lỗi thực sự và hạ cấp thành raw preferences
// (điểm khôi phục Status=degraded, nguyên văn được giữ lại) —— hạ cấp là sự thật nhìn thấy được, lý do lỗi vào nhật ký.
func (s *Service) normalizeOrDegrade(ctx context.Context, source, text string) rules.Candidate {
	cand, err := s.norm.Normalize(ctx, source, text)
	if err != nil {
		slog.Warn("chuẩn hóa quy tắc thất bại, hạ cấp thành sở thích nguyên văn", "module", "rules", "source", source, "err", err)
		return degraded(source, text)
	}
	return cand
}

// Build chuẩn hóa từ các nguồn tĩnh (system_defaults + file rules + prompt khởi động) sinh ra điểm khôi phục và ghi đĩa.
// Gọi khi mở sách/làm mới. startupPrompt có thể rỗng.
func (s *Service) Build(ctx context.Context, startupPrompt string) (*rules.Snapshot, error) {
	cands := []rules.Candidate{rules.SystemDefaults()}
	for _, rs := range rules.RawFileSources(s.rulesOpts) {
		cands = append(cands, s.normalizeOrDegrade(ctx, rs.Label, rs.Text))
	}
	if strings.TrimSpace(startupPrompt) != "" {
		cands = append(cands, s.normalizeOrDegrade(ctx, "startup_prompt", startupPrompt))
	}
	snap := rules.BuildSnapshot(cands)
	if err := s.store.UserRules.Save(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// GetOrBuild trả về điểm khôi phục hiện tại; khi thiếu sẽ khởi tạo theo system_defaults + file rules.
// Đường dẫn đọc lúc chạy hợp nhất đi qua đây.
func (s *Service) GetOrBuild(ctx context.Context) (*rules.Snapshot, error) {
	cur, err := s.store.UserRules.Load()
	if err != nil {
		return nil, err
	}
	if cur != nil {
		return cur, nil
	}
	return s.Build(ctx, "")
}

// AddRuntimeRule chuẩn hóa một quy tắc dài hạn lúc chạy, gộp đè lên điểm khôi phục hiện tại với độ ưu tiên cao nhất và ghi đĩa.
// Không bao giờ báo lỗi vì chuẩn hóa thất bại —— khi thất bại thì mục đó hạ cấp thành raw preferences.
// Trả về điểm khôi phục sau khi gộp đè và ứng viên chuẩn hóa của lần này.
func (s *Service) AddRuntimeRule(ctx context.Context, text string) (*rules.Snapshot, rules.Candidate, error) {
	cur, err := s.GetOrBuild(ctx)
	if err != nil {
		return nil, rules.Candidate{}, err
	}
	cand := s.normalizeOrDegrade(ctx, "runtime_update", text)
	merged := rules.OverlaySnapshot(*cur, cand)
	if err := s.store.UserRules.Save(&merged); err != nil {
		return nil, cand, err
	}
	return &merged, cand, nil
}