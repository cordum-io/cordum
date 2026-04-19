package safetykernel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policyshadow"
)

// capturingSender is an audit.AuditSender that retains every event it
// sees so tests can assert emission shape.
type capturingSender struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (c *capturingSender) Send(ev audit.SIEMEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}
func (c *capturingSender) Close() error { return nil }
func (c *capturingSender) snapshot() []audit.SIEMEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.SIEMEvent, len(c.events))
	copy(out, c.events)
	return out
}

// seedLoader returns a ShadowLoader pre-populated with one shadow per
// tenant from the supplied map. Useful for tests that want full
// control over the compiled snapshot without the configsvc round-trip.
func seedLoader(t *testing.T, shadows map[string]map[string]string) *ShadowLoader {
	t.Helper()
	store, cleanup := newShadowTestStore(t)
	t.Cleanup(cleanup)

	tenants := make([]string, 0, len(shadows))
	ctx := context.Background()
	for tenant, bundles := range shadows {
		tenants = append(tenants, tenant)
		for bundleID, content := range bundles {
			_, err := store.Put(ctx, policyshadow.ShadowPolicy{
				TenantID: tenant,
				BundleID: bundleID,
				Content:  content,
			})
			if err != nil {
				t.Fatalf("seed put: %v", err)
			}
		}
	}
	loader := NewShadowLoader(store, time.Hour, func() []string { return tenants })
	t.Cleanup(loader.Close)
	if err := loader.refreshOnce(ctx); err != nil {
		t.Fatalf("refreshOnce: %v", err)
	}
	return loader
}

func allowInput(tenant string) config.PolicyInput {
	return config.PolicyInput{
		Tenant: tenant,
		Topic:  "job.foo",
		Meta:   config.PolicyMeta{AgentID: "agent-1"},
	}
}

// drainUntil waits up to `budget` for sender.events to reach `n`.
// Non-polling time.Sleep is acceptable here because shadow eval runs
// on a worker pool and we need to give the goroutine time to execute.
func drainUntil(t *testing.T, sender *capturingSender, n int, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if len(sender.snapshot()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d events within %s, got %d", n, budget, len(sender.snapshot()))
}

// TestShadowEvaluator_EmitsShadowEvalEventWithExtraFields confirms the
// full happy path: submit → worker evaluates shadow → audit event
// fires with every required Extra field populated.
func TestShadowEvaluator_EmitsShadowEvalEventWithExtraFields(t *testing.T) {
	t.Parallel()
	shadowYAML := `version: "1"
rules:
  - id: shadow-deny-foo
    match:
      topics: ["job.foo"]
    decision: deny
    reason: shadow says no
`
	loader := seedLoader(t, map[string]map[string]string{
		"tenant-a": {"b-1": shadowYAML},
	})
	sender := &capturingSender{}
	eval := NewShadowEvaluator(loader, sender, ShadowEvaluatorOptions{Workers: 2, QueueSize: 8})
	defer eval.Close()

	activeDecision := config.PolicyDecision{Decision: "allow", RuleID: "active-allow", Reason: "active-ok"}
	eval.Submit(activeDecision, allowInput("tenant-a"), "tenant-a", "job-42")

	drainUntil(t, sender, 1, 500*time.Millisecond)
	events := sender.snapshot()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != audit.EventShadowEval {
		t.Errorf("EventType = %q, want shadow_eval", ev.EventType)
	}
	if ev.TenantID != "tenant-a" || ev.JobID != "job-42" || ev.AgentID != "agent-1" {
		t.Errorf("top-level identity mismatch: %+v", ev)
	}
	required := []string{"shadow_bundle_id", "bundle_id", "active_verdict", "shadow_verdict", "diff", "active_rule_id", "shadow_rule_id", "latency_ms"}
	for _, key := range required {
		if _, ok := ev.Extra[key]; !ok {
			t.Errorf("Extra missing %q: %v", key, ev.Extra)
		}
	}
	if ev.Extra["active_verdict"] != "allow" || ev.Extra["shadow_verdict"] != "deny" {
		t.Errorf("verdict extras = %+v", ev.Extra)
	}
	if ev.Extra["diff"] != "escalated" {
		t.Errorf("diff = %q, want escalated", ev.Extra["diff"])
	}
	if ev.Extra["bundle_id"] != "b-1" {
		t.Errorf("bundle_id = %q", ev.Extra["bundle_id"])
	}
}

// TestShadowEvaluator_DiffClassification is a table test for all four
// diff labels — missing a case here lets a regression silently change
// the results-API bucketing downstream.
func TestShadowEvaluator_DiffClassification(t *testing.T) {
	t.Parallel()
	d := func(decision, rule string, approval bool) config.PolicyDecision {
		return config.PolicyDecision{Decision: decision, RuleID: rule, ApprovalRequired: approval}
	}
	cases := []struct {
		name   string
		active config.PolicyDecision
		shadow config.PolicyDecision
		want   ShadowDiff
	}{
		{"both allow", d("allow", "a", false), d("allow", "s", false), ShadowDiffUnchanged},
		{"both deny", d("deny", "a", false), d("deny", "s", false), ShadowDiffUnchanged},
		{"active allow, shadow deny", d("allow", "a", false), d("deny", "s", false), ShadowDiffEscalated},
		{"active deny, shadow allow", d("deny", "a", false), d("allow", "s", false), ShadowDiffRelaxed},
		{"active allow, shadow approval", d("allow", "a", false), d("require_approval", "s", true), ShadowDiffApprovalDiffer},
		{"active approval, shadow allow", d("require_approval", "a", true), d("allow", "s", false), ShadowDiffApprovalDiffer},
		{"throttle vs allow", d("throttle", "a", false), d("allow", "s", false), ShadowDiffApprovalDiffer},
		{"active throttle, shadow deny", d("throttle", "a", false), d("deny", "s", false), ShadowDiffEscalated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyShadowDiff(tc.active, tc.shadow)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestShadowEvaluator_ActiveDecisionUnchanged ensures nothing about
// the evaluator path mutates the caller's inputs. This pins the core
// epic rail: "Shadow evaluation must NEVER affect actual job decisions".
func TestShadowEvaluator_ActiveDecisionUnchanged(t *testing.T) {
	t.Parallel()
	loader := seedLoader(t, map[string]map[string]string{
		"tenant-a": {"b-1": "version: \"1\"\nrules:\n  - id: x\n    match: {topics: [\"job.foo\"]}\n    decision: deny\n    reason: r\n"},
	})
	eval := NewShadowEvaluator(loader, &capturingSender{}, ShadowEvaluatorOptions{Workers: 1, QueueSize: 8})
	defer eval.Close()

	origDecision := config.PolicyDecision{Decision: "allow", Reason: "active allowed", RuleID: "active-r"}
	origInput := allowInput("tenant-a")
	origInput.Meta.RiskTags = []string{"pii"}

	// Save a deep copy for comparison after the worker runs.
	activeSnapshot := origDecision
	inputSnapshot := origInput
	tagsSnapshot := append([]string{}, origInput.Meta.RiskTags...)

	eval.Submit(origDecision, origInput, "tenant-a", "job-1")
	eval.Close()
	if origDecision.Decision != activeSnapshot.Decision || origDecision.RuleID != activeSnapshot.RuleID || origDecision.Reason != activeSnapshot.Reason {
		t.Errorf("active decision mutated: before=%+v after=%+v", activeSnapshot, origDecision)
	}
	if origInput.Tenant != inputSnapshot.Tenant || origInput.Topic != inputSnapshot.Topic {
		t.Error("policy input header mutated")
	}
	if len(origInput.Meta.RiskTags) != len(tagsSnapshot) {
		t.Error("RiskTags mutated")
	}
}

// TestShadowEvaluator_QueueOverflowDropsWithCounter covers the
// bounded-queue behaviour: once the queue is full, Submit drops and
// bumps the counter rather than blocking the caller (the active path).
func TestShadowEvaluator_QueueOverflowDropsWithCounter(t *testing.T) {
	t.Parallel()
	// Shadow with a deliberately heavy evaluator so the queue backs up.
	// In practice the rule set would be light, but a tiny workers /
	// queue combo plus a sleeping evaluation simulates overload.
	loader := seedLoader(t, map[string]map[string]string{
		"tenant-a": {"b-1": "version: \"1\"\nrules:\n  - id: x\n    match: {topics: [\"job.foo\"]}\n    decision: deny\n    reason: r\n"},
	})
	var drops atomic.Int64
	opts := ShadowEvaluatorOptions{
		Workers:   1,
		QueueSize: 2,
		DropCallback: func(r ShadowDropReason) {
			if r == ShadowDropQueueFull {
				drops.Add(1)
			}
		},
	}
	// Block the single worker so the queue saturates.
	block := make(chan struct{})
	sender := &blockingSender{gate: block}
	eval := NewShadowEvaluator(loader, sender, opts)
	// Fire many submissions: 1 goes to the worker, 2 sit in the queue,
	// the rest MUST drop with queue_full.
	const N = 64
	for range N {
		eval.Submit(config.PolicyDecision{Decision: "allow"}, allowInput("tenant-a"), "tenant-a", "job-x")
	}
	if got := drops.Load(); got == 0 {
		t.Errorf("expected queue_full drops, got 0 (N=%d, queue=2, workers=1)", N)
	}
	// Let the blocked worker finish and close cleanly.
	close(block)
	eval.Close()
}

// blockingSender gates Send() on a channel so tests can deterministically
// stall the worker pool and observe queue pressure.
type blockingSender struct{ gate chan struct{} }

func (b *blockingSender) Send(ev audit.SIEMEvent) { <-b.gate }
func (b *blockingSender) Close() error            { return nil }

// TestShadowEvaluator_ClosedEvaluatorDropsSubmits asserts Submit after
// Close turns into a drop — the kernel's shutdown path calls Close
// before returning and any straggling eval calls must not leak events
// after the sender has been torn down.
func TestShadowEvaluator_ClosedEvaluatorDropsSubmits(t *testing.T) {
	t.Parallel()
	loader := seedLoader(t, nil)
	sender := &capturingSender{}
	var drops atomic.Int64
	eval := NewShadowEvaluator(loader, sender, ShadowEvaluatorOptions{
		Workers:   1,
		QueueSize: 4,
		DropCallback: func(r ShadowDropReason) {
			if r == ShadowDropClosed {
				drops.Add(1)
			}
		},
	})
	eval.Close()
	eval.Submit(config.PolicyDecision{Decision: "allow"}, allowInput("tenant-a"), "tenant-a", "job-x")
	if drops.Load() != 1 {
		t.Errorf("expected 1 closed-drop, got %d", drops.Load())
	}
	if len(sender.snapshot()) != 0 {
		t.Errorf("unexpected event emission on closed evaluator: %+v", sender.snapshot())
	}
}

// TestShadowEvaluator_PanicInEvaluateDoesNotKillWorker confirms that a
// pathological shadow policy does not bring down the worker pool.
// The underlying Evaluate isn't panic-prone today, but classifyShadowDiff
// and severityForDiff both handle arbitrary decision strings without
// panicking — this test exercises the recover() seam in evalShadowSafely
// by invoking it directly.
func TestShadowEvaluator_PanicInEvaluateDoesNotKillWorker(t *testing.T) {
	t.Parallel()
	// Craft a policy that will panic during Evaluate by passing a nil
	// pointer as the receiver. evalShadowSafely must recover and return
	// an error; it must never panic out.
	var nilPolicy *config.SafetyPolicy
	_, err := evalShadowSafely(nilPolicy, config.PolicyInput{Tenant: "t", Topic: "x"})
	if err == nil {
		t.Fatal("expected recovered panic error from nil-receiver Evaluate")
	}
}

// TestShadowEvaluator_CallbackWiring pins the three lifecycle
// callbacks step-6 will wire to Prometheus counters/gauges — a
// regression that silently drops one of them would still pass the
// emission test above.
func TestShadowEvaluator_CallbackWiring(t *testing.T) {
	t.Parallel()
	shadowYAML := `version: "1"
rules:
  - id: x
    match: {topics: ["job.foo"]}
    decision: deny
    reason: r
`
	loader := seedLoader(t, map[string]map[string]string{"tenant-a": {"b-1": shadowYAML}})
	var emitCalls atomic.Int64
	var depthChanges atomic.Int64
	opts := ShadowEvaluatorOptions{
		Workers:   1,
		QueueSize: 4,
		EmitCallback: func(decision string, d ShadowDiff, latency time.Duration) {
			if d == "" {
				t.Error("emit diff empty")
			}
			if decision == "" {
				t.Error("emit decision empty")
			}
			emitCalls.Add(1)
		},
		QueueDepthCallback: func(delta int64) {
			depthChanges.Add(delta)
		},
	}
	sender := &capturingSender{}
	eval := NewShadowEvaluator(loader, sender, opts)
	eval.Submit(config.PolicyDecision{Decision: "allow"}, allowInput("tenant-a"), "tenant-a", "job-1")
	drainUntil(t, sender, 1, 500*time.Millisecond)
	eval.Close()

	if emitCalls.Load() != 1 {
		t.Errorf("EmitCallback invocations = %d, want 1", emitCalls.Load())
	}
	// depthChanges sums +1 (enqueue) and -1 (dequeue) → 0 once drained.
	if depthChanges.Load() != 0 {
		t.Errorf("queue-depth balance = %d, want 0", depthChanges.Load())
	}
}
