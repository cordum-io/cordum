package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/policy/actiongates"
)

func TestActionGateHTTPStatusMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code   string
		status int
	}{
		{actiongates.CodeUnauthorized, http.StatusUnauthorized},
		{actiongates.CodeAccessDenied, http.StatusForbidden},
		{actiongates.CodeNotFound, http.StatusNotFound},
		{actiongates.CodeConflict, http.StatusConflict},
		{actiongates.CodeServiceUnavailable, http.StatusServiceUnavailable},
		{actiongates.CodeResolverError, http.StatusServiceUnavailable},
		{actiongates.CodeInternalError, http.StatusInternalServerError},
		{actiongates.CodeRequireHuman, http.StatusOK},
		{"unknown_code", http.StatusInternalServerError},
		{"", http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			t.Parallel()
			if got := actionGateHTTPStatus(c.code); got != c.status {
				t.Fatalf("actionGateHTTPStatus(%q) = %d, want %d", c.code, got, c.status)
			}
		})
	}
}

func TestActionGateEdgeErrCodeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{actiongates.CodeUnauthorized, edgeErrCodeUnauthorized},
		{actiongates.CodeAccessDenied, edgeErrCodeAccessDenied},
		{actiongates.CodeNotFound, edgeErrCodeNotFound},
		{actiongates.CodeConflict, edgeErrCodeConflict},
		{actiongates.CodeServiceUnavailable, edgeErrCodeServiceUnavailable},
		{actiongates.CodeResolverError, edgeErrCodeServiceUnavailable},
		{actiongates.CodeInternalError, edgeErrCodeInternalError},
		{"weird_value", edgeErrCodeInternalError},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := actionGateEdgeErrCode(c.in); got != c.want {
				t.Fatalf("actionGateEdgeErrCode(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeActionGateDetails(t *testing.T) {
	t.Parallel()
	dec := actiongates.ActionGateDecision{
		Extra: map[string]string{
			"gate":          "actiongate.tenant",
			"sub_reason":    "cross_tenant:owner_mismatch",
			"target_type":   "user",
			"auth_tenant":   "tnt_a",
			"target_path":   "/etc/passwd",                   // should be stripped
			"target_url":    "https://leak.example.com/dump", // should be stripped
			"args":          "force=true",                    // should be stripped
			"claim_present": "true",
			"claim_chars":   "<=128",
			"approval_ref":  "appr_secret", // should be stripped
		},
	}
	got := sanitizeActionGateDetails(dec)
	wantKeys := []string{"gate", "sub_reason", "target_type", "auth_tenant", "claim_present", "claim_chars"}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing safe key %q in sanitized details", k)
		}
	}
	bannedKeys := []string{"target_path", "target_url", "args", "approval_ref"}
	for _, k := range bannedKeys {
		if _, ok := got[k]; ok {
			t.Errorf("unsafe key %q leaked into sanitized details", k)
		}
	}
}

func TestSanitizeActionGateDetailsEmpty(t *testing.T) {
	t.Parallel()
	// No safe keys -> nil result so writeEdgeError omits the field.
	dec := actiongates.ActionGateDecision{Extra: map[string]string{"target_path": "/etc/passwd"}}
	if got := sanitizeActionGateDetails(dec); got != nil {
		t.Fatalf("only-unsafe extras: got %#v, want nil", got)
	}
	// Nil extras -> nil result.
	if got := sanitizeActionGateDetails(actiongates.ActionGateDecision{}); got != nil {
		t.Fatalf("nil extras: got %#v, want nil", got)
	}
}

// TestWriteActionGatePolicyErrorAllStatuses exercises the 6 non-informational
// status codes through writeActionGatePolicyError, asserting both HTTP status
// and the edge envelope wire shape.
func TestWriteActionGatePolicyErrorAllStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		code       string
		wantStatus int
		wantCode   string
	}{
		{"unauthorized_401", actiongates.CodeUnauthorized, 401, edgeErrCodeUnauthorized},
		{"access_denied_403", actiongates.CodeAccessDenied, 403, edgeErrCodeAccessDenied},
		{"not_found_404", actiongates.CodeNotFound, 404, edgeErrCodeNotFound},
		{"conflict_409", actiongates.CodeConflict, 409, edgeErrCodeConflict},
		{"internal_500", actiongates.CodeInternalError, 500, edgeErrCodeInternalError},
		{"unavailable_503", actiongates.CodeServiceUnavailable, 503, edgeErrCodeServiceUnavailable},
		{"resolver_error_503", actiongates.CodeResolverError, 503, edgeErrCodeServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dec := actiongates.ActionGateDecision{
				Code:      c.code,
				GateID:    "actiongate.test",
				Reason:    "test reason",
				SubReason: "test_sub",
				Extra:     map[string]string{"gate": "actiongate.test", "sub_reason": "test_sub"},
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/simulate", nil)
			writeActionGatePolicyError(rr, req, "simulate", dec)

			if rr.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, c.wantStatus)
			}
			var env edgeErrorEnvelope
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
			}
			if env.Code != c.wantCode {
				t.Fatalf("envelope.code = %q, want %q", env.Code, c.wantCode)
			}
			if env.Message != "test reason" {
				t.Fatalf("envelope.message = %q, want \"test reason\"", env.Message)
			}
			if env.Details == nil || env.Details["gate"] != "actiongate.test" || env.Details["sub_reason"] != "test_sub" {
				t.Fatalf("envelope.details = %#v, want gate+sub_reason fields", env.Details)
			}
		})
	}
}

// TestWriteActionGatePolicyErrorRequireHumanSimulateIs200 verifies that
// REQUIRE_HUMAN at the simulate endpoint returns 200 informational JSON,
// NOT an error envelope. The body shape mirrors the regular simulate
// response so SDKs can switch on decision=REQUIRE_HUMAN.
func TestWriteActionGatePolicyErrorRequireHumanSimulateIs200(t *testing.T) {
	t.Parallel()
	dec := actiongates.ActionGateDecision{
		Code:      actiongates.CodeRequireHuman,
		GateID:    "actiongate.mutation",
		Reason:    "destructive action requires human approval",
		SubReason: "missing_approval",
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/simulate", nil)
	writeActionGatePolicyError(rr, req, "simulate", dec)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for simulate REQUIRE_HUMAN", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["decision"] != "DECISION_TYPE_REQUIRE_HUMAN" {
		t.Fatalf("decision = %v, want DECISION_TYPE_REQUIRE_HUMAN", body["decision"])
	}
	if body["rule_id"] != "actiongate.mutation" || body["gate"] != "actiongate.mutation" {
		t.Fatalf("rule_id/gate missing: %#v", body)
	}
	if body["sub_reason"] != "missing_approval" {
		t.Fatalf("sub_reason = %v, want missing_approval", body["sub_reason"])
	}
	// Must NOT be the edge error envelope.
	if _, hasCode := body["code"]; hasCode {
		if _, hasMsg := body["message"]; hasMsg {
			t.Fatal("simulate REQUIRE_HUMAN must not return an edge error envelope")
		}
	}
}

func TestWriteActionGatePolicyErrorRequireHumanEvaluateIs409(t *testing.T) {
	t.Parallel()
	dec := actiongates.ActionGateDecision{
		Code:      actiongates.CodeRequireHuman,
		GateID:    "actiongate.mutation",
		Reason:    "destructive action requires human approval",
		SubReason: "missing_approval",
		Extra: map[string]string{
			"gate":        "actiongate.mutation",
			"sub_reason":  "missing_approval",
			"target_path": "/secrets/prod.env",
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", nil)
	writeActionGatePolicyError(rr, req, "evaluate", dec)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for evaluate REQUIRE_HUMAN", rr.Code)
	}
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode edge envelope: %v body=%s", err, rr.Body.String())
	}
	if env.Code != edgeErrCodeConflict {
		t.Fatalf("envelope.code = %q, want %q", env.Code, edgeErrCodeConflict)
	}
	if env.Code == edgeErrCodeInternalError || rr.Code == http.StatusOK {
		t.Fatalf("legacy REQUIRE_HUMAN shape returned: status=%d code=%q", rr.Code, env.Code)
	}
	if env.Message != "destructive action requires human approval" {
		t.Fatalf("message = %q, want sanitized policy reason", env.Message)
	}
	if env.Details == nil || env.Details["gate"] != "actiongate.mutation" ||
		env.Details["sub_reason"] != "missing_approval" {
		t.Fatalf("details = %#v, want sanitized gate and sub_reason", env.Details)
	}
	if _, leaked := env.Details["target_path"]; leaked {
		t.Fatalf("details leaked unsafe target_path: %#v", env.Details)
	}
}

func TestSafeActionGateExtraKeysCoverDefaults(t *testing.T) {
	t.Parallel()
	must := []string{"gate", "sub_reason", "target_type", "auth_tenant"}
	for _, k := range must {
		if _, ok := safeActionGateExtraKeys[k]; !ok {
			t.Errorf("safeActionGateExtraKeys missing %q", k)
		}
	}
}

// TestPolicySimulateActionGateMapping is the per-DoD-line marker that
// the 6 status codes are mapped. This is a guard against future Code
// additions that forget to wire HTTP status.
func TestPolicySimulateActionGateMapping(t *testing.T) {
	t.Parallel()
	codes := map[string]int{
		actiongates.CodeUnauthorized:       401,
		actiongates.CodeAccessDenied:       403,
		actiongates.CodeNotFound:           404,
		actiongates.CodeConflict:           409,
		actiongates.CodeInternalError:      500,
		actiongates.CodeServiceUnavailable: 503,
		actiongates.CodeResolverError:      503,
	}
	for code, want := range codes {
		if got := actionGateHTTPStatus(code); got != want {
			t.Errorf("DoD line 4 mapping broken: %q -> %d, want %d", code, got, want)
		}
	}
	// Bonus: simulate REQUIRE_HUMAN must not be a 4xx/5xx.
	if got := actionGateHTTPStatus(actiongates.CodeRequireHuman); got >= 400 {
		t.Errorf("require_human at simulate must be informational, got %d", got)
	}
	// Bonus: also verify edge envelope coverage so callers can switch on `code`.
	for code := range codes {
		if ec := actionGateEdgeErrCode(code); ec == "" || strings.TrimSpace(ec) == "" {
			t.Errorf("edge envelope code missing for gate Code %q", code)
		}
	}
}
