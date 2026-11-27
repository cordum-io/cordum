package testredis

import (
	"context"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCrossSlotGuard(t *testing.T) {
	for _, pipeline := range []bool{false, true} {
		mode := "direct"
		if pipeline {
			mode = "pipeline"
		}
		for _, command := range []string{"del", "mget", "zinterstore"} {
			t.Run(mode+"/"+command, func(t *testing.T) {
				a, b := miniredis.RunT(t), miniredis.RunT(t)
				client := NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
				client.AddHook(CrossSlotGuard())
				ctx := context.Background()
				for _, key := range []string{"a", "b"} {
					require.NoError(t, client.Set(ctx, key, "value", 0).Err())
				}
				issue := func(c redis.Cmdable) error {
					switch command {
					case "del":
						return c.Del(ctx, "a", "b").Err()
					case "mget":
						return c.MGet(ctx, "a", "b").Err()
					default:
						return c.ZInterStore(ctx, "out", &redis.ZStore{Keys: []string{"a", "b"}}).Err()
					}
				}
				var err error
				if pipeline {
					_, err = client.Pipelined(ctx, func(p redis.Pipeliner) error { return issue(p) })
				} else {
					err = issue(client)
				}
				require.ErrorIs(t, err, redis.ErrCrossSlot)
				for _, key := range []string{"a", "b"} {
					require.Equal(t, "value", client.Get(ctx, key).Val())
				}
				require.NoError(t, client.Del(ctx, "a", "a").Err())
				require.NoError(t, client.Del(ctx, "b").Err())
			})
		}
	}
}

func TestCrossSlotGuardSyntax(t *testing.T) {
	groups := []struct{ names, tail string }{
		{"del unlink exists touch mget sinter sunion sdiff sinterstore sunionstore sdiffstore pfcount pfmerge", "k other"},
		{"mset msetnx", "k value other value"},
		{"rename renamenx smove lmove blmove rpoplpush brpoplpush copy", "k other ignored"},
		{"zinterstore zunionstore zdiffstore", "k 1 other"},
		{"zinter zunion zdiff zintercard sintercard", "2 k other"},
	}
	for _, group := range groups {
		for _, name := range strings.Fields(group.names) {
			t.Run(name, func(t *testing.T) {
				for _, distinct := range []bool{true, false} {
					line := name + " " + group.tail
					if !distinct {
						line = strings.ReplaceAll(line, "other", "k")
					}
					args := []interface{}{}
					for _, arg := range strings.Fields(line) {
						args = append(args, arg)
					}
					called := false
					err := CrossSlotGuard().ProcessHook(func(context.Context, redis.Cmder) error { called = true; return nil })(context.Background(), redis.NewCmd(context.Background(), args...))
					if distinct {
						require.ErrorIs(t, err, redis.ErrCrossSlot)
					} else {
						require.NoError(t, err)
					}
					require.Equal(t, !distinct, called)
				}
			})
		}
	}
}
