package edge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func edgeIdempotencyKeys(mr *miniredis.Miniredis) []string {
	var keys []string
	for _, key := range mr.Keys() {
		if strings.HasPrefix(key, "edge:idempotency:") {
			keys = append(keys, key)
		}
	}
	return keys
}

func TestTenantIsolationEdgeIdempotencyKey(t *testing.T) {
	store, _, mr, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	reqFor := func(tenant string) EdgeIdempotencyRequest {
		return EdgeIdempotencyRequest{TenantID: tenant, Endpoint: "POST /api/v1/edge/sessions", Key: "shared-key", RequestHash: "same-request-hash"}
	}
	bodyA, bodyB := []byte(`{"session_id":"sess-a"}`), []byte(`{"session_id":"sess-b"}`)

	a := reqFor("tenant-a")
	if res, err := store.ReserveIdempotency(ctx, a); err != nil || res.State != EdgeIdempotencyReserved {
		t.Fatalf("tenant-a reserve = %+v, %v; want reserved", res, err)
	}
	if _, err := store.CompleteIdempotency(ctx, a, EdgeIdempotencyResponse{StatusCode: 201, Body: bodyA}); err != nil {
		t.Fatalf("tenant-a complete: %v", err)
	}
	keys := edgeIdempotencyKeys(mr)
	if len(keys) != 1 {
		t.Fatalf("edge idempotency keys after tenant-a = %v; want 1", keys)
	}
	keyA := keys[0]
	rawA, err := mr.Get(keyA)
	if err != nil {
		t.Fatalf("read tenant-a record: %v", err)
	}

	// Same endpoint, key and request hash from tenant B: a fresh reservation,
	// never tenant A's completed response, a pending wait or a conflict.
	b := reqFor("tenant-b")
	res, err := store.ReserveIdempotency(ctx, b)
	if err != nil || res.State != EdgeIdempotencyReserved || res.Record == nil || res.Record.TenantID != "tenant-b" {
		t.Fatalf("tenant-b reserve with tenant-a's key = %+v, %v; want its own reservation", res, err)
	}
	if len(res.Record.Response.Body) != 0 {
		t.Fatalf("tenant-b reservation carries a response body %q", res.Record.Response.Body)
	}
	if _, err := store.CompleteIdempotency(ctx, b, EdgeIdempotencyResponse{StatusCode: 201, Body: bodyB}); err != nil {
		t.Fatalf("tenant-b complete: %v", err)
	}
	if res, err := store.ReserveIdempotency(ctx, b); err != nil || res.State != EdgeIdempotencyReplay || !bytes.Equal(res.Record.Response.Body, bodyB) {
		t.Fatalf("tenant-b replay = %+v, %v; want its own body", res, err)
	}

	if got, err := mr.Get(keyA); err != nil || got != rawA {
		t.Fatalf("tenant-a record changed: %q, %v", got, err)
	}
	if res, err := store.ReserveIdempotency(ctx, a); err != nil || res.State != EdgeIdempotencyReplay || !bytes.Equal(res.Record.Response.Body, bodyA) {
		t.Fatalf("tenant-a replay = %+v, %v; want its own body", res, err)
	}
	if keys := edgeIdempotencyKeys(mr); len(keys) != 2 {
		t.Fatalf("edge idempotency keys = %v; want one per tenant", keys)
	}
}
