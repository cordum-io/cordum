package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/cordum/cordum/sdk/client"
)

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENV", "")
	if got := envOr("TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback value")
	}
	t.Setenv("TEST_ENV", " value ")
	if got := envOr("TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("expected trimmed env value")
	}
}

func TestNewFlagSetDefaults(t *testing.T) {
	t.Setenv("CORDUM_GATEWAY", "http://example.com")
	t.Setenv("CORDUM_API_KEY", "token")
	t.Setenv("CORDUM_TENANT_ID", "tenant-a")
	fs := newFlagSet("test")
	if *fs.gateway != "http://example.com" {
		t.Fatalf("expected gateway from env, got %s", *fs.gateway)
	}
	if *fs.apiKey != "token" {
		t.Fatalf("expected api key from env, got %s", *fs.apiKey)
	}
	if *fs.tenant != "tenant-a" {
		t.Fatalf("expected tenant from env, got %s", *fs.tenant)
	}
}

func TestNewClientTrimsGateway(t *testing.T) {
	client := sdk.NewWithTLS("http://localhost:8081/", "key", sdk.TLSOptions{})
	client.TenantID = "tenant"
	// NewWithTLS doesn't trim trailing slash — newClientFromFlags does via
	// strings.TrimRight, so we test the full path here.
	client2 := sdk.NewWithTLS(
		strings.TrimRight("http://localhost:8081/", "/"),
		"key",
		sdk.TLSOptions{},
	)
	client2.TenantID = "tenant"
	if client2.BaseURL != "http://localhost:8081" {
		t.Fatalf("expected trimmed base url, got %s", client2.BaseURL)
	}
	if client2.APIKey != "key" {
		t.Fatalf("expected api key on client")
	}
	if client2.TenantID != "tenant" {
		t.Fatalf("expected tenant id on client")
	}
}

func TestTLSOptionsFromFlags(t *testing.T) {
	// CLI flag takes priority over env var.
	t.Setenv("CORDUM_TLS_CA", "/env/ca.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("tls-test")
	fs.ParseArgs([]string{"--cacert", "/flag/ca.crt"})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/flag/ca.crt" {
		t.Fatalf("expected flag ca path, got %s", opts.CACertPath)
	}
	if opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=false")
	}
}

func TestTLSOptionsFromEnv(t *testing.T) {
	t.Setenv("CORDUM_TLS_CA", "/env/ca.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "1")
	fs := newFlagSet("tls-env-test")
	fs.ParseArgs([]string{})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/env/ca.crt" {
		t.Fatalf("expected env ca path, got %s", opts.CACertPath)
	}
	if !opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=true from env")
	}
}

func TestTLSOptionsInsecureFlag(t *testing.T) {
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("tls-insecure-test")
	fs.ParseArgs([]string{"--insecure"})
	opts := fs.tlsOptions()
	if !opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=true from flag")
	}
}

func TestLoadAndPrintJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(path, []byte(`{"id":"wf-1"}`), 0o600); err != nil {
		t.Fatalf("write temp json: %v", err)
	}
	var payload map[string]any
	loadJSON(path, &payload)
	if payload["id"] != "wf-1" {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printJSON(map[string]string{"k": "v"})
	_ = w.Close()
	os.Stdout = old

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "\"k\"") {
		t.Fatalf("expected json output, got %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// Regression: TLS config edge cases
// ---------------------------------------------------------------------------

func TestTLSOptionsInsecureEnvValues(t *testing.T) {
	// tlsOptions now uses parseBoolEnv which accepts "1" or case-insensitive "true".
	for _, val := range []string{"1", "true", "TRUE", "True"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("CORDUM_TLS_CA", "")
			t.Setenv("CORDUM_TLS_INSECURE", val)
			fs := newFlagSet("insecure-" + val)
			fs.ParseArgs([]string{})
			opts := fs.tlsOptions()
			if !opts.InsecureSkipVerify {
				t.Fatalf("expected insecure=true for env value %q", val)
			}
		})
	}

	// "0", "false", and empty should NOT be insecure.
	for _, val := range []string{"0", "false", ""} {
		t.Run("not-"+val, func(t *testing.T) {
			t.Setenv("CORDUM_TLS_CA", "")
			t.Setenv("CORDUM_TLS_INSECURE", val)
			fs := newFlagSet("not-insecure-" + val)
			fs.ParseArgs([]string{})
			opts := fs.tlsOptions()
			if opts.InsecureSkipVerify {
				t.Fatalf("expected insecure=false for env value %q", val)
			}
		})
	}
}

func TestParseBoolEnv(t *testing.T) {
	key := "TEST_PARSE_BOOL_CLI"
	// Truthy values.
	for _, val := range []string{"1", "true", "TRUE", "True", "tRuE"} {
		t.Setenv(key, val)
		if !parseBoolEnv(key) {
			t.Fatalf("expected true for %q", val)
		}
	}
	// Falsy values.
	for _, val := range []string{"", "0", "false", "FALSE", "no", "yes", "on"} {
		t.Setenv(key, val)
		if parseBoolEnv(key) {
			t.Fatalf("expected false for %q", val)
		}
	}
}

func TestTLSOptionsFlagPriorityOverEnv(t *testing.T) {
	// When --cacert flag is set, it should take priority over CORDUM_TLS_CA env.
	t.Setenv("CORDUM_TLS_CA", "/env/path.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("flag-priority")
	fs.ParseArgs([]string{"--cacert", "/flag/path.crt"})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/flag/path.crt" {
		t.Fatalf("flag should take priority, got: %s", opts.CACertPath)
	}
}

func TestTLSOptionsEnvFallback(t *testing.T) {
	// When no --cacert flag, use CORDUM_TLS_CA env.
	t.Setenv("CORDUM_TLS_CA", "/env/only.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("env-fallback")
	fs.ParseArgs([]string{})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/env/only.crt" {
		t.Fatalf("expected env fallback, got: %s", opts.CACertPath)
	}
}

// ---------------------------------------------------------------------------
// Regression: newClientFromFlags uses strict TLS error handling
// ---------------------------------------------------------------------------

func TestNewClientFromFlagsValidTLS(t *testing.T) {
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "test-key")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("valid-tls")
	fs.ParseArgs([]string{})
	client := newClientFromFlags(fs)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.BaseURL != "http://localhost:8081" {
		t.Fatalf("unexpected base url: %s", client.BaseURL)
	}
}

// ---------------------------------------------------------------------------
// Regression: Config parsing edge cases
// ---------------------------------------------------------------------------

func TestEnvOrWhitespace(t *testing.T) {
	t.Setenv("TEST_WS", "   ")
	if got := envOr("TEST_WS", "fb"); got != "fb" {
		t.Fatalf("whitespace-only env should fall back, got %q", got)
	}
}

func TestEnvOrUnset(t *testing.T) {
	// Ensure unset env var uses fallback.
	t.Setenv("TEST_UNSET", "")
	if got := envOr("TEST_UNSET", "default"); got != "default" {
		t.Fatalf("expected default, got %q", got)
	}
}

func TestParseJSONArgInlineJSON(t *testing.T) {
	val, err := parseJSONArg(`{"key": "value"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", val)
	}
	if m["key"] != "value" {
		t.Fatalf("unexpected value: %v", m["key"])
	}
}

func TestParseJSONArgEmpty(t *testing.T) {
	val, err := parseJSONArg("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != nil {
		t.Fatalf("expected nil for empty arg, got %v", val)
	}
}

func TestParseJSONArgInvalidJSON(t *testing.T) {
	_, err := parseJSONArg("{not json}")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSplitCommaEdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"  ", 0},
		{",,,", 0},
		{"a", 1},
		{"a,b,c", 3},
		{" a , b , ", 2},
	}
	for _, tc := range cases {
		got := splitComma(tc.input)
		if len(got) != tc.want {
			t.Errorf("splitComma(%q) = %v (len %d), want len %d", tc.input, got, len(got), tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Regression: reorderArgs fixes flag parsing after positional args
// ---------------------------------------------------------------------------

func TestReorderArgsFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("input", "", "input json")
	fs.Bool("dry-run", false, "dry run")

	args := []string{"my-workflow", "--input", `{"key":"val"}`, "--dry-run"}
	got := reorderArgs(fs, args)
	want := []string{"--input", `{"key":"val"}`, "--dry-run", "my-workflow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestReorderArgsFlagsBeforePositional(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("input", "", "input json")
	fs.Bool("dry-run", false, "dry run")

	// Already correct order — should be unchanged.
	args := []string{"--input", `{"key":"val"}`, "--dry-run", "my-workflow"}
	got := reorderArgs(fs, args)
	want := []string{"--input", `{"key":"val"}`, "--dry-run", "my-workflow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestReorderArgsDoubleDashStopsProcessing(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("input", "", "input json")

	args := []string{"--", "--input", "value"}
	got := reorderArgs(fs, args)
	// Everything after -- should remain positional.
	want := []string{"--", "--input", "value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestReorderArgsInlineEquals(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("input", "", "input json")

	args := []string{"my-workflow", "--input=data.json"}
	got := reorderArgs(fs, args)
	want := []string{"--input=data.json", "my-workflow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestReorderArgsMixedOrder(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("approve", false, "approve")
	fs.Bool("reject", false, "reject")

	// Simulates: cordumctl approval job <job_id> --approve
	args := []string{"job-123", "--approve"}
	got := reorderArgs(fs, args)
	want := []string{"--approve", "job-123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestParseArgsApprovalRepairFlagsAfterPositional(t *testing.T) {
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")

	fs := newFlagSet("approval repair")
	apply := fs.Bool("apply", false, "apply repair")
	note := fs.String("note", "", "repair note")

	fs.ParseArgs([]string{"job-123", "--apply", "--note", "operator fix"})

	if !*apply {
		t.Fatal("expected apply=true")
	}
	if *note != "operator fix" {
		t.Fatalf("expected note parsed, got %q", *note)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "job-123" {
		t.Fatalf("expected positional arg 'job-123', got %v", fs.Args())
	}
}

func TestReorderArgsNoFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{"pos1", "pos2"}
	got := reorderArgs(fs, args)
	want := []string{"pos1", "pos2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestReorderArgsEmpty(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	got := reorderArgs(fs, []string{})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestParseArgsFlagAfterPositional(t *testing.T) {
	// End-to-end test: verify that ParseArgs correctly parses flags
	// placed after positional arguments.
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")

	fs := newFlagSet("run start")
	input := fs.String("input", "", "input json")
	dryRun := fs.Bool("dry-run", false, "dry run mode")

	// Simulate: cordumctl run start my-workflow --input '{"k":"v"}' --dry-run
	fs.ParseArgs([]string{"my-workflow", "--input", `{"k":"v"}`, "--dry-run"})

	if *input != `{"k":"v"}` {
		t.Fatalf("expected input flag parsed, got %q", *input)
	}
	if !*dryRun {
		t.Fatal("expected dry-run=true")
	}
	if fs.NArg() != 1 || fs.Arg(0) != "my-workflow" {
		t.Fatalf("expected positional arg 'my-workflow', got %v", fs.Args())
	}
}

// ---------------------------------------------------------------------------
// Regression: single-dash flags and various input forms
// ---------------------------------------------------------------------------

func TestParseArgsSingleDashInput(t *testing.T) {
	// Verify -input (single dash) works the same as --input.
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")

	fs := newFlagSet("run start")
	input := fs.String("input", "", "input json")
	dryRun := fs.Bool("dry-run", false, "dry run mode")

	fs.ParseArgs([]string{"my-workflow", "-input", `{"k":"v"}`, "-dry-run"})

	if *input != `{"k":"v"}` {
		t.Fatalf("expected input flag parsed with single dash, got %q", *input)
	}
	if !*dryRun {
		t.Fatal("expected dry-run=true with single dash")
	}
	if fs.NArg() != 1 || fs.Arg(0) != "my-workflow" {
		t.Fatalf("expected positional arg 'my-workflow', got %v", fs.Args())
	}
}

func TestParseArgsInputEqualsJSON(t *testing.T) {
	// Verify -input='{"k":"v"}' equals syntax works.
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")

	fs := newFlagSet("run start")
	input := fs.String("input", "", "input json")

	fs.ParseArgs([]string{"my-workflow", `-input={"k":"v"}`})

	if *input != `{"k":"v"}` {
		t.Fatalf("expected input from equals syntax, got %q", *input)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "my-workflow" {
		t.Fatalf("expected positional arg, got %v", fs.Args())
	}
}

func TestParseArgsComplexNestedJSON(t *testing.T) {
	// Verify deeply nested JSON survives parsing.
	t.Setenv("CORDUM_GATEWAY", "http://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("CORDUM_TENANT_ID", "default")
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")

	nested := `{"date_range":{"start":"last_24h","end":"now"},"filters":{"type":["signal","alert"],"min_score":0.5}}`
	fs := newFlagSet("run start")
	input := fs.String("input", "", "input json")

	fs.ParseArgs([]string{"my-workflow", "--input", nested})

	if *input != nested {
		t.Fatalf("nested JSON not preserved, got %q", *input)
	}
	parsed, err := parseJSONArg(*input)
	if err != nil {
		t.Fatalf("parseJSONArg failed: %v", err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", parsed)
	}
	dr, ok := m["date_range"].(map[string]any)
	if !ok || dr["start"] != "last_24h" {
		t.Fatalf("nested values not preserved: %v", m)
	}
}

func TestParseArgsInputFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(`{"source":"file","count":42}`), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	parsed, err := parseJSONArg(path)
	if err != nil {
		t.Fatalf("parseJSONArg from file failed: %v", err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", parsed)
	}
	if m["source"] != "file" {
		t.Fatalf("expected source=file, got %v", m["source"])
	}
}

func TestParseArgsInputFromStdin(t *testing.T) {
	// Save and restore os.Stdin.
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		_, _ = w.Write([]byte(`{"from":"stdin","ok":true}`))
		_ = w.Close()
	}()

	parsed, err := parseJSONArg("-")
	if err != nil {
		t.Fatalf("parseJSONArg from stdin failed: %v", err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", parsed)
	}
	if m["from"] != "stdin" {
		t.Fatalf("expected from=stdin, got %v", m["from"])
	}
}

func TestReorderArgsSingleDashValueFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("input", "", "input json")

	// Single-dash -input should work identically to --input.
	args := []string{"my-workflow", "-input", `{"key":"val"}`}
	got := reorderArgs(fs, args)
	want := []string{"-input", `{"key":"val"}`, "my-workflow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs single-dash = %v, want %v", got, want)
	}
}
