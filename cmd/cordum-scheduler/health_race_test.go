package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestHealthDepsConcurrentSetAndServe exercises the startup data-race window:
// the metrics server serves /health (ServeHTTP) while the main goroutine
// publishes the backing dependencies (setDeps). It must be clean under
// `go test -race` (the canonical CI gate; -race is unavailable on a CGO-off
// Windows host, so this is verified on Linux/WSL CI). nil deps are valid —
// ServeHTTP nil-checks each field and returns 503 "degraded" — so the
// reproduction needs no Redis/NATS.
func TestHealthDepsConcurrentSetAndServe(t *testing.T) {
	h := &healthDeps{}
	var wg sync.WaitGroup

	// Writer: repeatedly publish deps, mirroring the main goroutine's startup
	// assignment that previously raced with concurrent /health reads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.setDeps(nil, nil, nil)
		}
	}()

	// Readers: concurrent /health probes during the startup window.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/health", nil)
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
					t.Errorf("unexpected /health status %d (want 200 or 503)", rec.Code)
				}
			}
		}()
	}
	wg.Wait()
}
