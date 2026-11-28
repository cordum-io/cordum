package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// Retention proof cited by docs/contract/deletion-proofs.md. It writes markers
// through SaveSnapshot, requires tracescan to find them in every key family it
// names, drives the history trim or moves miniredis just past the retention,
// then requires no trace in the writer client's keyspace.
// Exports: SaveSnapshot reaches none; it writes only the Redis keys named here.

// TestRetentionProofDIC13 uses the store's fixed defaults (30d retention, 180
// history entries; neither has a setting). A marker snapshot carries two
// markers: the text marker in InstallID and Version, and its dated key name,
// which is also its cordum:telemetry:history member. CollectedAt values stay
// near now, one second apart, so the history score cutoff keeps them.
// cordum:telemetry:snapshot:last is DI-C14 (no cleanup path): only a newer
// snapshot replaces it, so each case saves newer snapshots after the marker.
func TestRetentionProofDIC13(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }
	t.Run("count_trim", func(t *testing.T) {
		_, client, marker := newProofRedis(t)
		s := NewStoreWithClient(client)
		dated := saveSnapshot(t, s, TelemetryPayload{CollectedAt: at(0), InstallID: marker, Version: marker + "-version"})
		requireTrace(t, client, marker, dated, "cordum:telemetry:snapshot:last")
		requireTrace(t, client, dated, dated, "cordum:telemetry:history:"+dated)
		for i := 1; i <= defaultMaxHistory; i++ {
			saveSnapshot(t, s, TelemetryPayload{CollectedAt: at(i), InstallID: "newer", Version: "newer"})
		}
		requireNoTrace(t, client, marker)
		requireNoTrace(t, client, dated)
	})
	// The TTL case checks the text marker only: the expired key's name stays a
	// history member until the count trim or a later save's cutoff drops it.
	t.Run("ttl", func(t *testing.T) {
		mr, client, marker := newProofRedis(t)
		s := NewStoreWithClient(client)
		dated := saveSnapshot(t, s, TelemetryPayload{CollectedAt: at(0), InstallID: marker, Version: marker + "-version"})
		saveSnapshot(t, s, TelemetryPayload{CollectedAt: at(1), InstallID: "newer", Version: "newer"})
		expireJustPast(t, mr, client, defaultRetention, marker, dated)
		requireNoTrace(t, client, marker)
	})
}

// saveSnapshot saves p and returns its dated key name.
func saveSnapshot(t *testing.T, s *Store, p TelemetryPayload) string {
	t.Helper()
	if err := s.SaveSnapshot(context.Background(), p); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	return snapshotKey(p.CollectedAt)
}

func newProofRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// expireJustPast requires the marker in each family, then again one second
// before ttl, then moves miniredis one second past ttl.
func expireJustPast(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient, ttl time.Duration, marker string, families ...string) {
	t.Helper()
	requireTrace(t, client, marker, families...)
	mr.FastForward(ttl - time.Second)
	requireTrace(t, client, marker, families...)
	mr.FastForward(2 * time.Second)
}

func requireTrace(t *testing.T, client redis.UniversalClient, marker string, families ...string) {
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
