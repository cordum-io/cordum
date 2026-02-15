package gateway

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

// TestGRPCGracefulStopTimeout verifies that the gRPC shutdown pattern falls
// back to Stop() when GracefulStop() exceeds the timeout.
func TestGRPCGracefulStopTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	go func() { _ = grpcServer.Serve(lis) }()

	// Simulate the shutdown pattern from gateway.go with a very short timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()

	select {
	case <-grpcDone:
		// GracefulStop completed within timeout — OK
	case <-shutdownCtx.Done():
		grpcServer.Stop()
		// Should not hang after Stop()
		select {
		case <-grpcDone:
		case <-time.After(5 * time.Second):
			t.Fatal("grpcServer.Stop() did not unblock GracefulStop")
		}
	}
}

// TestSlowWSClientEviction verifies that a slow WebSocket client is evicted
// by closing its channel, causing the handler goroutine to exit cleanly.
func TestSlowWSClientEviction(t *testing.T) {
	s := &server{
		clients:    make(map[*websocket.Conn]*wsClient),
		eventsCh:   make(chan wsEvent, 512),
		shutdownCh: make(chan struct{}),
	}

	// Start the broadcast loop (via startBusTaps-like inline goroutine).
	go func() {
		for {
			select {
			case evt, ok := <-s.eventsCh:
				if !ok {
					return
				}
				var slowClients []*websocket.Conn
				s.clientsMu.Lock()
				for conn, client := range s.clients {
					if client == nil {
						continue
					}
					select {
					case client.ch <- evt:
					default:
						slowClients = append(slowClients, conn)
					}
				}
				for _, conn := range slowClients {
					if client := s.clients[conn]; client != nil {
						client.closeChannel()
					}
					delete(s.clients, conn)
				}
				s.clientsMu.Unlock()
			case <-s.shutdownCh:
				return
			}
		}
	}()

	// Register a slow client directly (no actual WS needed for this unit test).
	// Buffer of 1 means the second event will overflow and trigger eviction.
	slowConn := &websocket.Conn{} // unique pointer, not used for I/O
	client := &wsClient{ch: make(chan wsEvent, 1), allowCrossTenant: true}
	s.clientsMu.Lock()
	s.clients[slowConn] = client
	s.clientsMu.Unlock()

	// Simulate a handler goroutine blocked on channel read.
	handlerExited := make(chan struct{})
	go func() {
		defer close(handlerExited)
		for {
			_, ok := <-client.ch
			if !ok {
				return // channel closed — clean exit
			}
			// Simulate slow processing — don't drain fast.
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Send events to fill the buffer and trigger eviction.
	// First event fills buffer (size 1), second overflows → slow client.
	s.eventsCh <- wsEvent{data: []byte("event-1"), tenant: ""}
	time.Sleep(20 * time.Millisecond)
	// Handler is now processing event-1 (sleeping 500ms), buffer is empty
	// but handler is blocked — next event fills buffer again.
	s.eventsCh <- wsEvent{data: []byte("event-2"), tenant: ""}
	time.Sleep(20 * time.Millisecond)
	// Buffer is full (handler still sleeping), this one triggers eviction.
	s.eventsCh <- wsEvent{data: []byte("event-3"), tenant: ""}
	time.Sleep(50 * time.Millisecond)

	// Verify client was evicted from map.
	s.clientsMu.RLock()
	_, exists := s.clients[slowConn]
	s.clientsMu.RUnlock()
	if exists {
		t.Fatal("slow client was not evicted from broadcast loop")
	}

	// Verify handler goroutine exits cleanly (channel was closed).
	select {
	case <-handlerExited:
		// Handler exited cleanly.
	case <-time.After(5 * time.Second):
		t.Fatal("handler goroutine did not exit after channel close")
	}

	// Clean up.
	close(s.shutdownCh)
}

// TestSafetyConnInServerClose verifies that safetyConn is closed by s.Close().
func TestSafetyConnInServerClose(t *testing.T) {
	// Create a test gRPC server to get a real connection.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	s := &server{
		clients:    make(map[*websocket.Conn]*wsClient),
		eventsCh:   make(chan wsEvent, 1),
		safetyConn: conn,
		shutdownCh: make(chan struct{}),
	}

	// Close should not panic and should close safetyConn.
	s.Close()

	// Verify connection state — GetState returns Shutdown after close.
	state := conn.GetState().String()
	if state != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN state after Close, got %s", state)
	}
}

// TestShutdownDoneWaitsForDrain verifies that the shutdownDone channel pattern
// blocks the caller until shutdown completes.
func TestShutdownDoneWaitsForDrain(t *testing.T) {
	shutdownDone := make(chan struct{})
	var drainCompleted bool
	var mu sync.Mutex

	// Simulate the shutdown goroutine.
	go func() {
		defer close(shutdownDone)
		// Simulate drain work.
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		drainCompleted = true
		mu.Unlock()
	}()

	// Wait for shutdown to complete (like RunWithAuth does).
	<-shutdownDone

	mu.Lock()
	if !drainCompleted {
		t.Fatal("expected drain to complete before shutdownDone unblocks")
	}
	mu.Unlock()
}
