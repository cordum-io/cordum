package gateway

// TestWireActionGatePipeline_TaintFailClosedProfileDefault (task-dddb761f)
// pins the production default of CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE
// through the REAL gateway wiring: s.wireActionGatePipeline() on a test
// gateway, a resolvable MCP identity in s.agentIdentityStore, and a
// descriptor built by mcp.BuildActionDescriptorFromToolCall, the same way
// core/mcp.EvaluateToolCall builds its PolicyInput. Every case unsets
// CORDUM_MCP_TAINT_FAILCLOSED_APPROVER, so a call that fails closed is denied.
//
// Not parallel: t.Setenv, and wireActionGatePipeline reads the env once.

import (
	"context"
	"os"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	taintFCTenant = "default"
	taintFCAgent  = "agent-taint-failclosed"
	taintFCServer = "github"
	taintFCFlag   = "CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE"
	// taintFCUnset marks an env var that must be absent, not just empty.
	taintFCUnset = "\x00unset"
)

func TestWireActionGatePipeline_TaintFailClosedProfileDefault(t *testing.T) {
	cases := []struct {
		name, mode, prod, flag string
		wantFailClosed         bool
	}{
		{"production_flag_unset", "production", taintFCUnset, taintFCUnset, true},
		{"prod_flag_unset", "prod", taintFCUnset, taintFCUnset, true},
		{"cordum_production_true_flag_unset", taintFCUnset, "true", taintFCUnset, true},
		{"production_flag_empty", "production", taintFCUnset, "", true},
		{"production_unrecognized_value", "production", taintFCUnset, "maybe", true},
		{"production_explicit_false", "production", taintFCUnset, "false", false},
		{"cordum_production_true_explicit_off", taintFCUnset, "true", "off", false},
		{"development_flag_unset", "development", taintFCUnset, taintFCUnset, false},
		{"all_unset", taintFCUnset, taintFCUnset, taintFCUnset, false},
		{"development_unrecognized_value", "development", taintFCUnset, "maybe", false},
		{"development_explicit_true", "development", taintFCUnset, "true", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			createTaintFCIdentity(t, s)
			setOrUnsetEnv(t, "CORDUM_ENV", tc.mode)
			setOrUnsetEnv(t, "CORDUM_PRODUCTION", tc.prod)
			setOrUnsetEnv(t, taintFCFlag, tc.flag)
			setOrUnsetEnv(t, "CORDUM_MCP_TAINT_FAILCLOSED_APPROVER", taintFCUnset)
			s.wireActionGatePipeline()

			dec, fired := runTaintFCCall(t, s, "delete_repo", markTaintLookupFailed)
			if tc.wantFailClosed {
				assertTaintFCDecision(t, "destructive+lookup-failed", dec, fired,
					pb.DecisionType_DECISION_TYPE_DENY, "taint_lookup_unavailable_failclosed")
			} else if fired {
				t.Fatalf("destructive+lookup-failed: fired %v/%q, want no decision (fail-open)", dec.Decision, dec.SubReason)
			}
			// Control: a non-destructive call is never held by this path.
			if dec, fired := runTaintFCCall(t, s, "list_repos", markTaintLookupFailed); fired {
				t.Fatalf("non-destructive+lookup-failed: fired %v/%q, want no hold", dec.Decision, dec.SubReason)
			}
			// Invariant: a tainted destructive call hard-denies in every mode.
			dec, fired = runTaintFCCall(t, s, "delete_repo", markSessionTainted)
			assertTaintFCDecision(t, "destructive+tainted", dec, fired,
				pb.DecisionType_DECISION_TYPE_DENY, "session_tainted_prompt_injection")
		})
	}
}

// setOrUnsetEnv sets name to value for the test, or removes it when value is
// taintFCUnset. t.Setenv first so the original value is restored on cleanup.
func setOrUnsetEnv(t *testing.T, name, value string) {
	t.Helper()
	t.Setenv(name, "")
	if value != taintFCUnset {
		t.Setenv(name, value)
		return
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}

func createTaintFCIdentity(t *testing.T, s *server) {
	t.Helper()
	_, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:                  taintFCAgent,
		TenantID:            taintFCTenant,
		Name:                "taint-failclosed-agent",
		Owner:               "platform",
		RiskTier:            "high",
		AllowedServers:      []string{taintFCServer},
		AllowedTools:        []string{"delete_repo", "list_repos"},
		DataClassifications: []string{"internal"},
		Status:              "active",
	})
	if err != nil {
		t.Fatalf("create agent identity: %v", err)
	}
}

func markTaintLookupFailed(d *config.ActionDescriptor) { d.TaintLookupFailed = true }

func markSessionTainted(d *config.ActionDescriptor) {
	d.RiskTags = append(d.RiskTags, config.RiskTagSessionPromptInjection)
}

// runTaintFCCall evaluates one MCP tools/call through s.actionGatePipeline.
// stamp applies what EvaluateToolCall's session-taint lookup would record.
func runTaintFCCall(t *testing.T, s *server, tool string, stamp func(*config.ActionDescriptor)) (actiongates.ActionGateDecision, bool) {
	t.Helper()
	meta := mcp.CallMetadata{Tenant: taintFCTenant, Principal: "principal-1", AgentID: taintFCAgent}
	desc, err := mcp.BuildActionDescriptorFromToolCall(meta, mcp.ToolCallParams{Name: tool, Arguments: []byte(`{"repo":"cordum"}`)}, taintFCServer)
	if err != nil {
		t.Fatalf("build descriptor: %v", err)
	}
	stamp(desc)
	ctx := context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{Tenant: taintFCTenant, PrincipalID: meta.Principal})
	return s.actionGatePipeline.Run(ctx, &config.PolicyInput{
		Tenant: meta.Tenant,
		Action: desc,
		Meta:   config.PolicyMeta{ActorID: meta.Principal, AgentID: meta.AgentID},
	})
}

func assertTaintFCDecision(t *testing.T, label string, dec actiongates.ActionGateDecision, fired bool, want pb.DecisionType, wantSub string) {
	t.Helper()
	if !fired || dec.Decision != want || dec.GateID != actiongates.GateIDMCP || dec.SubReason != wantSub {
		t.Fatalf("%s: fired=%v decision=%v gate=%q sub=%q, want %v from %s with %q",
			label, fired, dec.Decision, dec.GateID, dec.SubReason, want, actiongates.GateIDMCP, wantSub)
	}
}

// TestWireActionGatePipeline_TaintLookupFailureDefaults: the gateway parsed
// CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE itself with a default of off, so in
// production a destructive MCP call whose session-taint lookup failed ran as if
// the session were clean. The pipeline now takes the profile-aware default:
// fail closed in production, fail open elsewhere, and an explicit value wins
// either way. Failing closed denies the call unless
// CORDUM_MCP_TAINT_FAILCLOSED_APPROVER says an approver acts on holds.
func TestWireActionGatePipeline_TaintLookupFailureDefaults(t *testing.T) {
	ctx := context.Background()
	identities := store.NewAgentIdentityStoreFromClient(testredis.NewClient(t, miniredis.RunT(t).Addr()))
	agent, err := identities.Create(ctx, store.AgentIdentity{
		ID: "agent-taint", TenantID: "tenant-a", Name: "agent-taint", Owner: "platform",
		RiskTier: "high", AllowedServers: []string{"vault"}, AllowedTools: []string{"delete_*"}, Status: "active",
	})
	if err != nil {
		t.Fatalf("create agent identity: %v", err)
	}
	const unset = pb.DecisionType_DECISION_TYPE_UNSPECIFIED // the pipeline lets the call through
	for _, tc := range []struct {
		name, mode, flag, approver string
		want                       pb.DecisionType
	}{
		{name: "production, no env", mode: "production", want: pb.DecisionType_DECISION_TYPE_DENY},
		{name: "production, approver", mode: "production", approver: "true", want: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{name: "development, no env", mode: "development", want: unset},
		{name: "production, explicit off", mode: "production", flag: "false", want: unset},
		{name: "development, explicit on", mode: "development", flag: "true", want: pb.DecisionType_DECISION_TYPE_DENY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORDUM_ENV", tc.mode)
			t.Setenv("CORDUM_PRODUCTION", "")
			t.Setenv("CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE", tc.flag)
			t.Setenv("CORDUM_MCP_TAINT_FAILCLOSED_APPROVER", tc.approver)
			s := &server{agentIdentityStore: identities}
			s.wireActionGatePipeline()
			in := &config.PolicyInput{
				Action: &config.ActionDescriptor{
					Kind: config.ActionKindMCPCall, Verb: config.ActionVerbRead,
					Server: "vault", Tool: "delete_item", TaintLookupFailed: true,
				},
				Labels: map[string]string{"agent_id": agent.ID},
			}
			authCtx := context.WithValue(ctx, auth.ContextKey{}, &auth.AuthContext{Tenant: "tenant-a", PrincipalID: "p1", Role: "user"})
			dec, fired := s.actionGatePipeline.Run(authCtx, in)
			got := unset
			if fired {
				got = dec.Decision
			}
			if got != tc.want {
				t.Fatalf("decision = %v (sub_reason %q), want %v", got, dec.SubReason, tc.want)
			}
			if fired && dec.SubReason != "taint_lookup_unavailable_failclosed" {
				t.Fatalf("sub_reason = %q, want taint_lookup_unavailable_failclosed", dec.SubReason)
			}
		})
	}
}
