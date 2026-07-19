package scheduler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandshakeServiceSecurity_ConcurrentAuthenticateMintsExactlyOnce(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolAuthenticate(t, fixture, issuedProtocolChallenge(t, fixture), "")
	const attempts = 16
	results := make(chan *agentv1.BusPacket, attempts)
	errors := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.service.HandleAuthenticate(context.Background(), packet)
			results <- result
			errors <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent authenticate error: %v", err)
		}
	}
	accepted, replayed := countConcurrentResults(t, results)
	if accepted != 1 || replayed != attempts-1 {
		t.Fatalf("accepted=%d replayed=%d, want 1/%d", accepted, replayed, attempts-1)
	}
}

func countConcurrentResults(t *testing.T, results <-chan *agentv1.BusPacket) (int, int) {
	t.Helper()
	accepted, replayed := 0, 0
	for result := range results {
		if result.GetWorkerHandshakeResult().GetAccepted() {
			accepted++
			continue
		}
		if result.GetAuthToken() != "" || result.GetWorkerHandshakeResult().GetRejectionReason() != replayReason() {
			t.Fatalf("unexpected concurrent rejection: %+v", result)
		}
		replayed++
	}
	return accepted, replayed
}

func TestHandshakeServiceSecurity_ClockSkewExactBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		offset time.Duration
		accept bool
	}{
		{name: "positive boundary", offset: time.Minute, accept: true},
		{name: "negative boundary", offset: -time.Minute, accept: true},
		{name: "positive over", offset: time.Minute + time.Nanosecond},
		{name: "negative over", offset: -time.Minute - time.Nanosecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProtocolHandshakeFixture(t)
			defer fixture.cleanup()
			packet := protocolChallengeRequest(t, fixture, issuePurpose())
			packet.CreatedAt = timestamppb.New(fixture.now.Add(tc.offset))
			resignChallengeRequest(t, packet, fixture.workerKey)
			response, err := fixture.service.HandleChallenge(context.Background(), packet)
			if tc.accept && (err != nil || response == nil) {
				t.Fatalf("boundary rejected: response=%+v err=%v", response, err)
			}
			if !tc.accept {
				assertHandshakeErrorReason(t, response, err, clockSkewReason())
			}
		})
	}
}

func TestHandshakeServiceSecurity_ChallengeStoreUnavailableFailsClosed(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	fixture.sessionStore.Close()
	defer fixture.cleanup()
	packet := protocolChallengeRequest(t, fixture, issuePurpose())
	assertChallengeError(t, fixture, packet, internalErrorReason(), "store")
	assertNoVictimSession(t, fixture)
}

func TestHandshakeServiceSecurity_ExpiredChallengeRejectsWithoutMint(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	later := fixture.now.Add(31 * time.Second)
	fixture.service.now = func() time.Time { return later }
	packet := protocolAuthenticate(t, fixture, challenge, "")
	packet.CreatedAt = timestamppb.New(later)
	resignAuthenticate(t, packet, fixture.workerKey)
	assertAuthenticateRejected(t, fixture, packet, challengeExpiredReason())
	assertNoVictimSession(t, fixture)
}

func TestHandshakeServiceSecurity_AuditContainsNoSecrets(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	token := issueBoundForTest(t, fixture, victimSessionBinding())
	packet := protocolAuthenticate(t, fixture, issuedRenewChallenge(t, fixture), token)
	packet.Signature[0] ^= 1
	assertAuthenticateRejected(t, fixture, packet, authenticationFailedReason())
	encoded, err := json.Marshal(fixture.sink.snapshot())
	if err != nil {
		t.Fatalf("marshal audit: %v", err)
	}
	text := string(encoded)
	secrets := []string{token, base64.StdEncoding.EncodeToString(packet.GetSignature()), base64.StdEncoding.EncodeToString(packet.GetWorkerHandshakeAuthenticate().GetChallenge().GetServerNonce())}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("audit leaked secret material: %q", secret)
		}
	}
}

func assertHandshakeErrorReason(t *testing.T, response *agentv1.BusPacket, err error, reason agentv1.WorkerHandshakeRejectionReason) {
	t.Helper()
	handshakeError, ok := err.(*HandshakeError)
	if response != nil || !ok || handshakeError.Reason() != reason {
		t.Fatalf("response=%+v error=%T(%v), want opaque %s", response, err, err, reason)
	}
}
