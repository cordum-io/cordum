package edge

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/redis/go-redis/v9"
)

// Retention proofs cited by docs/contract/deletion-proofs.md. Each proof writes
// a marker through the production writer, requires tracescan to find it in the
// writer client's keyspace, drives the production cleanup path, then requires
// no trace anywhere in that keyspace. Exports: these writers reach none; they
// write only the Redis keys each proof names (the store's recorder is metrics).

// dic01Families are the DI-C01 key families the session, execution and event
// writers touch: edge:session:<id>, edge:events:<execution> (and its seq key),
// edge:execution:<execution> and the edge:index:* sets (tenant, principal,
// session/execution trace and run, job, session executions and events, event id).
var dic01Families = []string{"edge:session:", "edge:events:", "edge:execution:", "edge:index:"}

func TestRetentionProofDIC01(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	const marker = "pr2-08-dic01-retention"
	writeDIC01Session(t, store, "tenant-dic01", marker, now)
	requireTrace(t, client, marker, dic01Families...)

	sweeper := newDIC01Sweeper(t, store, audit.NewLegalHoldStoreFromClient(client), now)
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce = %d, %v; want the aged session swept", swept, err)
	}
	requireNoTrace(t, client, marker)
}

func TestLegalHoldProofDIC01(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	const tenant, marker = "tenant-dic01-held", "pr2-08-dic01-hold"
	writeDIC01Session(t, store, tenant, marker, now)
	requireTrace(t, client, marker, dic01Families...)

	holds := audit.NewLegalHoldStoreFromClient(client)
	hold, err := holds.CreateHold(ctx, tenant, "litigation", "pr2-08")
	if err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	sweeper := newDIC01Sweeper(t, store, holds, now)
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 0 {
		t.Fatalf("SweepOnce under hold = %d, %v; want nothing swept", swept, err)
	}
	requireTrace(t, client, marker, dic01Families...)

	if err := holds.ReleaseHold(ctx, hold.ID, "pr2-08"); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce after release = %d, %v; want the session swept", swept, err)
	}
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC18: updates keep the previous configuration in
// edge:mcp:upstream:bak:<tenant>:<name>:<ts> with the fixed 30d
// mcpUpstreamBackupTTL. The live record (DI-C33) has no cleanup path, so the
// marker is only in the two superseded versions.
func TestRetentionProofDIC18(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close() })
	registry := NewRedisMCPUpstreamRegistryFromClient(client)
	const marker = "pr2-08-dic18-backup"

	upstream := validMCPUpstream("tenant-dic18", "dic18-upstream")
	upstream.Labels = map[string]string{"proof": marker}
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	upstream.Endpoint = "https://two.example/mcp"
	if err := registry.Update(ctx, &upstream); err != nil {
		t.Fatalf("Update #1: %v", err)
	}
	upstream.Endpoint = "https://three.example/mcp"
	upstream.Labels = map[string]string{"team": "platform"}
	if err := registry.Update(ctx, &upstream); err != nil {
		t.Fatalf("Update #2: %v", err)
	}
	hits := requireTrace(t, client, marker, "edge:mcp:upstream:bak:")
	if len(hits) != 2 || !allHavePrefix(hits, "edge:mcp:upstream:bak:tenant-dic18:dic18-upstream:") {
		t.Fatalf("marker hits = %v, want exactly the two backups", hits)
	}

	mr.FastForward(mcpUpstreamBackupTTL + time.Second)
	requireNoTrace(t, client, marker)
	if _, ok, err := registry.Get(ctx, "tenant-dic18", "dic18-upstream"); err != nil || !ok {
		t.Fatalf("live upstream exists=%v err=%v; want kept", ok, err)
	}
}

// TestRetentionProofDIC30: the reservation and completed replay record live in
// edge:idempotency:<hash> with the fixed 24h defaultIdempotencyTTL.
func TestRetentionProofDIC30(t *testing.T) {
	ctx := context.Background()
	store, client, mr, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	const marker = "pr2-08-dic30-idempotency"
	req := EdgeIdempotencyRequest{TenantID: "tenant-dic30", Endpoint: "edge.events.append", Key: "dic30-key", RequestHash: marker}
	reservation, err := store.ReserveIdempotency(ctx, req)
	if err != nil || reservation.State != EdgeIdempotencyReserved {
		t.Fatalf("ReserveIdempotency = %+v, %v; want reserved", reservation, err)
	}
	if _, err := store.CompleteIdempotency(ctx, req, EdgeIdempotencyResponse{StatusCode: 200, Body: []byte(`{"ok":true}`)}); err != nil {
		t.Fatalf("CompleteIdempotency: %v", err)
	}
	requireTrace(t, client, marker, "edge:idempotency:")

	mr.FastForward(defaultIdempotencyTTL + time.Second)
	requireNoTrace(t, client, marker)
}

// writeDIC01Session writes, through CreateSession, CreateExecution and
// AppendEvents, an ended session whose ids carry marker. Its lifecycle anchor,
// EndedAt, is past DefaultSessionRetentionTTL, the default of
// CORDUM_EDGE_SESSION_RETENTION_TTL.
func writeDIC01Session(t *testing.T, store *RedisStore, tenant, marker string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	session, execution := marker+"-sess", marker+"-exec"
	started := now.Add(-40 * 24 * time.Hour)
	createSessionAndExecution(t, ctx, store, tenant, session, execution, started)
	event := validStoreEvent(tenant, session, execution, marker+"-event", 0, started.Add(2*time.Second), EventKindHookPostToolUse, DecisionAllow)
	if _, err := store.AppendEvents(ctx, []AgentActionEvent{event}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if _, err := store.EndSession(ctx, tenant, session, now.Add(-DefaultSessionRetentionTTL-time.Hour), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
}

func newDIC01Sweeper(t *testing.T, store *RedisStore, holds *audit.LegalHoldStore, now time.Time) *SessionSweeper {
	t.Helper()
	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{
		RetentionTTL: DefaultSessionRetentionTTL,
		Now:          func() time.Time { return now },
		LegalHolds:   holds,
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	return sweeper
}

// requireTrace is the positive control: marker is found, under every family.
func requireTrace(t *testing.T, client redis.UniversalClient, marker string, families ...string) []string {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	for _, family := range families {
		if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("no %s* trace of %q; hits=%v", family, marker, hits)
		}
	}
	return hits
}

func requireNoTrace(t *testing.T, client redis.UniversalClient, marker string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
	}
}

func allHavePrefix(values []string, prefix string) bool {
	for _, value := range values {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
	}
	return true
}
