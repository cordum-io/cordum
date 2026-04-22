package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/delegation"
)

func TestRunDelegationKeygenE(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "delegation.pem")

	stdout := captureStdout(t, func() {
		if err := runDelegationKeygenE([]string{"--out", outPath, "--kid", "DLG_7"}); err != nil {
			t.Fatalf("runDelegationKeygenE() error = %v", err)
		}
	})

	publicKey := strings.TrimSpace(stdout)
	if publicKey == "" {
		t.Fatal("expected public key on stdout")
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	t.Setenv("CORDUM_DELEGATION_PRIVATE_KEY", string(data))
	t.Setenv("CORDUM_DELEGATION_KEY_ID", "DLG_7")

	signingKey, err := delegation.LoadSigningKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSigningKeyFromEnv() error = %v", err)
	}
	encodedPublicKey, err := delegation.EncodePublicKeyBase64(signingKey.PublicKey())
	if err != nil {
		t.Fatalf("EncodePublicKeyBase64() error = %v", err)
	}
	if encodedPublicKey != publicKey {
		t.Fatalf("stdout public key = %q, want %q", publicKey, encodedPublicKey)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outPath)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("private key mode = %04o, want 0600", got)
		}
	}
}

func TestRunDelegationKeygenERejectsExistingFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "delegation.pem")
	if err := os.WriteFile(outPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := runDelegationKeygenE([]string{"--out", outPath})
	if err == nil {
		t.Fatal("expected error when output file already exists")
	}
}
