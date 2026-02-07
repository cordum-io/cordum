package gateway

import (
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestStartHTTPServerInvalidAddr(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := startHTTPServer(s, "127.0.0.1:-1", "127.0.0.1:-2", nil); err == nil {
		t.Fatalf("expected listen error")
	}
}

func TestStartHTTPServerGracefulShutdown(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	// Pick free ports.
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}
	metricsLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen metrics: %v", err)
	}
	httpAddr := httpLis.Addr().String()
	metricsAddr := metricsLis.Addr().String()
	httpLis.Close()
	metricsLis.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- startHTTPServer(s, httpAddr, metricsAddr, nil)
	}()

	// Wait for server to become healthy.
	var healthy bool
	for i := 0; i < 30; i++ {
		resp, err := http.Get("http://" + httpAddr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("server did not become healthy")
	}

	// Send interrupt to trigger graceful shutdown.
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find process: %v", err)
	}
	if err := p.Signal(os.Interrupt); err != nil {
		t.Skipf("signal not supported on this platform: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil on clean shutdown, got: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}
