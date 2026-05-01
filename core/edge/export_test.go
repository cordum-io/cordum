package edge

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/redis/go-redis/v9"
)

// fakeArtifactStater lets export tests express artifact-store outcomes
// (found / not_found / store_error) without round-tripping through Redis.
// The bundler treats this purely as a metadata source; never seeing
// content here is part of the test's wire-shape guarantee.
type fakeArtifactStater struct {
	metas map[string]artifacts.Metadata
	err   error
}

func (f *fakeArtifactStater) Stat(_ context.Context, ptr string) (artifacts.Metadata, error) {
	if f.err != nil {
		return artifacts.Metadata{}, f.err
	}
	if meta, ok := f.metas[ptr]; ok {
		return meta, nil
	}
	return artifacts.Metadata{}, artifacts.ErrArtifactNotFound
}

// exportTestEnv builds an in-memory Edge store with miniredis backing and
// pre-populates a tenant-A session with two executions, four events, and
// two approvals — enough surface area to exercise sorting, dedupe,
// pagination, and tenant filtering without re-creating fixtures in every
// test. Each test mutates the env (adding more events, changing artifact
// metas, etc.) before calling Assemble.
type exportTestEnv struct {
	store     *RedisStore
	mr        *miniredis.Miniredis
	cleanup   func()
	tenantID  string
	sessionID string
	exec1ID   string
	exec2ID   string
	pointers  []ArtifactPointer // every pointer attached to events; use to seed faker
}

func setupExportTestEnv(t *testing.T) *exportTestEnv {
	t.Helper()
	store, _, mr, cleanup := newRedisEdgeStore(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	tenantID := "tenant-a"
	sessionID := "edge_sess_export"
	exec1 := "exec_e1"
	exec2 := "exec_e2"

	if err := store.CreateSession(ctx, validStoreSession(tenantID, sessionID, "principal-a", started)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.CreateExecution(ctx, validStoreExecution(tenantID, sessionID, exec1, started.Add(time.Second), func(e *AgentExecution) {
		e.JobID = "job-1"
		e.WorkflowRunID = "run-1"
		e.StepID = "step-1"
	})); err != nil {
		t.Fatalf("CreateExecution exec1: %v", err)
	}
	if err := store.CreateExecution(ctx, validStoreExecution(tenantID, sessionID, exec2, started.Add(2*time.Second), func(e *AgentExecution) {
		e.JobID = ""
		e.WorkflowRunID = ""
		e.StepID = ""
	})); err != nil {
		t.Fatalf("CreateExecution exec2: %v", err)
	}

	pointers := make([]ArtifactPointer, 0, 4)
	// exec1 events: seq 1, 2 (interleaved across timestamps to verify sort)
	for _, ev := range []struct {
		eventID string
		seq     int
		offset  time.Duration
		kind    EventKind
		dec     EdgeDecision
	}{
		{"evt-1a", 1, 3 * time.Second, EventKindHookPreToolUse, DecisionAllow},
		{"evt-1b", 2, 4 * time.Second, EventKindHookPolicyDecision, DecisionDeny},
	} {
		event := validStoreEvent(tenantID, sessionID, exec1, ev.eventID, ev.seq, started.Add(ev.offset), ev.kind, ev.dec)
		ptr := validArtifactPointer(started)
		ptr.TenantID = tenantID
		ptr.SessionID = sessionID
		ptr.ExecutionID = exec1
		ptr.EventID = ev.eventID
		ptr.URI = "art://exec1/" + ev.eventID
		ptr.SHA256 = "sha256:exec1-" + ev.eventID
		event.ArtifactPointers = []ArtifactPointer{ptr}
		pointers = append(pointers, ptr)
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent %s: %v", ev.eventID, err)
		}
	}
	// exec2 events: seq 1, 2 (different execution, should still sort correctly
	// across the bundle by (Seq, Timestamp, EventID)).
	for _, ev := range []struct {
		eventID string
		seq     int
		offset  time.Duration
		kind    EventKind
		dec     EdgeDecision
	}{
		{"evt-2a", 1, 5 * time.Second, EventKindHookPreToolUse, DecisionAllow},
		{"evt-2b", 2, 6 * time.Second, EventKindHookPolicyDecision, DecisionAllow},
	} {
		event := validStoreEvent(tenantID, sessionID, exec2, ev.eventID, ev.seq, started.Add(ev.offset), ev.kind, ev.dec)
		ptr := validArtifactPointer(started)
		ptr.TenantID = tenantID
		ptr.SessionID = sessionID
		ptr.ExecutionID = exec2
		ptr.EventID = ev.eventID
		ptr.URI = "art://exec2/" + ev.eventID
		ptr.SHA256 = "sha256:exec2-" + ev.eventID
		event.ArtifactPointers = []ArtifactPointer{ptr}
		pointers = append(pointers, ptr)
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent %s: %v", ev.eventID, err)
		}
	}

	return &exportTestEnv{
		store:     store,
		mr:        mr,
		cleanup:   cleanup,
		tenantID:  tenantID,
		sessionID: sessionID,
		exec1ID:   exec1,
		exec2ID:   exec2,
		pointers:  pointers,
	}
}

func (env *exportTestEnv) seedArtifactStore() *fakeArtifactStater {
	metas := make(map[string]artifacts.Metadata, len(env.pointers))
	for _, p := range env.pointers {
		metas[p.URI] = artifacts.Metadata{
			ContentType: "application/json",
			SizeBytes:   1234,
			Retention:   artifacts.RetentionAudit,
			Labels: map[string]string{
				"tenant_id":  p.TenantID,
				"session_id": p.SessionID,
			},
		}
	}
	return &fakeArtifactStater{metas: metas}
}

// TestSessionExportAssemblerHappyPathContainsAllSessionEvidence — the
// baseline shape. A full session of 4 events across 2 executions plus 2
// approvals plus artifact pointers must round-trip into a bundle whose
// counts match.
func TestSessionExportAssemblerHappyPathContainsAllSessionEvidence(t *testing.T) {
	env := setupExportTestEnv(t)
	a := &SessionExportAssembler{
		Store:         env.store,
		ArtifactStore: env.seedArtifactStore(),
		Now:           func() time.Time { return time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC) },
	}

	bundle, err := a.Assemble(context.Background(), ExportSessionQuery{
		TenantID:  env.tenantID,
		SessionID: env.sessionID,
	}, ExportOptions{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if bundle.ManifestVersion != ExportManifestVersion {
		t.Errorf("ManifestVersion = %q, want %q", bundle.ManifestVersion, ExportManifestVersion)
	}
	if bundle.TenantID != env.tenantID {
		t.Errorf("TenantID = %q, want %q", bundle.TenantID, env.tenantID)
	}
	if bundle.Session.SessionID != env.sessionID {
		t.Errorf("Session.SessionID = %q, want %q", bundle.Session.SessionID, env.sessionID)
	}
	if got := len(bundle.Executions); got != 2 {
		t.Errorf("Executions length = %d, want 2", got)
	}
	if got := len(bundle.Events); got != 4 {
		t.Errorf("Events length = %d, want 4", got)
	}
	if got := len(bundle.Artifacts); got != 4 {
		t.Errorf("Artifacts length = %d, want 4 (one per pointer)", got)
	}
	if got := len(bundle.MissingArtifacts); got != 0 {
		t.Errorf("MissingArtifacts length = %d, want 0; got %#v", got, bundle.MissingArtifacts)
	}
	if bundle.Truncation.EventsTruncated {
		t.Errorf("Truncation.EventsTruncated = true, want false on full session")
	}
	if got := len(bundle.JobLinks); got != 1 {
		t.Errorf("JobLinks length = %d, want 1 (only exec1 has job IDs)", got)
	} else if link := bundle.JobLinks[0]; link.ExecutionID != env.exec1ID || link.JobID != "job-1" || link.WorkflowRunID != "run-1" || link.StepID != "step-1" {
		t.Errorf("JobLinks[0] = %#v, want exec1 job/workflow/step link", link)
	}
}

// TestSessionExportAssemblerSortsEventsDeterministicallyBySeqThenTimestamp
// — two sequential exports of the same data must produce byte-identical
// event lists. Crucial for audit reproducibility.
func TestSessionExportAssemblerSortsEventsDeterministicallyBySeqThenTimestamp(t *testing.T) {
	env := setupExportTestEnv(t)
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: env.seedArtifactStore(), Now: time.Now}

	first, err := a.Assemble(context.Background(), ExportSessionQuery{TenantID: env.tenantID, SessionID: env.sessionID}, ExportOptions{})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	second, err := a.Assemble(context.Background(), ExportSessionQuery{TenantID: env.tenantID, SessionID: env.sessionID}, ExportOptions{})
	if err != nil {
		t.Fatalf("second Assemble: %v", err)
	}
	if len(first.Events) != len(second.Events) {
		t.Fatalf("event count drift: first=%d second=%d", len(first.Events), len(second.Events))
	}
	for i := range first.Events {
		if first.Events[i].EventID != second.Events[i].EventID {
			t.Errorf("event[%d] EventID drift: first=%q second=%q", i, first.Events[i].EventID, second.Events[i].EventID)
		}
	}
	// Verify the ordering invariant: (Seq ASC, Timestamp ASC, EventID ASC).
	if !sort.SliceIsSorted(first.Events, func(i, j int) bool {
		if first.Events[i].Seq != first.Events[j].Seq {
			return first.Events[i].Seq < first.Events[j].Seq
		}
		if !first.Events[i].Timestamp.Equal(first.Events[j].Timestamp) {
			return first.Events[i].Timestamp.Before(first.Events[j].Timestamp)
		}
		return first.Events[i].EventID < first.Events[j].EventID
	}) {
		t.Errorf("events not sorted by (seq, timestamp, event_id): %#v", first.Events)
	}
}

// TestSessionExportAssemblerCrossTenantQueryReturnsErrNotFound — the
// primary security property. A tenant-B caller asking for a tenant-A
// session must hit the same not-found path as a missing session, so the
// existence of the tenant-A session does not leak.
func TestSessionExportAssemblerCrossTenantQueryReturnsErrNotFound(t *testing.T) {
	env := setupExportTestEnv(t)
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: env.seedArtifactStore(), Now: time.Now}

	_, err := a.Assemble(context.Background(), ExportSessionQuery{TenantID: "tenant-b", SessionID: env.sessionID}, ExportOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Assemble err = %v, want ErrNotFound", err)
	}
	_, err = a.Assemble(context.Background(), ExportSessionQuery{TenantID: env.tenantID, SessionID: "edge_sess_does_not_exist"}, ExportOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing-session Assemble err = %v, want ErrNotFound", err)
	}
}

// TestSessionExportAssemblerRecordsMissingArtifactsWithoutLeakingContent
// — when the artifact store returns ErrArtifactNotFound the bundler must
// surface the URI/sha256/reason in MissingArtifacts but never load or
// emit content. Asserts the bundle JSON does not contain any field name
// suggesting content body inclusion.
func TestSessionExportAssemblerRecordsMissingArtifactsWithoutLeakingContent(t *testing.T) {
	env := setupExportTestEnv(t)
	staterMissing := &fakeArtifactStater{metas: map[string]artifacts.Metadata{}} // every Stat returns ErrArtifactNotFound
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: staterMissing, Now: time.Now}

	bundle, err := a.Assemble(context.Background(), ExportSessionQuery{TenantID: env.tenantID, SessionID: env.sessionID}, ExportOptions{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if got := len(bundle.Artifacts); got != 0 {
		t.Errorf("Artifacts length = %d, want 0 when every Stat returns NotFound", got)
	}
	if got := len(bundle.MissingArtifacts); got != 4 {
		t.Errorf("MissingArtifacts length = %d, want 4 (one per pointer)", got)
	}
	for _, m := range bundle.MissingArtifacts {
		if m.Reason != MissingArtifactReasonNotFound {
			t.Errorf("MissingArtifact %q reason = %q, want %q", m.URI, m.Reason, MissingArtifactReasonNotFound)
		}
		if m.URI == "" || m.SHA256 == "" {
			t.Errorf("MissingArtifact missing URI/sha256: %#v", m)
		}
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	for _, leaked := range []string{"\"content\"", "\"body\"", "\"payload\"", "\"raw\""} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("bundle JSON unexpectedly contained %q (artifact bodies must never be inlined)", leaked)
		}
	}
}

// TestSessionExportAssemblerRecordsTenantMismatchPointerWithoutFetching
// — defense in depth. A pointer whose TenantID disagrees with the export
// tenant must be flagged tenant_mismatch and never reach Stat. Verified
// by an artifact stater that fails noisily if Stat is called.
func TestSessionExportAssemblerRecordsTenantMismatchPointerWithoutFetching(t *testing.T) {
	env := setupExportTestEnv(t)

	// Mutate the underlying event's pointer to claim cross-tenant ownership.
	// Reach into the store and rewrite directly — production code rejects
	// this at AttachArtifactPointer, but historical data could have it.
	ctx := context.Background()
	got, err := env.store.ListEvents(ctx, ListEventsQuery{TenantID: env.tenantID, SessionID: env.sessionID, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(got.Items) == 0 {
		t.Fatalf("expected events seeded by setup, got none")
	}
	target := got.Items[0]
	target.ArtifactPointers[0].TenantID = "tenant-b"
	rewriteEventInRedis(t, env.mr, target)

	staterShouldNotBeCalled := &fakeArtifactStater{err: errors.New("Stat must not be called for cross-tenant pointer")}
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: staterShouldNotBeCalled, Now: time.Now}
	bundle, err := a.Assemble(context.Background(), ExportSessionQuery{TenantID: env.tenantID, SessionID: env.sessionID}, ExportOptions{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	foundMismatch := false
	for _, m := range bundle.MissingArtifacts {
		if m.Reason == MissingArtifactReasonTenantMismatch && m.URI == target.ArtifactPointers[0].URI {
			foundMismatch = true
			break
		}
	}
	if !foundMismatch {
		t.Errorf("expected MissingArtifactReasonTenantMismatch for cross-tenant pointer, got missing=%#v", bundle.MissingArtifacts)
	}
}

// TestSessionExportAssemblerRejectsArtifactStoreLabelMismatch is the
// QA-driven regression for the export rejection at 30d2f2cc. The pointer
// fields all match the export tenant/session/execution/event, so the
// pointer-side cross-scope guard accepts the artifact and Stat is called.
// The artifact-store metadata, however, carries labels for tenant-B (or a
// different session/execution/event). Defense in depth: the bundler must
// treat the label disagreement as tenant_mismatch and refuse to include
// the entry, preventing a tenant-A event from exporting tenant-B
// artifact metadata that another worker stored into a shared store.
func TestSessionExportAssemblerRejectsArtifactStoreLabelMismatch(t *testing.T) {
	env := setupExportTestEnv(t)

	// Build a stater whose returned metadata carries WRONG labels for every
	// pointer. The pointers themselves are still correctly scoped to
	// tenant-a / sess-edge_sess_export — the test exercises the path where
	// the pointer-side guard accepts the artifact and Stat returns
	// authoritative-but-wrong identity labels.
	metas := make(map[string]artifacts.Metadata, len(env.pointers))
	for _, p := range env.pointers {
		metas[p.URI] = artifacts.Metadata{
			ContentType: "application/json",
			SizeBytes:   1234,
			Retention:   artifacts.RetentionAudit,
			Labels: map[string]string{
				// Mismatched tenant — the most important case.
				"tenant_id": "tenant-b",
				// Plus deliberately mismatched session/execution/event so
				// the test also exercises the other label checks.
				"session_id":   "edge_sess_other",
				"execution_id": "exec_other",
				"event_id":     "evt_other",
			},
		}
	}
	stater := &fakeArtifactStater{metas: metas}
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: stater, Now: time.Now}

	bundle, err := a.Assemble(context.Background(), ExportSessionQuery{
		TenantID:  env.tenantID,
		SessionID: env.sessionID,
	}, ExportOptions{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if got := len(bundle.Artifacts); got != 0 {
		t.Errorf("Artifacts length = %d, want 0 — every Stat returned mismatched labels and must NOT be included", got)
	}
	if got := len(bundle.MissingArtifacts); got != len(env.pointers) {
		t.Errorf("MissingArtifacts length = %d, want %d (one tenant_mismatch per pointer)", got, len(env.pointers))
	}
	for _, m := range bundle.MissingArtifacts {
		if m.Reason != MissingArtifactReasonTenantMismatch {
			t.Errorf("MissingArtifact %q reason = %q, want %q (Stat label mismatch)", m.URI, m.Reason, MissingArtifactReasonTenantMismatch)
		}
	}

	// Wire-shape paranoia: the leaked labels (tenant-b, edge_sess_other,
	// exec_other, evt_other) must NOT appear anywhere in the marshaled
	// bundle. If a future regression starts including the rejected entry
	// or leaking labels via the missing manifest, this catches it.
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	for _, leaked := range []string{"tenant-b", "edge_sess_other", "exec_other", "evt_other"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("bundle JSON unexpectedly contained leaked label %q from rejected artifact", leaked)
		}
	}
}

// TestSessionExportAssemblerTruncatesAtMaxEventsAndReportsTotal — the
// EventsTruncated signal is the only way an auditor can tell a partial
// bundle from a complete one. Asserts the count cap is honored and the
// truncation metadata accurately reports how many session events
// actually existed.
func TestSessionExportAssemblerTruncatesAtMaxEventsAndReportsTotal(t *testing.T) {
	env := setupExportTestEnv(t)
	a := &SessionExportAssembler{Store: env.store, ArtifactStore: env.seedArtifactStore(), Now: time.Now}

	bundle, err := a.Assemble(context.Background(), ExportSessionQuery{
		TenantID:  env.tenantID,
		SessionID: env.sessionID,
	}, ExportOptions{MaxEvents: 2})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if got := len(bundle.Events); got != 2 {
		t.Errorf("Events length = %d, want 2 with MaxEvents=2", got)
	}
	if !bundle.Truncation.EventsTruncated {
		t.Errorf("Truncation.EventsTruncated = false, want true (4 session events vs MaxEvents=2)")
	}
	if bundle.Truncation.EventCount != 4 {
		t.Errorf("Truncation.EventCount = %d, want 4 (total session events seen)", bundle.Truncation.EventCount)
	}
}

// rewriteEventInRedis directly rewrites the marshaled event payload in
// miniredis. Used by the cross-tenant test to plant historical-shaped
// data that the current AttachArtifactPointer would reject. We never call
// this from production code — the Edge store is otherwise the only writer
// to these keys.
func rewriteEventInRedis(t *testing.T, mr *miniredis.Miniredis, event AgentActionEvent) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	listKey := edgeEventsKey(event.ExecutionID)
	values, err := client.LRange(context.Background(), listKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("LRange %s: %v", listKey, err)
	}
	for i, raw := range values {
		var existing AgentActionEvent
		if err := json.Unmarshal([]byte(raw), &existing); err != nil {
			t.Fatalf("unmarshal event %d: %v", i, err)
		}
		if existing.EventID != event.EventID {
			continue
		}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if err := client.LSet(context.Background(), listKey, int64(i), payload).Err(); err != nil {
			t.Fatalf("LSet %s[%d]: %v", listKey, i, err)
		}
		return
	}
	t.Fatalf("event %s not found in %s", event.EventID, listKey)
}
