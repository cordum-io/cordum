package gateway

// TestWireActionGatePipeline_TaintFailClosedProfileDefault (task-dddb761f)
// pins the production default of CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE
// through the REAL gateway wiring: s.wireActionGatePipeline() on a test
// gateway, a resolvable MCP identity in s.agentIdentityStore, and a
// descriptor built by mcp.BuildActionDescriptorFromToolCall, the same way
// core/mcp.EvaluateToolCall builds its PolicyInput.
//
// Not parallel: t.Setenv, and wireActionGatePipeline reads the env once.

import (
	"context"
	"os"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
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
		wantHold               bool
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
			s.wireActionGatePipeline()

			dec, fired := runTaintFCCall(t, s, "delete_repo", markTaintLookupFailed)
			if tc.wantHold {
				assertTaintFCDecision(t, "destructive+lookup-failed", dec, fired,
					pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, "taint_lookup_unavailable_failclosed")
			} else if fired {
				t.Fatalf("destructive+lookup-failed: fired %v/%q, want no hold (fail-open)", dec.Decision, dec.SubReason)
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
