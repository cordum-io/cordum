package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestQueryDecisionsMultiFilterCluster(t *testing.T) {
	base := time.Now().Add(-time.Minute).UnixMilli()
	for _, backend := range []string{"single", "cluster"} {
		t.Run(backend, func(t *testing.T) {
			a, b := miniredis.RunT(t), miniredis.RunT(t)
			var client redis.UniversalClient = testredis.NewClient(t, a.Addr())
			if backend == "cluster" {
				client = testredis.NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
				client.AddHook(testredis.CrossSlotGuard())
			}
			s := NewRedisDecisionLogStoreFromClient(client)
			records := decisionQueryClusterRecords(base)
			for _, record := range records {
				require.NoError(t, s.AppendDecision(context.Background(), record))
			}
			for _, server := range []*miniredis.Miniredis{a, b} {
				if backend == "cluster" || server == a {
					require.True(t, slices.ContainsFunc(server.Keys(), func(key string) bool { return strings.HasPrefix(key, "gov:dec:") }))
				}
			}
			for name, q := range map[string]model.DecisionQuery{
				"two":            {RuleID: "r0", AgentID: "a0"},
				"three":          {RuleID: "r0", AgentID: "a0", Topic: "t0"},
				"four":           {RuleID: "r0", AgentID: "a0", Topic: "t0", Verdict: model.SafetyAllow},
				"bounds":         {RuleID: "r0", AgentID: "a0", Since: base + 4, Until: base + 12},
				"empty":          {RuleID: "missing", AgentID: "a0"},
				"max-limit":      {RuleID: "r0", AgentID: "a0", Limit: model.MaxDecisionQueryLimit},
				"sparse":         {Tenant: "sparse", RuleID: "r0", AgentID: "a0", Limit: 1},
				"skipped-cursor": {Tenant: "sparse", RuleID: "r0", AgentID: "a0", Limit: 1, Cursor: model.EncodeDecisionCursor(base+21, decisionRecordID(model.DecisionLogRecord{Tenant: "sparse", JobID: "21", Timestamp: base + 21}))},
			} {
				t.Run(name, func(t *testing.T) {
					if q.Tenant == "" {
						q.Tenant = "dense"
					}
					if q.Limit == 0 {
						q.Limit = 3
					}
					if q.Since == 0 {
						q.Since, q.Until = base-1, base+100
					}
					decisionQueryClusterPages(t, s, records, q)
				})
			}
		})
	}
}

func TestDecisionQueryClusterSource(t *testing.T) {
	client := testredis.NewClient(t, miniredis.RunT(t).Addr())
	s := NewRedisDecisionLogStoreFromClient(client)
	ctx := context.Background()
	rule, agent := decisionRuleIndexKey("t", "r"), decisionAgentIndexKey("t", "a")
	require.NoError(t, client.ZAdd(ctx, rule, redis.Z{Score: 1, Member: "old"}, redis.Z{Score: 3, Member: "new"}).Err())
	require.NoError(t, client.ZAdd(ctx, agent, redis.Z{Score: 2, Member: "middle"}).Err())
	for _, tc := range []struct {
		name         string
		since, until int64
		source       string
	}{
		{"smallest", 1, 3, agent}, {"bounded-tie", 1, 2, rule}, {"empty", 2, 2, rule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, others, err := s.querySourceKey(ctx, model.DecisionQuery{Tenant: "t", RuleID: "r", AgentID: "a", Since: tc.since, Until: tc.until})
			require.NoError(t, err)
			require.Equal(t, tc.source, source)
			expectedOther := rule
			if source == rule {
				expectedOther = agent
			}
			require.Equal(t, []string{expectedOther}, others)
		})
	}
}

type decisionQueryClusterErrorHook struct {
	redis.Hook
	command string
	failure error
}

func (h decisionQueryClusterErrorHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) > 0 && cmds[0].Name() == h.command {
			if h.command == "zcount" {
				return h.failure
			}
			// A missing member can be the pipeline's first error, hiding a later failure.
			cmds[0].SetErr(redis.Nil)
			cmds[len(cmds)-1].SetErr(h.failure)
			return redis.Nil
		}
		return next(ctx, cmds)
	}
}

func TestDecisionQueryClusterErrors(t *testing.T) {
	for _, command := range []string{"zcount", "zscore"} {
		t.Run(command, func(t *testing.T) {
			client := testredis.NewClient(t, miniredis.RunT(t).Addr())
			s := NewRedisDecisionLogStoreFromClient(client)
			base := time.Now().Add(-time.Minute).UnixMilli()
			for _, r := range decisionQueryClusterRecords(base) {
				require.NoError(t, s.AppendDecision(context.Background(), r))
			}
			failure := errors.New("index unavailable")
			client.AddHook(decisionQueryClusterErrorHook{Hook: testredis.CrossSlotGuard(), command: command, failure: failure})
			_, err := s.QueryDecisions(context.Background(), model.DecisionQuery{Tenant: "dense", RuleID: "r0", AgentID: "a0", Topic: "t0", Since: base - 1, Until: base + 100, Limit: 3})
			if command == "zscore" {
				require.ErrorIs(t, err, failure)
				require.Contains(t, err.Error(), "query decision filter membership:")
			} else {
				require.ErrorIs(t, err, failure)
				require.Contains(t, err.Error(), "count decision filter members:")
			}
		})
	}
}

func decisionQueryClusterRecords(base int64) []model.DecisionLogRecord {
	var records []model.DecisionLogRecord
	for i := range 64 {
		records = append(records, model.DecisionLogRecord{Tenant: "dense", JobID: fmt.Sprint(i),
			RuleID: fmt.Sprintf("r%d", i%2), AgentID: fmt.Sprintf("a%d", i/2%2), Topic: fmt.Sprintf("t%d", i/4%2),
			Verdict: []model.SafetyDecision{model.SafetyAllow, model.SafetyDeny}[i/8%2], Timestamp: base + int64(i/32)*8})
	}
	for i := range 83 {
		rule, agent := "r0", "a0"
		if i >= 3 {
			if i%2 == 0 {
				rule = "r1"
			} else {
				agent = "a1"
			}
		}
		records = append(records, model.DecisionLogRecord{Tenant: "sparse", JobID: fmt.Sprint(i), RuleID: rule,
			AgentID: agent, Topic: "t0", Verdict: model.SafetyAllow, Timestamp: base + int64(i)})
	}
	return records
}

func decisionQueryClusterPages(t *testing.T, s *RedisDecisionLogStore, records []model.DecisionLogRecord, q model.DecisionQuery) {
	t.Helper()
	var want []model.DecisionLogRecord
	for _, r := range records {
		if r.Tenant == q.Tenant && r.Timestamp >= q.Since && r.Timestamp <= q.Until &&
			(q.RuleID == "" || r.RuleID == q.RuleID) && (q.AgentID == "" || r.AgentID == q.AgentID) &&
			(q.Topic == "" || r.Topic == q.Topic) && (q.Verdict == "" || r.Verdict == q.Verdict) {
			if q.Cursor != "" {
				ts, id, err := model.DecodeDecisionCursor(q.Cursor)
				require.NoError(t, err)
				if r.Timestamp > ts || (r.Timestamp == ts && decisionRecordID(r) >= id) {
					continue
				}
			}
			want = append(want, r)
		}
	}
	slices.SortFunc(want, func(a, b model.DecisionLogRecord) int {
		if a.Timestamp != b.Timestamp {
			if a.Timestamp > b.Timestamp {
				return -1
			}
			return 1
		}
		return strings.Compare(decisionRecordID(b), decisionRecordID(a))
	})
	for {
		page, err := s.QueryDecisions(context.Background(), q)
		require.NoError(t, err)
		n := min(len(want), q.Limit)
		require.Len(t, page.Items, n)
		for i := range n {
			require.Equal(t, decisionRecordID(want[i]), decisionRecordID(page.Items[i]))
		}
		cursor := ""
		if n < len(want) {
			cursor = model.EncodeDecisionCursor(want[n-1].Timestamp, decisionRecordID(want[n-1]))
		}
		require.Equal(t, cursor, page.NextCursor)
		if cursor == "" {
			break
		}
		q.Cursor, want = cursor, want[n:]
	}
}
