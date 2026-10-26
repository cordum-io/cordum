//go:build integration

package audit

// Redis Cluster integration tests. They run against the cluster named by
// CORDUM_TEST_REDIS_CLUSTER_ADDRESSES and skip without it. A one-node cluster
// is enough to enforce CROSSSLOT:
//
//	docker run -d --name cordum-redis-cluster -p 7000:7000 -p 17000:17000 redis:7.4.2-alpine \
//	  redis-server --port 7000 --cluster-enabled yes --cluster-announce-ip 127.0.0.1
//	docker exec cordum-redis-cluster redis-cli -p 7000 CLUSTER ADDSLOTSRANGE 0 16383
//	CORDUM_TEST_REDIS_CLUSTER_ADDRESSES=127.0.0.1:7000 go test -tags=integration -run Cluster ./core/audit/

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// newIntegrationChainer returns a chainer under a key prefix unique to the
// test, whose keys are deleted afterwards.
func newIntegrationChainer(t *testing.T, opts ...ChainerOption) (*redis.ClusterClient, *Chainer) {
	t.Helper()
	client := testredis.NewClusterClient(t)
	prefix := "it:" + uuid.NewString()[:8] + ":" + ChainKeyPrefix
	t.Cleanup(func() { deleteClusterKeys(t, client, prefix+"*") })
	return client, NewChainer(client, prefix, opts...)
}

func deleteClusterKeys(t *testing.T, client *redis.ClusterClient, match string) {
	t.Helper()
	ctx := context.Background()
	err := client.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
		iter := node.Scan(ctx, 0, match, 100).Iterator()
		for iter.Next(ctx) {
			if err := node.Del(ctx, iter.Val()).Err(); err != nil {
				return err
			}
		}
		return iter.Err()
	})
	if err != nil {
		t.Errorf("clean up %s: %v", match, err)
	}
}

// The single-node scripts touch a tenant's stream together with its head or
// anchor key, which hash to different slots: a real cluster refuses them.
func TestClusterIntegration_SingleNodeScriptsAreCrossSlot(t *testing.T) {
	client, chainer := newIntegrationChainer(t)
	ctx := context.Background()
	for name, err := range map[string]error{
		"append": chainAppendScript.Run(ctx, client,
			[]string{chainer.HeadKey("acme"), chainer.StreamKey("acme")},
			"", "1", strings.Repeat("a", 64), "{}").Err(),
		"trim": retentionTrimScript.Run(ctx, client,
			[]string{chainer.StreamKey("acme"), chainer.AnchorKey("acme")}, "1-0", "").Err(),
	} {
		if err == nil || !strings.Contains(err.Error(), "CROSSSLOT") {
			t.Errorf("single-node %s script on a cluster = %v, want CROSSSLOT", name, err)
		}
	}
}

func TestClusterIntegration_AppendAndVerify(t *testing.T) {
	client, chainer := newIntegrationChainer(t, WithHMACKey(testHMACKey()))
	replica := NewChainer(client, chainer.StreamKey(""), WithHMACKey(testHMACKey())) // same key prefix
	ctx := context.Background()
	const producers, perProducer = 4, 25
	var wg sync.WaitGroup
	errs := make(chan error, producers)
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			c := []*Chainer{chainer, replica}[p%2]
			for i := 0; i < perProducer; i++ {
				if err := c.Append(ctx, baseEvent("acme", "p"+strconv.Itoa(p))); err != nil {
					errs <- err
					return
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("append on cluster: %v", err)
	}
	res, err := VerifyChain(ctx, client, chainer.StreamKey("acme"), VerifyOptions{HMACKey: testHMACKey()})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != VerifyStatusOK || res.TotalEvents != producers*perProducer || res.HMACVerified != producers*perProducer {
		t.Fatalf("unexpected verify result: %+v", res)
	}
	head, err := client.Get(ctx, chainer.HeadKey("acme")).Result()
	if err != nil || !strings.HasPrefix(head, strconv.Itoa(producers*perProducer)+":") {
		t.Fatalf("head hint = %q, %v", head, err)
	}
}

// newIntegrationTenant returns a tenant name unique to the test whose legal
// hold keys are deleted afterwards.
func newIntegrationTenant(t *testing.T, client *redis.ClusterClient) string {
	t.Helper()
	tenant := "it-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		ctx := context.Background()
		ids, _ := client.SMembers(ctx, legalHoldSetPrefix+tenant).Result()
		for _, id := range ids {
			client.Del(ctx, legalHoldKeyPrefix+id)
		}
		client.Del(ctx, legalHoldSetPrefix+tenant)
	})
	return tenant
}

// newIntegrationSweeper enforces a 1.8-second retention window, so events
// appended before a 2.5-second pause are past it.
func newIntegrationSweeper(t *testing.T, client *redis.ClusterClient, chainer *Chainer) (*RetentionSweeper, *LegalHoldStore) {
	t.Helper()
	t.Setenv(EnvRetentionEnforce, "true")
	t.Setenv(EnvRetentionHours, "0.0005")
	holds := NewLegalHoldStoreFromClient(client)
	return NewRetentionSweeper(client, chainer, holds, nil), holds
}

func appendAndAge(t *testing.T, chainer *Chainer, tenant string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if i == 3 {
			time.Sleep(2500 * time.Millisecond)
		}
		if err := chainer.Append(ctx, baseEvent(tenant, "act-"+strconv.Itoa(i))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

func clusterAnchor(t *testing.T, client *redis.ClusterClient, chainer *Chainer, tenant string) retentionAnchor {
	t.Helper()
	raw, err := client.Get(context.Background(), chainer.AnchorKey(tenant)).Result()
	if err != nil {
		t.Fatalf("get anchor: %v", err)
	}
	a, err := parseRetentionAnchor(raw)
	if err != nil {
		t.Fatalf("parse anchor: %v", err)
	}
	return a
}

func TestClusterIntegration_RetentionTrim(t *testing.T) {
	client, chainer := newIntegrationChainer(t, WithHMACKey(testHMACKey()))
	tenant := newIntegrationTenant(t, client)
	sweeper, _ := newIntegrationSweeper(t, client, chainer)
	appendAndAge(t, chainer, tenant)

	if n, err := sweeper.SweepOnce(context.Background()); err != nil || n != 3 {
		t.Fatalf("sweep = %d, %v; want 3 trimmed", n, err)
	}
	if a := clusterAnchor(t, client, chainer, tenant); a.Pending || a.Seq != 3 || !a.validMAC(chainer.AnchorKey(tenant), testHMACKey()) {
		t.Fatalf("anchor = %+v, want confirmed and signed at seq 3", a)
	}
	if n, err := client.XLen(context.Background(), chainer.StreamKey(tenant)).Result(); err != nil || n != 3 {
		t.Fatalf("stream len = %d, %v; want 3", n, err)
	}
}

func TestClusterIntegration_HoldAfterPendingAnchor(t *testing.T) {
	client, chainer := newIntegrationChainer(t, WithHMACKey(testHMACKey()))
	tenant := newIntegrationTenant(t, client)
	sweeper, holds := newIntegrationSweeper(t, client, chainer)
	ctx := context.Background()
	appendAndAge(t, chainer, tenant)
	var hold *LegalHold
	client.AddHook(&afterScriptHook{script: anchorSwapScript, fn: func() {
		var err error
		if hold, err = holds.CreateHold(ctx, tenant, "litigation", "test"); err != nil {
			t.Errorf("create hold: %v", err)
		}
	}})

	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 0 {
		t.Fatalf("sweep with a hold placed mid-trim = %d, %v; want 0", n, err)
	}
	if a := clusterAnchor(t, client, chainer, tenant); !a.Pending || a.Seq != 3 {
		t.Fatalf("anchor = %+v, want pending at seq 3", a)
	}
	if n, _ := client.XLen(ctx, chainer.StreamKey(tenant)).Result(); n != 6 {
		t.Fatalf("stream len = %d, want 6", n)
	}
	if err := holds.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 3 {
		t.Fatalf("sweep after release = %d, %v; want 3", n, err)
	}
	if a := clusterAnchor(t, client, chainer, tenant); a.Pending || a.Seq != 3 {
		t.Fatalf("anchor = %+v, want confirmed at seq 3", a)
	}
}

func TestClusterIntegration_LegalHolds(t *testing.T) {
	client := testredis.NewClusterClient(t)
	tenant := newIntegrationTenant(t, client)
	store := NewLegalHoldStoreFromClient(client)
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, tenant, "litigation", "admin")
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, tenant); err != nil || !held {
		t.Fatalf("IsUnderHold = %v, %v; want true", held, err)
	}
	if holds, err := store.ListHolds(ctx, tenant); err != nil || len(holds) != 1 || holds[0].ID != hold.ID {
		t.Fatalf("ListHolds = %+v, %v", holds, err)
	}
	if err := store.ReleaseHold(ctx, hold.ID, "admin"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, tenant); err != nil || held {
		t.Fatalf("IsUnderHold after release = %v, %v; want false", held, err)
	}
}
