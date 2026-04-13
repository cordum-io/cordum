package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunWorkerCredentialList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/workers/credentials" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(workerCredentialListResponse{
			Items: []workerCredentialRecord{
				{WorkerID: "worker-b", AllowedPools: []string{"pool-b"}, AllowedTopics: []string{"job.demo.write"}, CreatedBy: "admin", CreatedAt: "2026-04-08T10:00:00Z"},
				{WorkerID: "worker-a", AllowedPools: []string{"pool-a"}, AllowedTopics: []string{"job.demo.read"}, CreatedBy: "admin", CreatedAt: "2026-04-08T09:00:00Z", RevokedAt: "2026-04-08T11:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runWorkerCredentialList([]string{"--gateway", srv.URL}); err != nil {
			t.Fatalf("runWorkerCredentialList returned error: %v", err)
		}
	})

	if !strings.Contains(stdout, "WORKER ID") || !strings.Contains(stdout, "STATUS") {
		t.Fatalf("expected worker credential list header, got %q", stdout)
	}
	workerAIndex := strings.Index(stdout, "worker-a")
	workerBIndex := strings.Index(stdout, "worker-b")
	if workerAIndex < 0 || workerBIndex < 0 {
		t.Fatalf("expected workers in output, got %q", stdout)
	}
	if workerAIndex > workerBIndex {
		t.Fatalf("expected sorted worker output, got %q", stdout)
	}
	for _, want := range []string{"revoked", "active", "pool-a", "job.demo.read", "admin"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
}

func TestRunWorkerCredentialCreate(t *testing.T) {
	var got workerCredentialCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/workers/credentials" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(issuedWorkerCredential{
			workerCredentialRecord: workerCredentialRecord{
				WorkerID:      "worker-a",
				AllowedPools:  []string{"pool-a", "pool-b"},
				AllowedTopics: []string{"job.demo.read", "job.demo.write"},
				CreatedBy:     "admin",
				CreatedAt:     "2026-04-08T09:00:00Z",
			},
			Token: "secret-token-123",
		})
	}))
	defer srv.Close()

	stdout, stderr := captureOutput(t, func() {
		err := runWorkerCredentialCreate([]string{
			"--gateway", srv.URL,
			"--worker-id", "worker-a",
			"--allowed-pools", "pool-a,pool-b",
			"--allowed-topics", "job.demo.read,job.demo.write",
		})
		if err != nil {
			t.Fatalf("runWorkerCredentialCreate returned error: %v", err)
		}
	})

	if got.WorkerID != "worker-a" {
		t.Fatalf("unexpected worker id in request: %+v", got)
	}
	if strings.Join(got.AllowedPools, ",") != "pool-a,pool-b" {
		t.Fatalf("unexpected allowed pools: %v", got.AllowedPools)
	}
	if strings.Join(got.AllowedTopics, ",") != "job.demo.read,job.demo.write" {
		t.Fatalf("unexpected allowed topics: %v", got.AllowedTopics)
	}
	if strings.TrimSpace(stdout) != "secret-token-123" {
		t.Fatalf("expected token on stdout once, got %q", stdout)
	}
	if !strings.Contains(stderr, `WARNING: token for worker "worker-a" is shown only once`) {
		t.Fatalf("expected secure warning on stderr, got %q", stderr)
	}
	if strings.Contains(stderr, "secret-token-123") {
		t.Fatalf("token leaked to stderr: %q", stderr)
	}
}

func TestRunWorkerCredentialRevoke(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runWorkerCredentialRevoke([]string{"--gateway", srv.URL, "--worker-id", "worker-a"}); err != nil {
			t.Fatalf("runWorkerCredentialRevoke returned error: %v", err)
		}
	})

	if gotPath != "/api/v1/workers/credentials/worker-a" {
		t.Fatalf("unexpected revoke path: %s", gotPath)
	}
	if !strings.Contains(stdout, `Worker credential "worker-a" revoked`) {
		t.Fatalf("expected revoke confirmation, got %q", stdout)
	}
}
