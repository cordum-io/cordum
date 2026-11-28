package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestRetentionProofDIC12 is cited by docs/contract/deletion-proofs.md. The
// only cleanup the entry has is UpdateMemory's trim of mem:<memory>:events to
// the newest defaultMaxHistory (20) events, so later writes to the same memory
// push the marker's two events out. The memory id stays out of the marker: the
// key outlives its events. UpdateMemory writes no chunk or summary key, and
// those have no cleanup (the map's partial-path note). Exports: none.
func TestRetentionProofDIC12(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	svc, err := NewService("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.redis.Close() })
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + hex.EncodeToString(b)
	update := func(prompt, response string) {
		t.Helper()
		if _, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
			MemoryId: "memory-dic12", TenantId: "tenant-dic12", Mode: pb.ContextMode_CONTEXT_MODE_CHAT,
			LogicalPayload: []byte(fmt.Sprintf(`{"prompt":%q}`, prompt)), ModelResponse: []byte(response),
		}); err != nil {
			t.Fatalf("UpdateMemory: %v", err)
		}
	}
	trace := func() []string {
		t.Helper()
		hits, err := tracescan.Find(ctx, svc.redis, marker)
		if err != nil {
			t.Fatalf("tracescan.Find: %v", err)
		}
		return hits
	}
	update("prompt "+marker, "answer "+marker)
	// Each update pushes two events; nine later updates leave the marker's
	// events as the oldest two of twenty, the tenth trims them.
	for i := 0; i < 10; i++ {
		if hits := trace(); !slices.ContainsFunc(hits, func(hit string) bool {
			return strings.HasPrefix(hit, "mem:tenant-dic12/memory-dic12:events:")
		}) {
			t.Fatalf("after %d later updates: no mem:tenant-dic12/memory-dic12:events:* trace; hits=%v", i, hits)
		}
		update(fmt.Sprintf("prompt later-%d", i), fmt.Sprintf("answer later-%d", i))
	}
	if hits := trace(); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}
