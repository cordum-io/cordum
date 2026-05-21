// EDGE-143.5 — tests for ShadowAgentFinding store extensions:
// §10.1 typed fields, §10.2 query filters, §10.5 Redis indexes +
// per-finding retention classes. Tests written before implementation
// per TDD; they intentionally fail until the implementation lands in
// step-4..step-7.
package shadow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func fullCreateReq(tenant string) CreateFindingRequest {
	first := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 5, 17, 11, 30, 0, 0, time.UTC)
	return CreateFindingRequest{
		TenantID:         tenant,
		OwnerPrincipalID: "owner-1",
		PrincipalID:      "principal-1",
		AgentProduct:     "claude-code",
		AgentID:          "agent-xyz",
		Hostname:         "node-01",
		Risk:             FindingRiskHigh,
		EvidenceType:     "config_file",
		EvidenceSummary:  "found shadow MCP server",
		DetectedAt:       time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		SourceType:       "kubernetes",
		SourceID:         "k8s-detector-1",
		ClusterID:        "prod-east-1",
		Namespace:        "team-payments",
		WorkloadKind:     "Deployment",
		WorkloadName:     "payments-api",
		PodUID:           "550e8400-e29b-41d4-a716-446655440000",
		CIProvider:       "github_actions",
		Repo:             "acme/payments",
		Ref:              "refs/heads/main",
		WorkflowID:       "ci.yml",
		JobID:            "build",
		RunID:            "12345",
		RunnerID:         "self-hosted-runner-1",
		TenantSource:     "label",
		PrincipalSource:  "service_account",
		SignalSet:        []string{"k8s_heartbeat_missing", "k8s_unmanaged_process"},
		Confidence:       0.85,
		FirstSeen:        &first,
		LastSeen:         &last,
		RetentionClass:   ShadowRetentionDefault,
	}
}

func TestExtensionsFields_FullPopulated(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateFinding(ctx, fullCreateReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	got, err := s.GetFinding(ctx, "tenant-a", created.FindingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.SourceType != "kubernetes" {
		t.Errorf("SourceType = %q, want kubernetes", got.SourceType)
	}
	if got.SourceID != "k8s-detector-1" {
		t.Errorf("SourceID = %q", got.SourceID)
	}
	if got.ClusterID != "prod-east-1" {
		t.Errorf("ClusterID = %q", got.ClusterID)
	}
	if got.Namespace != "team-payments" {
		t.Errorf("Namespace = %q", got.Namespace)
	}
	if got.WorkloadKind != "Deployment" || got.WorkloadName != "payments-api" {
		t.Errorf("workload = %q/%q", got.WorkloadKind, got.WorkloadName)
	}
	if got.PodUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("PodUID = %q", got.PodUID)
	}
	if got.CIProvider != "github_actions" || got.Repo != "acme/payments" {
		t.Errorf("ci provider/repo = %q/%q", got.CIProvider, got.Repo)
	}
	if got.Ref != "refs/heads/main" || got.WorkflowID != "ci.yml" || got.JobID != "build" || got.RunID != "12345" || got.RunnerID != "self-hosted-runner-1" {
		t.Errorf("ci identifiers wrong: %+v", got)
	}
	if got.TenantSource != "label" || got.PrincipalSource != "service_account" {
		t.Errorf("attribution: %q/%q", got.TenantSource, got.PrincipalSource)
	}
	if len(got.SignalSet) != 2 || got.SignalSet[0] != "k8s_heartbeat_missing" {
		t.Errorf("SignalSet = %v", got.SignalSet)
	}
	if got.Confidence < 0.84 || got.Confidence > 0.86 {
		t.Errorf("Confidence = %v", got.Confidence)
	}
	if got.FirstSeen == nil || !got.FirstSeen.Equal(time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("FirstSeen = %v", got.FirstSeen)
	}
	if got.LastSeen == nil || !got.LastSeen.Equal(time.Date(2026, 5, 17, 11, 30, 0, 0, time.UTC)) {
		t.Errorf("LastSeen = %v", got.LastSeen)
	}
	if got.RetentionClass != ShadowRetentionDefault {
		t.Errorf("RetentionClass = %q, want shadow_default", got.RetentionClass)
	}
}

// TestExtensionsFields_LegacyDefaults verifies §10.4 backward-compat: a
// finding written by EDGE-141 (no §10.1 fields) reads back with
// source_type defaulted to "local" and the rest of the new fields zero.
func TestExtensionsFields_LegacyDefaults(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	legacyCreated := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	const legacyID = "edge_shadow_legacy_aaaaaaaaaaaaaaaaaaaaaa"
	legacy := map[string]any{
		"finding_id":         legacyID,
		"tenant_id":          "tenant-a",
		"owner_principal_id": "owner-1",
		"principal_id":       "principal-1",
		"agent_product":      "claude-code",
		"risk":               "medium",
		"status":             "detected",
		"evidence_type":      "config_file",
		"evidence_summary":   "legacy summary",
		"redacted_path":      "~/foo",
		"detected_at":        legacyCreated.Format(time.RFC3339Nano),
		"created_at":         legacyCreated.Format(time.RFC3339Nano),
		"updated_at":         legacyCreated.Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.client.Set(ctx, "edge:shadow:finding:"+legacyID, raw, 0).Err(); err != nil {
		t.Fatalf("set legacy: %v", err)
	}
	if err := s.client.ZAdd(ctx, "edge:shadow:index:tenant-a", redis.Z{
		Score:  float64(legacyCreated.UnixMilli()),
		Member: legacyID,
	}).Err(); err != nil {
		t.Fatalf("zadd legacy tenant: %v", err)
	}

	got, err := s.GetFinding(ctx, "tenant-a", legacyID)
	if err != nil {
		t.Fatalf("GetFinding legacy: %v", err)
	}
	if got.SourceType != "local" {
		t.Errorf("legacy SourceType default = %q, want local", got.SourceType)
	}
	if got.ClusterID != "" || got.Namespace != "" || got.CIProvider != "" || got.Repo != "" {
		t.Errorf("legacy non-source fields should be empty: %+v", got)
	}
	if got.RetentionClass != "" {
		t.Errorf("legacy RetentionClass = %q, want empty (falls back to terminalRetention)", got.RetentionClass)
	}
	if len(got.SignalSet) != 0 {
		t.Errorf("legacy SignalSet = %v, want empty", got.SignalSet)
	}
	if got.Confidence != 0 {
		t.Errorf("legacy Confidence = %v, want 0", got.Confidence)
	}

	// Listing legacy finding by source_type=local must surface it via the
	// broad-tenant fallback (legacy has no source index ZADD).
	page, err := s.ListFindings(ctx, ListFindingsQuery{
		TenantID:   "tenant-a",
		SourceType: "local",
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != 1 || page.Findings[0].FindingID != got.FindingID {
		t.Fatalf("source_type=local backward-compat list = %+v, want 1 legacy finding", page.Findings)
	}
}

func TestExtensionsFilters_PerDim(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-a"

	// 5 findings spanning the §10.2 filter dimensions.
	base := fullCreateReq(tenant)
	if _, err := s.CreateFinding(ctx, base); err != nil {
		t.Fatalf("create #1 (k8s/cluster-a): %v", err)
	}

	r2 := fullCreateReq(tenant)
	r2.ClusterID = "cluster-b"
	r2.Namespace = "team-billing"
	r2.SignalSet = []string{"k8s_unmanaged_mcp_service"}
	r2.CIProvider = ""
	r2.Repo = ""
	r2.Confidence = 0.5
	if _, err := s.CreateFinding(ctx, r2); err != nil {
		t.Fatalf("create #2 (k8s/cluster-b): %v", err)
	}

	r3 := fullCreateReq(tenant)
	r3.SourceType = "ci"
	r3.SourceID = "ci-github-1"
	r3.ClusterID = ""
	r3.Namespace = ""
	r3.WorkloadKind = ""
	r3.WorkloadName = ""
	r3.PodUID = ""
	r3.CIProvider = "github_actions"
	r3.Repo = "acme/billing"
	r3.SignalSet = []string{"ci_fork_pr"}
	r3.Confidence = 0.9
	if _, err := s.CreateFinding(ctx, r3); err != nil {
		t.Fatalf("create #3 (ci/billing): %v", err)
	}

	r4 := fullCreateReq(tenant)
	r4.SourceType = "ci"
	r4.SourceID = "ci-github-2"
	r4.ClusterID = ""
	r4.CIProvider = "gitlab_ci"
	r4.Repo = "internal/infra"
	r4.SignalSet = []string{"ci_self_hosted_runner"}
	r4.ExceptionID = "exc-001"
	r4.FalsePositiveReason = "operator_exception"
	r4.Confidence = 0.4
	if _, err := s.CreateFinding(ctx, r4); err != nil {
		t.Fatalf("create #4 (ci/gitlab, exception): %v", err)
	}

	r5 := fullCreateReq(tenant)
	r5.SourceType = "network"
	r5.SourceID = "net-aggregator-1"
	r5.ClusterID = ""
	r5.Namespace = ""
	r5.WorkloadKind = ""
	r5.WorkloadName = ""
	r5.PodUID = ""
	r5.CIProvider = ""
	r5.Repo = ""
	r5.SignalSet = []string{"network_direct_provider_egress"}
	r5.Confidence = 0.95
	if _, err := s.CreateFinding(ctx, r5); err != nil {
		t.Fatalf("create #5 (network): %v", err)
	}

	tests := []struct {
		name     string
		q        ListFindingsQuery
		wantSize int
	}{
		{"SourceType_kubernetes", ListFindingsQuery{TenantID: tenant, SourceType: "kubernetes"}, 2},
		{"SourceType_ci", ListFindingsQuery{TenantID: tenant, SourceType: "ci", IncludeManagedSkip: true}, 2},
		{"SourceType_network", ListFindingsQuery{TenantID: tenant, SourceType: "network"}, 1},
		{"ClusterID_specific", ListFindingsQuery{TenantID: tenant, ClusterID: "prod-east-1"}, 1},
		{"Namespace_team_billing", ListFindingsQuery{TenantID: tenant, Namespace: "team-billing"}, 1},
		{"CIProvider_github", ListFindingsQuery{TenantID: tenant, CIProvider: "github_actions"}, 2},
		{"Repo_billing", ListFindingsQuery{TenantID: tenant, Repo: "acme/billing"}, 1},
		{"SignalAnyOf_heartbeat", ListFindingsQuery{TenantID: tenant, Signals: []string{"k8s_heartbeat_missing"}}, 1},
		{"SignalAnyOf_two", ListFindingsQuery{TenantID: tenant, Signals: []string{"k8s_heartbeat_missing", "ci_fork_pr"}}, 2},
		{"ConfidenceMin_low", ListFindingsQuery{TenantID: tenant, ConfidenceMin: 0.85}, 3},
		{"ExceptionID_exc001", ListFindingsQuery{TenantID: tenant, ExceptionID: "exc-001", IncludeManagedSkip: true}, 1},
		{"IncludeManagedSkip_default_excludes", ListFindingsQuery{TenantID: tenant}, 4},
		{"IncludeManagedSkip_true_includes", ListFindingsQuery{TenantID: tenant, IncludeManagedSkip: true}, 5},
		{"FirstSeenAfter_future", ListFindingsQuery{TenantID: tenant, FirstSeenAfter: timePtr(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}, 0},
		{"LastSeenBefore_far_past", ListFindingsQuery{TenantID: tenant, LastSeenBefore: timePtr(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.ListFindings(ctx, tc.q)
			if err != nil {
				t.Fatalf("ListFindings: %v", err)
			}
			if len(page.Findings) != tc.wantSize {
				ids := make([]string, len(page.Findings))
				for i, f := range page.Findings {
					ids[i] = f.FindingID + "/" + string(f.SourceType) + "/" + f.ClusterID + "/" + f.CIProvider + "/" + f.ExceptionID
				}
				t.Fatalf("got %d findings, want %d; ids=%v", len(page.Findings), tc.wantSize, ids)
			}
		})
	}
}

func TestExtensionsFilters_Combined(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tenant := "tenant-a"

	for i := 0; i < 5; i++ {
		r := fullCreateReq(tenant)
		switch i {
		case 0:
			r.SourceType = "kubernetes"
			r.ClusterID = "cluster-a"
			r.Risk = FindingRiskHigh
		case 1:
			r.SourceType = "kubernetes"
			r.ClusterID = "cluster-b"
			r.Risk = FindingRiskHigh
		case 2:
			r.SourceType = "ci"
			r.ClusterID = ""
			r.Risk = FindingRiskHigh
			r.CIProvider = "github_actions"
			r.Repo = "acme/p"
		case 3:
			r.SourceType = "kubernetes"
			r.ClusterID = "cluster-a"
			r.Risk = FindingRiskLow
		case 4:
			r.SourceType = "kubernetes"
			r.ClusterID = "cluster-a"
			r.Risk = FindingRiskHigh
		}
		if _, err := s.CreateFinding(ctx, r); err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
	}

	page, err := s.ListFindings(ctx, ListFindingsQuery{
		TenantID:   tenant,
		SourceType: "kubernetes",
		ClusterID:  "cluster-a",
		Risk:       FindingRiskHigh,
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != 2 {
		t.Fatalf("AND query = %d findings, want 2 (k8s+cluster-a+high)", len(page.Findings))
	}
	for _, f := range page.Findings {
		if f.SourceType != "kubernetes" || f.ClusterID != "cluster-a" || f.Risk != FindingRiskHigh {
			t.Errorf("AND-query result mismatch: %+v", f)
		}
	}
}

// TestExtensionsCrossCluster_NoTenantLeak is the Q7 binding-governor test:
// the cluster index is NOT tenant-scoped (so multiple tenants share the
// same edge:shadow:index:cluster:<id> ZSET), but the read-time tenant
// gate at finding_store_redis.go:273 must filter out other tenants'
// findings AND must NOT delete them as "stale" — that would be data
// loss across tenants.
func TestExtensionsCrossCluster_NoTenantLeak(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// 3 findings tenant_a × cluster_a/b/c, 1 finding tenant_b × cluster_a.
	cases := []struct {
		tenant, cluster string
	}{
		{"tenant-a", "cluster-a"},
		{"tenant-a", "cluster-b"},
		{"tenant-a", "cluster-c"},
		{"tenant-b", "cluster-a"},
	}
	ids := make([]string, len(cases))
	for i, c := range cases {
		r := fullCreateReq(c.tenant)
		r.ClusterID = c.cluster
		f, err := s.CreateFinding(ctx, r)
		if err != nil {
			t.Fatalf("create #%d %s/%s: %v", i, c.tenant, c.cluster, err)
		}
		ids[i] = f.FindingID
	}

	page, err := s.ListFindings(ctx, ListFindingsQuery{
		TenantID:  "tenant-a",
		ClusterID: "cluster-a",
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != 1 {
		t.Fatalf("cross-cluster query as tenant-a returned %d findings, want 1", len(page.Findings))
	}
	if page.Findings[0].TenantID != "tenant-a" || page.Findings[0].ClusterID != "cluster-a" {
		t.Fatalf("wrong finding leaked: %+v", page.Findings[0])
	}

	// Critical: tenant_b's finding must STILL EXIST after the cross-cluster
	// query (no data-loss cleanup on tenant mismatch).
	tenantBFinding, err := s.GetFinding(ctx, "tenant-b", ids[3])
	if err != nil {
		t.Fatalf("tenant_b finding deleted by tenant_a query: %v (DATA LOSS)", err)
	}
	if tenantBFinding.TenantID != "tenant-b" {
		t.Fatalf("tenant_b finding corrupted: %+v", tenantBFinding)
	}
}

// TestExtensionsIndexes_CreateZADD asserts that CreateFinding ZADDs the
// finding_id into the 4 new §10.5 indexes (source / cluster / repo /
// signal). cluster/repo/signal are conditional on the corresponding
// field being non-empty.
func TestExtensionsIndexes_CreateZADD(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateFinding(ctx, fullCreateReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	checks := []struct {
		key  string
		want bool
	}{
		{"edge:shadow:index:source:kubernetes", true},
		{"edge:shadow:index:cluster:prod-east-1", true},
		{"edge:shadow:index:repo:github_actions:acme/payments", true},
		{"edge:shadow:index:signal:k8s_heartbeat_missing", true},
		{"edge:shadow:index:signal:k8s_unmanaged_process", true},
		// Negatives.
		{"edge:shadow:index:signal:ci_fork_pr", false},
		{"edge:shadow:index:source:local", false},
	}
	for _, c := range checks {
		members, err := s.client.ZRange(ctx, c.key, 0, -1).Result()
		if err != nil {
			t.Fatalf("ZRange %s: %v", c.key, err)
		}
		found := false
		for _, m := range members {
			if m == created.FindingID {
				found = true
				break
			}
		}
		if found != c.want {
			t.Errorf("key=%s membership = %v, want %v; members=%v", c.key, found, c.want, members)
		}
	}

	// A finding with empty cluster/repo/signal must NOT ZADD those.
	req2 := fullCreateReq("tenant-a")
	req2.ClusterID = ""
	req2.CIProvider = ""
	req2.Repo = ""
	req2.SignalSet = nil
	c2, err := s.CreateFinding(ctx, req2)
	if err != nil {
		t.Fatalf("CreateFinding #2: %v", err)
	}
	for _, prefix := range []string{
		"edge:shadow:index:cluster:",
		"edge:shadow:index:repo:",
		"edge:shadow:index:signal:",
	} {
		matches, err := s.client.Keys(ctx, prefix+"*").Result()
		if err != nil {
			t.Fatalf("Keys %s: %v", prefix, err)
		}
		for _, k := range matches {
			members, _ := s.client.ZRange(ctx, k, 0, -1).Result()
			for _, m := range members {
				if m == c2.FindingID {
					t.Errorf("empty-field finding ZADD'd to %s", k)
				}
			}
		}
	}
}

// TestExtensionsIndexes_CleanupZREM asserts that opportunisticCleanup
// removes the finding_id from the source bucket (closed-enum: blast all
// 4 values). Cluster/repo/signal buckets are open-set and may leak
// stale members; this is an accepted compromise that matches the
// existing agent/owner cleanup pattern at finding_store_redis.go:430.
func TestExtensionsIndexes_CleanupZREM(t *testing.T) {
	clock := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	s, _ := newTestStore(t,
		WithClock(func() time.Time { return clock }),
		WithTerminalRetention(time.Hour),
	)
	ctx := context.Background()
	req := fullCreateReq("tenant-a")
	req.RetentionClass = ""
	created, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := s.ResolveFinding(ctx, "tenant-a", created.FindingID, ResolveRequest{
		ResolvedBy: "alice@example.com",
		Reason:     "terminal retention elapsed",
	}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	clock = clock.Add(2 * time.Hour)
	s.opportunisticCleanup(ctx, "tenant-a", []string{created.FindingID})

	members, err := s.client.ZRange(ctx, "edge:shadow:index:source:kubernetes", 0, -1).Result()
	if err != nil {
		t.Fatalf("ZRange source: %v", err)
	}
	for _, m := range members {
		if m == created.FindingID {
			t.Errorf("source index still contains %s after cleanup", created.FindingID)
		}
	}

	// JSON key must be gone too.
	if _, err := s.client.Get(ctx, "edge:shadow:finding:"+created.FindingID).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("finding JSON key still present after cleanup: err=%v", err)
	}
}

// TestExtensionsRetention_PerClass asserts each retention_class drives a
// different terminal TTL: shadow_short=7d, shadow_default=90d,
// shadow_long=365d. Legacy findings (empty retention_class) fall back to
// s.terminalRetention (default 90d).
func TestExtensionsRetention_PerClass(t *testing.T) {
	clock := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	s, _ := newTestStore(t,
		WithClock(func() time.Time { return clock }),
		WithShadowRetentionClasses(map[ShadowFindingRetentionClass]time.Duration{
			ShadowRetentionShort:   7 * 24 * time.Hour,
			ShadowRetentionDefault: 90 * 24 * time.Hour,
			ShadowRetentionLong:    365 * 24 * time.Hour,
		}),
	)
	ctx := context.Background()

	mkResolved := func(rc ShadowFindingRetentionClass) *ShadowAgentFinding {
		r := fullCreateReq("tenant-a")
		r.RetentionClass = rc
		f, err := s.CreateFinding(ctx, r)
		if err != nil {
			t.Fatalf("create rc=%q: %v", rc, err)
		}
		resolved, err := s.ResolveFinding(ctx, "tenant-a", f.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "ok"})
		if err != nil {
			t.Fatalf("resolve rc=%q: %v", rc, err)
		}
		return resolved
	}

	short := mkResolved(ShadowRetentionShort)
	def := mkResolved(ShadowRetentionDefault)
	long := mkResolved(ShadowRetentionLong)

	// Day 8: short expired, default + long not.
	clock = clock.Add(8 * 24 * time.Hour)
	if !s.isExpiredTerminal(short) {
		t.Errorf("short not expired at day 8")
	}
	if s.isExpiredTerminal(def) {
		t.Errorf("default expired prematurely at day 8")
	}
	if s.isExpiredTerminal(long) {
		t.Errorf("long expired prematurely at day 8")
	}

	// Day 95: short + default expired, long not.
	clock = clock.Add(87 * 24 * time.Hour)
	if !s.isExpiredTerminal(def) {
		t.Errorf("default not expired at day 95")
	}
	if s.isExpiredTerminal(long) {
		t.Errorf("long expired prematurely at day 95")
	}

	// Day 370: all expired.
	clock = clock.Add(275 * 24 * time.Hour)
	if !s.isExpiredTerminal(long) {
		t.Errorf("long not expired at day 370")
	}
}

// TestExtensionsRetention_LegacyFallback verifies that a finding with no
// retention_class set falls back to s.terminalRetention (default 90d).
func TestExtensionsRetention_LegacyFallback(t *testing.T) {
	clock := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	s, _ := newTestStore(t, WithClock(func() time.Time { return clock }))
	ctx := context.Background()

	r := fullCreateReq("tenant-a")
	r.RetentionClass = ""
	legacy, err := s.CreateFinding(ctx, r)
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	resolved, err := s.ResolveFinding(ctx, "tenant-a", legacy.FindingID, ResolveRequest{ResolvedBy: "alice", Reason: "ok"})
	if err != nil {
		t.Fatalf("resolve legacy: %v", err)
	}
	// Default fallback is 90d. At day 91 it must be expired.
	clock = clock.Add(91 * 24 * time.Hour)
	if !s.isExpiredTerminal(resolved) {
		t.Errorf("legacy retention fallback failed: not expired at day 91 (terminalRetention=%v)", s.terminalRetention)
	}
}

// TestExtensionsRetention_EnvVarParseError verifies the (Store, error)
// signature of NewRedisStore surfaces env-var parse failures per §10.5
// "positive durations; 0/negative fail at startup".
func TestExtensionsRetention_EnvVarParseError(t *testing.T) {
	cases := []struct {
		name string
		envK string
		envV string
	}{
		{"negative_short", "CORDUM_EDGE_SHADOW_RETENTION_SHORT", "-1h"},
		{"malformed_default", "CORDUM_EDGE_SHADOW_RETENTION_DEFAULT", "not-a-duration"},
		{"zero_long", "CORDUM_EDGE_SHADOW_RETENTION_LONG", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envK, tc.envV)
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
			t.Cleanup(func() { _ = client.Close(); mr.Close() })
			if _, err := NewRedisStore(client); err == nil {
				t.Errorf("%s=%q accepted, want error", tc.envK, tc.envV)
			}
		})
	}
}
