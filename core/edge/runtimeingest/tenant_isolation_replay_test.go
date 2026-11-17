package runtimeingest

import (
	"context"
	"slices"
	"testing"
)

func TestTenantIsolationRuntimeReplayNonce(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	// A cap of one fills tenant A's window with its first nonce, so a shared
	// window would also refuse tenant B.
	window := NewReplayWindow(client, ReplayWindowTTL, 1)
	const collector, nonce = "llm:proxy", "nonce-shared-000000000001"

	if accepted, err := window.Reserve(ctx, "tenant-a", collector, nonce); err != nil || !accepted {
		t.Fatalf("tenant-a first nonce = %v, %v; want accepted", accepted, err)
	}
	keyA := mustReplayWindowKey(t, window, "tenant-a", collector)
	membersA, err := mr.Members(keyA)
	if err != nil || len(membersA) != 1 {
		t.Fatalf("tenant-a window = %v, %v; want one member", membersA, err)
	}

	accepted, err := window.Reserve(ctx, "tenant-b", collector, nonce)
	if err != nil || !accepted {
		t.Fatalf("tenant-b reuse of tenant-a's nonce = %v, %v; want accepted", accepted, err)
	}
	if keyB := mustReplayWindowKey(t, window, "tenant-b", collector); !mr.Exists(keyB) || keyB == keyA {
		t.Fatalf("tenant-b nonce not recorded in its own window %q", keyB)
	}

	// Replay protection still holds for each tenant.
	if accepted, err := window.Reserve(ctx, "tenant-a", collector, nonce); err != nil || accepted {
		t.Fatalf("tenant-a replay = %v, %v; want refused", accepted, err)
	}
	if accepted, err := window.Reserve(ctx, "tenant-b", collector, nonce); err != nil || accepted {
		t.Fatalf("tenant-b replay = %v, %v; want refused", accepted, err)
	}
	after, err := mr.Members(keyA)
	if err != nil || !slices.Equal(after, membersA) || mr.TTL(keyA) <= 0 {
		t.Fatalf("tenant-a window changed: %v, %v, ttl %v", after, err, mr.TTL(keyA))
	}
}
