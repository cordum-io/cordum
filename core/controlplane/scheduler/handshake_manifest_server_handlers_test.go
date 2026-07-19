package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runReplayVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	packet := protocolAuthenticate(t, fixture, challenge, "")
	count := mutationInt(t, vector.Mutation)
	reasons := make(chan string, count)
	invoke := func() {
		attempt := proto.Clone(packet).(*agentv1.BusPacket)
		result, err := fixture.service.HandleAuthenticate(context.Background(), attempt)
		reasons <- handshakeOutcomeReason(result, err)
	}
	if vector.Mutation.Kind == "concurrent_repeat" {
		var group sync.WaitGroup
		group.Add(count)
		for index := 0; index < count; index++ {
			go func() { defer group.Done(); invoke() }()
		}
		group.Wait()
	} else {
		for index := 0; index < count; index++ {
			invoke()
		}
	}
	close(reasons)
	out := serverOutcome{InstallCount: true, MintCount: true}
	for reason := range reasons {
		if reason == "UNSPECIFIED" {
			out.AcceptedTotal++
		} else {
			out.RejectedTotal++
			out.Reason = reason
		}
	}
	return out
}

func runSkewVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolChallengeRequest(t, fixture, issuePurpose())
	packet.CreatedAt = timestamppb.New(fixture.now.Add(time.Duration(mutationInt64(t, vector.Mutation))))
	resignTrustPacket(t, packet, fixture.workerKey)
	result, err := fixture.service.HandleChallenge(context.Background(), packet)
	if err == nil {
		return serverOutcome{Accepted: true, ChallengeTotal: 1, Reason: "UNSPECIFIED"}
	}
	return serverOutcome{Reason: handshakeOutcomeReason(result, err)}
}

func runAudienceVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolChallengeRequest(t, fixture, issuePurpose())
	packet.GetWorkerHandshakeChallengeRequest().Audience = mutationString(t, vector.Mutation)
	resignTrustPacket(t, packet, fixture.workerKey)
	result, err := fixture.service.HandleChallenge(context.Background(), packet)
	return serverOutcome{Reason: handshakeOutcomeReason(result, err)}
}

func runExpiredChallengeVector(t *testing.T, _ serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	fixture.service.now = func() time.Time { return fixture.now.Add(31 * time.Second) }
	packet := protocolAuthenticate(t, fixture, challenge, "")
	packet.CreatedAt = timestamppb.New(fixture.now.Add(31 * time.Second))
	resignTrustPacket(t, packet, fixture.workerKey)
	result, err := fixture.service.HandleAuthenticate(context.Background(), packet)
	return serverOutcome{Reason: handshakeOutcomeReason(result, err)}
}

func runResponseBindingVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	request := protocolChallengeRequest(t, fixture, issuePurpose())
	challengePacket, err := fixture.service.HandleChallenge(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := capsdk.VerifyWorkerHandshakeChallenge(workerTrustConfig(fixture), request, challengePacket, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	authenticate := protocolAuthenticate(t, fixture, challengePacket.GetWorkerHandshakeChallenge(), "")
	result, err := fixture.service.buildHandshakeResult(challengePacket.GetWorkerHandshakeChallenge(), "opaque-session", fixture.now.Add(time.Minute), unspecifiedReason())
	if err != nil {
		t.Fatal(err)
	}
	mutateResultChallenge(t, result.GetWorkerHandshakeResult().GetChallenge(), vector.Mutation)
	resignTrustPacket(t, result, fixture.schedulerKey)
	_, err = capsdk.VerifyWorkerHandshakeResult(workerTrustConfig(fixture), verified, authenticate, result, fixture.now)
	if !errors.Is(err, capsdk.ErrWorkerHandshakeBinding) && !errors.Is(err, capsdk.ErrWorkerHandshakePacket) {
		t.Fatalf("result verification error=%v", err)
	}
	return serverOutcome{Reason: "AUTHENTICATION_FAILED"}
}

func runTokenClaimVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	claims, err := fixture.issuer.newBoundClaims(protocolBinding())
	if err != nil {
		t.Fatal(err)
	}
	mutateSessionClaim(t, &claims, vector.Mutation)
	token, err := fixture.issuer.signClaims(claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.issuer.VerifyBound(context.Background(), token, true); err == nil {
		t.Fatal("mutated token accepted")
	}
	return serverOutcome{Reason: "SESSION_INVALID"}
}

func runRenewBindingVector(t *testing.T, vector serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	prior := issueProtocolSession(t, fixture)
	if vector.ID == "renew_a_to_b" {
		claims, err := fixture.issuer.VerifyBound(context.Background(), prior, false)
		if err != nil {
			t.Fatal(err)
		}
		claims.Subject = mutationString(t, vector.Mutation)
		prior, err = fixture.issuer.signClaims(claims)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		if _, _, err := fixture.issuer.IssueBound(context.Background(), protocolBinding()); err != nil {
			t.Fatal(err)
		}
	}
	challenge := issuedManifestRenewChallenge(t, fixture)
	result, err := fixture.service.HandleAuthenticate(context.Background(), protocolAuthenticate(t, fixture, challenge, prior))
	return serverOutcome{Reason: handshakeOutcomeReason(result, err)}
}

func runUnavailableStoreVector(t *testing.T, _ serverVector) serverOutcome {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	fixture.sessionStore.Close()
	result, err := fixture.service.HandleAuthenticate(context.Background(), protocolAuthenticate(t, fixture, challenge, ""))
	return serverOutcome{Reason: handshakeOutcomeReason(result, err)}
}
