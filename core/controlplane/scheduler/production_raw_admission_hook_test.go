package scheduler

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	capv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/infra/bus"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"google.golang.org/protobuf/proto"
)

func TestInstallProductionRawAdmissionFreezesWithFirstSubscription(t *testing.T) {
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "false")
	ns := startProductionAdmissionNATS(t)
	target, err := bus.NewNatsBus(ns.ClientURL())
	if err != nil {
		t.Fatalf("new NATS bus: %v", err)
	}
	t.Cleanup(target.Close)
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	resolved := make(chan string, 1)
	resolver := func(_ context.Context, subject string, _ []byte) (AuthenticatedProductionSession, error) {
		resolved <- subject
		return session, nil
	}
	if err := InstallProductionRawAdmission(target, boundary, resolver); err != nil {
		t.Fatalf("install production admission: %v", err)
	}
	handled := make(chan struct{}, 1)
	if err := target.Subscribe(productionTestSubject, "", func(*capv1.BusPacket) error {
		handled <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := InstallProductionRawAdmission(target, boundary, resolver); !errors.Is(err, bus.ErrRawAdmissionFrozen) {
		t.Fatalf("reinstall error = %v, want %v", err, bus.ErrRawAdmissionFrozen)
	}
	if err := target.Publish(productionTestSubject, productionTestPacket(session.Identity, productionTestMessageID(20))); err != nil {
		t.Fatalf("publish unsigned packet: %v", err)
	}
	select {
	case subject := <-resolved:
		if subject != productionTestSubject {
			t.Fatalf("resolved subject = %q, want %q", subject, productionTestSubject)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("installed admission hook was not invoked")
	}
	select {
	case <-handled:
		t.Fatal("unsigned packet reached scheduler handler")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestInstallProductionRawAdmissionRejectsIncompleteConfiguration(t *testing.T) {
	target := &bus.NatsBus{}
	boundary := productionTestBoundary(newProductionTestKey(t), capsdk.NewInMemoryReplayStore())
	resolver := func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return AuthenticatedProductionSession{}, nil
	}
	tests := []struct {
		name     string
		target   *bus.NatsBus
		boundary *ProductionRawBoundary
		resolver ProductionSessionResolver
	}{
		{name: "nil bus", boundary: boundary, resolver: resolver},
		{name: "nil boundary", target: target, resolver: resolver},
		{name: "nil resolver", target: target, boundary: boundary},
		{name: "nil key resolver", target: target, boundary: &ProductionRawBoundary{Replay: boundary.Replay}, resolver: resolver},
		{name: "nil replay store", target: target, boundary: &ProductionRawBoundary{ResolveKey: boundary.ResolveKey}, resolver: resolver},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := InstallProductionRawAdmission(tc.target, tc.boundary, tc.resolver); !errors.Is(err, ErrProductionAdmissionUnavailable) {
				t.Fatalf("install error = %v, want %v", err, ErrProductionAdmissionUnavailable)
			}
		})
	}
}

func startProductionAdmissionNATS(t *testing.T) *natsserver.Server {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("new NATS server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

func TestProductionRawAdmissionHookMapsVerifiedDelivery(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	hook := NewProductionRawAdmissionHook(boundary, func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return session, nil
	})
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(10)))

	first := hook(context.Background(), productionTestSubject, raw)
	if first.Disposition != bus.RawAdmissionAccepted || first.Packet == nil {
		t.Fatalf("first result = %#v, want accepted packet", first)
	}
	digest, err := capsdk.ProductionSignedBodyDigest(raw)
	if err != nil {
		t.Fatalf("signed body digest: %v", err)
	}
	if first.Authority == nil || first.Authority.ActualSubject != productionTestSubject ||
		first.Authority.SessionSubject != session.Subject || first.Authority.TenantID != session.Identity.GetTenantId() ||
		!bytes.Equal(first.Authority.MessageID, productionTestMessageID(10)) ||
		!bytes.Equal(first.Authority.UnsignedDigest, digest[:]) ||
		first.Authority.Identity == session.Identity || !proto.Equal(first.Authority.Identity, session.Identity) {
		t.Fatalf("first authority = %#v, want exact verified transport metadata", first.Authority)
	}
	duplicate := hook(context.Background(), productionTestSubject, raw)
	if duplicate.Disposition != bus.RawAdmissionDuplicate || duplicate.Packet != nil {
		t.Fatalf("duplicate result = %#v, want duplicate without packet", duplicate)
	}
}

func TestProductionRawAdmissionHookRejectsBeforeTrustedPacket(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	hook := NewProductionRawAdmissionHook(boundary, func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return session, nil
	})
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(11)))
	raw[len(raw)-1] ^= 1

	result := hook(context.Background(), productionTestSubject, raw)
	if result.Disposition != bus.RawAdmissionRejected || result.Packet != nil {
		t.Fatalf("tampered result = %#v, want rejected without packet", result)
	}
}

func TestProductionRawAdmissionHookRetriesUnavailableReplay(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, failingProductionReplayStore{})
	hook := NewProductionRawAdmissionHook(boundary, func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return session, nil
	})
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(12)))

	result := hook(context.Background(), productionTestSubject, raw)
	if result.Disposition != bus.RawAdmissionRetry || result.Packet != nil {
		t.Fatalf("unavailable replay result = %#v, want retry without packet", result)
	}
}

func TestProductionRawBoundaryNormalizesReplayBackendError(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, secretProductionReplayStore{})
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(13)))

	_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw, func(context.Context, *capv1.BusPacket) error {
		return nil
	})
	if !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Handle error=%v, want ErrReplayStoreUnavailable", err)
	}
	if strings.Contains(err.Error(), "secret backend detail") {
		t.Fatalf("Handle error exposed replay backend detail: %v", err)
	}
}

type secretProductionReplayStore struct{}

func (secretProductionReplayStore) Admit(string, string, string, []byte, []byte, time.Time) (capsdk.ReplayOutcome, error) {
	return 0, errors.New("secret backend detail")
}
