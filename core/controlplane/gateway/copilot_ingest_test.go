package gateway

import (
	"context"
	"strings"
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

func TestCopilotIngest_SamePrincipalSecondAppendSucceeds(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	for i, sess := range []string{"call-1", "call-2"} {
		dec.Send(audit.SIEMEvent{
			Timestamp: time.Now().UTC(),
			EventType: audit.EventMCPToolInvocation,
			TenantID:  "tenant-a",
			AgentID:   "copilot-1",
			Extra: map[string]string{
				"copilot_session_id": "sess-legit",
				"tool_name":          sess,
			},
		})
		if len(inner.sent) != i+1 {
			t.Fatalf("inner.sent = %d, want %d", len(inner.sent), i+1)
		}
	}
	got, err := store.GetSession(context.Background(), "tenant-a", "sess-legit", "copilot-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (both from the same principal)", len(got.Messages))
	}
}

func TestCopilotIngest_CrossPrincipalAppendRejected(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	// alice's agent (copilot-alice) creates the session first.
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-alice",
		Extra: map[string]string{
			"copilot_session_id": "sess-victim",
			"tool_name":          "cordum_list_jobs",
		},
	})
	// A different agent within the same tenant guesses/sets the same
	// X-Copilot-Session-Id header and tries to inject a transcript entry.
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-mallory",
		Extra: map[string]string{
			"copilot_session_id": "sess-victim",
			"tool_name":          "fabricated_call",
		},
	})
	// The event is still forwarded to the inner sender either way — ingest
	// never affects the audit chain.
	if len(inner.sent) != 2 {
		t.Fatalf("inner.sent = %d, want 2", len(inner.sent))
	}
	sess, err := store.GetSession(context.Background(), "tenant-a", "sess-victim", "copilot-alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("messages = %+v, want 1 (mallory's append must be rejected)", sess.Messages)
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, "fabricated_call") {
			t.Fatalf("fabricated message from a different principal was stored: %+v", m)
		}
	}
}

func TestCopilotIngest_MalformedSessionIDRejectedOnWrite(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-1",
		Extra: map[string]string{
			// Contains a space and a slash — fails
			// ^[A-Za-z0-9_-]{1,128}$, unlike the read path's regex which
			// would already reject this at the HTTP handler.
			"copilot_session_id": "not a valid/session id",
			"tool_name":          "cordum_list_jobs",
		},
	})
	if len(inner.sent) != 1 {
		t.Fatalf("inner.sent = %d, want 1 (always forwarded)", len(inner.sent))
	}
	if _, err := store.GetSession(context.Background(), "tenant-a", "not a valid/session id", "copilot-1"); err == nil {
		t.Fatal("expected no session written for a malformed session id")
	}
}

func TestCopilotIngest_OversizedSessionIDRejectedOnWrite(t *testing.T) {
	dec, inner, store := newIngestTestRig(t)
	oversized := strings.Repeat("a", 129) // one over the 128-char cap
	dec.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "tenant-a",
		AgentID:   "copilot-1",
		Extra: map[string]string{
			"copilot_session_id": oversized,
			"tool_name":          "cordum_list_jobs",
		},
	})
	if len(inner.sent) != 1 {
		t.Fatalf("inner.sent = %d, want 1 (always forwarded)", len(inner.sent))
	}
	if _, err := store.GetSession(context.Background(), "tenant-a", oversized, "copilot-1"); err == nil {
		t.Fatal("expected no session written for an oversized session id")
	}
}

func TestCopilotIngest_NilStoreReturnsInner(t *testing.T) {
	inner := &recordingSender{}
	if got := newCopilotIngestSender(inner, nil); got != audit.AuditSender(inner) {
		t.Fatal("nil store should return the inner sender unchanged")
	}
}
