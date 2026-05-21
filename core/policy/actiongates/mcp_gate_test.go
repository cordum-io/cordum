package actiongates

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// fakeMCPIdentityResolver returns identities by tenant+agent id; unknown
// ids resolve to nil (no identity) so the gate's fail-closed path is
// exercisable.
type fakeMCPIdentityResolver struct {
	by  map[string]*mcp.AgentIdentity // key = tenant + "/" + agentID
	err error
}

func (f *fakeMCPIdentityResolver) ResolveMCPIdentity(_ context.Context, tenant, agentID string) (*mcp.AgentIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	if id, ok := f.by[tenant+"/"+agentID]; ok {
		return id, nil
	}
	return nil, nil
}

// fakeReachability lets tests script per-server reachability + errors.
type fakeReachability struct {
	reachable map[string]bool
	err       error
}

func (f *fakeReachability) MCPServerReachable(_ context.Context, server string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if r, ok := f.reachable[server]; ok {
		return r, nil
	}
	return true, nil
}

const (
	mcpTenantA = "tnt_a"
	mcpAgentA  = "agent_a"
)

func mcpAuthCtx() context.Context {
	return ctxWithAuth(&auth.AuthContext{
		Tenant: mcpTenantA, PrincipalID: "p1", Role: "user",
	})
}

func mcpFullIdentity() *mcp.AgentIdentity {
	return &mcp.AgentIdentity{
		ID:                  mcpAgentA,
		AllowedServers:      []string{"vault", "calendar*"},
		AllowedTools:        []string{"read_*", "list_*"},
		AllowedResources:    []string{"cordum://docs/*", "cordum://calendar/*"},
		Entitlements:        []string{"vault.read", "calendar.read"},
		RiskTier:            "high",
		DataClassifications: []string{"public", "internal"},
	}
}

func mcpInputAction(server, tool string) *config.PolicyInput {
	return &config.PolicyInput{Action: &config.ActionDescriptor{
		Kind:   config.ActionKindMCPCall,
		Verb:   config.ActionVerbRead,
		Server: server,
		Tool:   tool,
	}}
}

func newMCPGateWithIdentity(id *mcp.AgentIdentity, opts ...func(*MCPGateOptions)) *MCPGate {
	resolver := &fakeMCPIdentityResolver{by: map[string]*mcp.AgentIdentity{}}
	if id != nil {
		resolver.by[mcpTenantA+"/"+id.ID] = id
	}
	gopts := MCPGateOptions{Identities: resolver, Reachability: &fakeReachability{}}
	for _, o := range opts {
		o(&gopts)
	}
	return NewMCPGate(gopts)
}

func withAgentIDLabel(in *config.PolicyInput, agentID string) *config.PolicyInput {
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	in.Labels["agent_id"] = agentID
	return in
}

func TestMCPGate_SkipsWhenNoAction(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	dec := gate.Evaluate(mcpAuthCtx(), &config.PolicyInput{})
	if dec.Fired() {
		t.Fatalf("no action: gate fired (decision=%v)", dec.Decision)
	}
}

func TestMCPGate_OtherKindsSkip(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	dec := gate.Evaluate(mcpAuthCtx(), &config.PolicyInput{Action: &config.ActionDescriptor{
		Kind: config.ActionKindFile, TargetPath: "/tmp/x",
	}})
	if dec.Fired() {
		t.Fatalf("file kind: gate fired")
	}
}

func TestMCPGate_UnauthDenies(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(ctxWithAuth(nil), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeUnauthorized {
		t.Fatalf("got %v / %q, want DENY / unauthorized", dec.Decision, dec.Code)
	}
}

func TestMCPGate_NoIdentityResolvedDenies(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(nil) // resolver returns (nil, nil)
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), "unknown_agent")
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeUnauthorized {
		t.Fatalf("got %v / %q, want DENY / unauthorized (no identity)", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "no_identity") {
		t.Fatalf("subReason = %q, want no_identity", dec.SubReason)
	}
}

func TestMCPGate_IdentityResolverErrorFailsClosed(t *testing.T) {
	t.Parallel()
	gate := NewMCPGate(MCPGateOptions{
		Identities:   &fakeMCPIdentityResolver{err: errors.New("redis unreachable")},
		Reachability: &fakeReachability{},
	})
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeInternalError {
		t.Fatalf("got %v / %q, want DENY / internal_error (fail-closed)", dec.Decision, dec.Code)
	}
}

func TestMCPGate_ServerNotAllowlistedDenied(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("billing", "charge"), mcpAgentA) // billing not in AllowedServers
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "server_not_allowlisted") {
		t.Fatalf("subReason = %q, want server_not_allowlisted", dec.SubReason)
	}
}

func TestMCPGate_EmptyAllowedServersDeniesAll(t *testing.T) {
	t.Parallel()
	id := mcpFullIdentity()
	id.AllowedServers = nil // fail-closed
	gate := newMCPGateWithIdentity(id)
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied for empty AllowedServers", dec.Decision, dec.Code)
	}
}

func TestMCPGate_ToolNotAllowlistedDenied(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("vault", "delete_secret"), mcpAgentA) // not in read_*/list_*
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "tool_not_allowlisted") {
		t.Fatalf("subReason = %q, want tool_not_allowlisted", dec.SubReason)
	}
}

// TestMCPGate_ConvergesWithFilterForIdentity asserts that the actiongates
// path and the existing core/mcp.EvaluateForIdentity path agree on tool-
// allowlist denials: both reject the same identity+tool pair.
func TestMCPGate_ConvergesWithFilterForIdentity(t *testing.T) {
	t.Parallel()
	id := mcpFullIdentity()
	tool := mcp.Tool{Name: "delete_secret", RiskTier: "high"}
	reason := mcp.EvaluateForIdentity(tool, id)
	if reason != mcp.DenyReasonNotInAllowedList {
		t.Fatalf("FilterForIdentity reason = %q, want %q", reason, mcp.DenyReasonNotInAllowedList)
	}

	gate := newMCPGateWithIdentity(id)
	in := withAgentIDLabel(mcpInputAction("vault", tool.Name), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || !strings.Contains(dec.SubReason, "tool_not_allowlisted") {
		t.Fatalf("MCPGate disagreed with FilterForIdentity: dec=%v sub=%q", dec.Decision, dec.SubReason)
	}
}

func TestMCPGate_ResourceNotAllowlistedDenied(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	in.Action.TargetURL = "cordum://secrets/db_password" // not in AllowedResources
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "resource_not_allowlisted") {
		t.Fatalf("subReason = %q, want resource_not_allowlisted", dec.SubReason)
	}
}

func TestMCPGate_AllowedResourceMatches(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	in.Action.TargetURL = "cordum://docs/runbook.md"
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("got %v, want ALLOW for allow-listed resource", dec.Decision)
	}
}

func TestMCPGate_DangerousParamsDenied(t *testing.T) {
	t.Parallel()
	customRules := map[string][]DangerousParamRule{
		"*":        {{Name: "force", Value: true}, {Name: "no_confirm", Value: true}},
		"delete_*": {{Name: "recursive", Value: true}},
		"*exec*":   {{Name: "shell", Value: true}},
	}
	cases := []struct {
		name   string
		server string
		tool   string
		args   map[string]any
	}{
		{"force_true", "vault", "read_secret", map[string]any{"force": true}},
		{"no_confirm_true", "vault", "read_secret", map[string]any{"no_confirm": true}},
		{"recursive_on_delete", "vault", "delete_secret_v2", map[string]any{"recursive": true}},
		{"shell_on_exec", "calendar", "shellexec", map[string]any{"shell": true}},
	}
	id := mcpFullIdentity()
	id.AllowedTools = append(id.AllowedTools, "delete_*", "*exec*") // permit so the dangerous-param check is reached
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gate := newMCPGateWithIdentity(id, func(o *MCPGateOptions) { o.DangerousParamRules = customRules })
			in := withAgentIDLabel(mcpInputAction(tc.server, tc.tool), mcpAgentA)
			in.Action.Args = tc.args
			dec := gate.Evaluate(mcpAuthCtx(), in)
			if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
				t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
			}
			if !strings.Contains(dec.SubReason, "dangerous_param") {
				t.Fatalf("subReason = %q, want dangerous_param", dec.SubReason)
			}
		})
	}
}

func TestMCPGate_DangerousParamRule_TypedValueMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		param string
		value any
		args  string
	}{
		{"int_rule", "force_overwrite", int(1), `{"force_overwrite":1}`},
		{"int64_rule", "delete_depth", int64(42), `{"delete_depth":42}`},
		{"json_number_rule", "risk_score", json.Number("7"), `{"risk_score":7}`},
		{
			name:  "composite_json_shape",
			param: "selector",
			value: struct {
				Mode  string `json:"mode"`
				Count int    `json:"count"`
			}{Mode: "force", Count: 2},
			args: `{"selector":{"mode":"force","count":2}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := mcpFullIdentity()
			id.AllowedTools = append(id.AllowedTools, "delete_*")
			rules := map[string][]DangerousParamRule{"*": {{Name: tc.param, Value: tc.value}}}
			gate := newMCPGateWithIdentity(id, func(o *MCPGateOptions) { o.DangerousParamRules = rules })
			in := withAgentIDLabel(mcpInputAction("vault", "delete_records"), mcpAgentA)
			in.Action.Args = mustJSONArgs(t, tc.args)

			dec := gate.Evaluate(mcpAuthCtx(), in)
			if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
				t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
			}
			if !strings.Contains(dec.SubReason, "dangerous_param:"+tc.param) {
				t.Fatalf("subReason = %q, want dangerous_param:%s", dec.SubReason, tc.param)
			}
		})
	}
}

func mustJSONArgs(t *testing.T, raw string) map[string]any {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("decode args %s: %v", raw, err)
	}
	return args
}

func TestMCPGate_UnlicensedDenied(t *testing.T) {
	t.Parallel()
	id := mcpFullIdentity()
	id.Entitlements = []string{"calendar.read"} // missing vault.read
	gate := newMCPGateWithIdentity(id)
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	in.Action.RequiredEntitlement = "vault.read"
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "unlicensed") {
		t.Fatalf("subReason = %q, want unlicensed", dec.SubReason)
	}
}

func TestMCPGate_ServerUnavailable(t *testing.T) {
	t.Parallel()
	id := mcpFullIdentity()
	reach := &fakeReachability{reachable: map[string]bool{"vault": false}}
	gate := NewMCPGate(MCPGateOptions{
		Identities:   &fakeMCPIdentityResolver{by: map[string]*mcp.AgentIdentity{mcpTenantA + "/" + mcpAgentA: id}},
		Reachability: reach,
	})
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeServiceUnavailable {
		t.Fatalf("got %v / %q, want DENY / service_unavailable", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "server_unreachable") {
		t.Fatalf("subReason = %q, want server_unreachable", dec.SubReason)
	}
}

func TestMCPGate_ProbeErrFailsClosed(t *testing.T) {
	t.Parallel()
	id := mcpFullIdentity()
	reach := &fakeReachability{err: errors.New("probe timed out")}
	gate := NewMCPGate(MCPGateOptions{
		Identities:   &fakeMCPIdentityResolver{by: map[string]*mcp.AgentIdentity{mcpTenantA + "/" + mcpAgentA: id}},
		Reachability: reach,
	})
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeServiceUnavailable {
		t.Fatalf("got %v / %q, want DENY / service_unavailable on probe err", dec.Decision, dec.Code)
	}
}

func TestMCPGate_AllowValid(t *testing.T) {
	t.Parallel()
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	in.Action.RequiredEntitlement = "vault.read"
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("got %v, want ALLOW for allow-listed valid call", dec.Decision)
	}
}

func TestMCPGate_NoReachabilityProbeAllows(t *testing.T) {
	t.Parallel()
	// nil probe means "skip reachability gate"; the test ensures the gate
	// does not crash on a nil probe.
	gate := NewMCPGate(MCPGateOptions{
		Identities:   &fakeMCPIdentityResolver{by: map[string]*mcp.AgentIdentity{mcpTenantA + "/" + mcpAgentA: mcpFullIdentity()}},
		Reachability: nil,
	})
	in := withAgentIDLabel(mcpInputAction("vault", "read_secret"), mcpAgentA)
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("got %v, want ALLOW with nil probe", dec.Decision)
	}
}

func TestMCPGate_MissingAgentLabelDenies(t *testing.T) {
	t.Parallel()
	// No agent_id label -> cannot resolve identity -> fail-closed.
	gate := newMCPGateWithIdentity(mcpFullIdentity())
	in := mcpInputAction("vault", "read_secret") // no agent_id label
	dec := gate.Evaluate(mcpAuthCtx(), in)
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeUnauthorized {
		t.Fatalf("got %v / %q, want DENY / unauthorized (missing agent_id)", dec.Decision, dec.Code)
	}
}
