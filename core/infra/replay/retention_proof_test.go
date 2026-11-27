package replay

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
)

// TestRetentionProofDIC34 is cited by docs/contract/deletion-proofs.md for the
// cap:replay key family; PR2-08-EDGE proves DI-C34's other families. A replay
// entry is kept until its packet expires. The key name is a hash, so the
// markers are the entry key itself and the hex digest it stores. Exports: the
// writer reaches none; it writes only cap:replay:<hash>.
func TestRetentionProofDIC34(t *testing.T) {
	t.Run("cap_replay", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := testredis.NewClient(t, mr.Addr())
		digest := make([]byte, 32)
		if _, err := rand.Read(digest); err != nil {
			t.Fatalf("digest: %v", err)
		}
		now := time.Now()
		s := NewRedisReplayStore(client, WithClock(func() time.Time { return now }))
		messageID := []byte(t.Name() + "-" + hex.EncodeToString(digest[:6]))
		if _, err := s.Admit("tenant-dic34", "sys.job.request", "worker-dic34", messageID, digest, now.Add(time.Minute)); err != nil {
			t.Fatalf("Admit: %v", err)
		}
		markers := []string{s.entryKey("tenant-dic34", "sys.job.request", "worker-dic34", messageID), hex.EncodeToString(digest)}
		find := func(marker string) []string {
			t.Helper()
			hits, err := tracescan.Find(ctx, client, marker)
			if err != nil {
				t.Fatalf("tracescan.Find: %v", err)
			}
			return hits
		}
		for _, advance := range []time.Duration{0, time.Minute - time.Second} {
			mr.FastForward(advance)
			for _, marker := range markers {
				if hits := find(marker); !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, DefaultKeyPrefix) }) {
					t.Fatalf("no %s* trace of %q; hits=%v", DefaultKeyPrefix, marker, hits)
				}
			}
		}
		mr.FastForward(2 * time.Second)
		for _, marker := range markers {
			if hits := find(marker); len(hits) != 0 {
				t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
			}
		}
	})
}
