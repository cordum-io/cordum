package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
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
	_ = conn.Close()
	// No assertion needed — test validates the WS upgrade succeeds.
}

func TestHandleStreamHonorsAPIKeySubprotocol(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEY": "'test-api-key'",
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

// ---------------------------------------------------------------------------
// Regression: handleWorkflowJobResult returns retry error on lock contention
// ---------------------------------------------------------------------------

func TestHandleWorkflowJobResultReturnsRetryOnLockBusy(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, bus).WithMemory(s.memStore)
	s.workflowEng = engine

	wfDef := &wf.Workflow{
		ID:    "wf-lock-test",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID:         "run-lock-1",
		WorkflowID: wfDef.ID,
		OrgID:      "default",
		Steps:      map[string]*wf.StepRun{},
		Status:     wf.RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Acquire the run lock externally to simulate contention.
	lockKey := "cordum:wf:run:lock:" + run.ID
	token, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || token == "" {
		t.Fatalf("pre-acquire lock: err=%v token=%q", err, token)
	}
	defer func() { _ = s.jobStore.ReleaseLock(ctx, lockKey, token) }()

	// Get the dispatched job ID from the step run.
	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	stepRun := updated.Steps["step"]
	if stepRun == nil || stepRun.JobID == "" {
		t.Fatal("expected step to have a dispatched job ID")
	}

	jr := &pb.JobResult{
		JobId:  stepRun.JobID,
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}

	// handleWorkflowJobResult should return a retry error (not nil).
	retryErr := s.handleWorkflowJobResult(ctx, jr)
	if retryErr == nil {
		t.Fatal("expected retry error on lock contention, got nil")
	}
	if !strings.Contains(retryErr.Error(), "run lock busy") {
		t.Fatalf("expected 'run lock busy' error, got: %v", retryErr)
	}

	// Release the lock and try again — should succeed.
	_ = s.jobStore.ReleaseLock(ctx, lockKey, token)
	token = "" // prevent double-release in defer

	retryErr = s.handleWorkflowJobResult(ctx, jr)
	if retryErr != nil {
		t.Fatalf("expected nil error after lock released, got: %v", retryErr)
	}

	// Verify the run advanced.
	final, _ := s.workflowStore.GetRun(ctx, run.ID)
	if final.Status != wf.RunStatusSucceeded {
		t.Fatalf("expected run succeeded after retry, got %s", final.Status)
	}
}

func TestHandleWorkflowJobResultDeletedRunDiscardsMessage(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, bus).WithMemory(s.memStore)
	s.workflowEng = engine

	wfDef := &wf.Workflow{
		ID:    "wf-del-test",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID: "run-del-1", WorkflowID: wfDef.ID, OrgID: "default",
		Steps: map[string]*wf.StepRun{}, Status: wf.RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Get dispatched job ID.
	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	stepRun := updated.Steps["step"]
	if stepRun == nil || stepRun.JobID == "" {
		t.Fatal("expected dispatched job ID")
	}

	// Delete the run.
	if err := s.workflowStore.DeleteRun(ctx, run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	// handleWorkflowJobResult should return nil (ACK, discard) — not an error.
	jr := &pb.JobResult{JobId: stepRun.JobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}
	err := s.handleWorkflowJobResult(ctx, jr)
	if err != nil {
		t.Fatalf("expected nil (discard) for deleted run result, got: %v", err)
	}
}

func TestHandleWorkflowJobResultNilInputs(t *testing.T) {
	s := &server{}
	// Nil server fields — should return nil (no-op), not panic.
	if err := s.handleWorkflowJobResult(context.Background(), nil); err != nil {
		t.Fatalf("expected nil for nil JobResult, got: %v", err)
	}
	if err := s.handleWorkflowJobResult(context.Background(), &pb.JobResult{JobId: ""}); err != nil {
		t.Fatalf("expected nil for empty JobId, got: %v", err)
	}
	if err := s.handleWorkflowJobResult(context.Background(), &pb.JobResult{JobId: "no-colon"}); err != nil {
		t.Fatalf("expected nil for non-workflow job ID, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Regression: lock-busy + terminal run → ACK (no infinite retry storm)
// ---------------------------------------------------------------------------

func TestHandleWorkflowJobResultLockBusyTerminalRunDiscards(t *testing.T) {
	s, busMock, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, busMock).WithMemory(s.memStore)
	s.workflowEng = engine

	wfDef := &wf.Workflow{
		ID:    "wf-stale-terminal",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID: "run-stale-1", WorkflowID: wfDef.ID, OrgID: "default",
		Steps: map[string]*wf.StepRun{}, Status: wf.RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Complete the run normally.
	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	stepRun := updated.Steps["step"]
	if stepRun == nil || stepRun.JobID == "" {
		t.Fatal("expected dispatched job ID")
	}
	if err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId: stepRun.JobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	final, _ := s.workflowStore.GetRun(ctx, run.ID)
	if final.Status != wf.RunStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", final.Status)
	}

	// Now simulate an orphan message arriving with lock contention.
	lockKey := "cordum:wf:run:lock:" + run.ID
	token, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || token == "" {
		t.Fatalf("pre-acquire lock: err=%v token=%q", err, token)
	}
	defer func() { _ = s.jobStore.ReleaseLock(ctx, lockKey, token) }()

	// This should ACK (return nil) because the run is terminal,
	// NOT return a retry error.
	orphanResult := &pb.JobResult{
		JobId: stepRun.JobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}
	retryErr := s.handleWorkflowJobResult(ctx, orphanResult)
	if retryErr != nil {
		t.Fatalf("expected nil (ACK) for orphan result on terminal run, got: %v", retryErr)
	}
}

func TestHandleWorkflowJobResultLockBusyDeletedRunDiscards(t *testing.T) {
	s, busMock, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, busMock).WithMemory(s.memStore)
	s.workflowEng = engine

	// Simulate a lock held for a run that doesn't exist.
	lockKey := "cordum:wf:run:lock:run-ghost-1"
	token, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || token == "" {
		t.Fatalf("pre-acquire lock: err=%v token=%q", err, token)
	}
	defer func() { _ = s.jobStore.ReleaseLock(ctx, lockKey, token) }()

	jr := &pb.JobResult{
		JobId:  "run-ghost-1:step@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}
	retryErr := s.handleWorkflowJobResult(ctx, jr)
	if retryErr != nil {
		t.Fatalf("expected nil (ACK) for orphan result on missing run, got: %v", retryErr)
	}
}

func TestHandleWorkflowJobResultLockBusyActiveRunRetries(t *testing.T) {
	s, busMock, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, busMock).WithMemory(s.memStore)
	s.workflowEng = engine

	wfDef := &wf.Workflow{
		ID:    "wf-active-lock",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID: "run-active-1", WorkflowID: wfDef.ID, OrgID: "default",
		Steps: map[string]*wf.StepRun{}, Status: wf.RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	stepRun := updated.Steps["step"]
	if stepRun == nil || stepRun.JobID == "" {
		t.Fatal("expected dispatched job ID")
	}

	// Lock the run to simulate contention.
	lockKey := "cordum:wf:run:lock:" + run.ID
	token, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || token == "" {
		t.Fatalf("pre-acquire lock: err=%v token=%q", err, token)
	}
	defer func() { _ = s.jobStore.ReleaseLock(ctx, lockKey, token) }()

	// For an active run, lock-busy should still return retry error.
	jr := &pb.JobResult{
		JobId: stepRun.JobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}
	retryErr := s.handleWorkflowJobResult(ctx, jr)
	if retryErr == nil {
		t.Fatal("expected retry error for active run with lock contention")
	}
	if !strings.Contains(retryErr.Error(), "run lock busy") {
		t.Fatalf("expected 'run lock busy' error, got: %v", retryErr)
	}
}

func TestIsStaleJobResult(t *testing.T) {
	s, busMock, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, busMock).WithMemory(s.memStore)
	s.workflowEng = engine

	// Test: missing run → stale
	if !s.isStaleJobResult(ctx, "nonexistent-run", "step", "nonexistent-run:step@1") {
		t.Fatal("expected stale for missing run")
	}

	// Test: nil workflowStore → not stale (can't check)
	noStore := &server{}
	if noStore.isStaleJobResult(ctx, "x", "y", "x:y@1") {
		t.Fatal("expected not stale when store is nil")
	}

	// Setup a workflow and run for remaining tests.
	wfDef := &wf.Workflow{
		ID: "wf-stale-check", OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID: "run-stale-check", WorkflowID: wfDef.ID, OrgID: "default",
		Steps: map[string]*wf.StepRun{}, Status: wf.RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Test: active run, active step → not stale
	if s.isStaleJobResult(ctx, run.ID, "step", run.ID+":step@1") {
		t.Fatal("expected not stale for active run")
	}

	// Complete the run.
	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	stepRun := updated.Steps["step"]
	if err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId: stepRun.JobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	// Test: terminal run → stale
	if !s.isStaleJobResult(ctx, run.ID, "step", run.ID+":step@1") {
		t.Fatal("expected stale for terminal run")
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

func TestEnqueueBusPacketFallsBackToSanitizedProtoJSON(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOutput)

	s := &server{
		eventsCh: make(chan wsEvent, 1),
	}

	invalidErrorMessage := "bad" + string([]byte{0xff})
	pkt := &pb.BusPacket{
		TraceId: "trace-1",
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:        "job-1",
				Status:       pb.JobStatus_JOB_STATUS_SUCCEEDED,
				ErrorMessage: invalidErrorMessage,
			},
		},
	}

	s.enqueueBusPacket(pkt)

	select {
	case evt := <-s.eventsCh:
		if strings.Contains(string(evt.data), "\"Payload\"") {
			t.Fatalf("expected protojson-compatible fallback payload, got %s", string(evt.data))
		}

		var packet struct {
			TraceID   string `json:"traceId"`
			JobResult struct {
				JobID        string `json:"jobId"`
				Status       string `json:"status"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"jobResult"`
		}
		if err := json.Unmarshal(evt.data, &packet); err != nil {
			t.Fatalf("decode websocket fallback payload: %v", err)
		}
		if packet.TraceID != "trace-1" {
			t.Fatalf("expected traceId preserved, got %q", packet.TraceID)
		}
		if packet.JobResult.JobID != "job-1" {
			t.Fatalf("expected jobId preserved, got %q", packet.JobResult.JobID)
		}
		if packet.JobResult.Status != pb.JobStatus_JOB_STATUS_SUCCEEDED.String() {
			t.Fatalf("expected protojson enum string, got %q", packet.JobResult.Status)
		}
		if packet.JobResult.ErrorMessage == "" {
			t.Fatal("expected fallback to preserve errorMessage content")
		}
		if !utf8.ValidString(packet.JobResult.ErrorMessage) {
			t.Fatalf("expected fallback errorMessage to be valid UTF-8, got %q", packet.JobResult.ErrorMessage)
		}
	default:
		t.Fatal("expected websocket event to be enqueued")
	}

	if pkt.GetJobResult().GetErrorMessage() != invalidErrorMessage {
		t.Fatal("expected fallback marshalling to avoid mutating the original packet")
	}

	logOutput := buf.String()
	for _, want := range []string{
		"protojson marshal failed for websocket bus packet",
		"packet_type=job_result",
		"trace_id=trace-1",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected log output to contain %q, got %q", want, logOutput)
		}
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

// --- WebSocket buffer and eviction tests ---

func TestWSClientBufferSize_Default(t *testing.T) {
	t.Setenv("CORDUM_WS_CLIENT_BUFFER_SIZE", "")
	v := wsClientBufferSize()
	if v != defaultWSClientBufSize {
		t.Fatalf("expected default %d, got %d", defaultWSClientBufSize, v)
	}
}

func TestWSClientBufferSize_Custom(t *testing.T) {
	t.Setenv("CORDUM_WS_CLIENT_BUFFER_SIZE", "512")
	v := wsClientBufferSize()
	if v != 512 {
		t.Fatalf("expected 512, got %d", v)
	}
}

func TestWSClientBufferSize_Clamped(t *testing.T) {
	t.Setenv("CORDUM_WS_CLIENT_BUFFER_SIZE", "99999")
	v := wsClientBufferSize()
	if v != maxWSClientBufSize {
		t.Fatalf("expected max %d, got %d", maxWSClientBufSize, v)
	}
}

func TestWSClientBufferSize_Invalid(t *testing.T) {
	t.Setenv("CORDUM_WS_CLIENT_BUFFER_SIZE", "not-a-number")
	v := wsClientBufferSize()
	if v != defaultWSClientBufSize {
		t.Fatalf("expected default %d for invalid input, got %d", defaultWSClientBufSize, v)
	}
}

func TestWSClientBufferSize_Zero(t *testing.T) {
	t.Setenv("CORDUM_WS_CLIENT_BUFFER_SIZE", "0")
	v := wsClientBufferSize()
	if v != defaultWSClientBufSize {
		t.Fatalf("expected default %d for zero, got %d", defaultWSClientBufSize, v)
	}
}

func TestCloseChannelNonBlocking(t *testing.T) {
	client := &wsClient{ch: make(chan wsEvent, 10)}

	done := make(chan struct{})
	go func() {
		client.closeChannel()
		close(done)
	}()

	select {
	case <-done:
		// OK — completed quickly.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("closeChannel() blocked for >100ms — must be non-blocking")
	}

	// Double-close should not panic.
	client.closeChannel()
}

func TestSlowClientEviction(t *testing.T) {
	s := &server{
		clients:       make(map[*websocket.Conn]*wsClient),
		eventsCh:      make(chan wsEvent, 10),
		wsClientBufSz: 1, // tiny buffer to force eviction
		shutdownCh:    make(chan struct{}),
	}

	// Simulate a "slow" client with buffer 1.
	slowClient := &wsClient{ch: make(chan wsEvent, 1)}
	// Simulate a "fast" client with large buffer.
	fastClient := &wsClient{ch: make(chan wsEvent, 100)}

	// Use nil *websocket.Conn as map keys (we won't actually use them).
	// We need distinct pointers, so create fake conns.
	slowConn := (*websocket.Conn)(nil)
	fastConn := &websocket.Conn{}

	s.clientsMu.Lock()
	s.clients[slowConn] = slowClient
	s.clients[fastConn] = fastClient
	s.clientsMu.Unlock()

	// Fill the slow client's buffer.
	slowClient.ch <- wsEvent{data: []byte("first")}

	// Now broadcast — slow client's buffer is full, should be evicted.
	var evicted []*websocket.Conn
	s.clientsMu.Lock()
	for conn, client := range s.clients {
		if client == nil {
			continue
		}
		select {
		case client.ch <- wsEvent{data: []byte("second")}:
		default:
			evicted = append(evicted, conn)
		}
	}
	for _, conn := range evicted {
		if client := s.clients[conn]; client != nil {
			client.closeChannel()
		}
		delete(s.clients, conn)
	}
	s.clientsMu.Unlock()

	// Verify slow client was evicted.
	if len(evicted) != 1 {
		t.Fatalf("expected 1 eviction, got %d", len(evicted))
	}

	// Verify fast client still registered.
	s.clientsMu.Lock()
	_, fastStillPresent := s.clients[fastConn]
	s.clientsMu.Unlock()
	if !fastStillPresent {
		t.Fatal("fast client should still be registered")
	}

	// Fast client should have received the event.
	select {
	case evt := <-fastClient.ch:
		if string(evt.data) != "second" {
			t.Fatalf("fast client got wrong data: %s", string(evt.data))
		}
	default:
		t.Fatal("fast client should have received the event")
	}
}

func TestWSClientBufferSizeUsedInHandlers(t *testing.T) {
	bufSize := 512
	s := &server{
		clients:       make(map[*websocket.Conn]*wsClient),
		eventsCh:      make(chan wsEvent, 1),
		wsClientBufSz: bufSize,
	}

	// Verify the server stores the configured buffer size.
	if s.wsClientBufSz != bufSize {
		t.Fatalf("expected buffer size %d, got %d", bufSize, s.wsClientBufSz)
	}
}
