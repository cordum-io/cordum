package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
)

func TestHealth_AllOK(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	// Create a real NATS bus for connected state.
	natsBus, err := bus.NewNatsBus("nats://127.0.0.1:14222")
	// NATS likely not available in unit tests — test with nil bus in degraded test.
	// For the "all OK" path, we mock by using a non-nil safety client.
	if err != nil {
		t.Skip("NATS not available, skipping full health OK test")
	}
	defer natsBus.Close()

	h := &healthDeps{
		jobStore:     jobStore,
		bus:          natsBus,
		safetyClient: &scheduler.SafetyClient{},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

func TestHealth_RedisDown(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	// Stop Redis to simulate failure.
	srv.Close()

	h := &healthDeps{
		jobStore:     jobStore,
		safetyClient: &scheduler.SafetyClient{},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "degraded" {
		t.Fatalf("expected status degraded, got %v", body["status"])
	}
	redis, ok := body["redis"].(map[string]any)
	if !ok {
		t.Fatalf("expected redis status object, got %v", body["redis"])
	}
	if redis["status"] != "error" {
		t.Fatalf("expected redis error status, got %v", redis["status"])
	}
}

func TestHealth_NATSDisconnected(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	// nil bus → disconnected
	h := &healthDeps{
		jobStore:     jobStore,
		bus:          nil,
		safetyClient: &scheduler.SafetyClient{},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "degraded" {
		t.Fatalf("expected status degraded, got %v", body["status"])
	}
	nats, ok := body["nats"].(map[string]any)
	if !ok {
		t.Fatalf("expected nats status object, got %v", body["nats"])
	}
	if nats["status"] != "error" {
		t.Fatalf("expected nats error status, got %v", nats["status"])
	}
}

func TestHealth_NilJobStore(t *testing.T) {
	h := &healthDeps{}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "degraded" {
		t.Fatalf("expected status degraded, got %v", body["status"])
	}
}
