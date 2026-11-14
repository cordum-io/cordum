package gateway

import (
	"context"
	"encoding/json"
	"log/slog"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
)

// mcpUpstreamSecretResolver maps `secret://<name>` upstream auth refs to
// environment variables, using EnvSecretResolver's convention (secret://a-b ->
// A_B); e.g. secret://monday-token resolves from $MONDAY_TOKEN. Kept
// upstream-agnostic — the concrete env var is a deployment concern, not gateway
// code, so the platform carries no per-integration knowledge.
func mcpUpstreamSecretResolver() mcp.SecretResolver {
	return mcp.EnvSecretResolver(nil)
}

// selectGatewayMCPUpstream returns the first enabled, use-time-revalidated remote
// MCP upstream registered for tenantID, or (nil,false). Mirrors the per-tenant
// selection mcp.Gateway.selectUsableUpstream performs; fail-closed on revalidation
// (a DNS-rebind / now-unsafe endpoint is skipped, never dialed).
func (s *server) selectGatewayMCPUpstream(ctx context.Context, tenantID string) (*edgecore.UpstreamServer, bool) {
	reg := mcpGatewayUpstreamRegistry(s)
	if reg == nil {
		return nil, false
	}
	ups, err := reg.List(ctx, tenantID)
	if err != nil {
		slog.Warn("mcp upstream fronting: registry list failed", "tenant", tenantID, "err", err)
		return nil, false
	}
	for i := range ups {
		if !ups[i].Enabled {
			continue
		}
		if rerr := edgecore.RevalidateMCPUpstreamAtUse(ctx, &ups[i]); rerr != nil {
			slog.Warn("mcp upstream fronting: use-time revalidation failed; skipping",
				"name", ups[i].Name, "err", rerr)
			continue
		}
		return &ups[i], true
	}
	return nil, false
}

// buildFrontedUpstreamToolService returns a policy-gateable ToolService that
// proxies tools/list + tools/call to the tenant's registered remote MCP upstream,
// together with the upstream's name (used verbatim as the gate server-name so the
// action-gate + audit attribute decisions to e.g. "cordum.monday"). The proxy is
// served only to a caller with a bound agent identity (frontedIdentityGate). Returns
// (nil,"") when no usable upstream is configured or the proxy cannot be built —
// the caller then serves the built-in tool registry unchanged. Fail-closed: a
// missing secret or unsafe endpoint disables fronting rather than dialing
// unauthenticated.
func (s *server) buildFrontedUpstreamToolService(ctx context.Context, tenantID string) (mcp.ToolService, string) {
	up, ok := s.selectGatewayMCPUpstream(ctx, tenantID)
	if !ok {
		return nil, ""
	}
	rcfg, err := mcp.ResolveUpstreamConfig(up, mcpUpstreamSecretResolver())
	if err != nil {
		slog.Error("mcp upstream fronting disabled: resolve config failed", "name", up.Name, "err", err)
		return nil, ""
	}
	remote, err := mcp.NewRemoteUpstream(ctx, rcfg)
	if err != nil {
		slog.Error("mcp upstream fronting disabled: build remote proxy failed", "name", up.Name, "err", err)
		return nil, ""
	}
	slog.Info("mcp fronting registered upstream", "name", up.Name, "transport", up.Transport)
	return frontedIdentityGate{upstream: remote}, up.Name
}

// frontedIdentityGate fails closed like the built-in ToolRegistry: a caller
// with no bound agent identity (none, or a revoked or suspended binding) sees
// no tools and every tools/call is refused with no_identity (-32098) before
// the upstream is dialed. It holds with the policy gate on or off, since the
// gate forwards allowed calls through this same ToolService.
type frontedIdentityGate struct {
	upstream mcp.ToolService
}

func (g frontedIdentityGate) ListTools(ctx context.Context) []mcp.Tool {
	if mcp.IdentityFromContext(ctx) == nil {
		return []mcp.Tool{}
	}
	return g.upstream.ListTools(ctx)
}

func (g frontedIdentityGate) Call(ctx context.Context, name string, params json.RawMessage) (*mcp.ToolCallResult, error) {
	if mcp.IdentityFromContext(ctx) == nil {
		return nil, &mcp.NotAuthorized{Tool: name, SubReason: mcp.DenyReasonNoIdentity}
	}
	return g.upstream.Call(ctx, name, params)
}
