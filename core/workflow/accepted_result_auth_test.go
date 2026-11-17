package workflow_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/workflow"
	"github.com/stretchr/testify/require"
)

func TestAcceptedResultServiceToken(t *testing.T) {
	setMatrixProductionBus(t)
	for _, tc := range []struct {
		name    string
		enforce bool
		want    bool
	}{
		{"NilTrustNoToken", false, true},
		{"NilTrustInvalidToken", false, true},
		{"NoToken", true, false},
		{"IdentityGateway", true, false},
		{"Expired", true, false},
		{"UntrustedKey", true, false},
		{"IdentityScheduler", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowMatrixFixture(t, tc.enforce)
			token := acceptedResultToken(t, f.key, tc.name)
			runBefore, stepBefore := f.state(t)
			require.Equal(t, string(workflow.RunStatusRunning), runBefore)
			require.Equal(t, string(workflow.StepStatusRunning), stepBefore)
			require.NoError(t, f.publisher.Publish(capsdk.SubjectAcceptedResult, matrixAcceptedResult(token)))
			f.awaitHandled(t)
			runAfter, stepAfter := f.state(t)
			if tc.want {
				require.Equal(t, string(workflow.RunStatusSucceeded), runAfter)
				require.Equal(t, string(workflow.StepStatusSucceeded), stepAfter)
			} else {
				require.Equal(t, runBefore, runAfter)
				require.Equal(t, stepBefore, stepAfter)
			}
		})
	}
}

func acceptedResultToken(t *testing.T, key ed25519.PrivateKey, name string) string {
	t.Helper()
	subject, now := servicetoken.IdentityScheduler, time.Now()
	switch name {
	case "NoToken", "NilTrustNoToken":
		return ""
	case "NilTrustInvalidToken":
		return "not-a-service-token"
	case "IdentityGateway":
		subject = servicetoken.IdentityGateway
	case "Expired":
		now = now.Add(-servicetoken.DefaultServiceTTL - servicetoken.DefaultSkew - time.Minute)
	case "UntrustedKey":
		var err error
		_, key, err = ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
	}
	token, err := servicetoken.MintService(key, matrixKeyID, subject, now)
	require.NoError(t, err)
	return token
}

func TestAcceptedResultRefusalLog(t *testing.T) {
	setMatrixProductionBus(t)
	f := newWorkflowMatrixFixture(t, true)
	token := acceptedResultToken(t, f.key, "IdentityGateway")
	packet := matrixAcceptedResult(token)
	packet.GetJobResult().ErrorMessage = "private-result-payload"
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	runBefore, stepBefore := f.state(t)
	require.NoError(t, f.publisher.Publish(capsdk.SubjectAcceptedResult, packet))
	f.awaitHandled(t)
	runAfter, stepAfter := f.state(t)
	require.Equal(t, runBefore, runAfter)
	require.Equal(t, stepBefore, stepAfter)
	line := strings.TrimSpace(output.String())
	require.NotContains(t, line, token)
	require.NotContains(t, line, packet.GetJobResult().ErrorMessage)
	var entry map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(line), &entry))
	require.Len(t, entry, 4) // time, level, msg, reason; no packet or claims fields.
	require.JSONEq(t, `"WARN"`, string(entry["level"]))
	require.JSONEq(t, `"workflow accepted result refused"`, string(entry["msg"]))
	require.JSONEq(t, `"service_token"`, string(entry["reason"]))
}

func TestAcceptedResultTrust(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, policysign.EnvPublicKeyPrefix) {
			t.Setenv(name, "")
		}
	}
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", "")
	t.Setenv(policysign.EnvDevSigningSeed, "")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv(policysign.EnvSigningKey, base64.StdEncoding.EncodeToString(priv))
	t.Setenv(policysign.EnvSigningKeyID, matrixKeyID)
	loaded, kid, err := policysign.LoadPrivateKeyFromEnv()
	require.NoError(t, err)
	t.Run("OwnKey", func(t *testing.T) {
		trust, err := workflow.AcceptedResultTrust(loaded, kid)
		require.NoError(t, err)
		got, ok := trust.Lookup(kid)
		require.True(t, ok)
		require.Equal(t, pub, got)
		token := acceptedResultToken(t, priv, "IdentityScheduler")
		_, err = servicetoken.VerifyService(token, trust, time.Now())
		require.NoError(t, err)
	})
	t.Run("ConfiguredKeyWins", func(t *testing.T) {
		other, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		t.Setenv(policysign.EnvPublicKeyPrefix+strings.ToUpper(kid), base64.StdEncoding.EncodeToString(other))
		trust, err := workflow.AcceptedResultTrust(loaded, kid)
		require.NoError(t, err)
		got, ok := trust.Lookup(kid)
		require.True(t, ok)
		require.Equal(t, other, got)
	})
	t.Run("MalformedPublicKey", func(t *testing.T) {
		t.Setenv(policysign.EnvPublicKeyPrefix+"X", "not-a-public-key")
		trust, err := workflow.AcceptedResultTrust(loaded, kid)
		require.Error(t, err)
		require.Nil(t, trust)
	})
}

// TestAcceptedResultRedeliveryAfterTokenExpiry: the engine is unavailable for
// the first delivery, so JetStream redelivers after the scheduler token has
// outlived its TTL plus skew. The redelivery carries the publish time, so the
// result still advances its run, once.
func TestAcceptedResultRedeliveryAfterTokenExpiry(t *testing.T) {
	setMatrixProductionBus(t)
	var retryUntil atomic.Int64 // unix nanos, set once the token is minted
	var retried atomic.Bool
	f := newWorkflowJetStreamFixture(t, func(context.Context, *pb.BusPacket) error {
		if retried.CompareAndSwap(false, true) {
			return bus.RetryAfter(errors.New("engine unavailable before processing"), time.Until(time.Unix(0, retryUntil.Load())))
		}
		return nil
	})
	issued := time.Now().Add(3*time.Second - servicetoken.DefaultServiceTTL - servicetoken.DefaultSkew)
	expires := issued.Add(servicetoken.DefaultServiceTTL + servicetoken.DefaultSkew)
	token, err := servicetoken.MintService(f.key, matrixKeyID, servicetoken.IdentityScheduler, issued)
	require.NoError(t, err)
	retryUntil.Store(expires.Add(500 * time.Millisecond).UnixNano())

	runBefore, stepBefore := f.state(t)
	require.NoError(t, f.publisher.Publish(capsdk.SubjectAcceptedResult, matrixAcceptedResult(token)))
	stored := f.stored(t)
	first := f.next(t)
	require.True(t, first.hasPublished && !first.published.IsZero() && first.published.Equal(stored.Time), "first delivery lacks the stored-message time")
	require.False(t, first.published.After(expires), "box was slow: token expired before the first publish")
	_, err = servicetoken.VerifyService(token, f.trust, first.published)
	require.NoError(t, err)
	require.Error(t, first.err, "first delivery did not ask for a retry")
	runMid, stepMid := f.state(t)
	require.Equal(t, runBefore, runMid)
	require.Equal(t, stepBefore, stepMid)

	second := f.next(t)
	require.NoError(t, second.err)
	require.True(t, second.hasPublished && second.published.Equal(first.published), "redelivery changed the publish time")
	_, err = servicetoken.VerifyService(token, f.trust, second.arrived)
	require.ErrorIs(t, err, servicetoken.ErrExpired, "token not expired at the redelivery")
	f.settled(t, stored.Sequence)
	runAfter, stepAfter := f.state(t)
	require.Equal(t, string(workflow.RunStatusSucceeded), runAfter)
	require.Equal(t, string(workflow.StepStatusSucceeded), stepAfter)

	// The accepted packet's token, on a new publish after expiry, is a replay.
	publishRefused(t, f, token, "run-replay")
}

// TestAcceptedResultRefusesStaleTokenOnFreshPublish: a token issued an hour ago
// is refused when freshly published, as the publish time is not in its window.
func TestAcceptedResultRefusesStaleTokenOnFreshPublish(t *testing.T) {
	setMatrixProductionBus(t)
	f := newWorkflowJetStreamFixture(t, nil)
	token, err := servicetoken.MintService(f.key, matrixKeyID, servicetoken.IdentityScheduler, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	publishRefused(t, f, token, "run-stale")
}

// publishRefused publishes an accepted result carrying token for a new Running
// run. It must be delivered once, refused and acked, and leave the run alone.
func publishRefused(t *testing.T, f *workflowMatrixFixture, token, runID string) {
	t.Helper()
	logs := captureWarnLogs(t)
	createMatrixRun(t, f.store, runID)
	runBefore, stepBefore := f.stateOf(t, runID)
	require.Equal(t, string(workflow.RunStatusRunning), runBefore)
	require.Equal(t, string(workflow.StepStatusRunning), stepBefore)
	jobID := runID + ":" + matrixStepID + "@1"
	require.NoError(t, f.publisher.Publish(capsdk.SubjectAcceptedResult, matrixAcceptedResultFor(token, jobID)))
	stored := f.stored(t)
	d := f.next(t)
	require.True(t, d.hasPublished && d.published.Equal(stored.Time), "delivery lacks the stored-message time")
	require.NoError(t, d.err)
	_, err := servicetoken.VerifyService(token, f.trust, d.published)
	require.ErrorIs(t, err, servicetoken.ErrExpired, "a fresh publish here must carry an expired token")
	f.settled(t, stored.Sequence)
	runAfter, stepAfter := f.stateOf(t, runID)
	require.Equal(t, runBefore, runAfter)
	require.Equal(t, stepBefore, stepAfter)
	requireOneRefusal(t, logs, token, servicetoken.IdentityScheduler, "tenant-matrix", jobID)
}

// logSink is a slog destination that is safe to read while bus goroutines write.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func captureWarnLogs(t *testing.T) *logSink {
	t.Helper()
	sink := new(logSink)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

// requireOneRefusal checks the log holds exactly one refusal entry with only the
// fixed time, level, msg and reason fields, and none of the secrets anywhere.
func requireOneRefusal(t *testing.T, logs *logSink, secrets ...string) {
	t.Helper()
	out := logs.String()
	require.NotEmpty(t, out, "no refusal was logged")
	refusals := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var entry map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if string(entry["msg"]) != `"workflow accepted result refused"` {
			continue
		}
		refusals++
		require.Len(t, entry, 4) // time, level, msg, reason
		require.JSONEq(t, `"WARN"`, string(entry["level"]))
		require.JSONEq(t, `"service_token"`, string(entry["reason"]))
	}
	require.Equal(t, 1, refusals)
	for _, secret := range secrets {
		require.NotContains(t, out, secret)
	}
}
