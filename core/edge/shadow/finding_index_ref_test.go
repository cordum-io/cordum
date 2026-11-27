package shadow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
