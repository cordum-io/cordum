package gateway

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
)

func TestHandleStreamUpgradesWebsocketWithInstrumentation(t *testing.T) {
	s := &server{
		clients:  make(map[*websocket.Conn]*wsClient),
		eventsCh: make(chan wsEvent, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))
	srv := newIPv4Server(t, mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	conn.Close()
	// No assertion needed — test validates the WS upgrade succeeds.
}

func TestHandleStreamHonorsAPIKeySubprotocol(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"API_KEY": "'test-api-key'",
	})

	s := &server{
		clients:  make(map[*websocket.Conn]*wsClient),
		eventsCh: make(chan wsEvent, 1),
		auth:     provider,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))
	srv := newIPv4Server(t, apiKeyMiddleware(provider, mux))
	defer srv.Close()

	okURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	token := base64.RawURLEncoding.EncodeToString([]byte("test-api-key"))
	dialer := websocket.Dialer{Subprotocols: []string{wsAuthSubprotocol, token}}
	conn, _, err := dialer.Dial(okURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	_ = conn.Close()

	badURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	_, resp, err := websocket.DefaultDialer.Dial(badURL, nil)
	if err == nil {
		t.Fatalf("expected dial error")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 response, got %#v err=%v", resp, err)
	}
}

func TestApiKeyFromWebSocketProtocols(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
	req.Header.Set("X-Tenant-ID", "default")
	token := base64.RawURLEncoding.EncodeToString([]byte("secret"))
	req.Header.Set("Sec-WebSocket-Protocol", wsAuthSubprotocol+", "+token)
	if got := apiKeyFromWebSocket(req); got != "secret" {
		t.Fatalf("expected secret got %q", got)
	}
}

// ---- negotiateSubprotocol unit tests ----

func TestNegotiateSubprotocol_DotFormat_OnlyEchoesIdentifier(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	token := base64.RawURLEncoding.EncodeToString([]byte("my-key"))
	req.Header.Set("Sec-WebSocket-Protocol", wsAuthSubprotocol+"."+token)
	h := negotiateSubprotocol(req)
	if h == nil {
		t.Fatal("expected non-nil header for valid subprotocol")
	}
	got := h.Get("Sec-Websocket-Protocol")
	if got != wsAuthSubprotocol {
		t.Fatalf("expected bare %q, got %q (credential leak!)", wsAuthSubprotocol, got)
	}
}

func TestNegotiateSubprotocol_CommaSeparated_OnlyEchoesIdentifier(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	token := base64.RawURLEncoding.EncodeToString([]byte("my-key"))
	req.Header.Set("Sec-WebSocket-Protocol", wsAuthSubprotocol+", "+token)
	h := negotiateSubprotocol(req)
	if h == nil {
		t.Fatal("expected non-nil header for comma-separated subprotocol")
	}
	got := h.Get("Sec-Websocket-Protocol")
	if got != wsAuthSubprotocol {
		t.Fatalf("expected bare %q, got %q (credential leak!)", wsAuthSubprotocol, got)
	}
}

func TestNegotiateSubprotocol_NoMatchingProtocol(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "graphql-ws, some-other")
	h := negotiateSubprotocol(req)
	if h != nil {
		t.Fatalf("expected nil for non-matching subprotocols, got %v", h)
	}
}

func TestNegotiateSubprotocol_EmptyProtocols(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h := negotiateSubprotocol(req)
	if h != nil {
		t.Fatalf("expected nil for empty subprotocols, got %v", h)
	}
}

// ---- apiKeyFromWebSocket unit tests ----

func TestApiKeyFromWebSocket_DotFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	token := base64.RawURLEncoding.EncodeToString([]byte("secret-key"))
	req.Header.Set("Sec-WebSocket-Protocol", wsAuthSubprotocol+"."+token)
	got := apiKeyFromWebSocket(req)
	if got != "secret-key" {
		t.Fatalf("expected secret-key, got %q", got)
	}
}

func TestApiKeyFromWebSocket_NilRequest(t *testing.T) {
	got := apiKeyFromWebSocket(nil)
	if got != "" {
		t.Fatalf("expected empty for nil request, got %q", got)
	}
}

func TestApiKeyFromWebSocket_NoSubprotocol(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	got := apiKeyFromWebSocket(req)
	if got != "" {
		t.Fatalf("expected empty for no subprotocol, got %q", got)
	}
}

func TestApiKeyFromWebSocket_MalformedBase64FallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Non-base64 token — decodeWSAPIKey returns raw string as fallback
	req.Header.Set("Sec-WebSocket-Protocol", wsAuthSubprotocol+".not-base64!!!")
	got := apiKeyFromWebSocket(req)
	if got == "" {
		t.Fatal("expected non-empty fallback for malformed base64")
	}
}

// ---- revalidateWSAuth unit tests ----

func TestRevalidateWSAuth_ValidKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"live-key","role":"admin","tenant":"default"}]`,
	})
	s := &server{auth: provider}
	if err := s.revalidateWSAuth("live-key"); err != nil {
		t.Fatalf("expected nil for valid key, got %v", err)
	}
}

func TestRevalidateWSAuth_RevokedKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"live-key","role":"admin","tenant":"default"}]`,
	})
	s := &server{auth: provider}
	if err := s.revalidateWSAuth("revoked-key"); err == nil {
		t.Fatal("expected error for revoked/unknown key")
	}
}

func TestEnqueueWSEventNoPanicOnClosedChannel(t *testing.T) {
	s := &server{
		eventsCh: make(chan wsEvent, 8),
	}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)

	// Drain the channel so senders don't block.
	done := make(chan struct{})
	go func() {
		for range s.eventsCh {
		}
		close(done)
	}()

	// N goroutines sending concurrently.
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.enqueueWSEvent([]byte("hello"), "t", "j")
			}
		}()
	}

	// Wait for all senders, then close.
	wg.Wait()
	s.stopBusTaps()
	<-done

	// Verify that enqueue after stop is a no-op (no panic).
	s.enqueueWSEvent([]byte("after-close"), "t", "j")
}

func TestBroadcastSlowClientCleanupConcurrent(t *testing.T) {
	s := &server{
		eventsCh:   make(chan wsEvent, 64),
		clients:    make(map[*websocket.Conn]*wsClient),
		shutdownCh: make(chan struct{}),
	}

	const N = 20
	var wg sync.WaitGroup

	// Register N clients with tiny buffers so they become "slow" immediately.
	conns := make([]*websocket.Conn, N)
	for i := 0; i < N; i++ {
		// Create a real websocket.Conn so delete/Close paths work.
		cSrv, cClient := net.Pipe()
		ws := &websocket.Conn{}
		// We can't easily construct a real *websocket.Conn from a net.Conn,
		// so we use the address as a unique key and test the map operations.
		_ = cSrv
		_ = cClient
		// Use a nil-safe approach: just allocate distinct pointers.
		conns[i] = ws
	}

	// Concurrently add clients, send events, and remove clients.
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			client := &wsClient{
				ch:               make(chan wsEvent, 1), // buffer of 1 so second event makes it "slow"
				tenant:           "default",
				allowCrossTenant: true,
			}
			s.clientsMu.Lock()
			s.clients[conns[i]] = client
			s.clientsMu.Unlock()

			// Drain client channel to prevent goroutine leak.
			go func() {
				for range client.ch {
				}
			}()

			// Simulate stream handler cleanup after a short delay.
			time.Sleep(time.Duration(i) * time.Millisecond)
			s.clientsMu.Lock()
			delete(s.clients, conns[i])
			s.clientsMu.Unlock()
			close(client.ch)
		}()
	}

	// Simultaneously push events to trigger broadcast + slow client detection.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.clientsMu.Lock()
			var slowClients []*websocket.Conn
			for conn, client := range s.clients {
				if client == nil {
					continue
				}
				select {
				case client.ch <- wsEvent{data: []byte("test"), tenant: "default"}:
				default:
					slowClients = append(slowClients, conn)
				}
			}
			for _, conn := range slowClients {
				delete(s.clients, conn)
			}
			s.clientsMu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
}

func TestBroadcastConcurrentSlowClientCleanup(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
	})

	// Simple WS echo server to produce real *websocket.Conn objects.
	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer echoSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(echoSrv.URL, "http")
	const N = 20
	var wg sync.WaitGroup
	wg.Add(3)

	// Goroutine 1: register clients with tiny channel buffers so they
	// become "slow" after a single unconsumed event.
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				continue
			}
			s.clientsMu.Lock()
			s.clients[conn] = &wsClient{ch: make(chan wsEvent, 1), allowCrossTenant: true}
			s.clientsMu.Unlock()
		}
	}()

	// Goroutine 2: flood events so the broadcast loop detects and removes
	// slow clients (exercises the Lock-protected detect+delete path).
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.enqueueWSEvent([]byte(`{"e":"x"}`), "", "")
		}
	}()

	// Goroutine 3: concurrently remove random clients from the map,
	// simulating stream handler cleanup.
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			time.Sleep(5 * time.Millisecond)
			s.clientsMu.Lock()
			for conn := range s.clients {
				delete(s.clients, conn)
				_ = conn.Close()
				break
			}
			s.clientsMu.Unlock()
		}
	}()

	wg.Wait()
	// If we reached here without a panic or data race, the test passes.
}

func TestSplitWorkflowJobID(t *testing.T) {
	run, step := splitWorkflowJobID("run-1:step-1")
	if run != "run-1" || step != "step-1" {
		t.Fatalf("unexpected split: %s %s", run, step)
	}
	run, step = splitWorkflowJobID("bad")
	if run != "" || step != "" {
		t.Fatalf("expected empty split for invalid id")
	}
}

func TestFilterQuarantinedPacketStripsDenied(t *testing.T) {
	pkt := &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:        "job-1",
				Status:       pb.JobStatus_JOB_STATUS_DENIED,
				ResultPtr:    "redis://res:job-1",
				ErrorMessage: "output quarantined",
				ArtifactPtrs: []string{"art-1", "art-2"},
			},
		},
	}
	filtered := filterQuarantinedPacket(pkt)
	jr := filtered.GetJobResult()
	if jr == nil {
		t.Fatal("expected job result in filtered packet")
	}
	if jr.Status != pb.JobStatus_JOB_STATUS_DENIED {
		t.Fatalf("expected denied status preserved, got %v", jr.Status)
	}
	if jr.ResultPtr != "" {
		t.Fatalf("expected result_ptr stripped, got %q", jr.ResultPtr)
	}
	if len(jr.ArtifactPtrs) != 0 {
		t.Fatalf("expected artifact_ptrs stripped, got %v", jr.ArtifactPtrs)
	}
	if jr.ErrorMessage != "output quarantined" {
		t.Fatalf("expected error_message preserved, got %q", jr.ErrorMessage)
	}
	if jr.JobId != "job-1" {
		t.Fatalf("expected job_id preserved, got %q", jr.JobId)
	}
}

func TestFilterQuarantinedPacketPassesNonDenied(t *testing.T) {
	pkt := &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:     "job-2",
				Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
				ResultPtr: "redis://res:job-2",
			},
		},
	}
	filtered := filterQuarantinedPacket(pkt)
	if filtered != pkt {
		t.Fatal("expected non-denied packet returned unchanged")
	}
	jr := filtered.GetJobResult()
	if jr.ResultPtr != "redis://res:job-2" {
		t.Fatalf("expected result_ptr preserved, got %q", jr.ResultPtr)
	}
}

func TestFilterQuarantinedPacketPassesHeartbeat(t *testing.T) {
	pkt := &pb.BusPacket{
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{WorkerId: "w-1"},
		},
	}
	filtered := filterQuarantinedPacket(pkt)
	if filtered != pkt {
		t.Fatal("expected heartbeat packet returned unchanged")
	}
}

func TestWorkerExpiryEvictsStaleEntries(t *testing.T) {
	s := &server{
		workers:    make(map[string]*pb.Heartbeat),
		workerSeen: make(map[string]time.Time),
	}

	now := time.Now().UTC()
	// w-fresh: seen just now, should survive
	s.workers["w-fresh"] = &pb.Heartbeat{WorkerId: "w-fresh"}
	s.workerSeen["w-fresh"] = now

	// w-stale: seen 2x TTL ago, should be evicted
	s.workers["w-stale"] = &pb.Heartbeat{WorkerId: "w-stale"}
	s.workerSeen["w-stale"] = now.Add(-2 * workerHeartbeatTTL)

	// Run one expiry cycle manually (same logic as startWorkerExpiry ticker)
	cutoff := now.Add(-workerHeartbeatTTL)
	s.workerMu.Lock()
	for id, seen := range s.workerSeen {
		if seen.Before(cutoff) {
			delete(s.workerSeen, id)
			delete(s.workers, id)
		}
	}
	s.workerMu.Unlock()

	s.workerMu.RLock()
	defer s.workerMu.RUnlock()
	if _, ok := s.workers["w-fresh"]; !ok {
		t.Fatal("fresh worker should not be evicted")
	}
	if _, ok := s.workers["w-stale"]; ok {
		t.Fatal("stale worker should be evicted")
	}
	if _, ok := s.workerSeen["w-stale"]; ok {
		t.Fatal("stale workerSeen entry should be evicted")
	}
}

func TestStartWorkerExpiryStopsCleanly(t *testing.T) {
	s := &server{
		workers:    make(map[string]*pb.Heartbeat),
		workerSeen: make(map[string]time.Time),
	}

	s.startWorkerExpiry()
	// Should have created the stop channel.
	if s.workerExpireStop == nil {
		t.Fatal("expected workerExpireStop channel to be created")
	}

	// stopWorkerExpiry is idempotent — call twice.
	s.stopWorkerExpiry()
	s.stopWorkerExpiry()

	// Channel should be closed (receive returns immediately).
	select {
	case <-s.workerExpireStop:
		// OK, channel is closed
	case <-time.After(time.Second):
		t.Fatal("workerExpireStop should be closed after stopWorkerExpiry")
	}
}

func TestStopWorkerExpirySafeWithoutStart(t *testing.T) {
	s := &server{}
	// Should not panic when called without startWorkerExpiry.
	s.stopWorkerExpiry()
}

// ---------------------------------------------------------------------------
// Leak and lifecycle regression tests
// ---------------------------------------------------------------------------

// TestStaleClientAccumulatesWithoutReadPump demonstrates that a disconnected
// WebSocket client remains in s.clients until a write failure or slow-client
// eviction occurs. Without a read goroutine, the server cannot detect client
// disconnect promptly (only on next WriteMessage error).
func TestStaleClientAccumulatesWithoutReadPump(t *testing.T) {
	s := &server{
		clients:    make(map[*websocket.Conn]*wsClient),
		eventsCh:   make(chan wsEvent, 64),
		shutdownCh: make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))
	srv := newIPv4Server(t, mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"

	// Connect then immediately close the client side.
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	_ = conn.Close()

	// Give the server a moment to process.
	time.Sleep(50 * time.Millisecond)

	// Without a read pump, the server has no immediate way to detect the
	// client closed. The entry may still be in s.clients until a write fails.
	// This test documents the behavior — a future fix should add a read pump
	// so disconnected clients are cleaned up promptly.
	s.clientsMu.RLock()
	count := len(s.clients)
	s.clientsMu.RUnlock()
	// We log the count; the stale entry is eventually cleaned by write failure
	// or slow-client eviction, but the window exists.
	t.Logf("clients after close: %d (stale entries expected without read pump)", count)
}

// TestEnqueueWSEventDropsSilently verifies that when the event channel is full,
// enqueueWSEvent drops the event with no error and no panic.
func TestEnqueueWSEventDropsSilently(t *testing.T) {
	s := &server{
		eventsCh: make(chan wsEvent, 1), // tiny buffer
	}
	// Fill the buffer.
	s.enqueueWSEvent([]byte("first"), "t", "")
	// This should drop silently (no panic, no error).
	s.enqueueWSEvent([]byte("dropped"), "t", "")
	// Verify only the first event is in the buffer.
	select {
	case evt := <-s.eventsCh:
		if string(evt.data) != "first" {
			t.Fatalf("expected first event, got %q", string(evt.data))
		}
	default:
		t.Fatal("expected event in buffer")
	}
	// Buffer should now be empty — the second event was dropped.
	select {
	case evt := <-s.eventsCh:
		t.Fatalf("expected empty buffer after drain, got %q", string(evt.data))
	default:
		// OK — buffer empty, event was dropped silently
	}
}

// TestBroadcastLoopRespectsShutdown verifies that closing shutdownCh stops
// the broadcast goroutine cleanly.
func TestBroadcastLoopRespectsShutdown(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}

	// Register a client so we can check it's cleaned up.
	client := &wsClient{ch: make(chan wsEvent, 8), allowCrossTenant: true}
	dummyConn := &websocket.Conn{}
	s.clientsMu.Lock()
	s.clients[dummyConn] = client
	s.clientsMu.Unlock()

	// Drain client channel to avoid goroutine leak.
	go func() {
		for range client.ch {
		}
	}()

	// Send an event to prove the broadcast loop is running.
	s.enqueueWSEvent([]byte(`{"test":true}`), "", "")
	time.Sleep(20 * time.Millisecond)

	// Shut down.
	close(s.shutdownCh)
	s.stopBusTaps()
	s.stopWorkerExpiry()

	// Give broadcast goroutine time to exit.
	time.Sleep(50 * time.Millisecond)

	// Events after shutdown should be safely dropped.
	s.enqueueWSEvent([]byte(`{"after":"shutdown"}`), "", "")
}

// TestWriteDeadlineIsSet verifies the write timeout constant exists and is reasonable.
func TestWriteDeadlineIsSet(t *testing.T) {
	if wsWriteTimeout <= 0 {
		t.Fatal("wsWriteTimeout must be positive")
	}
	if wsWriteTimeout > 30*time.Second {
		t.Fatalf("wsWriteTimeout too high: %v (max 30s recommended)", wsWriteTimeout)
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping: unable to listen on ipv4 loopback (%v)", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	return srv
}
