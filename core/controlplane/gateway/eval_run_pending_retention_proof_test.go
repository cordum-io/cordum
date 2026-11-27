package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
)

// TestRetentionProofDIC37 is cited by docs/contract/deletion-proofs.md. The
// gateway's pending eval-run record expires after the fixed evalRunPendingTTL
// (2h). The proof runs on its own miniredis, never newTestGateway's shared
// backend, so its FastForward cannot expire parallel tests' keys. Exports: the
// writer reaches none; it writes only eval:run:pending:<tenant>:<run>.
func TestRetentionProofDIC37(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + hex.EncodeToString(b)
	s := &server{jobStore: store.NewRedisJobStoreFromClient(client)}
	if err := s.saveEvalRunPending(ctx, evalRunPendingRecord{
		RunID: marker + "-run", DatasetID: "dataset-1", Tenant: "tenant-dic37",
		Status: "running", Error: "output " + marker,
	}); err != nil {
		t.Fatalf("saveEvalRunPending: %v", err)
	}
	trace := func() []string {
		t.Helper()
		hits, err := tracescan.Find(ctx, client, marker)
		if err != nil {
			t.Fatalf("tracescan.Find: %v", err)
		}
		return hits
	}
	for _, advance := range []time.Duration{0, evalRunPendingTTL - time.Second} {
		mr.FastForward(advance)
		if hits := trace(); !slices.ContainsFunc(hits, func(hit string) bool {
			return strings.HasPrefix(hit, "eval:run:pending:tenant-dic37:")
		}) {
			t.Fatalf("after %s: no eval:run:pending:tenant-dic37:* trace; hits=%v", advance, hits)
		}
	}
	mr.FastForward(2 * time.Second)
	if hits := trace(); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}
