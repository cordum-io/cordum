package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/safetykernel"
	"github.com/redis/go-redis/v9"
)

// sigStatusEnv sets the development profile with every signature override
// empty except CORDUM_GATEWAY_PACK_STRICT, and isolates the strict cache.
func sigStatusEnv(t *testing.T, packStrict string) {
	t.Helper()
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", "CORDUM_POLICY_STRICT", "SAFETY_POLICY_SIGNATURE_REQUIRED"} {
		t.Setenv(name, "")
	}
	t.Setenv("CORDUM_GATEWAY_PACK_STRICT", packStrict)
	resetGatewayPackStrictCache()
	t.Cleanup(resetGatewayPackStrictCache)
}

func sigStatusPayload(t *testing.T, sources map[string]string, reportedAt string) string {
	t.Helper()
	raw, err := json.Marshal(safetykernel.PolicySignatureReport{Sources: sources, TrustedKeys: 1, ReportedAt: reportedAt})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return string(raw)
}

func sigStatusReport(t *testing.T, file, url, store string) string {
	t.Helper()
	sources := map[string]string{"file": file, "url": url, "store": store}
	return sigStatusPayload(t, sources, time.Now().UTC().Format(time.RFC3339))
}

// sigStatusServer returns a fresh gateway with the kernel reports seeded.
func sigStatusServer(t *testing.T, reports map[string]string) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	for id, payload := range reports {
		key := safetykernel.PolicySignatureReportKeyPrefix + id
		if err := s.redisClient().Set(context.Background(), key, payload, time.Minute).Err(); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	return s
}

// sigStatusRequest sends the server's one status request as role.
func sigStatusRequest(t *testing.T, s *server, ctx context.Context, role string) map[string]any {
	t.Helper()
	s.auth = &tenantStrictAuth{tenant: "default", role: role}
	authCtx := &auth.AuthContext{Tenant: "default", Role: role, PrincipalID: role + "1"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req = req.WithContext(context.WithValue(ctx, auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func sigStatusBlock(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	block, ok := resp["signature_enforcement"].(map[string]any)
	if !ok {
		t.Fatalf("signature_enforcement missing or not an object: %#v", resp["signature_enforcement"])
	}
	return block
}

func sigStatusDeepEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", label, got, want)
	}
}

func sigStatusDisagreeing(t *testing.T) map[string]string {
	return map[string]string{
		"kernel-a": sigStatusReport(t, "enforce", "off", "warn"),
		"kernel-b": sigStatusReport(t, "warn", "enforce", "enforce"),
	}
}

var (
	sigStatusUnknown         = map[string]any{"status": "unknown"}
	sigStatusDisagreeingWant = map[string]any{
		"status":     "reported",
		"sources":    map[string]any{"file": "warn", "url": "off", "store": "warn"},
		"reporters":  float64(2),
		"consistent": false,
	}
)

func TestHandleStatusSignatureEnforcement_WeakestModePerSource(t *testing.T) {
	enforce := sigStatusReport(t, "enforce", "enforce", "enforce")
	cases := []struct {
		name    string
		reports map[string]string
		want    map[string]any
	}{
		{"disagreeing", sigStatusDisagreeing(t), sigStatusDisagreeingWant},
		{"agreeing", map[string]string{"kernel-a": enforce, "kernel-b": enforce}, map[string]any{
			"status":    "reported",
			"sources":   map[string]any{"file": "enforce", "url": "enforce", "store": "enforce"},
			"reporters": float64(2), "consistent": true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigStatusEnv(t, "")
			resp := sigStatusRequest(t, sigStatusServer(t, tc.reports), context.Background(), "admin")
			sigStatusDeepEqual(t, "policy", sigStatusBlock(t, resp)["policy"], tc.want)
		})
	}
}

func TestHandleStatusSignatureEnforcement_UnknownWithoutReport(t *testing.T) {
	sigStatusEnv(t, "")
	block := sigStatusBlock(t, sigStatusRequest(t, sigStatusServer(t, nil), context.Background(), "admin"))
	sigStatusDeepEqual(t, "policy", block["policy"], sigStatusUnknown)
	sigStatusDeepEqual(t, "packs", block["packs"], map[string]any{"strict": false})
}

func TestHandleStatusSignatureEnforcement_UnknownWithUnparsableReport(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	cases := map[string]map[string]string{
		"not json":     {"kernel-a": "{not json"},
		"alias strict": {"kernel-a": sigStatusReport(t, "enforce", "enforce", "strict")},
		"empty url":    {"kernel-a": sigStatusReport(t, "enforce", "", "enforce")},
		"store missing": {"kernel-a": sigStatusPayload(t,
			map[string]string{"file": "enforce", "url": "enforce"}, now)},
		"reported_at not RFC3339": {"kernel-a": sigStatusPayload(t,
			map[string]string{"file": "enforce", "url": "enforce", "store": "enforce"}, "yesterday")},
		"valid plus not json": {
			"kernel-a": sigStatusReport(t, "enforce", "enforce", "enforce"),
			"kernel-b": "{not json",
		},
	}
	for name, reports := range cases {
		t.Run(name, func(t *testing.T) {
			sigStatusEnv(t, "")
			resp := sigStatusRequest(t, sigStatusServer(t, reports), context.Background(), "admin")
			sigStatusDeepEqual(t, "policy", sigStatusBlock(t, resp)["policy"], sigStatusUnknown)
		})
	}
}

func TestHandleStatusSignatureEnforcement_PackStrictFollowsEnvAndRedis(t *testing.T) {
	cases := []struct {
		name, env, redisValue string
		setKey, want          bool
	}{
		{name: "env true, key absent", env: "true", want: true},
		{name: "key true", redisValue: "true", setKey: true, want: true},
		{name: "key false", redisValue: "false", setKey: true, want: false},
		{name: "key absent", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigStatusEnv(t, tc.env)
			s := sigStatusServer(t, nil)
			if tc.setKey {
				if err := s.redisClient().Set(context.Background(), "cfg:packs:strict_mode", tc.redisValue, 0).Err(); err != nil {
					t.Fatalf("seed strict mode: %v", err)
				}
			}
			block := sigStatusBlock(t, sigStatusRequest(t, s, context.Background(), "admin"))
			sigStatusDeepEqual(t, "packs", block["packs"], map[string]any{"strict": tc.want})
			resetGatewayPackStrictCache()
			if got := resolveGatewayPackStrict(context.Background(), s.redisClient()); got != tc.want {
				t.Fatalf("install-path resolver = %v, status reported %v", got, tc.want)
			}
		})
	}
}

func TestHandleStatusSignatureEnforcement_AdminOnly(t *testing.T) {
	sigStatusEnv(t, "")
	reports := map[string]string{"kernel-a": sigStatusReport(t, "enforce", "enforce", "enforce")}
	viewer := sigStatusRequest(t, sigStatusServer(t, reports), context.Background(), "viewer")
	if _, ok := viewer["signature_enforcement"]; ok {
		t.Fatalf("viewer response carries signature_enforcement: %#v", viewer["signature_enforcement"])
	}
	for _, field := range []string{"time", "build", "workers"} {
		if _, ok := viewer[field]; !ok {
			t.Fatalf("viewer response lacks %q", field)
		}
	}
	admin := sigStatusRequest(t, sigStatusServer(t, reports), context.Background(), "admin")
	policy, _ := sigStatusBlock(t, admin)["policy"].(map[string]any)
	if policy["status"] != "reported" {
		t.Fatalf("admin policy = %#v, want status reported", policy)
	}
}

func TestHandleStatusSignatureEnforcement_ClusterClientReadsReports(t *testing.T) {
	mr := miniredis.RunT(t)
	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = cluster.Close() })
	for id, payload := range sigStatusDisagreeing(t) {
		if err := mr.Set(safetykernel.PolicySignatureReportKeyPrefix+id, payload); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := json.Marshal(readPolicySignatureEnforcement(ctx, cluster))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sigStatusDeepEqual(t, "policy", got, sigStatusDisagreeingWant)
}

func TestHandleStatusSignatureEnforcement_CancelledRequestKeepsPackStrict(t *testing.T) {
	sigStatusEnv(t, "")
	s := sigStatusServer(t, nil)
	if err := s.redisClient().Set(context.Background(), "cfg:packs:strict_mode", "true", 0).Err(); err != nil {
		t.Fatalf("seed strict mode: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	block := sigStatusBlock(t, sigStatusRequest(t, s, ctx, "admin"))
	sigStatusDeepEqual(t, "policy", block["policy"], sigStatusUnknown)
	sigStatusDeepEqual(t, "packs", block["packs"], map[string]any{"strict": true})
	if !resolveGatewayPackStrict(context.Background(), s.redisClient()) {
		t.Fatal("a cancelled status request left pack strict mode cached as false")
	}
}
