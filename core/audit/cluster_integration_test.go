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

// The single-node append script touches the head key and the stream, which
// hash to different slots: a real cluster refuses it.
func TestClusterIntegration_SingleNodeAppendScriptIsCrossSlot(t *testing.T) {
	client, chainer := newIntegrationChainer(t)
	err := chainAppendScript.Run(context.Background(), client,
		[]string{chainer.HeadKey("acme"), chainer.StreamKey("acme")},
		"", "1", strings.Repeat("a", 64), "{}").Err()
	if err == nil || !strings.Contains(err.Error(), "CROSSSLOT") {
		t.Fatalf("two-key append script on a cluster = %v, want CROSSSLOT", err)
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
