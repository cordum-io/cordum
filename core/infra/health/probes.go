// Package health provides reusable HTTP health probe endpoints for Cordum services.
//
// Three probe types are supported:
//   - /healthz (liveness): process alive, not deadlocked. No dependency checks.
//   - /readyz (readiness): all dependencies connected and healthy.
//   - /livez (startup): initialization complete, ready to serve first request.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// CheckFunc is a health check that returns nil if healthy, or an error describing
// the problem. Implementations must respect the provided context deadline.
type CheckFunc func(ctx context.Context) error

// ProbeServer manages health check registrations and serves probe endpoints.
type ProbeServer struct {
	mu              sync.RWMutex
	livenessChecks  map[string]CheckFunc
	readinessChecks map[string]CheckFunc
	startupComplete atomic.Bool
	maxGoroutines   int
}

// New creates a ProbeServer with sensible defaults.
func New() *ProbeServer {
	return &ProbeServer{
		livenessChecks:  make(map[string]CheckFunc),
		readinessChecks: make(map[string]CheckFunc),
		maxGoroutines:   10000,
	}
}

// RegisterLiveness adds a named liveness check. Liveness checks must be
// lightweight (<10ms) and must NOT check external dependencies.
func (p *ProbeServer) RegisterLiveness(name string, check CheckFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.livenessChecks[name] = check
}

// RegisterReadiness adds a named readiness check. Readiness checks verify
// that external dependencies (Redis, NATS, gRPC) are connected and responsive.
func (p *ProbeServer) RegisterReadiness(name string, check CheckFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readinessChecks[name] = check
}

// SetStartupComplete marks initialization as done. After this call,
// /livez returns 200 instead of 503.
func (p *ProbeServer) SetStartupComplete() {
	p.startupComplete.Store(true)
}

// SetMaxGoroutines overrides the goroutine threshold for the built-in
// liveness check. Default is 10000.
func (p *ProbeServer) SetMaxGoroutines(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxGoroutines = n
}

// Register mounts /healthz, /readyz, and /livez on the given mux.
func (p *ProbeServer) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", p.handleHealthz)
	mux.HandleFunc("GET /readyz", p.handleReadyz)
	mux.HandleFunc("GET /livez", p.handleLivez)
}

// handleHealthz runs liveness checks + a built-in goroutine count guard.
func (p *ProbeServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Millisecond)
	defer cancel()

	checks := make(map[string]string)
	healthy := true

	// Built-in goroutine guard.
	p.mu.RLock()
	maxG := p.maxGoroutines
	p.mu.RUnlock()
	numG := runtime.NumGoroutine()
	if numG > maxG {
		checks["goroutines"] = "excessive"
		healthy = false
	} else {
		checks["goroutines"] = "ok"
	}

	// Run registered liveness checks.
	p.mu.RLock()
	for name, check := range p.livenessChecks {
		if err := check(ctx); err != nil {
			checks[name] = err.Error()
			healthy = false
		} else {
			checks[name] = "ok"
		}
	}
	p.mu.RUnlock()

	writeProbeResponse(w, healthy, checks)
}

// handleReadyz runs all readiness checks (dependency connectivity).
func (p *ProbeServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	checks := make(map[string]string)
	healthy := true

	p.mu.RLock()
	for name, check := range p.readinessChecks {
		if err := check(ctx); err != nil {
			checks[name] = err.Error()
			healthy = false
		} else {
			checks[name] = "ok"
		}
	}
	p.mu.RUnlock()

	// If no readiness checks registered, report healthy (service has no deps).
	writeProbeResponse(w, healthy, checks)
}

// handleLivez reports whether initialization is complete.
func (p *ProbeServer) handleLivez(w http.ResponseWriter, r *http.Request) {
	if p.startupComplete.Load() {
		writeProbeResponse(w, true, map[string]string{"startup": "complete"})
	} else {
		writeProbeResponse(w, false, map[string]string{"startup": "initializing"})
	}
}

type probeResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func writeProbeResponse(w http.ResponseWriter, healthy bool, checks map[string]string) {
	status := "ok"
	httpCode := http.StatusOK
	if !healthy {
		status = "degraded"
		httpCode = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	if err := json.NewEncoder(w).Encode(probeResponse{Status: status, Checks: checks}); err != nil {
		slog.Error("health probe encode failed", "error", err)
	}
}
