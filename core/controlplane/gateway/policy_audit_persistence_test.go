package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const (
	auditPersistLogKey    = "cfg:system:policy_audit"
	auditPersistPolicyKey = "cfg:system:policy"
	auditPersistWriteKey  = "cfg:system:policy_write_audit{cfg:system:policy}"
)

// auditPersistHook calls afterGet once, right after the first GET of getKey,
// and fails every pipeline with a command naming failKey. go-redis also sends
// its connection handshake through client hooks, so failures match a key.
type auditPersistHook struct {
	getKey, failKey string
	afterGet        func()
	fired           *atomic.Bool
}

func (h auditPersistHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h auditPersistHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if h.afterGet != nil && strings.EqualFold(cmd.Name(), "get") && len(args) > 1 && args[1] == h.getKey && h.fired.CompareAndSwap(false, true) {
			h.afterGet()
		}
		return err
	}
}

func (h auditPersistHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			for _, arg := range cmd.Args() {
				if h.failKey != "" && arg == h.failKey {
					return errors.New("injected pipeline failure")
				}
			}
		}
		return next(ctx, cmds)
	}
}

// auditPersistServer returns a test gateway whose config service runs on a
// fresh miniredis through hooked, plus plain, a hook-free client on the same
// server for out-of-band writes.
func auditPersistServer(t *testing.T) (*server, *miniredis.Miniredis, *redis.Client, *redis.Client) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	mr := miniredis.RunT(t)
	hooked := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	plain := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = hooked.Close(); _ = plain.Close() })
	s.configSvc = configsvc.NewFromClient(hooked)
	return s, mr, hooked, plain
}

func auditPersistEntry(action string) policybundles.PolicyAuditEntry {
	return policybundles.PolicyAuditEntry{Action: action, ResourceType: "policy_bundle", ResourceID: "secops/test", ActorID: "tester"}
}

// auditPersistActions returns the actions GET /api/v1/policy/audit reads.
func auditPersistActions(t *testing.T, s *server) []string {
	t.Helper()
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	actions := make([]string, len(entries))
	for i, entry := range entries {
		actions[i] = entry.Action
	}
	return actions
}

// auditPersistErrors returns the ERROR records that report action's entry as not stored.
func auditPersistErrors(logs *storeWriterLogBuffer, action string) []map[string]any {
	var out []map[string]any
	for _, rec := range logs.records("ERROR", "msg", "policy audit entry not persisted") {
		if rec["action"] == action {
			out = append(out, rec)
		}
	}
	return out
}

func auditPersistRaw(t *testing.T, mr *miniredis.Miniredis, key string) string {
	t.Helper()
	raw, err := mr.Get(key)
	require.NoError(t, err)
	return raw
}

func TestAppendPolicyAudit_PersistsAfterRequestContextIsCanceled(t *testing.T) {
	s, _, _, _ := auditPersistServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, s.appendPolicyAudit(ctx, auditPersistEntry("policy.canceled")))
	require.Equal(t, []string{"policy.canceled"}, auditPersistActions(t, s))
}

func TestAppendPolicyAudit_RetriesWhenAnotherWriterChangesTheLog(t *testing.T) {
	s, _, hooked, plain := auditPersistServer(t)
	var fired atomic.Bool
	hooked.AddHook(auditPersistHook{getKey: auditPersistLogKey, fired: &fired, afterGet: func() {
		other := map[string]any{"id": "other-1", "action": "policy.other", "created_at": "2026-01-01T00:00:00Z"}
		require.NoError(t, configsvc.NewFromClient(plain).Set(context.Background(), &configsvc.Document{
			Scope: configsvc.ScopeSystem, ScopeID: policybundles.PolicyAuditID, Data: map[string]any{policybundles.PolicyAuditKey: []any{other}},
		}))
	}})

	require.NoError(t, s.appendPolicyAudit(context.Background(), auditPersistEntry("policy.mine")))
	require.True(t, fired.Load(), "the concurrent writer never ran")
	require.ElementsMatch(t, []string{"policy.mine", "policy.other"}, auditPersistActions(t, s))
}

func TestAppendPolicyAudit_ReportsAndLogsPersistenceFailure(t *testing.T) {
	s, mr, hooked, _ := auditPersistServer(t)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)

	require.Error(t, s.appendPolicyAudit(context.Background(), auditPersistEntry("policy.lost")))
	require.Len(t, auditPersistErrors(logs, "policy.lost"), 1)
	require.False(t, mr.Exists(auditPersistLogKey), "a failed append stored the audit log")

	t.Run("the void wrapper logs the failure", func(t *testing.T) {
		s.appendAuditEntryNamed(context.Background(), "policy.lost.named", "policy_bundle", "secops/test", "", "tester", "admin", "")
		require.Len(t, auditPersistErrors(logs, "policy.lost.named"), 1)
		require.False(t, mr.Exists(auditPersistLogKey), "a failed append stored the audit log")
	})
}

func TestAppendPolicyAudit_RefusesToOverwriteUnreadableEntries(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	require.NoError(t, configsvc.NewFromClient(plain).Set(context.Background(), &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: policybundles.PolicyAuditID, Data: map[string]any{policybundles.PolicyAuditKey: "not-a-list"},
	}))
	before := auditPersistRaw(t, mr, auditPersistLogKey)

	require.Error(t, s.appendPolicyAudit(context.Background(), auditPersistEntry("policy.dropped")))
	require.Equal(t, before, auditPersistRaw(t, mr, auditPersistLogKey))
}

// auditPersistKeyspace returns every stored key with its raw value.
func auditPersistKeyspace(t *testing.T, mr *miniredis.Miniredis) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, key := range mr.Keys() {
		out[key] = auditPersistRaw(t, mr, key)
	}
	return out
}

// auditPersistSeedPolicy stores system/policy with one bundle through plain
// and returns the keyspace afterwards.
func auditPersistSeedPolicy(t *testing.T, mr *miniredis.Miniredis, plain *redis.Client) map[string]string {
	t.Helper()
	require.NoError(t, configsvc.NewFromClient(plain).Set(context.Background(), &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: packs.PolicyConfigID,
		Data: map[string]any{packs.PolicyConfigKey: map[string]any{"secops/seed": map[string]any{"enabled": true}}},
	}))
	return auditPersistKeyspace(t, mr)
}

// auditPersistAddBundle returns an apply that adds bundle id to system/policy
// and counts its runs.
func auditPersistAddBundle(id string, runs *atomic.Int32) func(*configsvc.Document) error {
	return func(doc *configsvc.Document) error {
		runs.Add(1)
		bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
		if bundles == nil {
			bundles = map[string]any{}
		}
		bundles[id] = map[string]any{"enabled": true}
		if doc.Data == nil {
			doc.Data = map[string]any{}
		}
		doc.Data[packs.PolicyConfigKey] = bundles
		return nil
	}
}

// auditPersistCommitted returns the bundle ids and revision of system/policy
// and the actions stored in the co-located audit document.
func auditPersistCommitted(t *testing.T, plain *redis.Client) ([]string, int64, []string) {
	t.Helper()
	svc := configsvc.NewFromClient(plain)
	policy, err := svc.Get(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID)
	require.NoError(t, err)
	bundles, _ := policy.Data[packs.PolicyConfigKey].(map[string]any)
	ids := make([]string, 0, len(bundles))
	for id := range bundles {
		ids = append(ids, id)
	}
	audit, err := svc.Get(context.Background(), configsvc.ScopeSystem, policybundles.PolicyWriteAuditID)
	require.NoError(t, err)
	entries, err := policyAuditEntriesFromDoc(audit)
	require.NoError(t, err)
	actions := make([]string, len(entries))
	for i, entry := range entries {
		actions[i] = entry.Action
	}
	return ids, policy.Revision, actions
}

func TestCommitPolicyWithAudit_CommitsPolicyAndAuditTogether(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	var runs atomic.Int32

	require.NoError(t, s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.commit")))
	ids, revision, actions := auditPersistCommitted(t, plain)
	require.ElementsMatch(t, []string{"secops/seed", "secops/new"}, ids)
	require.Equal(t, int64(2), revision)
	require.Equal(t, []string{"policy.commit"}, actions)
	require.False(t, mr.Exists(auditPersistLogKey), "the commit also wrote the shared audit log")
}

func TestCommitPolicyWithAudit_WritesNothingWhenTheAuditWriteFails(t *testing.T) {
	s, mr, hooked, plain := auditPersistServer(t)
	before := auditPersistSeedPolicy(t, mr, plain)
	hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})
	logs := storeWriterLogs(t)
	var runs atomic.Int32

	require.Error(t, s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.commit")))
	require.Equal(t, before, auditPersistKeyspace(t, mr))
	require.Len(t, logs.records("ERROR", "msg", "policy write and audit entry not committed"), 1)
}

func TestCommitPolicyWithAudit_WritesNothingWhenApplyRefuses(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	before := auditPersistSeedPolicy(t, mr, plain)
	refused := errors.New("apply refused")

	err := s.commitPolicyWithAudit(context.Background(), func(*configsvc.Document) error { return refused }, auditPersistEntry("policy.commit"))
	require.ErrorIs(t, err, refused)
	require.Equal(t, before, auditPersistKeyspace(t, mr))
}

func TestCommitPolicyWithAudit_RetriesAndKeepsAConcurrentPolicyChange(t *testing.T) {
	s, mr, hooked, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	var fired atomic.Bool
	var runs, otherRuns atomic.Int32
	hooked.AddHook(auditPersistHook{getKey: auditPersistPolicyKey, fired: &fired, afterGet: func() {
		require.NoError(t, configsvc.NewFromClient(plain).SetWithRetry(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID, 1, auditPersistAddBundle("secops/concurrent", &otherRuns)))
	}})

	require.NoError(t, s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.commit")))
	require.True(t, fired.Load(), "the concurrent writer never ran")
	require.Equal(t, int32(2), runs.Load())
	ids, _, actions := auditPersistCommitted(t, plain)
	require.ElementsMatch(t, []string{"secops/seed", "secops/concurrent", "secops/new"}, ids)
	require.Equal(t, []string{"policy.commit"}, actions)
}

func TestCommitPolicyWithAudit_CompletesAfterRequestContextIsCanceled(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var runs atomic.Int32

	require.NoError(t, s.commitPolicyWithAudit(ctx, auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.commit")))
	ids, _, actions := auditPersistCommitted(t, plain)
	require.ElementsMatch(t, []string{"secops/seed", "secops/new"}, ids)
	require.Equal(t, []string{"policy.commit"}, actions)
}

func TestPolicyAuditRead_IncludesEntriesCommittedWithThePolicy(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	logged, committed := auditPersistEntry("policy.logged"), auditPersistEntry("policy.committed")
	logged.CreatedAt, committed.CreatedAt = "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z"
	var runs atomic.Int32
	require.NoError(t, s.appendPolicyAudit(context.Background(), logged))
	require.NoError(t, s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), committed))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handleListPolicyAudit(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
		Total int64                            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, int64(2), body.Total)
	require.Len(t, body.Items, 2)
	require.Equal(t, []string{"policy.committed", "policy.logged"}, []string{body.Items[0].Action, body.Items[1].Action})
}
