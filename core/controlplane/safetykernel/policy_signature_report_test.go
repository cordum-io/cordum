package safetykernel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/redis/go-redis/v9"
)

func psrEnv(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	clearFileLoaderEnv(t)
	for _, name := range []string{"CORDUM_GATEWAY_PACK_STRICT", "CORDUM_POLICY_DEV_SIGNING_SEED", "SAFETY_POLICY_RELOAD_INTERVAL"} {
		t.Setenv(name, "")
	}
	t.Setenv("CORDUM_INSTANCE_ID", "kernel-report-test")
	pub, _ := genKeyPair(t)
	t.Setenv("CORDUM_POLICY_PUBLIC_KEY_REPORTKEY", base64.StdEncoding.EncodeToString(pub))
	return "REPORTKEY", pub
}

func psrRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return mr, client
}

func psrWait(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("reporter did not exit")
	}
}

func TestPolicySignatureReport_ModesMatchLoaders(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"off", "off"}, {"warn", "warn"}, {"enforce", "enforce"}, {"", "warn"}, {"wobble", "warn"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			psrEnv(t)
			t.Setenv("CORDUM_POLICY_STRICT", tc.mode)
			r := buildPolicySignatureReport(time.Now())
			if len(r.Sources) != 3 {
				t.Fatalf("sources = %v", r.Sources)
			}
			for source, resolved := range map[string]string{
				"file": resolveFileLoaderMode().String(), "url": resolveFileLoaderMode().String(),
				"store": newBundleVerifier().mode.String(),
			} {
				if got := r.Sources[source]; got != tc.want || got != resolved {
					t.Errorf("%s = %q, want %q and loader %q", source, got, tc.want, resolved)
				}
			}
		})
	}
}

func TestPolicySignatureReport_WrittenAtStartWithTTL(t *testing.T) {
	for _, tc := range []struct {
		interval, instance string
		ttl                time.Duration
	}{
		{"", "kernel-report-test", time.Minute}, {"45s", "kernel-report-test", 90 * time.Second},
		{"10s", "kernel-report-test", time.Minute}, {"bogus", "kernel-report-test", time.Minute},
		{"", "", time.Minute}, {"0s", "kernel-report-test", time.Minute},
		{"-1s", "kernel-report-test", time.Minute}, {"2562047h", "kernel-report-test", time.Minute},
	} {
		t.Run(tc.interval+"/"+tc.instance, func(t *testing.T) {
			psrEnv(t)
			t.Setenv("CORDUM_POLICY_STRICT", "enforce")
			t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", tc.interval)
			t.Setenv("CORDUM_INSTANCE_ID", tc.instance)
			mr, client := psrRedis(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var wg sync.WaitGroup
			startPolicySignatureReporter(ctx, &wg, client)
			key := PolicySignatureReportKeyPrefix + registry.ResolveInstanceID()
			raw, err := mr.Get(key)
			cancel()
			psrWait(t, &wg, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if tc.instance != "" && key != PolicySignatureReportKeyPrefix+"kernel-report-test" {
				t.Fatalf("key = %q", key)
			}
			if got := mr.TTL(key); got != tc.ttl {
				t.Errorf("TTL = %v, want %v", got, tc.ttl)
			}
			var report PolicySignatureReport
			if err := json.Unmarshal([]byte(raw), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Sources) != 3 || report.Sources["file"] != "enforce" || report.Sources["url"] != "enforce" || report.Sources["store"] != "enforce" {
				t.Errorf("sources = %v", report.Sources)
			}
		})
	}
}

func TestPolicySignatureReport_PayloadFieldsAndNoKeyMaterial(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.FixedZone("test", 3*60*60))
	for _, tc := range []struct {
		name       string
		production bool
		keys       int
	}{
		{"development", false, 1}, {"production_env_explicit_enforce", true, 1}, {"malformed_key", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, pub := psrEnv(t)
			if tc.production {
				t.Setenv("CORDUM_ENV", "production")
				t.Setenv("CORDUM_POLICY_STRICT", "enforce")
			}
			if tc.keys == 0 {
				t.Setenv("CORDUM_POLICY_PUBLIC_KEY_BADKEY", "not-a-key")
			}
			r := buildPolicySignatureReport(now)
			if r.Production != tc.production || r.TrustedKeys != tc.keys || r.ReportedAt != now.UTC().Format(time.RFC3339) {
				t.Fatalf("unexpected report metadata: %+v", r)
			}
			payload, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 4 {
				t.Fatalf("field count = %d", len(fields))
			}
			for _, field := range []string{"sources", "trusted_keys", "production", "reported_at"} {
				if _, ok := fields[field]; !ok {
					t.Errorf("missing field %s", field)
				}
			}
			for _, secret := range []string{id, "BADKEY", base64.StdEncoding.EncodeToString(pub), hex.EncodeToString(pub)} {
				if strings.Contains(strings.ToLower(string(payload)), strings.ToLower(secret)) {
					t.Error("report contains key identity or material")
				}
			}
		})
	}
}

func TestPolicySignatureReport_TickerRewritesUntilCancelled(t *testing.T) {
	psrEnv(t)
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "20ms")
	mr, client := psrRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	startPolicySignatureReporter(ctx, &wg, client)
	key := PolicySignatureReportKeyPrefix + registry.ResolveInstanceID()
	mr.Del(key)
	deadline := time.Now().Add(2 * time.Second)
	for !mr.Exists(key) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !mr.Exists(key) {
		t.Fatal("ticker did not rewrite report")
	}
	cancel()
	psrWait(t, &wg, 2*time.Second)
	mr.Del(key)
	time.Sleep(200 * time.Millisecond)
	if mr.Exists(key) {
		t.Fatal("report written after cancellation")
	}
}

func TestPolicySignatureReport_NilClientStartsNothing(t *testing.T) {
	psrEnv(t)
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	startPolicySignatureReporter(ctx, &wg, nil)
	psrWait(t, &wg, 200*time.Millisecond)
	if strings.Count(buf.String(), "level=WARN") != 1 || !strings.Contains(buf.String(), "policy signature report") {
		t.Fatalf("expected one report warning, got %q", buf.String())
	}
}

func TestPolicySignatureReport_StartupBoundedWhenRedisStalls(t *testing.T) {
	psrEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { <-stop; _ = conn.Close() }()
		}
	}()
	// The client's own timeouts exceed the bound, so the test measures the reporter's wait alone.
	client := redis.NewClient(&redis.Options{Addr: ln.Addr().String(), ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxRetries: -1})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	began := time.Now()
	startPolicySignatureReporter(ctx, &wg, client)
	elapsed := time.Since(began)
	cancel()
	close(stop)
	if err := ln.Close(); err != nil {
		t.Error(err)
	}
	psrWait(t, &wg, 5*time.Second)
	if err := client.Close(); err != nil {
		t.Error(err)
	}
	const bound = 2 * time.Second
	if elapsed < bound || elapsed > bound+time.Second {
		t.Fatalf("startup waited %v, want the %v bound", elapsed, bound)
	}
}
