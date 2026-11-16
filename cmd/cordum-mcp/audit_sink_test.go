package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/policysign"
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

// recordingAuditBus accepts every publish and keeps the packets.
type recordingAuditBus struct {
	mu      sync.Mutex
	packets []*pb.BusPacket
}

func (b *recordingAuditBus) Publish(_ string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.packets = append(b.packets, packet)
	return nil
}

func (*recordingAuditBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func (b *recordingAuditBus) published() []*pb.BusPacket {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*pb.BusPacket(nil), b.packets...)
}

// blankAuditPublisherEnv clears the audit publisher key variables and the
// production profile, so each case sets only what it tests.
func blankAuditPublisherEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		audit.EnvPublisherSigningKey, audit.EnvPublisherSigningKeyPath, audit.EnvPublisherSigningKeyID,
		audit.EnvPublisherDevSigningSeed, audit.EnvPublisherPublicKeyPrefix + "CORDUM_MCP",
		env.EnvMode, env.EnvProduction,
	} {
		t.Setenv(name, "")
	}
}

// TestNATSAuditSenderAttachesMCPServiceToken pins that a published packet
// carries a service token that verifies as cordum-mcp against the audit
// publisher trust.
func TestNATSAuditSenderAttachesMCPServiceToken(t *testing.T) {
	blankAuditPublisherEnv(t)
	t.Setenv(audit.EnvPublisherDevSigningSeed, "pr1-22d-test-seed")
	rec := &recordingAuditBus{}
	sender, err := newNATSAuditSender(rec, func() *audit.BufferedExporter { return nil })
	if err != nil {
		t.Fatal(err)
	}
	sender.Send(audit.SIEMEvent{EventType: "test.mcp.token"})
	packets := rec.published()
	if len(packets) != 1 {
		t.Fatalf("published packets = %d, want 1", len(packets))
	}
	trust, err := audit.LoadPublisherTrustFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	pub := trust[servicetoken.IdentityMCP]
	lookup := func(string) (ed25519.PublicKey, bool) { return pub, pub != nil }
	claims, typ, err := servicetoken.Verify(packets[0].GetAuthToken(), lookup, time.Now())
	if err != nil || typ != servicetoken.TypService || claims.Subject != servicetoken.IdentityMCP {
		t.Fatalf("token subject %q typ %q err %v, want subject %q typ %q",
			claims.Subject, typ, err, servicetoken.IdentityMCP, servicetoken.TypService)
	}
}

// TestNATSAuditSenderWithoutUsableKey pins the outcomes without a usable key.
// Production and a malformed key build no publisher and publish nothing;
// other profiles publish without a token and name the variable.
func TestNATSAuditSenderWithoutUsableKey(t *testing.T) {
	cases := []struct {
		name, mode, key string
		wantErr         error
	}{
		{"production without key", "production", "", audit.ErrPublisherKeyNotConfigured},
		{"malformed key", "", "not-a-key", policysign.ErrInvalidPrivateKey},
		{"dev without key", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blankAuditPublisherEnv(t)
			t.Setenv(env.EnvMode, tc.mode)
			t.Setenv(audit.EnvPublisherSigningKey, tc.key)
			stop := captureStderrAtWarn(t, "text")
			rec := &recordingAuditBus{}
			fallbacks := 0
			sender, err := newNATSAuditSender(rec, func() *audit.BufferedExporter { fallbacks++; return nil })
			if sender != nil {
				sender.Send(audit.SIEMEvent{EventType: "test.mcp.nokey"})
			}
			out, packets := stop(), rec.published()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || !strings.Contains(err.Error(), audit.EnvPublisherSigningKey) ||
					sender != nil || fallbacks != 0 || len(packets) != 0 {
					t.Fatalf("err %v sender %T fallbacks %d packets %d; want %v naming %s, no sender, no fallback, nothing published",
						err, sender, fallbacks, len(packets), tc.wantErr, audit.EnvPublisherSigningKey)
				}
				return
			}
			if err != nil || len(packets) != 1 || packets[0].GetAuthToken() != "" || !strings.Contains(out, audit.EnvPublisherSigningKey) {
				t.Fatalf("err %v packets %d stderr %q; want one token-less packet and a warning naming %s",
					err, len(packets), out, audit.EnvPublisherSigningKey)
			}
		})
	}
}

// TestResolveAuditSenderWithoutNATSStartsInProduction pins that the key is
// required only where a NATS publisher is built.
func TestResolveAuditSenderWithoutNATSStartsInProduction(t *testing.T) {
	blankAuditPublisherEnv(t)
	t.Setenv(env.EnvMode, "production")
	sender, err := resolveAuditSender("")
	if _, ok := sender.(stderrAuditSender); err != nil || !ok {
		t.Fatalf("resolveAuditSender(\"\") = %T, %v; want stderrAuditSender, nil", sender, err)
	}
}
