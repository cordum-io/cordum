package shadow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newIndexRefStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	return newTestStore(t, WithShadowRetentionClasses(map[ShadowFindingRetentionClass]time.Duration{
		ShadowRetentionShort: time.Hour,
	}))
}

// indexRefReq writes the finding to every index family. No field value
// contains the tenant or the marker.
func indexRefReq(tenant, marker string) CreateFindingRequest {
	req := minimalCreateReq(tenant, "owner-idxref", "principal-idxref", "claude-code", FindingRiskHigh, "config_file", "idxref summary")
	req.FindingID = marker
	req.RetentionClass = ShadowRetentionShort
	req.SourceType = SourceTypeCI
	req.CIProvider = "github_actions"
	req.Repo = "org/idxref"
	req.ClusterID = "cluster-idxref"
	req.SignalSet = []string{"mcp_config"}
	return req
}

func indexRefFields(status FindingStatus) findingIndexFields {
	return findingIndexFields{
		Status:           status,
		Risk:             FindingRiskHigh,
		AgentProduct:     "claude-code",
		OwnerPrincipalID: "owner-idxref",
		SourceType:       SourceTypeCI,
		ClusterID:        "cluster-idxref",
		CIProvider:       "github_actions",
		Repo:             "org/idxref",
		SignalSet:        []string{"mcp_config"},
	}
}

func TestFindingIndexRefWrites(t *testing.T) {
	ctx := context.Background()
	s, mr := newIndexRefStore(t)
	const tenant = "tenant-idxref-a"
	f, err := s.CreateFinding(ctx, indexRefReq(tenant, "pr2-08-f1-ref"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	requireIndexRef(t, mr, tenant, f.FindingID, indexRefFields(FindingStatusDetected))

	mr.FastForward(30 * time.Minute)
	if _, err := s.ResolveFinding(ctx, tenant, f.FindingID, ResolveRequest{ResolvedBy: "pr2-08", Reason: "retired"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	requireIndexRef(t, mr, tenant, f.FindingID, indexRefFields(FindingStatusResolved))
}

// requireIndexRef checks the record's fields, that it holds neither the id
// nor the tenant, and that its TTL is the class retention plus
// DefaultTerminalRetention.
func requireIndexRef(t *testing.T, mr *miniredis.Miniredis, tenant, id string, want findingIndexFields) {
	t.Helper()
	key := findingIndexRefKey(tenant, id)
	raw, err := mr.Get(key)
	if err != nil {
		t.Fatalf("index record %s: %v", key, err)
	}
	if strings.Contains(raw, id) || strings.Contains(raw, tenant) {
		t.Fatalf("index record %s = %s holds the finding id or tenant", key, raw)
	}
	var got findingIndexFields
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode index record %s: %v", raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("index record = %+v, want %+v", got, want)
	}
	if ttl := mr.TTL(key); ttl != time.Hour+DefaultTerminalRetention {
		t.Fatalf("index record TTL = %s, want %s", ttl, time.Hour+DefaultTerminalRetention)
	}
}

// expiredIndexRefFinding creates and resolves the marker finding for tenant,
// then moves past its 1h retention: the finding key has expired and its
// index record remains.
func expiredIndexRefFinding(t *testing.T, s *RedisStore, mr *miniredis.Miniredis, tenant, marker string) *ShadowAgentFinding {
	t.Helper()
	ctx := context.Background()
	f, err := s.CreateFinding(ctx, indexRefReq(tenant, marker))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := s.ResolveFinding(ctx, tenant, f.FindingID, ResolveRequest{ResolvedBy: "pr2-08", Reason: "retired"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	mr.FastForward(time.Hour + time.Second)
	if mr.Exists(findingKey(f.FindingID)) {
		t.Fatalf("finding key %s still present past its retention", findingKey(f.FindingID))
	}
	return f
}

func requireNoKey(t *testing.T, mr *miniredis.Miniredis, key string) {
	t.Helper()
	if mr.Exists(key) {
		t.Fatalf("key %s still present", key)
	}
}

func requireListed(t *testing.T, s *RedisStore, q ListFindingsQuery, id string) {
	t.Helper()
	page, err := s.ListFindings(context.Background(), q)
	if err != nil {
		t.Fatalf("ListFindings(%+v): %v", q, err)
	}
	if len(page.Findings) != 1 || page.Findings[0].FindingID != id {
		t.Fatalf("ListFindings(%+v) = %+v, want only %s", q, page.Findings, id)
	}
}

// TestStaleCleanupKeepsOtherTenantSharedMembers: tenant A's finding X has
// expired and tenant B created X with the same shared index values. A's
// cleanup removes X from A's sets and deletes A's record; every shared set
// and B's record keep X.
func TestStaleCleanupKeepsOtherTenantSharedMembers(t *testing.T) {
	ctx := context.Background()
	s, mr := newIndexRefStore(t)
	const tenantA, tenantB, marker = "tenant-idxref-a", "tenant-idxref-b", "pr2-08-f1-shared"
	x := expiredIndexRefFinding(t, s, mr, tenantA, marker)
	if _, err := s.CreateFinding(ctx, indexRefReq(tenantB, marker)); err != nil {
		t.Fatalf("CreateFinding tenant B: %v", err)
	}

	s.opportunisticCleanup(ctx, tenantA, []string{x.FindingID})

	scopedA, _ := findingIndexKeys(tenantA, indexRefFields(FindingStatusResolved))
	for _, key := range scopedA {
		assertZSetMemberCount(t, ctx, s.client, key, x.FindingID, 0)
	}
	requireNoKey(t, mr, findingIndexRefKey(tenantA, x.FindingID))
	scopedB, sharedB := findingIndexKeys(tenantB, indexRefFields(FindingStatusDetected))
	for _, key := range append(scopedB, sharedB...) {
		assertZSetMemberCount(t, ctx, s.client, key, x.FindingID, 1)
	}
	requireIndexRef(t, mr, tenantB, x.FindingID, indexRefFields(FindingStatusDetected))
	requireListed(t, s, ListFindingsQuery{TenantID: tenantB, ClusterID: "cluster-idxref"}, x.FindingID)
}

// cmdHook runs after on each command once it has run, and pipeline on each
// pipeline before it runs.
type cmdHook struct {
	after    func(redis.Cmder)
	pipeline func([]redis.Cmder)
}

func (h cmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h cmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if h.after != nil {
			h.after(cmd)
		}
		return err
	}
}

func (h cmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.pipeline != nil {
			h.pipeline(cmds)
		}
		return next(ctx, cmds)
	}
}

func cmdKey(cmd redis.Cmder) string {
	if args := cmd.Args(); len(args) > 1 {
		return fmt.Sprint(args[1])
	}
	return ""
}

// TestStaleCleanupRestoresRecreatedFinding: X is created again, for the same
// or another tenant, while tenant A's cleanup reads A's index record. After
// the cleanup the new finding is listed and in every set it was written to,
// with its record.
func TestStaleCleanupRestoresRecreatedFinding(t *testing.T) {
	const tenantA, marker = "tenant-idxref-a", "pr2-08-f1-repair"
	for _, owner := range []string{tenantA, "tenant-idxref-b"} {
		t.Run(owner, func(t *testing.T) {
			ctx := context.Background()
			s, mr := newIndexRefStore(t)
			x := expiredIndexRefFinding(t, s, mr, tenantA, marker)
			var fired atomic.Bool
			s.client.(*redis.Client).AddHook(cmdHook{after: func(cmd redis.Cmder) {
				if cmd.Name() != "get" || !strings.HasPrefix(cmdKey(cmd), redisKeyFindingIndexRef) || !fired.CompareAndSwap(false, true) {
					return
				}
				if _, err := s.CreateFinding(context.Background(), indexRefReq(owner, marker)); err != nil {
					t.Errorf("CreateFinding during cleanup: %v", err)
				}
			}})

			s.opportunisticCleanup(ctx, tenantA, []string{x.FindingID})

			if !fired.Load() {
				t.Fatal("cleanup never read the index record")
			}
			scoped, shared := findingIndexKeys(owner, indexRefFields(FindingStatusDetected))
			for _, key := range append(scoped, shared...) {
				assertZSetMemberCount(t, ctx, s.client, key, x.FindingID, 1)
			}
			requireIndexRef(t, mr, owner, x.FindingID, indexRefFields(FindingStatusDetected))
			requireListed(t, s, ListFindingsQuery{TenantID: owner}, x.FindingID)
			if owner != tenantA {
				requireNoKey(t, mr, findingIndexRefKey(tenantA, x.FindingID))
			}
		})
	}
}

// TestStaleCleanupWatchAndMultiNameOneKey: a resolved finding whose key is
// still present but past its retention is cleaned up by ListFindings. The
// cleanup's WATCH and each of its MULTI blocks name one key, and it leaves
// no trace of the finding and no index record.
func TestStaleCleanupWatchAndMultiNameOneKey(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	s, mr := newTestStore(t,
		WithClock(func() time.Time { return clock }),
		WithShadowRetentionClasses(map[ShadowFindingRetentionClass]time.Duration{ShadowRetentionShort: time.Hour}),
	)
	const tenant, marker = "tenant-idxref-a", "pr2-08-f1-onekey"
	f, err := s.CreateFinding(ctx, indexRefReq(tenant, marker))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := s.ResolveFinding(ctx, tenant, f.FindingID, ResolveRequest{ResolvedBy: "pr2-08", Reason: "retired"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	if before := dIC03Trace(t, s, marker); len(before) != 10 {
		t.Fatalf("trace of %q before cleanup = %v, want the record and 9 index members", marker, before)
	}
	var armed atomic.Bool
	s.client.(*redis.Client).AddHook(cmdHook{
		after: func(cmd redis.Cmder) {
			if armed.Load() && cmd.Name() == "watch" && len(cmd.Args()) > 2 {
				t.Errorf("cleanup WATCH names more than one key: %v", cmd.Args())
			}
		},
		pipeline: func(cmds []redis.Cmder) {
			if !armed.Load() || len(cmds) == 0 || cmds[0].Name() != "multi" {
				return
			}
			keys := map[string]bool{}
			for _, cmd := range cmds {
				if name := cmd.Name(); name != "multi" && name != "exec" {
					keys[cmdKey(cmd)] = true
				}
			}
			if len(keys) > 1 {
				t.Errorf("cleanup MULTI names %d keys: %v", len(keys), cmds)
			}
		},
	})

	clock = clock.Add(time.Hour + time.Second)
	armed.Store(true)
	if _, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: tenant}); err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	waitForZSetCard(t, ctx, s.client, tenantIndexKey(tenant), 0)
	waitForCleanupIdle(t, s)
	armed.Store(false)

	if hits := dIC03Trace(t, s, marker); len(hits) != 0 {
		t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
	}
	requireNoKey(t, mr, findingIndexRefKey(tenant, f.FindingID))
}
