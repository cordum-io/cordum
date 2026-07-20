package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

func TestProductionRawBoundaryRejectsNegativeLimits(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(29)))
	for name, mutate := range map[string]func(*ProductionRawBoundary){
		"lifetime":   func(boundary *ProductionRawBoundary) { boundary.MaxLifetime = -time.Second },
		"clock skew": func(boundary *ProductionRawBoundary) { boundary.ClockSkew = -time.Second },
		"raw bytes":  func(boundary *ProductionRawBoundary) { boundary.MaxRawBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
			mutate(boundary)
			calls := 0
			_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw,
				func(context.Context, *agentv1.BusPacket) error { calls++; return nil })
			if !errors.Is(err, ErrProductionAdmissionUnavailable) || calls != 0 {
				t.Fatalf("Handle = (%v, calls=%d), want unavailable before handler", err, calls)
			}
		})
	}
}

func TestProductionRawBoundaryBindsTrustTenantToSession(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	trust := boundary.trust(productionTestSubject, session)
	if trust.Tenant != session.Identity.GetTenantId() {
		t.Fatalf("trust tenant=%q, want authenticated %q", trust.Tenant, session.Identity.GetTenantId())
	}
}

type recordingProductionReplayStore struct {
	expires time.Time
}

func (s *recordingProductionReplayStore) Admit(
	_, _, _ string,
	_, _ []byte,
	expires time.Time,
) (capsdk.ReplayOutcome, error) {
	s.expires = expires
	return capsdk.ReplayOutcomeFirst, nil
}

func TestProductionRawBoundaryRetainsReplayThroughClockSkew(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	packet := productionTestPacket(session.Identity, productionTestMessageID(30))
	replay := &recordingProductionReplayStore{}
	boundary := productionTestBoundary(key, replay)
	boundary.ClockSkew = 45 * time.Second
	raw := signProductionTestPacket(t, key, packet)

	_, err := boundary.Handle(
		context.Background(), productionTestSubject, session, raw,
		func(context.Context, *agentv1.BusPacket) error { return nil },
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	want := packet.GetSignatureMetadata().GetExpiresAt().AsTime().Add(boundary.ClockSkew)
	if !replay.expires.Equal(want) {
		t.Fatalf("replay expiry = %v, want expiry plus skew %v", replay.expires, want)
	}
}
