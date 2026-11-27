package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// TestRetentionProofDIC06 is cited by docs/contract/deletion-proofs.md. The
// governance helper writes a decision record with its own copy of the store's
// write and TTL logic; services own the cleanup, so it is driven through
// RedisDecisionLogStore on the same client: the record TTL, a later append's
// index trim for the same tenant and verdict, and an unfiltered query. Only
// cleanup that runs without anyone naming the marker's values is driven.
// Exports: the writer reaches none; it writes only the gov:dec: keys named here.
func TestRetentionProofDIC06(t *testing.T) {
	t.Setenv(governanceDecisionTTLSeconds, "1")
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + hex.EncodeToString(b)
	trace := func(value string, families ...string) {
		t.Helper()
		hits, err := tracescan.Find(ctx, client, value)
		if err != nil {
			t.Fatalf("tracescan.Find: %v", err)
		}
		if len(families) == 0 && len(hits) != 0 {
			t.Fatalf("trace of %q remains after cleanup: %v", value, hits)
		}
		for _, family := range families {
			if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
				t.Fatalf("no %s* trace of %q; hits=%v", family, value, hits)
			}
		}
	}
	const tenant = "tenant-dic06"
	record := governanceDecisionLogRecord{
		Tenant: tenant, JobID: marker + "-job", RuleID: marker + "-rule", AgentID: marker + "-agent",
		Topic: "job." + marker, Reason: "reason " + marker, Verdict: governanceVerdictDeny,
		Timestamp: time.Now().UTC().UnixMilli(),
	}
	runtime := &governanceRuntime{rawClient: client, decisionTTL: governanceDecisionTTL()}
	if err := runtime.appendDecisionRecord(ctx, record); err != nil {
		t.Fatalf("appendDecisionRecord: %v", err)
	}
	recordID := governanceDecisionRecordID(record)
	trace(marker, "gov:dec:tenant-dic06:rec:",
		"gov:dec:tenant-dic06:idx:rule:", "gov:dec:tenant-dic06:idx:agent:", "gov:dec:tenant-dic06:idx:topic:")
	families := []string{"gov:dec:tenant-dic06:rec:", "gov:dec:tenant-dic06:idx:t:",
		"gov:dec:tenant-dic06:idx:rule:", "gov:dec:tenant-dic06:idx:agent:", "gov:dec:tenant-dic06:idx:topic:",
		"gov:dec:tenant-dic06:idx:verdict:deny:"}
	trace(recordID, families...)
	mr.FastForward(time.Second - time.Millisecond)
	trace(recordID, families...)
	mr.FastForward(time.Second)
	// The index trim cutoff reads the wall clock, not miniredis time.
	time.Sleep(time.Until(time.UnixMilli(record.Timestamp).Add(time.Second + 50*time.Millisecond)))
	s := infraStore.NewRedisDecisionLogStoreFromClient(client)
	if err := s.AppendDecision(ctx, model.DecisionLogRecord{
		Tenant: tenant, JobID: "job-later", RuleID: "rule-later", AgentID: "agent-later",
		Topic: "job.later", Verdict: model.SafetyDeny,
	}); err != nil {
		t.Fatalf("later AppendDecision: %v", err)
	}
	if _, err := s.QueryDecisions(ctx, model.DecisionQuery{Tenant: tenant}); err != nil {
		t.Fatalf("unfiltered QueryDecisions: %v", err)
	}
	trace(marker)
	trace(recordID)
}
