package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/packs/signing"
	"gopkg.in/yaml.v3"
)

// writeTestPubKey generates a random Ed25519 keypair and writes its
// public half to dir/<kid>.pub in the YAML format the loader expects.
// Returns the kid and the raw public bytes so the test can also match
// what the loader should register.
func writeTestPubKey(t *testing.T, dir, kid string) (string, ed25519.PublicKey) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	record := packPublicKeyRecord{
		KeyID:        kid,
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
	}
	body, err := yaml.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, kid+".pub")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return kid, pub
}

func TestLoadPackTrustStore_FromDirectory(t *testing.T) {
	dir := t.TempDir()
	kidA, pubA := writeTestPubKey(t, dir, "alpha")
	kidB, pubB := writeTestPubKey(t, dir, "beta")

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  dir,
		HomeDirOverride: t.TempDir(), // isolate from real ~/.cordum
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := store.Keys[kidA]; !ed25519.PublicKey(pubA).Equal(got) {
		t.Errorf("kid %s missing or wrong", kidA)
	}
	if got := store.Keys[kidB]; !ed25519.PublicKey(pubB).Equal(got) {
		t.Errorf("kid %s missing or wrong", kidB)
	}
	if store.Publishers[kidA].PublisherID != "alpha" {
		t.Errorf("publisher metadata missing for %s", kidA)
	}
}

func TestLoadPackTrustStore_FromEnv(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(k string) string { return "" },
		EnvList: func() []string {
			return []string{"CORDUM_PACK_TRUSTED_KEY_ACME=" + encoded}
		},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, ok := store.Keys["acme"]; !ok || !ed25519.PublicKey(pub).Equal(got) {
		t.Errorf("env-loaded key not registered under kid 'acme'")
	}
	if store.Publishers["acme"].PublisherID != "env:acme" {
		t.Errorf("env loader should tag PublisherID env:acme, got %q", store.Publishers["acme"].PublisherID)
	}
}

func TestLoadPackTrustStore_DirOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	_, dirPub := writeTestPubKey(t, dir, "acme")

	// Env offers a DIFFERENT public key under the same kid. The
	// directory must win per the first-loader-wins rule.
	envPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(envPub)

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  dir,
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList: func() []string {
			return []string{"CORDUM_PACK_TRUSTED_KEY_ACME=" + encoded}
		},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := store.Keys["acme"]
	if !ed25519.PublicKey(dirPub).Equal(got) {
		t.Errorf("directory key must win over env key")
	}
}

func TestLoadPackTrustStore_CorruptKeyFile(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "corrupt.pub")
	if err := os.WriteFile(badPath, []byte("not-yaml::"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  dir,
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err == nil {
		t.Fatalf("expected error on corrupt key file")
	}
	if !errors.Is(err, signing.ErrInvalidKey) && !strings.Contains(err.Error(), "corrupt.pub") {
		// Malformed YAML that happens to parse to an empty record
		// surfaces as "public key must be a N-byte base64 blob", which
		// we wrap under signing.ErrInvalidKey. Other YAML errors come
		// through unwrapped but still reference the file path.
		t.Errorf("want ErrInvalidKey or path reference, got %v", err)
	}
}

func TestLoadPackTrustStore_EmptyKeyringInStrictMode(t *testing.T) {
	// Empty dir, no env, no home, and we'll blank the embedded key to
	// simulate the OSS distribution default.
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = nil
	defer func() { embeddedCordumCounterSigningKey = saved }()

	_, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  "",
		HomeDirOverride: t.TempDir(),
		Strict:          true,
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if !errors.Is(err, ErrEmptyKeyringInStrictMode) {
		t.Fatalf("want ErrEmptyKeyringInStrictMode, got %v", err)
	}
}

func TestLoadPackTrustStore_EmptyKeyringInNonStrictMode(t *testing.T) {
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = nil
	defer func() { embeddedCordumCounterSigningKey = saved }()

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: t.TempDir(),
		Strict:          false,
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(store.Keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(store.Keys))
	}
}

func TestLoadPackTrustStore_SizeCapEnforced(t *testing.T) {
	dir := t.TempDir()
	// Write packTrustStoreMaxKeys + 1 keys to provoke the cap.
	for i := 0; i < packTrustStoreMaxKeys+1; i++ {
		writeTestPubKey(t, dir, fmt.Sprintf("cap-%04d", i))
	}
	_, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  dir,
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if !errors.Is(err, ErrTrustStoreFull) {
		t.Fatalf("want ErrTrustStoreFull, got %v", err)
	}
}

func TestLoadPackTrustStore_PermissionsCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions check not meaningful on Windows")
	}
	dir := t.TempDir()
	kid, _ := writeTestPubKey(t, dir, "acme")
	// Relax permissions so the check must trip.
	if err := os.Chmod(filepath.Join(dir, kid+".pub"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir:  dir,
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if !errors.Is(err, ErrTrustedKeyPermissions) {
		t.Fatalf("want ErrTrustedKeyPermissions, got %v", err)
	}
}

func TestLoadPackTrustStore_EmbeddedCordumKey(t *testing.T) {
	// Inject a synthetic embedded key so we don't depend on what the
	// ship build happens to carry.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	record := packPublicKeyRecord{
		KeyID:        "cordum-test",
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
	}
	body, err := yaml.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = body
	defer func() { embeddedCordumCounterSigningKey = saved }()

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !store.HasCordumCounterSigningKey() {
		t.Fatalf("embedded Cordum key not registered")
	}
	if store.CordumCounterSigningKID != "cordum-test" {
		t.Errorf("CordumCounterSigningKID=%q, want cordum-test", store.CordumCounterSigningKID)
	}
	if got := store.Keys["cordum-test"]; !ed25519.PublicKey(pub).Equal(got) {
		t.Errorf("embedded key not in keyring under its kid")
	}
}

func TestLoadPackTrustStore_DefaultDirLookup(t *testing.T) {
	home := t.TempDir()
	trustedDir := filepath.Join(home, ".cordum", "trusted-keys")
	if err := os.MkdirAll(trustedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	kid, pub := writeTestPubKey(t, trustedDir, "home-default")

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: home,
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, ok := store.Keys[kid]; !ok || !ed25519.PublicKey(pub).Equal(got) {
		t.Errorf("default-dir key not loaded")
	}
}

func TestLoadPackTrustStore_EnvDirOverridesHomeDefault(t *testing.T) {
	home := t.TempDir()
	trustedDir := filepath.Join(home, ".cordum", "trusted-keys")
	if err := os.MkdirAll(trustedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Home default has one key.
	writeTestPubKey(t, trustedDir, "home-key")

	// Env dir has a different key.
	envDir := t.TempDir()
	envKid, _ := writeTestPubKey(t, envDir, "env-dir-key")

	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: home,
		EnvLookup: func(k string) string {
			if k == packTrustedKeysDirEnv {
				return envDir
			}
			return ""
		},
		EnvList: func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := store.Keys[envKid]; !ok {
		t.Errorf("env dir key %s not loaded", envKid)
	}
	if _, ok := store.Keys["home-key"]; ok {
		t.Errorf("home default key should NOT load when env dir is set")
	}
}

func TestLoadPackTrustStore_ExtraKeyFiles(t *testing.T) {
	dir := t.TempDir()
	kid, _ := writeTestPubKey(t, dir, "acme")
	store, err := LoadPackTrustStore(PackTrustStoreOptions{
		ExtraKeyFiles:   []string{filepath.Join(dir, kid+".pub")},
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList:         func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := store.Keys[kid]; !ok {
		t.Errorf("--key file not loaded into trust store")
	}
}

func TestLoadPackTrustStore_EnvBadBase64(t *testing.T) {
	_, err := LoadPackTrustStore(PackTrustStoreOptions{
		HomeDirOverride: t.TempDir(),
		EnvLookup:       func(string) string { return "" },
		EnvList: func() []string {
			return []string{"CORDUM_PACK_TRUSTED_KEY_BAD=%%not-base64%%"}
		},
	})
	if !errors.Is(err, signing.ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey on bad env base64, got %v", err)
	}
}
