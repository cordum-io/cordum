package shadow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

const exceptionClusterTenant = "tenant-cluster"

var exceptionClusterNow = time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)

type exceptionClusterCase struct {
	name string
	want []string // nil: compare with the single-node result only
	run  func(context.Context, *RedisStore) ([]string, error)
}

// TestExceptionReadsOnCluster checks that the exception reads return the same
// pages and matches on a two-master cluster, which refuses any multi-key
// command, as on a single node.
func TestExceptionReadsOnCluster(t *testing.T) {
	single := map[string][]string{}
	for _, backend := range []string{"single", "cluster"} {
		t.Run(backend, func(t *testing.T) {
			s, records := exceptionClusterStore(t, backend == "cluster")
			for _, tc := range exceptionClusterCases(records) {
				t.Run(tc.name, func(t *testing.T) {
					got, err := tc.run(context.Background(), s)
					if err != nil {
						t.Fatalf("%s: %v", tc.name, err)
					}
					want := tc.want
					if backend == "single" {
						single[tc.name] = got
					} else {
						want = single[tc.name]
					}
					switch {
					case want == nil && len(got) == 0:
						t.Fatalf("%s returned nothing", tc.name)
					case want != nil && !slices.Equal(got, want):
						t.Fatalf("%s = %q, want %q", tc.name, got, want)
					}
				})
			}
		})
	}
}

func exceptionClusterCases(records []*Exception) []exceptionClusterCase {
	var all []string
	for i := 0; i < len(records); i += 2 {
		page := records[i:min(i+2, len(records))]
		all = append(all, page[0].ExceptionID+","+page[len(page)-1].ExceptionID)
	}
	match := &ShadowAgentFinding{TenantID: exceptionClusterTenant, SourceType: "kubernetes", Risk: FindingRiskHigh}
	var matched []string
	for _, rec := range records {
		if rec.matchesFinding(match, exceptionClusterNow) {
			matched = append(matched, rec.ExceptionID)
		}
	}
	none := &ShadowAgentFinding{TenantID: exceptionClusterTenant, SourceType: "kubernetes", Risk: FindingRiskCritical}
	return []exceptionClusterCase{
		{name: "list", want: all, run: exceptionClusterPages(ListExceptionsQuery{})},
		{name: "list_active", run: exceptionClusterPages(ListExceptionsQuery{Status: ExceptionStatusActive})},
		{name: "match", want: matched, run: exceptionClusterMatch(match)},
		{name: "match_none", want: []string{}, run: exceptionClusterMatch(none)},
	}
}

// exceptionClusterPages follows NextCursor with a page limit of 2 and returns
// one "first,last" entry per page.
func exceptionClusterPages(q ListExceptionsQuery) func(context.Context, *RedisStore) ([]string, error) {
	return func(ctx context.Context, s *RedisStore) ([]string, error) {
		q.TenantID, q.Limit, q.Cursor = exceptionClusterTenant, 2, ""
		var pages []string
		for {
			page, err := s.ListExceptions(ctx, q)
			if err != nil {
				return nil, err
			}
			if n := len(page.Exceptions); n > 0 {
				pages = append(pages, page.Exceptions[0].ExceptionID+","+page.Exceptions[n-1].ExceptionID)
			}
			if page.NextCursor == "" || len(pages) > 20 {
				return pages, nil
			}
			q.Cursor = page.NextCursor
		}
	}
}

func exceptionClusterMatch(f *ShadowAgentFinding) func(context.Context, *RedisStore) ([]string, error) {
	return func(ctx context.Context, s *RedisStore) ([]string, error) {
		matches, err := s.MatchActiveExceptions(ctx, f)
		ids := []string{}
		for _, exc := range matches {
			ids = append(ids, exc.ExceptionID)
		}
		return ids, err
	}
}

// exceptionClusterStore seeds nine records, newest first, and one index id
// with no record, using single-key writes. Each record's status, source type
// and risk follow its position. On the cluster backend both servers must hold
// some of the records.
func exceptionClusterStore(t *testing.T, cluster bool) (*RedisStore, []*Exception) {
	t.Helper()
	servers := []*miniredis.Miniredis{miniredis.RunT(t)}
	var client redis.UniversalClient = testredis.NewClient(t, servers[0].Addr())
	if cluster {
		servers = append(servers, miniredis.RunT(t))
		c := testredis.NewTwoMasterClusterClient(t, servers[0].Addr(), servers[1].Addr())
		c.AddHook(testredis.CrossSlotGuard())
		client = c
	}
	ctx := context.Background()
	index := exceptionTenantIndexKey(exceptionClusterTenant)
	records := make([]*Exception, 9)
	for i := range records {
		records[i] = exceptionClusterRecord(t, i)
		payload, err := json.Marshal(records[i])
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		score := float64(records[i].CreatedAt.UnixMilli())
		if err := client.Set(ctx, exceptionKey(records[i].ExceptionID), payload, 0).Err(); err != nil {
			t.Fatalf("seed record: %v", err)
		}
		if err := client.ZAdd(ctx, index, redis.Z{Score: score, Member: records[i].ExceptionID}).Err(); err != nil {
			t.Fatalf("seed index: %v", err)
		}
	}
	dangling := redis.Z{Score: float64(records[3].CreatedAt.Add(-time.Second).UnixMilli()), Member: exceptionIDPrefix + "dangling"}
	if err := client.ZAdd(ctx, index, dangling).Err(); err != nil {
		t.Fatalf("seed dangling index id: %v", err)
	}
	for _, mr := range servers {
		if !slices.ContainsFunc(mr.Keys(), func(k string) bool { return strings.HasPrefix(k, redisKeyException+exceptionIDPrefix) }) {
			t.Fatalf("server %s holds no exception record", mr.Addr())
		}
	}
	s, err := NewRedisStore(client, WithClock(func() time.Time { return exceptionClusterNow }))
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return s, records
}

// exceptionClusterRecord builds record i: revoked when i%4 == 1, expired when
// i%4 == 3, otherwise active; source type ci when i%3 == 0, otherwise
// kubernetes; risk high for i < 5, otherwise low.
func exceptionClusterRecord(t *testing.T, i int) *Exception {
	t.Helper()
	createdAt := exceptionClusterNow.Add(-time.Duration(i+1) * time.Minute)
	req := minimalCreateExceptionReq(exceptionClusterTenant)
	req.ExpiresAt = exceptionClusterNow.Add(24 * time.Hour)
	if i%4 == 3 {
		req.ExpiresAt = exceptionClusterNow.Add(-time.Second)
	}
	req.ScopeSourceID, req.ScopeSignalSet = "", nil
	if i%3 == 0 {
		req.ScopeSourceType = "ci"
	}
	if i >= 5 {
		req.ScopeRiskLevel = FindingRiskLow
	}
	exc, err := normalizeAndValidateException(req, createdAt, func() string { return fmt.Sprintf("%032x", i+1) })
	if err != nil {
		t.Fatalf("record %d: %v", i, err)
	}
	if i%4 == 1 {
		exc.Status, exc.RevokedBy, exc.RevokedAt = ExceptionStatusRevoked, "bob@example.com", &createdAt
	}
	return exc
}
