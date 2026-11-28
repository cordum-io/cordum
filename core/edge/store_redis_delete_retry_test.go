package edge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

var errPartialSessionCleanup = errors.New("injected partial session cleanup")

type partialSessionCleanupHook struct {
	target, units int
	failed        bool
	cluster       *redis.ClusterClient
	failedPort    string
}

func (h *partialSessionCleanupHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *partialSessionCleanupHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.ProcessPipelineHook(func(ctx context.Context, cmds []redis.Cmder) error {
			return next(ctx, cmds[0])
		})(ctx, []redis.Cmder{cmd})
	}
}

func (h *partialSessionCleanupHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		cleanup := false
		for _, cmd := range cmds {
			cleanup = cleanup || cmd.Name() == "del" || cmd.Name() == "zrem"
		}
		if h.target == 0 || !cleanup {
			return next(ctx, cmds)
		}
		h.units++
		if h.units != h.target {
			return next(ctx, cmds)
		}
		kept, err := h.keepCommands(ctx, cmds)
		if err != nil {
			return err
		}
		if len(kept) > 0 {
			if err := next(ctx, kept); err != nil {
				return err
			}
		}
		if h.failed {
			return errPartialSessionCleanup
		}
		return nil
	}
}

func (h *partialSessionCleanupHook) keepCommands(ctx context.Context, cmds []redis.Cmder) ([]redis.Cmder, error) {
	kept := make([]redis.Cmder, 0, len(cmds))
	for i, cmd := range cmds {
		fail := i == len(cmds)-1
		if h.cluster != nil {
			key, ok := cmd.Args()[1].(string)
			if !ok {
				return nil, fmt.Errorf("cleanup key is not a string: %v", cmd.Args())
			}
			master, err := h.cluster.MasterForKey(ctx, key)
			if err != nil {
				return nil, err
			}
			// go-redis may strip the loopback host; ports identify these local masters.
			_, port, err := net.SplitHostPort(master.Options().Addr)
			if err != nil {
				return nil, err
			}
			fail = port == h.failedPort
		}
		if fail {
			cmd.SetErr(errPartialSessionCleanup)
			h.failed = true
		} else {
			kept = append(kept, cmd)
		}
	}
	return kept, nil
}

func TestDeleteSessionRetryAfterPartialCleanup(t *testing.T) {
	for _, backend := range []string{"single", "cluster"} {
		t.Run(backend, func(t *testing.T) {
			for unit := 1; ; unit++ {
				if !retryPartialSessionCleanup(t, backend == "cluster", unit) {
					return
				}
			}
		})
	}
}

func retryPartialSessionCleanup(t *testing.T, cluster bool, unit int) bool {
	t.Helper()
	ctx := context.Background()
	servers, client := deleteClusterBackend(t, cluster)
	deleteClusterSeed(t, ctx, client)
	hook := &partialSessionCleanupHook{target: unit}
	if cluster {
		hook.cluster = client.(*redis.ClusterClient)
		_, port, err := net.SplitHostPort(servers[1].Addr())
		if err != nil {
			partialCleanupFailure(t, unit, servers[1], err.Error())
		}
		hook.failedPort = port
	}
	client.AddHook(hook)
	store := NewRedisStoreFromClient(client)
	err := store.DeleteSession(ctx, "tenant-a", "session-a")
	if (hook.failed && !errors.Is(err, errPartialSessionCleanup)) || (!hook.failed && err != nil) {
		partialCleanupFailure(t, unit, servers[0], fmt.Sprintf("DeleteSession: %v (injected=%t)", err, hook.failed))
	}
	hook.target = 0
	if err := store.DeleteSession(ctx, "tenant-a", "session-a"); err != nil {
		partialCleanupFailure(t, unit, servers[0], fmt.Sprintf("retry: %v", err))
	}
	for _, server := range servers {
		if len(server.Keys()) != 0 {
			partialCleanupFailure(t, unit, server, "retry left keys")
		}
	}
	return hook.units >= unit
}

func partialCleanupFailure(t *testing.T, unit int, server *miniredis.Miniredis, message string) {
	t.Helper()
	keys, first := server.Keys(), ""
	if len(keys) > 0 {
		first = keys[0]
	}
	t.Fatalf("unit %d failed partway: server %s holds %d keys, first %q: %s",
		unit, server.Addr(), len(keys), first, message)
}
