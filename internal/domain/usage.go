package domain

import "time"

// UsageSchemaVersion là số phiên bản tương thích của meta/usage.json.
// Tương lai nếu ngữ nghĩa trường AgentUsageTotals thay đổi, tăng giá trị này; UsageStore.Load khi gặp phiên bản khác nên bỏ qua và kích hoạt phát lại để xây dựng lại.
const UsageSchemaVersion = 2

// UsageState là snapshot cố định của lượng token / cost tích lũy.
// Trong bộ nhớ do UsageTracker bảo trì, định kỳ debounce ghi đĩa vào meta/usage.json.
//
// Chú ý: Cửa sổ trượt samples ("tỷ lệ trúng N lần gần đây") bên trong UsageTracker **không cố định**——
// Nó chỉ phục vụ chẩn đoán ngắn hạn cho UI, khi process khởi động lại bắt đầu tích lũy lại vài vòng từ đầu là có thể khôi phục ngữ nghĩa.
// MissingAssistantUsage giữ lại cố định, tích lũy qua các lần khởi động lại có giá trị chẩn đoán hơn.
type UsageState struct {
	Schema       int                         `json:"schema"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	Overall      AgentUsageTotals            `json:"overall"`
	PerAgent     map[string]AgentUsageTotals `json:"per_agent"`
	PerModel     map[string]AgentUsageTotals `json:"per_model,omitempty"`
	MissingUsage int                         `json:"missing_assistant_usage"`
}

// AgentUsageTotals là hình thái cố định của đếm tích lũy cho một vai trò (hoặc overall).
type AgentUsageTotals struct {
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	CacheRead    int     `json:"cache_read"`
	CacheWrite   int     `json:"cache_write"`
	Cost         float64 `json:"cost_usd"`
	Saved        float64 `json:"saved_usd"`
	CacheCapable bool    `json:"cache_capable"`
	// CacheBreaks là số lần đứt chuỗi bộ nhớ đệm phát hiện lúc live (tiền tố không ngắn lại mà lượng trúng giảm mạnh).
	// Chỉ tích lũy trong đường dẫn thời gian thực, phát lại session không phát lại phần phát hiện.
	CacheBreaks int `json:"cache_breaks,omitempty"`
}
