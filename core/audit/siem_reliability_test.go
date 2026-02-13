package audit

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ---- Syslog reconnect past Close() error ----

// brokenConn simulates a connection where Close() returns an error.
type brokenConn struct {
	net.Conn
}

func (c *brokenConn) Close() error {
	return errors.New("connection reset by peer")
}

func (c *brokenConn) SetWriteDeadline(_ time.Time) error {
	return errors.New("broken")
}

func TestSyslogReconnectPastCloseError(t *testing.T) {
	// Start a real listener for the new connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	received := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		if n > 0 {
			received <- string(buf[:n])
		}
	}()

	exp := &SyslogExporter{
		network:  "tcp",
		address:  ln.Addr().String(),
		facility: FacilityLocal0,
		appName:  "cordum",
		hostname: "test",
		conn:     &brokenConn{}, // broken conn that fails Close()
	}

	// connect() should succeed despite Close() error on old connection.
	if err := exp.connect(); err != nil {
		t.Fatalf("connect() should succeed past Close error, got: %v", err)
	}
	if exp.conn == nil {
		t.Fatal("expected new connection to be established")
	}

	// Verify the new connection works by sending a message.
	ev := SIEMEvent{
		Timestamp: time.Now().UTC(),
		Severity:  SeverityInfo,
		Action:    "reconnect-verify",
	}
	if err := exp.Export(context.Background(), []SIEMEvent{ev}); err != nil {
		t.Fatalf("Export after reconnect: %v", err)
	}

	select {
	case msg := <-received:
		if !strings.Contains(msg, "reconnect-verify") {
			t.Errorf("expected 'reconnect-verify' in message, got: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message after reconnect")
	}
}

// ---- Batch drop prometheus metric ----

// alwaysFailExporter fails every Export call.
type alwaysFailExporter struct{}

func (e *alwaysFailExporter) Export(_ context.Context, _ []SIEMEvent) error {
	return errors.New("permanent failure")
}

func (e *alwaysFailExporter) Close() error { return nil }

func TestBufferedExporterBatchDropMetric(t *testing.T) {
	// Reset the counter for a clean test.
	before := testutil.ToFloat64(auditBatchDrops)

	mock := &alwaysFailExporter{}
	buf := &BufferedExporter{exporter: mock}

	ctx := context.Background()
	buf.exportWithRetry(ctx, []SIEMEvent{{Action: "drop-test"}})

	after := testutil.ToFloat64(auditBatchDrops)
	if after-before != 1 {
		t.Fatalf("expected auditBatchDrops to increment by 1, got delta=%f", after-before)
	}
}

// ---- CloudWatch skips events on marshal failure ----

func TestCloudWatchExporterEmptyBatchSkipped(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	called := false
	// If Export is called with empty events after filtering, it returns nil early.
	exp, err := NewCloudWatchExporter("group", "stream",
		WithCloudWatchEndpoint("http://should-not-be-called.invalid"))
	if err != nil {
		t.Fatalf("NewCloudWatchExporter: %v", err)
	}

	// Empty batch should return nil without making HTTP request.
	err = exp.Export(context.Background(), []SIEMEvent{})
	if err != nil {
		t.Fatalf("Export empty batch: %v", err)
	}
	if called {
		t.Fatal("HTTP client should not be called for empty batch")
	}
}

// ---- Datadog skips events on marshal failure ----

func TestDatadogExporterEmptyBatchSkipped(t *testing.T) {
	exp := NewDatadogExporter("test-key")
	exp.endpoint = "http://should-not-be-called.invalid"

	// Empty batch should return nil without making HTTP request.
	err := exp.Export(context.Background(), []SIEMEvent{})
	if err != nil {
		t.Fatalf("Export empty batch: %v", err)
	}
}
