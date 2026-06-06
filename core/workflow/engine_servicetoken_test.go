package workflow

// Coverage for task-948d913b: the workflow-engine attaches a control-plane
// service token (subject "workflow-engine") to its internal cancel broadcasts
// when a minter is wired, and nothing when it is not (back-compat).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
)

func TestPublishJobCancel_AttachesWorkflowServiceToken(t *testing.T) {
	ws := newWorkflowStore(t)
	defer func() { _ = ws.Close() }()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	bus := &failNBus{} // failCount 0 -> succeeds first try and records
	engine := NewEngine(ws, bus).WithServiceTokenMinter(func() (string, error) {
		// Mirrors the runner.go production wiring.
		return servicetoken.MintService(priv, "primary", servicetoken.IdentityWorkflow, time.Now())
	})

	if err := engine.publishJobCancel("job-wf", "cancelling"); err != nil {
		t.Fatalf("publishJobCancel: %v", err)
	}
	if bus.publishedCount() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.publishedCount())
	}
	pkt := bus.published[0].packet
	tok := pkt.GetAuthToken()
	if tok == "" {
		t.Fatal("workflow cancel must carry a service token when a minter is wired")
	}
	if got := servicetoken.PeekTyp(tok); got != servicetoken.TypService {
		t.Fatalf("attached token typ = %q, want %q", got, servicetoken.TypService)
	}
	// Decode the (signed) claims to confirm the subject is the workflow
	// identity, so the scheduler's subject-binding (Subject==SenderId==
	// RequestedBy=="workflow-engine") admits it. Signature validity is covered
	// by the servicetoken and scheduler VerifyService tests.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token: %q", tok)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims servicetoken.Claims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Subject != servicetoken.IdentityWorkflow {
		t.Fatalf("service token subject = %q, want %q", claims.Subject, servicetoken.IdentityWorkflow)
	}
	if pkt.GetSenderId() != servicetoken.IdentityWorkflow {
		t.Fatalf("SenderId = %q, want %q", pkt.GetSenderId(), servicetoken.IdentityWorkflow)
	}
}

func TestPublishJobCancel_NoMinterNoToken(t *testing.T) {
	ws := newWorkflowStore(t)
	defer func() { _ = ws.Close() }()
	bus := &failNBus{}
	engine := NewEngine(ws, bus) // no minter wired (back-compat)
	if err := engine.publishJobCancel("job-nomint", "cancelling"); err != nil {
		t.Fatalf("publishJobCancel: %v", err)
	}
	if bus.publishedCount() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.publishedCount())
	}
	if tok := bus.published[0].packet.GetAuthToken(); tok != "" {
		t.Fatalf("no minter must attach no token; got %q", tok)
	}
}
