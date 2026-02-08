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
	dialer := websocket.Dialer{Subprotocols: []string{wsAPIKeyProtocol, token}}
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
	req.Header.Set("Sec-WebSocket-Protocol", wsAPIKeyProtocol+", "+token)
	if got := apiKeyFromWebSocket(req); got != "secret" {
		t.Fatalf("expected secret got %q", got)
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
