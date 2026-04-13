package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(data)
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	originalStdout := os.Stdout
	originalStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	}()

	fn()

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := stdoutR.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	if err := stderrR.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return string(stdout), string(stderr)
}

func TestRunTopicList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/topics" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(topicListResponse{
			Items: []topicListItem{
				{Name: "job.zeta", Pool: "pool-b", InputSchemaID: "schemas/ZetaIn", OutputSchemaID: "schemas/ZetaOut", Status: "deprecated", ActiveWorkerCount: 2},
				{Name: "job.alpha", Pool: "pool-a", InputSchemaID: "schemas/AlphaIn", OutputSchemaID: "schemas/AlphaOut", Status: "active", ActiveWorkerCount: 7},
			},
		})
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runTopicList([]string{"--gateway", srv.URL}); err != nil {
			t.Fatalf("runTopicList returned error: %v", err)
		}
	})

	if !strings.Contains(stdout, "NAME") || !strings.Contains(stdout, "ACTIVE WORKERS") {
		t.Fatalf("expected topic list header, got %q", stdout)
	}
	alphaIndex := strings.Index(stdout, "job.alpha")
	zetaIndex := strings.Index(stdout, "job.zeta")
	if alphaIndex < 0 || zetaIndex < 0 {
		t.Fatalf("expected both topics in output, got %q", stdout)
	}
	if alphaIndex > zetaIndex {
		t.Fatalf("expected sorted output, got %q", stdout)
	}
	for _, want := range []string{"pool-a", "schemas/AlphaIn", "schemas/AlphaOut", "active", "7"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
}

func TestRunTopicCreate(t *testing.T) {
	var got topicRegistration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/topics" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(got)
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		err := runTopicCreate([]string{
			"--gateway", srv.URL,
			"--pool", "pool-a",
			"--input-schema", "schema/input",
			"--output-schema", "schema/output",
			"--pack-id", "pack.demo",
			"--requires", "gpu,linux",
			"--risk-tags", "external,write",
			"--status", "ACTIVE",
			"job.demo.sync",
		})
		if err != nil {
			t.Fatalf("runTopicCreate returned error: %v", err)
		}
	})

	if got.Name != "job.demo.sync" || got.Pool != "pool-a" {
		t.Fatalf("unexpected request payload: %+v", got)
	}
	if got.InputSchemaID != "schema/input" || got.OutputSchemaID != "schema/output" {
		t.Fatalf("unexpected schema IDs: %+v", got)
	}
	if got.PackID != "pack.demo" || got.Status != "active" {
		t.Fatalf("unexpected pack/status values: %+v", got)
	}
	if strings.Join(got.Requires, ",") != "gpu,linux" {
		t.Fatalf("unexpected requires: %v", got.Requires)
	}
	if strings.Join(got.RiskTags, ",") != "external,write" {
		t.Fatalf("unexpected risk tags: %v", got.RiskTags)
	}
	if !strings.Contains(stdout, `Topic "job.demo.sync" registered`) {
		t.Fatalf("expected success message, got %q", stdout)
	}
}

func TestRunTopicCreateValidatesNameBeforeRequest(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Fatalf("unexpected HTTP request: %s %s", r.Method, r.URL.String())
	}))
	defer srv.Close()

	err := runTopicCreate([]string{"--gateway", srv.URL, "--pool", "pool-a", "bad.topic"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "job.*") {
		t.Fatalf("expected topic validation error, got %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected no HTTP requests, got %d", requests)
	}
}

func TestRunTopicDelete(t *testing.T) {
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
		if err := runTopicDelete([]string{"--gateway", srv.URL, "job.demo.sync"}); err != nil {
			t.Fatalf("runTopicDelete returned error: %v", err)
		}
	})

	if gotPath != "/api/v1/topics/job.demo.sync" {
		t.Fatalf("unexpected delete path: %s", gotPath)
	}
	if !strings.Contains(stdout, `Topic "job.demo.sync" deleted`) {
		t.Fatalf("expected delete confirmation, got %q", stdout)
	}
}

func TestUsageIncludesTopicAndWorkerCommands(t *testing.T) {
	stdout := captureStdout(t, func() {
		usage()
	})

	for _, want := range []string{
		"cordumctl license install",
		"cordumctl license info",
		"cordumctl topic list",
		"cordumctl topic create",
		"cordumctl worker credential list",
		"cordumctl worker credential create",
		"--tenant",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected usage to contain %q, got %q", want, stdout)
		}
	}
}
