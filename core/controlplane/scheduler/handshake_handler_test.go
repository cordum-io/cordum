package scheduler

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewHandshakeServiceRequiresSecurityDependencies(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	valid := protocolServiceOptions(fixture)
	cases := []struct {
		name       string
		issuer     *SessionTokenIssuer
		resolver   HandshakeTrustResolver
		challenges HandshakeChallengeStore
		sink       AuditSink
		options    HandshakeServiceOptions
	}{
		{name: "issuer", resolver: &protocolTrustResolver{}, challenges: fixture.service.challenges, sink: fixture.sink, options: valid},
		{name: "resolver", issuer: fixture.issuer, challenges: fixture.service.challenges, sink: fixture.sink, options: valid},
		{name: "challenge store", issuer: fixture.issuer, resolver: &protocolTrustResolver{}, sink: fixture.sink, options: valid},
		{name: "audit", issuer: fixture.issuer, resolver: &protocolTrustResolver{}, challenges: fixture.service.challenges, options: valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHandshakeService(tc.issuer, tc.resolver, tc.challenges, tc.sink, tc.options); err == nil {
				t.Fatal("missing security dependency accepted")
			}
		})
	}
}

func TestNewHandshakeServiceRejectsTypedNilSecurityDependencies(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	options := protocolServiceOptions(fixture)
	var resolver *protocolTrustResolver
	var challenges *RedisHandshakeChallengeStore
	var sink *protocolAuditSink
	cases := []struct {
		name       string
		resolver   HandshakeTrustResolver
		challenges HandshakeChallengeStore
		sink       AuditSink
	}{
		{name: "resolver", resolver: resolver, challenges: fixture.service.challenges, sink: fixture.sink},
		{name: "challenge store", resolver: fixture.resolver, challenges: challenges, sink: fixture.sink},
		{name: "audit", resolver: fixture.resolver, challenges: fixture.service.challenges, sink: sink},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHandshakeService(fixture.issuer, tc.resolver, tc.challenges, tc.sink, options); err == nil {
				t.Fatal("typed-nil security dependency accepted")
			}
		})
	}
}

func TestNewHandshakeServiceRejectsMismatchedPrivateScalar(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	options := protocolServiceOptions(fixture)
	// Deliberately mismatch the private scalar against the (copied) public
	// key to exercise the private/public correspondence check below; there's
	// no non-deprecated constructor for "wrong D, same public point".
	bad := *fixture.schedulerKey
	bad.D = new(big.Int).Sub(fixture.schedulerKey.D, big.NewInt(1)) //nolint:staticcheck // SA1019: see comment above
	if bad.D.Sign() <= 0 {                                          //nolint:staticcheck // SA1019
		bad.D = big.NewInt(2) //nolint:staticcheck // SA1019
	}
	options.SchedulerPrivateKey = &bad
	_, err := NewHandshakeService(fixture.issuer, &protocolTrustResolver{}, fixture.service.challenges, fixture.sink, options)
	if err == nil || !strings.Contains(err.Error(), "P-256") {
		t.Fatalf("mismatched private scalar error = %v", err)
	}
}

func protocolServiceOptions(fixture *protocolHandshakeFixture) HandshakeServiceOptions {
	return HandshakeServiceOptions{
		Audience: WorkerHandshakeAudience, SchedulerID: "cordum-scheduler",
		SchedulerKeyID: "scheduler-key-v1", SchedulerPrivateKey: fixture.schedulerKey,
		Skew: time.Minute, ChallengeTTL: 30 * time.Second, Now: func() time.Time { return fixture.now },
	}
}

func TestRedisHandshakeChallengeStoreCompareAndDelete(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	wrong := proto.Clone(challenge).(*agentv1.WorkerHandshakeChallenge)
	wrong.ServerNonce[0] ^= 1
	status, err := fixture.service.challenges.Consume(context.Background(), wrong)
	if err != nil || status != HandshakeConsumeMismatch {
		t.Fatalf("mismatch consume = (%v, %v)", status, err)
	}
	status, err = fixture.service.challenges.Consume(context.Background(), challenge)
	if err != nil || status != HandshakeConsumeMatched {
		t.Fatalf("exact consume = (%v, %v)", status, err)
	}
	status, err = fixture.service.challenges.Consume(context.Background(), challenge)
	if err != nil || status != HandshakeConsumeMissing {
		t.Fatalf("replay consume = (%v, %v)", status, err)
	}
	created, err := fixture.service.challenges.Create(context.Background(), challenge, time.Minute)
	if err != nil || created {
		t.Fatalf("consumed request/nonce replay create = (%t, %v), want replay tombstone", created, err)
	}
	status, err = fixture.service.challenges.Consume(context.Background(), challenge)
	if err != nil || status != HandshakeConsumeMissing {
		t.Fatalf("rejected replay created challenge state = (%v, %v)", status, err)
	}
	assertNoVictimSession(t, fixture)
	fixture.sessionStore.FastForward(31 * time.Second)
	created, err = fixture.service.challenges.Create(context.Background(), challenge, time.Minute)
	if err != nil || !created {
		t.Fatalf("expired request/nonce tombstones create = (%t, %v), want TTL cleanup", created, err)
	}
}

func TestRedisHandshakeChallengeStoreConcurrentConsumeHasOneWinner(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	const attempts = 16
	results := make(chan HandshakeConsumeStatus, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			status, _ := fixture.service.challenges.Consume(context.Background(), challenge)
			results <- status
		}()
	}
	wait.Wait()
	close(results)
	winners := 0
	for status := range results {
		if status == HandshakeConsumeMatched {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("atomic consume winners = %d, want 1", winners)
	}
}

func TestRedisHandshakeChallengeStoreRejectsDuplicateRequestOrNonce(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	duplicateRequest := proto.Clone(challenge).(*agentv1.WorkerHandshakeChallenge)
	duplicateRequest.ChallengeId = "second-challenge"
	duplicateRequest.ClientNonce[0] ^= 1
	created, err := fixture.service.challenges.Create(context.Background(), duplicateRequest, time.Minute)
	if err != nil || created {
		t.Fatalf("duplicate request create = (%t, %v)", created, err)
	}
	duplicateNonce := proto.Clone(challenge).(*agentv1.WorkerHandshakeChallenge)
	duplicateNonce.ChallengeId = "third-challenge"
	duplicateNonce.RequestId = "different-request"
	created, err = fixture.service.challenges.Create(context.Background(), duplicateNonce, time.Minute)
	if err != nil || created {
		t.Fatalf("duplicate nonce create = (%t, %v)", created, err)
	}
}

func issuedProtocolChallenge(t *testing.T, fixture *protocolHandshakeFixture) *agentv1.WorkerHandshakeChallenge {
	t.Helper()
	packet, err := fixture.service.HandleChallenge(context.Background(), protocolChallengeRequest(t, fixture, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE))
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	return packet.GetWorkerHandshakeChallenge()
}

func TestHandshakeServiceHasNoLegacyJSONMintEntryPoint(t *testing.T) {
	files, err := filepath.Glob("handshake*.go")
	if err != nil {
		t.Fatalf("glob handshake sources: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv != nil && (function.Name.Name == "HandleHandshake" || function.Name.Name == "HandleRenew") {
				t.Fatalf("legacy unsigned JSON mint entry point remains: %s", function.Name.Name)
			}
		}
	}
}
