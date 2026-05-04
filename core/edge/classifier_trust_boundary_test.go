package edge

import (
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	dto "github.com/prometheus/client_model/go"
)

// EDGE-069 trust-boundary tests. These pin the contract that the
// classifier OWNS the reserved label namespaces (edge.*, hook.*, mcp.*,
// llm.*, runtime.*, agent.*, path.*, command.*, unknown.*, action.*) and
// that user-controlled request bodies CANNOT downgrade their own
// classification by setting one of these labels in event.Labels.
//
// Each test below corresponds to one of the 7 architect-spec'd
// invariants from the EDGE-069 task DoD. The tests are RED on
// pre-EDGE-069 code where path./command./unknown. namespaces are NOT
// in `isReservedPolicyLabel` — a malicious client can leak labels
// into the policy input under those prefixes.

func newBaseHookEvent(toolName string, input map[string]any) AgentActionEvent {
	return AgentActionEvent{
		EventID:       "evt-trust-boundary",
		SessionID:     "sess-trust-boundary",
		ExecutionID:   "exec-trust-boundary",
		TenantID:      "default",
		PrincipalID:   "principal-test",
		Seq:           1,
		Timestamp:     time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		Layer:         LayerHook,
		Kind:          EventKindHookPreToolUse,
		AgentProduct:  "claude-code",
		ToolName:      toolName,
		InputRedacted: input,
	}
}

// Invariant (a): Unknown classifier output → never silent ALLOW.
// A hook tool_name not recognised by the classifier defaults to
// capabilityUnknown + risk_tags including review_required. Policy
// authors enforce the deny in `claude-code.deny-unknown-high-risk`
// for the high-impact case; this test pins the capability + risk_tags
// the policy depends on.
func TestClassifier_UnknownToolFailsToHigherTier(t *testing.T) {
	event := newBaseHookEvent("UnknownFutureTool", map[string]any{"file_path": "/etc/passwd"})
	got, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	if got.Capability != capabilityUnknown {
		t.Fatalf("expected capability=%q for unknown tool, got %q", capabilityUnknown, got.Capability)
	}
	if !trustBoundaryContains(got.RiskTags, "review_required") {
		t.Fatalf("expected risk_tags to include review_required for unknown tool; got %v", got.RiskTags)
	}
	if !trustBoundaryContains(got.RiskTags, "unknown") {
		t.Fatalf("expected risk_tags to include unknown for unknown tool; got %v", got.RiskTags)
	}
}

// Invariant (d): User-controlled risk_tags CANNOT remove classifier-
// emitted risk tags. The classifier OWNS RiskTags via the return
// value of ClassifyEvent — `event.RiskTags` is not even consulted by
// the classifier (verified by reading classifyHookEvent + classifyBashCommand).
func TestClassifier_RiskTagsAreOwnedByClassifierNotRequest(t *testing.T) {
	event := newBaseHookEvent("Bash", map[string]any{"command": "rm -rf /"})
	// Attacker tries to claim "harmless" risk_tags on the request body.
	event.RiskTags = []string{}

	got, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	// Classifier-emitted tags MUST include destructive + filesystem regardless
	// of what the user said.
	for _, want := range []string{"destructive", "filesystem"} {
		if !trustBoundaryContains(got.RiskTags, want) {
			t.Fatalf("expected classifier-emitted risk tag %q for `rm -rf /`; got %v", want, got.RiskTags)
		}
	}
}

// Invariant (e): User-controlled labels in the reserved namespace
// CANNOT poison the classifier's emitted labels. Specifically: a
// request body labels.edge.kind="hook.user_prompt_submit" must NOT
// cause a Bash tool action to be classified as a UserPromptSubmit.
//
// This is the integration-level invariant — at the classifier level,
// `event.Labels` is not consulted for kind/layer (verified by reading
// classifier.go); the strip happens at the gateway boundary in
// mapLabelsForPolicy via isReservedPolicyLabel. This test asserts
// the policy-mapper output excludes the user-poisoned label.
func TestClassifier_ReservedLabelsCannotBePoisonedByRequest(t *testing.T) {
	event := newBaseHookEvent("Bash", map[string]any{"command": "rm -rf /"})
	// Attacker tries to flip the kind label.
	event.Labels = Labels{
		"edge.kind":       "hook.user_prompt_submit",
		"hook.tool_name":  "innocuous_tool",
		"command.class":   "safe",
		"command.family":  "test",
		"path.class":      "file",
		"unknown.impact":  "low",
		"action.name":     "fake.bash.test",
		"action.hash":     "deadbeef",
		"benign_label":    "kept",
	}

	classification, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	// Classifier-emitted labels for this Bash rm -rf /:
	//  - edge.kind / edge.layer / hook.tool_name set by baseClassificationLabels
	//  - command.class=destructive / command.family=filesystem_delete by classifyBashCommand
	if classification.Labels["edge.kind"] != string(EventKindHookPreToolUse) {
		t.Fatalf("classifier edge.kind = %q, want hook.pre_tool_use (user request poisoning leaked into classification)",
			classification.Labels["edge.kind"])
	}
	if classification.Labels["command.class"] != "destructive" {
		t.Fatalf("classifier command.class = %q, want destructive (attacker tried to set safe)",
			classification.Labels["command.class"])
	}

	// Now pass through the policy mapper — the boundary that strips user-set
	// reserved-namespace labels.
	policyLabels := mapLabelsForPolicy(event, classification)

	// The classifier's authoritative values must win.
	if policyLabels["edge.kind"] != string(EventKindHookPreToolUse) {
		t.Fatalf("policy labels edge.kind = %q, want hook.pre_tool_use; user poisoning leaked through",
			policyLabels["edge.kind"])
	}
	if policyLabels["command.class"] != "destructive" {
		t.Fatalf("policy labels command.class = %q, want destructive; attacker bypass succeeded",
			policyLabels["command.class"])
	}
	if policyLabels["hook.tool_name"] != "Bash" {
		t.Fatalf("policy labels hook.tool_name = %q, want Bash; user-set hook.tool_name leaked through",
			policyLabels["hook.tool_name"])
	}
	// Reserved-namespace user-set labels MUST be stripped — these are the
	// CORE EDGE-069 invariant (RED before fix, GREEN after).
	for _, leaked := range []string{"path.class", "unknown.impact", "action.name", "action.hash"} {
		// path.class/unknown.impact/action.name/action.hash were not
		// emitted by the classifier for this Bash event — if the policy
		// labels still contain them, the user-set value leaked through
		// via the missing-prefix gap in isReservedPolicyLabel.
		if value, present := policyLabels[leaked]; present {
			// Distinguish classifier-emitted (acceptable) from user-leaked
			// (unacceptable) by comparing against classifier output.
			if classification.Labels[leaked] == "" || classification.Labels[leaked] != value {
				t.Fatalf("user-set reserved-namespace label leaked through to policy: %s=%q (classifier did not emit it)",
					leaked, value)
			}
		}
	}
	// Non-reserved user labels SHOULD pass through.
	if policyLabels["benign_label"] != "kept" {
		t.Fatalf("benign user label dropped: got %q, want kept", policyLabels["benign_label"])
	}
}

// Invariant (e) deeper: even when classifier did not emit a value for
// a reserved-namespace label, a user-set value in that namespace must
// be stripped (the namespace ownership is enforced as a whole, not
// per-key).
func TestClassifier_PathLabelInjectionDoesNotDowngrade(t *testing.T) {
	// Read of /etc/passwd-like path; classifier emits path.class=file
	// for non-secret paths. Attacker tries to claim path.class=secret
	// to mismatch a deny rule's expectation, OR claim path.class=file
	// when reading .env to bypass the deny-secret-reads rule.
	event := newBaseHookEvent("Read", map[string]any{"file_path": "/home/user/.env"})
	event.Labels = Labels{"path.class": "file"} // attacker tries to downgrade

	classification, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	// Classifier should set path.class=secret for .env.
	if classification.Labels["path.class"] != "secret" {
		t.Fatalf("classifier path.class = %q for /home/user/.env, want secret",
			classification.Labels["path.class"])
	}

	policyLabels := mapLabelsForPolicy(event, classification)
	if policyLabels["path.class"] != "secret" {
		t.Fatalf("policy labels path.class = %q, want secret; user-set path.class=file leaked through",
			policyLabels["path.class"])
	}
}

// Invariant (e) deeper: command.class injection cannot downgrade a
// destructive shell command.
func TestClassifier_CommandLabelInjectionDoesNotDowngrade(t *testing.T) {
	event := newBaseHookEvent("Bash", map[string]any{"command": "rm -rf /"})
	event.Labels = Labels{
		"command.class":  "safe",
		"command.family": "test",
	}

	classification, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	if classification.Labels["command.class"] != "destructive" {
		t.Fatalf("classifier command.class = %q for `rm -rf /`, want destructive",
			classification.Labels["command.class"])
	}

	policyLabels := mapLabelsForPolicy(event, classification)
	if policyLabels["command.class"] != "destructive" {
		t.Fatalf("policy labels command.class = %q, want destructive; user injection leaked through",
			policyLabels["command.class"])
	}
}

// Invariant (e) deeper: unknown.impact injection cannot downgrade an
// unknown high-risk action.
func TestClassifier_UnknownImpactInjectionDoesNotDowngrade(t *testing.T) {
	// Unknown tool with high-impact-looking input — classifier sets
	// unknown.impact=high.
	event := newBaseHookEvent("UnknownFutureTool", map[string]any{
		"command_or_query": "delete production database",
	})
	event.Labels = Labels{"unknown.impact": "low"} // attacker tries to downgrade

	classification, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	if classification.Labels["unknown.impact"] != "high" {
		t.Fatalf("classifier unknown.impact = %q, want high (high-impact tokens in input)",
			classification.Labels["unknown.impact"])
	}

	policyLabels := mapLabelsForPolicy(event, classification)
	if policyLabels["unknown.impact"] != "high" {
		t.Fatalf("policy labels unknown.impact = %q, want high; user injection leaked through",
			policyLabels["unknown.impact"])
	}
}

// Invariant (b): Empty/sentinel classifier output (capability empty,
// risk_tags empty) MUST be flagged for fail-closed handling. EDGE-069
// step 4 adds Complete + MissingFields to the classification so
// downstream consumers see the partial state.
func TestClassifier_EmptyClassificationFailsClosed(t *testing.T) {
	// Construct a classification by calling ClassifyEvent with a normal
	// hook event; verify the happy-path Complete=true.
	happy, err := ClassifyEvent(newBaseHookEvent("Read", map[string]any{"file_path": "/tmp/x"}))
	if err != nil {
		t.Fatalf("happy ClassifyEvent: %v", err)
	}
	if !happy.Complete || len(happy.MissingFields) != 0 {
		t.Fatalf("happy classification expected Complete=true MissingFields=nil; got Complete=%v MissingFields=%v",
			happy.Complete, happy.MissingFields)
	}

	// Now exercise the helper directly with a partial classification —
	// proves the boundary detects each missing field.
	partial := ActionClassification{}
	complete, missing := computeClassificationCompleteness(partial)
	if complete {
		t.Fatal("zero-value classification reported Complete=true")
	}
	for _, want := range []string{"action_name", "capability", "risk_tags"} {
		if !trustBoundaryContains(missing, want) {
			t.Errorf("zero-value classification missing %q not flagged; got MissingFields=%v", want, missing)
		}
	}
}

// Invariant (g): Audit-evidence event MUST cite rule_id (or the
// synthetic "<default>" sentinel). This is enforced at the policy-
// evaluator boundary; the classifier itself doesn't emit rule_id.
// Skipping — the audit-emit invariant lives in core/controlplane/safetykernel
// + core/edge/recorder.go and is exercised by their package tests.
func TestClassifier_AuditEventCitesRuleID(t *testing.T) {
	t.Skip("invariant (g) tracked in safetykernel/recorder package tests, not classifier")
}

// Invariant (c): Tenant from auth, never from labels. Tested at the
// gateway-handler boundary (handlers_edge_evaluate_test.go) since the
// classifier doesn't touch tenant resolution. This stub exists so the
// 7-invariant audit table cross-references back to the test surface.
func TestClassifier_TenantNotUserSettable(t *testing.T) {
	t.Skip("invariant (c) tracked in handlers_edge_evaluate_test.go (EDGE-008.7 already covers this)")
}

// Invariant (f): Partial classification → flagged for fail-closed
// downstream handling. The classifier produces ActionClassification
// with Complete=false + MissingFields populated when any of
// {action_name, capability, risk_tags} is empty. The architect's
// "missing action_hash" wording maps to "missing one of the three
// required field categories" since action_hash is computed downstream
// in policy_mapper.go from the classifier's output.
func TestClassifier_PartialClassificationDenied(t *testing.T) {
	// Manufacture a classification with each of the 3 required fields
	// missing in turn; assert the helper flags it.
	cases := []struct {
		name        string
		c           ActionClassification
		wantMissing string
	}{
		{
			name:        "missing_action_name",
			c:           ActionClassification{Capability: "exec.shell", RiskTags: []string{"exec"}},
			wantMissing: "action_name",
		},
		{
			name:        "missing_capability",
			c:           ActionClassification{ActionName: "bash.exec", RiskTags: []string{"exec"}},
			wantMissing: "capability",
		},
		{
			name:        "missing_risk_tags",
			c:           ActionClassification{ActionName: "bash.exec", Capability: "exec.shell"},
			wantMissing: "risk_tags",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			complete, missing := computeClassificationCompleteness(tc.c)
			if complete {
				t.Fatalf("expected Complete=false for partial classification, got Complete=true")
			}
			if !trustBoundaryContains(missing, tc.wantMissing) {
				t.Fatalf("expected MissingFields to include %q; got %v", tc.wantMissing, missing)
			}
		})
	}
}

// TestPolicyMapper_ClassifierCompletenessSurfacedInLabels pins the
// EDGE-069 step-5 audit-evidence contract: every policy decision
// includes classifier.complete and (when partial) classifier.missing_fields
// so downstream consumers (governance timeline, SIEM, dashboard) can
// see when a classification was partial.
//
// Defense-in-depth: classifier.* is in reservedPolicyLabelPrefixes so
// a malicious request body labels.classifier.complete=true cannot
// short-circuit a downstream fail-closed evaluator.
func TestPolicyMapper_ClassifierCompletenessSurfacedInLabels(t *testing.T) {
	t.Run("complete_classification", func(t *testing.T) {
		event := newBaseHookEvent("Read", map[string]any{"file_path": "/tmp/x"})
		classification, err := ClassifyEvent(event)
		if err != nil {
			t.Fatalf("ClassifyEvent: %v", err)
		}
		labels := mapLabelsForPolicy(event, classification)
		if got := labels["classifier.complete"]; got != "true" {
			t.Fatalf("classifier.complete = %q, want true", got)
		}
		if got, ok := labels["classifier.missing_fields"]; ok {
			t.Fatalf("classifier.missing_fields should be absent on complete classifications; got %q", got)
		}
	})

	t.Run("partial_classification", func(t *testing.T) {
		// Hand-craft a partial classification (missing capability).
		event := newBaseHookEvent("Read", map[string]any{"file_path": "/tmp/x"})
		partial := ActionClassification{
			ActionName:    "file.read",
			Capability:    "", // intentionally missing
			RiskTags:      []string{"filesystem", "read"},
			Labels:        Labels{},
			Complete:      false,
			MissingFields: []string{"capability"},
		}
		labels := mapLabelsForPolicy(event, partial)
		if got := labels["classifier.complete"]; got != "false" {
			t.Fatalf("classifier.complete = %q, want false", got)
		}
		if got := labels["classifier.missing_fields"]; got != "capability" {
			t.Fatalf("classifier.missing_fields = %q, want capability", got)
		}
	})

	t.Run("user_cannot_set_classifier_complete", func(t *testing.T) {
		// Malicious request tries to claim Complete=true; classifier.*
		// is in reservedPolicyLabelPrefixes so the user value drops.
		event := newBaseHookEvent("Read", map[string]any{"file_path": "/tmp/x"})
		event.Labels = Labels{"classifier.complete": "true"}
		// Pass a partial classification — labels output must reflect
		// the classifier's "false", not the user's "true".
		partial := ActionClassification{
			ActionName:    "file.read",
			Capability:    "",
			RiskTags:      []string{"filesystem", "read"},
			Labels:        Labels{},
			Complete:      false,
			MissingFields: []string{"capability"},
		}
		labels := mapLabelsForPolicy(event, partial)
		if got := labels["classifier.complete"]; got != "false" {
			t.Fatalf("classifier.complete = %q, want false (user injection ignored)", got)
		}
	})
}

// TestPolicyMapper_StripMetricEmittedPerNamespace pins the EDGE-069
// observability contract: every label drop at the trust boundary
// increments cordum_edge_request_labels_stripped_total{namespace=...}
// so operators can alert on a sustained non-zero rate. Pre-fix this
// counter does not exist; post-fix the increment fires for the four
// new namespaces (path./command./unknown./action.) as well as the
// already-protected six (edge./hook./mcp./llm./runtime./agent.).
func TestPolicyMapper_StripMetricEmittedPerNamespace(t *testing.T) {
	event := newBaseHookEvent("Bash", map[string]any{"command": "rm -rf /"})
	event.Labels = Labels{
		"path.class":     "file",
		"command.class":  "safe",
		"unknown.impact": "low",
		"action.name":    "fake.bash",
		"edge.kind":      "fake.kind",
		"hook.tool_name": "fake_tool",
		"benign":         "kept",
	}

	classification, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}

	beforePath := readStripCounter(t, "path")
	beforeCommand := readStripCounter(t, "command")
	beforeUnknown := readStripCounter(t, "unknown")
	beforeAction := readStripCounter(t, "action")
	beforeEdge := readStripCounter(t, "edge")
	beforeHook := readStripCounter(t, "hook")

	_ = mapLabelsForPolicy(event, classification)

	for _, want := range []struct {
		ns     string
		before float64
	}{
		{"path", beforePath},
		{"command", beforeCommand},
		{"unknown", beforeUnknown},
		{"action", beforeAction},
		{"edge", beforeEdge},
		{"hook", beforeHook},
	} {
		got := readStripCounter(t, want.ns)
		if got <= want.before {
			t.Errorf("namespace=%q strip counter did not increment: before=%v after=%v",
				want.ns, want.before, got)
		}
	}
}

// readStripCounter reads the Prometheus counter value for a single
// namespace label so tests can assert the metric increments.
func readStripCounter(t *testing.T, namespace string) float64 {
	t.Helper()
	counter, err := edgeRequestLabelsStrippedTotal.GetMetricWithLabelValues(namespace)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%q): %v", namespace, err)
	}
	var m dto.Metric
	if err := counter.Write(&m); err != nil {
		t.Fatalf("counter.Write: %v", err)
	}
	if m.Counter == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// trustBoundaryContains is a local test helper to keep the file
// dependency-free.
func trustBoundaryContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// (compile-time use of pb to avoid unused-import in the placeholder
// tests; remove when the classifier_complete fields land and the real
// invariants are exercised through the policy evaluator.)
var _ = pb.DecisionType_DECISION_TYPE_DENY
