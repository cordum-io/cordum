package workflow

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// TestStep_PolicyGate_RoundTrip asserts that PolicyGate marshals and
// unmarshals losslessly through JSON for each accepted value.
func TestStep_PolicyGate_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, gate := range []string{"allow", "deny", "require_approval"} {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
			step := Step{ID: "s1", Name: "step-1", Type: StepTypeWorker, PolicyGate: gate}
			raw, err := json.Marshal(step)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), `"policy_gate":"`+gate+`"`) {
				t.Fatalf("marshaled JSON missing policy_gate=%q: %s", gate, raw)
			}
			var got Step
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.PolicyGate != gate {
				t.Fatalf("PolicyGate: got %q, want %q", got.PolicyGate, gate)
			}
		})
	}
}

// TestStep_PolicyGate_OmitEmpty asserts that an unset PolicyGate is
// elided from JSON. Empty string is the "no hint" semantic and must
// not surface as a present-but-empty key on the wire.
func TestStep_PolicyGate_OmitEmpty(t *testing.T) {
	t.Parallel()
	step := Step{ID: "s1", Name: "step-1", Type: StepTypeWorker}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "policy_gate") {
		t.Fatalf("expected policy_gate elided when empty, got: %s", raw)
	}
}

// TestStep_PolicyGate_InvalidValueRejectedByIsValidPolicyGate asserts the
// canonical-values predicate accepts all 3 documented values and rejects
// everything else, including case mismatches and adjacent-but-distinct
// values like "constrain" or "allow_with_constraints" (which are
// SafetyDecisionType members, not PolicyGate values). The validation entry
// point at handlers_workflows.go consults IsValidPolicyGate; this is the
// unit-level guard.
func TestStep_PolicyGate_InvalidValueRejectedByIsValidPolicyGate(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{PolicyGateAllow, PolicyGateDeny, PolicyGateRequireApproval} {
		if !IsValidPolicyGate(valid) {
			t.Fatalf("IsValidPolicyGate rejected canonical value %q", valid)
		}
	}
	for _, invalid := range []string{"escalate", "ALLOW", "constrain", "throttle", "allow_with_constraints", " allow", "approval", ""} {
		if IsValidPolicyGate(invalid) {
			t.Fatalf("IsValidPolicyGate accepted invalid value %q", invalid)
		}
	}
}

// TestStep_PolicyGate_ConstantsMatchJSONWireValues asserts the exported
// constants stay aligned with the snake_case strings the JSON tag emits;
// changing one without the other would silently break clients.
func TestStep_PolicyGate_ConstantsMatchJSONWireValues(t *testing.T) {
	t.Parallel()
	if PolicyGateAllow != "allow" {
		t.Errorf("PolicyGateAllow = %q, want \"allow\"", PolicyGateAllow)
	}
	if PolicyGateDeny != "deny" {
		t.Errorf("PolicyGateDeny = %q, want \"deny\"", PolicyGateDeny)
	}
	if PolicyGateRequireApproval != "require_approval" {
		t.Errorf("PolicyGateRequireApproval = %q, want \"require_approval\"", PolicyGateRequireApproval)
	}
}

// TestStep_PolicyGate_IsValidPolicyGateConcurrentRead exercises the exported
// predicate from multiple goroutines; callers share this read-only validator.
func TestStep_PolicyGate_IsValidPolicyGateConcurrentRead(t *testing.T) {
	t.Parallel()
	values := []string{
		PolicyGateAllow,
		PolicyGateDeny,
		PolicyGateRequireApproval,
		"",
		"ALLOW",
		"allow_with_constraints",
	}
	var wg sync.WaitGroup
	errs := make(chan string, len(values)*16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, value := range values {
				got := IsValidPolicyGate(value)
				want := value == PolicyGateAllow ||
					value == PolicyGateDeny ||
					value == PolicyGateRequireApproval
				if got != want {
					errs <- value
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for value := range errs {
		t.Errorf("IsValidPolicyGate(%q) returned unexpected result", value)
	}
}

// TestStepRun_AuditHash_RoundTrip asserts that AuditHash marshals and
// unmarshals losslessly through JSON for a populated 64-char hex hash.
func TestStepRun_AuditHash_RoundTrip(t *testing.T) {
	t.Parallel()
	const hash = "a1b2c3d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff00"
	if len(hash) != 64 {
		t.Fatalf("test-fixture hash must be 64 hex chars (got %d)", len(hash))
	}
	step := StepRun{StepID: "s1", Status: StepStatusSucceeded, JobID: "job-001", AuditHash: hash}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"audit_hash":"`+hash+`"`) {
		t.Fatalf("marshaled JSON missing audit_hash: %s", raw)
	}
	var got StepRun
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AuditHash != hash {
		t.Fatalf("AuditHash: got %q, want %q", got.AuditHash, hash)
	}
}

// TestStepRun_AuditHash_OmitEmpty asserts that an unset AuditHash is
// elided from JSON. Skipped/upstream-failed/never-emitted steps must
// not surface a present-but-empty audit_hash key.
func TestStepRun_AuditHash_OmitEmpty(t *testing.T) {
	t.Parallel()
	step := StepRun{StepID: "skipped-step", Status: StepStatusSkipped, SkipReason: "upstream failed"}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "audit_hash") {
		t.Fatalf("expected audit_hash elided when empty, got: %s", raw)
	}
}
