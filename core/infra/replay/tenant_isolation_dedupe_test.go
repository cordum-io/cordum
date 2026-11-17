package replay

import (
	"testing"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

func TestTenantIsolationReplayStoreKey(t *testing.T) {
	store, srv := newTestStore(t)
	msgID, digest := []byte("msg-shared"), []byte("digest-shared")
	admit := func(tenant string, body []byte) (capsdk.ReplayOutcome, error) {
		return store.Admit(tenant, "sys.job.request", "worker-1", msgID, body, expiry())
	}

	if got, err := admit("tenant-a", digest); err != nil || got != capsdk.ReplayOutcomeFirst {
		t.Fatalf("tenant-a admit = %v, %v; want first", got, err)
	}
	keys := srv.Keys()
	if len(keys) != 1 {
		t.Fatalf("replay keys after tenant-a = %v; want 1", keys)
	}
	rawA, err := srv.Get(keys[0])
	if err != nil {
		t.Fatalf("read tenant-a entry: %v", err)
	}

	// Tenant B's identical message is new to B, not a duplicate of A's.
	if got, err := admit("tenant-b", digest); err != nil || got != capsdk.ReplayOutcomeFirst {
		t.Fatalf("tenant-b identical admit = %v, %v; want first", got, err)
	}
	// Tenant C reuses the message id with another body: new to C, not a conflict with A.
	if got, err := admit("tenant-c", []byte("digest-other")); err != nil || got != capsdk.ReplayOutcomeFirst {
		t.Fatalf("tenant-c admit = %v, %v; want first", got, err)
	}

	if got, err := srv.Get(keys[0]); err != nil || got != rawA {
		t.Fatalf("tenant-a entry changed: %q, %v", got, err)
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if got, err := admit(tenant, digest); err != nil || got != capsdk.ReplayOutcomeDuplicate {
			t.Fatalf("%s redelivery = %v, %v; want duplicate", tenant, got, err)
		}
	}
	if keys := srv.Keys(); len(keys) != 3 {
		t.Fatalf("replay keys = %v; want one per tenant", keys)
	}
}
