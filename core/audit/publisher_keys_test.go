package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/policysign"
)

func clearPublisherEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"CORDUM_AUDIT_SIGNING_KEY", "CORDUM_AUDIT_SIGNING_KEY_PATH",
		"CORDUM_AUDIT_SIGNING_KEY_ID", "CORDUM_AUDIT_DEV_SIGNING_SEED",
		"CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP", "CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL",
	} {
		t.Setenv(name, "")
	}
}

func publisherTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publisherKeyFile(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "publisher.key")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPublisherKeySources(t *testing.T) {
	first, second := publisherTestKey(t), publisherTestKey(t)
	inline := base64.StdEncoding.EncodeToString(first)
	path := publisherKeyFile(t, base64.StdEncoding.EncodeToString(second))
	for _, tc := range []struct {
		name, raw, path, seed, kid, wantID string
		want                               ed25519.PrivateKey
	}{
		{"inline", " " + inline + " ", "", "", " custom ", "custom", first},
		{"default id", inline, "", "", " ", "default", first},
		{"path", "", " " + path + " ", "", "", "default", second},
		{"inline precedence", inline, path, "seed", "", "default", first},
		{"path precedence", "", path, "seed", "", "default", second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPublisherEnv(t)
			t.Setenv("CORDUM_AUDIT_SIGNING_KEY", tc.raw)
			t.Setenv("CORDUM_AUDIT_SIGNING_KEY_PATH", tc.path)
			t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", tc.seed)
			t.Setenv("CORDUM_AUDIT_SIGNING_KEY_ID", tc.kid)
			key, id, err := LoadPublisherKeyFromEnv(servicetoken.IdentityMCP)
			if err != nil || !key.Equal(tc.want) || id != tc.wantID {
				t.Fatalf("key equality=%v, id=%q, err=%v", key.Equal(tc.want), id, err)
			}
		})
	}
}

func TestPublisherDerivedKeys(t *testing.T) {
	clearPublisherEnv(t)
	t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", " shared development seed ")
	trust, err := LoadPublisherTrustFromEnv()
	if err != nil || len(trust) != 2 {
		t.Fatalf("trust count=%d, err=%v", len(trust), err)
	}
	var previous ed25519.PrivateKey
	for _, identity := range []string{servicetoken.IdentityMCP, servicetoken.IdentitySafetyKernel} {
		key, id, err := LoadPublisherKeyFromEnv(identity)
		if err != nil || len(key) != ed25519.PrivateKeySize || id != "default" {
			t.Fatalf("derived key length=%d, id=%q, err=%v", len(key), id, err)
		}
		if !key.Public().(ed25519.PublicKey).Equal(trust[identity]) {
			t.Fatal("derived signing key and trust entry differ")
		}
		if previous != nil && key.Equal(previous) {
			t.Fatal("publisher identities share a derived key")
		}
		previous = key
	}
}

func TestPublisherUnconfigured(t *testing.T) {
	clearPublisherEnv(t)
	key, id, err := LoadPublisherKeyFromEnv(servicetoken.IdentityMCP)
	if !errors.Is(err, ErrPublisherKeyNotConfigured) || key != nil || id != "" {
		t.Fatalf("unconfigured key length=%d, id=%q, err=%v", len(key), id, err)
	}
	trust, err := LoadPublisherTrustFromEnv()
	if err != nil || trust == nil || len(trust) != 0 {
		t.Fatalf("unconfigured trust count=%d, err=%v", len(trust), err)
	}
}

func TestPublisherInvalidIdentity(t *testing.T) {
	clearPublisherEnv(t)
	t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", "seed")
	for _, identity := range []string{"", "worker-1", servicetoken.IdentityScheduler} {
		key, id, err := LoadPublisherKeyFromEnv(identity)
		if err == nil || errors.Is(err, ErrPublisherKeyNotConfigured) || key != nil || id != "" {
			t.Fatalf("unsupported identity %q: key length=%d, id=%q, err=%v", identity, len(key), id, err)
		}
	}
}

func TestPublisherMalformedKeys(t *testing.T) {
	const bad = "invalid-key-material!"
	for _, name := range []string{
		"CORDUM_AUDIT_SIGNING_KEY", "CORDUM_AUDIT_SIGNING_KEY_PATH",
		"CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP", "CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL",
	} {
		t.Run(name, func(t *testing.T) {
			clearPublisherEnv(t)
			t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", "valid fallback")
			value := bad
			if name == "CORDUM_AUDIT_SIGNING_KEY_PATH" {
				value = publisherKeyFile(t, bad)
			}
			t.Setenv(name, value)
			var err error
			if strings.Contains(name, "PUBLIC_KEY") {
				var trust map[string]ed25519.PublicKey
				trust, err = LoadPublisherTrustFromEnv()
				if trust != nil {
					t.Fatal("malformed public key returned partial trust")
				}
			} else {
				var key ed25519.PrivateKey
				var id string
				key, id, err = LoadPublisherKeyFromEnv(servicetoken.IdentityMCP)
				if key != nil || id != "" {
					t.Fatal("malformed signing key returned credentials")
				}
			}
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), bad) {
				t.Fatalf("expected a variable-named, material-free error: %v", err)
			}
		})
	}
}

func TestPublisherMissingKeyFile(t *testing.T) {
	clearPublisherEnv(t)
	t.Setenv("CORDUM_AUDIT_SIGNING_KEY_PATH", filepath.Join(t.TempDir(), "missing.key"))
	t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", "valid fallback")
	key, id, err := LoadPublisherKeyFromEnv(servicetoken.IdentityMCP)
	if key != nil || id != "" || !errors.Is(err, os.ErrNotExist) ||
		!strings.Contains(err.Error(), "CORDUM_AUDIT_SIGNING_KEY_PATH") {
		t.Fatalf("missing file: key length=%d, id=%q, err=%v", len(key), id, err)
	}
}

func TestPublisherExplicitTrust(t *testing.T) {
	public := publisherTestKey(t).Public().(ed25519.PublicKey)
	for _, seed := range []string{"", "seed"} {
		t.Run("seed="+seed, func(t *testing.T) {
			clearPublisherEnv(t)
			t.Setenv("CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP", " "+base64.StdEncoding.EncodeToString(public)+" ")
			t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", seed)
			trust, err := LoadPublisherTrustFromEnv()
			if err != nil || !public.Equal(trust[servicetoken.IdentityMCP]) {
				t.Fatalf("explicit trust mismatch: %v", err)
			}
			_, hasKernel := trust[servicetoken.IdentitySafetyKernel]
			if hasKernel != (seed != "") {
				t.Fatal("identity without explicit key must follow seed availability")
			}
		})
	}
}

func clearPublisherPolicyEnv(t *testing.T) {
	t.Helper()
	clearPublisherEnv(t)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CORDUM_POLICY_") || strings.HasPrefix(name, "SAFETY_POLICY_") {
			t.Setenv(name, "")
		}
	}
}

func TestPublisherControlPlaneIsolation(t *testing.T) {
	clearPublisherPolicyEnv(t)
	signing, mcp, kernel, policy := publisherTestKey(t), publisherTestKey(t), publisherTestKey(t), publisherTestKey(t)
	raw := base64.StdEncoding.EncodeToString(signing)
	t.Setenv("CORDUM_AUDIT_SIGNING_KEY", raw)
	t.Setenv("CORDUM_AUDIT_SIGNING_KEY_PATH", publisherKeyFile(t, raw))
	t.Setenv("CORDUM_AUDIT_SIGNING_KEY_ID", "audit")
	t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", "seed")
	t.Setenv("CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP", base64.StdEncoding.EncodeToString(mcp.Public().(ed25519.PublicKey)))
	t.Setenv("CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL", base64.StdEncoding.EncodeToString(kernel.Public().(ed25519.PublicKey)))
	t.Setenv("CORDUM_POLICY_PUBLIC_KEY_PRIMARY", base64.StdEncoding.EncodeToString(policy.Public().(ed25519.PublicKey)))
	trust, err := policysign.LoadTrustStoreFromEnv()
	if err != nil || trust.Len() != 1 {
		t.Fatalf("control-plane trust count=%d, err=%v", trust.Len(), err)
	}
	public, ok := trust.Lookup("primary")
	if !ok || !public.Equal(policy.Public()) {
		t.Fatal("control-plane trust did not retain only its configured key")
	}
	for _, key := range []ed25519.PrivateKey{signing, mcp, kernel} {
		if public.Equal(key.Public()) {
			t.Fatal("audit key entered the control-plane trust store")
		}
	}
}

func TestPublisherSharedSeedIsolation(t *testing.T) {
	clearPublisherPolicyEnv(t)
	t.Setenv("CORDUM_AUDIT_DEV_SIGNING_SEED", "same seed")
	t.Setenv("CORDUM_POLICY_DEV_SIGNING_SEED", "same seed")
	policy, err := policysign.LoadTrustStoreFromEnv()
	if err != nil || policy.Len() != 1 {
		t.Fatalf("control-plane seed trust count=%d, err=%v", policy.Len(), err)
	}
	public, ok := policy.Lookup("default")
	if !ok {
		t.Fatal("control-plane dev key missing")
	}
	trust, err := LoadPublisherTrustFromEnv()
	if err != nil || len(trust) != 2 {
		t.Fatalf("publisher seed trust count=%d, err=%v", len(trust), err)
	}
	for _, key := range trust {
		if public.Equal(key) {
			t.Fatal("audit seed derived a control-plane key")
		}
	}
}
