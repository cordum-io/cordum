package safetykernel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/policysign"
)

const fileLoaderPolicy = `rules:
  - id: allow-all
    match:
      topics: ['job.*']
    decision: allow
`

// writePolicyFile drops data into a temp file and returns the path.
func writePolicyFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// clearFileLoaderEnv wipes every env var the file loader reads so each
// test sees a clean slate. t.Setenv auto-restores on cleanup.
func clearFileLoaderEnv(t *testing.T) {
	t.Helper()
	t.Setenv(policysign.EnvStrictMode, "")
	t.Setenv(env.EnvProduction, "")
	t.Setenv(env.EnvMode, "")
	t.Setenv(envLegacyRequireSig, "")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", "")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY_ID", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE_PATH", "")
	for _, e := range os.Environ() {
		name := e
		if i := indexByte(e, '='); i >= 0 {
			name = e[:i]
		}
		if hasPrefix(name, policysign.EnvPublicKeyPrefix) {
			t.Setenv(name, "")
		}
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
func hasPrefix(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func TestFileLoader_OffSkipsEvenWithoutKey(t *testing.T) {
	clearFileLoaderEnv(t)
	t.Setenv(policysign.EnvStrictMode, "off")

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), "missing.yaml"); err != nil {
		t.Fatalf("off should skip, got %v", err)
	}
}

func TestFileLoader_WarnNoKeyReturnsNil(t *testing.T) {
	clearFileLoaderEnv(t)
	t.Setenv(policysign.EnvStrictMode, "warn")

	// No trust store keys; warn mode tolerates — no signature to check means
	// the kernel logs and continues rather than refusing to boot.
	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), "nonexistent.yaml"); err != nil {
		t.Fatalf("warn + no key should tolerate, got %v", err)
	}
}

func TestFileLoader_EnforceRejectsWithoutTrustStore(t *testing.T) {
	clearFileLoaderEnv(t)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	err := verifyFilePolicySignature([]byte(fileLoaderPolicy), "nope.yaml")
	if !errors.Is(err, ErrNoTrustStoreConfigured) {
		t.Fatalf("want ErrNoTrustStoreConfigured, got %v", err)
	}
}

func TestFileLoader_StructuredJSONSidecar_Warn_ValidSig(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "warn")
	t.Setenv(policysign.EnvPublicKeyPrefix+"PRIMARY", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	sig, err := policysign.Sign(priv, "PRIMARY", []byte(fileLoaderPolicy))
	if err != nil {
		t.Fatal(err)
	}
	sigJSON, _ := json.Marshal(sig)
	writePolicyFile(t, dir, "safety.yaml.sig", sigJSON)

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath); err != nil {
		t.Fatalf("warn+valid: %v", err)
	}
}

func TestFileLoader_StructuredJSONSidecar_Enforce_ValidSig(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	sig, _ := policysign.Sign(priv, "P1", []byte(fileLoaderPolicy))
	sigJSON, _ := json.Marshal(sig)
	writePolicyFile(t, dir, "safety.yaml.sig", sigJSON)

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath); err != nil {
		t.Fatalf("enforce+valid: %v", err)
	}
}

func TestFileLoader_StructuredJSONSidecar_Enforce_TamperedPolicy(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	// Sign the ORIGINAL policy, then verify against TAMPERED bytes.
	sig, _ := policysign.Sign(priv, "P1", []byte(fileLoaderPolicy))
	sigJSON, _ := json.Marshal(sig)
	writePolicyFile(t, dir, "safety.yaml.sig", sigJSON)

	err := verifyFilePolicySignature([]byte(fileLoaderPolicy+"# tampered\n"), policyPath)
	if err == nil {
		t.Fatal("enforce should reject tampered policy")
	}
}

func TestFileLoader_StructuredJSONSidecar_Enforce_UntrustedKey(t *testing.T) {
	clearFileLoaderEnv(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	trustedPub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"TRUSTED", base64.StdEncoding.EncodeToString(trustedPub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	sig, _ := policysign.Sign(priv, "ROGUE", []byte(fileLoaderPolicy))
	sigJSON, _ := json.Marshal(sig)
	writePolicyFile(t, dir, "safety.yaml.sig", sigJSON)

	err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath)
	if !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("want ErrUntrustedKeyID, got %v", err)
	}
}

func TestFileLoader_LegacyRawBytes_Warn_ValidSig(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "warn")
	t.Setenv(policysign.EnvPublicKeyPrefix+"LEGACY", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	// Legacy: just the raw 64-byte sig, base64-encoded.
	rawSig := ed25519.Sign(priv, []byte(fileLoaderPolicy))
	writePolicyFile(t, dir, "safety.yaml.sig", []byte(base64.StdEncoding.EncodeToString(rawSig)))

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath); err != nil {
		t.Fatalf("legacy raw sig should verify under warn, got %v", err)
	}
}

func TestFileLoader_LegacyRawBytes_Enforce_ValidSig(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"LEGACY", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	rawSig := ed25519.Sign(priv, []byte(fileLoaderPolicy))
	writePolicyFile(t, dir, "safety.yaml.sig", []byte(base64.StdEncoding.EncodeToString(rawSig)))

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath); err != nil {
		t.Fatalf("legacy raw sig should verify under enforce, got %v", err)
	}
}

func TestFileLoader_LegacyRawBytes_Enforce_WrongKey(t *testing.T) {
	clearFileLoaderEnv(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	trustedPub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"T1", base64.StdEncoding.EncodeToString(trustedPub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	rawSig := ed25519.Sign(priv, []byte(fileLoaderPolicy))
	writePolicyFile(t, dir, "safety.yaml.sig", []byte(base64.StdEncoding.EncodeToString(rawSig)))

	err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath)
	if err == nil {
		t.Fatal("enforce should reject legacy sig from untrusted key")
	}
}

func TestFileLoader_EnforceRejectsMissingSig(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "safety.yaml", []byte(fileLoaderPolicy))
	// No sidecar.

	err := verifyFilePolicySignature([]byte(fileLoaderPolicy), policyPath)
	if !errors.Is(err, ErrBundleUnsigned) {
		t.Fatalf("want ErrBundleUnsigned, got %v", err)
	}
}

func TestFileLoader_EnvSignatureOverride(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))

	sig, _ := policysign.Sign(priv, "P1", []byte(fileLoaderPolicy))
	sigJSON, _ := json.Marshal(sig)
	t.Setenv("SAFETY_POLICY_SIGNATURE", string(sigJSON))

	if err := verifyFilePolicySignature([]byte(fileLoaderPolicy), ""); err != nil {
		t.Fatalf("env-provided sig should verify, got %v", err)
	}
}

func TestResolveFileLoaderMode(t *testing.T) {
	clearFileLoaderEnv(t)
	if got := resolveFileLoaderMode(); got != policysign.ModeWarn {
		t.Errorf("default = %v want warn", got)
	}
	t.Setenv(envLegacyRequireSig, "true")
	if got := resolveFileLoaderMode(); got != policysign.ModeEnforce {
		t.Errorf("legacy required = %v want enforce", got)
	}
	t.Setenv(envLegacyRequireSig, "")
	t.Setenv(policysign.EnvStrictMode, "off")
	if got := resolveFileLoaderMode(); got != policysign.ModeOff {
		t.Errorf("explicit off wins, got %v", got)
	}
}

// BUG-001: an unrecognized CORDUM_POLICY_STRICT used to silently downgrade to
// ModeWarn even in production. After the fix, production fails closed
// (ModeEnforce) while non-production keeps the legacy lenient fallback.
func TestResolveFileLoaderMode_InvalidValue_ProdFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		strict string
	}{
		{"typo", "enforse"},
		{"mixed_case_punctuation", "Enforce!"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"_production_enforces", func(t *testing.T) {
			clearFileLoaderEnv(t)
			t.Setenv(policysign.EnvStrictMode, tc.strict)
			t.Setenv(env.EnvProduction, "true")
			t.Setenv(env.EnvMode, "")

			if got := resolveFileLoaderMode(); got != policysign.ModeEnforce {
				t.Fatalf("invalid strict %q in production: got %v, want ModeEnforce", tc.strict, got)
			}
		})
		t.Run(tc.name+"_non_production_warns", func(t *testing.T) {
			clearFileLoaderEnv(t)
			t.Setenv(policysign.EnvStrictMode, tc.strict)
			t.Setenv(env.EnvProduction, "")
			t.Setenv(env.EnvMode, "")

			if got := resolveFileLoaderMode(); got != policysign.ModeWarn {
				t.Fatalf("invalid strict %q outside production: got %v, want ModeWarn", tc.strict, got)
			}
		})
	}
}
