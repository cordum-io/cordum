package shadow

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/redis/go-redis/v9"
)

// shadowKeyspace renders the value of every key. TTLs are left out: time
// passing is not a write by the caller.
func shadowKeyspace(t *testing.T, ctx context.Context, client redis.UniversalClient) map[string]string {
	t.Helper()
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		var value any
		switch kind := client.Type(ctx, key).Val(); kind {
		case "string":
			value, err = client.Get(ctx, key).Result()
		case "hash":
			value, err = client.HGetAll(ctx, key).Result()
		case "list":
			value, err = client.LRange(ctx, key, 0, -1).Result()
		case "set":
			var members []string
			members, err = client.SMembers(ctx, key).Result()
			sort.Strings(members)
			value = members
		case "zset":
			value, err = client.ZRangeWithScores(ctx, key, 0, -1).Result()
		default:
			t.Fatalf("key %s has type %q", key, kind)
		}
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		out[key] = fmt.Sprint(value)
	}
	return out
}

// Both tenants hold the same exception scope and the same finding, so every
// shared source, cluster, repo and signal index holds one member per tenant.
func TestTenantIsolationShadowIndexes(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	pairs := [][2]string{{"tenant-a", "tenant-b"}, {"tenant-b", "tenant-a"}}
	exceptions, findings := map[string]string{}, map[string]string{}
	for _, pair := range pairs {
		exc, err := s.CreateException(ctx, minimalCreateExceptionReq(pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		exceptions[pair[0]] = exc.ExceptionID
	}
	for _, pair := range pairs {
		f, err := s.CreateFinding(ctx, fullCreateReq(pair[0]))
		if err != nil || f.ExceptionID != exceptions[pair[0]] {
			t.Fatalf("seed %s finding: %+v, %v; want exception %s", pair[0], f, err, exceptions[pair[0]])
		}
		findings[pair[0]] = f.FindingID
	}
	before := shadowKeyspace(t, ctx, s.client)
	for _, pair := range pairs {
		tenant, other := pair[0], pair[1]
		for name, q := range map[string]ListFindingsQuery{
			"tenant":          {},
			"status":          {Status: FindingStatusManagedSkip},
			"risk":            {Risk: FindingRiskHigh},
			"agent":           {AgentProduct: "claude-code"},
			"owner":           {OwnerPrincipalID: "owner-1"},
			"source":          {SourceType: "kubernetes"},
			"cluster":         {ClusterID: "prod-east-1"},
			"repo":            {CIProvider: "github_actions", Repo: "acme/payments"},
			"signal":          {Signals: []string{"k8s_unmanaged_process"}},
			"signals":         {Signals: []string{"k8s_heartbeat_missing", "k8s_unmanaged_process"}},
			"own exception":   {ExceptionID: exceptions[tenant]},
			"other exception": {ExceptionID: exceptions[other]},
		} {
			q.TenantID, q.IncludeManagedSkip = tenant, true
			page, err := s.ListFindings(ctx, q)
			var got []string
			for _, f := range page.Findings {
				got = append(got, f.FindingID)
			}
			want := []string{findings[tenant]}
			if name == "other exception" {
				want = nil
			}
			if err != nil || !slices.Equal(got, want) {
				t.Errorf("%s %s findings = %v, %v; want %v", tenant, name, got, err, want)
			}
		}
		page, err := s.ListExceptions(ctx, ListExceptionsQuery{TenantID: tenant})
		var got []string
		for _, exc := range page.Exceptions {
			got = append(got, exc.ExceptionID)
		}
		if want := []string{exceptions[tenant]}; err != nil || !slices.Equal(got, want) {
			t.Errorf("%s exceptions = %v, %v; want %v", tenant, got, err, want)
		}
		members, err := s.client.ZRange(ctx, exceptionMembersIndexKey(exceptions[tenant]), 0, -1).Result()
		if want := []string{findings[tenant]}; err != nil || !slices.Equal(members, want) {
			t.Errorf("%s exception members = %v, %v; want %v", tenant, members, err, want)
		}
	}
	if after := shadowKeyspace(t, ctx, s.client); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
}
