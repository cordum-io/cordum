package workflow_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
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
