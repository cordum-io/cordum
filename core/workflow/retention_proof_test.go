package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
)

// TestRetentionProofDIC09 is cited by docs/contract/deletion-proofs.md. A hash
// for a workflow job whose run is not stored yet waits in
// wf:run:pending_audit_hash:<job> for the fixed pendingAuditHashTTL (24h, no
// setting). The marker is in the run id; the random 64-hex hash is a second
// marker. The writer reaches no export.
func TestRetentionProofDIC09(t *testing.T) {
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	marker, hash := t.Name()+"-"+randomHex(t, 6), randomHex(t, 32)
	jobID := marker + ":deploy@1"
	if err := NewRedisWorkflowStoreFromClient(client).UpdateAuditHash(context.Background(), jobID, hash); err != nil {
		t.Fatalf("UpdateAuditHash: %v", err)
	}
	key := "wf:run:pending_audit_hash:" + jobID
	trace := func(want []string) {
		t.Helper()
		for _, m := range []string{marker, hash} {
			hits, err := tracescan.Find(context.Background(), client, m)
			if err != nil || !slices.Equal(hits, want) {
				t.Fatalf("trace of %q = %v, %v; want %v", m, hits, err, want)
			}
		}
	}
	trace([]string{key})
	mr.FastForward(pendingAuditHashTTL - time.Second)
	trace([]string{key})
	mr.FastForward(2 * time.Second)
	trace(nil)
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return hex.EncodeToString(b)
}
