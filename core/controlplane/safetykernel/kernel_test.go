package safetykernel

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

type fakeConfigChangeBus struct {
	subscribeCalls    int
	lastHandler       func(*pb.BusPacket) error
	reconnectHandler  func(*nats.Conn)
	disconnectHandler func(*nats.Conn, error)
}

func (f *fakeConfigChangeBus) ReplaceSubscription(_ *nats.Subscription, _ string, _ string, handler func(*pb.BusPacket) error) (*nats.Subscription, error) {
	f.subscribeCalls++
	f.lastHandler = handler
	return &nats.Subscription{}, nil
}

func (f *fakeConfigChangeBus) AddReconnectHandler(handler func(*nats.Conn)) {
	f.reconnectHandler = handler
}

func (f *fakeConfigChangeBus) AddDisconnectHandler(handler func(*nats.Conn, error)) {
	f.disconnectHandler = handler
}

func TestCheckMCPPolicyDenies(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					DenyServers: []string{"blocked.example.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.server": "blocked.example.com",
			"mcp.tool":   "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
}

func TestCheckMCPPolicyRequiresFieldWhenAllowlistSet(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					AllowServers: []string{"github.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-2",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.tool": "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny when mcp.server missing, got %v", resp.GetDecision())
	}
}

func TestCheckInputScopeRuleDeniesWhenContentMissing(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		InputRules: []config.InputPolicyRule{
			{
				ID:       "visa-tx2-scope-native",
				Severity: "high",
				Decision: "deny",
				Reason:   "scope violation requires structured content",
				Match: config.InputPolicyMatch{
					Topics: []string{"job.visa-governance.evaluate"},
					Scope: &config.ScopeConfig{
						InstructionPath: "instruction",
						ItemsPath:       "items",
						CategoryPath:    "category",
						NamePath:        "name",
						AllowedCategories: map[string][]string{
							"headphones": {"headphones"},
						},
						OnMissingInput: "deny",
						OnAmbiguous:    "deny",
					},
				},
			},
		},
	}

	srv := &server{
		policy:     policy,
		inputRules: compileInputRules(policy),
		scanners:   loadOutputScanners(),
		snapshot:   "test",
	}

	resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.visa-governance.evaluate",
		Tenant: "default",
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "structured content") {
		t.Fatalf("expected missing-content reason, got %q", resp.GetReason())
	}
	if got := resp.GetRuleId(); got != "visa-tx2-scope-native" {
		t.Fatalf("expected rule id visa-tx2-scope-native, got %q", got)
	}
}

func TestCheckReturnsRemediations(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Rules: []config.PolicyRule{
			{
				ID:       "deny-delete",
				Decision: "deny",
				Match: config.PolicyMatch{
					Tenants: []string{"default"},
					Topics:  []string{"job.db.delete"},
				},
				Remediations: []config.PolicyRemediation{
					{
						ID:               "archive",
						Title:            "Archive instead",
						Summary:          "Use archive flow for retention",
						ReplacementTopic: "job.db.archive",
					},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-5",
		Topic:  "job.db.delete",
		Tenant: "default",
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(resp.GetRemediations()) != 1 {
		t.Fatalf("expected remediation, got %d", len(resp.GetRemediations()))
	}
	if resp.GetRemediations()[0].GetReplacementTopic() != "job.db.archive" {
		t.Fatalf("unexpected remediation topic")
	}
}

func TestCheckAppliesEffectiveConfigDeny(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:           "job-3",
		Topic:           "job.deny",
		Tenant:          "default",
		EffectiveConfig: []byte(`{"safety":{"denied_topics":["job.deny"]}}`),
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "denied") {
		t.Fatalf("expected denial reason, got %q", resp.GetReason())
	}
}

func TestExtractPolicyFragmentHonorsEnabled(t *testing.T) {
	if content, ok := extractPolicyFragment(map[string]any{"content": "foo", "enabled": true}); !ok || content != "foo" {
		t.Fatalf("expected enabled content")
	}
	if content, ok := extractPolicyFragment(map[string]any{"content": "bar", "enabled": false}); ok || content != "" {
		t.Fatalf("expected disabled fragment")
	}
}

func TestPolicyLoaderLoadsFragments(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = svc.Close() }()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"alpha": `
default_tenant: default
tenants:
  default:
    allow_topics:
      - job.*
`,
				"beta": map[string]any{
					"content": `
rules:
  - id: require-prod
    match:
      topics:
        - job.prod.*
    decision: require_approval
    reason: prod writes
`,
				},
				"disabled": map[string]any{
					"content": "tenants:\n  default:\n    deny_topics:\n      - job.disabled\n",
					"enabled": false,
				},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config doc: %v", err)
	}

	loader := &policyLoader{
		configSvc:   svc,
		configScope: configsvc.ScopeSystem,
		configID:    "policy",
		configKey:   "bundles",
	}
	policy, snapshot, _, err := loader.loadFragments(context.Background())
	if err != nil {
		t.Fatalf("load fragments: %v", err)
	}
	if policy == nil {
		t.Fatalf("expected policy")
	}
	if snapshot == "" {
		t.Fatalf("expected snapshot hash")
	}
	resp := policy.Evaluate(config.PolicyInput{Tenant: "default", Topic: "job.prod.test"})
	if resp.Decision != "require_approval" {
		t.Fatalf("expected require_approval got %q", resp.Decision)
	}
}

func TestPolicyLoaderRejectsInvalidFragments(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = svc.Close() }()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"valid": `
rules:
  - id: require-ops
    match:
      topics:
        - job.ops.*
    decision: require_approval
    reason: ops guardrail
`,
				"invalid": `
default_decision: maybe
`,
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config doc: %v", err)
	}

	loader := &policyLoader{
		configSvc:   svc,
		configScope: configsvc.ScopeSystem,
		configID:    "policy",
		configKey:   "bundles",
	}
	policy, snapshot, _, err := loader.loadFragments(context.Background())
	// Malformed fragments are now skipped instead of failing all
	if err != nil {
		t.Fatalf("expected no error (malformed fragments should be skipped): %v", err)
	}
	// The valid fragment should still be loaded
	if policy == nil {
		t.Fatalf("expected policy from valid fragment to be loaded")
	}
	if snapshot == "" {
		t.Fatalf("expected snapshot hash from valid fragment")
	}
	// Verify the valid fragment's rule is present
	resp := policy.Evaluate(config.PolicyInput{Tenant: "default", Topic: "job.ops.deploy"})
	if resp.Decision != "require_approval" {
		t.Fatalf("expected require_approval from valid fragment, got %q", resp.Decision)
	}
}

func TestEvaluateExplainSimulate(t *testing.T) {
	srv := &server{}
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}
	srv.setPolicy(policy, "snap-1")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-9",
		Topic:  "job.test",
		Tenant: "default",
	}

	if resp, err := srv.Evaluate(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("evaluate expected allow: resp=%v err=%v", resp, err)
	}
	if resp, err := srv.Explain(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("explain expected allow: resp=%v err=%v", resp, err)
	}
	if resp, err := srv.Simulate(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("simulate expected allow: resp=%v err=%v", resp, err)
	}
}

func TestListSnapshotsTracksHistory(t *testing.T) {
	srv := &server{}
	for i := 0; i < 12; i++ {
		srv.setPolicy(nil, fmt.Sprintf("snap-%d", i))
	}

	resp, err := srv.ListSnapshots(context.Background(), &pb.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(resp.Snapshots) != 10 {
		t.Fatalf("expected 10 snapshots, got %d", len(resp.Snapshots))
	}
	if resp.Snapshots[0] != "snap-11" {
		t.Fatalf("expected latest snapshot first, got %s", resp.Snapshots[0])
	}
}

func TestEvaluateMissingTopic(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{DefaultTenant: "default"}}
	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{})
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny on missing topic")
	}
	if resp.GetReason() == "" {
		t.Fatalf("expected reason on missing topic")
	}
}

func TestPolicyLoaderFromSource(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/policy.yaml"
	content := []byte("default_tenant: default\ntenants:\n  default:\n    allow_topics:\n      - job.*\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	loader := &policyLoader{source: path}
	policy, snapshot, _, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if policy == nil || snapshot == "" {
		t.Fatalf("expected policy and snapshot")
	}
}

func TestNewPolicyLoaderDefaults(t *testing.T) {
	t.Setenv("SAFETY_POLICY_CONFIG_DISABLE", "1")
	loader := newPolicyLoader(nil, "", nil)
	if loader.configSvc != nil {
		t.Fatalf("expected config service disabled")
	}
	if loader.ShouldWatch() {
		t.Fatalf("expected ShouldWatch false for empty loader")
	}
	loader.Close()

	loader = newPolicyLoader(nil, "/tmp/policy.yaml", nil)
	if !loader.ShouldWatch() {
		t.Fatalf("expected ShouldWatch true when source set")
	}
}

func TestVerifyPolicySignatureRejectsInvalidPublicKeyLength(t *testing.T) {
	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize-1)))
	t.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("verifyPolicySignature panicked: %v", r)
		}
	}()

	err := verifyPolicySignature([]byte("data"), "policy.yaml")
	if err == nil {
		t.Fatalf("expected error for invalid public key length")
	}
	if !strings.Contains(err.Error(), "SAFETY_POLICY_PUBLIC_KEY length") {
		t.Fatalf("expected public key length error, got %v", err)
	}
}

func TestVerifyPolicySignatureRejectsInvalidSignatureLength(t *testing.T) {
	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)))
	t.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize-1)))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("verifyPolicySignature panicked: %v", r)
		}
	}()

	err := verifyPolicySignature([]byte("data"), "policy.yaml")
	if err == nil {
		t.Fatalf("expected error for invalid signature length")
	}
	if !strings.Contains(err.Error(), "policy signature length") {
		t.Fatalf("expected signature length error, got %v", err)
	}
}

func TestWatchPolicy_ContextCancel(t *testing.T) {
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "50ms")

	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"
	if err := os.WriteFile(policyPath, []byte("default_tenant: default\ntenants:\n  default:\n    allow_topics:\n      - job.*\n"), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	loader := &policyLoader{source: policyPath}

	srv := &server{}
	srv.setPolicy(&config.SafetyPolicy{}, "initial")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.watchPolicy(ctx, loader, nil)
		close(done)
	}()

	// Cancel context — watchPolicy must exit promptly.
	cancel()
	select {
	case <-done:
		// Success: goroutine exited.
	case <-time.After(2 * time.Second):
		t.Fatal("watchPolicy did not exit after context cancellation (goroutine leak)")
	}
}

func TestWatchPolicyNotificationTrigger(t *testing.T) {
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "1h")

	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"
	if err := os.WriteFile(policyPath, []byte("default_tenant: default\ntenants:\n  default:\n    allow_topics:\n      - job.default.*\n"), 0o600); err != nil {
		t.Fatalf("write initial policy: %v", err)
	}

	srv := &server{}
	srv.setPolicy(&config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.default.*"}},
		},
	}, "initial-snapshot")

	loader := &policyLoader{source: policyPath}
	notifyCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.watchPolicy(ctx, loader, notifyCh)
		close(done)
	}()

	if err := os.WriteFile(policyPath, []byte("default_tenant: updated\ntenants:\n  updated:\n    allow_topics:\n      - job.updated.*\n"), 0o600); err != nil {
		cancel()
		<-done
		t.Fatalf("write updated policy: %v", err)
	}

	notifyCh <- struct{}{}

	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		currentPolicy := srv.policy
		currentSnapshot := srv.snapshot
		srv.mu.RUnlock()
		if currentPolicy != nil && currentPolicy.DefaultTenant == "updated" && currentSnapshot != "initial-snapshot" {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("watchPolicy did not exit after notification test cancellation")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchPolicy did not exit after notification timeout cancellation")
	}

	srv.mu.RLock()
	currentPolicy := srv.policy
	currentSnapshot := srv.snapshot
	srv.mu.RUnlock()
	if currentPolicy == nil {
		t.Fatal("expected policy to be loaded after notification")
	}
	t.Fatalf("expected notification-triggered reload to update policy within deadline, got tenant=%q snapshot=%q", currentPolicy.DefaultTenant, currentSnapshot)
}

func TestRegisterConfigChangeNotificationsResubscribesOnReconnect(t *testing.T) {
	fakeBus := &fakeConfigChangeBus{}
	notifyCh := make(chan struct{}, 1)

	registerConfigChangeNotifications(fakeBus, notifyCh)

	if fakeBus.subscribeCalls != 1 {
		t.Fatalf("expected initial subscription, got %d", fakeBus.subscribeCalls)
	}
	if fakeBus.reconnectHandler == nil {
		t.Fatal("expected reconnect handler to be registered")
	}
	if fakeBus.disconnectHandler == nil {
		t.Fatal("expected disconnect handler to be registered")
	}
	if fakeBus.lastHandler == nil {
		t.Fatal("expected subscription callback to be installed")
	}

	fakeBus.reconnectHandler(nil)
	if fakeBus.subscribeCalls != 2 {
		t.Fatalf("expected reconnect to re-subscribe, got %d subscriptions", fakeBus.subscribeCalls)
	}

	if err := fakeBus.lastHandler(&pb.BusPacket{}); err != nil {
		t.Fatalf("expected config callback to return nil, got %v", err)
	}
	select {
	case <-notifyCh:
	default:
		t.Fatal("expected config callback to notify policy watcher")
	}
}

func TestRegisterConfigChangeNotificationsRecoversFromClosedChannel(t *testing.T) {
	fakeBus := &fakeConfigChangeBus{}
	notifyCh := make(chan struct{})
	close(notifyCh)

	registerConfigChangeNotifications(fakeBus, notifyCh)
	if fakeBus.lastHandler == nil {
		t.Fatal("expected subscription callback to be installed")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("config callback panicked: %v", r)
		}
	}()
	if err := fakeBus.lastHandler(&pb.BusPacket{}); err != nil {
		t.Fatalf("expected recovered callback to return nil, got %v", err)
	}
}

func newTestRedisServer(t *testing.T) (*server, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(mr.Close)
	rc, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return &server{resultClient: rc}, mr
}

func TestSnapshotHistoryRedis(t *testing.T) {
	srv, mr := newTestRedisServer(t)

	srv.setPolicy(nil, "snap-a")
	srv.setPolicy(nil, "snap-b")
	srv.setPolicy(nil, "snap-c")

	// Verify Redis has 3 entries in newest-first order.
	vals, err := mr.List(snapshotHistoryKey)
	if err != nil {
		t.Fatalf("redis LRANGE: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 snapshots in Redis, got %d", len(vals))
	}
	if vals[0] != "snap-c" || vals[1] != "snap-b" || vals[2] != "snap-a" {
		t.Fatalf("unexpected order: %v", vals)
	}

	// Verify ListSnapshots reads from Redis.
	resp, err := srv.ListSnapshots(context.Background(), &pb.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(resp.Snapshots) != 3 {
		t.Fatalf("expected 3 snapshots from ListSnapshots, got %d", len(resp.Snapshots))
	}
	if resp.Snapshots[0] != "snap-c" {
		t.Fatalf("expected newest first, got %s", resp.Snapshots[0])
	}
}

func TestSnapshotHistoryTrim(t *testing.T) {
	srv, mr := newTestRedisServer(t)

	for i := 0; i < 12; i++ {
		srv.setPolicy(nil, fmt.Sprintf("snap-%d", i))
	}

	// Verify Redis list trimmed to 10.
	vals, err := mr.List(snapshotHistoryKey)
	if err != nil {
		t.Fatalf("redis LRANGE: %v", err)
	}
	if len(vals) != 10 {
		t.Fatalf("expected 10 snapshots in Redis after LTRIM, got %d", len(vals))
	}
	// Newest (snap-11) should be first, oldest kept (snap-2) last.
	if vals[0] != "snap-11" {
		t.Fatalf("expected snap-11 first, got %s", vals[0])
	}
	if vals[9] != "snap-2" {
		t.Fatalf("expected snap-2 last, got %s", vals[9])
	}
}

func TestSnapshotHistoryRedisFallback(t *testing.T) {
	// No Redis — local slice is the fallback.
	srv := &server{}
	srv.setPolicy(nil, "local-a")
	srv.setPolicy(nil, "local-b")

	resp, err := srv.ListSnapshots(context.Background(), &pb.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(resp.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots from local fallback, got %d", len(resp.Snapshots))
	}
	if resp.Snapshots[0] != "local-b" {
		t.Fatalf("expected newest first in fallback, got %s", resp.Snapshots[0])
	}
}

func TestSnapshotHistoryOrder(t *testing.T) {
	srv, _ := newTestRedisServer(t)

	srv.setPolicy(nil, "first")
	srv.setPolicy(nil, "second")
	srv.setPolicy(nil, "third")

	resp, err := srv.ListSnapshots(context.Background(), &pb.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	expected := []string{"third", "second", "first"}
	if len(resp.Snapshots) != len(expected) {
		t.Fatalf("expected %d snapshots, got %d", len(expected), len(resp.Snapshots))
	}
	for i, want := range expected {
		if resp.Snapshots[i] != want {
			t.Fatalf("snapshot[%d] = %q, want %q", i, resp.Snapshots[i], want)
		}
	}
}

// ---------------------------------------------------------------------------
// Security regression tests: fail-open, policy fetch, signature enforcement
// ---------------------------------------------------------------------------

func TestEvaluateNilPolicyDeniesFailClosed(t *testing.T) {
	// BUG FIX REGRESSION: When policy is nil (no policy source configured),
	// the safety kernel must deny requests (fail-closed), not allow them.
	srv := &server{}
	// No policy set — s.policy is nil.

	req := &pb.PolicyCheckRequest{
		JobId:  "job-nil-policy",
		Topic:  "job.test",
		Tenant: "default",
	}
	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("FAIL-OPEN BUG: nil policy should deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "no policy loaded") {
		t.Fatalf("expected 'no policy loaded' reason, got %q", resp.GetReason())
	}
}

func TestEvaluateNilPolicyAfterSetPolicyNilDenies(t *testing.T) {
	// Explicitly calling setPolicy(nil, "") should result in deny.
	srv := &server{}
	srv.setPolicy(nil, "")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-explicit-nil",
		Topic:  "job.test",
		Tenant: "default",
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("FAIL-OPEN BUG: setPolicy(nil) should deny, got %v", resp.GetDecision())
	}
}

func TestEvaluateAllErrorClassesDeny(t *testing.T) {
	// All error classes must result in deny (fail-closed) decisions,
	// never allow.
	tests := []struct {
		name   string
		policy *config.SafetyPolicy
	}{
		{"nil policy", nil},
		{"empty policy no rules", &config.SafetyPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{}
			if tt.policy != nil {
				srv.setPolicy(tt.policy, "test")
			}
			req := &pb.PolicyCheckRequest{
				JobId:  "job-err",
				Topic:  "job.test",
				Tenant: "default",
			}
			resp, err := srv.Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("evaluate error: %v", err)
			}
			if resp.GetDecision() == pb.DecisionType_DECISION_TYPE_ALLOW {
				t.Fatalf("FAIL-OPEN: %s should not allow, got %v", tt.name, resp.GetDecision())
			}
		})
	}
}

func TestFetchPolicyURLRejectsHTTPInProduction(t *testing.T) {
	// In production mode, plaintext HTTP policy URLs must be rejected
	// to prevent MITM injection of malicious policies.
	t.Setenv("CORDUM_PRODUCTION", "1")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1") // separate concern

	_, err := fetchPolicyURL("http://example.com/policy.yaml")
	if err == nil {
		t.Fatalf("expected HTTP to be rejected in production mode")
	}
	if !strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("expected HTTPS requirement error, got %v", err)
	}
}

func TestFetchPolicyURLAllowsHTTPInDev(t *testing.T) {
	// In non-production mode, HTTP should still work for development.
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "dev")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1")

	// This will fail to connect (no server), but should NOT fail with scheme error.
	_, err := fetchPolicyURL("http://127.0.0.1:19999/policy.yaml")
	if err != nil && strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("HTTP should be allowed in dev mode, got: %v", err)
	}
}

func TestFetchPolicyURLAllowsHTTPS(t *testing.T) {
	// HTTPS should always be accepted regardless of mode.
	t.Setenv("CORDUM_PRODUCTION", "1")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1")

	// This will fail to connect, but should NOT fail with scheme error.
	_, err := fetchPolicyURL("https://127.0.0.1:19999/policy.yaml")
	if err != nil && strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("HTTPS should always be allowed, got: %v", err)
	}
}

func TestValidatePolicyURLPrivateIPVariants(t *testing.T) {
	// Comprehensive private IP blocking test matrix.
	privateAddrs := []string{
		"http://127.0.0.1/policy",
		"http://10.0.0.1/policy",
		"http://172.16.0.1/policy",
		"http://192.168.1.1/policy",
		"http://[::1]/policy",
		"http://0.0.0.0/policy",
		"http://localhost/policy",
		"http://[fe80::1]/policy",       // link-local
		"http://169.254.169.254/policy", // AWS metadata
	}
	for _, addr := range privateAddrs {
		t.Run(addr, func(t *testing.T) {
			// Override DNS lookback for hostname tests
			origLookup := policyLookupIP
			t.Cleanup(func() { policyLookupIP = origLookup })
			policyLookupIP = func(host string) ([]net.IP, error) {
				switch host {
				case "localhost":
					return []net.IP{net.ParseIP("127.0.0.1")}, nil
				default:
					return net.LookupIP(host)
				}
			}

			u, err := url.Parse(addr)
			if err != nil {
				t.Skipf("parse error: %v", err)
			}
			if err := validatePolicyURL(u); err == nil {
				t.Fatalf("expected private IP %s to be blocked", addr)
			}
		})
	}
}

func TestValidatePolicyURLAllowlistEnforced(t *testing.T) {
	t.Setenv("SAFETY_POLICY_URL_ALLOWLIST", "trusted.example.com")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1") // separate concern

	// Allowed host should pass.
	u, _ := url.Parse("https://trusted.example.com/policy")
	if err := validatePolicyURL(u); err != nil {
		t.Fatalf("expected allowlisted host to pass: %v", err)
	}

	// Non-allowed host should fail.
	u2, _ := url.Parse("https://evil.attacker.com/policy")
	if err := validatePolicyURL(u2); err == nil {
		t.Fatalf("expected non-allowlisted host to be blocked")
	}

	// Subdomain of allowlisted host should pass.
	u3, _ := url.Parse("https://api.trusted.example.com/policy")
	if err := validatePolicyURL(u3); err != nil {
		t.Fatalf("expected subdomain of allowlisted host to pass: %v", err)
	}
}

func TestPolicyFetchRedirectToPrivateBlocked(t *testing.T) {
	// Redirect chain: public URL -> private IP should be blocked.
	origLookup := policyLookupIP
	t.Cleanup(func() { policyLookupIP = origLookup })

	// First call resolves to public, subsequent calls resolve to private (DNS rebinding).
	callCount := 0
	policyLookupIP = func(host string) ([]net.IP, error) {
		callCount++
		if callCount <= 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		// DNS rebinding: second resolution returns private IP.
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	_, err := fetchPolicyURL("http://attacker.example.com/policy")
	if err == nil {
		t.Fatalf("expected DNS rebinding to be blocked")
	}
}

func TestLoadPolicyBundleEmptySourceReturnsNil(t *testing.T) {
	// Empty source should return nil policy, empty snapshot, no error.
	policy, snapshot, err := loadPolicyBundle("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy != nil {
		t.Fatalf("expected nil policy for empty source")
	}
	if snapshot != "" {
		t.Fatalf("expected empty snapshot for empty source")
	}
}

func TestEvaluateDecisionReasonMetadata(t *testing.T) {
	// Every deny/approval response must include actionable reason metadata
	// for observability and incident triage.
	tests := []struct {
		name           string
		policy         *config.SafetyPolicy
		req            *pb.PolicyCheckRequest
		expectDecision pb.DecisionType
		expectReason   string // substring that must be present
		expectSnapshot string
	}{
		{
			name:           "nil policy — reason must say no policy loaded",
			policy:         nil,
			req:            &pb.PolicyCheckRequest{JobId: "j1", Topic: "job.test"},
			expectDecision: pb.DecisionType_DECISION_TYPE_DENY,
			expectReason:   "no policy loaded",
			expectSnapshot: "", // no setPolicy called, snapshot is empty
		},
		{
			name: "missing topic — reason says missing topic",
			policy: &config.SafetyPolicy{
				DefaultTenant: "default",
			},
			req:            &pb.PolicyCheckRequest{JobId: "j2"},
			expectDecision: pb.DecisionType_DECISION_TYPE_DENY,
			expectReason:   "missing topic",
		},
		{
			name: "unsupported topic — reason says unsupported",
			policy: &config.SafetyPolicy{
				DefaultTenant: "default",
			},
			req:            &pb.PolicyCheckRequest{JobId: "j3", Topic: "event.custom"},
			expectDecision: pb.DecisionType_DECISION_TYPE_DENY,
			expectReason:   "unsupported topic",
		},
		{
			name: "default deny — reason says no matching rule",
			policy: &config.SafetyPolicy{
				DefaultDecision: "deny",
				Rules: []config.PolicyRule{
					{ID: "r1", Decision: "allow", Match: config.PolicyMatch{Topics: []string{"job.allowed"}}},
				},
			},
			req:            &pb.PolicyCheckRequest{JobId: "j4", Topic: "job.other", Tenant: "default"},
			expectDecision: pb.DecisionType_DECISION_TYPE_DENY,
			expectReason:   "no matching rule",
		},
		{
			name: "effective config deny — reason says denied by effective config",
			policy: &config.SafetyPolicy{
				DefaultDecision: "allow",
				DefaultTenant:   "default",
			},
			req: &pb.PolicyCheckRequest{
				JobId:           "j5",
				Topic:           "job.test",
				Tenant:          "default",
				EffectiveConfig: []byte(`{"safety":{"denied_topics":["job.test"]}}`),
			},
			expectDecision: pb.DecisionType_DECISION_TYPE_DENY,
			expectReason:   "denied by effective config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{}
			snap := "snap-meta"
			if tt.policy != nil {
				srv.setPolicy(tt.policy, snap)
			}
			resp, err := srv.evaluate(context.Background(), tt.req, "check")
			if err != nil {
				t.Fatalf("evaluate error: %v", err)
			}
			if resp.GetDecision() != tt.expectDecision {
				t.Fatalf("expected %v, got %v", tt.expectDecision, resp.GetDecision())
			}
			if !strings.Contains(resp.GetReason(), tt.expectReason) {
				t.Fatalf("expected reason containing %q, got %q", tt.expectReason, resp.GetReason())
			}
			if tt.expectSnapshot != "" && resp.GetPolicySnapshot() != tt.expectSnapshot {
				t.Fatalf("expected snapshot %q, got %q", tt.expectSnapshot, resp.GetPolicySnapshot())
			}
		})
	}
}

func TestEvaluateNilPolicyBeforeTopicValidation(t *testing.T) {
	// Nil-policy denial must occur BEFORE topic validation to prevent
	// information leakage about request structure when policy is absent.
	srv := &server{}

	// Empty topic with nil policy: should say "no policy loaded", NOT "missing topic".
	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{})
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !strings.Contains(resp.GetReason(), "no policy loaded") {
		t.Fatalf("expected 'no policy loaded' before topic validation, got %q", resp.GetReason())
	}
}

func TestCachedDecisionNotServedAfterPolicyNil(t *testing.T) {
	// If a policy was active, decisions were cached, and then policy becomes nil
	// (e.g. via corrupted reload that sets nil), cached allow decisions must NOT
	// be served. The version check in getCachedDecision handles this.
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}

	// Load policy, evaluate to populate cache.
	policy := &config.SafetyPolicy{
		DefaultDecision: "allow",
		DefaultTenant:   "default",
	}
	srv.setPolicy(policy, "snap-cached")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-cached",
		Topic:  "job.test",
		Tenant: "default",
	}
	resp1, _ := srv.evaluate(context.Background(), req, "check")
	if resp1.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected allow with policy, got %v", resp1.GetDecision())
	}

	// Simulate policy becoming nil (setPolicy clears cache).
	srv.mu.Lock()
	srv.policy = nil
	srv.mu.Unlock()
	srv.policyVersion.Add(1) // Bump version to invalidate cache entries

	// New evaluation must deny (nil policy), not serve stale cached allow.
	resp2, _ := srv.evaluate(context.Background(), req, "check")
	if resp2.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny after policy nil, got %v (stale cache?)", resp2.GetDecision())
	}
	if !strings.Contains(resp2.GetReason(), "no policy loaded") {
		t.Fatalf("expected 'no policy loaded' reason, got %q", resp2.GetReason())
	}
}

func TestEvaluatePanicRecoveryReturnsDeny(t *testing.T) {
	// SECURITY: If policy.Evaluate() panics (e.g., malformed topic causes regex panic),
	// the safety kernel must return DENY (fail-closed), not ALLOW (fail-open).
	srv := &server{}
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}
	srv.setPolicy(policy, "snap-panic")

	// Inject a panic via the test hook.
	origHook := policyEvalTestHook
	policyEvalTestHook = func() { panic("simulated policy evaluation panic") }
	t.Cleanup(func() { policyEvalTestHook = origHook })

	req := &pb.PolicyCheckRequest{
		JobId:  "job-panic",
		Topic:  "job.test",
		Tenant: "default",
	}

	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("evaluate should not return error on panic recovery: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("FAIL-OPEN BUG: panic during evaluation should deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "policy evaluation panic") {
		t.Fatalf("expected panic reason, got %q", resp.GetReason())
	}
	if !strings.Contains(resp.GetReason(), "simulated") {
		t.Fatalf("expected panic value in reason, got %q", resp.GetReason())
	}
}

func TestEvaluatePanicRecoveryWithNilMapAccess(t *testing.T) {
	// Simulate a nil map access panic (a realistic panic scenario).
	srv := &server{}
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}
	srv.setPolicy(policy, "snap-nilmap")

	origHook := policyEvalTestHook
	policyEvalTestHook = func() {
		var m map[string]string
		_ = m["trigger"] // safe — won't panic
		// Force a nil pointer dereference to simulate realistic panic.
		var p *config.SafetyPolicy
		_ = p.DefaultTenant
	}
	t.Cleanup(func() { policyEvalTestHook = origHook })

	req := &pb.PolicyCheckRequest{
		JobId:  "job-nilmap",
		Topic:  "job.test",
		Tenant: "default",
	}

	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("evaluate should not return error on panic recovery: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("FAIL-OPEN BUG: nil map panic should deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "policy evaluation panic") {
		t.Fatalf("expected panic reason, got %q", resp.GetReason())
	}
}

func TestEvaluateDefaultDecisionIsDeny(t *testing.T) {
	// Verify the default decision variable is DENY, not ALLOW.
	// An unrecognized policy decision string should result in DENY.
	srv := &server{}
	policy := &config.SafetyPolicy{
		DefaultDecision: "unknown_decision_value",
		DefaultTenant:   "default",
	}
	srv.setPolicy(policy, "snap-default")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-default",
		Topic:  "job.test",
		Tenant: "default",
	}

	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	// The policy returns an unrecognized decision string ("unknown_decision_value"),
	// which doesn't match any case in the switch. Default decision must be DENY.
	if resp.GetDecision() == pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("FAIL-OPEN BUG: unrecognized policy decision should not result in ALLOW")
	}
}

func TestWatchPolicyReloadFailureKeepsOldPolicy(t *testing.T) {
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "50ms")

	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"
	if err := os.WriteFile(policyPath, []byte("default_tenant: default\ntenants:\n  default:\n    allow_topics:\n      - job.*\n"), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	srv := &server{}
	initialPolicy := &config.SafetyPolicy{
		DefaultTenant: "prod",
		Tenants: map[string]config.TenantPolicy{
			"prod": {AllowTopics: []string{"job.prod.*"}},
		},
	}
	srv.setPolicy(initialPolicy, "initial-snap")

	// Corrupt the policy file so reload fails.
	if err := os.WriteFile(policyPath, []byte("invalid:\n  - [broken"), 0o600); err != nil {
		t.Fatalf("write corrupt policy: %v", err)
	}

	loader := &policyLoader{source: policyPath}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.watchPolicy(ctx, loader, nil)
		close(done)
	}()

	// Wait for at least one reload attempt.
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Old policy should still be active (fail-closed on reload error).
	srv.mu.RLock()
	currentPolicy := srv.policy
	currentSnap := srv.snapshot
	srv.mu.RUnlock()

	if currentPolicy == nil {
		t.Fatalf("expected old policy to be preserved after failed reload")
	}
	if currentSnap != "initial-snap" {
		t.Fatalf("expected snapshot to remain 'initial-snap', got %q", currentSnap)
	}
	if currentPolicy.DefaultTenant != "prod" {
		t.Fatalf("expected DefaultTenant to remain 'prod', got %q", currentPolicy.DefaultTenant)
	}
}

func TestMergePolicies_DuplicateRuleID(t *testing.T) {
	base := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "allow", Reason: "base"},
			{ID: "r2", Decision: "deny", Reason: "base"},
		},
	}
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "deny", Reason: "extra"},
			{ID: "r3", Decision: "allow", Reason: "extra"},
		},
	}
	merged := mergePolicies(base, extra)

	// Should have 3 rules: r1 (replaced), r2, r3
	if len(merged.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(merged.Rules))
	}
	// r1 should have been replaced with extra's version
	for _, r := range merged.Rules {
		if r.ID == "r1" {
			if r.Decision != "deny" || r.Reason != "extra" {
				t.Errorf("expected r1 to be replaced by extra (deny/extra), got %s/%s", r.Decision, r.Reason)
			}
		}
	}
}

func TestMergePolicies_NoDuplicates(t *testing.T) {
	base := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "allow"},
		},
	}
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r2", Decision: "deny"},
		},
	}
	merged := mergePolicies(base, extra)
	if len(merged.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(merged.Rules))
	}
}

func TestMergePolicies_DuplicateOutputRule(t *testing.T) {
	base := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{ID: "o1", Severity: "low"},
		},
	}
	extra := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{ID: "o1", Severity: "critical"},
			{ID: "o2", Severity: "medium"},
		},
	}
	merged := mergePolicies(base, extra)
	if len(merged.OutputRules) != 2 {
		t.Fatalf("expected 2 output rules, got %d", len(merged.OutputRules))
	}
	for _, r := range merged.OutputRules {
		if r.ID == "o1" && r.Severity != "critical" {
			t.Errorf("expected o1 severity=critical (replaced), got %s", r.Severity)
		}
	}
}

func TestClonePolicy_PreservesInputRules(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "allow"},
		},
		InputRules: []config.InputPolicyRule{
			{ID: "ir1", Severity: "high", Decision: "deny", Reason: "pii detected"},
			{ID: "ir2", Severity: "medium", Decision: "require_approval", Reason: "keyword match"},
		},
		OutputRules: []config.OutputPolicyRule{
			{ID: "o1", Severity: "low"},
		},
	}
	cloned := clonePolicy(policy)
	if len(cloned.InputRules) != 2 {
		t.Fatalf("expected 2 input rules after clone, got %d", len(cloned.InputRules))
	}
	if cloned.InputRules[0].ID != "ir1" || cloned.InputRules[1].ID != "ir2" {
		t.Errorf("input rule IDs mismatch: got %s, %s", cloned.InputRules[0].ID, cloned.InputRules[1].ID)
	}
	// Verify it's a copy, not a shared slice
	cloned.InputRules[0].Severity = "changed"
	if policy.InputRules[0].Severity == "changed" {
		t.Error("clonePolicy shares InputRules slice with original")
	}
}

func TestMergePolicies_InputRules(t *testing.T) {
	base := &config.SafetyPolicy{
		InputRules: []config.InputPolicyRule{
			{ID: "ir1", Severity: "high", Decision: "deny"},
		},
	}
	extra := &config.SafetyPolicy{
		InputRules: []config.InputPolicyRule{
			{ID: "ir2", Severity: "medium", Decision: "require_approval"},
		},
	}
	merged := mergePolicies(base, extra)
	if len(merged.InputRules) != 2 {
		t.Fatalf("expected 2 input rules, got %d", len(merged.InputRules))
	}
}

func TestMergePolicies_DuplicateInputRuleID(t *testing.T) {
	base := &config.SafetyPolicy{
		InputRules: []config.InputPolicyRule{
			{ID: "ir1", Severity: "low", Decision: "deny", Reason: "base"},
		},
	}
	extra := &config.SafetyPolicy{
		InputRules: []config.InputPolicyRule{
			{ID: "ir1", Severity: "critical", Decision: "deny", Reason: "extra"},
			{ID: "ir2", Severity: "medium", Decision: "require_approval"},
		},
	}
	merged := mergePolicies(base, extra)
	if len(merged.InputRules) != 2 {
		t.Fatalf("expected 2 input rules (ir1 replaced, ir2 added), got %d", len(merged.InputRules))
	}
	for _, r := range merged.InputRules {
		if r.ID == "ir1" {
			if r.Severity != "critical" || r.Reason != "extra" {
				t.Errorf("expected ir1 replaced by extra (critical/extra), got %s/%s", r.Severity, r.Reason)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Velocity — bundle-loaded rules regression tests
// ---------------------------------------------------------------------------

// newTestServerWithVelocity creates a server with a Redis-backed velocity checker
// and optionally sets a policy. The returned server can evaluate velocity rules.
func newTestServerWithVelocity(t *testing.T, policy *config.SafetyPolicy, snapshot string) (*server, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	srv := &server{
		resultClient:    client,
		velocityChecker: newVelocityChecker(client),
		cache:           map[string]cacheEntry{},
		cacheMaxSize:    100,
	}
	if policy != nil {
		srv.setPolicy(policy, snapshot)
	}
	return srv, mr
}

// bundleVelocityPolicy returns a policy that mimics the Visa demo bundle:
// a velocity deny rule (max_requests=3, window=60s, key=labels.session_id)
// followed by a fallthrough allow rule on the same topic.
func bundleVelocityPolicy() *config.SafetyPolicy {
	return &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID: "visa-velocity-control",
				Match: config.PolicyMatch{
					Topics: []string{"job.visa-governance.velocity-check"},
				},
				Velocity: &config.VelocityConfig{
					MaxRequests:   3,
					WindowSeconds: 60,
					Key:           "labels.session_id",
				},
				Decision: "deny",
				Reason:   "Velocity limit exceeded",
			},
			{
				ID: "visa-allow-fallback",
				Match: config.PolicyMatch{
					Topics: []string{"job.visa-governance.*"},
				},
				Decision: "allow",
				Reason:   "Allowed by fallback rule",
			},
		},
	}
}

func TestVelocityBundleRule_First3AllowThen4thDenies(t *testing.T) {
	policy := bundleVelocityPolicy()
	srv, _ := newTestServerWithVelocity(t, policy, "snap-velocity-1")

	sessionID := "sess-visa-demo-001"
	for i := 1; i <= 3; i++ {
		req := &pb.PolicyCheckRequest{
			JobId:  fmt.Sprintf("job-%d", i),
			Topic:  "job.visa-governance.velocity-check",
			Tenant: "default",
			Labels: map[string]string{"session_id": sessionID},
		}
		resp, err := srv.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("request %d: expected ALLOW (within velocity limit), got %v reason=%q ruleId=%q",
				i, resp.GetDecision(), resp.GetReason(), resp.GetRuleId())
		}
		if resp.GetRuleId() != "visa-allow-fallback" {
			t.Fatalf("request %d: expected fallthrough to visa-allow-fallback, got ruleId=%q", i, resp.GetRuleId())
		}
	}

	// 4th request — should exceed velocity limit and be denied.
	req := &pb.PolicyCheckRequest{
		JobId:  "job-4",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": sessionID},
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("request 4: unexpected error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("request 4: expected DENY (velocity exceeded), got %v reason=%q", resp.GetDecision(), resp.GetReason())
	}
	if resp.GetRuleId() != "visa-velocity-control" {
		t.Fatalf("request 4: expected ruleId=visa-velocity-control, got %q", resp.GetRuleId())
	}
}

func TestVelocityBundleRule_RedisKeysCreated(t *testing.T) {
	policy := bundleVelocityPolicy()
	srv, mr := newTestServerWithVelocity(t, policy, "snap-velocity-2")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-redis-key-check",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": "sess-key-check"},
	}
	_, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check error: %v", err)
	}

	// Verify that a cordum:velocity:* Redis key was created.
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if strings.HasPrefix(k, "cordum:velocity:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cordum:velocity:* Redis key, got keys: %v", keys)
	}
}

func TestVelocityBundleRule_NoJobID_StillEnforces(t *testing.T) {
	policy := bundleVelocityPolicy()
	srv, _ := newTestServerWithVelocity(t, policy, "snap-velocity-3")

	sessionID := "sess-no-jobid"
	for i := 1; i <= 3; i++ {
		req := &pb.PolicyCheckRequest{
			// No JobId — direct gRPC callers may omit this.
			Topic:  "job.visa-governance.velocity-check",
			Tenant: "default",
			Labels: map[string]string{"session_id": sessionID},
		}
		resp, err := srv.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("request %d (no job_id): expected ALLOW within limit, got %v", i, resp.GetDecision())
		}
	}

	// 4th call without job_id should still exceed velocity.
	req := &pb.PolicyCheckRequest{
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": sessionID},
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("request 4 (no job_id): unexpected error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("request 4 (no job_id): expected DENY, got %v — empty job_id must not collapse sliding window members",
			resp.GetDecision())
	}
}

func TestVelocityBundleRule_RedisUnavailable_FailOpen(t *testing.T) {
	policy := bundleVelocityPolicy()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	srv := &server{
		resultClient:    client,
		velocityChecker: newVelocityChecker(client),
		cache:           map[string]cacheEntry{},
		cacheMaxSize:    100,
	}
	srv.setPolicy(policy, "snap-velocity-failopen")

	// Close Redis to simulate unavailability.
	mr.Close()

	req := &pb.PolicyCheckRequest{
		JobId:  "job-failopen",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": "sess-failopen"},
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check error: %v", err)
	}
	// Fail-open: velocity rule skipped, fallthrough allow should fire.
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW (fail-open when Redis unavailable), got %v reason=%q", resp.GetDecision(), resp.GetReason())
	}
}

func TestVelocityBundleRule_NilChecker_FailOpen(t *testing.T) {
	policy := bundleVelocityPolicy()
	// Server without a velocity checker (no Redis at all).
	srv := &server{
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(policy, "snap-velocity-nilchecker")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-nilchecker",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": "sess-nilchecker"},
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check error: %v", err)
	}
	// Fail-open: nil velocity checker should skip velocity rule, fallthrough to allow.
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW (fail-open with nil checker), got %v reason=%q ruleId=%q",
			resp.GetDecision(), resp.GetReason(), resp.GetRuleId())
	}
	if resp.GetRuleId() != "visa-allow-fallback" {
		t.Fatalf("expected fallthrough to visa-allow-fallback with nil checker, got ruleId=%q", resp.GetRuleId())
	}
}

func TestVelocityBundleRule_CacheDoesNotBypassVelocity(t *testing.T) {
	policy := bundleVelocityPolicy()
	srv, _ := newTestServerWithVelocity(t, policy, "snap-velocity-cache")
	srv.cacheTTL = 5 * time.Minute // Enable caching.

	sessionID := "sess-cache-test"
	for i := 1; i <= 4; i++ {
		req := &pb.PolicyCheckRequest{
			JobId:  fmt.Sprintf("job-cache-%d", i),
			Topic:  "job.visa-governance.velocity-check",
			Tenant: "default",
			Labels: map[string]string{"session_id": sessionID},
		}
		resp, err := srv.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if i <= 3 {
			if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
				t.Fatalf("request %d (cache enabled): expected ALLOW, got %v", i, resp.GetDecision())
			}
		} else {
			if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
				t.Fatalf("request %d (cache enabled): expected DENY (velocity exceeded despite cache), got %v",
					i, resp.GetDecision())
			}
		}
	}
}

func TestVelocityBundleRule_PolicyReloadActivatesVelocity(t *testing.T) {
	// Start with a policy that has NO velocity rules.
	noVelocityPolicy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
	}
	srv, _ := newTestServerWithVelocity(t, noVelocityPolicy, "snap-no-velocity")

	// All requests should be allowed.
	req := &pb.PolicyCheckRequest{
		JobId:  "job-pre-reload",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": "sess-reload"},
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("pre-reload check error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("pre-reload: expected ALLOW, got %v", resp.GetDecision())
	}

	// Simulate bundle reload that introduces velocity rules.
	srv.setPolicy(bundleVelocityPolicy(), "snap-with-velocity")

	// Now velocity should be active: 3 allows, 4th denied.
	sessionID := "sess-post-reload"
	for i := 1; i <= 3; i++ {
		req := &pb.PolicyCheckRequest{
			JobId:  fmt.Sprintf("job-post-reload-%d", i),
			Topic:  "job.visa-governance.velocity-check",
			Tenant: "default",
			Labels: map[string]string{"session_id": sessionID},
		}
		resp, err := srv.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("post-reload request %d: error: %v", i, err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("post-reload request %d: expected ALLOW, got %v", i, resp.GetDecision())
		}
	}
	req = &pb.PolicyCheckRequest{
		JobId:  "job-post-reload-4",
		Topic:  "job.visa-governance.velocity-check",
		Tenant: "default",
		Labels: map[string]string{"session_id": sessionID},
	}
	resp, err = srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("post-reload request 4: error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("post-reload request 4: expected DENY after velocity reload, got %v", resp.GetDecision())
	}
}

func TestMergePolicies_NilBaseInputRules(t *testing.T) {
	extra := &config.SafetyPolicy{
		InputRules: []config.InputPolicyRule{
			{ID: "ir1", Severity: "high", Decision: "deny"},
			{ID: "ir2", Severity: "medium", Decision: "require_approval"},
		},
	}
	merged := mergePolicies(nil, extra)
	if len(merged.InputRules) != 2 {
		t.Fatalf("expected 2 input rules from extra when base is nil, got %d", len(merged.InputRules))
	}
}

// TestConcurrentEvaluateAndSetPolicy verifies that concurrent policy reloads
// during evaluate() don't cause panics, nil dereferences, or stale decisions.
// This reproduces the TOCTOU race fixed by capturing all policy-related state
// under a single RLock in evaluate().
func TestConcurrentEvaluateAndSetPolicy(t *testing.T) {
	policyA := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
		Rules: []config.PolicyRule{
			{
				ID:       "allow-all",
				Match:    config.PolicyMatch{Topics: []string{"job.*"}},
				Decision: "allow",
			},
		},
	}
	policyB := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
		Rules: []config.PolicyRule{
			{
				ID:       "deny-all",
				Match:    config.PolicyMatch{Topics: []string{"job.*"}},
				Decision: "deny",
				Reason:   "policy B denies all",
			},
		},
	}

	srv := &server{}
	srv.setPolicy(policyA, "snap-a")

	const (
		numEvaluators = 50
		numReloads    = 200
	)
	ctx := context.Background()

	// Channel to collect panics — any panic is a test failure.
	panics := make(chan any, numEvaluators)
	done := make(chan struct{})

	// Goroutine that swaps policy rapidly.
	go func() {
		for i := 0; i < numReloads; i++ {
			if i%2 == 0 {
				srv.setPolicy(policyB, "snap-b")
			} else {
				srv.setPolicy(policyA, "snap-a")
			}
		}
		close(done)
	}()

	// Concurrent evaluators.
	for i := 0; i < numEvaluators; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()
			for {
				select {
				case <-done:
					return
				default:
				}
				req := &pb.PolicyCheckRequest{
					Topic: "job.test",
					JobId: fmt.Sprintf("job-%d", time.Now().UnixNano()),
				}
				resp, err := srv.evaluate(ctx, req, "check")
				if err != nil {
					t.Errorf("evaluate returned error: %v", err)
					return
				}
				// Decision must be valid — either allow or deny, never zero-value.
				if resp.Decision == pb.DecisionType_DECISION_TYPE_UNSPECIFIED {
					t.Errorf("evaluate returned unspecified decision")
					return
				}
			}
		}()
	}

	<-done

	// Drain any panics.
	close(panics)
	for p := range panics {
		t.Fatalf("concurrent evaluate panicked: %v", p)
	}
}
