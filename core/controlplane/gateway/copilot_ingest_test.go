package gateway

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/internal/testredis"
)

type recordingSender struct {
	sent   []audit.SIEMEvent
	closed bool
}

func (r *recordingSender) Send(ev audit.SIEMEvent) { r.sent = append(r.sent, ev) }
func (r *recordingSender) Close() error            { r.closed = true; return nil }

func newIngestTestRig(t *testing.T) (*copilotIngestSender, *recordingSender, *copilot.RedisStore) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	store := copilot.NewRedisStore(testredis.NewClient(t, srv.Addr()))
	inner := &recordingSender{}
	dec := newCopilotIngestSender(inner, store).(*copilotIngestSender)
	return dec, inner, store
}

func TestCopilotIngest_AppendsOnInvocationWithSession(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-1",
		Decision:  "allow",
		Extra: map[string]string{
			"copilot_session_id": "sess-1",
			"tool_name":          "cordum_list_jobs",
			"result_type":        "ok",
			"args_redacted":      `{"status":"running"}`,
		},
	})
	// Inner sender always receives the event (chain unaffected).
	if len(inner.sent) != 1 {
		t.Fatalf("inner.sent = %d, want 1", len(inner.sent))
	}
	sess, err := store.GetSession(context.Background(), "tenant-a", "sess-1", "copilot-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(sess.Messages) != 1 || sess.Messages[0].Role != "tool" {
		t.Fatalf("messages = %+v, want 1 tool message", sess.Messages)
	}
}

func TestCopilotIngest_IgnoresWhenNoSessionID(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-1",
		Extra:     map[string]string{"tool_name": "cordum_list_jobs"},
	})
	if len(inner.sent) != 1 {
		t.Fatalf("inner.sent = %d, want 1 (always forwarded)", len(inner.sent))
	}
	if _, err := store.GetSession(context.Background(), "tenant-a", "sess-x", "copilot-1"); err == nil {
		t.Fatal("expected no session written when session id absent")
	}
}

func TestCopilotIngest_IgnoresNonInvocationEvents(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolOutboundInvocation, // outbound, not transcript
		TenantID:  "tenant-a",
		AgentID:   "copilot-1",
		Extra:     map[string]string{"copilot_session_id": "sess-2", "tool_name": "x"},
	})
	if len(inner.sent) != 1 {
		t.Fatalf("inner.sent = %d, want 1", len(inner.sent))
	}
	if _, err := store.GetSession(context.Background(), "tenant-a", "sess-2", "copilot-1"); err == nil {
		t.Fatal("outbound event must not create a transcript message")
	}
}

func TestCopilotIngest_NilStoreReturnsInner(t *testing.T) {
	inner := &recordingSender{}
	if got := newCopilotIngestSender(inner, nil); got != audit.AuditSender(inner) {
		t.Fatal("nil store should return the inner sender unchanged")
	}
}
