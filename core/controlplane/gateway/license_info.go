package gateway

// LicenseInfo describes license metadata for the status endpoint.
type LicenseInfo struct {
	Mode           string           `json:"mode,omitempty"`
	Status         string           `json:"status,omitempty"`
	Plan           string           `json:"plan,omitempty"`
	OrgID          string           `json:"org_id,omitempty"`
	LicenseID      string           `json:"license_id,omitempty"`
	DeploymentType string           `json:"deployment_type,omitempty"`
	IssuedAt       string           `json:"issued_at,omitempty"`
	NotBefore      string           `json:"not_before,omitempty"`
	ExpiresAt      string           `json:"expires_at,omitempty"`
	Features       []string         `json:"features,omitempty"`
	Limits         map[string]int64 `json:"limits,omitempty"`
}

// LicenseInfoProvider optionally supplies license metadata for status responses.
type LicenseInfoProvider interface {
	LicenseInfo() *LicenseInfo
}
