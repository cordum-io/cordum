package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWorkerCredentialCreateEnrollsProofKey(t *testing.T) {
	publicKeyPath := filepath.Join(t.TempDir(), "worker-public.pem")
	publicKey := "-----BEGIN PUBLIC KEY-----\ntest-public-key\n-----END PUBLIC KEY-----\n"
	if err := os.WriteFile(publicKeyPath, []byte(publicKey), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var got workerCredentialCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(issuedWorkerCredential{
			workerCredentialRecord: workerCredentialRecord{WorkerID: "worker-a"}, Token: "token",
		})
	}))
	defer srv.Close()

	_, _ = captureOutput(t, func() {
		err := runWorkerCredentialCreate([]string{
			"--gateway", srv.URL, "--worker-id", "worker-a",
			"--agent-id", "agent-a",
			"--proof-key-id", "key-a", "--proof-public-key-file", publicKeyPath,
		})
		if err != nil {
			t.Fatalf("runWorkerCredentialCreate: %v", err)
		}
	})
	if got.ProofKeyID != "key-a" || got.ProofAlgorithm != "ECDSA_P256_SHA256" || got.ProofPublicKeyPEM != publicKey {
		t.Fatalf("proof enrollment request = %+v", got)
	}
	if got.AgentID != "agent-a" {
		t.Fatalf("agent linkage request = %q, want agent-a", got.AgentID)
	}
}

func TestRunWorkerCredentialCreateRejectsInvalidProofFlags(t *testing.T) {
	validPath := filepath.Join(t.TempDir(), "worker-public.pem")
	if err := os.WriteFile(validPath, []byte("public key"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	oversizedPath := filepath.Join(t.TempDir(), "oversized.pem")
	if err := os.WriteFile(oversizedPath, []byte(strings.Repeat("x", 4097)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing agent id", []string{"--worker-id", "worker-a", "--proof-key-id", "key-a", "--proof-public-key-file", validPath}, "agent-id"},
		{"missing key id", []string{"--worker-id", "worker-a", "--proof-public-key-file", validPath}, "proof-key-id"},
		{"missing public key file", []string{"--worker-id", "worker-a", "--proof-key-id", "key-a"}, "proof-public-key-file"},
		{"wrong algorithm", []string{"--worker-id", "worker-a", "--proof-key-id", "key-a", "--proof-public-key-file", validPath, "--proof-algorithm", "ECDSA_P384_SHA384"}, "ECDSA_P256_SHA256"},
		{"oversized public key", []string{"--worker-id", "worker-a", "--proof-key-id", "key-a", "--proof-public-key-file", oversizedPath}, "too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runWorkerCredentialCreate(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestRunWorkerCredentialListShowsProofMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(workerCredentialListResponse{Items: []workerCredentialRecord{{
			WorkerID: "worker-a", AgentID: "agent-a", ProofKeyID: "key-a", ProofAlgorithm: "ECDSA_P256_SHA256",
		}}})
	}))
	defer srv.Close()
	stdout := captureStdout(t, func() {
		if err := runWorkerCredentialList([]string{"--gateway", srv.URL}); err != nil {
			t.Fatalf("runWorkerCredentialList: %v", err)
		}
	})
	for _, want := range []string{"AGENT ID", "agent-a", "PROOF KEY", "key-a", "ECDSA_P256_SHA256"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list output missing %q: %s", want, stdout)
		}
	}
}
