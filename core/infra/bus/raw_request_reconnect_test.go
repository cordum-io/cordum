package bus

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestQueueRespondSurvivesCoreNATSReconnect(t *testing.T) {
	first := startTestNATSServer(t, false)
	port := first.Addr().(*net.TCPAddr).Port
	reconnected := make(chan struct{}, 1)
	nc, err := nats.Connect(
		first.ClientURL(),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(10*time.Millisecond),
		nats.ReconnectHandler(func(*nats.Conn) {
			select {
			case reconnected <- struct{}{}:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("connect first server: %v", err)
	}
	t.Cleanup(nc.Close)
	bus := &NatsBus{nc: nc}

	_, err = bus.QueueRespond("rpc.reconnect", "raw-responders", func(_ context.Context, request model.RawRequest) (model.RawResponse, error) {
		return append(model.RawResponse("reconnected:"), request...), nil
	})
	if err != nil {
		t.Fatalf("QueueRespond: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}

	first.Shutdown()
	first.WaitForShutdown()
	second := restartTestNATSServer(t, port)

	// IsConnected may still reflect the old connection while EOF is pending.
	// Wait for the reconnect, then acknowledge the replayed subscription.
	readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-reconnected:
	case <-readyCtx.Done():
		t.Fatalf("bus did not reconnect: %v, status=%s", readyCtx.Err(), nc.Status())
	}
	if err := nc.FlushWithContext(readyCtx); err != nil {
		t.Fatalf("flush restored subscription: %v", err)
	}

	client, err := nats.Connect(second.ClientURL())
	if err != nil {
		t.Fatalf("connect replacement client: %v", err)
	}
	t.Cleanup(client.Close)
	response, err := client.Request("rpc.reconnect", []byte("request"), time.Second)
	if err != nil {
		t.Fatalf("request after reconnect: %v", err)
	}
	if got := string(response.Data); got != "reconnected:request" {
		t.Fatalf("response = %q, want %q", got, "reconnected:request")
	}
}

func restartTestNATSServer(t *testing.T, port int) *server.Server {
	t.Helper()
	replacement, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   port,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		t.Fatalf("create replacement server: %v", err)
	}
	go replacement.Start()
	if !replacement.ReadyForConnections(5 * time.Second) {
		t.Fatal("replacement NATS server not ready")
	}
	t.Cleanup(replacement.Shutdown)
	return replacement
}
