package store

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

func BenchmarkQueryDecisions7Day(b *testing.B) {
	srv, err := miniredis.Run()
	if err != nil {
		b.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisDecisionLogStore("redis://" + srv.Addr())
	if err != nil {
		b.Fatalf("NewRedisDecisionLogStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, time.April, 20, 0, 0, 0, 0, time.UTC)
	if err := seedDecisionBenchmarkRecords(ctx, store, base); err != nil {
		b.Fatalf("seed benchmark records: %v", err)
	}

	query := model.DecisionQuery{
		Tenant:  "tenant-bench",
		Since:   base.Add(-7 * 24 * time.Hour).UnixMilli(),
		Until:   base.UnixMilli(),
		Verdict: model.SafetyDeny,
		Limit:   100,
	}

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		page, err := store.QueryDecisions(ctx, query)
		if err != nil {
			b.Fatalf("QueryDecisions() error = %v", err)
		}
		if len(page.Items) == 0 {
			b.Fatal("expected benchmark query to return items")
		}
	}
	b.StopTimer()
	avg := time.Since(start) / time.Duration(b.N)
	if avg >= 500*time.Millisecond {
		b.Fatalf("average query time %v exceeds 500ms", avg)
	}
	b.ReportMetric(float64(avg.Milliseconds()), "ms/query")
}

func seedDecisionBenchmarkRecords(ctx context.Context, store *RedisDecisionLogStore, base time.Time) error {
	const total = 50_000
	const batchSize = 1000

	flush := func(pipe redis.Pipeliner) error {
		_, err := pipe.Exec(ctx)
		return err
	}

	pipe := store.client.TxPipeline()
	pending := 0
	for i := 0; i < total; i++ {
		record := decisionFixture("tenant-bench", jobIDForIndex(i), base.Add(-time.Duration(i%10_080)*time.Minute).UnixMilli())
		record.Topic = "topic-" + strconv.Itoa(i%5)
		if i%7 == 0 {
			record.Verdict = model.SafetyDeny
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		id := decisionRecordID(record)
		verdict, err := record.Verdict.DecisionLogWireValue()
		if err != nil {
			return err
		}
		score := float64(record.Timestamp)
		pipe.HSet(ctx, decisionRecordKey(record.Tenant, id), decisionLogRecordFieldJSON, payload)
		pipe.ZAdd(ctx, decisionPrimaryIndexKey(record.Tenant), redis.Z{Score: score, Member: id})
		pipe.ZAdd(ctx, decisionVerdictIndexKey(record.Tenant, verdict), redis.Z{Score: score, Member: id})
		pipe.ZAdd(ctx, decisionTopicIndexKey(record.Tenant, record.Topic), redis.Z{Score: score, Member: id})
		pending++
		if pending == batchSize {
			if err := flush(pipe); err != nil {
				return err
			}
			pipe = store.client.TxPipeline()
			pending = 0
		}
	}
	if pending > 0 {
		if err := flush(pipe); err != nil {
			return err
		}
	}
	return nil
}
