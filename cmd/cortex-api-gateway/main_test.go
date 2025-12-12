package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

func TestHandleStreamUpgradesWebsocketWithInstrumentation(t *testing.T) {
	s := &server{
		clients:  make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh: make(chan *pb.BusPacket, 1),
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

	// Give the handler a moment to observe the close and exit cleanly.
	time.Sleep(20 * time.Millisecond)
}

// newIPv4Server ensures tests work in sandboxes without IPv6 listeners.
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
