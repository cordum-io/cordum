package edge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Tenant-isolation fixtures for the Edge evidence export (PR1-03E). Each
// tenant's marker event input and approval reason carry its marker, so a
// foreign record shows up in the serialized bundle, not only in counts.
const (
	isoEdgeMarkerA  = "MARKER-TENANT-ISO-A"
	isoEdgeMarkerB  = "MARKER-TENANT-ISO-B"
	isoEdgeTenantB  = "tenant-iso-b"
	isoEdgeSessionB = "edge_sess_iso_b"
	isoEdgeExecB    = "exec_iso_b"
	isoEdgeParentB  = "evt-iso-b-parent"
)

// isoEdgeSeed is one tenant's seeded evidence, compared against its bundle.
type isoEdgeSeed struct {
	tenantID, sessionID, marker   string
	events, executions, approvals []string
}

// seedIsoEdgeMarker appends a marker-bearing event to the execution and
// enqueues a marker-bearing approval on parentEventID; it returns both ids.
func seedIsoEdgeMarker(t *testing.T, store *RedisStore, tenantID, sessionID, executionID, parentEventID, marker string) (string, string) {
	t.Helper()
	ctx := context.Background()
	eventID := "evt-" + strings.ToLower(marker)
	event := validStoreEvent(tenantID, sessionID, executionID, eventID, 0, time.Date(2026, 4, 30, 10, 1, 0, 0, time.UTC), EventKindHookPreToolUse, DecisionAllow)
	event.InputRedacted = map[string]any{"summary": marker}
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent %s: %v", eventID, err)
	}
	req := validApprovalRequest(tenantID, sessionID, executionID, parentEventID, time.Now())
	req.Reason = marker + " approval"
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval %s: %v", tenantID, err)
	}
	return eventID, approval.ApprovalRef
}

// bundleIDs returns the sorted event, execution and approval ids of a bundle
// and fails if any record names another tenant or session.
func bundleIDs(t *testing.T, b SessionExportBundle, tenantID, sessionID string) (events, executions, approvals []string) {
	t.Helper()
	for _, e := range b.Events {
		if e.TenantID != tenantID || e.SessionID != sessionID {
			t.Errorf("event %s belongs to %s/%s, want %s/%s", e.EventID, e.TenantID, e.SessionID, tenantID, sessionID)
		}
		events = append(events, e.EventID)
	}
	for _, x := range b.Executions {
		if x.TenantID != tenantID || x.SessionID != sessionID {
			t.Errorf("execution %s belongs to %s/%s, want %s/%s", x.ExecutionID, x.TenantID, x.SessionID, tenantID, sessionID)
		}
		executions = append(executions, x.ExecutionID)
	}
	for _, a := range b.Approvals {
		if a.TenantID != tenantID || a.SessionID != sessionID {
			t.Errorf("approval %s belongs to %s/%s, want %s/%s", a.ApprovalRef, a.TenantID, a.SessionID, tenantID, sessionID)
		}
		approvals = append(approvals, a.ApprovalRef)
	}
	sort.Strings(events)
	sort.Strings(executions)
	sort.Strings(approvals)
	return events, executions, approvals
}

// TestTenantIsolationEdgeExportAssembly seeds an evidence session in two
// tenants. Each tenant's bundle holds every one of its own events,
// executions and approvals and nothing of the other's, and assembling B's
// session id under A's tenant returns not-found with an empty bundle while
// B's evidence stays unchanged.
func TestTenantIsolationEdgeExportAssembly(t *testing.T) {
	env := setupExportTestEnv(t)
	ctx := context.Background()
	assembler := &SessionExportAssembler{
		Store:         env.store,
		ArtifactStore: env.seedArtifactStore(),
		Now:           func() time.Time { return time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC) },
	}

	markerEventA, approvalA := seedIsoEdgeMarker(t, env.store, env.tenantID, env.sessionID, env.exec1ID, "evt-1a", isoEdgeMarkerA)
	createApprovalParents(t, ctx, env.store, isoEdgeTenantB, isoEdgeSessionB, isoEdgeExecB, isoEdgeParentB, time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))
	markerEventB, approvalB := seedIsoEdgeMarker(t, env.store, isoEdgeTenantB, isoEdgeSessionB, isoEdgeExecB, isoEdgeParentB, isoEdgeMarkerB)

	seeds := []isoEdgeSeed{
		{env.tenantID, env.sessionID, isoEdgeMarkerA,
			[]string{"evt-1a", "evt-1b", "evt-2a", "evt-2b", markerEventA}, []string{env.exec1ID, env.exec2ID}, []string{approvalA}},
		{isoEdgeTenantB, isoEdgeSessionB, isoEdgeMarkerB,
			[]string{isoEdgeParentB, markerEventB}, []string{isoEdgeExecB}, []string{approvalB}},
	}
	bundles := make([]SessionExportBundle, len(seeds))
	for i, own := range seeds {
		bundle, err := assembler.Assemble(ctx, ExportSessionQuery{TenantID: own.tenantID, SessionID: own.sessionID}, ExportOptions{})
		if err != nil {
			t.Fatalf("Assemble %s: %v", own.tenantID, err)
		}
		assertIsoEdgeBundle(t, bundle, own, seeds[1-i])
		bundles[i] = bundle
	}

	cross, err := assembler.Assemble(ctx, ExportSessionQuery{TenantID: env.tenantID, SessionID: isoEdgeSessionB}, ExportOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant A assembling B's session err = %v, want ErrNotFound", err)
	}
	if !reflect.DeepEqual(cross, SessionExportBundle{}) {
		t.Fatalf("cross-tenant Assemble returned a non-empty bundle: %+v", cross)
	}
	after, err := assembler.Assemble(ctx, ExportSessionQuery{TenantID: isoEdgeTenantB, SessionID: isoEdgeSessionB}, ExportOptions{})
	if err != nil {
		t.Fatalf("Assemble B after cross-tenant attempt: %v", err)
	}
	if !reflect.DeepEqual(after, bundles[1]) {
		t.Fatalf("B's evidence changed after A's cross-tenant export:\nbefore %+v\nafter  %+v", bundles[1], after)
	}
}

// assertIsoEdgeBundle checks that a tenant's bundle holds exactly its own
// seeded records plus its marker, and no id or marker of the other tenant.
func assertIsoEdgeBundle(t *testing.T, bundle SessionExportBundle, own, other isoEdgeSeed) {
	t.Helper()
	if bundle.TenantID != own.tenantID || bundle.Session.TenantID != own.tenantID || bundle.Session.SessionID != own.sessionID {
		t.Fatalf("bundle for %s names tenant %q session %s/%s", own.tenantID, bundle.TenantID, bundle.Session.TenantID, bundle.Session.SessionID)
	}
	events, executions, approvals := bundleIDs(t, bundle, own.tenantID, own.sessionID)
	sort.Strings(own.events)
	sort.Strings(own.executions)
	if !reflect.DeepEqual(events, own.events) || !reflect.DeepEqual(executions, own.executions) || !reflect.DeepEqual(approvals, own.approvals) {
		t.Fatalf("%s bundle ids = events %v executions %v approvals %v, want %v %v %v",
			own.tenantID, events, executions, approvals, own.events, own.executions, own.approvals)
	}
	raw := mustMarshalIsoBundle(t, bundle)
	if !strings.Contains(raw, own.marker) {
		t.Fatalf("%s bundle lacks its own marker %q", own.tenantID, own.marker)
	}
	leaks := []string{other.tenantID, other.sessionID, other.marker}
	leaks = append(leaks, other.events...)
	leaks = append(leaks, other.executions...)
	leaks = append(leaks, other.approvals...)
	for _, leak := range leaks {
		if strings.Contains(raw, leak) {
			t.Errorf("%s bundle carries foreign tenant data %q", own.tenantID, leak)
		}
	}
}

func mustMarshalIsoBundle(t *testing.T, b SessionExportBundle) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return string(raw)
}
