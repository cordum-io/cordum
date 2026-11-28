package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/stretchr/testify/require"
)

func TestPolicyAuditStoreThenExport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		export bool
		fail   bool
	}{
		{name: "store"},
		{name: "append", export: true},
		{name: "store_failure", fail: true},
		{name: "append_failure", export: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyAuditStoreThenExportCheck(t, tc.export, tc.fail)
		})
	}
}

func policyAuditStoreThenExportCheck(t *testing.T, export, fail bool) {
	t.Helper()
	s, mr, hooked, _ := auditPersistServer(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	if fail {
		hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	}
	entry := auditPersistEntry("policy.evidence")
	var stored policybundles.PolicyAuditEntry
	var err error
	if export {
		err = s.appendPolicyAudit(context.Background(), entry)
	} else {
		stored, err = s.storePolicyAudit(context.Background(), entry)
	}
	if fail {
		require.Error(t, err)
		require.False(t, mr.Exists(auditPersistLogKey))
		require.Empty(t, auditPersistActions(t, s))
		require.Empty(t, sink.events)
		return
	}
	require.NoError(t, err)
	require.Equal(t, []string{entry.Action}, auditPersistActions(t, s))
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	if export {
		require.Len(t, sink.events, 1)
		require.Equal(t, policybundles.AuditEntryToSIEM(entries[0], s.tenant), sink.events[0])
	} else {
		require.Equal(t, entries[0], stored)
		require.NotEmpty(t, stored.ID)
		require.NotEmpty(t, stored.CreatedAt)
		require.Empty(t, sink.events)
	}
}
