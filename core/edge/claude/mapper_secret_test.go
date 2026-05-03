package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	step12BearerToken = "sk-test-secret-token-0000"
	step12GitHubToken = "ghp_testtoken123456"
	step12AWSAccess   = "AKIAIOSFODNN7EXAMPLE"
	step12AWSSecret   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	step12Password    = "password=hunter2"
	step12SecretPath  = "secret-worktree"
)

func TestMapHookInputRedactsSecretsFromMappedActionOutsideRawPayload(t *testing.T) {
	input := mustParseStep12HookInput(t, `{
		"hook_event_name":"PostToolUse",
		"session_id":"sess_synthetic_secret",
		"transcript_path":"/redacted/`+step12SecretPath+`/`+step12BearerToken+`.jsonl",
		"cwd":"/redacted/`+step12SecretPath+`/repo",
		"tool_name":"Bash",
		"tool_use_id":"tu_secret",
		"tool_input":{
			"command":"echo Authorization: Bearer `+step12BearerToken+`",
			"env":{"AWS_SECRET_ACCESS_KEY":"`+step12AWSSecret+`"},
			"metadata":{"neutral":"prefix_`+step12GitHubToken+`"}
		},
		"tool_response":{
			"stdout":"`+step12AWSAccess+`",
			"stderr":"`+step12Password+`"
		},
		"duration_ms":12
	}`)
	ctx := newTestMappingContext()
	ctx.TenantID = "tenant-" + step12BearerToken
	ctx.PrincipalID = "principal-" + step12GitHubToken
	ctx.AgentVersion = "2.1-" + step12AWSAccess

	got, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	if !strings.Contains(string(got.RawPayload), step12BearerToken) {
		t.Fatalf("RawPayload should retain bounded verbatim stdin only in memory; got %q", string(got.RawPayload))
	}
	public := got
	public.RawPayload = nil
	assertNoStep12Secrets(t, mustJSON(t, public))
}

func TestAgentdRequestRedactsSecretsOutsideRawPayload(t *testing.T) {
	input := mustParseStep12HookInput(t, `{
		"hook_event_name":"PreToolUse",
		"session_id":"sess_synthetic_secret",
		"transcript_path":"/redacted/`+step12SecretPath+`/`+step12BearerToken+`.jsonl",
		"cwd":"/redacted/`+step12SecretPath+`/repo",
		"tool_name":"Bash",
		"tool_use_id":"tu_secret",
		"prompt":"please use `+step12GitHubToken+`",
		"tool_input":{
			"command":"echo Authorization: Bearer `+step12BearerToken+`",
			"env":{"AWS_SECRET_ACCESS_KEY":"`+step12AWSSecret+`"},
			"metadata":{"nested":"prefix_`+step12GitHubToken+`"}
		},
		"tool_response":{"stdout":"`+step12AWSAccess+`","stderr":"`+step12Password+`"}
	}`)
	env := map[string]string{
		"CORDUM_EDGE_SESSION_ID":   "edge_sess_" + step12BearerToken,
		"CORDUM_EDGE_EXECUTION_ID": "edge_exec_" + step12GitHubToken,
		"CORDUM_TENANT_ID":         "tenant-" + step12BearerToken,
		"CORDUM_EDGE_PRINCIPAL_ID": "principal-" + step12GitHubToken,
		"CORDUM_AGENT_PRODUCT":     "claude-code",
		"CORDUM_AGENT_VERSION":     "2.1-" + step12AWSAccess,
	}

	req := agentdRequest(input, []string{"cordum-hook", "claude", "pre-tool-use"}, env)
	if !strings.Contains(string(req.RawPayload), step12BearerToken) {
		t.Fatalf("RawPayload should retain bounded verbatim stdin only for local agentd; got %q", string(req.RawPayload))
	}
	public := req
	public.RawPayload = nil
	assertNoStep12Secrets(t, mustJSON(t, public))
}

func TestMapEdgeDecisionToHookOutputRedactsReasonsContextAndUpdatedInput(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision:          "REQUIRE_APPROVAL",
		Reason:            "blocked Authorization: Bearer " + step12BearerToken,
		ApprovalRef:       "edge_appr_synthetic_123",
		AdditionalContext: "review trace " + step12GitHubToken,
		UpdatedInput: map[string]any{
			"command": "echo " + step12AWSSecret,
			"env":     map[string]any{"AWS_ACCESS_KEY_ID": step12AWSAccess},
		},
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	encoded := marshalOutput(t, out)
	assertNoStep12Secrets(t, encoded)
	if !strings.Contains(encoded, "edge_appr_synthetic_123") {
		t.Fatalf("approval_ref guidance missing after redaction: %s", encoded)
	}
}

func TestMapperFixturesAndEdgeDocsContainOnlySyntheticRedactedExamples(t *testing.T) {
	var paths []string
	for _, pattern := range []string{
		filepath.Join("testdata", "hooks", "*.json"),
		filepath.Join("..", "..", "..", "docs", "edge", "*.md"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no mapper fixtures/docs found for secret-marker scan")
	}

	for _, path := range paths {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			text := string(data)
			for _, re := range []*regexp.Regexp{
				regexp.MustCompile(`Authorization:\s*Bearer\s+\S+`),
				regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`),
				regexp.MustCompile(`ghp_[A-Za-z0-9_]{12,}`),
				regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
				regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
			} {
				if re.MatchString(text) {
					t.Fatalf("committed fixture/doc contains forbidden secret-shaped marker %q", re.String())
				}
			}
		})
	}
}

func mustParseStep12HookInput(t *testing.T, payload string) HookInput {
	t.Helper()
	input, err := parseHookInput([]byte(payload))
	if err != nil {
		t.Fatalf("parse hook input: %v", err)
	}
	input.RawPayload = []byte(payload)
	return input
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func assertNoStep12Secrets(t *testing.T, text string) {
	t.Helper()
	assertNoSyntheticSecrets(t, text)
	// EDGE-046: step12SecretPath ("secret-worktree") used to be in this list
	// because the legacy redactHookBoundaryString (mapper.go:594) wholesale-
	// replaced any string containing the substring "secret". EDGE-046
	// narrowed the guard to actual leak markers only, so a path component
	// named "secret-worktree" now correctly passes through to public agentd
	// request fields like cwd / transcript_path. Real-secret leak detection
	// (bearer tokens, AWS keys, GitHub tokens, password assignments, the
	// literal "Authorization: Bearer" prefix) is unchanged.
	for _, secret := range []string{
		step12BearerToken,
		step12GitHubToken,
		step12AWSAccess,
		step12AWSSecret,
		step12Password,
		"Authorization: Bearer",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("step-12 synthetic secret %q leaked in %q", secret, text)
		}
	}
}

func TestMapHookInputDoesNotMutateCallerMapsWhenRedactingSecrets(t *testing.T) {
	input := HookInput{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolUseID:     "tu_secret_mutation",
		ToolInput: map[string]any{
			"command": "echo Authorization: Bearer " + step12BearerToken,
		},
		RawPayload: []byte(`{"hook_event_name":"PreToolUse"}`),
	}
	ctx := newTestMappingContext()
	ctx.Now = func() time.Time { return time.Date(2026, 5, 2, 12, 30, 0, 0, time.UTC) }
	original := input.ToolInput["command"]

	got, err := MapHookInput(input, ctx)
	if err != nil {
		t.Fatalf("MapHookInput: %v", err)
	}
	assertNoStep12Secrets(t, mustJSON(t, struct {
		InputRedacted map[string]any
		InputHash     string
		ActionHash    string
	}{got.InputRedacted, got.InputHash, got.ActionHash}))
	if input.ToolInput["command"] != original {
		t.Fatalf("MapHookInput mutated caller ToolInput: got %q want %q", input.ToolInput["command"], original)
	}
}
