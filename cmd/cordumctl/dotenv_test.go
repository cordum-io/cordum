package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// roundTripCredential holds what CodeRabbit r4580272526 named ($, both quote
// kinds, # and a line break) and a backslash.
const roundTripCredential = "a$b'c\"d#e\nf\\g"

func writeDotEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadEnvFileParsesLikeCompose pins Compose's .env rules (CodeRabbit
// r4580272519): comments, quotes, escapes, interpolation, last definition wins.
func TestReadEnvFileParsesLikeCompose(t *testing.T) {
	t.Setenv("DOTENV_TEST_FROM_ENV", "env")
	for _, k := range []string{"DOTENV_TEST_FROM_FILE", "DOTENV_TEST_UNSET", "REDIS_PASSWORD"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ name, content, want string }{
		{"inline comment", "CORDUM_API_KEY=secret # local\n", "secret"},
		{"hash inside a value", "CORDUM_API_KEY=sec#ret\n", "sec#ret"},
		{"comment after the closing quote", "CORDUM_API_KEY=\"quoted\" # note\n", "quoted"},
		{"double-quote escapes", `CORDUM_API_KEY="a\"b # c\\d\te"` + "\n", "a\"b # c\\d\te"},
		{"single quotes are literal", `CORDUM_API_KEY='a$b # c\'d\n'` + "\n", "a$b # c'd\\n"},
		{"escaped dollar", "CORDUM_API_KEY=\"a$$b\"\n", "a$b"},
		{"dollar that starts no name", "CORDUM_API_KEY=pa$1word$\n", "pa$1word$"},
		{"interpolation", "DOTENV_TEST_FROM_FILE=file\nCORDUM_API_KEY=${DOTENV_TEST_FROM_FILE}-$DOTENV_TEST_FROM_ENV-${DOTENV_TEST_UNSET:-dflt}\n", "file-env-dflt"},
		{"export prefix", "export CORDUM_API_KEY=exported\n", "exported"},
		{"last definition wins", "CORDUM_API_KEY=old\nCORDUM_API_KEY=new\n", "new"},
		{"unterminated quote", "CORDUM_API_KEY=\"open\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readEnvFile(writeDotEnvFile(t, tc.content), "CORDUM_API_KEY"); got != tc.want {
				t.Errorf("readEnvFile = %q, want %q", got, tc.want)
			}
		})
	}
	// The shipped .env.example parses, and its REDIS_URL takes REDIS_PASSWORD from the file.
	if got := readEnvFile(filepath.Join("..", "..", ".env.example"), "REDIS_URL"); got == "" || strings.Contains(got, "${") {
		t.Errorf(".env.example REDIS_URL = %q, want it parsed and interpolated", got)
	}
}

// TestEnsureDevBootstrapEnvReadsExistingValuesLikeCompose: parseEnvContent uses
// readEnvFile's parser, so up --dev keeps the CORDUM_API_KEY that Compose reads.
func TestEnsureDevBootstrapEnvReadsExistingValuesLikeCompose(t *testing.T) {
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CORDUM_API_KEY=secret # local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "secret" || slices.Contains(res.AddedKeys, "CORDUM_API_KEY") {
		t.Errorf("APIKey = %q, AddedKeys = %v; want %q, kept rather than written again", res.APIKey, res.AddedKeys, "secret")
	}
}

// TestComposeEnvDropsBlankCredentials pins CodeRabbit r4580272522: Compose prefers
// the process environment to .env, so a blank CORDUM_API_KEY or REDIS_PASSWORD
// must not reach it, while other blank variables still do.
func TestComposeEnvDropsBlankCredentials(t *testing.T) {
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("REDIS_PASSWORD", " ")
	t.Setenv("CORDUM_TENANT_ID", "")
	has := func(prefix string) bool {
		return slices.ContainsFunc(composeEnv(), func(kv string) bool { return strings.HasPrefix(kv, prefix) })
	}
	if has("CORDUM_API_KEY=") || has("REDIS_PASSWORD=") || !has("CORDUM_TENANT_ID=") {
		t.Error("composeEnv must drop the blank CORDUM_API_KEY and REDIS_PASSWORD and keep the blank CORDUM_TENANT_ID")
	}
	t.Setenv("CORDUM_API_KEY", "set")
	t.Setenv("REDIS_PASSWORD", "pw")
	if env := composeEnv(); !slices.Contains(env, "CORDUM_API_KEY=set") || !slices.Contains(env, "REDIS_PASSWORD=pw") {
		t.Error("composeEnv dropped a set CORDUM_API_KEY or REDIS_PASSWORD")
	}
}

// TestEnsureDevBootstrapEnvCopiesCredentialThatReadsBackUnchanged pins CodeRabbit
// r4580272526: the API_KEY that up --dev copies to CORDUM_API_KEY is written so
// that Compose and readEnvFile both read it back unchanged.
func TestEnsureDevBootstrapEnvCopiesCredentialThatReadsBackUnchanged(t *testing.T) {
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")
	t.Setenv("b", "INTERPOLATED") // an unescaped $b would read back changed
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	// Compose reads this API_KEY as roundTripCredential.
	if err := os.WriteFile(envPath, []byte(`API_KEY="a$$b'c\"d#e\nf\\g"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := readEnvFile(envPath, "CORDUM_API_KEY"); res.APIKey != roundTripCredential || got != roundTripCredential {
		t.Errorf("APIKey = %q, readEnvFile = %q, want %q", res.APIKey, got, roundTripCredential)
	}
	data, err := os.ReadFile(envPath) // #nosec G304 -- test temp dir.
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CORDUM_API_KEY=") {
			t.Logf("dotenv line: %s", line) // the verify hands this line to docker compose config
		}
	}
}
