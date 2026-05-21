package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestHTTPTransport_TimerStoppedOnEarlyReturn drives N successful
// roundtrips through HandleMessage with a long responseTimeout.
//
// Pre-fix (`case <-time.After(responseTimeout):`) every roundtrip
// allocates a runtime timer that survives until responseTimeout
// elapses, even though resp arrives first — so heap pressure grows
// proportional to RPS in long-lived gateway processes.
//
// Go does not expose a public timer-count metric, so this is a
// behavioural smoke + goroutine-count regression test: it asserts
// that 100 fast roundtrips do not leak Go goroutines (handler should
// return as soon as resp lands; the timer must not block the
// handler from finishing). It pairs with the source-level rail
// (`grep '\\btime\\.After\\b' core/mcp/transport_http.go` returns
// empty) which is the load-bearing assertion for the fix.
func TestHTTPTransport_TimerStoppedOnEarlyReturn(t *testing.T) {
	t.Parallel()

	const responseTimeout = time.Hour
	transport := NewHTTPTransport(DefaultMaxMessageBytes, responseTimeout)
	t.Cleanup(func() { _ = transport.Close() })

	// Echo loop: every read becomes a write so the handler's select
	// hits the resp branch immediately.
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		for {
			msg, err := transport.ReadMessage()
			if err != nil || msg == nil {
				return
			}
			_ = transport.WriteMessage(&JSONRPCMessage{
				JSONRPC:   JSONRPCVersion,
				ID:        msg.ID,
				Result:    map[string]any{"ok": true},
				sessionID: msg.sessionID,
			})
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", transport.HandleMessage)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const N = 100
	client := &http.Client{Timeout: 10 * time.Second}

	// Warm up to settle the http transport's internal goroutine pool
	// (idle connection reader, keepalive). Then sample baseline.
	warmupBody := strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	resp, err := client.Post(srv.URL+"/mcp/message", "application/json", warmupBody)
	if err != nil {
		t.Fatalf("warmup POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	runtime.GC()
	runtime.Gosched()
	baseline := runtime.NumGoroutine()

	for i := 1; i <= N; i++ {
		body := strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, i))
		resp, err := client.Post(srv.URL+"/mcp/message", "application/json", body)
		if err != nil {
			t.Fatalf("POST %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("POST %d: status %d body %s", i, resp.StatusCode, string(b))
		}
		var rpc JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if rpc.Error != nil {
			t.Fatalf("POST %d: rpc error %+v", i, rpc.Error)
		}
	}

	runtime.GC()
	runtime.Gosched()
	after := runtime.NumGoroutine()

	// Allow generous slack for net/http's idle-conn pool; the test
	// fails on order-of-magnitude growth that would indicate a
	// per-request goroutine leak.
	if delta := after - baseline; delta > 25 {
		t.Fatalf("goroutine leak suspected after %d roundtrips: baseline=%d after=%d delta=%d",
			N, baseline, after, delta)
	}
}
