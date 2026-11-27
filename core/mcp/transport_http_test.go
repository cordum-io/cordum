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
//
// It must not call t.Parallel: runtime.NumGoroutine counts the whole test
// process, and a sequential top-level test never overlaps the package's
// parallel tests. The client gets its own http.Transport, and the final
// sample waits for exiting goroutines to finish, so only a goroutine that
// outlives its request can push the count past the slack.
func TestHTTPTransport_TimerStoppedOnEarlyReturn(t *testing.T) {
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
	clientTransport := &http.Transport{}
	t.Cleanup(clientTransport.CloseIdleConnections)
	client := &http.Client{Timeout: 10 * time.Second, Transport: clientTransport}

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

	// Closing the idle connection ends its reader and writer goroutines on
	// both sides. A per-request leak adds at least N goroutines, far above
	// the slack, and keeps the count there until the settle deadline.
	clientTransport.CloseIdleConnections()
	const leakSlack = 25
	after := settledGoroutineCount(baseline+leakSlack, 5*time.Second)
	if delta := after - baseline; delta > leakSlack {
		t.Fatalf("goroutine leak suspected after %d roundtrips: baseline=%d after=%d delta=%d",
			N, baseline, after, delta)
	}
}

// settledGoroutineCount polls runtime.NumGoroutine until it is at most limit
// or the deadline passes, and returns the last count. Goroutines that are
// already exiting get time to finish; a leaked goroutine never does.
func settledGoroutineCount(limit int, deadline time.Duration) int {
	end := time.Now().Add(deadline)
	for {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n <= limit || time.Now().After(end) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}
