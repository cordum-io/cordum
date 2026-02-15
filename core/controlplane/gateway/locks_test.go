package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLockHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	acquire := map[string]any{
		"resource": "lock:test",
		"owner":    "tester",
		"mode":     "exclusive",
		"ttl_ms":   5000,
	}
	body, _ := json.Marshal(acquire)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("acquire lock: %d %s", rr.Code, rr.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/locks?resource=lock:test", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getRR := httptest.NewRecorder()
	s.handleGetLock(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get lock: %d %s", getRR.Code, getRR.Body.String())
	}

	renew := map[string]any{
		"resource": "lock:test",
		"owner":    "tester",
		"ttl_ms":   5000,
	}
	renewBody, _ := json.Marshal(renew)
	renewReq := httptest.NewRequest(http.MethodPost, "/api/v1/locks/renew", bytes.NewReader(renewBody))
	renewReq.Header.Set("X-Tenant-ID", "default")
	renewRR := httptest.NewRecorder()
	s.handleRenewLock(renewRR, renewReq)
	if renewRR.Code != http.StatusOK {
		t.Fatalf("renew lock: %d %s", renewRR.Code, renewRR.Body.String())
	}

	release := map[string]any{
		"resource": "lock:test",
		"owner":    "tester",
	}
	releaseBody, _ := json.Marshal(release)
	releaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/locks/release", bytes.NewReader(releaseBody))
	releaseReq.Header.Set("X-Tenant-ID", "default")
	releaseRR := httptest.NewRecorder()
	s.handleReleaseLock(releaseRR, releaseReq)
	if releaseRR.Code != http.StatusOK {
		t.Fatalf("release lock: %d %s", releaseRR.Code, releaseRR.Body.String())
	}
}

func TestLockGet_ViewerAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"admin-key","role":"admin"},{"key":"viewer-key","role":"viewer"}]`,
	})

	// Acquire a lock first (as admin).
	acquire := map[string]any{"resource": "lock:viewer-test", "owner": "tester", "mode": "exclusive", "ttl_ms": 5000}
	body, _ := json.Marshal(acquire)
	aReq := httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", bytes.NewReader(body))
	aReq.Header.Set("X-Tenant-ID", "default")
	authCtx := &AuthContext{Role: "admin", Tenant: "default"}
	aReq = aReq.WithContext(context.WithValue(aReq.Context(), authContextKey{}, authCtx))
	aRR := httptest.NewRecorder()
	s.handleAcquireLock(aRR, aReq)
	if aRR.Code != http.StatusOK {
		t.Fatalf("acquire lock: %d %s", aRR.Code, aRR.Body.String())
	}

	// Viewer should be allowed to read locks.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/locks?resource=lock:viewer-test", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	viewerCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	getReq = getReq.WithContext(context.WithValue(getReq.Context(), authContextKey{}, viewerCtx))
	getRR := httptest.NewRecorder()
	s.handleGetLock(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for viewer on lock get, got %d: %s", getRR.Code, getRR.Body.String())
	}
}

func TestLockAcquire_ViewerForbidden(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer"}]`,
	})

	acquire := map[string]any{"resource": "lock:forbidden", "owner": "tester", "mode": "exclusive", "ttl_ms": 5000}
	body, _ := json.Marshal(acquire)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	authCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleAcquireLock(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on lock acquire, got %d: %s", rec.Code, rec.Body.String())
	}
}
