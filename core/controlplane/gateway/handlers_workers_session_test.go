package gateway

// /api/v1/workers session-authority response shape. Every test drives
// the real SessionTokenIssuer / TrustResolver / MemoryRegistry /
// miniredis pipeline — no mocks. See
// core/controlplane/scheduler/trust_state.go for the authoritative
// trust contract and docs/internal/heartbeat-demotion-audit.md for the
// migration blast radius.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// newTrustResolverForTest wires a real SessionTokenIssuer +
// TrustResolver onto the gateway's existing Redis client so the
// session-token store and trust resolver share the same backing
// state — exactly like production.
func newTrustResolverForTest(t *testing.T, s *server) (*scheduler.SessionTokenIssuer, *scheduler.TrustResolver) {
	t.Helper()
	if s.jobStore == nil {
		t.Fatal("test gateway must have a jobStore")
	}
	client := s.jobStore.Client()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", pub); err != nil {
		t.Fatalf("trust add: %v", err)
	}
	issuer, err := scheduler.NewSessionTokenIssuer(priv, "primary", trust, client, scheduler.SessionTokenIssuerOptions{})
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	resolver := scheduler.NewTrustResolver(client)
	return issuer, resolver
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestHandleGetWorker_IncludesSessionAuthority(t *testing.T) {
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	snap := testSnapshot()
	seedSnapshot(t, s, snap)
	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeBody(t, rec)

	// Legacy payload must still type-check.
	for _, key := range []string{"worker_id", "pool", "active_jobs", "max_parallel_jobs", "capabilities", "cpu_load", "last_heartbeat"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("legacy field %q missing", key)
		}
	}

	if resp["online"] != true {
		t.Errorf("online=%v want true", resp["online"])
	}
	if resp["session_valid"] != true {
		t.Errorf("session_valid=%v want true", resp["session_valid"])
	}
	if resp["session_state"] != scheduler.TrustReasonValid {
		t.Errorf("session_state=%v want %q", resp["session_state"], scheduler.TrustReasonValid)
	}
	if _, ok := resp["session_exp_ms"]; !ok {
		t.Errorf("session_exp_ms missing")
	}
	if _, ok := resp["session_revoked"]; ok {
		t.Errorf("session_revoked key must be absent when session is valid (not false)")
	}
	if resp["last_heartbeat_at"] == nil {
		t.Errorf("last_heartbeat_at missing")
	}
	if _, ok := resp["heartbeat_age_seconds"].(float64); !ok {
		t.Errorf("heartbeat_age_seconds missing or wrong type: %T", resp["heartbeat_age_seconds"])
	}
}

func TestHandleGetWorker_RevokedSessionMarksOffline(t *testing.T) {
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	seedSnapshot(t, s, testSnapshot())

	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "w1", "tenant-acme", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	resp := decodeBody(t, rec)
	if resp["online"] != false {
		t.Errorf("online=%v want false", resp["online"])
	}
	if resp["session_valid"] != false {
		t.Errorf("session_valid=%v want false", resp["session_valid"])
	}
	if resp["session_state"] != scheduler.TrustReasonRevoked {
		t.Errorf("session_state=%v want %q", resp["session_state"], scheduler.TrustReasonRevoked)
	}
	if resp["session_revoked"] != true {
		t.Errorf("session_revoked=%v want true", resp["session_revoked"])
	}
}

func TestHandleGetWorker_FreshSessionOverridesStaleHeartbeat(t *testing.T) {
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	stale := registry.Snapshot{
		CapturedAt: time.Now().Add(-2 * workerHeartbeatTTL).UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{WorkerID: "w1", Pool: "pool-a", ActiveJobs: 0, MaxParallelJobs: 4},
		},
	}
	seedSnapshot(t, s, stale)
	if _, _, err := issuer.Issue(context.Background(), "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	resp := decodeBody(t, rec)
	if resp["online"] != true {
		t.Fatalf("online=%v want true (fresh session overrides stale heartbeat)", resp["online"])
	}
	age, ok := resp["heartbeat_age_seconds"].(float64)
	if !ok {
		t.Fatalf("heartbeat_age_seconds missing or wrong type: %T", resp["heartbeat_age_seconds"])
	}
	if age <= workerHeartbeatTTL.Seconds() {
		t.Errorf("heartbeat_age_seconds=%v want > %v (snapshot was seeded stale)", age, workerHeartbeatTTL.Seconds())
	}
}

func TestHandleGetWorker_FutureSnapshotClampsAgeToZero(t *testing.T) {
	// Clock skew regression: if a replica's snapshot CapturedAt is
	// ahead of the reader's wall clock, the arithmetic must not
	// produce a negative age.
	s, _, _ := newTestGateway(t)
	_, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	future := registry.Snapshot{
		CapturedAt: time.Now().Add(+5 * time.Minute).UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{WorkerID: "w1", Pool: "pool-a"},
		},
	}
	seedSnapshot(t, s, future)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	age, ok := resp["heartbeat_age_seconds"].(float64)
	if !ok {
		t.Fatalf("age missing: %+v", resp)
	}
	if age < 0 {
		t.Errorf("heartbeat_age_seconds=%v must never be negative", age)
	}
}

func TestHandleGetWorker_NoSessionReportsNoSession(t *testing.T) {
	// Worker never handshook — the trust resolver must surface
	// TrustReasonNoSession, not StoreUnready.
	s, _, _ := newTestGateway(t)
	_, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	if resp["session_state"] != scheduler.TrustReasonNoSession {
		t.Errorf("session_state=%v want %q", resp["session_state"], scheduler.TrustReasonNoSession)
	}
	if resp["online"] != false {
		t.Errorf("online=%v want false (no session)", resp["online"])
	}
	if _, ok := resp["session_exp_ms"]; ok {
		t.Errorf("session_exp_ms must be absent when no session on file")
	}
}

func TestHandleGetWorker_StoreUnreadyWhenNoResolver(t *testing.T) {
	// No resolver wired → response must report TrustReasonStoreUnready
	// so operators can tell "I haven't wired it yet" from "resolver
	// exists but says no session".
	s, _, _ := newTestGateway(t)
	s.trustResolver = nil
	seedSnapshot(t, s, testSnapshot())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	if resp["session_state"] != scheduler.TrustReasonStoreUnready {
		t.Errorf("session_state=%v want %q", resp["session_state"], scheduler.TrustReasonStoreUnready)
	}
	// Legacy fallback: fresh heartbeat → online=true.
	if resp["online"] != true {
		t.Errorf("online=%v want true (legacy heartbeat-recency)", resp["online"])
	}
}

func TestHandleGetWorker_TenantIsolationDoesNotLeak(t *testing.T) {
	// Two tenants issuing sessions for the same agent ID (a possible
	// collision in multi-tenant dev environments) must not let one
	// tenant's revocation accidentally mark the other as revoked. The
	// session-token store stamps Tenant on the active record, and the
	// revocation key is scoped per-tenant:jti.
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())

	ctx := context.Background()
	// Tenant A's session for w1.
	_, claimsA, err := issuer.Issue(ctx, "w1", "tenant-A", "v1")
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	// Revoke tenant A's JTI before tenant B reissues. The active
	// record then points at tenant A's JTI (revoked) — the resolver
	// should surface TrustReasonRevoked.
	if err := issuer.Revoke(ctx, claimsA.Tenant, claimsA.JTI, claimsA.ExpiresAt); err != nil {
		t.Fatalf("revoke A: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	if resp["session_state"] != scheduler.TrustReasonRevoked {
		t.Errorf("tenant A state=%v want revoked", resp["session_state"])
	}

	// Tenant B re-issues for the same agent id — the new active
	// record replaces tenant A's record, and the resolver now sees
	// a valid session under tenant B (the revocation key was scoped
	// per-tenant:jti, so it does not match the new JTI).
	_, claimsB, err := issuer.Issue(ctx, "w1", "tenant-B", "v1")
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}
	if claimsB.JTI == claimsA.JTI {
		t.Fatalf("JTI must be unique across issues; got %q for both", claimsA.JTI)
	}

	rec2 := httptest.NewRecorder()
	s.handleGetWorker(rec2, req)
	resp2 := decodeBody(t, rec2)

	if resp2["session_state"] != scheduler.TrustReasonValid {
		t.Errorf("after tenant B re-issue, state=%v want valid", resp2["session_state"])
	}
	if resp2["online"] != true {
		t.Errorf("after tenant B re-issue, online=%v want true", resp2["online"])
	}
}

func TestHandleGetWorker_FallbackInMemoryStillProducesSessionFields(t *testing.T) {
	// The in-memory fallback path (no Redis snapshot) must also run
	// the trust resolver — the UX contract is that /api/v1/workers
	// always returns the authoritative online signal.
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	s.workerMu.Lock()
	s.workers["mem-w1"] = &pb.Heartbeat{
		WorkerId:        "mem-w1",
		Pool:            "pool-mem",
		ActiveJobs:      1,
		MaxParallelJobs: 2,
	}
	s.workerSeen["mem-w1"] = time.Now().UTC()
	s.workerMu.Unlock()

	if _, _, err := issuer.Issue(context.Background(), "mem-w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/mem-w1", nil)
	req.SetPathValue("id", "mem-w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	if resp["online"] != true {
		t.Errorf("online=%v want true (valid session, in-memory fallback)", resp["online"])
	}
	if resp["session_valid"] != true {
		t.Errorf("session_valid=%v want true", resp["session_valid"])
	}
	if _, ok := resp["last_heartbeat_at"]; !ok {
		t.Errorf("last_heartbeat_at missing on in-memory path")
	}
}

func TestHandleGetWorker_SessionExpMsMatchesClaims(t *testing.T) {
	// Integration: session_exp_ms must round-trip through the store
	// so dashboards can compute "expires in N seconds" locally.
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())

	_, claims, err := issuer.Issue(context.Background(), "w1", "tenant-acme", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	got, ok := resp["session_exp_ms"].(float64)
	if !ok {
		t.Fatalf("session_exp_ms missing or wrong type: %T", resp["session_exp_ms"])
	}
	want := claims.ExpiresAt.UnixMilli()
	if math.Abs(got-float64(want)) > 1000 {
		t.Errorf("session_exp_ms=%v want ~%v (within 1s)", got, want)
	}
}

func TestHandleGetWorkers_ListContainsSessionFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)

	seedSnapshot(t, s, testSnapshot())

	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue w1: %v", err)
	}
	// w2: valid → becomes online
	if _, _, err := issuer.Issue(ctx, "w2", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue w2: %v", err)
	}
	// w3: revoked → offline
	_, claims3, err := issuer.Issue(ctx, "w3", "tenant-acme", "v1")
	if err != nil {
		t.Fatalf("issue w3: %v", err)
	}
	if err := issuer.Revoke(ctx, claims3.Tenant, claims3.JTI, claims3.ExpiresAt); err != nil {
		t.Fatalf("revoke w3: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	resp := decodeBody(t, rec)
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("items missing or wrong type: %T", resp["items"])
	}
	byID := make(map[string]map[string]any, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item is not a map: %T", item)
		}
		byID[m["worker_id"].(string)] = m
	}

	cases := map[string]struct {
		online       bool
		sessionState string
		revoked      bool
	}{
		"w1": {true, scheduler.TrustReasonValid, false},
		"w2": {true, scheduler.TrustReasonValid, false},
		"w3": {false, scheduler.TrustReasonRevoked, true},
	}
	for id, want := range cases {
		row := byID[id]
		if row == nil {
			t.Errorf("row %s missing from response", id)
			continue
		}
		if row["online"] != want.online {
			t.Errorf("%s online=%v want %v", id, row["online"], want.online)
		}
		if row["session_state"] != want.sessionState {
			t.Errorf("%s session_state=%v want %q", id, row["session_state"], want.sessionState)
		}
		if want.revoked {
			if row["session_revoked"] != true {
				t.Errorf("%s session_revoked=%v want true", id, row["session_revoked"])
			}
		} else {
			if _, ok := row["session_revoked"]; ok {
				t.Errorf("%s session_revoked key present; should be absent", id)
			}
		}
		for _, key := range []string{"worker_id", "pool", "active_jobs", "max_parallel_jobs"} {
			if _, ok := row[key]; !ok {
				t.Errorf("row %s: legacy field %q missing", id, key)
			}
		}
	}
}

func TestHandleGetWorkers_ConcurrentReadsStableResponse(t *testing.T) {
	// Hammer the endpoint from 16 goroutines in parallel. Each must
	// see the same session-state decision, i.e. the resolver is
	// read-only and the handler has no race between snapshot and
	// trust lookup. miniredis serialises under the hood, so any
	// inconsistency surfaces as a test flake.
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())
	if _, _, err := issuer.Issue(context.Background(), "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	const goroutines = 16
	const perGoroutine = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
				rec := httptest.NewRecorder()
				s.handleGetWorkers(rec, req)
				if rec.Code != http.StatusOK {
					errs <- jsonErr(rec)
					return
				}
				var resp map[string]any
				if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
					errs <- err
					return
				}
				items := resp["items"].([]any)
				var w1Online any
				for _, item := range items {
					m := item.(map[string]any)
					if m["worker_id"] == "w1" {
						w1Online = m["online"]
						break
					}
				}
				if w1Online != true {
					errs <- asErr("w1 online flipped to %v under concurrent read", w1Online)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestHandleGetWorkers_NoResolverPreservesLegacyHeartbeatSemantics(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.trustResolver = nil
	seedSnapshot(t, s, testSnapshot())

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)

	resp := decodeBody(t, rec)
	items, ok := resp["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items missing: %+v", resp["items"])
	}
	first := items[0].(map[string]any)
	if first["online"] != true {
		t.Errorf("online=%v want true (fresh hb, no resolver)", first["online"])
	}
	if first["session_state"] != scheduler.TrustReasonStoreUnready {
		t.Errorf("session_state=%v want %q", first["session_state"], scheduler.TrustReasonStoreUnready)
	}
	if first["session_valid"] != false {
		t.Errorf("session_valid=%v want false (no resolver)", first["session_valid"])
	}
}

func TestHandleGetPool_WorkersInheritSessionFields(t *testing.T) {
	// /api/v1/pools/{name} embeds the same worker payload — the
	// session signal must be present per worker in that nested list.
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())

	if _, _, err := issuer.Issue(context.Background(), "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools/pool-a", nil)
	req.SetPathValue("name", "pool-a")
	rec := httptest.NewRecorder()
	s.handleGetPool(rec, req)
	resp := decodeBody(t, rec)

	workers, ok := resp["workers"].([]any)
	if !ok {
		t.Fatalf("workers missing or wrong type: %T", resp["workers"])
	}
	for _, item := range workers {
		m := item.(map[string]any)
		for _, key := range []string{"online", "session_valid", "session_state", "last_heartbeat_at"} {
			if _, ok := m[key]; !ok {
				t.Errorf("pool-a worker %v missing %q", m["worker_id"], key)
			}
		}
		if m["worker_id"] == "w1" && m["online"] != true {
			t.Errorf("w1 online=%v want true", m["online"])
		}
		if m["worker_id"] == "w2" && m["online"] != false {
			t.Errorf("w2 online=%v want false (no session issued)", m["online"])
		}
	}
}

func TestHandleGetWorker_ExpiredSessionReportsExpired(t *testing.T) {
	// A session whose exp is past but whose per-agent record hasn't
	// been swept from Redis yet must surface TrustReasonExpired and
	// online=false. This is the clock-skew boundary case: if miniredis
	// honours TTL and sweeps the record, Reason=NoSession is also a
	// valid outcome (still "not alive").
	s, _, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.WithTrustResolver(resolver)
	seedSnapshot(t, s, testSnapshot())

	// Short-lifetime issuer using the same redis client.
	client := s.jobStore.Client()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("short", pub); err != nil {
		t.Fatalf("trust: %v", err)
	}
	shortIssuer, err := scheduler.NewSessionTokenIssuer(priv, "short", trust, client, scheduler.SessionTokenIssuerOptions{
		Lifetime: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("short issuer: %v", err)
	}
	if _, _, err := shortIssuer.Issue(context.Background(), "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Wait past the lifetime.
	time.Sleep(800 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)
	resp := decodeBody(t, rec)

	if resp["online"] != false {
		t.Errorf("online=%v want false (session expired)", resp["online"])
	}
	state := resp["session_state"]
	if state != scheduler.TrustReasonExpired && state != scheduler.TrustReasonNoSession {
		t.Errorf("session_state=%v want expired or no_session", state)
	}
	_ = issuer
}

func jsonErr(rec *httptest.ResponseRecorder) error {
	return fmt.Errorf("status %d body %s", rec.Code, rec.Body.String())
}

func asErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
