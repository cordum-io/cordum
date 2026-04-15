package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz_Healthy(t *testing.T) {
	p := New()
	p.RegisterLiveness("tick", func(ctx context.Context) error { return nil })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected ok, got %s", resp.Status)
	}
	if resp.Checks["tick"] != "ok" {
		t.Fatalf("expected tick=ok, got %s", resp.Checks["tick"])
	}
}

func TestHealthz_Degraded(t *testing.T) {
	p := New()
	p.RegisterLiveness("stuck", func(ctx context.Context) error {
		return errors.New("deadlocked")
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "degraded" {
		t.Fatalf("expected degraded, got %s", resp.Status)
	}
	if resp.Checks["stuck"] != "deadlocked" {
		t.Fatalf("expected stuck=deadlocked, got %s", resp.Checks["stuck"])
	}
}

func TestReadyz_Healthy(t *testing.T) {
	p := New()
	p.RegisterReadiness("redis", func(ctx context.Context) error { return nil })
	p.RegisterReadiness("nats", func(ctx context.Context) error { return nil })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	p.handleReadyz(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Checks["redis"] != "ok" || resp.Checks["nats"] != "ok" {
		t.Fatalf("expected all checks ok, got %v", resp.Checks)
	}
}

func TestReadyz_Degraded(t *testing.T) {
	p := New()
	p.RegisterReadiness("redis", func(ctx context.Context) error { return nil })
	p.RegisterReadiness("nats", func(ctx context.Context) error {
		return errors.New("disconnected")
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	p.handleReadyz(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Checks["nats"] != "disconnected" {
		t.Fatalf("expected nats=disconnected, got %s", resp.Checks["nats"])
	}
}

func TestLivez_BeforeStartup(t *testing.T) {
	p := New()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/livez", nil)
	p.handleLivez(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before startup complete, got %d", rr.Code)
	}
}

func TestLivez_AfterStartup(t *testing.T) {
	p := New()
	p.SetStartupComplete()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/livez", nil)
	p.handleLivez(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after startup complete, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Checks["startup"] != "complete" {
		t.Fatalf("expected startup=complete, got %s", resp.Checks["startup"])
	}
}

func TestRegister_MountsAllEndpoints(t *testing.T) {
	p := New()
	p.SetStartupComplete()

	mux := http.NewServeMux()
	p.Register(mux)

	for _, path := range []string{"/healthz", "/readyz", "/livez"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rr.Code)
		}
	}
}

func TestHealthz_GoroutineGuard(t *testing.T) {
	p := New()
	p.SetMaxGoroutines(1) // threshold lower than any running program

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	p.handleHealthz(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when goroutine count exceeds threshold, got %d", rr.Code)
	}
	var resp probeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Checks["goroutines"] != "excessive" {
		t.Fatalf("expected goroutines=excessive, got %s", resp.Checks["goroutines"])
	}
}

func TestReadyz_NoDeps_Healthy(t *testing.T) {
	p := New()
	// No readiness checks registered — service has no dependencies.

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	p.handleReadyz(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when no deps registered, got %d", rr.Code)
	}
}
