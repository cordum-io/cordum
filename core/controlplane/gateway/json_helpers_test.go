package gateway

import (
	"math"
	"net/http"
	"net/http/httptest"
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
