package model

import "context"

// DelegationChainLink captures one hop in a verified delegation chain.
// The shape mirrors the token claims but lives in model so stores, APIs,
// and scheduler code can share a stable representation without depending
// directly on the token package.
type DelegationChainLink struct {
	AgentID   string `json:"agent_id,omitempty"`
	IssuedAt  string `json:"issued_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	JTI       string `json:"jti,omitempty"`
	ParentJTI string `json:"parent_jti,omitempty"`
	IssuedBy  string `json:"issued_by,omitempty"`
}

// DelegationLineage is the persisted scheduler-time snapshot of the
// delegation token that authorized a job submission.
type DelegationLineage struct {
	TokenJTI       string                `json:"token_jti,omitempty"`
	ParentTokenJTI string                `json:"parent_token_jti,omitempty"`
	Subject        string                `json:"subject,omitempty"`
	Audience       string                `json:"audience,omitempty"`
	Tenant         string                `json:"tenant,omitempty"`
	RootIssuer     string                `json:"root_issuer,omitempty"`
	ParentIssuer   string                `json:"parent_issuer,omitempty"`
	IssuerChain    []DelegationChainLink `json:"issuer_chain,omitempty"`
	ChainDepth     int                   `json:"chain_depth,omitempty"`
	ExpiresAt      string                `json:"expires_at,omitempty"`
	Scope          []string              `json:"scope,omitempty"`
	AllowedTopics  []string              `json:"allowed_topics,omitempty"`
	VerifiedAt     int64                 `json:"verified_at,omitempty"`
}

// DelegationDispatchToken is a scheduler-only secret used to re-verify
// delegation at dispatch time. It is intentionally stored outside the public
// job request payload so operators and workers do not receive the raw token.
type DelegationDispatchToken struct {
	Token    string `json:"token,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// DelegationDispatchTokenStore is an optional extension implemented by stores
// that can persist the raw delegation token required for dispatch-time
// re-verification.
type DelegationDispatchTokenStore interface {
	SetDelegationDispatchToken(ctx context.Context, jobID string, token DelegationDispatchToken) error
	GetDelegationDispatchToken(ctx context.Context, jobID string) (DelegationDispatchToken, error)
}
