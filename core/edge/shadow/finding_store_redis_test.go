package shadow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/redis/go-redis/v9"
)

func setStaleCleanupStartHookForTest(t *testing.T, s *RedisStore, hook func(context.Context, string, []string)) {
	t.Helper()
	s.staleCleanupMu.Lock()
	defer s.staleCleanupMu.Unlock()
	s.staleCleanupStartHook = hook
}

func newTestStore(t *testing.T, opts ...StoreOption) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	now := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	var clockCounter, idCounter int
	defaults := []StoreOption{
		WithClock(func() time.Time {
			t := now.Add(time.Duration(clockCounter) * time.Second)
			clockCounter++
			return t
		}),
		WithIDGen(func() string {
			idCounter++
			// 32-hex-char deterministic id keyed by counter; the
			// findingIDPrefix is applied by the normaliser.
			return strings.Repeat("0", 28) + fmt.Sprintf("%04x", idCounter)
		}),
	}
	s, err := NewRedisStore(client, append(defaults, opts...)...)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return s, mr
}

func newConcurrentTransitionStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	now := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	idCounter := 0
	s, err := NewRedisStore(client,
		WithClock(func() time.Time { return now }),
		WithIDGen(func() string {
			idCounter++
			return strings.Repeat("0", 28) + fmt.Sprintf("%04x", idCounter)
		}),
	)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return s, mr
}

func minimalCreateReq(tenant, owner, principal, product string, risk FindingRisk, evType, summary string) CreateFindingRequest {
	return CreateFindingRequest{
		TenantID:         tenant,
		OwnerPrincipalID: owner,
		PrincipalID:      principal,
		AgentProduct:     product,
		Risk:             risk,
		EvidenceType:     evType,
		EvidenceSummary:  summary,
		DetectedAt:       time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
}

func TestCreateFinding_PersistsAllRequiredFields(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "2 mcp servers configured")
	req.AgentID = "agent-xyz"
	req.Hostname = "dev-mac-01"
	req.RedactedPath = "C:\\Users\\yaron\\.claude.json"
	req.Metadata = map[string]string{"detector_version": "1.2.3"}

	got, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if got.FindingID == "" || !strings.HasPrefix(got.FindingID, findingIDPrefix) {
		t.Fatalf("FindingID %q lacks prefix %q", got.FindingID, findingIDPrefix)
	}
	if got.TenantID != "tenant-a" || got.OwnerPrincipalID != "owner-1" || got.PrincipalID != "principal-1" {
		t.Fatalf("identity fields unexpected: %+v", got)
	}
	if got.Status != FindingStatusDetected {
		t.Fatalf("Status = %q, want detected", got.Status)
	}
	if got.RedactedPath == "" || strings.Contains(got.RedactedPath, "yaron") {
		t.Fatalf("RedactedPath not home-stripped: %q", got.RedactedPath)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps zero: %+v", got)
	}
}

func validEvidencePointer(tenant string) *EvidencePointer {
	return &EvidencePointer{
		TenantID:       tenant,
		URI:            "s3://shadow-bucket/evidence.json",
		SHA256:         "deadbeef",
		RetentionClass: edgecore.RetentionClassStandard,
		RedactionLevel: edgecore.RedactionLevelStandard,
		CreatedAt:      time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC),
	}
}

func TestCreateFinding_EvidenceRequirement(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CreateFindingRequest)
		wantErr bool
	}{
		{
			name:    "rejects neither evidence field",
			mutate:  func(req *CreateFindingRequest) {},
			wantErr: true,
		},
		{
			name: "accepts summary only",
			mutate: func(req *CreateFindingRequest) {
				req.EvidenceSummary = "summary only"
			},
		},
		{
			name: "accepts artifact pointer only",
			mutate: func(req *CreateFindingRequest) {
				req.EvidenceArtifact = validEvidencePointer(req.TenantID)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			req := minimalCreateReq("t1", "o1", "p1", "claude-code", FindingRiskLow, "config_file", "")
			tc.mutate(&req)

			got, err := s.CreateFinding(context.Background(), req)
			if tc.wantErr {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("CreateFinding error = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateFinding: %v", err)
			}
			if got.EvidenceSummary == "" && got.EvidenceArtifact == nil {
				t.Fatalf("CreateFinding persisted no evidence: %+v", got)
			}
		})
	}
}

func TestCreateFinding_RedactsSecretsInSummary(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq("t1", "o1", "p1", "claude-code", FindingRiskHigh, "config_file",
		"cordum_fake_sk-ant-abcdef1234567890ABCDEFGHIJ saw key in config")
	got, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if strings.Contains(got.EvidenceSummary, "sk-ant-") {
		t.Fatalf("EvidenceSummary still contains raw secret: %q", got.EvidenceSummary)
	}
	if !strings.Contains(got.EvidenceSummary, "<REDACTED>") {
		t.Fatalf("EvidenceSummary should have REDACTED marker: %q", got.EvidenceSummary)
	}
}

func TestCreateFinding_RejectsOversizedSummary(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	huge := strings.Repeat("a", MaxEvidenceSummaryBytes+1)
	req := minimalCreateReq("t1", "o1", "p1", "claude-code", FindingRiskLow, "config_file", huge)
	_, err := s.CreateFinding(ctx, req)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("oversized-summary error = %v, want ErrValidation", err)
	}
}

func TestCreateFinding_ExplicitIDIdempotencyRequiresEquivalentPayload(t *testing.T) {
	fixedNow := time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)

	t.Run("same tenant byte equivalent returns existing without duplicate indexes", func(t *testing.T) {
		s, _ := newTestStore(t, WithClock(func() time.Time { return fixedNow }))
		ctx := context.Background()
		req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "same payload")
		req.FindingID = "atomic-same-id"

		first, err := s.CreateFinding(ctx, req)
		if err != nil {
			t.Fatalf("CreateFinding first: %v", err)
		}
		again, err := s.CreateFinding(ctx, req)
		if err != nil {
			t.Fatalf("CreateFinding duplicate: %v", err)
		}
		if again.FindingID != first.FindingID || again.TenantID != first.TenantID || again.EvidenceSummary != first.EvidenceSummary {
			t.Fatalf("duplicate returned %+v, want existing %+v", again, first)
		}

		assertZSetMemberCount(t, ctx, s.client, tenantIndexKey("tenant-a"), first.FindingID, 1)
		assertZSetMemberCount(t, ctx, s.client, secondaryIndexKey("tenant-a", redisIndexSegStatus, string(FindingStatusDetected)), first.FindingID, 1)
		assertZSetMemberCount(t, ctx, s.client, sourceIndexKey(SourceTypeLocal), first.FindingID, 1)
	})

	t.Run("same tenant different payload conflicts", func(t *testing.T) {
		s, _ := newTestStore(t, WithClock(func() time.Time { return fixedNow }))
		ctx := context.Background()
		req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "original payload")
		req.FindingID = "atomic-conflict-id"
		first, err := s.CreateFinding(ctx, req)
		if err != nil {
			t.Fatalf("CreateFinding first: %v", err)
		}

		conflict := req
		conflict.EvidenceSummary = "changed payload"
		if _, err := s.CreateFinding(ctx, conflict); !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("CreateFinding conflict = %v, want ErrAlreadyExists", err)
		}
		got, err := s.GetFinding(ctx, "tenant-a", first.FindingID)
		if err != nil {
			t.Fatalf("GetFinding tenant-a: %v", err)
		}
		if got.EvidenceSummary != first.EvidenceSummary {
			t.Fatalf("stored summary = %q, want original %q", got.EvidenceSummary, first.EvidenceSummary)
		}
	})

	t.Run("different tenant same id conflicts without tenant leak", func(t *testing.T) {
		s, _ := newTestStore(t, WithClock(func() time.Time { return fixedNow }))
		ctx := context.Background()
		req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "tenant-a payload")
		req.FindingID = "atomic-cross-tenant-id"
		first, err := s.CreateFinding(ctx, req)
		if err != nil {
			t.Fatalf("CreateFinding tenant-a: %v", err)
		}

		crossTenant := req
		crossTenant.TenantID = "tenant-b"
		crossTenant.OwnerPrincipalID = "owner-2"
		crossTenant.PrincipalID = "principal-2"
		if _, err := s.CreateFinding(ctx, crossTenant); !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("CreateFinding tenant-b duplicate = %v, want ErrAlreadyExists", err)
		}
		if _, err := s.GetFinding(ctx, "tenant-b", first.FindingID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetFinding tenant-b = %v, want ErrNotFound", err)
		}
		page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-b"})
		if err != nil {
			t.Fatalf("ListFindings tenant-b: %v", err)
		}
		for _, f := range page.Findings {
			if f.FindingID == first.FindingID {
				t.Fatalf("tenant-b list leaked conflicting finding: %+v", f)
			}
		}
	})
}

func TestCreateFinding_ConcurrentSameIDCrossTenantIsSingleWinner(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 8})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })

	fixedNow := time.Date(2026, 5, 17, 14, 15, 0, 0, time.UTC)
	store, err := NewRedisStore(client,
		WithClock(func() time.Time { return fixedNow }),
		WithIDGen(func() string { return "unused-by-explicit-id" }),
	)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	type createResult struct {
		tenant  string
		finding *ShadowAgentFinding
		err     error
	}
	reqs := []CreateFindingRequest{
		minimalCreateReq("tenant-a", "owner-a", "principal-a", "claude-code", FindingRiskHigh, "config_file", "tenant-a evidence"),
		minimalCreateReq("tenant-b", "owner-b", "principal-b", "claude-code", FindingRiskCritical, "config_file", "tenant-b evidence"),
	}
	for i := range reqs {
		reqs[i].FindingID = "atomic-concurrent-cross-tenant"
	}

	start := make(chan struct{})
	results := make(chan createResult, len(reqs))
	var wg sync.WaitGroup
	for _, req := range reqs {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := store.CreateFinding(context.Background(), req)
			results <- createResult{tenant: req.TenantID, finding: got, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner createResult
	successes := 0
	failures := 0
	for res := range results {
		if res.err == nil {
			successes++
			winner = res
			continue
		}
		failures++
		if !errors.Is(res.err, ErrAlreadyExists) {
			t.Fatalf("CreateFinding loser %s error = %v, want ErrAlreadyExists", res.tenant, res.err)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("successes=%d failures=%d, want exactly one winner and one ErrAlreadyExists loser", successes, failures)
	}
	if winner.finding == nil {
		t.Fatalf("winner %s returned nil finding", winner.tenant)
	}

	loserTenant := "tenant-a"
	if winner.tenant == "tenant-a" {
		loserTenant = "tenant-b"
	}
	persisted, err := store.GetFinding(context.Background(), winner.tenant, winner.finding.FindingID)
	if err != nil {
		t.Fatalf("GetFinding winner tenant %s: %v", winner.tenant, err)
	}
	if persisted.TenantID != winner.tenant {
		t.Fatalf("persisted tenant = %q, want winner %q", persisted.TenantID, winner.tenant)
	}
	if _, err := store.GetFinding(context.Background(), loserTenant, winner.finding.FindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetFinding loser tenant %s = %v, want ErrNotFound", loserTenant, err)
	}
	page, err := store.ListFindings(context.Background(), ListFindingsQuery{TenantID: loserTenant})
	if err != nil {
		t.Fatalf("ListFindings loser tenant %s: %v", loserTenant, err)
	}
	for _, f := range page.Findings {
		if f.FindingID == winner.finding.FindingID {
			t.Fatalf("loser tenant %s list leaked winner finding: %+v", loserTenant, f)
		}
	}
}

func TestListFindings_StaleCleanupDoesNotDeleteCrossTenantFinding(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq("tenant-b", "owner-b", "principal-b", "claude-code", FindingRiskHigh, "config_file", "tenant-b evidence")
	created, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding tenant-b: %v", err)
	}

	score := float64(created.CreatedAt.UnixMilli())
	corruptIndexes := []string{
		tenantIndexKey("tenant-a"),
		secondaryIndexKey("tenant-a", redisIndexSegStatus, string(FindingStatusDetected)),
		sourceIndexKey(SourceTypeLocal),
	}
	for _, key := range corruptIndexes {
		if err := s.client.ZAdd(ctx, key, redis.Z{Score: score, Member: created.FindingID}).Err(); err != nil {
			t.Fatalf("ZAdd corrupt index %s: %v", key, err)
		}
	}

	page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Status: FindingStatusDetected})
	if err != nil {
		t.Fatalf("ListFindings tenant-a: %v", err)
	}
	if len(page.Findings) != 0 {
		t.Fatalf("tenant-a list returned cross-tenant findings: %+v", page.Findings)
	}

	// ListFindings schedules cleanup asynchronously. Invoke the same helper
	// directly as well so the data-loss assertion is deterministic.
	s.opportunisticCleanup(ctx, "tenant-a", []string{created.FindingID})

	if _, err := s.client.Get(ctx, findingKey(created.FindingID)).Bytes(); err != nil {
		t.Fatalf("tenant-b primary finding was deleted by tenant-a cleanup: %v", err)
	}
	got, err := s.GetFinding(ctx, "tenant-b", created.FindingID)
	if err != nil {
		t.Fatalf("GetFinding tenant-b after tenant-a cleanup: %v", err)
	}
	if got.TenantID != "tenant-b" {
		t.Fatalf("finding tenant after cleanup = %q, want tenant-b", got.TenantID)
	}
	assertZSetMemberCount(t, ctx, s.client, sourceIndexKey(SourceTypeLocal), created.FindingID, 1)
}

func TestOpportunisticCleanup_WATCHvsConcurrentCreateFinding(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-cleanup-race"
	id := "edge_shadow_cleanup-race-id"
	if err := s.client.ZAdd(ctx, tenantIndexKey(tenant), redis.Z{Score: 1, Member: id}).Err(); err != nil {
		t.Fatalf("ZAdd stale member: %v", err)
	}

	setStaleCleanupStartHookForTest(t, s, func(context.Context, string, []string) {
		req := minimalCreateReq(tenant, "owner-race", "principal-race", "claude-code",
			FindingRiskHigh, "config_file", "fresh create during stale cleanup")
		req.FindingID = id
		if _, err := s.CreateFinding(context.Background(), req); err != nil {
			t.Errorf("CreateFinding during cleanup: %v", err)
		}
	})

	s.opportunisticCleanup(ctx, tenant, []string{id})

	got, err := s.GetFinding(ctx, tenant, id)
	if err != nil {
		t.Fatalf("fresh finding was deleted by stale cleanup: %v", err)
	}
	if got.FindingID != id || got.TenantID != tenant {
		t.Fatalf("fresh finding = %+v, want id %q tenant %q", got, id, tenant)
	}
	assertZSetMemberCount(t, ctx, s.client, tenantIndexKey(tenant), id, 1)
}

func TestStaleCleanupStartHook_NoRace(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-hook-race"
	id := "hook-race-id"
	stop := make(chan struct{})
	var writes atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				setStaleCleanupStartHookForTest(t, s, func(context.Context, string, []string) {
					writes.Add(1)
				})
			}
		}
	}()

	for i := 0; i < 200; i++ {
		s.opportunisticCleanup(ctx, tenant, []string{id})
	}
	close(stop)
	wg.Wait()
	if writes.Load() == 0 {
		t.Fatal("cleanup hook was never invoked; race test did not exercise the hook path")
	}
}

func TestListFindings_StaleCleanupSchedulingIsBounded(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const staleCount = 25
	tenant := "tenant-stale"
	for i := 0; i < staleCount; i++ {
		id := fmt.Sprintf("stale-%02d", i)
		if err := s.client.ZAdd(ctx, tenantIndexKey(tenant), redis.Z{
			Score:  float64(i + 1),
			Member: id,
		}).Err(); err != nil {
			t.Fatalf("ZAdd stale tenant member %s: %v", id, err)
		}
	}

	started := make(chan []string, 32)
	release := make(chan struct{})
	var closeRelease sync.Once
	var calls atomic.Int32
	setStaleCleanupStartHookForTest(t, s, func(_ context.Context, cleanupTenant string, ids []string) {
		if cleanupTenant != tenant {
			t.Errorf("cleanup tenant = %q, want %q", cleanupTenant, tenant)
		}
		calls.Add(1)
		started <- ids
		<-release
	})
	releaseCleanup := func() {
		closeRelease.Do(func() {
			close(release)
		})
	}
	defer releaseCleanup()

	page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: tenant, Limit: staleCount})
	if err != nil {
		t.Fatalf("ListFindings first stale page: %v", err)
	}
	if len(page.Findings) != 0 {
		t.Fatalf("stale-only page returned findings: %+v", page.Findings)
	}
	firstIDs := waitForCleanupStart(t, started)
	if len(firstIDs) != staleCount {
		t.Fatalf("first cleanup ids len = %d, want %d: %v", len(firstIDs), staleCount, firstIDs)
	}

	for i := 0; i < 5; i++ {
		page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: tenant, Limit: staleCount})
		if err != nil {
			t.Fatalf("ListFindings repeat %d: %v", i, err)
		}
		if len(page.Findings) != 0 {
			t.Fatalf("repeat %d returned findings: %+v", i, page.Findings)
		}
	}

	select {
	case extra := <-started:
		t.Fatalf("cleanup started %d times while first cleanup was still in-flight; want one bounded/deduped cleanup, extra ids=%v", calls.Load(), extra)
	case <-time.After(200 * time.Millisecond):
	}

	waitForZSetCard(t, ctx, s.client, tenantIndexKey(tenant), staleCount)
	releaseCleanup()
	waitForZSetCard(t, ctx, s.client, tenantIndexKey(tenant), 0)
}

type cleanupContextMarker struct{}

func TestGetFinding_ExpiredCleanupUsesCallerContext(t *testing.T) {
	clock := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	store, _ := newTestStore(t, WithTerminalRetention(time.Hour), WithClock(func() time.Time { return clock }))
	f, err := store.CreateFinding(context.Background(), minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := store.ResolveFinding(context.Background(), "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "done"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	clock = clock.Add(2 * time.Hour)

	result := make(chan error, 1)
	setStaleCleanupStartHookForTest(t, store, func(ctx context.Context, tenant string, ids []string) {
		if tenant != "tenant-a" {
			result <- fmt.Errorf("cleanup tenant = %q, want tenant-a", tenant)
			return
		}
		if len(ids) != 1 || ids[0] != f.FindingID {
			result <- fmt.Errorf("cleanup ids = %v, want [%s]", ids, f.FindingID)
			return
		}
		if got := ctx.Value(cleanupContextMarker{}); got != "caller-context" {
			result <- fmt.Errorf("cleanup context marker = %v, want caller-context", got)
			return
		}
		result <- nil
	})

	ctx := context.WithValue(context.Background(), cleanupContextMarker{}, "caller-context")
	if _, err := store.GetFinding(ctx, "tenant-a", f.FindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired GetFinding err = %v, want ErrNotFound", err)
	}
	if err := waitForCleanupResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestStaleCleanupSchedulerCapsDedupesAndPreservesIndexCleanup(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-cap-cleanup"
	total := staleCleanupMaxIDs + 5
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("stale-cap-%03d", i)
		ids = append(ids, id)
		for _, key := range []string{
			tenantIndexKey(tenant),
			secondaryIndexKey(tenant, redisIndexSegStatus, string(FindingStatusDetected)),
			sourceIndexKey(SourceTypeLocal),
		} {
			if err := s.client.ZAdd(ctx, key, redis.Z{Score: float64(i + 1), Member: id}).Err(); err != nil {
				t.Fatalf("ZAdd %s member %s: %v", key, id, err)
			}
		}
	}

	started := make(chan []string, 1)
	setStaleCleanupStartHookForTest(t, s, func(_ context.Context, cleanupTenant string, cleanupIDs []string) {
		if cleanupTenant != tenant {
			t.Errorf("cleanup tenant = %q, want %q", cleanupTenant, tenant)
		}
		started <- cleanupIDs
	})
	input := append(append([]string(nil), ids...), ids[0], ids[1], "", " ")
	s.scheduleStaleCleanup(ctx, tenant, input)

	cleanupIDs := waitForCleanupStart(t, started)
	if len(cleanupIDs) != staleCleanupMaxIDs {
		t.Fatalf("cleanup ids len = %d, want cap %d", len(cleanupIDs), staleCleanupMaxIDs)
	}
	if uniqueStringCount(cleanupIDs) != len(cleanupIDs) {
		t.Fatalf("cleanup ids were not deduped: %v", cleanupIDs)
	}
	wantRemaining := total - staleCleanupMaxIDs
	waitForZSetCard(t, ctx, s.client, tenantIndexKey(tenant), wantRemaining)
	waitForZSetCard(t, ctx, s.client, secondaryIndexKey(tenant, redisIndexSegStatus, string(FindingStatusDetected)), wantRemaining)
	waitForZSetCard(t, ctx, s.client, sourceIndexKey(SourceTypeLocal), wantRemaining)
}

func TestRunScheduledStaleCleanup_DetachedContext(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-canceled-cleanup"
	id := "stale-canceled"
	if err := s.client.ZAdd(ctx, tenantIndexKey(tenant), redis.Z{Score: 1, Member: id}).Err(); err != nil {
		t.Fatalf("ZAdd stale member: %v", err)
	}

	result := make(chan error, 1)
	setStaleCleanupStartHookForTest(t, s, func(ctx context.Context, cleanupTenant string, cleanupIDs []string) {
		if cleanupTenant != tenant || len(cleanupIDs) != 1 || cleanupIDs[0] != id {
			result <- fmt.Errorf("cleanup call = tenant %q ids %v, want tenant %q id %q", cleanupTenant, cleanupIDs, tenant, id)
			return
		}
		if err := ctx.Err(); err != nil {
			result <- fmt.Errorf("cleanup context is canceled: %w", err)
			return
		}
		result <- nil
	})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	s.scheduleStaleCleanup(canceled, tenant, []string{id})
	if err := waitForCleanupResult(t, result); err != nil {
		t.Fatal(err)
	}
	waitForCleanupIdle(t, s)
	waitForZSetCard(t, ctx, s.client, tenantIndexKey(tenant), 0)
}

func TestCreateFinding_ArtifactPointerTenantMustMatch(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq("t1", "o1", "p1", "claude-code", FindingRiskMedium, "config_file", "")
	req.EvidenceArtifact = &EvidencePointer{
		TenantID:       "other-tenant",
		URI:            "s3://shadow-bucket/x.json",
		SHA256:         "deadbeef",
		RetentionClass: edgecore.RetentionClassStandard,
		RedactionLevel: edgecore.RedactionLevelStandard,
		CreatedAt:      time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC),
	}
	_, err := s.CreateFinding(ctx, req)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("mismatched-tenant-pointer error = %v, want ErrValidation", err)
	}
}

func assertZSetMemberCount(t *testing.T, ctx context.Context, client redis.UniversalClient, key, member string, want int) {
	t.Helper()
	members, err := client.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		t.Fatalf("ZRange %s: %v", key, err)
	}
	var got int
	for _, m := range members {
		if m == member {
			got++
		}
	}
	if got != want {
		t.Fatalf("member %q count in %s = %d, want %d; members=%v", member, key, got, want, members)
	}
}

func waitForCleanupStart(t *testing.T, ch <-chan []string) []string {
	t.Helper()
	select {
	case ids := <-ch:
		return ids
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cleanup to start")
		return nil
	}
}

func waitForCleanupResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		return errors.New("timed out waiting for cleanup hook")
	}
}

func waitForZSetCard(t *testing.T, ctx context.Context, client redis.UniversalClient, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := client.ZCard(ctx, key).Result()
		if err != nil {
			t.Fatalf("ZCard %s: %v", key, err)
		}
		if got == int64(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ZCard %s = %d, want %d", key, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForCleanupIdle(t *testing.T, s *RedisStore) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.staleCleanupMu.Lock()
		inFlight := len(s.staleCleanupInFlight)
		slotCount := 0
		if s.staleCleanupSlots != nil {
			slotCount = len(s.staleCleanupSlots)
		}
		s.staleCleanupMu.Unlock()
		if inFlight == 0 && slotCount == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup still busy: inFlight=%d slots=%d", inFlight, slotCount)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func uniqueStringCount(in []string) int {
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		seen[s] = struct{}{}
	}
	return len(seen)
}

func transitionBarrierClock(t *testing.T, parties int, at time.Time) func() time.Time {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	return func() time.Time {
		mu.Lock()
		arrived++
		if arrived == parties {
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
			return at
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for %d concurrent transitions; got %d", parties, arrived)
			return at
		}
	}
}

type transitionTestOutcome struct {
	status FindingStatus
	err    error
}

func transitionOutcome(f *ShadowAgentFinding, err error) transitionTestOutcome {
	if f == nil {
		return transitionTestOutcome{err: err}
	}
	return transitionTestOutcome{status: f.Status, err: err}
}

func assertTerminalStatusIndexes(t *testing.T, ctx context.Context, s *RedisStore, tenantID, findingID string) {
	t.Helper()
	got, err := s.GetFinding(ctx, tenantID, findingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got == nil || !isTerminal(got.Status) {
		t.Fatalf("final finding = %+v, want terminal status", got)
	}
	assertZSetMemberCount(t, ctx, s.client, secondaryIndexKey(tenantID, redisIndexSegStatus, string(FindingStatusDetected)), findingID, 0)
	for _, status := range []FindingStatus{FindingStatusResolved, FindingStatusSuppressed} {
		want := 0
		if got.Status == status {
			want = 1
		}
		assertZSetMemberCount(t, ctx, s.client, secondaryIndexKey(tenantID, redisIndexSegStatus, string(status)), findingID, want)
	}
}

func TestGetFinding_TenantIsolation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	// Same id, different tenant lookup → ErrNotFound.
	if _, err := s.GetFinding(ctx, "tenant-b", a.FindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrNotFound", err)
	}
	got, err := s.GetFinding(ctx, "tenant-a", a.FindingID)
	if err != nil {
		t.Fatalf("same-tenant get: %v", err)
	}
	if got.FindingID != a.FindingID {
		t.Fatalf("got %q, want %q", got.FindingID, a.FindingID)
	}
}

func TestListFindings_FiltersAndTenantIsolation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	// Two tenants, mixed risk/status/agent_product.
	for i := 0; i < 5; i++ {
		req := minimalCreateReq("tenant-a", "owner-1", "p", "claude-code", FindingRiskHigh, "config_file", "summary-a")
		if _, err := s.CreateFinding(ctx, req); err != nil {
			t.Fatalf("CreateFinding tenant-a: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		req := minimalCreateReq("tenant-a", "owner-2", "p", "codex", FindingRiskMedium, "process_name", "summary-codex")
		if _, err := s.CreateFinding(ctx, req); err != nil {
			t.Fatalf("CreateFinding tenant-a codex: %v", err)
		}
	}
	for i := 0; i < 4; i++ {
		req := minimalCreateReq("tenant-b", "owner-1", "p", "claude-code", FindingRiskHigh, "config_file", "summary-b")
		if _, err := s.CreateFinding(ctx, req); err != nil {
			t.Fatalf("CreateFinding tenant-b: %v", err)
		}
	}

	tcs := []struct {
		name    string
		q       ListFindingsQuery
		wantMin int
		wantMax int
	}{
		{name: "tenant-a all", q: ListFindingsQuery{TenantID: "tenant-a"}, wantMin: 8, wantMax: 8},
		{name: "tenant-b all", q: ListFindingsQuery{TenantID: "tenant-b"}, wantMin: 4, wantMax: 4},
		{name: "tenant-a risk=high", q: ListFindingsQuery{TenantID: "tenant-a", Risk: FindingRiskHigh}, wantMin: 5, wantMax: 5},
		{name: "tenant-a agent=codex", q: ListFindingsQuery{TenantID: "tenant-a", AgentProduct: "codex"}, wantMin: 3, wantMax: 3},
		{name: "tenant-a owner-2", q: ListFindingsQuery{TenantID: "tenant-a", OwnerPrincipalID: "owner-2"}, wantMin: 3, wantMax: 3},
		{name: "tenant-a status=detected", q: ListFindingsQuery{TenantID: "tenant-a", Status: FindingStatusDetected}, wantMin: 8, wantMax: 8},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.ListFindings(ctx, tc.q)
			if err != nil {
				t.Fatalf("ListFindings: %v", err)
			}
			n := len(page.Findings)
			if n < tc.wantMin || n > tc.wantMax {
				t.Fatalf("got %d findings, want [%d, %d]", n, tc.wantMin, tc.wantMax)
			}
			// Tenant isolation invariant: every returned finding's tenant
			// matches the query tenant.
			for _, f := range page.Findings {
				if f.TenantID != tc.q.TenantID {
					t.Errorf("got finding from tenant %q, want %q", f.TenantID, tc.q.TenantID)
				}
			}
		})
	}
}

func TestListFindings_PaginationViaCursor(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		req := minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary")
		if _, err := s.CreateFinding(ctx, req); err != nil {
			t.Fatalf("CreateFinding: %v", err)
		}
	}
	page1, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Limit: 5})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Findings) != 5 {
		t.Fatalf("page1 len = %d, want 5", len(page1.Findings))
	}
	if page1.NextCursor == "" {
		t.Fatalf("expected NextCursor on full page")
	}
	page2, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Limit: 5, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Findings) == 0 {
		t.Fatalf("page2 empty; want >0 with NextCursor from page1")
	}
	// No overlap between pages.
	seen := map[string]bool{}
	for _, f := range page1.Findings {
		seen[f.FindingID] = true
	}
	for _, f := range page2.Findings {
		if seen[f.FindingID] {
			t.Fatalf("page2 returns id %q already seen on page1", f.FindingID)
		}
	}
}

func TestListFindings_InvalidCursor(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Cursor: "not-a-cursor"})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid-cursor error = %v, want ErrInvalidCursor", err)
	}
}

// TestClampListPageSize pins the bound used at every make() site that
// allocates per-request scratch buffers from a caller-supplied page
// limit. Resolves CodeQL go/allocation-size-overflow alerts on
// finding_store_redis.go:394 + 491 + 492 (PR #276) by making the
// bound a single named helper that static analysis can recognize
// as a sanitizer. See task-8002b1ee.
func TestClampListPageSize(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero falls back to default", 0, DefaultListPageSize},
		{"negative falls back to default", -1, DefaultListPageSize},
		{"large negative falls back to default", -1 << 30, DefaultListPageSize},
		{"one is preserved", 1, 1},
		{"default value passes through", DefaultListPageSize, DefaultListPageSize},
		{"mid-range value passes through", 100, 100},
		{"max boundary passes through", MaxListPageSize, MaxListPageSize},
		{"one above max is capped", MaxListPageSize + 1, MaxListPageSize},
		{"large value is capped", 10_000, MaxListPageSize},
		{"adversarial maxint is capped", 1 << 30, MaxListPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampListPageSize(tc.in)
			if got != tc.want {
				t.Fatalf("clampListPageSize(%d) = %d, want %d", tc.in, got, tc.want)
			}
			if got < 1 || got > MaxListPageSize {
				t.Fatalf("clampListPageSize(%d) = %d is outside [1, %d]; the bound is what CodeQL relies on", tc.in, got, MaxListPageSize)
			}
		})
	}
}

// TestShadowFindings_LimitCappedAtMax is the DoD-named anchor for the
// CodeQL go/uncontrolled-allocation-size guard on finding_store_redis.go
// (alerts #37-39 / GHAS #894 / #896 on PR #276). It composes the existing
// helper-table cases on clampListPageSize with single-index, multi-signal,
// and zero-limit end-to-end paths so a single test name on the DoD checks
// off the full surface. Concrete coverage lives in TestClampListPageSize
// and TestListFindings_LimitCapAcrossPaths; this wrapper exists so QA can
// match the DoD line item without traversing both files.
func TestShadowFindings_LimitCappedAtMax(t *testing.T) {
	t.Run("clamp helper covers zero/negative/max/max+1/MaxInt", TestClampListPageSize)
	t.Run("list end-to-end caps single-signal/multi-signal/zero-limit", TestListFindings_LimitCapAcrossPaths)
	t.Run("multi-signal cap enforced at list layer", TestListFindings_MultiSignalCapEnforcedAtListLayer)
}

// TestListFindings_LimitCapAcrossPaths verifies the page-size bound
// holds end-to-end on BOTH the single-signal path (ListFindings) and
// the multi-signal path (listFindingsByMultiSignal). Adversarial
// limits beyond MaxListPageSize must not over-allocate; both paths
// must cap at MaxListPageSize. Pairs with TestClampListPageSize to
// guard the EDGE-141 + EDGE-143.5 surfaces flagged by CodeQL.
func TestListFindings_LimitCapAcrossPaths(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const total = MaxListPageSize + 25
	for i := 0; i < total; i++ {
		req := minimalCreateReq("tenant-cap", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "summary")
		req.SignalSet = []string{"namespace_untenanted", "unmanaged_workload"}
		if _, err := s.CreateFinding(ctx, req); err != nil {
			t.Fatalf("CreateFinding[%d]: %v", i, err)
		}
	}
	t.Run("single-signal path caps at MaxListPageSize", func(t *testing.T) {
		page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-cap", Limit: 1 << 30})
		if err != nil {
			t.Fatalf("ListFindings: %v", err)
		}
		if len(page.Findings) > MaxListPageSize {
			t.Fatalf("single-signal page len = %d, want <= %d", len(page.Findings), MaxListPageSize)
		}
		if len(page.Findings) != MaxListPageSize {
			t.Fatalf("single-signal page len = %d, want exactly %d (enough data exists)", len(page.Findings), MaxListPageSize)
		}
	})
	t.Run("multi-signal path caps at MaxListPageSize", func(t *testing.T) {
		page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-cap", Limit: 1 << 30, Signals: []string{"namespace_untenanted", "unmanaged_workload"}})
		if err != nil {
			t.Fatalf("ListFindings(multi-signal): %v", err)
		}
		if len(page.Findings) > MaxListPageSize {
			t.Fatalf("multi-signal page len = %d, want <= %d", len(page.Findings), MaxListPageSize)
		}
		if len(page.Findings) != MaxListPageSize {
			t.Fatalf("multi-signal page len = %d, want exactly %d (enough data exists)", len(page.Findings), MaxListPageSize)
		}
	})
	t.Run("zero limit falls back to default", func(t *testing.T) {
		page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-cap"})
		if err != nil {
			t.Fatalf("ListFindings(zero-limit): %v", err)
		}
		if len(page.Findings) != DefaultListPageSize {
			t.Fatalf("zero-limit page len = %d, want %d", len(page.Findings), DefaultListPageSize)
		}
	})
}

// TestListFindings_MultiSignalCapEnforcedAtListLayer asserts the
// list-layer defence-in-depth cap fires when a caller submits more than
// maxShadowSignalSetEntries signals to the multi-signal path. The
// handler-layer cap in parseShadowFindingListQuery is the primary gate;
// this test pins the redundant store-layer check so a future internal
// caller that bypasses the handler cannot trigger unbounded fan-out.
func TestListFindings_MultiSignalCapEnforcedAtListLayer(t *testing.T) {
	s, _ := newTestStore(t)
	q := ListFindingsQuery{TenantID: "tenant-cap", Signals: make([]string, maxShadowSignalSetEntries+1)}
	for i := range q.Signals {
		q.Signals[i] = fmt.Sprintf("signal_%02d", i)
	}
	page, err := s.ListFindings(context.Background(), q)
	if err == nil {
		t.Fatalf("ListFindings with %d signals returned no error; want ErrValidation cap", len(q.Signals))
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want errors.Is(err, ErrValidation)", err)
	}
	if !strings.Contains(err.Error(), "signals exceeds max") {
		t.Fatalf("err = %v, want message containing %q", err, "signals exceeds max")
	}
	if len(page.Findings) != 0 {
		t.Fatalf("findings len = %d, want 0 on capped error", len(page.Findings))
	}
}

func TestResolveFinding_TerminalAndIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	f, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	resolved, err := s.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{
		ResolvedBy: "alice",
		Reason:     "operator uninstalled the shadow agent",
	})
	if err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	if resolved.Status != FindingStatusResolved {
		t.Fatalf("Status = %q, want resolved", resolved.Status)
	}
	if resolved.ResolvedAt == nil || resolved.ResolvedBy != "alice" {
		t.Fatalf("resolution fields not set: %+v", resolved)
	}
	// Idempotent: re-resolve is a no-op success.
	again, err := s.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "again"})
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if again.Status != FindingStatusResolved {
		t.Fatalf("re-resolve Status = %q, want resolved", again.Status)
	}
	// Cannot suppress a resolved finding.
	if _, err := s.SuppressFinding(ctx, "tenant-a", f.FindingID, SuppressRequest{SuppressedBy: "alice"}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("suppress-after-resolve = %v, want ErrTerminalConflict", err)
	}
}

func TestTransitionFinding_ConcurrentResolveVsSuppressIsAtomic(t *testing.T) {
	s, _ := newConcurrentTransitionStore(t)
	ctx := context.Background()
	f, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	s.now = transitionBarrierClock(t, 2, time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC))

	start := make(chan struct{})
	results := make(chan transitionTestOutcome, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		got, err := s.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "fixed"})
		results <- transitionOutcome(got, err)
	}()
	go func() {
		defer wg.Done()
		<-start
		got, err := s.SuppressFinding(ctx, "tenant-a", f.FindingID, SuppressRequest{SuppressedBy: "bob", Reason: "accepted"})
		results <- transitionOutcome(got, err)
	}()
	close(start)
	wg.Wait()
	close(results)

	var successes, conflicts int
	for res := range results {
		switch {
		case res.err == nil:
			successes++
			if !isTerminal(res.status) {
				t.Fatalf("successful transition status = %q, want terminal", res.status)
			}
		case errors.Is(res.err, ErrTerminalConflict):
			conflicts++
		default:
			t.Fatalf("transition error = %v, want nil or ErrTerminalConflict", res.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent resolve/suppress outcomes: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	assertTerminalStatusIndexes(t, ctx, s, "tenant-a", f.FindingID)
}

func TestTransitionFinding_DuplicateResolveDoesNotCorruptIndexes(t *testing.T) {
	s, _ := newConcurrentTransitionStore(t)
	ctx := context.Background()
	f, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	s.now = transitionBarrierClock(t, 2, time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC))

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "fixed"})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("duplicate resolve error = %v, want nil idempotent success", err)
		}
	}
	assertTerminalStatusIndexes(t, ctx, s, "tenant-a", f.FindingID)
}

func TestTransitionFinding_NotFoundAndWrongTenantAreReadOnly(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ResolveFinding(ctx, "tenant-a", "missing-finding", ResolveRequest{ResolvedBy: "alice"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve missing = %v, want ErrNotFound", err)
	}
	f, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := s.SuppressFinding(ctx, "tenant-b", f.FindingID, SuppressRequest{SuppressedBy: "bob"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant suppress = %v, want ErrNotFound", err)
	}

	got, err := s.GetFinding(ctx, "tenant-a", f.FindingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.Status != FindingStatusDetected {
		t.Fatalf("wrong-tenant transition changed status to %q, want %q", got.Status, FindingStatusDetected)
	}
	assertZSetMemberCount(t, ctx, s.client, secondaryIndexKey("tenant-a", redisIndexSegStatus, string(FindingStatusDetected)), f.FindingID, 1)
	assertZSetMemberCount(t, ctx, s.client, secondaryIndexKey("tenant-a", redisIndexSegStatus, string(FindingStatusSuppressed)), f.FindingID, 0)
}

func TestSuppressFinding_TerminalAndIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	f, err := s.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskMedium, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	until := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	suppressed, err := s.SuppressFinding(ctx, "tenant-a", f.FindingID, SuppressRequest{
		SuppressedBy:    "bob",
		Reason:          "approved exception per change ticket",
		SuppressedUntil: &until,
	})
	if err != nil {
		t.Fatalf("SuppressFinding: %v", err)
	}
	if suppressed.Status != FindingStatusSuppressed {
		t.Fatalf("Status = %q, want suppressed", suppressed.Status)
	}
	if suppressed.SuppressedUntil == nil || !suppressed.SuppressedUntil.Equal(until) {
		t.Fatalf("SuppressedUntil = %v, want %v", suppressed.SuppressedUntil, until)
	}
	// Listing by status=suppressed must include it; status=detected must not.
	listSup, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Status: FindingStatusSuppressed})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(listSup.Findings) != 1 || listSup.Findings[0].FindingID != f.FindingID {
		t.Fatalf("suppressed list = %+v, want [%s]", listSup.Findings, f.FindingID)
	}
	listDet, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", Status: FindingStatusDetected})
	if err != nil {
		t.Fatalf("ListFindings detected: %v", err)
	}
	if len(listDet.Findings) != 0 {
		t.Fatalf("detected list = %+v, want empty", listDet.Findings)
	}
}

func TestTerminalRetention_HidesExpiredAndCleansIndex(t *testing.T) {
	now := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	clock := now
	store, _ := newTestStore(t, WithTerminalRetention(time.Hour), WithClock(func() time.Time { return clock }))
	ctx := context.Background()
	f, err := store.CreateFinding(ctx, minimalCreateReq("tenant-a", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := store.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "done"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	// Within retention: still listable + gettable.
	got, err := store.GetFinding(ctx, "tenant-a", f.FindingID)
	if err != nil || got == nil {
		t.Fatalf("within-retention get = (%v, %v), want non-nil + nil err", got, err)
	}
	// Past retention: hidden from get + list.
	clock = clock.Add(2 * time.Hour)
	if _, err := store.GetFinding(ctx, "tenant-a", f.FindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-retention get = %v, want ErrNotFound", err)
	}
	page, err := store.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != 0 {
		t.Fatalf("post-retention list = %+v, want empty", page.Findings)
	}
}

func TestRedisStore_NilClientReturnsStoreUnavailable(t *testing.T) {
	got, err := NewRedisStore(nil)
	if err != nil {
		t.Fatalf("NewRedisStore(nil) err = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("NewRedisStore(nil) = %v, want nil", got)
	}
	var s *RedisStore
	if _, err := s.CreateFinding(context.Background(), CreateFindingRequest{}); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("nil-store create = %v, want ErrStoreUnavailable", err)
	}
}

func TestRedisStore_StoreClosedSurfacesError(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	mr.Close()
	// CreateFinding probes via GET first; expect a non-nil error.
	_, err := s.CreateFinding(ctx, minimalCreateReq("t", "o", "p", "claude-code", FindingRiskHigh, "config_file", "summary"))
	if err == nil {
		t.Fatalf("expected error after miniredis close, got nil")
	}
}

// TestFinding_ConcurrentCreatesCollisionFree drives N concurrent
// CreateFinding calls against a single tenant via N goroutines using
// a shared miniredis client + the PRODUCTION defaultIDGen (NOT the
// deterministic test counter, which races on counter increment AND
// would manufacture collisions that the real crypto/rand-based gen
// cannot produce). Pins that the ID generator is concurrency-safe:
// zero ErrAlreadyExists collisions on auto-generated FindingIDs,
// every goroutine returns a unique id, and all N records persist.
// Run at -count=10 to catch crypto/rand misuse, lock leaks, or
// pipeline-ordering bugs that only surface under contention.
func TestFinding_ConcurrentCreatesCollisionFree(t *testing.T) {
	mr := miniredis.RunT(t)
	// Larger pool than newTestStore's PoolSize=1 so the contention
	// surfaces at the ID-gen level instead of being serialised by a
	// single Redis connection. Force RESP2 so go-redis does not perform
	// RESP3 maintenance-notification option mutation while this race test
	// concurrently uses Watch/Tx paths.
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 16, Protocol: 2})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })

	// Construct the store with NO options so it falls back to the
	// production defaultIDGen (crypto/rand-backed) and a wall-clock
	// time.Now seam. Both are the surfaces this test exercises.
	store, err := NewRedisStore(client)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	const tenant = "tenant-concurrent"
	baseReq := minimalCreateReq(tenant, "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "concurrent-create-evidence")

	const N = 50
	var wg sync.WaitGroup
	errCh := make(chan error, N)
	idCh := make(chan string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Value-copy the request so each goroutine owns its own
			// CreateFindingRequest; the request struct contains no
			// shared pointers in this test path.
			req := baseReq
			got, err := store.CreateFinding(context.Background(), req)
			if err != nil {
				errCh <- err
				return
			}
			idCh <- got.FindingID
		}()
	}
	wg.Wait()
	close(errCh)
	close(idCh)

	var failed int
	for err := range errCh {
		failed++
		if errors.Is(err, ErrAlreadyExists) {
			t.Errorf("ErrAlreadyExists collision on auto-generated FindingID: %v — defaultIDGen produced a duplicate under contention", err)
		} else {
			t.Errorf("unexpected CreateFinding error: %v", err)
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d concurrent CreateFinding calls failed; see errors above", failed, N)
	}

	seen := make(map[string]struct{}, N)
	for id := range idCh {
		if _, dup := seen[id]; dup {
			t.Errorf("defaultIDGen returned duplicate id %q across concurrent calls", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != N {
		t.Fatalf("got %d unique FindingIDs from %d goroutines, want exactly %d", len(seen), N, N)
	}

	page, err := store.ListFindings(context.Background(), ListFindingsQuery{TenantID: tenant, Limit: 200})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != N {
		t.Fatalf("persisted findings count = %d, want %d (some pipeline operations did not commit, or index drift hid records)", len(page.Findings), N)
	}
}
