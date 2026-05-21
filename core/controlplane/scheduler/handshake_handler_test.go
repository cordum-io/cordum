package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

// fakeIdentityResolver is an in-memory AgentIdentityResolver.
type fakeIdentityResolver struct {
	records map[string]*store.AgentIdentity
	err     error
}

func (f *fakeIdentityResolver) Get(_ context.Context, id string) (*store.AgentIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.records[id]
	if !ok {
		return nil, nil
	}
	return rec, nil
}

// memoryNonceStore is a thread-safe in-memory NonceStore.
type memoryNonceStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
	err  error
	now  func() time.Time
}

func newMemoryNonceStore() *memoryNonceStore {
	return &memoryNonceStore{seen: map[string]time.Time{}, now: time.Now}
}

func (m *memoryNonceStore) Claim(_ context.Context, tenant, nonce string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return false, m.err
	}
	key := tenant + ":" + nonce
	now := m.now()
	if exp, ok := m.seen[key]; ok && exp.After(now) {
		return false, nil
	}
	m.seen[key] = now.Add(ttl)
	return true, nil
}

// recordingSink captures every emitted SIEMEvent for assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (r *recordingSink) Emit(_ context.Context, ev audit.SIEMEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingSink) last() audit.SIEMEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return audit.SIEMEvent{}
	}
	return r.events[len(r.events)-1]
}

func (r *recordingSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func newHandshakeFixture(t *testing.T) (*HandshakeService, *recordingSink, *memoryNonceStore, *fakeIdentityResolver, func()) {
	t.Helper()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	identities := &fakeIdentityResolver{
		records: map[string]*store.AgentIdentity{
			"agent-001": {ID: "agent-001", Name: "alpha", Owner: "tenant-acme", Status: "active"},
		},
	}
	nonces := newMemoryNonceStore()
	sink := &recordingSink{}
	svc, err := NewHandshakeService(issuer, identities, nonces, sink, HandshakeServiceOptions{
		Skew:     30 * time.Second,
		NonceTTL: 2 * time.Minute,
	})
	if err != nil {
		cleanup()
		t.Fatalf("new handshake service: %v", err)
	}
	return svc, sink, nonces, identities, cleanup
}

func validRequestBytes(t *testing.T, overrides func(*capsdk.HandshakeRequest)) []byte {
	t.Helper()
	req := &capsdk.HandshakeRequest{
		AgentID:    "agent-001",
		Tenant:     "tenant-acme",
		SDKVersion: "v2.9.0",
		Nonce:      strings.Repeat("n", capsdk.WorkerHandshakeNonceLength),
		RequestID:  "req-1",
		Timestamp:  time.Now().UTC(),
	}
	if overrides != nil {
		overrides(req)
	}
	raw, err := capsdk.MarshalHandshakeRequest(req)
	if err != nil {
		t.Fatalf("marshal handshake req: %v", err)
	}
	return raw
}

func TestHandshakeService_HappyPath(t *testing.T) {
	t.Parallel()
	svc, sink, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, nil)
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Rejected {
		t.Fatalf("expected acceptance, got rejection: %+v", resp)
	}
	if resp.SessionToken == "" {
		t.Fatal("missing session token")
	}
	if resp.RequestID != "req-1" {
		t.Fatalf("request id echo mismatch: %q", resp.RequestID)
	}
	if resp.TokenExp.IsZero() {
		t.Fatal("missing token exp")
	}

	if sink.count() != 1 {
		t.Fatalf("expected 1 audit event, got %d", sink.count())
	}
	ev := sink.last()
	if ev.EventType != EventWorkerHandshake || ev.Decision != "accept" {
		t.Fatalf("unexpected audit: %+v", ev)
	}
	if ev.Extra["outcome"] != "accepted" {
		t.Fatalf("extra.outcome=%q", ev.Extra["outcome"])
	}
}

func TestHandshakeService_ReplayRejected(t *testing.T) {
	t.Parallel()
	svc, sink, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, nil)
	if _, err := svc.HandleHandshake(context.Background(), raw); err != nil {
		t.Fatalf("first: %v", err)
	}
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectReplay {
		t.Fatalf("expected replay rejection, got %+v", resp)
	}
	if sink.count() != 2 {
		t.Fatalf("expected 2 audit events, got %d", sink.count())
	}
	ev := sink.last()
	if ev.Extra["reason"] != capsdk.HandshakeRejectReplay {
		t.Fatalf("extra.reason=%q", ev.Extra["reason"])
	}
}

func TestHandshakeService_UnknownAgent(t *testing.T) {
	t.Parallel()
	svc, _, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, func(r *capsdk.HandshakeRequest) { r.AgentID = "ghost" })
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectUnknownAgent {
		t.Fatalf("expected unknown_agent, got %+v", resp)
	}
}

func TestHandshakeService_TenantMismatch(t *testing.T) {
	t.Parallel()
	svc, _, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, func(r *capsdk.HandshakeRequest) {
		r.Tenant = "tenant-evil"
		// New nonce so we don't accidentally hit replay.
		r.Nonce = strings.Repeat("m", capsdk.WorkerHandshakeNonceLength)
	})
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectTenantMismatch {
		t.Fatalf("expected tenant_mismatch, got %+v", resp)
	}
}

func TestHandshakeService_ClockSkew(t *testing.T) {
	t.Parallel()
	svc, _, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, func(r *capsdk.HandshakeRequest) {
		r.Timestamp = time.Now().UTC().Add(-5 * time.Minute)
	})
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectClockSkew {
		t.Fatalf("expected clock_skew, got %+v", resp)
	}
}

func TestHandshakeService_SuspendedIdentity(t *testing.T) {
	t.Parallel()
	svc, _, _, ids, cleanup := newHandshakeFixture(t)
	defer cleanup()

	ids.records["agent-001"].Status = "suspended"

	raw := validRequestBytes(t, nil)
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectCapabilityDenied {
		t.Fatalf("expected capability_denied, got %+v", resp)
	}
}

func TestHandshakeService_MalformedPayload(t *testing.T) {
	t.Parallel()
	svc, _, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	cases := [][]byte{
		nil,
		[]byte("{"),
		[]byte(`{"agent_id":""}`),
		[]byte(`{"agent_id":"a","tenant":"t","sdk_version":"v","nonce":"short","request_id":"r","timestamp":"2026-04-18T00:00:00Z"}`),
	}
	for _, raw := range cases {
		body, err := svc.HandleHandshake(context.Background(), raw)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		resp, err := capsdk.UnmarshalHandshakeResponse(body)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectMalformedRequest {
			t.Fatalf("expected malformed_request, got %+v", resp)
		}
	}
}

func TestHandshakeService_IdentityStoreFailure(t *testing.T) {
	t.Parallel()
	svc, _, _, ids, cleanup := newHandshakeFixture(t)
	defer cleanup()

	ids.err = errors.New("redis down")

	raw := validRequestBytes(t, nil)
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectInternalError {
		t.Fatalf("expected internal_error, got %+v", resp)
	}
}

func TestHandshakeService_NonceStoreFailure(t *testing.T) {
	t.Parallel()
	svc, _, nonces, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	nonces.err = errors.New("redis hiccup")

	raw := validRequestBytes(t, nil)
	body, err := svc.HandleHandshake(context.Background(), raw)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp, err := capsdk.UnmarshalHandshakeResponse(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Rejected || resp.Reason != capsdk.HandshakeRejectInternalError {
		t.Fatalf("expected internal_error, got %+v", resp)
	}
}

func TestHandshakeService_RenewEmitsRenewedOutcome(t *testing.T) {
	t.Parallel()
	svc, sink, _, _, cleanup := newHandshakeFixture(t)
	defer cleanup()

	raw := validRequestBytes(t, nil)
	if _, err := svc.HandleRenew(context.Background(), raw); err != nil {
		t.Fatalf("renew: %v", err)
	}
	ev := sink.last()
	if ev.Extra["outcome"] != "renewed" {
		t.Fatalf("expected outcome=renewed, got %q", ev.Extra["outcome"])
	}
}

func TestNewHandshakeService_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ids := &fakeIdentityResolver{records: map[string]*store.AgentIdentity{}}

	if _, err := NewHandshakeService(nil, ids, nil, nil, HandshakeServiceOptions{}); err == nil {
		t.Fatal("expected error for nil issuer")
	}
	if _, err := NewHandshakeService(issuer, nil, nil, nil, HandshakeServiceOptions{}); err == nil {
		t.Fatal("expected error for nil identity resolver")
	}
}

func TestNewHandshakeService_ExtendsNonceTTL(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ids := &fakeIdentityResolver{records: map[string]*store.AgentIdentity{}}

	svc, err := NewHandshakeService(issuer, ids, nil, nil, HandshakeServiceOptions{
		Skew:     time.Minute,
		NonceTTL: time.Second, // smaller than 2x skew
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if svc.nonceTTL < 2*svc.skew {
		t.Fatalf("nonce TTL not extended: ttl=%s skew=%s", svc.nonceTTL, svc.skew)
	}
}
