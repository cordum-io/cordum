package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startTestNATS starts an embedded NATS server for testing.
func startTestNATS(t *testing.T) (*server.Server, string) {
	t.Helper()
	opts := &server.Options{
		Port:     -1, // Random available port
		Host:     "127.0.0.1",
		NoLog:    true,
		NoSigs:   true,
		MaxPending: 64 * 1024 * 1024,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(func() { ns.Shutdown() })
	return ns, ns.ClientURL()
}

// startTestRedis starts miniredis for testing.
func startTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	t.Cleanup(func() { mr.Close() })
	return mr
}

// testNATSConn creates a NATS connection for testing.
func testNATSConn(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect to nats: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

// multiCapturePublisher captures multiple published messages for testing.
type multiCapturePublisher struct {
	mu   sync.Mutex
	msgs []capturedMsg
	err  error
}

type capturedMsg struct {
	subject string
	data    []byte
}

func (p *multiCapturePublisher) Publish(subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.msgs = append(p.msgs, capturedMsg{subject: subject, data: append([]byte(nil), data...)})
	return nil
}

func (p *multiCapturePublisher) Messages() []capturedMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capturedMsg(nil), p.msgs...)
}

func (p *multiCapturePublisher) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.msgs)
}
