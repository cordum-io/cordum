package gateway

import "testing"

func TestStartHTTPServerInvalidAddr(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := startHTTPServer(s, "127.0.0.1:-1", "127.0.0.1:-2"); err == nil {
		t.Fatalf("expected listen error")
	}
}
