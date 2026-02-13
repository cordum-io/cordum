package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPBridgeSubmitJob(t *testing.T) {
	t.Parallel()

	var seenAPIKey string
	var seenTenant string
	var seenPrompt string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %q", r.Method)
		}
		seenAPIKey = r.Header.Get("X-API-Key")
		seenTenant = r.Header.Get("X-Tenant-ID")

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		seenPrompt, _ = body["prompt"].(string)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id":   "job-1",
			"trace_id": "trace-1",
		})
	}))
	defer srv.Close()

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "tenant-a",
		AllowPrivateHosts: true,
	}.WithAuthToken("test-key"))
	out, err := bridge.SubmitJob(context.Background(), SubmitJobInput{
		Prompt:   "hello",
		Topic:    "job.default",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}
	if out.JobID != "job-1" || out.TraceID != "trace-1" {
		t.Fatalf("unexpected submit output: %+v", out)
	}
	if seenAPIKey != "test-key" {
		t.Fatalf("expected api key header, got %q", seenAPIKey)
	}
	if seenTenant != "tenant-a" {
		t.Fatalf("expected tenant header, got %q", seenTenant)
	}
	if seenPrompt != "hello" {
		t.Fatalf("expected prompt=hello, got %q", seenPrompt)
	}
}

func TestHTTPBridgeErrorMapping(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs":
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "queue full"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-missing/cancel":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "job not found"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workflows/wf-1/runs":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "input schema validation failed: missing field"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/policy/simulate":
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "upstream service error"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "default",
		AllowPrivateHosts: true,
	})

	_, err := bridge.SubmitJob(context.Background(), SubmitJobInput{Prompt: "hello", Topic: "job.default"})
	assertBridgeStatus(t, err, http.StatusTooManyRequests)

	err = bridge.CancelJob(context.Background(), "job-missing", "")
	assertBridgeStatus(t, err, http.StatusNotFound)

	_, err = bridge.TriggerWorkflow(context.Background(), TriggerWorkflowInput{WorkflowID: "wf-1", Input: map[string]any{}})
	assertBridgeStatus(t, err, http.StatusUnprocessableEntity)

	_, err = bridge.SimulatePolicy(context.Background(), PolicySimInput{Topic: "job.default"})
	assertBridgeStatus(t, err, http.StatusBadGateway)
}

func TestHTTPBridgeMapsTerminalCancelStateToConflict(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs/job-1/cancel" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "job-1",
			"state":  "succeeded",
			"reason": "job already terminal",
		})
	}))
	defer srv.Close()

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "default",
		AllowPrivateHosts: true,
	})
	err := bridge.CancelJob(context.Background(), "job-1", "")
	var be *BridgeError
	if !errors.As(err, &be) {
		t.Fatalf("expected BridgeError, got %v", err)
	}
	if be.StatusCode != http.StatusConflict {
		t.Fatalf("expected status=409, got %d", be.StatusCode)
	}
	if !strings.Contains(strings.ToLower(be.Message), "already") {
		t.Fatalf("expected terminal conflict message, got %q", be.Message)
	}
}

func TestHTTPBridgeRejectsPrivateTargetByDefault(t *testing.T) {
	t.Parallel()

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:  "http://127.0.0.1:8081",
		TenantID: "default",
	})
	_, err := bridge.SubmitJob(context.Background(), SubmitJobInput{
		Prompt: "hello",
		Topic:  "job.default",
	})
	if err == nil {
		t.Fatal("expected private target rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("expected private-target error, got %v", err)
	}
}

func assertBridgeStatus(t *testing.T, err error, status int) {
	t.Helper()
	var be *BridgeError
	if !errors.As(err, &be) {
		t.Fatalf("expected BridgeError, got %v", err)
	}
	if be.StatusCode != status {
		t.Fatalf("expected status=%d, got %d (err=%v)", status, be.StatusCode, err)
	}
}
