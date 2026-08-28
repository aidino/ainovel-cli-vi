package domain

// FoundationAuditIssue là vấn đề nhất quán xuyên tệp do Architect đưa ra đối với thiết lập cơ bản đã ghi đĩa.
type FoundationAuditIssue struct {
	Artifact    string `json:"artifact"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// FoundationAudit ghi lại một lần thẩm tra của model đối với thiết lập cơ bản phiên bản xác định.
type FoundationAudit struct {
	Fingerprint string                 `json:"fingerprint"`
	Ready       bool                   `json:"ready"`
	Summary     string                 `json:"summary"`
	Issues      []FoundationAuditIssue `json:"issues"`
}
