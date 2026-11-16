package scheduler

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
)

// TestAudienceMatrixBusHandshake presents every credential on the worker
// authentication handshake (sys.worker.handshake.challenge and .authenticate).
// The worker credential is the registered proof key signing an ISSUE exchange
// with no auth_token. Every other credential runs the same exchange with that
// credential as auth_token and without the registered proof key: its packets
// are signed by a key the scheduler never registered. The protected effect is
// a new active bound session in the session store.
func TestAudienceMatrixBusHandshake(t *testing.T) {
	identity.Run(t, identity.BusHandshake, func(t *testing.T, c identity.Credential) identity.Result {
		matrixProductionMode(t)
		f := newProtocolHandshakeFixture(t)
		t.Cleanup(f.cleanup)
		rdb := redis.NewClient(&redis.Options{Addr: f.sessionStore.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		binding := SessionBinding{
			WorkerID: "worker-victim", AgentID: "agent-victim", Tenant: "tenant-victim",
			Audience: WorkerHandshakeAudience, ProofKeyID: "worker-key-v1", SDKVersion: "v2.13.1",
		}
		kit := newMatrixKit(t, f.issuer, rdb, binding, &f.workerKey.PublicKey, nil, []string{"job.allowed"})
		token, key := "", f.workerKey
		if c != identity.WorkerCredential {
			token, key = kit.token(t, c), generateProtocolKey(t)
		}
		before := activeHandshakeSession(t, f)
		outcome := handshakeExchange(t, f, kit.sender(c, binding.WorkerID), token, key)
		after := activeHandshakeSession(t, f)
		changed := before != after
		return identity.Result{Accepted: changed, Effect: changed, Evidence: fmt.Sprintf(
			"session store snapshot (Accepted from the same snapshot): worker-victim active session %s -> %s; exchange %s",
			before, after, outcome)}
	})
}

// handshakeExchange runs the ISSUE challenge and, when a challenge is issued,
// the authenticate, each sent by sender with token and signed by key.
func handshakeExchange(t *testing.T, f *protocolHandshakeFixture, sender, token string, key *ecdsa.PrivateKey) string {
	t.Helper()
	request := protocolChallengeRequest(t, f, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE)
	request.SenderId, request.AuthToken = sender, token
	signHandshake(t, request, key)
	challenge, err := f.service.HandleChallenge(context.Background(), request)
	if err != nil {
		return "refused at challenge: " + err.Error()
	}
	authenticate := protocolAuthenticate(t, f, challenge.GetWorkerHandshakeChallenge(), token)
	authenticate.SenderId = sender
	signHandshake(t, authenticate, key)
	result, err := f.service.HandleAuthenticate(context.Background(), authenticate)
	if err != nil {
		return "refused at authenticate: " + err.Error()
	}
	return fmt.Sprintf("authenticate answered accepted=%t", result.GetWorkerHandshakeResult().GetAccepted())
}

func signHandshake(t *testing.T, packet *agentv1.BusPacket, key *ecdsa.PrivateKey) {
	t.Helper()
	if err := capsdk.SignTrustHandshake(packet, key); err != nil {
		t.Fatalf("sign handshake packet: %v", err)
	}
}

// activeHandshakeSession reads the worker's active bound-session record.
func activeHandshakeSession(t *testing.T, f *protocolHandshakeFixture) string {
	t.Helper()
	raw, err := f.sessionStore.Get(boundWorkerKey("tenant-victim", "worker-victim"))
	if errors.Is(err, miniredis.ErrKeyNotFound) {
		return "none"
	}
	if err != nil {
		t.Fatalf("read active session: %v", err)
	}
	var record activeRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("decode active session: %v", err)
	}
	return "jti " + record.JTI
}
