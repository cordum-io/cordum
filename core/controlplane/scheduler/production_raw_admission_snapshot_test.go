package scheduler

import (
	"context"
	"crypto/ecdsa"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/infra/bus"
)

func TestProductionRawAdmissionHookSnapshotsBoundaryConfiguration(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	hook := NewProductionRawAdmissionHook(boundary, staticProductionSession(session))
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(40)))

	boundary.ResolveKey = nil
	boundary.Replay = failingProductionReplayStore{}
	boundary.MaxRawBytes = 1
	result := hook(context.Background(), productionTestSubject, raw)
	if result.Disposition != bus.RawAdmissionAccepted || result.Packet == nil {
		t.Fatalf("result after caller mutation = %#v, want frozen accepted configuration", result)
	}
}

func TestProductionRawAdmissionHookSnapshotsResolvedIdentity(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	keyLookup := make(chan struct{})
	releaseLookup := make(chan struct{})
	boundary.ResolveKey = blockingProductionKeyResolver(key, keyLookup, releaseLookup)
	hook := NewProductionRawAdmissionHook(boundary, staticProductionSession(session))
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(41)))
	result := make(chan bus.RawAdmissionResult, 1)
	go func() { result <- hook(context.Background(), productionTestSubject, raw) }()

	awaitProductionSignal(t, keyLookup, "key lookup")
	session.Identity.TenantId = "mutated-after-resolution"
	close(releaseLookup)
	select {
	case admitted := <-result:
		if admitted.Disposition != bus.RawAdmissionAccepted || admitted.Packet == nil {
			t.Fatalf("result after identity mutation = %#v, want accepted snapshot", admitted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for admission")
	}
}

func staticProductionSession(session AuthenticatedProductionSession) ProductionSessionResolver {
	return func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return session, nil
	}
}

func blockingProductionKeyResolver(
	key *ecdsa.PrivateKey,
	started chan<- struct{},
	release <-chan struct{},
) func(string, string, string) (*ecdsa.PublicKey, error) {
	return func(string, string, string) (*ecdsa.PublicKey, error) {
		close(started)
		<-release
		return &key.PublicKey, nil
	}
}

func awaitProductionSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}
