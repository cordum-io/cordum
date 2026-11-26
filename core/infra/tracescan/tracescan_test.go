package tracescan

import (
	"context"
	"fmt"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRetentionTraceScanFindsEveryType(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr(), DB: 3})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := t.Context()
	const marker = "trace-marker"
	require.NoError(t, client.Set(ctx, "key-"+marker, "clean", 0).Err())
	require.NoError(t, client.Set(ctx, "string", "prefix-"+marker+"-suffix", 0).Err())
	require.NoError(t, client.HSet(ctx, "hash", marker, "clean", "field", marker).Err())
	require.NoError(t, client.SAdd(ctx, "set", marker).Err())
	require.NoError(t, client.ZAdd(ctx, "zset", redis.Z{Member: marker, Score: 1}).Err())
	require.NoError(t, client.RPush(ctx, "list", "clean", marker).Err())
	require.NoError(t, client.XAdd(ctx, &redis.XAddArgs{Stream: "stream", ID: "1-0", Values: []string{marker, "clean"}}).Err())
	require.NoError(t, client.XAdd(ctx, &redis.XAddArgs{Stream: "stream", ID: "2-0", Values: []string{"field", marker}}).Err())
	hits, err := Find(ctx, client, marker)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"key-" + marker, "string", "hash:" + marker, "hash:field", "set:" + marker, "zset:" + marker, "list:1", "stream:1-0", "stream:2-0"}, hits)
	hits, err = Find(ctx, client, "absent")
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestRetentionTraceScanEveryMaster(t *testing.T) {
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	require.NoError(t, a.Set("first", "marker"))
	require.NoError(t, b.Set("second", "marker"))
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{a.Addr(), b.Addr()},
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			return []redis.ClusterSlot{
				{Start: 0, End: 8191, Nodes: []redis.ClusterNode{{Addr: a.Addr()}}},
				{Start: 8192, End: 16383, Nodes: []redis.ClusterNode{{Addr: b.Addr()}}},
			}, nil
		},
	})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	hits, err := Find(t.Context(), client, "marker")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first", "second"}, hits)
}

func TestRetentionTraceScanPages(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := t.Context()
	var want []string
	for i := 0; i < 130; i++ {
		value := fmt.Sprintf("marker-%03d", i)
		require.NoError(t, client.Set(ctx, value, "clean", 0).Err())
		require.NoError(t, client.HSet(ctx, "hash", value, value).Err())
		require.NoError(t, client.SAdd(ctx, "set", value).Err())
		require.NoError(t, client.ZAdd(ctx, "zset", redis.Z{Member: value, Score: float64(i)}).Err())
		require.NoError(t, client.RPush(ctx, "list", value).Err())
		id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: "stream", Values: []string{"field", value}}).Result()
		require.NoError(t, err)
		want = append(want, value, "hash:"+value, "set:"+value, "zset:"+value, fmt.Sprintf("list:%d", i), "stream:"+id)
	}
	hits, err := Find(ctx, client, "marker")
	require.NoError(t, err)
	require.ElementsMatch(t, want, hits)
}

func TestRetentionTraceScanRejectsClients(t *testing.T) {
	ring := redis.NewRing(&redis.RingOptions{})
	t.Cleanup(func() { require.NoError(t, ring.Close()) })
	for _, client := range []redis.UniversalClient{nil, (*redis.Client)(nil), (*redis.ClusterClient)(nil), ring} {
		hits, err := Find(t.Context(), client, "marker")
		require.Error(t, err)
		require.Empty(t, hits)
	}
	_, err := Find(t.Context(), nil, "")
	require.ErrorContains(t, err, "empty marker")
}

func TestRetentionTraceScanReadErrors(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.PFAdd(t.Context(), "marker-hll", "value").Err())
	hits, err := Find(t.Context(), client, "marker")
	require.Error(t, err) // miniredis stores an hll type, which cannot be read as a string.
	require.Empty(t, hits)
	srv.SetError("ERR unreadable")
	hits, err = Find(t.Context(), client, "marker")
	require.ErrorContains(t, err, "unreadable")
	require.Empty(t, hits)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Find(ctx, client, "marker")
	require.ErrorIs(t, err, context.Canceled)
}

type disappearingKey struct {
	server  *miniredis.Miniredis
	command string
}

func (h disappearingKey) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h disappearingKey) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h disappearingKey) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == h.command {
			h.server.Del("marker")
		}
		return next(ctx, cmd)
	}
}

func TestRetentionTraceScanDisappearingKey(t *testing.T) {
	for _, command := range []string{"type", "get"} {
		t.Run(command, func(t *testing.T) {
			srv := miniredis.RunT(t)
			require.NoError(t, srv.Set("marker", "marker"))
			client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			client.AddHook(disappearingKey{server: srv, command: command})
			hits, err := Find(t.Context(), client, "marker")
			require.NoError(t, err)
			require.Empty(t, hits)
		})
	}
}
