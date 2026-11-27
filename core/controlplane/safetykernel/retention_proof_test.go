package safetykernel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// kernelRetentionFixture gives a proof its own miniredis and a marker made of
// the test name plus random hex, with no ':' or whitespace.
func kernelRetentionFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// kernelRetentionTrace returns every location of marker in client's keyspace.
func kernelRetentionTrace(t *testing.T, client redis.UniversalClient, marker string) []string {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	return hits
}

// kernelRetentionRequire fails unless family has a hit before cleanup.
func kernelRetentionRequire(t *testing.T, hits []string, family string) {
	t.Helper()
	if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
		t.Fatalf("no %s* trace before cleanup; hits=%v", family, hits)
	}
}

// TestRetentionProofDIC48Velocity is cited by docs/contract/deletion-proofs.md,
// with gateway's TestRetentionProofDIC48. A velocity window
// cordum:velocity:<rule>:<key> expires after the rule window plus 60s. The rule
// id, which is policy config, also labels the velocity metrics, so the marker
// is only in the key value and request id. The key value also reaches a debug
// log line; the writer reaches no other export.
func TestRetentionProofDIC48Velocity(t *testing.T) {
	mr, client, marker := kernelRetentionFixture(t)
	cfg := &config.VelocityConfig{MaxRequests: 10, WindowSeconds: 60, Key: "tenant"}
	if _, _, err := newVelocityChecker(client).CheckAndRecord(context.Background(), "dic48-rule", marker+"-key", marker+"-request", cfg); err != nil {
		t.Fatalf("CheckAndRecord: %v", err)
	}
	kernelRetentionRequire(t, kernelRetentionTrace(t, client, marker), "cordum:velocity:dic48-rule:"+marker+"-key")
	mr.FastForward(time.Duration(cfg.WindowSeconds)*time.Second + 60*time.Second + time.Second)
	if hits := kernelRetentionTrace(t, client, marker); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}

// TestRetentionProofDIC08 is cited by docs/contract/deletion-proofs.md. The
// snapshot history cordum:safety:snapshots keeps the newest snapshotHistoryMax
// (10) entries, so 10 newer snapshots push the marker out while the list stays.
// The in-memory copy, server.snapshots (the ListSnapshots fallback), keeps the
// same 10. The writer reaches no export.
func TestRetentionProofDIC08(t *testing.T) {
	_, client, marker := kernelRetentionFixture(t)
	ctx := context.Background()
	srv := &server{resultClient: client}
	if err := srv.setPolicyWithInvariants(ctx, &config.SafetyPolicy{}, nil, marker, 0); err != nil {
		t.Fatalf("setPolicyWithInvariants(marker): %v", err)
	}
	kernelRetentionRequire(t, kernelRetentionTrace(t, client, marker), "cordum:safety:snapshots")
	for i := range 10 {
		if err := srv.setPolicyWithInvariants(ctx, &config.SafetyPolicy{}, nil, fmt.Sprintf("snapshot-%d", i), 0); err != nil {
			t.Fatalf("setPolicyWithInvariants(%d): %v", i, err)
		}
	}
	if hits := kernelRetentionTrace(t, client, marker); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
	if slices.Contains(srv.snapshots, marker) || srv.snapshot == marker {
		t.Fatalf("in-memory snapshot history still holds the marker: %v", srv.snapshots)
	}
	if n, err := client.LLen(ctx, "cordum:safety:snapshots").Result(); err != nil || n != 10 {
		t.Fatalf("cordum:safety:snapshots should keep 10 entries: n=%d err=%v", n, err)
	}
}
