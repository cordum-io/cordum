package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/logging"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// captureStderrAtWarn routes os.Stderr through a pipe and installs the
// production logger the way logging.Init does for CORDUM_LOG_LEVEL=warn. The
// returned function restores both and returns everything written to stderr.
func captureStderrAtWarn(t *testing.T, format string) func() string {
	t.Helper()
	t.Setenv("CORDUM_LOG_LEVEL", "warn")
	t.Setenv("CORDUM_LOG_FORMAT", format)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr, origLogger := os.Stderr, slog.Default()
	os.Stderr = w
	logging.Init("mcp-server")
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, r)
		close(done)
	}()
	var once sync.Once
	stop := func() string {
		once.Do(func() {
			slog.SetDefault(origLogger)
			os.Stderr = origStderr
			_ = w.Close()
			<-done
			_ = r.Close()
		})
		return out.String()
	}
	t.Cleanup(func() { stop() })
	return stop
}

func lineContaining(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// TestStderrAuditEventsWrittenAtWarnLevel pins that both stderr audit paths
// write every event at CORDUM_LOG_LEVEL=warn, in the process log format.
func TestStderrAuditEventsWrittenAtWarnLevel(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, path := range []string{"sender", "fallback"} {
			t.Run(format+"/"+path, func(t *testing.T) {
				stop := captureStderrAtWarn(t, format)
				marker := "test.mcp.stderr." + format + "." + path
				slog.Info("diagnostic below warn")
				event := audit.SIEMEvent{EventType: marker, Action: "mcp.tool_outbound_invocation"}
				if path == "sender" {
					newStderrAuditSender().Send(event)
				} else {
					fallback := newAuditFallback(func() (*audit.BufferedExporter, error) { return nil, nil })
					fallback.Send(event)
					if err := fallback.Close(); err != nil {
						t.Fatal(err)
					}
				}
				out := stop()
				if strings.Contains(out, "diagnostic below warn") {
					t.Fatalf("logger is not at WARN; stderr: %q", out)
				}
				line := lineContaining(out, marker)
				if line == "" {
					t.Fatalf("audit event %q not on stderr at WARN; stderr: %q", marker, out)
				}
				if format == "text" {
					const prefix = "[MCP-SERVER] INFO audit.event event="
					var ev audit.SIEMEvent
					if !strings.HasPrefix(line, prefix) {
						t.Fatalf("text line = %q, want prefix %q", line, prefix)
					}
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &ev); err != nil || ev.EventType != marker {
						t.Fatalf("text line %q does not carry the event as JSON text (event_type %q, err %v)", line, ev.EventType, err)
					}
					return
				}
				var rec struct {
					Level     string          `json:"level"`
					Msg       string          `json:"msg"`
					Component string          `json:"component"`
					Event     audit.SIEMEvent `json:"event"`
				}
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatalf("json line %q: %v", line, err)
				}
				if rec.Level != "INFO" || rec.Msg != "audit.event" || rec.Component != "mcp-server" || rec.Event.EventType != marker {
					t.Fatalf("json record = %+v, want INFO audit.event from mcp-server with event_type %q", rec, marker)
				}
			})
		}
	}
}

// failingAuditBus refuses every publish, so NATSAuditPublisher hands each
// event to its fallback buffer.
type failingAuditBus struct{}

func (failingAuditBus) Publish(string, *pb.BusPacket) error {
	return errors.New("bus unavailable")
}

func (failingAuditBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

type recordingAuditExporter struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (r *recordingAuditExporter) Export(_ context.Context, events []audit.SIEMEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, events...)
	return nil
}

func (r *recordingAuditExporter) Close() error { return nil }

func (r *recordingAuditExporter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// TestAuditShutdownExportsQueuedFallbackEvent pins that an event queued in
// the fallback buffer is exported before close returns, and before exit
// calls os.Exit.
func TestAuditShutdownExportsQueuedFallbackEvent(t *testing.T) {
	queued := func(t *testing.T) (*auditShutdown, *recordingAuditExporter) {
		t.Helper()
		rec := &recordingAuditExporter{}
		// An hour-long flush interval keeps the event queued until shutdown.
		fallback := audit.NewBufferedExporter(rec, audit.WithFlushInterval(time.Hour))
		sender := audit.NewNATSAuditPublisher(failingAuditBus{}, fallback)
		sender.Send(audit.SIEMEvent{EventType: "test.mcp.shutdown"})
		if got := rec.count(); got != 0 {
			t.Fatalf("exported before shutdown = %d, want 0", got)
		}
		return &auditShutdown{sender: sender, osExit: func(code int) { t.Errorf("unexpected exit(%d)", code) }}, rec
	}
	t.Run("close", func(t *testing.T) {
		s, rec := queued(t)
		s.close()
		if got := rec.count(); got != 1 {
			t.Fatalf("exported after close = %d, want 1", got)
		}
		s.close()
		if got := rec.count(); got != 1 {
			t.Fatalf("exported after a second close = %d, want 1", got)
		}
	})
	t.Run("exit", func(t *testing.T) {
		s, rec := queued(t)
		code, exportedAtExit := -1, -1
		s.osExit = func(c int) { code, exportedAtExit = c, rec.count() }
		s.exit(1)
		if code != 1 || exportedAtExit != 1 {
			t.Fatalf("exit code %d with %d exported when os.Exit ran, want code 1 with 1 exported", code, exportedAtExit)
		}
	})
}
