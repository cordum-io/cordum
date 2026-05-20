//go:build windows

package main

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestInheritedListenerFromWindowsHandleEnvServesReadiness(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := base.Addr().String()
	tcp, ok := base.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.TCPListener", base)
	}
	file, err := tcp.File()
	if err != nil {
		_ = base.Close()
		t.Fatalf("listener file: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := base.Close(); err != nil {
		t.Fatalf("close original listener: %v", err)
	}

	inherited, err := inheritedListenerFromEnv(map[string]string{
		"CORDUM_AGENTD_LISTENER_HANDLE": strconv.FormatUint(uint64(file.Fd()), 10),
	})
	if err != nil {
		t.Fatalf("inheritedListenerFromEnv returned error: %v", err)
	}
	if inherited == nil {
		t.Fatal("inheritedListenerFromEnv returned nil listener for CORDUM_AGENTD_LISTENER_HANDLE")
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(inherited)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("inherited listener server did not exit")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET inherited listener readiness: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
