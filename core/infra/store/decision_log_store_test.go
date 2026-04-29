package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
)

func TestRedisDecisionLogStoreAppendAndQueryRoundTrip(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	record := decisionFixture("tenant-a", "job-1", time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC).UnixMilli())
	if err := store.AppendDecision(ctx, record); err != nil {
		t.Fatalf("AppendDecision() error = %v", err)
	}

	page, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  record.Timestamp - 1000,
		Until:  record.Timestamp + 1000,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("QueryDecisions() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items)=%d want 1", len(page.Items))
	}
	if got := page.Items[0]; got.JobID != record.JobID || got.RuleID != record.RuleID || got.Topic != record.Topic || got.Verdict != record.Verdict {
		t.Fatalf("roundtrip mismatch: %#v", got)
	}
	if page.NextCursor != "" {
		t.Fatalf("NextCursor=%q want empty", page.NextCursor)
	}
}

func TestRedisDecisionLogStoreQueryFilters(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC).UnixMilli()
	records := []model.DecisionLogRecord{
		decisionFixture("tenant-a", "job-1", base-3_000),
		decisionFixture("tenant-a", "job-2", base-2_000),
		decisionFixture("tenant-a", "job-3", base-1_000),
	}
	records[1].RuleID = "rule-special"
	records[2].AgentID = "agent-special"
	records[2].Topic = "topic-special"
	records[2].Verdict = model.SafetyDeny
	for _, record := range records {
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%s) error = %v", record.JobID, err)
		}
	}

	tests := []struct {
		name    string
		query   model.DecisionQuery
		wantJob string
	}{
		{
			name:    "filter by rule",
			query:   model.DecisionQuery{Tenant: "tenant-a", RuleID: "rule-special", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-2",
		},
		{
			name:    "filter by agent",
			query:   model.DecisionQuery{Tenant: "tenant-a", AgentID: "agent-special", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-3",
		},
		{
			name:    "filter by topic",
			query:   model.DecisionQuery{Tenant: "tenant-a", Topic: "topic-special", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-3",
		},
		{
			name:    "filter by verdict",
			query:   model.DecisionQuery{Tenant: "tenant-a", Verdict: model.SafetyDeny, Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-3",
		},
		{
			name:    "multiple filters use intersection",
			query:   model.DecisionQuery{Tenant: "tenant-a", Topic: "topic-special", AgentID: "agent-special", Verdict: model.SafetyDeny, Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-3",
		},
		{
			name:    "time range",
			query:   model.DecisionQuery{Tenant: "tenant-a", Since: base - 2_100, Until: base - 1_900, Limit: 10},
			wantJob: "job-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := store.QueryDecisions(ctx, tt.query)
			if err != nil {
				t.Fatalf("QueryDecisions() error = %v", err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("len(page.Items)=%d want 1", len(page.Items))
			}
			if page.Items[0].JobID != tt.wantJob {
				t.Fatalf("JobID=%q want %q", page.Items[0].JobID, tt.wantJob)
			}
		})
	}
}

func TestRedisDecisionLogStoreSingleFilterQueriesUseSecondaryIndexes(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 10, 30, 0, 0, time.UTC).UnixMilli()
	records := []model.DecisionLogRecord{
		decisionFixture("tenant-a", "job-rule", base-4_000),
		decisionFixture("tenant-a", "job-agent", base-3_000),
		decisionFixture("tenant-a", "job-topic", base-2_000),
		decisionFixture("tenant-a", "job-verdict", base-1_000),
		decisionFixture("tenant-b", "job-other-tenant", base-500),
	}
	records[0].RuleID = "rule-only"
	records[0].AgentID = "agent-rule"
	records[0].Topic = "topic-rule"
	records[1].RuleID = "rule-agent"
	records[1].AgentID = "agent-only"
	records[1].Topic = "topic-agent"
	records[2].RuleID = "rule-topic"
	records[2].AgentID = "agent-topic"
	records[2].Topic = "topic-only"
	records[3].RuleID = "rule-verdict"
	records[3].AgentID = "agent-verdict"
	records[3].Topic = "topic-verdict"
	records[3].Verdict = model.SafetyDeny
	records[4].RuleID = "rule-verdict"
	records[4].AgentID = "agent-verdict"
	records[4].Topic = "topic-verdict"
	records[4].Verdict = model.SafetyDeny
	for _, record := range records {
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%s) error = %v", record.JobID, err)
		}
	}
	if err := store.client.Del(ctx, decisionPrimaryIndexKey("tenant-a")).Err(); err != nil {
		t.Fatalf("delete primary index: %v", err)
	}

	tests := []struct {
		name    string
		query   model.DecisionQuery
		wantJob string
	}{
		{
			name:    "rule-only",
			query:   model.DecisionQuery{Tenant: "tenant-a", RuleID: "rule-only", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-rule",
		},
		{
			name:    "agent-only",
			query:   model.DecisionQuery{Tenant: "tenant-a", AgentID: "agent-only", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-agent",
		},
		{
			name:    "topic-only",
			query:   model.DecisionQuery{Tenant: "tenant-a", Topic: "topic-only", Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-topic",
		},
		{
			name:    "verdict-only",
			query:   model.DecisionQuery{Tenant: "tenant-a", Verdict: model.SafetyDeny, Since: base - 10_000, Until: base, Limit: 10},
			wantJob: "job-verdict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := store.QueryDecisions(ctx, tt.query)
			if err != nil {
				t.Fatalf("QueryDecisions() error = %v", err)
			}
			if got := decisionJobIDs(page.Items); len(got) != 1 || got[0] != tt.wantJob {
				t.Fatalf("jobs=%v want [%s]", got, tt.wantJob)
			}
			if page.NextCursor != "" {
				t.Fatalf("NextCursor=%q want empty", page.NextCursor)
			}
		})
	}
}

func TestRedisDecisionLogStoreSingleFilterCursorUsesSecondaryIndex(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 10, 45, 0, 0, time.UTC).UnixMilli()
	records := []model.DecisionLogRecord{
		decisionFixture("tenant-a", "job-1", base),
		decisionFixture("tenant-a", "job-2", base),
		decisionFixture("tenant-a", "job-3", base),
		decisionFixture("tenant-a", "job-4", base-1000),
	}
	for _, record := range records {
		record.Verdict = model.SafetyDeny
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%s) error = %v", record.JobID, err)
		}
	}
	if err := store.client.Del(ctx, decisionPrimaryIndexKey("tenant-a")).Err(); err != nil {
		t.Fatalf("delete primary index: %v", err)
	}

	query := model.DecisionQuery{
		Tenant:  "tenant-a",
		Verdict: model.SafetyDeny,
		Since:   base - 10_000,
		Until:   base + 1_000,
		Limit:   2,
	}
	first, err := store.QueryDecisions(ctx, query)
	if err != nil {
		t.Fatalf("first QueryDecisions() error = %v", err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("len(first.Items)=%d want 2", len(first.Items))
	}
	if first.NextCursor == "" {
		t.Fatal("first.NextCursor unexpectedly empty")
	}

	second, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant:  query.Tenant,
		Verdict: query.Verdict,
		Since:   query.Since,
		Until:   query.Until,
		Limit:   query.Limit,
		Cursor:  first.NextCursor,
	})
	if err != nil {
		t.Fatalf("second QueryDecisions() error = %v", err)
	}
	if len(second.Items) != 2 {
		t.Fatalf("len(second.Items)=%d want 2", len(second.Items))
	}
	if second.NextCursor != "" {
		t.Fatalf("second.NextCursor=%q want empty", second.NextCursor)
	}

	seen := make(map[string]struct{}, 4)
	for _, record := range append(first.Items, second.Items...) {
		seen[record.JobID] = struct{}{}
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d unique jobs across pages, want 4", len(seen))
	}
}

func TestRedisDecisionLogStorePagination(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 11, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < 5; i++ {
		record := decisionFixture("tenant-a", "job-"+string(rune('A'+i)), base-int64(i)*1000)
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%d) error = %v", i, err)
		}
	}

	query := model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  base - 10_000,
		Until:  base + 1_000,
		Limit:  2,
	}
	first, err := store.QueryDecisions(ctx, query)
	if err != nil {
		t.Fatalf("first QueryDecisions() error = %v", err)
	}
	if got := decisionJobIDs(first.Items); len(got) != 2 || got[0] != "job-A" || got[1] != "job-B" {
		t.Fatalf("first page jobs=%v want [job-A job-B]", got)
	}
	if first.NextCursor == "" {
		t.Fatal("first.NextCursor unexpectedly empty")
	}

	second, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  query.Since,
		Until:  query.Until,
		Limit:  query.Limit,
		Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("second QueryDecisions() error = %v", err)
	}
	if got := decisionJobIDs(second.Items); len(got) != 2 || got[0] != "job-C" || got[1] != "job-D" {
		t.Fatalf("second page jobs=%v want [job-C job-D]", got)
	}
	if second.NextCursor == "" {
		t.Fatal("second.NextCursor unexpectedly empty")
	}

	third, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  query.Since,
		Until:  query.Until,
		Limit:  query.Limit,
		Cursor: second.NextCursor,
	})
	if err != nil {
		t.Fatalf("third QueryDecisions() error = %v", err)
	}
	if got := decisionJobIDs(third.Items); len(got) != 1 || got[0] != "job-E" {
		t.Fatalf("third page jobs=%v want [job-E]", got)
	}
	if third.NextCursor != "" {
		t.Fatalf("third.NextCursor=%q want empty", third.NextCursor)
	}
}

func TestRedisDecisionLogStorePaginationHandlesTimestampTies(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 11, 30, 0, 0, time.UTC).UnixMilli()
	records := []model.DecisionLogRecord{
		decisionFixture("tenant-a", "job-1", base),
		decisionFixture("tenant-a", "job-2", base),
		decisionFixture("tenant-a", "job-3", base),
		decisionFixture("tenant-a", "job-4", base-1000),
	}
	for _, record := range records {
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%s) error = %v", record.JobID, err)
		}
	}

	query := model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  base - 10_000,
		Until:  base + 1_000,
		Limit:  2,
	}
	first, err := store.QueryDecisions(ctx, query)
	if err != nil {
		t.Fatalf("first QueryDecisions() error = %v", err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("len(first.Items)=%d want 2", len(first.Items))
	}
	if first.NextCursor == "" {
		t.Fatal("first.NextCursor unexpectedly empty")
	}

	second, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  query.Since,
		Until:  query.Until,
		Limit:  query.Limit,
		Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("second QueryDecisions() error = %v", err)
	}

	seen := make(map[string]struct{}, 4)
	for _, record := range append(first.Items, second.Items...) {
		seen[record.JobID] = struct{}{}
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d unique jobs across pages, want 4", len(seen))
	}
}

func TestRedisDecisionLogStoreDefaultsAndEmptyResults(t *testing.T) {
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < 60; i++ {
		record := decisionFixture("tenant-a", jobIDForIndex(i), base-int64(i)*1000)
		if err := store.AppendDecision(ctx, record); err != nil {
			t.Fatalf("AppendDecision(%d) error = %v", i, err)
		}
	}

	page, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  base - 70_000,
		Until:  base + 1_000,
	})
	if err != nil {
		t.Fatalf("QueryDecisions() error = %v", err)
	}
	if len(page.Items) != model.DefaultDecisionQueryLimit {
		t.Fatalf("len(page.Items)=%d want %d", len(page.Items), model.DefaultDecisionQueryLimit)
	}
	if page.NextCursor == "" {
		t.Fatal("NextCursor unexpectedly empty for default-limit page")
	}

	empty, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-b",
		Since:  base - 70_000,
		Until:  base + 1_000,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("QueryDecisions(empty) error = %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("len(empty.Items)=%d want 0", len(empty.Items))
	}
	if empty.NextCursor != "" {
		t.Fatalf("empty.NextCursor=%q want empty", empty.NextCursor)
	}

	if _, err := store.QueryDecisions(ctx, model.DecisionQuery{
		Tenant: "tenant-a",
		Since:  base - 70_000,
		Until:  base + 1_000,
		Limit:  model.MaxDecisionQueryLimit + 1,
	}); err == nil {
		t.Fatal("expected limit validation error")
	}
}

func TestRedisDecisionLogStoreTTLAppliedOnWrite(t *testing.T) {
	t.Setenv(decisionLogTTLSecondsEnv, "120")
	store, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	record := decisionFixture("tenant-a", "job-ttl", time.Date(2026, time.April, 20, 13, 0, 0, 0, time.UTC).UnixMilli())
	if err := store.AppendDecision(ctx, record); err != nil {
		t.Fatalf("AppendDecision() error = %v", err)
	}

	ttl := srv.TTL(decisionRecordKey(record.Tenant, decisionRecordID(record)))
	if ttl <= 0 || ttl > 120*time.Second {
		t.Fatalf("TTL=%v want >0 and <=120s", ttl)
	}
}

func newTestDecisionLogStore(t *testing.T) (*RedisDecisionLogStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisDecisionLogStore("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("NewRedisDecisionLogStore() error = %v", err)
	}
	return store, srv
}

func decisionFixture(tenant, jobID string, timestamp int64) model.DecisionLogRecord {
	return model.DecisionLogRecord{
		JobID:            jobID,
		Tenant:           tenant,
		AgentID:          "agent-default",
		Topic:            "topic-default",
		Verdict:          model.SafetyAllow,
		RuleID:           "rule-default",
		PolicyVersion:    "policy-v1",
		Reason:           "policy matched",
		ApprovalStatus:   model.ApprovalStatusPending,
		ApprovalDecision: model.ApprovalDecisionApprove,
		Timestamp:        timestamp,
	}
}

func decisionJobIDs(records []model.DecisionLogRecord) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.JobID)
	}
	return out
}

func jobIDForIndex(i int) string {
	return "job-" + strconv.Itoa(i)
}
