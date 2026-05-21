package gateway

import (
	"strings"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// mcpIdentityFromStore adapts a persisted AgentIdentity from the Redis
// store into the narrow view core/mcp expects on its filtering path.
// Returns nil for revoked or suspended identities so the filter
// fails closed — those identities see zero tools regardless of their
// other fields.
func mcpIdentityFromStore(src *store.AgentIdentity) *mcp.AgentIdentity {
	if src == nil {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(src.Status))
	if status == "revoked" || status == "suspended" {
		return nil
	}
	return &mcp.AgentIdentity{
		ID:                  src.ID,
		AllowedServers:      cloneAgentStrings(src.AllowedServers),
		AllowedTools:        cloneAgentStrings(src.AllowedTools),
		AllowedResources:    cloneAgentStrings(src.AllowedResources),
		Entitlements:        cloneAgentStrings(src.Entitlements),
		RiskTier:            src.RiskTier,
		DataClassifications: cloneAgentStrings(src.DataClassifications),
	}
}
