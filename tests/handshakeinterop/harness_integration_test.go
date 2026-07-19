//go:build handshakeinterop

package handshakeinterop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type clientSpec struct {
	language string
	command  string
	args     []string
	identity *interopIdentity
}

type interopHarness struct {
	t       *testing.T
	server  *interopServer
	clients []clientSpec
	tempDir string
}

type clientResult struct {
	Language                string `json:"language"`
	Case                    string `json:"case"`
	Status                  string `json:"status"`
	Issue                   bool   `json:"issue"`
	Renew                   bool   `json:"renew"`
	Rotated                 bool   `json:"rotated"`
	MutationSignatureValid  bool   `json:"mutation_signature_valid"`
	TamperSignatureRejected bool   `json:"tamper_signature_rejected"`
}

func newInteropHarness(t *testing.T) *interopHarness {
	t.Helper()
	redisURL := requiredEnv(t, "CAP_HANDSHAKE_REDIS_URL")
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "false")
	t.Setenv("NATS_PING_INTERVAL", "250ms")
	server := newInteropServer(t, redisURL)
	harness := &interopHarness{t: t, server: server, tempDir: t.TempDir()}
	for _, language := range []string{"go", "python", "node"} {
		server.addIdentity(language)
	}
	server.addIdentity("victim")
	server.addIdentity("inline")
	server.addIdentity("concurrent")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	server.assertNoOwnedHandshakeState(ctx)
	cancel()
	server.enrollIdentities()
	harness.writeKeys()
	harness.clients = loadClientSpecs(t, server.identities)
	server.proveCrossReplica()
	return harness
}

func loadClientSpecs(t *testing.T, identities map[string]*interopIdentity) []clientSpec {
	t.Helper()
	return []clientSpec{
		{language: "go", command: requiredEnv(t, "CAP_HANDSHAKE_GO_CLIENT"), identity: identities["go"]},
		{language: "python", command: requiredEnv(t, "CAP_HANDSHAKE_PYTHON_BIN"),
			args: []string{requiredEnv(t, "CAP_HANDSHAKE_PYTHON_CLIENT")}, identity: identities["python"]},
		{language: "node", command: requiredEnv(t, "CAP_HANDSHAKE_NODE_BIN"),
			args: []string{requiredEnv(t, "CAP_HANDSHAKE_NODE_CLIENT")}, identity: identities["node"]},
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the declared handshake interop gate", name)
	}
	return value
}

func (h *interopHarness) TestInstalledClients(t *testing.T) {
	for _, client := range h.clients {
		client := client
		t.Run(client.language, func(t *testing.T) {
			before := h.mustOwnedSessionState(t)
			result := h.runClient(t, client, "valid")
			if !result.Issue || !result.Renew || !result.Rotated {
				t.Fatalf("%s did not prove ISSUE+RENEW+rotation", client.language)
			}
			after := h.mustOwnedSessionState(t)
			if err := validateInstalledSessionTransition(client.identity, before, after, time.Now().UTC()); err != nil {
				t.Fatalf("%s Redis session authority: %v", client.language, err)
			}
		})
	}
}

func (h *interopHarness) TestNegativeClients(t *testing.T) {
	cases := []string{
		"impersonation", "replay", "skew", "wrong_audience",
		"missing_identity", "missing_trace", "unsupported_version", "tamper",
	}
	for _, client := range h.clients {
		for _, testCase := range cases {
			client, testCase := client, testCase
			t.Run(client.language+"/"+testCase, func(t *testing.T) {
				before := h.mustOwnedSessionState(t)
				result := h.runClient(t, client, testCase)
				if result.Issue || result.Renew || result.Rotated {
					t.Fatalf("negative %s/%s leaked session authority", client.language, testCase)
				}
				h.assertMutationProof(t, testCase, result)
				after := h.mustOwnedSessionState(t)
				if !equalRedisState(before, after) {
					t.Fatalf("negative %s/%s changed Redis authority: %s",
						client.language, testCase, describeRedisStateChange(before, after))
				}
				t.Logf("negative %s/%s redis_authority_delta=0", client.language, testCase)
			})
		}
	}
}

func (h *interopHarness) runClient(t *testing.T, client clientSpec, testCase string) clientResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, client.command, client.args...)
	command.Env = clientProcessEnvironment(h.clientEnvironment(client, testCase))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s client case %s failed: %v; stderr=%s", client.language, testCase, err, bounded(stderr.String()))
	}
	if ctx.Err() != nil {
		t.Fatalf("%s client case %s timed out", client.language, testCase)
	}
	var result clientResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("%s client returned invalid JSON: %v", client.language, err)
	}
	if result.Language != client.language || result.Case != testCase || result.Status != "PASS" {
		t.Fatalf("unexpected %s client result metadata", client.language)
	}
	return result
}

func (h *interopHarness) clientEnvironment(client clientSpec, testCase string) []string {
	identity := h.caseIdentity(client, testCase)
	keyOwner := identity
	if testCase == "impersonation" {
		keyOwner = client.identity
	}
	return []string{
		"CAP_HANDSHAKE_NATS_URL=" + h.server.natsURL(), "CAP_HANDSHAKE_CASE=" + testCase,
		"CAP_HANDSHAKE_WORKER_ID=" + identity.workerID, "CAP_HANDSHAKE_AGENT_ID=" + identity.agentID,
		"CAP_HANDSHAKE_TENANT_ID=" + identity.tenantID, "CAP_HANDSHAKE_PROOF_KEY_ID=" + identity.keyID,
		"CAP_HANDSHAKE_SDK_VERSION=" + client.identity.sdkVersion,
		"CAP_HANDSHAKE_WORKER_PRIVATE_KEY=" + h.workerKeyPath(keyOwner),
		"CAP_HANDSHAKE_SCHEDULER_ID=" + h.server.schedulerID,
		"CAP_HANDSHAKE_SCHEDULER_KEY_ID=" + h.server.schedulerKeyID,
		"CAP_HANDSHAKE_SCHEDULER_PUBLIC_KEY=" + filepath.Join(h.tempDir, "scheduler-public.pem"),
	}
}

func (h *interopHarness) caseIdentity(client clientSpec, testCase string) *interopIdentity {
	if testCase == "impersonation" {
		return h.server.identities["victim"]
	}
	return client.identity
}

func (h *interopHarness) assertMutationProof(t *testing.T, testCase string, result clientResult) {
	t.Helper()
	wantMutation := testCase == "wrong_audience"
	wantTamper := testCase == "tamper"
	if result.MutationSignatureValid != wantMutation || result.TamperSignatureRejected != wantTamper {
		t.Fatalf("case %s mutation proof = (%t,%t), want (%t,%t)", testCase,
			result.MutationSignatureValid, result.TamperSignatureRejected, wantMutation, wantTamper)
	}
}

func (h *interopHarness) mustOwnedSessionState(t *testing.T) redisState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state, err := h.server.ownedSessionState(ctx)
	if err != nil {
		t.Fatalf("read run-owned Redis session authority: %v", err)
	}
	return state
}

func (h *interopHarness) TestLegacySubjects(t *testing.T) {
	connection, err := nats.Connect(h.server.natsURL(), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer connection.Close()
	for _, subject := range []string{"sys.worker.handshake", "sys.worker.handshake.renew"} {
		message, requestErr := connection.Request(subject, []byte(`{"worker_id":"unsigned"}`), 300*time.Millisecond)
		if message != nil || (!errors.Is(requestErr, nats.ErrNoResponders) && !errors.Is(requestErr, nats.ErrTimeout)) {
			t.Fatalf("legacy subject %s produced a reply or unexpected error", subject)
		}
	}
}

func (h *interopHarness) TestReplicaUse(t *testing.T) {
	if !h.server.crossReplicaOK {
		t.Fatal("challenge on replica A was not consumed by replica B with replay fencing")
	}
}

func (h *interopHarness) TestBrokerRestart(t *testing.T) {
	h.server.restartNATS()
	result := h.runClient(t, h.clients[0], "valid")
	if !result.Issue || !result.Renew || !result.Rotated {
		t.Fatal("post-restart client did not re-establish trust")
	}
}

func (h *interopHarness) Close() { h.server.Close() }

func bounded(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512] + "..."
	}
	return value
}
