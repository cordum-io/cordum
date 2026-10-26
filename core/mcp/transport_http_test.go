package mcp

import (
	"bufio"
	"bytes"
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

// testSessionOwner stands in for the gateway's auth-derived owner key.
func testSessionOwner(r *http.Request) string { return r.Header.Get("X-Test-Owner") }

// newOwnedHTTPTransport serves a session-bound transport on /sse + /message.
// Its tests stay sequential: their servers and streams would otherwise skew
// the goroutine count TestHTTPTransport_TimerStoppedOnEarlyReturn samples.
func newOwnedHTTPTransport(t *testing.T, responseTimeout time.Duration) (*HTTPTransport, string) {
	t.Helper()
	transport := NewHTTPTransport(DefaultMaxMessageBytes, responseTimeout).WithSessionOwner(testSessionOwner)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sse", transport.HandleSSE)
	mux.HandleFunc("POST /message", transport.HandleMessage)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { _ = transport.Close() }) // runs first: ends open SSE streams
	return transport, srv.URL
}

// openSession opens an SSE stream as owner and returns its session id plus the
// stream positioned after the initial session event.
func openSession(t *testing.T, baseURL, owner string) (string, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/sse", nil)
	if err != nil {
		t.Fatalf("new sse request: %v", err)
	}
	req.Header.Set("X-Test-Owner", owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	sessionID := resp.Header.Get("X-MCP-Session-ID")
	if resp.StatusCode != http.StatusOK || sessionID == "" {
		t.Fatalf("open sse: status %d session %q", resp.StatusCode, sessionID)
	}
	stream := bufio.NewReader(resp.Body)
	for i := 0; i < 3; i++ { // "event: session", "data: {...}", blank line
		if _, err := stream.ReadString('\n'); err != nil {
			t.Fatalf("read sse prelude: %v", err)
		}
	}
	return sessionID, stream
}

// post sends one JSON-RPC message as owner, naming sessionID when non-empty.
func post(target, owner, sessionID, body string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Owner", owner)
	if sessionID != "" {
		req.Header.Set("X-MCP-Session-ID", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func mustPost(t *testing.T, target, owner, sessionID, body string) (int, []byte) {
	t.Helper()
	status, raw, err := post(target, owner, sessionID, body)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	return status, raw
}

func readMessageWithin(t *testing.T, transport *HTTPTransport, d time.Duration) *JSONRPCMessage {
	t.Helper()
	got := make(chan *JSONRPCMessage, 1)
	go func() {
		msg, _ := transport.ReadMessage()
		got <- msg
	}()
	select {
	case msg := <-got:
		if msg == nil {
			t.Fatal("transport closed before a message was queued")
		}
		return msg
	case <-time.After(d):
		t.Fatal("no message was queued")
		return nil
	}
}

// A caller naming another caller's session id gets exactly the answer an
// unknown session id gets, and its request is never dispatched, so nothing it
// sends can be answered into the owner's SSE stream.
func TestHTTPTransport_RejectsForeignSession(t *testing.T) {
	transport, baseURL := newOwnedHTTPTransport(t, 500*time.Millisecond)
	aliceSession, _ := openSession(t, baseURL, "alice")
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	// No dispatcher runs: anything HandleMessage accepted would stay queued.
	foreignStatus, foreign := mustPost(t, baseURL+"/message", "bob", aliceSession, ping)
	unknownStatus, unknown := mustPost(t, baseURL+"/message", "bob", "no-such-session", ping)
	queryStatus, viaQuery := mustPost(t, baseURL+"/message?session_id="+aliceSession, "bob", "", ping)

	if foreignStatus != http.StatusNotFound {
		t.Fatalf("foreign session: status %d body %s, want 404", foreignStatus, foreign)
	}
	if unknownStatus != foreignStatus || !bytes.Equal(unknown, foreign) {
		t.Fatalf("foreign vs unknown session answers differ: %d %s / %d %s", foreignStatus, foreign, unknownStatus, unknown)
	}
	if queryStatus != foreignStatus || !bytes.Equal(viaQuery, foreign) {
		t.Fatalf("session_id query param not bound: %d %s", queryStatus, viaQuery)
	}
	var rpc JSONRPCResponse
	if err := json.Unmarshal(foreign, &rpc); err != nil || rpc.Error == nil || rpc.Error.Code != -32001 {
		t.Fatalf("foreign session: want JSON-RPC error -32001, got %s (err %v)", foreign, err)
	}
	if queued := len(transport.incoming); queued != 0 {
		t.Fatalf("%d rejected requests were queued for dispatch", queued)
	}
	transport.mu.RLock()
	pending, streamed := len(transport.pending), len(transport.sessions[aliceSession].events)
	transport.mu.RUnlock()
	if pending != 0 || streamed != 0 {
		t.Fatalf("rejected requests left %d pending slots and %d events in alice's stream", pending, streamed)
	}
}

// A caller reusing another session's id and JSON-RPC id cannot take over the
// owner's pending reply slot: the owner still receives its own response.
func TestHTTPTransport_ForeignCallerCannotTakePendingResponse(t *testing.T) {
	transport, baseURL := newOwnedHTTPTransport(t, 5*time.Second)
	aliceSession, _ := openSession(t, baseURL, "alice")
	const call = `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`

	type result struct {
		status int
		body   []byte
		err    error
	}
	aliceDone := make(chan result, 1)
	go func() {
		status, body, err := post(baseURL+"/message", "alice", aliceSession, call)
		aliceDone <- result{status, body, err}
	}()
	aliceMsg := readMessageWithin(t, transport, 5*time.Second) // alice's slot is registered

	if status, body := mustPost(t, baseURL+"/message", "bob", aliceSession, call); status != http.StatusNotFound {
		t.Fatalf("bob reusing alice's session and id: status %d body %s, want 404", status, body)
	}
	if err := transport.WriteMessage(&JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: aliceMsg.ID, Result: "for-alice", sessionID: aliceMsg.sessionID}); err != nil {
		t.Fatalf("write alice's response: %v", err)
	}
	select {
	case got := <-aliceDone:
		if got.err != nil || got.status != http.StatusOK || !strings.Contains(string(got.body), `"for-alice"`) {
			t.Fatalf("alice lost her response: status %d body %s err %v", got.status, got.body, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alice's request never completed")
	}
}

// The owner keeps the whole flow: replies in the POST body, late replies on its
// own SSE stream, and session-less POSTs are still answered directly.
func TestHTTPTransport_OwnerSessionAndDirectRequestsStillWork(t *testing.T) {
	transport, baseURL := newOwnedHTTPTransport(t, 500*time.Millisecond)
	aliceSession, aliceStream := openSession(t, baseURL, "alice")

	release := make(chan struct{})
	go func() { // echo dispatcher; "slow" waits for release to force a late reply
		for {
			msg, err := transport.ReadMessage()
			if err != nil {
				return
			}
			if msg.Method == "slow" {
				select {
				case <-release:
				case <-transport.done:
					return
				}
			}
			_ = transport.WriteMessage(&JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: msg.ID, Result: msg.Method, sessionID: msg.sessionID})
		}
	}()

	for _, tc := range []struct{ name, owner, session string }{
		{"own session", "alice", aliceSession},
		{"no session", "bob", ""},
	} {
		status, body := mustPost(t, baseURL+"/message", tc.owner, tc.session, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if status != http.StatusOK || !strings.Contains(string(body), `"result":"ping"`) {
			t.Fatalf("%s: status %d body %s, want 200 with ping result", tc.name, status, body)
		}
	}

	if status, body := mustPost(t, baseURL+"/message", "alice", aliceSession, `{"jsonrpc":"2.0","id":"late","method":"slow"}`); status != http.StatusGatewayTimeout {
		t.Fatalf("slow call: status %d body %s, want 504", status, body)
	}
	close(release)
	line := make(chan string, 1)
	go func() {
		s, _ := aliceStream.ReadString('\n')
		line <- s
	}()
	select {
	case got := <-line:
		if !strings.Contains(got, `"id":"late"`) {
			t.Fatalf("late reply not on alice's stream: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late reply never reached alice's stream")
	}
}
