// cordum-gateway-sim is the conformance-suite gateway simulator. It
// serves an in-memory subset of the real Cordum gateway surface —
// exactly enough to drive the 20 fixtures in
// sdk/conformance/fixtures/ — on an ephemeral port, then prints its
// bound URL to stdout so each harness subprocess can read the port
// and build an SDK client pointed at it.
//
// Startup budget: <100 ms. No external deps, no Redis, no NATS, no
// disk writes. Determinism guarantees: every id comes from a
// monotonic counter seeded fresh on each process launch; every
// timestamp is Origin + monotonic offset. Fixtures mask opaque
// values with $any$ / $timestamp$ so byte-equal diffs across runs
// are possible on the grading harness.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
	"github.com/cordum/cordum-sdk-conformance-simulator/internal/handlers"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sim: %v", err)
	}
}

func run() error {
	addr := os.Getenv("CORDUM_SIM_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0" // ephemeral port by default
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	eng := engine.New()
	mux := http.NewServeMux()

	handlers.Agents(mux, eng)
	handlers.Jobs(mux, eng)
	handlers.Workflows(mux, eng)
	handlers.Policies(mux, eng)
	handlers.Auth(mux, eng)
	handlers.Stream(mux, eng)

	// Health probe — used by harnesses to wait-for-up without
	// guessing at a startup sleep.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	url := "http://" + listener.Addr().String()
	fmt.Fprintln(os.Stdout, url)
	fmt.Fprintf(os.Stderr, "cordum-gateway-sim listening on %s\n", url)

	// Graceful shutdown on SIGINT/SIGTERM so the harness's test
	// teardown can send a signal without the OS reaping the process
	// mid-request.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return nil
}
