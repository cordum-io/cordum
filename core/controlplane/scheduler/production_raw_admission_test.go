package scheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const productionTestSubject = "sys.job.result"

func TestProductionRawBoundaryRejectsBeforeHandler(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(1)))

	tests := map[string]func([]byte, *AuthenticatedProductionSession, *ProductionRawBoundary){
		"tampered exact wire": func(raw []byte, _ *AuthenticatedProductionSession, _ *ProductionRawBoundary) {
			raw[len(raw)-1] ^= 0x01
		},
		"session subject mismatch": func(_ []byte, session *AuthenticatedProductionSession, _ *ProductionRawBoundary) {
			session.Subject = "other-worker"
		},
		"payload session identity mismatch": func(_ []byte, session *AuthenticatedProductionSession, _ *ProductionRawBoundary) {
			session.Identity = cloneProductionIdentity(session.Identity)
			session.Identity.TenantId = "other-tenant"
		},
		"unknown local key": func(_ []byte, _ *AuthenticatedProductionSession, boundary *ProductionRawBoundary) {
			boundary.ResolveKey = func(string, string, string) (*ecdsa.PublicKey, error) { return nil, nil }
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := append([]byte(nil), raw...)
			candidateSession := cloneProductionSession(session)
			boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
			mutate(candidate, &candidateSession, boundary)
			calls := 0

			_, err := boundary.Handle(context.Background(), productionTestSubject, candidateSession, candidate, func(context.Context, *agentv1.BusPacket) error {
				calls++
				return nil
			})
			if err == nil {
				t.Fatal("Handle error = nil, want fail-closed rejection")
			}
			if calls != 0 {
				t.Fatalf("handler calls = %d, want 0", calls)
			}
		})
	}
}

func TestProductionRawBoundaryUsesActualSubjectAsAudience(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(2)))
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())

	_, err := boundary.Handle(context.Background(), "sys.job.other", session, raw, func(context.Context, *agentv1.BusPacket) error {
		t.Fatal("wrong-audience packet reached handler")
		return nil
	})
	if !errors.Is(err, capsdk.ErrAudienceMismatch) {
		t.Fatalf("Handle error = %v, want audience mismatch", err)
	}
}

func TestProductionRawBoundaryScopesLocalKeyLookupToAuthenticatedIdentity(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	untrustedIdentity := cloneProductionIdentity(session.Identity)
	untrustedIdentity.TenantId = "attacker-tenant"
	raw := signProductionTestPacket(t, key, productionTestPacket(untrustedIdentity, productionTestMessageID(6)))
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	var resolvedTenant, resolvedSender string
	boundary.ResolveKey = func(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
		resolvedTenant, resolvedSender = tenant, sender
		return &key.PublicKey, nil
	}

	_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw, func(context.Context, *agentv1.BusPacket) error {
		t.Fatal("identity-mismatched packet reached handler")
		return nil
	})
	if !errors.Is(err, capsdk.ErrUnknownKeyID) {
		t.Fatalf("Handle error = %v, want rejection before local key lookup", err)
	}
	if resolvedTenant != "" || resolvedSender != "" {
		t.Fatalf("identity-mismatched packet reached local key lookup: (%q, %q)", resolvedTenant, resolvedSender)
	}
}

func TestProductionRawBoundaryScopesSuccessfulKeyLookupToSession(t *testing.T) {
	key := newProductionTestKey(t)
	session := productionTestSession()
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(7)))
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	var resolvedTenant, resolvedSender string
	boundary.ResolveKey = func(tenant, sender, _ string) (*ecdsa.PublicKey, error) {
		resolvedTenant, resolvedSender = tenant, sender
		return &key.PublicKey, nil
	}

	_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw,
		func(context.Context, *agentv1.BusPacket) error { return nil })
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resolvedTenant != session.Identity.GetTenantId() || resolvedSender != session.Subject {
		t.Fatalf("key lookup scope=(%q,%q), want authenticated session", resolvedTenant, resolvedSender)
	}
}

func TestProductionRawBoundaryDistinguishesRedeliveryFromReplayConflict(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	messageID := productionTestMessageID(3)
	firstPacket := productionTestPacket(session.Identity, messageID)
	firstRaw := signProductionTestPacket(t, key, firstPacket)
	conflictPacket := proto.Clone(firstPacket).(*agentv1.BusPacket)
	conflictPacket.TraceId = "trace-conflicting-body"
	conflictRaw := signProductionTestPacket(t, key, conflictPacket)
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	calls := 0
	handler := func(context.Context, *agentv1.BusPacket) error { calls++; return nil }

	outcome, err := boundary.Handle(context.Background(), productionTestSubject, session, firstRaw, handler)
	if err != nil || outcome != capsdk.ReplayOutcomeFirst {
		t.Fatalf("first Handle = (%v, %v), want first, nil", outcome, err)
	}
	outcome, err = boundary.Handle(context.Background(), productionTestSubject, session, firstRaw, handler)
	if err != nil || outcome != capsdk.ReplayOutcomeDuplicate {
		t.Fatalf("duplicate Handle = (%v, %v), want duplicate, nil", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("handler calls after redelivery = %d, want 1", calls)
	}

	_, err = boundary.Handle(context.Background(), productionTestSubject, session, conflictRaw, handler)
	if !errors.Is(err, capsdk.ErrReplayConflict) {
		t.Fatalf("conflicting Handle error = %v, want replay conflict", err)
	}
	if calls != 1 {
		t.Fatalf("handler calls after conflict = %d, want 1", calls)
	}
}

func TestProductionRawBoundaryFailsClosedWhenReplayStoreUnavailable(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(4)))
	boundary := productionTestBoundary(key, failingProductionReplayStore{})

	_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw, func(context.Context, *agentv1.BusPacket) error {
		t.Fatal("replay-store failure reached handler")
		return nil
	})
	if !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Handle error = %v, want replay store unavailable", err)
	}
}

func TestProductionRawBoundaryErrorsDoNotExposePacketSecrets(t *testing.T) {
	t.Parallel()
	key := newProductionTestKey(t)
	session := productionTestSession()
	packet := productionTestPacket(session.Identity, productionTestMessageID(5))
	packet.AuthToken = "top-secret-session-token"
	raw := signProductionTestPacket(t, key, packet)
	boundary := productionTestBoundary(key, capsdk.NewInMemoryReplayStore())
	verified, verifyErr := capsdk.VerifyProductionPacket(raw, boundary.trust(productionTestSubject, session))
	if verifyErr != nil || len(verified.GetSignature()) == 0 {
		t.Fatalf("extract signed fixture = (%v, %v), want nonempty signature", verified, verifyErr)
	}
	signature := string(verified.GetSignature())
	boundary.ResolveKey = func(string, string, string) (*ecdsa.PublicKey, error) { return nil, errors.New("backend unavailable") }

	_, err := boundary.Handle(context.Background(), productionTestSubject, session, raw, func(context.Context, *agentv1.BusPacket) error { return nil })
	if err == nil {
		t.Fatal("Handle error = nil, want unknown-key rejection")
	}
	if strings.Contains(err.Error(), packet.AuthToken) || strings.Contains(err.Error(), signature) {
		t.Fatalf("error exposed packet secret: %q", err)
	}
}

type failingProductionReplayStore struct{}

func (failingProductionReplayStore) Admit(string, string, string, []byte, []byte, time.Time) (capsdk.ReplayOutcome, error) {
	return 0, capsdk.ErrReplayStoreUnavailable
}

func productionTestBoundary(key *ecdsa.PrivateKey, replay capsdk.ReplayStore) *ProductionRawBoundary {
	return &ProductionRawBoundary{
		Replay: replay,
		ResolveKey: func(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
			if tenant != "tenant-a" || sender != "worker-a" || keyID != "local-key" {
				return nil, nil
			}
			return &key.PublicKey, nil
		},
		Now: func() time.Time { return time.Now() },
	}
}

func productionTestSession() AuthenticatedProductionSession {
	return AuthenticatedProductionSession{
		Subject: "worker-a",
		Identity: &agentv1.IdentityBinding{
			TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "worker-a", DelegationId: "delegation-a",
		},
	}
}

func productionTestPacket(identity *agentv1.IdentityBinding, messageID []byte) *agentv1.BusPacket {
	now := time.Now()
	return &agentv1.BusPacket{
		TraceId: "trace-production", SenderId: "worker-a", CreatedAt: timestamppb.New(now), ProtocolVersion: 1,
		SignatureMetadata: &agentv1.SignatureMetadata{
			ProfileVersion: capsdk.ProductionProfileVersion, Algorithm: capsdk.ProductionAlgorithm,
			MessageId: messageID, Audience: productionTestSubject, ExpiresAt: timestamppb.New(now.Add(2 * time.Minute)), KeyId: "local-key",
		},
		Identity: cloneProductionIdentity(identity),
		Payload: &agentv1.BusPacket_JobResult{JobResult: &agentv1.JobResult{
			JobId: "job-a", WorkerId: "worker-a", Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
			Identity: cloneProductionIdentity(identity),
		}},
	}
}

func signProductionTestPacket(t *testing.T, key *ecdsa.PrivateKey, packet *agentv1.BusPacket) []byte {
	t.Helper()
	raw, err := capsdk.SignProductionPacket(packet, key)
	if err != nil {
		t.Fatalf("SignProductionPacket: %v", err)
	}
	return raw
}

func newProductionTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func productionTestMessageID(last byte) []byte {
	id := make([]byte, 16)
	id[len(id)-1] = last
	return id
}

func cloneProductionSession(session AuthenticatedProductionSession) AuthenticatedProductionSession {
	session.Identity = cloneProductionIdentity(session.Identity)
	return session
}

func cloneProductionIdentity(identity *agentv1.IdentityBinding) *agentv1.IdentityBinding {
	if identity == nil {
		return nil
	}
	return proto.Clone(identity).(*agentv1.IdentityBinding)
}
