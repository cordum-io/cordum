//go:build integration

package edge

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/internal/testredis"
)

// The session sweeper finds expired sessions on every master and deletes
// them without a multi-key DEL. See core/audit/cluster_integration_test.go
// for a local cluster setup; the sweep covers every edge:session:* key in the
// cluster, so use a disposable one.
func TestClusterIntegration_SessionSweeperReachesEveryMaster(t *testing.T) {
	client := testredis.NewClusterClient(t)
	ctx := context.Background()
	tenant := "it-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	started := time.Now().UTC().Add(-40 * 24 * time.Hour)
	var ids []string
	for i := 0; i < 12; i++ {
		id := tenant + "-s" + strconv.Itoa(i)
		doc, err := json.Marshal(EdgeSession{SessionID: id, TenantID: tenant, StartedAt: started})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := client.Set(ctx, edgeSessionKey(id), doc, 0).Err(); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		t.Cleanup(func() { client.Del(context.Background(), edgeSessionKey(id)) })
		ids = append(ids, id)
	}

	sweeper, err := NewSessionSweeper(NewRedisStoreFromClient(client), SessionSweeperOptions{
		LegalHolds: audit.NewLegalHoldStoreFromClient(client),
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept < len(ids) {
		t.Fatalf("SweepOnce = %d, %v; want at least %d", swept, err, len(ids))
	}
	for _, id := range ids {
		if n, err := client.Exists(ctx, edgeSessionKey(id)).Result(); err != nil || n != 0 {
			t.Fatalf("session %s left behind: %d, %v", id, n, err)
		}
	}
}
