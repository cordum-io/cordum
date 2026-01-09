package gateway

import (
	"bytes"
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
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("acquire lock: %d %s", rr.Code, rr.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/locks?resource=lock:test", nil)
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
	releaseRR := httptest.NewRecorder()
	s.handleReleaseLock(releaseRR, releaseReq)
	if releaseRR.Code != http.StatusOK {
		t.Fatalf("release lock: %d %s", releaseRR.Code, releaseRR.Body.String())
	}
}
