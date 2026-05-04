package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

const fixtureDir = "testdata/hooks"

func loadHookFixture(t *testing.T, name string) HookInput {
	t.Helper()
	path := filepath.Join(fixtureDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	input, err := parseHookInput(data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return input
}

func newTestMappingContext() MappingContext {
	frozen := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	return MappingContext{
		TenantID:     "tenant-edge-test",
		PrincipalID:  "principal-edge-test",
		SessionID:    "edge_sess_test",
		ExecutionID:  "edge_exec_test",
		AgentProduct: "claude-code",
		AgentVersion: "2.1.x",
		Now:          func() time.Time { return frozen },
		HashMode:     edge.RedactionHashRedacted,
	}
}

func TestMapHookInputBashTest(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_bash_test.json")
	ctx := newTestMappingContext()

	got, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}

	if got.Layer != edge.LayerHook {
		t.Errorf("Layer = %q, want %q", got.Layer, edge.LayerHook)
	}
	if got.Kind != edge.EventKindHookPreToolUse {
		t.Errorf("Kind = %q, want %q", got.Kind, edge.EventKindHookPreToolUse)
	}
	if got.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", got.ToolName)
	}
	if got.ToolUseID != "tu_synthetic_1" {
		t.Errorf("ToolUseID = %q, want tu_synthetic_1", got.ToolUseID)
	}
	if got.SessionID != ctx.SessionID {
		t.Errorf("SessionID = %q, want %q (from context, not hook payload)", got.SessionID, ctx.SessionID)
	}
	if got.ExecutionID != ctx.ExecutionID {
		t.Errorf("ExecutionID = %q, want %q", got.ExecutionID, ctx.ExecutionID)
	}
	if got.TenantID != ctx.TenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, ctx.TenantID)
	}
	if got.Capability != "exec.shell" {
		t.Errorf("Capability = %q, want exec.shell", got.Capability)
	}
	if !containsRisk(got.RiskTags, "exec") || !containsRisk(got.RiskTags, "test") {
		t.Errorf("RiskTags = %v, want exec+test", got.RiskTags)
	}
	if got.Labels["command.class"] != "safe" {
		t.Errorf("Labels[command.class] = %q, want safe; labels=%v", got.Labels["command.class"], got.Labels)
	}
	if got.Labels["command.family"] != "test" {
		t.Errorf("Labels[command.family] = %q, want test", got.Labels["command.family"])
	}
	if got.Labels["edge.layer"] != string(edge.LayerHook) {
		t.Errorf("Labels[edge.layer] = %q, want hook", got.Labels["edge.layer"])
	}
	if got.Labels["hook.tool_name"] != "Bash" {
		t.Errorf("Labels[hook.tool_name] = %q, want Bash", got.Labels["hook.tool_name"])
	}
	if !strings.HasPrefix(got.InputHash, "sha256:") {
		t.Errorf("InputHash = %q, want sha256: prefix", got.InputHash)
	}
	if !strings.HasPrefix(got.ActionHash, "sha256:") {
		t.Errorf("ActionHash = %q, want sha256: prefix", got.ActionHash)
	}
	if got.ReasonCode != "" {
		t.Errorf("ReasonCode = %q, want empty for normal mapping", got.ReasonCode)
	}
	// EDGE-041: tool_input.command renamed to command_redacted on the wire so
	// the dashboard sanitizer renders the value (bare `command` is not on the
	// dashboard's strip list, but renaming consistently keeps the contract
	// uniform across all tool_input fields). Bare key MUST NOT survive.
	if cmd, _ := got.InputRedacted["command_redacted"].(string); cmd != "go test ./core/edge" {
		t.Errorf("InputRedacted[command_redacted] = %q, want go test ./core/edge", cmd)
	}
	if _, present := got.InputRedacted["command"]; present {
		t.Errorf("InputRedacted retains bare command key; want only command_redacted (got %v)", got.InputRedacted)
	}
	if len(got.RawPayload) == 0 {
		t.Errorf("RawPayload empty, want verbatim hook stdin bytes for agentd forward")
	}
}

func TestMapHookInputBashDestructiveCarriesDestructiveTags(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_bash_destructive.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Labels["command.class"] != "destructive" {
		t.Errorf("Labels[command.class] = %q, want destructive", got.Labels["command.class"])
	}
	if !containsRisk(got.RiskTags, "destructive") {
		t.Errorf("RiskTags = %v, want destructive present", got.RiskTags)
	}
	if got.Capability != "exec.shell" {
		t.Errorf("Capability = %q, want exec.shell", got.Capability)
	}
}

func TestMapHookInputReadSecretCarriesSecretsTag(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_read_secret.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Capability != "file.read" {
		t.Errorf("Capability = %q, want file.read", got.Capability)
	}
	if got.Labels["path.class"] != "secret" {
		t.Errorf("Labels[path.class] = %q, want secret", got.Labels["path.class"])
	}
	if !containsRisk(got.RiskTags, "secrets") {
		t.Errorf("RiskTags = %v, want secrets present", got.RiskTags)
	}
}

func TestMapHookInputEditWriteCapability(t *testing.T) {
	for _, name := range []string{"pre_tool_use_edit.json", "pre_tool_use_multiedit.json"} {
		t.Run(name, func(t *testing.T) {
			input := loadHookFixture(t, name)
			got, err := MapHookInput(input, newTestMappingContext())
			if err != nil {
				t.Fatalf("MapHookInput: %v", err)
			}
			if got.Capability != "file.write" {
				t.Errorf("Capability = %q, want file.write", got.Capability)
			}
			if got.Kind != edge.EventKindHookPreToolUse {
				t.Errorf("Kind = %q, want hook.pre_tool_use", got.Kind)
			}
		})
	}
}

func TestMapHookInputUnknownToolUsesUnknownCapability(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_unknown_tool.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Capability != "edge.unknown" {
		t.Errorf("Capability = %q, want edge.unknown", got.Capability)
	}
	if !containsRisk(got.RiskTags, "review_required") {
		t.Errorf("RiskTags = %v, want review_required for unknown tool", got.RiskTags)
	}
}

// EDGE-066 — mapHookEventToKind must accept "PolicyDecision" and
// "PermissionRequest" as valid Claude hook event names. event.go declares
// EventKindHookPolicyDecision + EventKindHookPermissionRequest as canonical
// EventKind values, but the mapper switch only handled 6 of the 8 hook
// kinds defined in event.go. Pre-fix, those names fall through to the
// default branch and the mapper degrades the event with
// "unsupported hook event name" — the same shape of failure that EDGE-049
// fixed for UserPromptSubmit.
func TestMapHookEventToKindHandlesPolicyDecision(t *testing.T) {
	got, ok := mapHookEventToKind("PolicyDecision")
	if !ok {
		t.Fatalf("mapHookEventToKind(\"PolicyDecision\") ok = false, want true")
	}
	if got != edge.EventKindHookPolicyDecision {
		t.Fatalf("mapHookEventToKind(\"PolicyDecision\") = %q, want %q", got, edge.EventKindHookPolicyDecision)
	}
}

func TestMapHookEventToKindHandlesPermissionRequest(t *testing.T) {
	got, ok := mapHookEventToKind("PermissionRequest")
	if !ok {
		t.Fatalf("mapHookEventToKind(\"PermissionRequest\") ok = false, want true")
	}
	if got != edge.EventKindHookPermissionRequest {
		t.Fatalf("mapHookEventToKind(\"PermissionRequest\") = %q, want %q", got, edge.EventKindHookPermissionRequest)
	}
}

// Negative regression — typos / unknown event names still fall through
// to the unsupported-hook-event default branch.
func TestMapHookEventToKindRejectsUnknownEvent(t *testing.T) {
	if _, ok := mapHookEventToKind("PolicyDecisionTypo"); ok {
		t.Fatalf("mapHookEventToKind(\"PolicyDecisionTypo\") ok = true, want false (unknown name must NOT map)")
	}
}

func TestMapHookInputUserPromptSubmit(t *testing.T) {
	input := loadHookFixture(t, "user_prompt_submit.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Kind != edge.EventKindHookUserPromptSubmit {
		t.Errorf("Kind = %q, want hook.user_prompt_submit", got.Kind)
	}
	if got.ToolName != "" {
		t.Errorf("ToolName = %q, want empty for UserPromptSubmit", got.ToolName)
	}
	if _, ok := got.InputRedacted["prompt_redacted"]; !ok {
		t.Errorf("InputRedacted missing prompt_redacted key; got %v", got.InputRedacted)
	}
}

func TestMapHookInputPostToolUseSuccess(t *testing.T) {
	input := loadHookFixture(t, "post_tool_use_success.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Kind != edge.EventKindHookPostToolUse {
		t.Errorf("Kind = %q, want hook.post_tool_use", got.Kind)
	}
	if got.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", got.ToolName)
	}
}

func TestMapHookInputPostToolUseFailure(t *testing.T) {
	input := loadHookFixture(t, "post_tool_use_failure.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Kind != edge.EventKindHookPostToolUseFailure {
		t.Errorf("Kind = %q, want hook.post_tool_use_failure", got.Kind)
	}
}

// TestMapHookInputPreToolUseEditEmitsRedactedSuffix asserts the EDGE-041
// per-field rename for Edit's tool_input fields. file_path/old_string/
// new_string are renamed with a `_redacted` suffix on the wire so the
// dashboard sanitizer accepts them; bare keys must NOT survive.
func TestMapHookInputPreToolUseEditEmitsRedactedSuffix(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_edit.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	want := map[string]string{
		"file_path_redacted":  "core/edge/claude/mapper.go",
		"old_string_redacted": "redacted-old",
		"new_string_redacted": "redacted-new",
	}
	for key, expected := range want {
		actual, _ := got.InputRedacted[key].(string)
		if actual != expected {
			t.Errorf("InputRedacted[%q] = %q, want %q", key, actual, expected)
		}
	}
	for _, bareKey := range []string{"file_path", "old_string", "new_string"} {
		if _, present := got.InputRedacted[bareKey]; present {
			t.Errorf("InputRedacted retains bare key %q after rename; want only the *_redacted variant (got %v)", bareKey, got.InputRedacted)
		}
	}
}

// TestMapHookInputPostToolUseSuccessEmitsToolResponseRedacted asserts the
// EDGE-041 PostToolUse rename: tool_response is wrapped under a
// `tool_response_redacted` key. Bare `tool_response` must NOT survive — the
// dashboard's transform.ts isUnsafeEdgeKey lists it explicitly.
func TestMapHookInputPostToolUseSuccessEmitsToolResponseRedacted(t *testing.T) {
	input := loadHookFixture(t, "post_tool_use_success.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	resp, ok := got.InputRedacted["tool_response_redacted"].(map[string]any)
	if !ok {
		t.Fatalf("InputRedacted[tool_response_redacted] missing or wrong type; got %v", got.InputRedacted)
	}
	if stdout, _ := resp["stdout"].(string); stdout != "redacted-stdout" {
		t.Errorf("tool_response_redacted.stdout = %q, want redacted-stdout", stdout)
	}
	if _, present := got.InputRedacted["tool_response"]; present {
		t.Errorf("InputRedacted retains bare tool_response key; want only tool_response_redacted (got %v)", got.InputRedacted)
	}
}

// TestMapHookInputPreToolUseUnknownFieldsBucketed asserts that Claude
// tool_input fields the mapper does not know about (version drift, new
// tools) fall through into a `tool_input_redacted` bucket so evidence does
// not silently drop unknown content.
func TestMapHookInputPreToolUseUnknownFieldsBucketed(t *testing.T) {
	got, err := MapHookInput(HookInput{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput: map[string]any{
			"command":           "npm test",
			"unrecognized_flag": true,
			"future_field":      "some-value",
		},
	}, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if cmd, _ := got.InputRedacted["command_redacted"].(string); cmd != "npm test" {
		t.Errorf("InputRedacted[command_redacted] = %q, want npm test", cmd)
	}
	bucket, ok := got.InputRedacted["tool_input_redacted"].(map[string]any)
	if !ok {
		t.Fatalf("InputRedacted[tool_input_redacted] missing or wrong type; got %v", got.InputRedacted)
	}
	if v, _ := bucket["unrecognized_flag"].(bool); !v {
		t.Errorf("tool_input_redacted[unrecognized_flag] = %v, want true", bucket["unrecognized_flag"])
	}
	if v, _ := bucket["future_field"].(string); v != "some-value" {
		t.Errorf("tool_input_redacted[future_field] = %q, want some-value", v)
	}
}

func TestMapHookInputMissingToolNameDegrades(t *testing.T) {
	input := loadHookFixture(t, "missing_tool_name.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.ReasonCode != "missing_tool_name" {
		t.Errorf("ReasonCode = %q, want missing_tool_name", got.ReasonCode)
	}
	if got.Capability != "edge.unknown" {
		t.Errorf("Capability = %q, want edge.unknown for degraded mapping", got.Capability)
	}
}

func TestMapHookInputMissingToolInputDegrades(t *testing.T) {
	input := loadHookFixture(t, "missing_tool_input.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.ReasonCode != "missing_tool_input" {
		t.Errorf("ReasonCode = %q, want missing_tool_input", got.ReasonCode)
	}
	// A missing-input Bash event must NOT classify as safe; treat as
	// review_required so policy can deny it.
	if got.Labels["command.class"] == "safe" {
		t.Errorf("missing tool_input must not be safe; labels=%v", got.Labels)
	}
}

func TestMapHookInputUnknownFutureFieldsDoNotPanicAndAreRedacted(t *testing.T) {
	input := loadHookFixture(t, "unknown_future_fields.json")
	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.Kind != edge.EventKindHookPreToolUse {
		t.Errorf("Kind = %q, want hook.pre_tool_use", got.Kind)
	}
	if got.Capability != "exec.shell" {
		t.Errorf("Capability = %q, want exec.shell despite unknown extras", got.Capability)
	}
	// Future top-level fields must NOT appear as raw values in labels (they
	// are not part of the AgentActionEvent contract). Either the mapper
	// drops them or stores them in InputRedacted.future_*; never in Labels.
	for k := range got.Labels {
		if strings.HasPrefix(k, "future_") || strings.HasPrefix(k, "experimental") {
			t.Errorf("Labels contains untrusted future-field key %q", k)
		}
	}
}

func TestMapHookInputReusesContextSessionEvenWhenHookHasOne(t *testing.T) {
	// Hook payload has session_id=sess_synthetic_pretooluse_bash;
	// context has session_id=edge_sess_test. The runner-supplied context
	// must win because cordum-agentd's session is the source of truth, not
	// whatever Claude reports in the hook payload.
	input := loadHookFixture(t, "pre_tool_use_bash_test.json")
	ctx := newTestMappingContext()
	got, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if got.SessionID != ctx.SessionID {
		t.Errorf("SessionID = %q, want %q (context wins over hook payload)", got.SessionID, ctx.SessionID)
	}
}

func TestMapHookInputInputHashIsDeterministic(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_bash_test.json")
	ctx := newTestMappingContext()
	first, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("first MapHookInput: %v", err)
	}
	second, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("second MapHookInput: %v", err)
	}
	if first.InputHash != second.InputHash {
		t.Errorf("InputHash not deterministic: first=%q second=%q", first.InputHash, second.InputHash)
	}
	if first.ActionHash != second.ActionHash {
		t.Errorf("ActionHash not deterministic: first=%q second=%q", first.ActionHash, second.ActionHash)
	}
}

func TestMapHookInputDoesNotEchoRawSecretIntoLabels(t *testing.T) {
	const rawSecret = "Bearer claude-mapper-test-secret-EDGE016"
	input := loadHookFixture(t, "pre_tool_use_bash_test.json")
	// Inject a synthetic secret into both the in-memory raw payload and a
	// known field; assert it never appears in any output the mapper hands
	// to the gateway.
	input.RawPayload = []byte(`{"command":"echo ` + rawSecret + `"}`)
	input.ToolInput["command"] = "echo " + rawSecret

	got, err := MapHookInput(input, newTestMappingContext())
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	for k, v := range got.Labels {
		if strings.Contains(k, rawSecret) || strings.Contains(v, rawSecret) {
			t.Errorf("Labels leaked raw secret: %q=%q", k, v)
		}
	}
	// EDGE-041: command renamed to command_redacted on the wire.
	if cmd, _ := got.InputRedacted["command_redacted"].(string); strings.Contains(cmd, rawSecret) {
		t.Errorf("InputRedacted[command_redacted] leaked raw secret: %q", cmd)
	}
	// Reason code stays empty for normal flow even with secrets.
	if got.ReasonCode != "" {
		t.Errorf("ReasonCode = %q, want empty", got.ReasonCode)
	}
}

// TestMapHookInputNotImplementedYet pins the temporary error from step-4 so
// step-7 can flip it to a real error-free implementation. Once the mapper is
// implemented this test is deleted, not edited.
func TestMapHookInputNotImplementedYet(t *testing.T) {
	if testing.Short() {
		t.Skip("step-4 sentinel; remove with step-7")
	}
	t.Skip("placeholder kept for step-7 visibility; safe to delete after impl")
}

func containsRisk(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// TestAgentdRequestCarriesMappedActionFields locks in the EDGE-016 step-10
// wiring: when the runner builds an AgentdRequest from a HookInput, it
// must populate the EDGE-016 mapped/redacted/hashed fields (Layer/Kind/
// Capability/RiskTags/Labels/InputRedacted/InputHash/ActionHash/TenantID/
// PrincipalID/ReasonCode) so cordum-agentd can call Gateway evaluate
// without re-classifying. SessionID/ExecutionID also come from the
// mapping context (env vars), not the hook payload.
func TestAgentdRequestCarriesMappedActionFields(t *testing.T) {
	input := loadHookFixture(t, "pre_tool_use_bash_test.json")
	env := map[string]string{
		"CORDUM_EDGE_SESSION_ID":   "edge_sess_runner_test",
		"CORDUM_EDGE_EXECUTION_ID": "edge_exec_runner_test",
		"CORDUM_TENANT_ID":         "tenant-runner",
		"CORDUM_EDGE_PRINCIPAL_ID": "principal-runner",
		"CORDUM_AGENT_PRODUCT":     "claude-code",
		"CORDUM_AGENT_VERSION":     "2.1.x",
	}
	req := agentdRequest(input, []string{"cordum-hook"}, env)

	if req.Layer != "hook" {
		t.Errorf("AgentdRequest.Layer = %q, want hook", req.Layer)
	}
	if req.Kind != "hook.pre_tool_use" {
		t.Errorf("AgentdRequest.Kind = %q, want hook.pre_tool_use", req.Kind)
	}
	if req.Capability != "exec.shell" {
		t.Errorf("AgentdRequest.Capability = %q, want exec.shell", req.Capability)
	}
	if req.TenantID != "tenant-runner" {
		t.Errorf("AgentdRequest.TenantID = %q, want tenant-runner", req.TenantID)
	}
	if req.PrincipalID != "principal-runner" {
		t.Errorf("AgentdRequest.PrincipalID = %q, want principal-runner", req.PrincipalID)
	}
	if req.SessionID != "edge_sess_runner_test" {
		t.Errorf("AgentdRequest.SessionID = %q, want edge_sess_runner_test (from env)", req.SessionID)
	}
	if req.ExecutionID != "edge_exec_runner_test" {
		t.Errorf("AgentdRequest.ExecutionID = %q, want edge_exec_runner_test (from env)", req.ExecutionID)
	}
	if !strings.HasPrefix(req.InputHash, "sha256:") {
		t.Errorf("AgentdRequest.InputHash = %q, want sha256: prefix", req.InputHash)
	}
	if !strings.HasPrefix(req.ActionHash, "sha256:") {
		t.Errorf("AgentdRequest.ActionHash = %q, want sha256: prefix", req.ActionHash)
	}
	if req.Labels["command.class"] != "safe" {
		t.Errorf("AgentdRequest.Labels[command.class] = %q, want safe", req.Labels["command.class"])
	}
	if !containsRisk(req.RiskTags, "test") {
		t.Errorf("AgentdRequest.RiskTags = %v, want test present", req.RiskTags)
	}
	if len(req.RawPayload) == 0 {
		t.Errorf("AgentdRequest.RawPayload empty; runner must still forward verbatim bytes for in-memory agentd boundary")
	}
	// Raw fields must still be populated for backward compat with older
	// agentd builds that don't read the new mapped fields.
	if req.ToolName != "Bash" {
		t.Errorf("AgentdRequest.ToolName = %q, want Bash", req.ToolName)
	}
	if cmd, _ := req.ToolInput["command"].(string); cmd != "go test ./core/edge" {
		t.Errorf("AgentdRequest.ToolInput[command] = %q", cmd)
	}
}

// EDGE-046 regression tests: redactHookBoundaryString must NOT wholesale
// replace strings that mention "secret" as a normal English word, file path,
// or CLI flag. The previous final guard `strings.Contains(... "secret")` was
// over-broad and confused CONTEXT (the user is talking about secrets) with
// CONTENT (an actual secret value). The other 4 guards (`result.Redacted`,
// `result.Truncated`, `diagnostic != candidate`, and the `[REDACTED]` marker
// substring) cover real-leak detection precisely.

func TestRedactHookBoundaryStringPreservesPlainEnglishWithSecretsWord(t *testing.T) {
	// Common in agent prompts: user describes a workflow that touches secret
	// material without including any actual secret value. The whole prompt
	// must round-trip unchanged.
	prompt := "Please read the file /tmp/secrets/.env and then create a new file at ./hello.go with a hello-world Go program."
	got := redactHookBoundaryString(prompt)
	if got != prompt {
		t.Fatalf("redactHookBoundaryString(plain English mentioning 'secrets') = %q, want %q (input round-trips unchanged)", got, prompt)
	}
}

func TestRedactHookBoundaryStringPreservesFilePathWithSecretsDir(t *testing.T) {
	// Standard secret-mount paths on POSIX systems are everywhere in
	// production runbooks; the file_path field carrying one is not itself a
	// secret leak, just a reference to where one lives.
	for _, path := range []string{
		"/etc/secrets/credentials.json",
		"/var/run/secrets/kubernetes.io/serviceaccount/token",
		"~/.aws/credentials",
		"C:/Users/ops/secret-store/policy.yaml",
	} {
		got := redactHookBoundaryString(path)
		if got != path {
			t.Errorf("redactHookBoundaryString(%q) = %q, want unchanged", path, got)
		}
	}
}

func TestRedactHookBoundaryStringPreservesCliFlagWithSecretWord(t *testing.T) {
	// CLI flag names like --secret-name=my-vault use "secret" as a domain
	// keyword, not as a secret value. The whole command line should pass
	// through untouched.
	cmd := "kubectl get secret my-vault --namespace=prod --output=name"
	got := redactHookBoundaryString(cmd)
	if got != cmd {
		t.Fatalf("redactHookBoundaryString(%q) = %q, want unchanged", cmd, got)
	}
}

func TestRedactHookBoundaryStringStillRedactsActualBearer(t *testing.T) {
	// EDGE-004's bearerPattern is the real leak vector here. Defense-in-depth
	// guards (`result.Redacted` / `[REDACTED]` substring) must continue to
	// fire for these.
	leak := "Authorization: Bearer abc123def456ghi789jkl012mno345pqr678"
	got := redactHookBoundaryString(leak)
	if got != "<redacted>" {
		t.Fatalf("redactHookBoundaryString(actual Bearer) = %q, want <redacted>", got)
	}
}

func TestRedactHookBoundaryStringStillRedactsAPIKey(t *testing.T) {
	// envSecretPattern catches assignments of API keys.
	leak := "API_KEY=sk-proj-abc1234567890supersecretvalue"
	got := redactHookBoundaryString(leak)
	if got != "<redacted>" {
		t.Fatalf("redactHookBoundaryString(API_KEY=...) = %q, want <redacted>", got)
	}
}

func TestRedactHookBoundaryStringStillRedactsAWSKey(t *testing.T) {
	// awsKeyPattern catches AKIA... access keys.
	leak := "AKIAIOSFODNN7EXAMPLE"
	got := redactHookBoundaryString(leak)
	if got != "<redacted>" {
		t.Fatalf("redactHookBoundaryString(AKIA...) = %q, want <redacted>", got)
	}
}

func TestRedactHookBoundaryStringStillRedactsEnvSecretAssignment(t *testing.T) {
	// envSecretPattern catches CLIENT_SECRET / PASSWORD / etc. assignments.
	for _, leak := range []string{
		"CLIENT_SECRET=abcdef0123456789",
		"PASSWORD=hunter2-but-really-long-version",
		"PRIVATE_KEY=verysecretkeyvaluelongenoughtomatch",
	} {
		got := redactHookBoundaryString(leak)
		if got != "<redacted>" {
			t.Errorf("redactHookBoundaryString(%q) = %q, want <redacted>", leak, got)
		}
	}
}
