package redisutil_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

var scanKeys = []string{"scan:{a}:1", "scan:{a}:2", "scan:{b}:1", "scan:{b}:2"}

func scanCluster(t *testing.T, hook func(*redis.Client, string)) *redis.ClusterClient {
	t.Helper()
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	client := testredis.NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
	if hook != nil {
		client.OnNewNode(func(node *redis.Client) { hook(node, b.Addr()) })
	}
	seedScanKeys(t, client)
	if len(a.Keys()) == 0 || len(b.Keys()) == 0 {
		t.Fatal("test requires keys on both masters")
	}
	return client
}

func seedScanKeys(t *testing.T, client redis.UniversalClient) {
	t.Helper()
	for _, key := range append(slices.Clone(scanKeys), "other:{a}", "other:{b}") {
		if err := client.Set(t.Context(), key, "value", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertScanKeys(t *testing.T, client redis.UniversalClient) {
	t.Helper()
	var got []string
	err := redisutil.ScanKeys(t.Context(), client, "scan:*", 1, func(keys []string) error {
		if len(keys) == 0 {
			t.Error("empty callback batch")
		}
		got = append(got, keys...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !reflect.DeepEqual(got, scanKeys) {
		t.Fatalf("keys = %v, want %v exactly once", got, scanKeys)
	}
}

func TestScanKeysVisitsEveryMaster(t *testing.T) {
	t.Run("cluster", func(t *testing.T) { assertScanKeys(t, scanCluster(t, nil)) })
	t.Run("plain", func(t *testing.T) {
		client := testredis.NewClient(t, miniredis.RunT(t).Addr())
		seedScanKeys(t, client)
		assertScanKeys(t, client)
	})
}

func TestScanKeysDedupesRepeatedKeys(t *testing.T) {
	client := scanCluster(t, func(node *redis.Client, _ string) {
		node.AddHook(scanHook(func(cmd *redis.ScanCmd) error {
			keys, cursor := cmd.Val()
			cmd.SetVal(append(keys, keys...), cursor)
			return nil
		}))
	})
	assertScanKeys(t, client)
}

func TestScanKeysStopsOnErrStopScan(t *testing.T) {
	client := scanCluster(t, nil)
	calls := 0
	err := redisutil.ScanKeys(t.Context(), client, "scan:*", 1, func([]string) error {
		calls++
		return redisutil.ErrStopScan
	})
	if err != nil || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want nil, 1", err, calls)
	}
}

func TestScanKeysReturnsNodeScanError(t *testing.T) {
	want := errors.New("node scan unavailable")
	client := scanCluster(t, func(node *redis.Client, addrB string) {
		// Custom slots have no origin host; go-redis may rewrite the dial
		// address, while NodeAddress retains the slot map's address.
		if node.Options().NodeAddress == addrB {
			node.AddHook(scanHook(func(*redis.ScanCmd) error { return want }))
		}
	})
	err := redisutil.ScanKeys(t.Context(), client, "scan:*", 1, func([]string) error { return nil })
	if !errors.Is(err, want) || err != want {
		t.Fatalf("error = %v, want original node error %v", err, want)
	}
}

func TestScanKeysReturnsCallbackError(t *testing.T) {
	want := errors.New("callback stopped")
	client := scanCluster(t, nil)
	calls := 0
	err := redisutil.ScanKeys(t.Context(), client, "scan:*", 1, func([]string) error {
		calls++
		return want
	})
	if err != want || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want original callback error, 1", err, calls)
	}
}

func TestScanKeysCancellation(t *testing.T) {
	for _, when := range []string{"before scan", "after page", "in callback"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := scanCluster(t, func(node *redis.Client, _ string) {
				if when == "after page" {
					node.AddHook(scanHook(func(*redis.ScanCmd) error { cancel(); return nil }))
				}
			})
			if when == "before scan" {
				cancel()
			}
			calls := 0
			err := redisutil.ScanKeys(ctx, client, "scan:*", 1, func([]string) error {
				calls++
				cancel()
				return nil
			})
			wantCalls := 0
			if when == "in callback" {
				wantCalls = 1
			}
			if err != context.Canceled || calls != wantCalls {
				t.Fatalf("error = %v, calls = %d; want canceled, %d", err, calls, wantCalls)
			}
		})
	}
}

func TestScanKeysEmptyMatch(t *testing.T) {
	err := redisutil.ScanKeys(t.Context(), scanCluster(t, nil), "absent:*", 1, func([]string) error {
		t.Error("callback for empty match")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type scanHook func(*redis.ScanCmd) error

func (scanHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (scanHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h scanHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := next(ctx, cmd); err != nil {
			return err
		}
		if scan, ok := cmd.(*redis.ScanCmd); ok {
			return h(scan)
		}
		return nil
	}
}
