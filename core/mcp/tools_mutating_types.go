package mcp

// Mutating tool input/output types.
//
// Every input carries an IdempotencyKey the MCP layer forwards as an
// `Idempotency-Key` header on the underlying gateway request. The
// gateway deduplicates retries so an LLM replaying a tool call after
// a human approval does not create a second workflow / install a pack
// twice / register two agents.
//
// Outputs are deliberately narrow — just the record IDs the caller
// needs to follow up. Full detail is available via the read-only
// get-by-id tools shipped in task-466b6a6a.

// CreateWorkflowInput captures the body for POST /api/v1/workflows.
// Matches core/controlplane/gateway/handlers_workflows.go
// createWorkflowRequest — the gateway expects top-level workflow
// fields (Steps / Config / Parameters / InputSchema), NOT a wrapped
// `spec` object. An earlier version of this tool wrapped everything
// under `spec` and the gateway silently dropped it (QA reopen).
type CreateWorkflowInput struct {
	ID             string           `json:"id,omitempty"`
	Name           string           `json:"name,omitempty"`
	Description    string           `json:"description,omitempty"`
	OrgID          string           `json:"org_id,omitempty"`
	TeamID         string           `json:"team_id,omitempty"`
	Version        string           `json:"version,omitempty"`
	TimeoutSec     int64            `json:"timeout_sec,omitempty"`
	Steps          map[string]any   `json:"steps"`
	Config         map[string]any   `json:"config,omitempty"`
	Parameters     []map[string]any `json:"parameters,omitempty"`
	InputSchema    map[string]any   `json:"input_schema,omitempty"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
}

type CreateWorkflowOutput struct {
	WorkflowID string `json:"workflow_id"`
	Version    string `json:"version,omitempty"`
}

// InstallPackInput captures the body for POST /api/v1/marketplace/install.
// An earlier version of this tool targeted /api/v1/packs/install, but
// that endpoint expects a multipart bundle upload, not a JSON body
// (QA reopen). Marketplace-install is the JSON path and the correct
// target for LLM-driven installs: the gateway uses {catalog_id, pack_id,
// version} or {url, sha256} to fetch the pack from a trusted source.
//
// Typical LLM usage: supply pack_id + version (catalog_id optional
// when the default marketplace catalogue resolves the pack), or URL
// + Sha256 for private / air-gapped installs.
type InstallPackInput struct {
	CatalogID      string `json:"catalog_id,omitempty"`
	PackID         string `json:"pack_id,omitempty"`
	Version        string `json:"version,omitempty"`
	URL            string `json:"url,omitempty"`
	Sha256         string `json:"sha256,omitempty"`
	Force          bool   `json:"force,omitempty"`
	Upgrade        bool   `json:"upgrade,omitempty"`
	Inactive       bool   `json:"inactive,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type InstallPackOutput struct {
	PackID    string `json:"pack_id"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed"`
}

// UninstallPackInput captures POST /api/v1/packs/{id}/uninstall. Reason
// lands on the audit record so the post-incident reviewer can see why
// the pack was removed.
type UninstallPackInput struct {
	PackID         string `json:"pack_id"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// RegisterAgentInput captures POST /api/v1/agents. Mirrors the
// gateway's createAgentRequest struct in handlers_agents.go — Owner +
// RiskTier are mandatory, and the gateway generates the ID server-
// side if none is provided. An earlier version accepted a client
// `id` and unknown fields (`org_id`, `labels`), which the gateway
// silently dropped (QA reopen).
type RegisterAgentInput struct {
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Owner               string   `json:"owner"`
	Team                string   `json:"team,omitempty"`
	RiskTier            string   `json:"risk_tier"`
	AllowedTopics       []string `json:"allowed_topics,omitempty"`
	AllowedPools        []string `json:"allowed_pools,omitempty"`
	AllowedServers      []string `json:"allowed_servers,omitempty"`
	AllowedTools        []string `json:"allowed_tools,omitempty"`
	AllowedResources    []string `json:"allowed_resources,omitempty"`
	Entitlements        []string `json:"entitlements,omitempty"`
	DataClassifications []string `json:"data_classifications,omitempty"`
	IdempotencyKey      string   `json:"idempotency_key,omitempty"`
}

type RegisterAgentOutput struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Owner      string `json:"owner,omitempty"`
	RiskTier   string `json:"risk_tier,omitempty"`
	Registered bool   `json:"registered"`
}

// UpdatePolicyBundleInput carries the YAML content the operator wants
// to save. Signing happens server-side using the policy-signing key
// from task-fcd39725 so the MCP client never holds the private key.
type UpdatePolicyBundleInput struct {
	BundleID       string `json:"bundle_id"`
	Content        string `json:"content"`
	Author         string `json:"author,omitempty"`
	Message        string `json:"message,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type UpdatePolicyBundleOutput struct {
	BundleID  string `json:"bundle_id"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Signed    bool   `json:"signed"`
	KeyID     string `json:"key_id,omitempty"`
}

// RevokeWorkerSessionInput targets POST /api/v1/workers/{id}/revoke-session.
// An earlier version targeted DELETE /api/v1/workers/credentials/{id}
// which revokes the persistent worker credential — a strictly broader
// operation than revoking just the active session. The mutating-tool
// contract says "revoke session", so we use the session-specific
// endpoint and leave credential revocation to a dedicated flow (QA
// reopen). Reason is audit-visible via the worker_trust_change event
// emitted by handleRevokeWorkerSession.
type RevokeWorkerSessionInput struct {
	WorkerID       string `json:"worker_id"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// SetAgentScopeInput targets PUT /api/v1/agents/{id}. Mirrors
// handlers_agents.go updateAgentRequest: all fields are optional,
// non-empty ones overwrite the stored identity. The MCP layer
// focuses on the scope-relevant fields (AllowedTools /
// PreapprovedMutatingTools / AllowedTopics / AllowedPools), but we
// also expose Status so operators can suspend/revoke an identity
// through the same tool.
type SetAgentScopeInput struct {
	AgentID                  string   `json:"agent_id"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools,omitempty"`
	Status                   string   `json:"status,omitempty"`
	IdempotencyKey           string   `json:"idempotency_key,omitempty"`
}

type SetAgentScopeOutput struct {
	AgentID                  string   `json:"agent_id"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools,omitempty"`
	Status                   string   `json:"status,omitempty"`
}
