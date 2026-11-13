package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const auditPersistLogKey = "cfg:system:policy_audit"

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
