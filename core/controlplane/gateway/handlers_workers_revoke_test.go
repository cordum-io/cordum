package gateway

// Tests for POST /api/v1/workers/{id}/revoke-session. Real issuer +
// miniredis + real AuthContext — no mocks.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// alwaysRejectAuth returns 403 from RequireRole so the test can verify
// that the handler delegates to the auth provider rather than enforcing
// role membership inline.
type alwaysRejectAuth struct{}

func (alwaysRejectAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	return nil, nil
}
func (alwaysRejectAuth) AuthenticateGRPC(ctx context.Context) (*auth.AuthContext, error) {
	return nil, nil
}
func (alwaysRejectAuth) RequireRole(r *http.Request, roles ...string) error {
	return errors.New("test: role denied")
}
func (alwaysRejectAuth) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	return "default", nil
}
func (alwaysRejectAuth) RequireTenantAccess(r *http.Request, tenant string) error { return nil }
func (alwaysRejectAuth) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	return "anon", nil
}

func newPacketWithToken(t *testing.T, token string) *pb.BusPacket {
	t.Helper()
	packet := &pb.BusPacket{}
	raw := packet.ProtoReflect().GetUnknown()
	buf := make([]byte, 0, len(raw)+len(token)+8)
	buf = append(buf, raw...)
	buf = protowire.AppendTag(buf, 18, protowire.BytesType)
	buf = protowire.AppendString(buf, token)
	packet.ProtoReflect().SetUnknown(buf)
	return packet
}

func wireSessionIssuer(t *testing.T, s *server) *scheduler.SessionTokenIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", pub); err != nil {
		t.Fatalf("trust: %v", err)
	}
	issuer, err := scheduler.NewSessionTokenIssuer(priv, "primary", trust, s.jobStore.Client(), scheduler.SessionTokenIssuerOptions{})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	s.WithSessionIssuer(issuer)
	return issuer
}

func TestHandleRevokeWorkerSession_AdminHappyPath(t *testing.T) {
	s, _, _ := newTestGateway(t)
	issuer := wireSessionIssuer(t, s)

	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "w-revoke", "default", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/w-revoke/revoke-session", nil))
	req.SetPathValue("id", "w-revoke")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["worker_id"] != "w-revoke" || resp["tenant"] != "default" || resp["revoked"] != true {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Verify the session is now actually revoked by attempting to
	// verify the old token — issuer.Verify(checkActive=true) must
	// return ErrSessionTokenRevoked.
	_, _, _ = claims, issuer, ctx // silence unused if we short-circuit below
	resolver := scheduler.NewTrustResolver(s.jobStore.Client())
	state, err := resolver.ResolveTrust(ctx, "w-revoke")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if state.SessionValid || state.RevokedAt == nil {
		t.Fatalf("post-revoke state: %+v", state)
	}
	if state.Reason != scheduler.TrustReasonRevoked {
		t.Fatalf("reason=%q want %q", state.Reason, scheduler.TrustReasonRevoked)
	}
}

func TestHandleRevokeWorkerSession_RegisteredRoute(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	for _, route := range s.routeTable {
		if route.Method == http.MethodPost && route.Path == "/api/v1/workers/{id}/revoke-session" {
			return
		}
	}
	t.Fatalf("POST /api/v1/workers/{id}/revoke-session route is not registered; routes=%+v", s.routeTable)
}

func TestHandleRevokeWorkerSession_NoActiveSessionIsOk(t *testing.T) {
	// Revoking a worker that has never handshook is a no-op success —
	// operator scripts can retry safely.
	s, _, _ := newTestGateway(t)
	_ = wireSessionIssuer(t, s)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/ghost/revoke-session", nil))
	req.SetPathValue("id", "ghost")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRevokeWorkerSession_CallsRequireRole(t *testing.T) {
	// Admin gating is enforced by s.auth.RequireRole (tested in
	// auth_regression_test.go); when s.auth is nil in newTestGateway
	// requireRole returns nil so the handler proceeds. Here we assert
	// the handler DOES route through requireRole by passing a
	// synthesised auth provider that always rejects.
	s, _, _ := newTestGateway(t)
	_ = wireSessionIssuer(t, s)
	s.auth = &alwaysRejectAuth{}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/w/revoke-session", nil))
	req.SetPathValue("id", "w")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 when auth rejects", rec.Code)
	}
}

func TestHandleRevokeWorkerSession_EmptyIDIsBadRequest(t *testing.T) {
	s, _, _ := newTestGateway(t)
	_ = wireSessionIssuer(t, s)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers//revoke-session", nil))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestHandleRevokeWorkerSession_NoIssuerIs503(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Intentionally do NOT wire sessionIssuer.

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/w/revoke-session", nil))
	req.SetPathValue("id", "w")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 when issuer unwired", rec.Code)
	}
}

// recordingAuditSender captures SIEMEvents for shape assertions.
type recordingAuditSender struct {
	events []audit.SIEMEvent
}

func (r *recordingAuditSender) Send(ev audit.SIEMEvent) {
	r.events = append(r.events, ev)
}

func (r *recordingAuditSender) Close() error { return nil }

func TestHandleRevokeWorkerSession_EmitsBothAuditEvents(t *testing.T) {
	// Revoke must emit BOTH worker_handshake (outcome=revoked) AND
	// worker_trust_change so SIEM rules can correlate on either
	// event type independently.
	s, _, _ := newTestGateway(t)
	issuer := wireSessionIssuer(t, s)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w-audit", "default", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/w-audit/revoke-session", nil))
	req.SetPathValue("id", "w-audit")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	foundHandshake, foundTrust := false, false
	for _, ev := range sink.events {
		switch ev.EventType {
		case scheduler.EventWorkerHandshake:
			foundHandshake = true
			if ev.AgentID != "w-audit" || ev.TenantID != "default" {
				t.Errorf("handshake event identity mismatch: %+v", ev)
			}
			if ev.Extra["outcome"] != "revoked" {
				t.Errorf("handshake outcome=%q want revoked", ev.Extra["outcome"])
			}
			if ev.Severity != audit.SeverityHigh {
				t.Errorf("handshake severity=%q want HIGH", ev.Severity)
			}
		case audit.EventWorkerTrustChange:
			foundTrust = true
			if ev.Extra["from"] != "valid" || ev.Extra["to"] != "revoked" {
				t.Errorf("trust-change transition wrong: %+v", ev.Extra)
			}
			if ev.Extra["actor"] == "" {
				t.Error("trust-change must carry actor")
			}
		}
	}
	if !foundHandshake {
		t.Errorf("expected worker_handshake outcome=revoked event; got %d events", len(sink.events))
	}
	if !foundTrust {
		t.Errorf("expected worker_trust_change event; got %d events", len(sink.events))
	}
}

func TestHandleRevokeWorkerSession_DispatchRefusedAfterRevoke(t *testing.T) {
	// End-to-end: issue a token, revoke it via the endpoint, and
	// assert the session-token middleware rejects a subsequent
	// packet carrying that token.
	s, _, _ := newTestGateway(t)
	issuer := wireSessionIssuer(t, s)

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "w-flow", "default", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/w-flow/revoke-session", nil))
	req.SetPathValue("id", "w-flow")
	rec := httptest.NewRecorder()
	s.handleRevokeWorkerSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d", rec.Code)
	}

	mw := scheduler.NewSessionTokenMiddleware(issuer, scheduler.HandshakeModeEnforce, scheduler.NewHandshakeMissingTracker())
	packet := newPacketWithToken(t, token)
	res := mw.Verify(ctx, "w-flow", packet)
	if res.Verdict != scheduler.TokenVerdictRejectInvalid {
		t.Fatalf("post-revoke verify verdict=%v want reject_invalid", res.Verdict)
	}
}
