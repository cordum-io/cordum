package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/locks"
)

// stubLockStore implements locks.Store for testing.
type stubLockStore struct {
	acquireErr error
	releaseErr error
	renewErr   error
	lock       *locks.Lock
	ok         bool
}

func (s *stubLockStore) Acquire(_ context.Context, _, _ string, _ locks.Mode, _ time.Duration) (*locks.Lock, bool, error) {
	return s.lock, s.ok, s.acquireErr
}

func (s *stubLockStore) Release(_ context.Context, _, _ string) (*locks.Lock, bool, error) {
	return s.lock, s.ok, s.releaseErr
}

func (s *stubLockStore) Renew(_ context.Context, _, _ string, _ time.Duration) (*locks.Lock, bool, error) {
	return s.lock, s.ok, s.renewErr
}

func (s *stubLockStore) Get(_ context.Context, _ string) (*locks.Lock, error) {
	return s.lock, nil
}

func adminCtx(req *http.Request) *http.Request {
	authCtx := &AuthContext{Role: "admin", Tenant: "default"}
	return req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
}

func lockBody(resource, owner string) string {
	return `{"resource":"` + resource + `","owner":"` + owner + `","ttl_ms":5000}`
}

func TestHandleAcquireLock_StoreError_Returns500(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.lockStore = &stubLockStore{acquireErr: errors.New("redis: connection refused to 10.0.0.5:6379")}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(lockBody("res1", "agent-1"))))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleAcquireLock(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errMsg, _ := body["error"].(string)
	if strings.Contains(errMsg, "redis") {
		t.Errorf("response leaks internal error: %s", errMsg)
	}
	if errMsg != "internal error" {
		t.Errorf("expected 'internal error', got %q", errMsg)
	}
}

func TestHandleReleaseLock_StoreError_Returns500(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.lockStore = &stubLockStore{releaseErr: errors.New("EVALSHA failed: cluster moved")}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/release", strings.NewReader(lockBody("res1", "agent-1"))))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleReleaseLock(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "internal error" {
		t.Errorf("expected 'internal error', got %q", body["error"])
	}
}

func TestHandleRenewLock_StoreError_Returns500(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.lockStore = &stubLockStore{renewErr: errors.New("context deadline exceeded")}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/renew", strings.NewReader(lockBody("res1", "agent-1"))))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleRenewLock(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "internal error" {
		t.Errorf("expected 'internal error', got %q", body["error"])
	}
}

func TestHandleAcquireLock_ValidationError_Returns400(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Empty resource — validation error should stay as 400 with controlled message.
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(`{"resource":"","owner":"a","ttl_ms":1000}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleAcquireLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "resource required" {
		t.Errorf("expected 'resource required', got %q", body["error"])
	}
}
