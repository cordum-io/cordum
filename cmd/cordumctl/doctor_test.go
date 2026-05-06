package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeStatusServer returns an HTTP server that mimics the gateway's
// /readyz + /api/v1/status endpoints for doctor probes.
func fakeStatusServer(t *testing.T, status gatewayStatusResponse, statusCode int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(status)
	})
	return httptest.NewServer(mux)
}

func newEnv(t *testing.T, gateway, apiKey string) *doctorEnv {
	t.Helper()
	return &doctorEnv{
		gateway:    strings.TrimRight(gateway, "/"),
		apiKey:     apiKey,
		tenant:     "default",
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
}

func TestCheckGatewayReachable_OK(t *testing.T) {
	t.Parallel()
	srv := fakeStatusServer(t, gatewayStatusResponse{}, http.StatusOK)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkGatewayReachable(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestCheckGatewayReachable_Fail_OnConnectRefused(t *testing.T) {
	t.Parallel()
	env := newEnv(t, "http://127.0.0.1:1", "k")
	got := checkGatewayReachable(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
	if got.Fix == "" {
		t.Fatal("expected fix hint on fail")
	}
}

func TestCheckGatewayAuth_FailWhenAPIKeyMissing(t *testing.T) {
	t.Parallel()
	env := newEnv(t, "http://example.invalid", "")
	got := checkGatewayAuth(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
	if !strings.Contains(got.Fix, "CORDUM_API_KEY") {
		t.Fatalf("expected fix mentioning CORDUM_API_KEY, got %q", got.Fix)
	}
}

func TestCheckGatewayAuth_401(t *testing.T) {
	t.Parallel()
	srv := fakeStatusServer(t, gatewayStatusResponse{}, http.StatusOK)
	t.Cleanup(srv.Close)
	// Empty api-key forces a 401 from our fake server.
	env := newEnv(t, srv.URL, "")
	got := checkGatewayAuth(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail on 401, got %+v", got)
	}
}

func TestCheckGatewayAuth_Populates_Status(t *testing.T) {
	t.Parallel()
	want := gatewayStatusResponse{
		Build:   gatewayBuildResponse{Version: "v0.1.2"},
		NATS:    gatewayNATSResponse{Connected: true, Status: "connected"},
		Redis:   gatewayRedisResponse{OK: true},
		Workers: gatewayWorkersSummary{Count: 3},
	}
	srv := fakeStatusServer(t, want, http.StatusOK)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkGatewayAuth(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
	if env.status == nil {
		t.Fatal("expected env.status populated")
	}
	if env.status.Build.Version != "v0.1.2" {
		t.Fatalf("status build version not preserved: %+v", env.status.Build)
	}
}

func TestCheckNATSConnected_FailWhenDisconnected(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{status: &gatewayStatusResponse{NATS: gatewayNATSResponse{Connected: false}}}
	got := checkNATSConnected(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
	if !strings.Contains(got.Fix, "nats") {
		t.Fatalf("expected fix to reference nats, got %q", got.Fix)
	}
}

func TestCheckNATSConnected_OK(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{status: &gatewayStatusResponse{NATS: gatewayNATSResponse{Connected: true, Status: "connected"}}}
	got := checkNATSConnected(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestCheckRedisOK_Branches(t *testing.T) {
	t.Parallel()
	envOK := &doctorEnv{status: &gatewayStatusResponse{Redis: gatewayRedisResponse{OK: true}}}
	if r := checkRedisOK(context.Background(), envOK); r.State != stateOK {
		t.Fatalf("ok branch: %+v", r)
	}
	envFail := &doctorEnv{status: &gatewayStatusResponse{Redis: gatewayRedisResponse{OK: false}}}
	if r := checkRedisOK(context.Background(), envFail); r.State != stateFail {
		t.Fatalf("fail branch: %+v", r)
	}
	envNoStatus := &doctorEnv{}
	if r := checkRedisOK(context.Background(), envNoStatus); r.State != stateSkip {
		t.Fatalf("skip branch: %+v", r)
	}
}

func TestCheckWorkersRegistered_WarnOnZero(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{status: &gatewayStatusResponse{Workers: gatewayWorkersSummary{Count: 0}}}
	if r := checkWorkersRegistered(context.Background(), env); r.State != stateWarn {
		t.Fatalf("expected warn, got %+v", r)
	}
}

func TestCheckWorkersRegistered_SkipFlag(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{
		status:      &gatewayStatusResponse{Workers: gatewayWorkersSummary{Count: 0}},
		skipWorkers: true,
	}
	if r := checkWorkersRegistered(context.Background(), env); r.State != stateSkip {
		t.Fatalf("expected skip, got %+v", r)
	}
}

func TestCheckBuildInfo_WarnOnDev(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "dev"}}}
	if r := checkBuildInfo(context.Background(), env); r.State != stateWarn {
		t.Fatalf("expected warn, got %+v", r)
	}
}

func TestCheckBuildInfo_OKOnVersion(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "v0.5.0"}}}
	if r := checkBuildInfo(context.Background(), env); r.State != stateOK {
		t.Fatalf("expected ok, got %+v", r)
	}
}

// makeCert generates a self-signed CA cert valid for the given
// duration and writes it to dir/ca.crt. Returns the path.
func makeCert(t *testing.T, dir string, validFor time.Duration) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "doctor-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validFor),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	path := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

func TestCheckTLSCertExpiry_OK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := makeCert(t, dir, 30*24*time.Hour)
	env := &doctorEnv{caCert: path}
	if r := checkTLSCertExpiry(context.Background(), env); r.State != stateOK {
		t.Fatalf("expected ok, got %+v", r)
	}
}

func TestCheckTLSCertExpiry_WarnUnder7Days(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := makeCert(t, dir, 3*24*time.Hour)
	env := &doctorEnv{caCert: path}
	r := checkTLSCertExpiry(context.Background(), env)
	if r.State != stateWarn {
		t.Fatalf("expected warn at 3 days, got %+v", r)
	}
	if !strings.Contains(r.Fix, "generate-certs") {
		t.Fatalf("expected fix to mention generate-certs, got %q", r.Fix)
	}
}

func TestCheckTLSCertExpiry_FailUnder24h(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := makeCert(t, dir, 12*time.Hour)
	env := &doctorEnv{caCert: path}
	if r := checkTLSCertExpiry(context.Background(), env); r.State != stateFail {
		t.Fatalf("expected fail at 12h, got %+v", r)
	}
}

func TestCheckTLSCertExpiry_SkipWhenAbsent(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{caCert: filepath.Join(t.TempDir(), "missing.crt")}
	if r := checkTLSCertExpiry(context.Background(), env); r.State != stateSkip {
		t.Fatalf("expected skip on missing cert, got %+v", r)
	}
}

func TestParseServiceURLOverrides(t *testing.T) {
	t.Parallel()
	got := parseServiceURLOverrides("scheduler=http://a:1, mcp = http://b:2 ,bogus,=v")
	if got["scheduler"] != "http://a:1" {
		t.Fatalf("scheduler: %q", got["scheduler"])
	}
	if got["mcp"] != "http://b:2" {
		t.Fatalf("mcp: %q", got["mcp"])
	}
	if _, ok := got["bogus"]; ok {
		t.Fatal("bogus entry must be ignored")
	}
}

func TestSummaryAndExitCode(t *testing.T) {
	t.Parallel()
	results := []checkResult{
		{ID: "a", State: stateOK},
		{ID: "b", State: stateWarn},
		{ID: "c", State: stateFail},
		{ID: "d", State: stateSkip},
	}
	sum := summaryCounts(results)
	if sum["ok"] != 1 || sum["warn"] != 1 || sum["fail"] != 1 || sum["skip"] != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if computeExitCode(results, false) != 1 {
		t.Fatal("expected exit 1 when fail present")
	}
	if computeExitCode([]checkResult{{State: stateOK}, {State: stateWarn}}, true) != 1 {
		t.Fatal("expected --strict to promote warn to fail")
	}
	if computeExitCode([]checkResult{{State: stateOK}, {State: stateWarn}}, false) != 0 {
		t.Fatal("default mode should not exit on warn")
	}
}

func TestRunDoctorChecks_RespectsOverallDeadline(t *testing.T) {
	t.Parallel()
	// Use a pre-cancelled context instead of a tiny timeout + sleep: on CI
	// under -race the 1ms timer + 5ms sleep racy window occasionally lets
	// the check execute before ctx.Err() fires. Cancelling explicitly is
	// deterministic and equivalent for the invariant under test (any
	// terminal ctx state short-circuits the check loop to stateSkip).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := &doctorEnv{}
	results := runDoctorChecks(ctx, env, []doctorCheck{
		{id: "x", label: "X", run: func(_ context.Context, _ *doctorEnv) checkResult {
			return checkResult{State: stateOK}
		}},
	})
	if len(results) != 1 || results[0].State != stateSkip {
		t.Fatalf("expected single skip after deadline, got %+v", results)
	}
}

// Service-probe checks (step-5).

func TestProbeServiceReadyz_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	env := newEnv(t, "http://ignored", "k")
	p := serviceProbe{id: "scheduler", defaultURL: srv.URL + "/readyz", fix: "docker compose logs scheduler"}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, false)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestProbeServiceReadyz_FailOnNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	env := newEnv(t, "http://ignored", "k")
	p := serviceProbe{id: "mcp", defaultURL: srv.URL + "/readyz", fix: "docker compose logs mcp"}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, false)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
	if !strings.Contains(got.Fix, "mcp") {
		t.Errorf("fix hint missing service name: %q", got.Fix)
	}
	if !strings.Contains(got.Detail, "503") {
		t.Errorf("detail missing status code: %q", got.Detail)
	}
}

func TestProbeServiceReadyz_SkipWhenPortUnboundButGatewayHealthy(t *testing.T) {
	t.Parallel()
	env := newEnv(t, "http://ignored", "k")
	env.status = &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.3"}}
	p := serviceProbe{
		id:               "scheduler",
		defaultURL:       "http://127.0.0.1:1/readyz",
		portMessage:      "scheduler host port 9090",
		fix:              "unused",
		optionalHostPort: true,
	}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, false)
	if got.State != stateSkip {
		t.Fatalf("expected skip when gateway healthy + port unbound, got %+v", got)
	}
	if !strings.Contains(got.Detail, "not exposed") {
		t.Errorf("skip detail should explain port-not-exposed semantics: %q", got.Detail)
	}
}

func TestProbeServiceReadyz_FailWhenPortUnboundAndGatewayUnknown(t *testing.T) {
	t.Parallel()
	env := newEnv(t, "http://ignored", "k") // env.status stays nil
	p := serviceProbe{
		id:               "scheduler",
		defaultURL:       "http://127.0.0.1:1/readyz",
		portMessage:      "scheduler host port 9090",
		fix:              "docker compose logs scheduler",
		optionalHostPort: true,
	}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, false)
	if got.State != stateFail {
		t.Fatalf("expected fail when gateway state unknown, got %+v", got)
	}
}

func TestProbeServiceReadyz_SkipOptionalHostPortNon2xxWhenGatewayHealthy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	env := newEnv(t, "http://ignored", "k")
	env.status = &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.3"}}
	p := serviceProbe{
		id:               "scheduler",
		defaultURL:       srv.URL + "/readyz",
		portMessage:      "scheduler host port 9090",
		fix:              "docker compose logs scheduler",
		optionalHostPort: true,
	}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, false)
	if got.State != stateSkip {
		t.Fatalf("expected skip when optional host port returns non-2xx + gateway healthy, got %+v", got)
	}
	if !strings.Contains(got.Detail, "returned 404") || !strings.Contains(got.Detail, "reports deploy up") {
		t.Errorf("skip detail should explain occupied/incorrect optional port semantics: %q", got.Detail)
	}
}

func TestProbeServiceReadyz_FailOptionalHostPortNon2xxWhenOverridden(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	env := newEnv(t, "http://ignored", "k")
	env.status = &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.3"}}
	p := serviceProbe{
		id:               "scheduler",
		defaultURL:       srv.URL + "/readyz",
		portMessage:      "scheduler host port 9090",
		fix:              "docker compose logs scheduler",
		optionalHostPort: true,
	}
	got := probeServiceReadyz(context.Background(), env, p, p.defaultURL, true)
	if got.State != stateFail {
		t.Fatalf("expected fail when explicit optional service override returns non-2xx, got %+v", got)
	}
	if !strings.Contains(got.Detail, "returned 404") {
		t.Errorf("detail missing status code: %q", got.Detail)
	}
}

func TestMakeServiceProbeCheck_HonoursServiceURLOverride(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	env := newEnv(t, "http://ignored", "k")
	env.serviceURL = map[string]string{"mcp": srv.URL + "/custom"}
	p := serviceProbe{id: "mcp", defaultURL: "http://127.0.0.1:1/readyz"}
	run := makeServiceProbeCheck(p)
	got := run(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("override URL not used, got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("expected override hit once, got %d", calls)
	}
}

func TestDefaultChecks_IncludesAllServiceProbes(t *testing.T) {
	t.Parallel()
	checks := defaultChecks()
	seen := map[string]bool{}
	for _, c := range checks {
		seen[c.id] = true
	}
	for _, p := range defaultServiceProbes {
		if !seen["service_"+p.id] {
			t.Errorf("defaultChecks missing service_%s probe", p.id)
		}
	}
}

// Policy + pack checks (step-6).

// newPackPolicyServer spins up a fake gateway that serves /api/v1/packs,
// /api/v1/policy/bundles, and /api/v1/policy/rules with configurable
// payloads. Defaults to a demo-rules payload so the common OK path
// tests stay terse; override via newPackPolicyServerWithRules when a
// test needs a different rules response (e.g. regression for the
// "unrelated bundle enabled, demo bundle missing" case).
func newPackPolicyServer(t *testing.T, packsBody string, packsStatus int, bundlesBody string, bundlesStatus int) *httptest.Server {
	t.Helper()
	return newPackPolicyServerWithRules(
		t,
		packsBody, packsStatus,
		bundlesBody, bundlesStatus,
		`{"items":[{"id":"demo-quickstart-greet-allow","fragment_id":"demo-quickstart","decision":"allow"},{"id":"demo-quickstart-delete-deny","fragment_id":"demo-quickstart","decision":"deny"},{"id":"demo-quickstart-admin-approve","fragment_id":"demo-quickstart","decision":"require_approval"}]}`,
		0,
	)
}

func newPackPolicyServerWithRules(t *testing.T, packsBody string, packsStatus int, bundlesBody string, bundlesStatus int, rulesBody string, rulesStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/packs", func(w http.ResponseWriter, _ *http.Request) {
		if packsStatus != 0 {
			w.WriteHeader(packsStatus)
		}
		_, _ = w.Write([]byte(packsBody))
	})
	mux.HandleFunc("/api/v1/policy/bundles", func(w http.ResponseWriter, _ *http.Request) {
		if bundlesStatus != 0 {
			w.WriteHeader(bundlesStatus)
		}
		_, _ = w.Write([]byte(bundlesBody))
	})
	mux.HandleFunc("/api/v1/policy/rules", func(w http.ResponseWriter, _ *http.Request) {
		if rulesStatus != 0 {
			w.WriteHeader(rulesStatus)
		}
		_, _ = w.Write([]byte(rulesBody))
	})
	return httptest.NewServer(mux)
}

func TestCheckDemoPackInstalled_OK(t *testing.T) {
	t.Parallel()
	srv := newPackPolicyServer(t, `{"items":[{"id":"demo-quickstart"},{"id":"slack"}]}`, 0, "{}", 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkDemoPackInstalled(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestCheckDemoPackInstalled_WarnWhenMissing(t *testing.T) {
	t.Parallel()
	srv := newPackPolicyServer(t, `{"items":[{"id":"slack"}]}`, 0, "{}", 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkDemoPackInstalled(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn, got %+v", got)
	}
	if !strings.Contains(got.Fix, "pack install") {
		t.Errorf("fix missing install hint: %q", got.Fix)
	}
}

func TestCheckDemoPackInstalled_SkipWhenNoAPIKey(t *testing.T) {
	t.Parallel()
	env := newEnv(t, "http://example.invalid", "")
	got := checkDemoPackInstalled(context.Background(), env)
	if got.State != stateSkip {
		t.Fatalf("expected skip, got %+v", got)
	}
}

func TestCheckDemoPackInstalled_FailOnNon2xx(t *testing.T) {
	t.Parallel()
	srv := newPackPolicyServer(t, "", http.StatusInternalServerError, "{}", 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkDemoPackInstalled(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
}

func TestCheckPolicyBundleLoaded_OK(t *testing.T) {
	t.Parallel()
	body := `{"items":[{"id":"secops","enabled":true,"rules_count":5}]}`
	srv := newPackPolicyServer(t, "{}", 0, body, 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestCheckPolicyBundleLoaded_WarnOnEmpty(t *testing.T) {
	t.Parallel()
	srv := newPackPolicyServer(t, "{}", 0, `{"items":[]}`, 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn, got %+v", got)
	}
	if !strings.Contains(got.Detail, "no policy bundles") {
		t.Errorf("detail should mention empty bundles: %q", got.Detail)
	}
}

func TestCheckPolicyBundleLoaded_WarnOnAllDisabled(t *testing.T) {
	t.Parallel()
	body := `{"items":[{"id":"secops","enabled":false}]}`
	srv := newPackPolicyServer(t, "{}", 0, body, 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn, got %+v", got)
	}
	if !strings.Contains(got.Detail, "none enabled") {
		t.Errorf("detail should mention no bundles enabled: %q", got.Detail)
	}
}

// TestCheckPolicyBundleLoaded_WarnWhenUnrelatedBundleEnabledButDemoMissing
// is the regression test QA called for. It proves the check does NOT
// treat "any enabled bundle" as success — an unrelated enabled bundle
// (e.g. secops/core) with no demo-quickstart-* rules must surface as
// warn with a fix pointing to the demo pack install. Without this
// test the install-verification DoD is indistinguishable from a
// generic "some policy exists" check.
func TestCheckPolicyBundleLoaded_WarnWhenUnrelatedBundleEnabledButDemoMissing(t *testing.T) {
	t.Parallel()
	bundles := `{"items":[{"id":"secops/core","enabled":true,"rules_count":2}]}`
	rules := `{"items":[{"id":"block-prod-writes","fragment_id":"secops/core","decision":"deny"},{"id":"allow-reads","fragment_id":"secops/core","decision":"allow"}]}`
	srv := newPackPolicyServerWithRules(t, "{}", 0, bundles, 0, rules, 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn (demo absent), got %+v", got)
	}
	if !strings.Contains(got.Detail, "demo-quickstart") {
		t.Errorf("detail must mention demo-quickstart missing: %q", got.Detail)
	}
	if !strings.Contains(got.Fix, "pack install") {
		t.Errorf("fix must point at pack install: %q", got.Fix)
	}
}

// TestCheckPolicyBundleLoaded_OKWhenDemoAndOtherBundlesCoexist pins the
// positive case: secops + demo-quickstart bundles both enabled, demo
// rules parsed → ok with rule-count detail. Prevents the check from
// accidentally regressing to "must be the only bundle" semantics.
func TestCheckPolicyBundleLoaded_OKWhenDemoAndOtherBundlesCoexist(t *testing.T) {
	t.Parallel()
	bundles := `{"items":[{"id":"secops/core","enabled":true,"rules_count":2},{"id":"default","enabled":true,"rules_count":3}]}`
	rules := `{"items":[{"id":"block-prod-writes","fragment_id":"secops/core","decision":"deny"},{"id":"demo-quickstart-greet-allow","fragment_id":"default","decision":"allow"},{"id":"demo-quickstart-delete-deny","fragment_id":"default","decision":"deny"},{"id":"demo-quickstart-admin-approve","fragment_id":"default","decision":"require_approval"}]}`
	srv := newPackPolicyServerWithRules(t, "{}", 0, bundles, 0, rules, 0)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
	if !strings.Contains(got.Detail, "full demo-quickstart ruleset") {
		t.Errorf("detail should advertise full ruleset: %q", got.Detail)
	}
}

// TestCheckPolicyBundleLoaded_WarnOnPartialDemoRuleset is the
// regression QA demanded in reopen #2. Scenario: one demo rule
// present (e.g. the ALLOW rule survived), but the DENY + APPROVE
// rules are missing. The prior code returned ok because it only
// counted `demoRules > 0`; that false-greens the install even
// though the ALLOW/DENY/APPROVE quickstart demo is broken. This
// test pins the required behaviour: any missing rule id from
// demoPolicyRequiredRules → stateWarn with detail + fix naming
// the missing ids. Three sub-tests cover the three natural
// partial-subset shapes.
func TestCheckPolicyBundleLoaded_WarnOnPartialDemoRuleset(t *testing.T) {
	t.Parallel()
	bundles := `{"items":[{"id":"default","enabled":true,"rules_count":1}]}`
	cases := []struct {
		name     string
		rules    string
		wantMiss []string
	}{
		{
			name:     "only_allow_rule_present",
			rules:    `{"items":[{"id":"demo-quickstart-greet-allow","fragment_id":"default","decision":"allow"}]}`,
			wantMiss: []string{"demo-quickstart-delete-deny", "demo-quickstart-admin-approve"},
		},
		{
			name:     "only_deny_rule_present",
			rules:    `{"items":[{"id":"demo-quickstart-delete-deny","fragment_id":"default","decision":"deny"}]}`,
			wantMiss: []string{"demo-quickstart-greet-allow", "demo-quickstart-admin-approve"},
		},
		{
			name:     "allow_and_deny_but_no_approve",
			rules:    `{"items":[{"id":"demo-quickstart-greet-allow","fragment_id":"default","decision":"allow"},{"id":"demo-quickstart-delete-deny","fragment_id":"default","decision":"deny"}]}`,
			wantMiss: []string{"demo-quickstart-admin-approve"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := newPackPolicyServerWithRules(t, "{}", 0, bundles, 0, c.rules, 0)
			t.Cleanup(srv.Close)
			env := newEnv(t, srv.URL, "k")
			got := checkPolicyBundleLoaded(context.Background(), env)
			if got.State != stateWarn {
				t.Fatalf("expected warn for partial demo ruleset, got %+v", got)
			}
			if !strings.Contains(got.Detail, "partial demo policy") {
				t.Errorf("detail should flag partial policy: %q", got.Detail)
			}
			for _, id := range c.wantMiss {
				if !strings.Contains(got.Detail, id) {
					t.Errorf("detail should name missing rule %q; got %q", id, got.Detail)
				}
			}
			if !strings.Contains(got.Fix, "pack install") {
				t.Errorf("fix should point at pack install: %q", got.Fix)
			}
		})
	}
}

// TestCheckPolicyBundleLoaded_WarnWhenRulesEndpointFails pins the
// graceful-degradation path: /api/v1/policy/rules returning 500 after
// a successful bundle list should still surface as warn (operator
// knows the gateway answers) rather than fail (which would imply the
// whole gateway is down).
func TestCheckPolicyBundleLoaded_WarnWhenRulesEndpointFails(t *testing.T) {
	t.Parallel()
	bundles := `{"items":[{"id":"default","enabled":true}]}`
	srv := newPackPolicyServerWithRules(t, "{}", 0, bundles, 0, "", http.StatusInternalServerError)
	t.Cleanup(srv.Close)
	env := newEnv(t, srv.URL, "k")
	got := checkPolicyBundleLoaded(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn, got %+v", got)
	}
	if !strings.Contains(got.Detail, "/api/v1/policy/rules returned 500") {
		t.Errorf("detail should mention the 500: %q", got.Detail)
	}
}

// Version-skew check (step-8).

func TestCheckVersionSkew_OKWhenMatching(t *testing.T) {
	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "1.2.3"
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.3"}}}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok, got %+v", got)
	}
}

func TestCheckVersionSkew_WarnOnMinorSkew(t *testing.T) {
	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "1.2.3"
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.3.0"}}}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateWarn {
		t.Fatalf("expected warn, got %+v", got)
	}
}

func TestCheckVersionSkew_FailOnMajorSkew(t *testing.T) {
	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "1.2.3"
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "2.0.0"}}}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateFail {
		t.Fatalf("expected fail, got %+v", got)
	}
	if !strings.Contains(got.Fix, "docker compose pull") {
		t.Errorf("expected docker compose pull fix: %q", got.Fix)
	}
}

func TestCheckVersionSkew_SkipOnDevOrUnknown(t *testing.T) {
	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "dev"
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.3"}}}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateSkip {
		t.Fatalf("expected skip on dev CLI, got %+v", got)
	}
}

func TestCheckVersionSkew_OKOnPatchDiff(t *testing.T) {
	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "1.2.3"
	env := &doctorEnv{status: &gatewayStatusResponse{Build: gatewayBuildResponse{Version: "1.2.4"}}}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("expected ok for patch-level diff, got %+v", got)
	}
}

func TestCheckVersionSkew_SkipWhenNoStatus(t *testing.T) {
	t.Parallel()
	env := &doctorEnv{}
	got := checkVersionSkew(context.Background(), env)
	if got.State != stateSkip {
		t.Fatalf("expected skip, got %+v", got)
	}
}

func TestSplitMajorMinor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		maj, min int
		ok       bool
	}{
		{"1.2.3", 1, 2, true},
		{"v1.2.3", 1, 2, true},
		{"1.2.3-rc1", 1, 2, true},
		{"1.2.3+build.1", 1, 2, true},
		{"1.2", 1, 2, true},
		{"1", 0, 0, false},
		{"", 0, 0, false},
		{"x.y.z", 0, 0, false},
	}
	for _, c := range cases {
		maj, min, ok := splitMajorMinor(c.in)
		if maj != c.maj || min != c.min || ok != c.ok {
			t.Errorf("splitMajorMinor(%q) = (%d, %d, %t); want (%d, %d, %t)", c.in, maj, min, ok, c.maj, c.min, c.ok)
		}
	}
}

// Interactive --fix mode (step-10).

func TestIsDestructiveFix(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"docker compose logs scheduler":    false,
		"cordumctl pack install ./demo":    false,
		"cordumctl generate-certs --force": true,
		"docker compose down -v":           true,
		"git reset --hard HEAD":            true,
		"rm -rf /var/lib/redis":            true,
		"cordumctl policy activate demo":   false,
	}
	for in, want := range cases {
		if got := isDestructiveFix(in); got != want {
			t.Errorf("isDestructiveFix(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestRunInteractiveFixes_YRunsAndReRuns(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{
		{
			id: "x", label: "X",
			run: func(_ context.Context, _ *doctorEnv) checkResult {
				return checkResult{ID: "x", Label: "X", State: stateOK, Detail: "recovered"}
			},
		},
	}
	initial := []checkResult{{ID: "x", Label: "X", State: stateFail, Detail: "was broken", Fix: "docker compose logs scheduler"}}
	stdin := strings.NewReader("y\n")
	stdout := &bytes.Buffer{}
	runs := 0
	runner := func(_ context.Context, cmd string) (string, error) {
		runs++
		if !strings.Contains(cmd, "docker compose logs") {
			t.Errorf("runner got wrong command: %q", cmd)
		}
		return "restart ok", nil
	}
	updated := runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner)
	if runs != 1 {
		t.Fatalf("expected runner called once, got %d", runs)
	}
	if updated[0].State != stateOK {
		t.Fatalf("expected check to flip to ok after fix, got %+v", updated[0])
	}
	if !strings.Contains(stdout.String(), "re-run: ok") {
		t.Errorf("stdout missing re-run marker:\n%s", stdout.String())
	}
}

func TestRunInteractiveFixes_NSkipsFix(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{{
		id: "x", label: "X",
		run: func(_ context.Context, _ *doctorEnv) checkResult {
			t.Fatal("re-check should not run when user declines")
			return checkResult{}
		},
	}}
	initial := []checkResult{{ID: "x", Label: "X", State: stateFail, Fix: "docker compose logs x"}}
	stdin := strings.NewReader("n\n")
	stdout := &bytes.Buffer{}
	called := false
	runner := func(_ context.Context, _ string) (string, error) {
		called = true
		return "", nil
	}
	updated := runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner)
	if called {
		t.Fatal("runner must not execute when user declines")
	}
	if updated[0].State != stateFail {
		t.Fatalf("expected fail preserved, got %+v", updated[0])
	}
}

func TestRunInteractiveFixes_AAborts(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{
		{id: "a", label: "A", run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} }},
		{id: "b", label: "B", run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} }},
	}
	initial := []checkResult{
		{ID: "a", Label: "A", State: stateFail, Fix: "cmd-a"},
		{ID: "b", Label: "B", State: stateFail, Fix: "cmd-b"},
	}
	stdin := strings.NewReader("a\n")
	stdout := &bytes.Buffer{}
	runs := 0
	runner := func(_ context.Context, _ string) (string, error) {
		runs++
		return "", nil
	}
	updated := runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner)
	if runs != 0 {
		t.Fatalf("aborting must not execute any command, got %d runs", runs)
	}
	if updated[0].State != stateFail || updated[1].State != stateFail {
		t.Fatalf("abort must preserve original fail states: %+v", updated)
	}
}

func TestRunInteractiveFixes_DestructiveRequiresSecondConfirm(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{{
		id: "x", label: "X",
		run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} },
	}}
	initial := []checkResult{{ID: "x", Label: "X", State: stateFail, Fix: "cordumctl generate-certs --force --days 365"}}
	runner := func(_ context.Context, _ string) (string, error) { return "", nil }

	// First prompt "y", confirmation "no" → fix must NOT run.
	stdout := &bytes.Buffer{}
	ran := false
	stdin := strings.NewReader("y\nno\n")
	guard := func(_ context.Context, c string) (string, error) {
		ran = true
		return "", nil
	}
	_ = runner
	_ = runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, guard)
	if ran {
		t.Fatalf("destructive fix ran without the 'yes' confirmation")
	}

	// First prompt "y", confirmation "yes" → fix must run.
	stdout = &bytes.Buffer{}
	ran = false
	stdin = strings.NewReader("y\nyes\n")
	runner2 := func(_ context.Context, _ string) (string, error) {
		ran = true
		return "", nil
	}
	_ = runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner2)
	if !ran {
		t.Fatalf("destructive fix with explicit 'yes' confirmation did not execute")
	}
}

func TestRunInteractiveFixes_EOFIsSkip(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{{
		id: "x", label: "X",
		run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} },
	}}
	initial := []checkResult{{ID: "x", Label: "X", State: stateFail, Fix: "docker compose logs x"}}
	stdin := &bytes.Buffer{} // empty stdin → immediate EOF
	stdout := &bytes.Buffer{}
	ran := false
	runner := func(_ context.Context, _ string) (string, error) {
		ran = true
		return "", nil
	}
	updated := runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner)
	if ran {
		t.Fatalf("EOF must not trigger execution")
	}
	if updated[0].State != stateFail {
		t.Fatalf("EOF must preserve fail state, got %+v", updated[0])
	}
	_ = errors.New
}

func TestRunInteractiveFixes_SkipsNonFailingOrFixless(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{
		{id: "ok", label: "OK", run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} }},
		{id: "nofix", label: "NoFix", run: func(_ context.Context, _ *doctorEnv) checkResult { return checkResult{State: stateOK} }},
	}
	initial := []checkResult{
		{ID: "ok", Label: "OK", State: stateOK},
		{ID: "nofix", Label: "NoFix", State: stateFail},
	}
	stdin := strings.NewReader("y\n")
	stdout := &bytes.Buffer{}
	runs := 0
	runner := func(_ context.Context, _ string) (string, error) {
		runs++
		return "", nil
	}
	_ = runInteractiveFixes(stdin, stdout, context.Background(), &doctorEnv{}, checks, initial, runner)
	if runs != 0 {
		t.Fatalf("ok + fixless-fail must not prompt; got %d runs", runs)
	}
}

// Output renderers + end-to-end coverage (step-11).

func TestEmitJSONTo_SchemaShape(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	results := []checkResult{
		{ID: "gateway_reachable", Label: "Gateway reachable", State: stateOK, Detail: "ok"},
		{ID: "nats_connected", Label: "NATS connected", State: stateFail, Detail: "down", Fix: "docker compose logs nats"},
	}
	emitJSONTo(buf, results, true)
	var payload struct {
		Checks   []checkResult  `json:"checks"`
		Summary  map[string]int `json:"summary"`
		ExitCode int            `json:"exitCode"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("json decode: %v — raw:\n%s", err, buf.String())
	}
	if len(payload.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(payload.Checks))
	}
	for _, key := range []string{"ok", "warn", "fail", "skip"} {
		if _, ok := payload.Summary[key]; !ok {
			t.Errorf("summary missing %q key", key)
		}
	}
	if payload.Summary["fail"] != 1 {
		t.Errorf("expected 1 fail in summary, got %d", payload.Summary["fail"])
	}
	if payload.ExitCode != 1 {
		t.Errorf("expected exitCode=1 (fail present), got %d", payload.ExitCode)
	}
}

func TestEmitHumanTo_RendersStableOrderWithoutColor(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	results := []checkResult{
		{ID: "zeta", Label: "Zeta", State: stateOK, Detail: "good"},
		{ID: "alpha", Label: "Alpha", State: stateFail, Detail: "bad", Fix: "fix-it"},
	}
	emitHumanTo(buf, false, results, false)
	out := buf.String()
	// Stable sort ⇒ alpha comes before zeta even though it was added second.
	alphaIdx := strings.Index(out, "alpha")
	zetaIdx := strings.Index(out, "zeta")
	if alphaIdx == -1 || zetaIdx == -1 || alphaIdx > zetaIdx {
		t.Fatalf("results not sorted by id: alpha=%d zeta=%d\n%s", alphaIdx, zetaIdx, out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("unexpected ANSI escape when useColor=false:\n%s", out)
	}
	if !strings.Contains(out, "fix-it") {
		t.Errorf("fix column missing from output:\n%s", out)
	}
	if !strings.Contains(out, "2 checks:") {
		t.Errorf("summary line missing:\n%s", out)
	}
}

func TestEmitHumanTo_WithColorEmitsANSI(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	results := []checkResult{{ID: "x", Label: "X", State: stateFail, Detail: "down"}}
	emitHumanTo(buf, true, results, false)
	if !strings.Contains(buf.String(), "\x1b[31m") {
		t.Errorf("expected red ANSI for fail state, got: %q", buf.String())
	}
}

func TestEmitHumanTo_TruncatesLongDetailUnlessVerbose(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200)
	results := []checkResult{{ID: "x", Label: "X", State: stateOK, Detail: long}}

	short := &bytes.Buffer{}
	emitHumanTo(short, false, results, false)
	if strings.Contains(short.String(), long) {
		t.Errorf("non-verbose should truncate long detail")
	}
	if !strings.Contains(short.String(), "...") {
		t.Errorf("non-verbose should end truncated detail with ellipsis")
	}

	verbose := &bytes.Buffer{}
	emitHumanTo(verbose, false, results, true)
	if !strings.Contains(verbose.String(), long) {
		t.Errorf("verbose mode should preserve full detail")
	}
}

func TestColorise_EachStateHasDistinctEscape(t *testing.T) {
	t.Parallel()
	seen := map[string]checkState{}
	for _, s := range []checkState{stateOK, stateWarn, stateFail, stateSkip} {
		code := colorise(s)
		if code == "" {
			t.Errorf("colorise(%q) returned empty string", s)
		}
		if prev, dup := seen[code]; dup {
			t.Errorf("colorise duplicate: %q == %q (code=%q)", prev, s, code)
		}
		seen[code] = s
	}
	if colorise(checkState("unknown")) != "" {
		t.Errorf("colorise of unknown state should return empty")
	}
}

func TestShouldUseColor_NOCOLOREnvDisables(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// Use a regular file — never a char device, so even without NO_COLOR
	// the answer would be false. The point here is NO_COLOR short-circuits
	// before the stat() call, so a non-existent path is safe.
	f, err := os.CreateTemp("", "doctor-nocolor-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()); _ = f.Close() })
	if shouldUseColor(f) {
		t.Fatal("shouldUseColor must return false when NO_COLOR is set")
	}
}

func TestShouldUseColor_NonTTYReturnsFalse(t *testing.T) {
	// Unset NO_COLOR to isolate the isatty branch.
	t.Setenv("NO_COLOR", "")
	f, err := os.CreateTemp("", "doctor-notty-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()); _ = f.Close() })
	if shouldUseColor(f) {
		t.Fatal("shouldUseColor must return false for a regular file (non-tty)")
	}
}

// TestRunDoctorChecks_EndToEnd_AllGreen drives the full check sequence
// against an httptest server that mimics a healthy stack. Exit code
// must be 0 and every service probe must land on ok (every probe URL
// is overridden to the fake server so the host's real ports don't
// bleed into the test).
func TestRunDoctorChecks_EndToEnd_AllGreen(t *testing.T) {
	status := gatewayStatusResponse{
		NATS:    gatewayNATSResponse{Connected: true, Status: "ok"},
		Redis:   gatewayRedisResponse{OK: true},
		Workers: gatewayWorkersSummary{Count: 2},
		Build:   gatewayBuildResponse{Version: "1.2.3"},
	}
	packsBody := `{"items":[{"id":"demo-quickstart"}]}`
	bundlesBody := `{"items":[{"id":"default","enabled":true,"rules_count":7}]}`

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("/api/v1/packs", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(packsBody)) })
	mux.HandleFunc("/api/v1/policy/bundles", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(bundlesBody)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	old := doctorCLIVersion
	t.Cleanup(func() { doctorCLIVersion = old })
	doctorCLIVersion = "1.2.3"

	env := newEnv(t, srv.URL, "k")
	env.tenant = "default"
	// Redirect every service probe to the fake server so the host's
	// real listener (if any) doesn't influence the result.
	overrides := map[string]string{}
	for _, p := range defaultServiceProbes {
		overrides[p.id] = srv.URL + "/readyz"
	}
	env.serviceURL = overrides

	results := runDoctorChecks(context.Background(), env, defaultChecks())
	if computeExitCode(results, false) != 0 {
		t.Fatalf("end-to-end all-green case must exit 0; results:\n%+v", results)
	}
	if got := summaryCounts(results); got["fail"] != 0 {
		t.Fatalf("unexpected fail count in all-green run: %+v", got)
	}
}

func TestParseServiceURLOverrides_Table(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"":                                {},
		"scheduler=http://a":              {"scheduler": "http://a"},
		"scheduler=http://a,mcp=http://b": {"scheduler": "http://a", "mcp": "http://b"},
		"scheduler=http://a,malformed,mcp=http://b": {"scheduler": "http://a", "mcp": "http://b"},
		" scheduler = http://a ":                    {"scheduler": "http://a"},
	}
	for in, want := range cases {
		got := parseServiceURLOverrides(in)
		if len(got) != len(want) {
			t.Errorf("parseServiceURLOverrides(%q) size = %d, want %d", in, len(got), len(want))
			continue
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("parseServiceURLOverrides(%q)[%s] = %q, want %q", in, k, got[k], v)
			}
		}
	}
}

func TestDoctorShellRunner_EchoSucceeds(t *testing.T) {
	t.Parallel()
	out, err := doctorShellRunner(context.Background(), "echo cordum-doctor-smoke")
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if !strings.Contains(out, "cordum-doctor-smoke") {
		t.Fatalf("echo output missing token: %q", out)
	}
}

func TestEmitJSON_WritesEnvelopeToStdout(t *testing.T) {
	// emitJSON writes to os.Stdout; redirect via pipe so we can assert
	// the wrapper forwards to emitJSONTo. Not parallel because it
	// swaps a process-wide resource.
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		_ = w.Close()
		os.Stdout = orig
	}()

	results := []checkResult{{ID: "x", Label: "X", State: stateOK, Detail: "ok"}}
	emitJSON(results, false)
	_ = w.Close()

	buf := &bytes.Buffer{}
	_, _ = io.Copy(buf, r)
	if !strings.Contains(buf.String(), `"id": "x"`) {
		t.Fatalf("emitJSON did not forward to stdout: %q", buf.String())
	}
}

func TestHumanDuration_Format(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]string{
		-time.Hour:         "expired",
		30 * time.Minute:   "30m",
		3 * time.Hour:      "3h",
		2 * 24 * time.Hour: "2d",
		7 * 24 * time.Hour: "7d",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q want %q", d, got, want)
		}
	}
}
