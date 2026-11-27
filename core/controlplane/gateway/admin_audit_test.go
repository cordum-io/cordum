package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const adminAuditUserBody = `{"username":"audit-user","password":"Audit-Passw0rd!"}`

// adminAuditKeyStore records the secret of the last key it created.
type adminAuditKeyStore struct {
	*auth.RedisKeyStore
	raw string
}

func (k *adminAuditKeyStore) Create(ctx context.Context, key *auth.ManagedKey, rawSecret string) error {
	k.raw = rawSecret
	return k.RedisKeyStore.Create(ctx, key, rawSecret)
}

// adminAuditUsers gives s a Redis user store on plain.
func adminAuditUsers(t *testing.T, s *server, plain *redis.Client) *auth.RedisUserStore {
	t.Helper()
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key","role":"admin","principal_id":"admin","tenant":"default"}]`,
	})
	users := auth.NewRedisUserStoreFromClient(plain)
	provider.SetUserStore(users)
	s.auth = provider
	return users
}

func adminAuditRequest(method, path, body string) *http.Request {
	return withAuth(httptest.NewRequest(method, path, strings.NewReader(body)),
		&auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin"})
}

// adminAuditFail makes every policy audit append through hooked fail.
func adminAuditFail(t *testing.T, hooked *redis.Client) *storeWriterLogBuffer {
	t.Helper()
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	return storeWriterLogs(t)
}

func requireAuditNotStored(t *testing.T, rec *httptest.ResponseRecorder, logs *storeWriterLogBuffer, action, message string) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), message)
	require.Len(t, auditPersistErrors(logs, action), 1)
}

func TestAdminAudit_CreateUserAnswers500(t *testing.T) {
	s, _, hooked, plain := auditPersistServer(t)
	users := adminAuditUsers(t, s, plain)
	logs := adminAuditFail(t, hooked)
	rec := httptest.NewRecorder()
	s.handleCreateUser(rec, adminAuditRequest(http.MethodPost, "/api/v1/users", adminAuditUserBody))

	requireAuditNotStored(t, rec, logs, "create", "user created but its audit entry was not stored")
	_, err := users.GetByUsername(context.Background(), "audit-user", "default")
	require.NoError(t, err, "the user was created before its entry")
}

func TestAdminAudit_CreateKeyRevokesUnstoredKey(t *testing.T) {
	s, _, hooked, plain := auditPersistServer(t)
	keys := &adminAuditKeyStore{RedisKeyStore: auth.NewRedisKeyStoreFromClient(plain)}
	s.keyStore = keys
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	logs := adminAuditFail(t, hooked)
	rec := httptest.NewRecorder()
	s.handleCreateKey(rec, adminAuditRequest(http.MethodPost, "/api/v1/auth/keys", `{"name":"audit-key"}`))

	requireAuditNotStored(t, rec, logs, "create", "API key not created: its audit entry was not stored")
	require.NotEmpty(t, keys.raw)
	require.NotContains(t, rec.Body.String(), keys.raw)
	require.NotContains(t, rec.Body.String(), "secret")
	_, err := keys.ValidateKey(context.Background(), keys.raw)
	require.ErrorIs(t, err, auth.ErrInvalidKey, "the unaudited key still authenticates")
	require.Empty(t, sink.events)
}

func TestAdminAudit_SetConfigAnswers500(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	logs := adminAuditFail(t, hooked)
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, adminAuditRequest(http.MethodPost, "/api/v1/config",
		`{"scope":"system","scope_id":"default","data":{"audit_probe":"on"}}`))

	requireAuditNotStored(t, rec, logs, "set", "config set but its audit entry was not stored")
}

func TestAdminAudit_RevokeKeyStillRevokes(t *testing.T) {
	s, _, hooked, plain := auditPersistServer(t)
	keys := auth.NewRedisKeyStoreFromClient(plain)
	s.keyStore = keys
	raw, err := auth.GenerateRawKey()
	require.NoError(t, err)
	mk := &auth.ManagedKey{Name: "audit-revoke", Tenant: "default", Scopes: []string{}, CreatedAt: time.Now().UTC()}
	require.NoError(t, keys.Create(context.Background(), mk, raw))
	logs := adminAuditFail(t, hooked)
	req := adminAuditRequest(http.MethodDelete, "/api/v1/auth/keys/"+mk.ID, "")
	req.SetPathValue("id", mk.ID)
	rec := httptest.NewRecorder()
	s.handleRevokeKey(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Len(t, auditPersistErrors(logs, "revoke"), 1)
	_, err = keys.ValidateKey(context.Background(), raw)
	require.ErrorIs(t, err, auth.ErrInvalidKey)
}

func TestAdminAudit_CreateUserStoresAndExportsEntry(t *testing.T) {
	s, _, _, plain := auditPersistServer(t)
	adminAuditUsers(t, s, plain)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	rec := httptest.NewRecorder()
	s.handleCreateUser(rec, adminAuditRequest(http.MethodPost, "/api/v1/users", adminAuditUserBody))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created AuthUser
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	require.Equal(t, policybundles.PolicyAuditEntry{
		ID: entry.ID, CreatedAt: entry.CreatedAt, Action: "create", ResourceType: "user",
		ResourceID: created.ID, ResourceName: "audit-user", Tenant: "default", ActorID: "admin", Role: "admin",
		Message: "create user audit-user",
	}, entry)
	require.Len(t, sink.events, 1)
	require.Equal(t, policybundles.AuditEntryToSIEM(entry, s.tenant), sink.events[0])
	api := httptest.NewRecorder()
	s.handleListPolicyAudit(api, adminAuditRequest(http.MethodGet, "/api/v1/policy/audit", ""))
	require.Equal(t, http.StatusOK, api.Code, api.Body.String())
	var response struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(api.Body.Bytes(), &response))
	require.Equal(t, entries, response.Items)
}

func TestAdminAudit_CreatePoolAnswers500(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	seedPoolConfig(t, s, map[string]any{"job.seed": "seed-pool"}, map[string]config.PoolConfig{"seed-pool": {}})
	logs := adminAuditFail(t, hooked)
	req := adminAuditRequest(http.MethodPut, "/api/v1/pools/audit-pool", `{"requires":["docker"]}`)
	req.SetPathValue("name", "audit-pool")
	rec := httptest.NewRecorder()
	s.handleCreatePool(rec, req)

	requireAuditNotStored(t, rec, logs, "create", "pool created but its audit entry was not stored")
}

func TestAdminAudit_DrainPoolStillDrains(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	seedPoolConfig(t, s, map[string]any{"job.seed": "audit-pool"}, map[string]config.PoolConfig{
		"audit-pool": {Status: config.PoolStatusActive},
	})
	logs := adminAuditFail(t, hooked)
	req := adminAuditRequest(http.MethodPost, "/api/v1/pools/audit-pool/drain", `{"timeout_seconds":60}`)
	req.SetPathValue("name", "audit-pool")
	rec := httptest.NewRecorder()
	s.handleDrainPool(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, auditPersistErrors(logs, "drain"), 1)
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	require.NoError(t, err)
	_, poolMap, err := extractPoolsFromConfig(doc)
	require.NoError(t, err)
	require.Equal(t, config.PoolStatusDraining, poolMap["audit-pool"].EffectiveStatus())
}

func TestAdminAudit_RotateWorkerCredentialIssuesNothing(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	ctx := context.Background()
	old, err := s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{TenantID: "default", WorkerID: "worker-audit", CreatedBy: "admin"})
	require.NoError(t, err)
	before, err := s.workerCredentialStore.Get(ctx, "default", "worker-audit")
	require.NoError(t, err)
	logs := adminAuditFail(t, hooked)
	rec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec, adminAuditRequest(http.MethodPost, "/api/v1/workers/credentials", `{"worker_id":"worker-audit"}`))

	requireAuditNotStored(t, rec, logs, "rotate", "worker credential not issued: its audit entry was not stored")
	after, err := s.workerCredentialStore.Get(ctx, "default", "worker-audit")
	require.NoError(t, err)
	require.Equal(t, before, after, "a credential was issued without its entry")
	_, ok, err := s.workerCredentialStore.Verify(ctx, "worker-audit", old.Token)
	require.NoError(t, err)
	require.True(t, ok, "the old token stopped verifying")
}

func TestAdminAudit_CreateAgentAnswers500(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	enableAgentIdentityEntitlement(t, s)
	logs := adminAuditFail(t, hooked)
	rec := httptest.NewRecorder()
	s.handleCreateAgent(rec, adminAuditRequest(http.MethodPost, "/api/v1/agents", `{"name":"audit-agent","owner":"team","risk_tier":"high"}`))

	requireAuditNotStored(t, rec, logs, "create", "agent identity created but its audit entry was not stored")
}

func TestAdminAudit_DeleteAgentStillRevokes(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()
	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{Name: "audit-agent", TenantID: "default", Owner: "team", RiskTier: "high"})
	require.NoError(t, err)
	logs := adminAuditFail(t, hooked)
	req := adminAuditRequest(http.MethodDelete, "/api/v1/agents/"+agent.ID, "")
	req.SetPathValue("id", agent.ID)
	rec := httptest.NewRecorder()
	s.handleDeleteAgent(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Len(t, auditPersistErrors(logs, "revoke"), 1)
	revoked, err := s.agentIdentityStore.Get(ctx, "default", agent.ID)
	require.NoError(t, err)
	require.Equal(t, "active", agent.Status)
	require.Equal(t, "revoked", revoked.Status)
}
