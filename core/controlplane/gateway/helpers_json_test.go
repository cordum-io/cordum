package gateway

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"ok": "true"})

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected non-empty body")
	}
}

func TestWriteJSONLogsErrorNoPanic(t *testing.T) {
	w := httptest.NewRecorder()
	// math.Inf is not representable in JSON and causes json.Encoder to return an error.
	writeJSON(w, map[string]float64{"bad": math.Inf(1)})
	// The key assertion is that writeJSON does not panic — it logs the error internally.
}

func TestMaxBodyMiddleware_OversizedContentLength(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for oversized body")
	})
	handler := maxBodyMiddleware(inner)

	body := strings.NewReader(strings.Repeat("x", 100))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", body)
	req.ContentLength = defaultMaxJSONBodyBytes + 1
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") {
		t.Fatalf("expected tier limit error in body, got %s", rr.Body.String())
	}
}

func TestMaxBodyMiddleware_OversizedChunkedBody(t *testing.T) {
	// Handler that reads body via decodeJSONBody — exercises MaxBytesReader.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dst map[string]any
		if err := decodeJSONBody(w, r, &dst); err != nil {
			writeJSONDecodeError(w, err, "bad json")
			return
		}
		writeJSON(w, dst)
	})
	handler := maxBodyMiddleware(inner)

	// Build valid JSON that exceeds the 2MB limit.
	// {"x":"aaa...aaa"} — the value is padded to exceed the limit.
	bigVal := strings.Repeat("a", int(defaultMaxJSONBodyBytes))
	bigJSON := `{"x":"` + bigVal + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(bigJSON))
	req.ContentLength = -1 // unknown length (chunked transfer)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "max_body_bytes") {
		t.Fatalf("expected max_body_bytes limit in body, got %s", rr.Body.String())
	}
}

func TestMaxBodyMiddleware_PassesNormalRequest(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var dst map[string]any
		if err := decodeJSONBody(w, r, &dst); err != nil {
			writeJSONDecodeError(w, err, "bad json")
			return
		}
		writeJSON(w, dst)
	})
	handler := maxBodyMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{"key":"value"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("handler should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestDecodeJSONBody_RejectsUnknownFields(t *testing.T) {
	type request struct {
		Name string `json:"name"`
	}
	var dst request
	req := httptest.NewRequest(http.MethodPost, "/api/v1/example", strings.NewReader(`{"name":"ok","extra":"reject"}`))
	rec := httptest.NewRecorder()

	err := decodeJSONBody(rec, req, &dst)
	if err == nil {
		t.Fatal("expected unknown JSON field to be rejected")
	}
	writeJSONDecodeError(rec, err, "invalid request body")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMaxBodyMiddleware_SkipsGET(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := maxBodyMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("GET should pass through")
	}
}

func TestMaxBodyMiddleware_SkipsMultipart(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := maxBodyMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/install", strings.NewReader("fake multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("multipart should pass through without body limit")
	}
}

func TestMaxBodyMiddleware_LimitsDELETEWithBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for oversized DELETE body")
	})
	handler := maxBodyMiddleware(inner)

	body := strings.NewReader(strings.Repeat("x", 100))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/j1", body)
	req.ContentLength = defaultMaxJSONBodyBytes + 1
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for oversized DELETE body, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") {
		t.Fatalf("expected tier limit error in body, got %s", rr.Body.String())
	}
}

func TestMaxBodyMiddleware_SkipsHEAD(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := maxBodyMiddleware(inner)

	req := httptest.NewRequest(http.MethodHead, "/api/v1/config", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("HEAD should pass through without body limit")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestMaxBodyMiddleware_SkipsOPTIONS(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := maxBodyMiddleware(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/config", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("OPTIONS should pass through without body limit")
	}
}
