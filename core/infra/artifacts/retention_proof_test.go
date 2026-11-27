package artifacts

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

// TestRetentionProofDIC10 is cited by docs/contract/deletion-proofs.md. Each
// retention class gets its own marker, written through Put (art:<id> and
// art:meta:<id>) and PutContent (cas:<tenant>:<sha256>) with the class TTLs
// shortened to 1s, 2s and 3s. Moving miniredis past each TTL removes that
// class's keys while the longer classes remain. Exports: these writers reach
// none; they write only the Redis keys named here.
func TestRetentionProofDIC10(t *testing.T) {
	t.Setenv(envArtifactTTLShort, "1s")
	t.Setenv(envArtifactTTLStandard, "2s")
	t.Setenv(envArtifactTTLAudit, "3s")
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + hex.EncodeToString(b)
	s := NewRedisStoreFromClient(client)
	classes := []RetentionClass{RetentionShort, RetentionStandard, RetentionAudit}
	for _, class := range classes {
		m := marker + "-" + string(class)
		meta := Metadata{ContentType: "text/plain", Retention: class, Labels: map[string]string{"proof": m}}
		if _, err := s.Put(ctx, []byte("content "+m), meta); err != nil {
			t.Fatalf("Put(%s): %v", class, err)
		}
		if _, err := s.PutContent(ctx, "tenant-dic10", []byte("content "+m), class); err != nil {
			t.Fatalf("PutContent(%s): %v", class, err)
		}
	}
	// families reports which key families hold the class's marker.
	families := func(class RetentionClass) []string {
		t.Helper()
		hits, err := tracescan.Find(ctx, client, marker+"-"+string(class))
		if err != nil {
			t.Fatalf("tracescan.Find: %v", err)
		}
		var out []string
		for _, family := range []string{"art:", "art:meta:", "cas:tenant-dic10:"} {
			if slices.ContainsFunc(hits, func(hit string) bool {
				return strings.HasPrefix(hit, family) && (family != "art:" || !strings.HasPrefix(hit, "art:meta:"))
			}) {
				out = append(out, family)
			}
		}
		return out
	}
	all := []string{"art:", "art:meta:", "cas:tenant-dic10:"}
	elapsed := time.Duration(0)
	for i, class := range classes {
		ttl := s.ttlFor(class)
		for _, step := range []struct {
			at   time.Duration
			gone int
		}{{ttl - 100*time.Millisecond, i}, {ttl + 100*time.Millisecond, i + 1}} {
			mr.FastForward(step.at - elapsed)
			elapsed = step.at
			for j, other := range classes {
				want := all
				if j < step.gone {
					want = nil
				}
				if got := families(other); !slices.Equal(got, want) {
					t.Fatalf("at %s: %s marker in %v; want %v", elapsed, other, got, want)
				}
			}
		}
	}
	hits, err := tracescan.Find(ctx, client, marker)
	if err != nil || len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v, %v", hits, err)
	}
}
