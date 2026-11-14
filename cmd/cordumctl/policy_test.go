package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/policysign"
	"github.com/stretchr/testify/require"
)

// buildPolicyCLI compiles cordumctl into a temp binary and returns the
// path. The tests exec this binary so they can observe real exit codes
// (which `runPolicy*Cmd` produces via os.Exit).
func buildPolicyCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cordumctl.exe")
	if runtime.GOOS != "windows" {
		bin = strings.TrimSuffix(bin, ".exe")
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build cordumctl: %v", err)
	}
	return bin
}

// runCLI invokes the compiled binary and returns the exit code along
// with stderr so assertions can include the error text when debugging.
func runCLI(t *testing.T, bin string, env []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run cordumctl: %v", err)
		}
	}
	return code, stdoutBuf.String(), stderrBuf.String()
}

func generateKeyEnvs(t *testing.T) (privEnvVal, pubEnvVal string, pub ed25519.PublicKey) {
	t.Helper()
	p, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(p), p
}

func TestPolicySignVerify_RoundTrip(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, pubB64, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	policyContent := "rules:\n  - id: r\n    decision: allow\n"
	if err := os.WriteFile(policyPath, []byte(policyContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Sign
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=test-key",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatalf("sign failed: code=%d stderr=%s", code, stderr)
	}

	// Inspect signature file
	sigBytes, err := os.ReadFile(policyPath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	var sig policysign.Signature
	if err := json.Unmarshal(sigBytes, &sig); err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if sig.KeyID != "test-key" {
		t.Errorf("key_id = %q want test-key", sig.KeyID)
	}
	if sig.Algorithm != policysign.AlgorithmEd25519 {
		t.Errorf("algorithm = %q", sig.Algorithm)
	}

	// Verify
	code, stdout, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "TEST-KEY=" + pubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 0 {
		t.Fatalf("verify failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "verified") {
		t.Errorf("expected 'verified' in output, got %q", stdout)
	}
}

func TestPolicyVerify_TamperedPolicy(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, pubB64, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	// Tamper with the policy.
	if err := os.WriteFile(policyPath, []byte("rules: [tampered]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "K1=" + pubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 on tamper, got %d stderr=%s", code, stderr)
	}
}

func TestPolicyVerify_WrongPublicKey(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, _, _ := generateKeyEnvs(t)
	// Different public key — NOT the one that signed.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	otherPubB64 := base64.StdEncoding.EncodeToString(otherPub)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "K1=" + otherPubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 with wrong key, got %d stderr=%s", code, stderr)
	}
}

func TestPolicyVerify_MissingTrustedKey(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, _, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	// No trust store configured at all.
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "OTHER=",
	}, "policy", "verify", "--in", policyPath)
	if code != 2 {
		t.Fatalf("want exit 2 with no trust store, got %d stderr=%s", code, stderr)
	}
}

func TestPolicySign_NoPrivateKey(t *testing.T) {
	bin := buildPolicyCLI(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=",
	}, "policy", "sign", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 without key, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "not set") {
		t.Errorf("expected 'not set' error, got %q", stderr)
	}
}

type policyStoreTestGateway struct {
	mu       sync.Mutex
	docs     map[string]configDoc
	requests []string
	refusal  int
	missing  int
	body     string
}

func (g *policyStoreTestGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r.Method+" "+r.URL.EscapedPath())
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/config" {
		if doc, ok := g.docs[r.URL.Query().Get("scope_id")]; ok {
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
		// A missing document gets g.missing and g.body: by default the gateway's 404 CONFIG_NOT_FOUND.
		w.WriteHeader(g.missing)
		_, _ = w.Write([]byte(g.body))
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resign") {
		if g.refusal != 0 {
			w.WriteHeader(g.refusal)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "POLICY_SIGNING_REQUIRED", "error": "no trusted signing key"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"key_id": "store-test"})
		return
	}
	// Make an unexpected write visible in both the request and document oracles.
	g.docs["unexpected-write"] = configDoc{ScopeID: r.URL.Path}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (g *policyStoreTestGateway) snapshot(t *testing.T) ([]string, []byte) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	docs, err := json.Marshal(g.docs)
	require.NoError(t, err)
	return append([]string{}, g.requests...), docs
}

func policyStoreTestSetup(t *testing.T, bundles map[string]any) (*policyStoreTestGateway, string, []string) {
	t.Helper()
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		if strings.HasPrefix(key, policysign.EnvPublicKeyPrefix) || strings.HasPrefix(key, "CORDUM_TLS_") || key == "SAFETY_POLICY_PUBLIC_KEY" {
			t.Setenv(key, "")
		}
	}
	g := &policyStoreTestGateway{missing: http.StatusNotFound, body: `{"code":"CONFIG_NOT_FOUND","error":"config not found","status":404}`, docs: map[string]configDoc{
		"policy": {Data: map[string]any{"bundles": bundles}},
		"packs": {Data: map[string]any{"installed": map[string]any{"demo": map[string]any{
			"overlays": map[string]any{"policy": []any{map[string]any{"fragment_id": "demo/default"}}},
		}}}},
	}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, buildPolicyCLI(t), []string{"CORDUM_GATEWAY=" + srv.URL, "CORDUM_API_KEY=store-test-key", "CORDUM_TENANT_ID=default"}
}

func policyStoreTestBundle(t *testing.T, content, pubEnv string) map[string]any {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv(pubEnv, base64.StdEncoding.EncodeToString(pub))
	sig, err := policysign.Sign(priv, "store-test", []byte(content))
	require.NoError(t, err)
	data, err := json.Marshal(sig)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	return map[string]any{"content": content, "_signature": fields}
}

func TestPolicyVerifyStore_ListsEveryBundleTheEnforcedStoreRefuses(t *testing.T) {
	bundles := map[string]any{}
	g, bin, env := policyStoreTestSetup(t, bundles)
	pubEnv := policysign.EnvPublicKeyPrefix + "STORE-TEST"
	valid := policyStoreTestBundle(t, "rules: []\n", pubEnv)
	bundles["valid"], bundles["unsigned"] = valid, "rules: []\n"
	bundles["disabled"] = map[string]any{"content": "rules: []\n", "enabled": false}
	bundles["empty"] = map[string]any{"content": "", "policy": "rules: []\n"}
	bundles["demo/default"] = "rules: []\n"
	for _, id := range []string{"unknown", "bad", "stale", "malformed", "numeric", "alternate"} {
		data, err := json.Marshal(valid)
		require.NoError(t, err)
		var bundle map[string]any
		require.NoError(t, json.Unmarshal(data, &bundle))
		fields := bundle["_signature"].(map[string]any)
		switch id {
		case "unknown":
			fields["key_id"] = "other"
		case "bad":
			fields["value"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		case "stale":
			bundle["content"] = "rules: [changed]\n"
		case "malformed":
			fields["algorithm"] = "rsa"
		case "numeric":
			fields["value"] = 7
			fields["key_id"] = "other"
		case "alternate":
			bundle["content"], bundle["policy"] = 7, valid["content"]
		}
		bundles[id] = bundle
	}
	_, before := g.snapshot(t)
	code, out, stderr := runCLI(t, bin, env, "policy", "verify-store")
	require.Equal(t, 1, code, stderr)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Equal(t, []string{"REFUSED bad bad-signature remedy=resign", "REFUSED demo/default unsigned remedy=reinstall-pack:demo", "REFUSED malformed malformed remedy=resign", "REFUSED numeric malformed remedy=resign", "REFUSED stale stale-signature remedy=resign", "REFUSED unknown untrusted-key remedy=resign", "REFUSED unsigned unsigned remedy=resign", "7 of 9 enabled policy bundles would be refused by the enforced store source"}, lines)
	requests, after := g.snapshot(t)
	require.Equal(t, []string{"GET /api/v1/config", "GET /api/v1/config"}, requests)
	require.Equal(t, before, after)
}

func TestPolicyVerifyStore_CleanStoreExitsZero(t *testing.T) {
	bundles := map[string]any{}
	g, bin, env := policyStoreTestSetup(t, bundles)
	bundles["valid"] = policyStoreTestBundle(t, "rules: []\n", policysign.EnvPublicKeyPrefix+"STORE-TEST")
	code, out, stderr := runCLI(t, bin, env, "policy", "verify-store")
	require.Equal(t, 0, code, stderr)
	require.Equal(t, "all 1 enabled policy bundles verify\n", out)
	t.Run("no-trust", func(t *testing.T) {
		before, docs := g.snapshot(t)
		code, _, stderr := runCLI(t, bin, append(env, policysign.EnvPublicKeyPrefix+"STORE-TEST="), "policy", "verify-store")
		require.Equal(t, 2, code)
		require.Contains(t, stderr, "no trusted policy keys")
		after, final := g.snapshot(t)
		require.Equal(t, before, after)
		require.Equal(t, docs, final)
	})
	t.Run("missing-policy-document", func(t *testing.T) {
		// Only the gateway's 404 CONFIG_NOT_FOUND means zero bundles; a 404 from another route or any other read error stays a configuration error.
		delete(g.docs, "policy")
		for _, tc := range []struct {
			status, exit, gets int
			body, out          string
		}{{http.StatusNotFound, 0, 2, g.body, "all 0 enabled policy bundles verify\n"}, {http.StatusNotFound, 2, 1, "404 page not found", ""}, {http.StatusInternalServerError, 2, 1, `{"error":"internal error"}`, ""}} {
			g.missing, g.body = tc.status, tc.body
			before, docs := g.snapshot(t)
			code, out, stderr := runCLI(t, bin, env, "policy", "verify-store")
			require.Equal(t, tc.exit, code, stderr)
			require.Equal(t, tc.out, out)
			after, final := g.snapshot(t)
			require.Equal(t, []string{"GET /api/v1/config", "GET /api/v1/config"}[:tc.gets], after[len(before):])
			require.Equal(t, docs, final)
		}
	})
}

func TestPolicyResign_SignsOnlyNamedBundles(t *testing.T) {
	for _, selection := range [][]string{{"--bundle", "secops/a", "--bundle", "velocity/b"}, {"--bundle", "secops/a,velocity/b"}, {"--bundle", "legacy/space %"}} {
		t.Run(strings.Join(selection, " "), func(t *testing.T) {
			g, bin, env := policyStoreTestSetup(t, nil)
			code, out, stderr := runCLI(t, bin, env, append([]string{"policy", "resign"}, selection...)...)
			require.Equal(t, 0, code, stderr)
			want := []string{"POST /api/v1/policy/bundles/secops~a/resign", "POST /api/v1/policy/bundles/velocity~b/resign"}
			if strings.Contains(selection[1], "space") {
				want = []string{"POST /api/v1/policy/bundles/legacy~space%20%25/resign"}
			}
			requests, _ := g.snapshot(t)
			require.Equal(t, want, requests)
			require.Equal(t, len(want), strings.Count(out, " key_id=store-test\n"))
		})
	}
}

func TestPolicyResign_AllListedSkipsPackOwned(t *testing.T) {
	bundles := map[string]any{"b": "rules: []\n", "a": "rules: []\n", "demo/default": "rules: []\n"}
	g, bin, env := policyStoreTestSetup(t, bundles)
	bundles["valid"] = policyStoreTestBundle(t, "rules: []\n", policysign.EnvPublicKeyPrefix+"STORE-TEST")
	code, out, stderr := runCLI(t, bin, env, "policy", "resign", "--all")
	require.Equal(t, 1, code, stderr)
	require.Contains(t, out, "SKIPPED demo/default reinstall-pack:demo")
	requests, _ := g.snapshot(t)
	require.Equal(t, []string{"GET /api/v1/config", "GET /api/v1/config", "POST /api/v1/policy/bundles/a/resign", "POST /api/v1/policy/bundles/b/resign"}, requests)
}

func TestPolicyResign_StopsAtFirstRefusal(t *testing.T) {
	g, bin, env := policyStoreTestSetup(t, nil)
	g.refusal = http.StatusServiceUnavailable
	code, _, stderr := runCLI(t, bin, env, "policy", "resign", "--bundle", "a,b")
	require.Equal(t, 1, code)
	require.Contains(t, stderr, "POLICY_SIGNING_REQUIRED")
	requests, _ := g.snapshot(t)
	require.Equal(t, []string{"POST /api/v1/policy/bundles/a/resign"}, requests)
	for _, flags := range [][]string{nil, {"--bundle", ""}, {"--all", "--bundle", "a"}, {"--all"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			before, docs := g.snapshot(t)
			code, _, _ := runCLI(t, bin, env, append([]string{"policy", "resign"}, flags...)...)
			require.Equal(t, 2, code)
			after, final := g.snapshot(t)
			require.Equal(t, before, after)
			require.Equal(t, docs, final)
		})
	}
}

func TestPolicyVerifyStore_ConfigurationErrorsWriteNothing(t *testing.T) {
	g, bin, env := policyStoreTestSetup(t, nil)
	policyStoreTestBundle(t, "rules: []\n", policysign.EnvPublicKeyPrefix+"STORE-TEST")
	for _, command := range [][]string{
		{"verify-store", "--cacert", filepath.Join(t.TempDir(), "missing.pem")},
		{"resign", "--bundle", "a", "--cacert", filepath.Join(t.TempDir(), "missing.pem")},
		{"verify-store", "--gateway", "http://127.0.0.1:0"},
	} {
		before, docs := g.snapshot(t)
		code, _, _ := runCLI(t, bin, env, append([]string{"policy"}, command...)...)
		require.Equal(t, 2, code, command)
		after, final := g.snapshot(t)
		require.Equal(t, before, after)
		require.Equal(t, docs, final)
	}
	for _, id := range []string{"policy", "packs"} {
		g.docs[id] = configDoc{Data: map[string]any{"bundles": true, "installed": true}}
		_, before := g.snapshot(t)
		code, _, _ := runCLI(t, bin, env, "policy", "verify-store")
		require.Equal(t, 2, code)
		_, after := g.snapshot(t)
		require.Equal(t, before, after)
		g.docs[id] = configDoc{Data: map[string]any{}}
	}
}
