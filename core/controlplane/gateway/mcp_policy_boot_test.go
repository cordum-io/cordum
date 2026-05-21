package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
)

// configurePolicyGateBootDeps wires the gateway-scoped dependencies the
// production MCP policy gate consumes: a real edge.RedisStore (via
// miniredis), a real action-gate pipeline, and the existing miniredis-
// backed artifact store newTestGateway already builds. Tests reuse this
// helper so each boot-wiring assertion exercises the same shape as
// production rather than a hand-rolled fixture.
func configurePolicyGateBootDeps(t *testing.T, s *server) {
	t.Helper()
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	s.wireActionGatePipeline()
}

// setMCPConfigForBoot writes the system-scope config the loader will
// consume at registerMCPRoutes time. Returns once writes have settled
// so the subsequent loadMCPConfig call sees the values.
func setMCPConfigForBoot(t *testing.T, s *server, enabled bool, policyGateEnabled bool) {
	t.Helper()
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":             enabled,
				"transport":           "http",
				"policy_gate_enabled": policyGateEnabled,
			},
		},
	}); err != nil {
		t.Fatalf("set mcp config: %v", err)
	}
}

// TestMCPProdBoot_PolicyGateWiredWhenFlagOn is the boot-wiring
// regression guard demanded by acceptance criteria #1 + #2. With
// `mcp.policy_gate_enabled: true` set in config, the production boot
// path MUST call MCPServer.WithPolicyGate(serverName, deps) against
// the real edge.RedisStore + real artifact store. The previous wiring
// (b02fdafb) constructed BuildMCPPolicyDeps as dead code: the booted
// server's HasPolicyGate() always returned false. This test fails
// against HEAD and passes once Phase 4 wiring lands.
func TestMCPProdBoot_PolicyGateWiredWhenFlagOn(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, true)

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime with non-nil server, got nil")
	}
	if !runtime.server.HasPolicyGate() {
		t.Fatal("MCPServer.HasPolicyGate() = false after boot with policy_gate_enabled=true; want true (boot path must call WithPolicyGate)")
	}
	if name := runtime.server.PolicyServerName(); name == "" {
		t.Fatal("MCPServer.PolicyServerName() empty after wired boot; want non-empty (descriptors need server-derived name)")
	}
	emitter := runtime.server.PolicyEventEmitter()
	if emitter == nil {
		t.Fatal("MCPServer.PolicyEventEmitter() nil after wired boot; want a real edge-store-backed emitter")
	}
	if _, isProd := emitter.(edgeStoreEventEmitter); !isProd {
		t.Fatalf("MCPServer.PolicyEventEmitter() type = %T; want edgeStoreEventEmitter (acceptance criterion #1: production boot must wire the real edge.RedisStore adapter)", emitter)
	}
	artifactStore := runtime.server.PolicyArtifactStore()
	if artifactStore == nil {
		t.Fatal("MCPServer.PolicyArtifactStore() nil after wired boot; want a real artifacts.Store adapter")
	}
	if _, isProd := artifactStore.(productionArtifactStore); !isProd {
		t.Fatalf("MCPServer.PolicyArtifactStore() type = %T; want productionArtifactStore (acceptance criterion #1: production boot must wire the real artifacts.Store adapter)", artifactStore)
	}
}

// TestMCPProdBoot_PolicyGateSkippedWhenFlagOff is the legacy-opt-out
// guard demanded by acceptance criterion #2. With
// `mcp.policy_gate_enabled: false`, the boot path MUST NOT wire the
// policy gate — older deploys keep the direct-dispatch path until
// operators explicitly opt in. Defaults to false in line with
// feedback_dont_change_deployment_defaults so a missing config key
// leaves prod unchanged.
func TestMCPProdBoot_PolicyGateSkippedWhenFlagOff(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, false)

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime with non-nil server, got nil")
	}
	if runtime.server.HasPolicyGate() {
		t.Fatal("MCPServer.HasPolicyGate() = true after boot with policy_gate_enabled=false; want false (legacy opt-out path must be honored)")
	}
	if runtime.server.HasApprovalHold() {
		t.Fatal("MCPServer.HasApprovalHold() = true after boot with policy_gate_enabled=false; the approval hold wiring is gated by the same flag")
	}
}

// TestMCPProdBoot_PolicyGateDefaultsOffWhenFlagUnset asserts the
// safest-default contract: a config that does NOT mention
// `policy_gate_enabled` MUST leave the gate disabled. This protects
// the prod fleet — flipping the gate on requires an explicit operator
// action, never an implicit upgrade.
func TestMCPProdBoot_PolicyGateDefaultsOffWhenFlagUnset(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	configurePolicyGateBootDeps(t, s)
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
				// policy_gate_enabled intentionally omitted
			},
		},
	}); err != nil {
		t.Fatalf("set mcp config: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime with non-nil server, got nil")
	}
	if runtime.server.HasPolicyGate() {
		t.Fatal("MCPServer.HasPolicyGate() = true when flag unset; want false (safe default; explicit opt-in only)")
	}
}

// TestLoadMCPConfigReadsPolicyGateEnabled asserts the config loader
// resolves `mcp.policy_gate_enabled` from the system scope; without
// the loader entry the boot wiring has no signal regardless of how
// operators write the YAML.
func TestLoadMCPConfigReadsPolicyGateEnabled(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)

	cfg := s.loadMCPConfig(context.Background())
	if cfg.PolicyGateEnabled {
		t.Fatal("expected PolicyGateEnabled=false by default, got true")
	}
	setMCPConfigForBoot(t, s, true, true)
	cfg = s.loadMCPConfig(context.Background())
	if !cfg.PolicyGateEnabled {
		t.Fatal("expected PolicyGateEnabled=true after config set, got false (loader is not reading mcp.policy_gate_enabled)")
	}
}

// TestMCPProdBoot_PolicyGateWiredEmitsBootLog asserts the operator-facing
// boot log line lands when the gate is wired. Operators grep for
// `mcp.policy_gate wired` to confirm the gate is live after a rolling
// restart; without this signal a misconfigured deploy is invisible.
//
// Sequential (not t.Parallel) so the global slog.Default() swap does
// not race with sibling parallel tests' logs landing in the same buf.
func TestMCPProdBoot_PolicyGateWiredEmitsBootLog(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, true)
	if err := s.registerMCPRoutes(http.NewServeMux()); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "mcp.policy_gate wired") {
		t.Fatalf("boot log missing 'mcp.policy_gate wired' signal:\n%s", out)
	}
	if !strings.Contains(out, `server_name=cordum.builtin`) {
		t.Fatalf("boot log missing server_name=cordum.builtin:\n%s", out)
	}
	if !strings.Contains(out, "policy_gate_active=true") {
		t.Fatalf("boot log missing policy_gate_active=true:\n%s", out)
	}
}

// TestMCPProdBoot_PolicyGateDegradedEmitsBootLog asserts the third
// terminal state: flag on, but a required production dep is missing
// (here: actionGatePipeline nil) so attachMCPPolicyDeps refuses to
// wire. Operators MUST see `mcp.policy_gate degraded` with a reason
// naming the missing dep — without that signal, a misconfigured
// production deploy would look identical to a healthy one because
// HasPolicyGate() and the previous hard-coded `wired` log line both
// reported success regardless of the actual server state.
//
// Sequential (not t.Parallel) for the same slog-isolation reason.
func TestMCPProdBoot_PolicyGateDegradedEmitsBootLog(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Wire the edge store but deliberately leave actionGatePipeline nil
	// (simulating an upstream wiring bug). The flag is on, so the boot
	// path attempts to wire — and the new actionGatePipeline-nil guard
	// in attachMCPPolicyDeps must trip before reaching WithPolicyGate.
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	// s.actionGatePipeline intentionally left nil.
	setMCPConfigForBoot(t, s, true, true)
	if err := s.registerMCPRoutes(http.NewServeMux()); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime")
	}
	if runtime.server.HasPolicyGate() {
		t.Fatal("HasPolicyGate() = true on degraded wiring; want false (actionGatePipeline nil must abort wiring)")
	}
	out := buf.String()
	if !strings.Contains(out, "mcp.policy_gate degraded") {
		t.Fatalf("boot log missing 'mcp.policy_gate degraded' signal:\n%s", out)
	}
	if !strings.Contains(out, "actionGatePipeline nil") {
		t.Fatalf("boot log missing reason='actionGatePipeline nil':\n%s", out)
	}
	if strings.Contains(out, "mcp.policy_gate wired") {
		t.Fatalf("degraded boot must NOT log 'wired'; saw both lines:\n%s", out)
	}
}

// TestMCPProdBoot_PolicyGateSkippedEmitsBootLog asserts the symmetric
// "skipped" log line lands when the flag is off — without it, operators
// can't distinguish "skipped on purpose" from "wiring crashed silently".
//
// Sequential (not t.Parallel) for the same slog-isolation reason.
func TestMCPProdBoot_PolicyGateSkippedEmitsBootLog(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, false)
	if err := s.registerMCPRoutes(http.NewServeMux()); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "mcp.policy_gate skipped") {
		t.Fatalf("boot log missing 'mcp.policy_gate skipped' signal:\n%s", out)
	}
}

// TestMCPProdBoot_ApprovalHoldWiredWhenFlagOn is the EDGE-103 reopen #1
// boot-wiring regression. QA's prior rejection cited
// `WithApprovalHold never called in handlers_mcp.go startMCPRuntimeFromConfig`
// — the gate was constructed but the production server never had its
// resume path wired, so any `_approval_ref` retry would hit a nil
// approvalHoldDeps and return -32601 method-not-found instead of
// consuming. Commit 30c07614 wired it; this test pins the wiring so a
// future refactor cannot silently re-introduce the dead-path.
func TestMCPProdBoot_ApprovalHoldWiredWhenFlagOn(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, true)

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime with non-nil server, got nil")
	}
	if !runtime.server.HasApprovalHold() {
		t.Fatal("MCPServer.HasApprovalHold() = false after boot with policy_gate_enabled=true; want true (resume path must reach the Edge claim store, not nil approvalHoldDeps)")
	}
}

// Silence the unused-import / non-test reference warnings: ensure the
// mcp accessor names are referenced once at compile time so future
// refactors that rename them break the boot-wiring tests immediately.
var _ = mcp.MCPServer{}
