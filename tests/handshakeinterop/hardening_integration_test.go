//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestClientProcessEnvironmentDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("CAP_HANDSHAKE_REDIS_URL", "redis://secret")
	t.Setenv("GITHUB_TOKEN", "ci-secret")
	t.Setenv("PYTHONPATH", "host-python-path")
	t.Setenv("PYTHONHOME", "host-python-home")
	t.Setenv("NODE_PATH", "host-node-path")

	environment := clientProcessEnvironment([]string{"CAP_HANDSHAKE_CASE=valid"})
	values := environmentMap(environment)
	for _, forbidden := range []string{"CAP_HANDSHAKE_REDIS_URL", "GITHUB_TOKEN"} {
		if _, exists := values[forbidden]; exists {
			t.Fatalf("child environment inherited %s", forbidden)
		}
	}
	for _, cleared := range []string{"PYTHONPATH", "PYTHONHOME", "NODE_PATH"} {
		if values[cleared] != "" {
			t.Fatalf("child environment %s=%q, want empty", cleared, values[cleared])
		}
	}
	if values["PYTHONNOUSERSITE"] != "1" || values["CAP_HANDSHAKE_CASE"] != "valid" {
		t.Fatalf("child environment missing isolation or explicit case: %#v", values)
	}
}

func TestOwnedHandshakeStateCleanupIsExact(t *testing.T) {
	server, client := cleanupTestServer(t)
	identity := server.identities["worker"]
	owned := seedOwnedHandshakeState(t, client, identity)
	foreignKey := "session:handshake:request:Zm9yZWlnbg:aWQ"
	client.Set(context.Background(), foreignKey, "foreign", 0)

	before, err := server.ownedHandshakeState(context.Background())
	if err != nil || len(before) != len(owned) {
		t.Fatalf("owned state before cleanup = %d, %v; want %d", len(before), err, len(owned))
	}
	server.cleanupHandshakeState(context.Background())
	after, err := server.ownedHandshakeState(context.Background())
	if err != nil || len(after) != 0 {
		t.Fatalf("owned state after cleanup = %#v, %v; want empty", after, err)
	}
	if value := client.Get(context.Background(), foreignKey).Val(); value != "foreign" {
		t.Fatalf("cleanup changed foreign state: %q", value)
	}
}

func TestRegisteredConstructorCleanupRemovesPartialState(t *testing.T) {
	mr := miniredis.RunT(t)
	owner := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	observer := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = observer.Close() })
	server := &interopServer{t: t, redis: owner, runID: "partial-run", identities: map[string]*interopIdentity{}}
	identity := &interopIdentity{
		workerID: "interop-worker-partial-run", agentID: "agent-worker-partial-run",
		tenantID: "tenant-interop-partial-run",
	}
	server.identities["worker"] = identity
	seedOwnedHandshakeState(t, owner, identity)
	registrar := &cleanupCapture{}
	registerInteropServerCleanup(registrar, server)
	if len(registrar.callbacks) != 1 {
		t.Fatalf("registered cleanup callbacks=%d want=1", len(registrar.callbacks))
	}
	registrar.callbacks[0]()
	keys, err := observer.Keys(context.Background(), "session:*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("partial constructor state after cleanup=%v err=%v", keys, err)
	}
}

func TestWorkerConfigCleanupRestoresExactPriorBytes(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	server := &interopServer{t: t, redis: client, identities: map[string]*interopIdentity{}}
	key := "cfg:system:workers"
	original := []byte(`{"scope":"system","scope_id":"workers","revision":7,"data":{"foreign":{"worker_id":"foreign"}}}`)
	if err := client.Set(context.Background(), key, original, 0).Err(); err != nil {
		t.Fatalf("seed worker config: %v", err)
	}
	server.captureWorkerConfigState()
	client.Set(context.Background(), key, `{"revision":8,"data":{"interop":{}}}`, 0)
	server.captureOwnedWorkerConfigState()
	server.restoreWorkerConfigState(context.Background())
	restored, err := client.Get(context.Background(), key).Bytes()
	if err != nil || string(restored) != string(original) {
		t.Fatalf("restored config=%s err=%v; want exact original", restored, err)
	}
}

func TestWorkerConfigCleanupDeletesRunCreatedKey(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	server := &interopServer{t: t, redis: client, identities: map[string]*interopIdentity{}}
	server.captureWorkerConfigState()
	client.Set(context.Background(), "cfg:system:workers", `{"revision":1}`, 0)
	server.captureOwnedWorkerConfigState()
	server.restoreWorkerConfigState(context.Background())
	if client.Exists(context.Background(), "cfg:system:workers").Val() != 0 {
		t.Fatal("cleanup left run-created worker config key")
	}
}

func TestWorkerConfigCleanupPreservesConcurrentForeignWrite(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	identity := &interopIdentity{workerID: "owned-worker"}
	server := &interopServer{
		t: t, redis: client, identities: map[string]*interopIdentity{"worker": identity},
		config: configsvc.NewFromClient(client),
	}
	ctx := context.Background()
	if err := server.config.Set(ctx, &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: "workers", Data: map[string]any{"foreign": "before"},
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	server.captureWorkerConfigState()
	mutateWorkerConfig(t, server.config, identity.workerID, "owned")
	server.captureOwnedWorkerConfigState()
	mutateWorkerConfig(t, server.config, "concurrent", "after")
	server.restoreWorkerConfigState(ctx)
	doc, err := server.config.Get(ctx, configsvc.ScopeSystem, "workers")
	if err != nil || doc.Data["foreign"] != "before" || doc.Data["concurrent"] != "after" {
		t.Fatalf("foreign config lost: data=%#v err=%v", doc.Data, err)
	}
	if _, exists := doc.Data[identity.workerID]; exists {
		t.Fatal("owned worker remained after concurrent cleanup")
	}
}

func mutateWorkerConfig(t *testing.T, service *configsvc.Service, key, value string) {
	t.Helper()
	err := service.SetWithRetry(context.Background(), configsvc.ScopeSystem, "workers", 3,
		func(doc *configsvc.Document) error {
			doc.Data[key] = value
			return nil
		})
	if err != nil {
		t.Fatalf("mutate worker config: %v", err)
	}
}

type cleanupCapture struct{ callbacks []func() }

func (c *cleanupCapture) Cleanup(callback func()) { c.callbacks = append(c.callbacks, callback) }

func cleanupTestServer(t *testing.T) (*interopServer, redis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	server := &interopServer{t: t, redis: client, runID: "cleanup-run", identities: map[string]*interopIdentity{}}
	server.identities["worker"] = &interopIdentity{
		workerID: "interop-worker-cleanup-run", agentID: "agent-worker-cleanup-run",
		tenantID: "tenant-interop-cleanup-run",
	}
	return server, client
}

func seedOwnedHandshakeState(t *testing.T, client redis.UniversalClient, identity *interopIdentity) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	challenge, err := proto.Marshal(&agentv1.WorkerHandshakeChallenge{
		WorkerId: identity.workerID, TenantId: identity.tenantID,
	})
	if err != nil {
		t.Fatalf("marshal challenge: %v", err)
	}
	keys := map[string][]byte{
		"session:handshake:challenge:b3duZWQ":                           challenge,
		handshakeScopedTestKey("request", identity.workerID, "request"): []byte("challenge-id"),
		handshakeScopedTestKey("nonce", identity.workerID, "nonce"):     []byte("challenge-id"),
		activeSessionKey(identity):                                      []byte(`{"jti":"fresh"}`),
		"session:worker:" + identity.workerID:                           []byte(`{"jti":"legacy"}`),
		revokedSessionPrefix(identity) + "old":                          []byte("1"),
	}
	for key, value := range keys {
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	return keys
}

func handshakeScopedTestKey(kind, workerID, value string) string {
	return handshakeStatePrefix(kind, workerID) + encodeHandshakeStatePart([]byte(value))
}

func environmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		name, value, found := strings.Cut(item, "=")
		if found {
			values[strings.ToUpper(name)] = value
		}
	}
	return values
}

func TestRunnerScriptsEnforceSourceProvenance(t *testing.T) {
	for _, name := range []string{"run.sh", "run.ps1"} {
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		for _, required := range []string{"CAP_HANDSHAKE_CAP_SHA", "list -m -f", "go mod download"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s does not enforce %q", name, required)
			}
		}
	}
}

func TestLinuxRunnerBuildsWheelInVenv(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(".", "run.sh"))
	if err != nil {
		t.Fatalf("read run.sh: %v", err)
	}
	text := string(data)
	for _, required := range []string{"python-builder-venv", `"$builder/bin/python" -m build`} {
		if !strings.Contains(text, required) {
			t.Fatalf("run.sh missing isolated builder marker %q", required)
		}
	}
}
