package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/safetykernel"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	storeWriterKeyID  = "storewriter"
	storeWriterRuleID = "login-burst"
)

var storeWriterEnvNames = []string{
	"CORDUM_ENV", "CORDUM_PRODUCTION", policysign.EnvStrictMode, policysign.EnvLegacyRequireSig,
	policysign.EnvSigningKey, policysign.EnvSigningKeyPath, policysign.EnvSigningKeyID, policysign.EnvDevSigningSeed,
	"SAFETY_POLICY_PUBLIC_KEY", "SAFETY_POLICY_PUBLIC_KEY_ID", "SAFETY_POLICY_URL", "SAFETY_POLICY_CONFIG_DISABLE",
	"SAFETY_POLICY_CONFIG_SCOPE", "SAFETY_POLICY_CONFIG_ID", "SAFETY_POLICY_CONFIG_KEY",
}

// storeWriterEnv starts every test from a clean policy-signing environment,
// then selects the profile and the strict mode the test asks for.
func storeWriterEnv(t *testing.T, production bool, strict string) {
	t.Helper()
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, policysign.EnvPublicKeyPrefix) {
			t.Setenv(name, "")
		}
	}
	for _, name := range storeWriterEnvNames {
		t.Setenv(name, "")
	}
	if production {
		t.Setenv("CORDUM_ENV", "production")
	}
	t.Setenv(policysign.EnvStrictMode, strict)
}

// storeWriterKey configures a signing key generated at runtime. trust selects
// what the policy trust store holds under its id: the matching public key
// ("trusted"), nothing ("absent") or a different public key ("other").
func storeWriterKey(t *testing.T, trust string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv(policysign.EnvSigningKey, base64.StdEncoding.EncodeToString(key))
	t.Setenv(policysign.EnvSigningKeyID, storeWriterKeyID)
	switch trust {
	case "trusted":
	case "other":
		pub, _, err = ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
	case "absent":
		return
	default:
		t.Fatalf("unknown trust %q", trust)
	}
	t.Setenv(policysign.EnvPublicKeyPrefix+strings.ToUpper(storeWriterKeyID), base64.StdEncoding.EncodeToString(pub))
}

type storeWriterCase struct {
	name, strict, trust string
	production          bool
}

func (c storeWriterCase) setup(t *testing.T) {
	t.Helper()
	storeWriterEnv(t, c.production, c.strict)
	if c.trust != "" {
		storeWriterKey(t, c.trust)
	}
}

// storeWriterRefusals are the profiles in which a policy-store writer cannot
// store content the enforced store source accepts.
var storeWriterRefusals = []storeWriterCase{
	{name: "production without a key", production: true},
	{name: "production key the trust store lacks", production: true, trust: "absent"},
	{name: "production key the trust store holds differently", production: true, trust: "other"},
	{name: "development with an unrecognised mode", strict: "bogus"},
}

// storeWriterKeyspace is the per-DB SCAN oracle: every key with its type and a
// canonical value.
func storeWriterKeyspace(t *testing.T, s *server) map[string]string {
	t.Helper()
	return snapshotPackKeyspace(t, s)
}

func storeWriterPublished(bus *stubBus) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return len(bus.published)
}

// storeWriterVerify checks a stored bundle's signature against the policy
// trust store, over exactly the content that was stored.
func storeWriterVerify(t *testing.T, bundle map[string]any) error {
	t.Helper()
	sig, ok := signatureFromMap(bundle[policyBundleSignatureKey])
	if !ok {
		return errors.New("bundle carries no signature")
	}
	store, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		return err
	}
	pub, found := store.Lookup(sig.KeyID)
	if !found {
		return fmt.Errorf("signature key %q is not trusted", sig.KeyID)
	}
	content, _ := bundle["content"].(string)
	return policysign.Verify(pub, []byte(content), sig)
}

func storeWriterBundle(t *testing.T, s *server, bundleID string) map[string]any {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), packs.PolicyConfigScope, packs.PolicyConfigID)
	require.NoError(t, err)
	bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
	bundle, ok := bundles[bundleID].(map[string]any)
	require.True(t, ok, "bundle %s is not stored", bundleID)
	return bundle
}

// storeWriterKernelLoad boots the real safety kernel over a copy of the stored
// policy document, with its store source in enforce, and returns the kernel's
// snapshots or the error that refused the whole load.
func storeWriterKernelLoad(t *testing.T, s *server) ([]string, error) {
	t.Helper()
	ctx := context.Background()
	doc, err := s.configSvc.Get(ctx, packs.PolicyConfigScope, packs.PolicyConfigID)
	require.NoError(t, err)
	mr := miniredis.RunT(t)
	svc, err := configsvc.New("redis://" + mr.Addr())
	require.NoError(t, err)
	defer func() { _ = svc.Close() }()
	require.NoError(t, svc.Set(ctx, &configsvc.Document{Scope: doc.Scope, ScopeID: doc.ScopeID, Data: doc.Data}))
	// The development profile keeps the kernel's production TLS check out of
	// the way; the explicit enforce still applies to the store source.
	restore := map[string]string{}
	for name, value := range map[string]string{"CORDUM_ENV": "", "CORDUM_PRODUCTION": "", policysign.EnvStrictMode: "enforce"} {
		restore[name] = os.Getenv(name)
		t.Setenv(name, value)
	}
	defer func() {
		for name, value := range restore {
			t.Setenv(name, value)
		}
	}()
	t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", reserveLocalAddr(t))
	// As in startRealSafetyKernel: a kernel that boots outlives the test, so it must not poll.
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "24h")
	addr := reserveLocalAddr(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- safetykernel.RunWithEntitlements(&runtimeconfig.Config{
			NatsURL: "nats://127.0.0.1:1", RedisURL: "redis://" + mr.Addr(), SafetyKernelAddr: addr,
		}, nil)
	}()
	conn, err := storeWriterDial(t, addr, errCh)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	resp, err := pb.NewSafetyKernelClient(conn).ListSnapshots(ctx, &pb.ListSnapshotsRequest{})
	require.NoError(t, err)
	return resp.GetSnapshots(), nil
}

// storeWriterDial waits for the kernel to accept connections, or returns the
// error it exited with.
func storeWriterDial(t *testing.T, addr string, errCh <-chan error) (*grpc.ClientConn, error) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		select {
		case runErr := <-errCh:
			_ = conn.Close()
			if runErr == nil {
				runErr = errors.New("safety kernel exited")
			}
			return nil, runErr
		default:
		}
		state := conn.GetState()
		if state == connectivity.Ready {
			return conn, nil
		}
		conn.Connect()
		waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn.WaitForStateChange(waitCtx, state)
		cancel()
	}
	_ = conn.Close()
	t.Fatalf("safety kernel did not listen on %s before the deadline", addr)
	return nil, nil
}

// storeWriterRequireLoads asserts that the enforced store source loads the
// stored document whole.
func storeWriterRequireLoads(t *testing.T, s *server) {
	t.Helper()
	snapshots, err := storeWriterKernelLoad(t, s)
	require.NoError(t, err)
	require.NotEmpty(t, snapshots)
	require.True(t, strings.HasPrefix(snapshots[0], "cfg:"), "snapshot %q", snapshots[0])
}

type storeWriterLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *storeWriterLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// records returns the captured records at level whose attribute key is value.
func (b *storeWriterLogBuffer) records(level, key, value string) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for line := range strings.Lines(b.buf.String()) {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["level"] == level && rec[key] == value {
			out = append(out, rec)
		}
	}
	return out
}

func storeWriterLogs(t *testing.T) *storeWriterLogBuffer {
	t.Helper()
	logs := &storeWriterLogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return logs
}

func storeWriterRefused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, errorCodePolicySigningRequired, body["code"])
}

func storeWriterVelocityGateway(t *testing.T) (*server, *stubBus) {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanTeam, func(e *licensing.Entitlements) { e.VelocityRules = true })
	return s, bus
}

func storeWriterVelocity(t *testing.T, s *server, method string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	if method == http.MethodPost {
		req := httptest.NewRequest(method, "/api/v1/policy/velocity-rules", bytes.NewReader(body))
		return performVelocityJSONRequest(t, req, s.handleCreateVelocityRule)
	}
	id, _ := payload["id"].(string)
	req := httptest.NewRequest(method, "/api/v1/policy/velocity-rules/"+id, bytes.NewReader(body))
	req.SetPathValue("id", id)
	return performVelocityJSONRequest(t, req, s.handlePutVelocityRule)
}

// storeWriterVelocityBundle is the bundle velocityRuleBundleMap builds for
// payload, stamped with the timestamps of stored.
func storeWriterVelocityBundle(t *testing.T, payload, stored map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var body velocityRuleUpsertRequest
	require.NoError(t, json.Unmarshal(raw, &body))
	def, err := normalizeVelocityRuleRequest(body, "")
	require.NoError(t, err)
	updatedAt, _ := stored["updated_at"].(string)
	createdAt, _ := stored["created_at"].(string)
	bundle, err := velocityRuleBundleMap(def, updatedAt, createdAt)
	require.NoError(t, err)
	return bundle
}

func storeWriterSeed(t *testing.T, s *server, bundles map[string]any) {
	t.Helper()
	require.NoError(t, s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope: packs.PolicyConfigScope, ScopeID: packs.PolicyConfigID, Data: map[string]any{packs.PolicyConfigKey: bundles},
	}))
}

func TestVelocityRuleWrite_EnforceStoresSignedBundle(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _ := storeWriterVelocityGateway(t)
	payload := velocityRulePayload(storeWriterRuleID)
	rec := storeWriterVelocity(t, s, http.MethodPost, payload)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := storeWriterBundle(t, s, velocityRuleBundleID(storeWriterRuleID))
	require.NoError(t, storeWriterVerify(t, created))
	sig, _ := signatureFromMap(created[policyBundleSignatureKey])
	require.Equal(t, storeWriterKeyID, sig.KeyID)
	storeWriterRequireLoads(t, s)

	payload["threshold"] = 5
	rec = storeWriterVelocity(t, s, http.MethodPut, payload)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated := storeWriterBundle(t, s, velocityRuleBundleID(storeWriterRuleID))
	require.NoError(t, storeWriterVerify(t, updated))
	require.NotEqual(t, created["content"], updated["content"])
	require.NotEqual(t, created[policyBundleSignatureKey], updated[policyBundleSignatureKey])
	storeWriterRequireLoads(t, s)
}

func TestVelocityRuleWrite_EnforceRefusesWithoutSigningKeyBeforeAnyWrite(t *testing.T) {
	for _, tc := range storeWriterRefusals {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			s, bus := storeWriterVelocityGateway(t)
			seeded := velocityRulePayload("seeded-burst")
			stamp := map[string]any{"updated_at": "2026-01-02T03:04:05Z"}
			storeWriterSeed(t, s, map[string]any{velocityRuleBundleID("seeded-burst"): storeWriterVelocityBundle(t, seeded, stamp)})
			before, published := storeWriterKeyspace(t, s), storeWriterPublished(bus)

			storeWriterRefused(t, storeWriterVelocity(t, s, http.MethodPost, velocityRulePayload(storeWriterRuleID)))
			seeded["threshold"] = 7
			storeWriterRefused(t, storeWriterVelocity(t, s, http.MethodPut, seeded))

			require.Equal(t, before, storeWriterKeyspace(t, s))
			require.Equal(t, published, storeWriterPublished(bus))
		})
	}
}

func TestVelocityRuleWrite_ExplicitWarnStoresAsBefore(t *testing.T) {
	for _, strict := range []string{"warn", "off"} {
		t.Run(strict, func(t *testing.T) {
			storeWriterEnv(t, true, strict)
			logs := storeWriterLogs(t)
			s, _ := storeWriterVelocityGateway(t)
			payload := velocityRulePayload(storeWriterRuleID)
			rec := storeWriterVelocity(t, s, http.MethodPost, payload)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			created := storeWriterBundle(t, s, velocityRuleBundleID(storeWriterRuleID))
			require.Equal(t, storeWriterVelocityBundle(t, payload, created), created)

			payload["threshold"] = 7
			rec = storeWriterVelocity(t, s, http.MethodPut, payload)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			updated := storeWriterBundle(t, s, velocityRuleBundleID(storeWriterRuleID))
			require.Equal(t, storeWriterVelocityBundle(t, payload, updated), updated)
			require.NotEqual(t, created["content"], updated["content"])
			require.NotEmpty(t, logs.records("WARN", "subject", "velocity rule "+storeWriterRuleID))
		})
	}
}

func TestSignedStoreCheck_KernelRefusesUnsignedVelocityBundle(t *testing.T) {
	storeWriterEnv(t, true, "warn")
	s, _ := storeWriterVelocityGateway(t)
	rec := storeWriterVelocity(t, s, http.MethodPost, velocityRulePayload(storeWriterRuleID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	_, err := storeWriterKernelLoad(t, s)
	require.ErrorIs(t, err, safetykernel.ErrBundleUnsigned)
}

// storeWriterOutputBundle sits outside secops/, so the community-plan kernel
// verifies it rather than skipping it by tier limit.
const storeWriterOutputBundle = "storewriter/output"

// storeWriterSigned is a bundle holding content signed by the configured key,
// or by a throwaway key when none is configured.
func storeWriterSigned(t *testing.T, content string, enabled bool) map[string]any {
	t.Helper()
	key, keyID, err := policysign.LoadPrivateKeyFromEnv()
	if err != nil {
		_, key, err = ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		keyID = storeWriterKeyID
	}
	sig, err := policysign.Sign(key, keyID, []byte(content))
	require.NoError(t, err)
	return map[string]any{"content": content, "enabled": enabled, policyBundleSignatureKey: signatureToMap(sig)}
}

func storeWriterSeedOutput(t *testing.T, s *server) map[string]any {
	t.Helper()
	storeWriterSeed(t, s, map[string]any{storeWriterOutputBundle: storeWriterSigned(t, outputPolicyContent, true)})
	return storeWriterBundle(t, s, storeWriterOutputBundle)
}

func storeWriterToggle(t *testing.T, s *server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/output/rules/out-secret", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	req.SetPathValue("id", "out-secret")
	rec := httptest.NewRecorder()
	s.handlePutPolicyOutputRule(rec, req)
	return rec
}

func storeWriterOutputEnabled(t *testing.T, bundle map[string]any) bool {
	t.Helper()
	content, _ := bundle["content"].(string)
	policy, err := runtimeconfig.ParseSafetyPolicy([]byte(content))
	require.NoError(t, err)
	for _, rule := range policy.OutputRules {
		if rule.ID == "out-secret" {
			return rule.Enabled != nil && *rule.Enabled
		}
	}
	t.Fatalf("out-secret is not in the stored content")
	return false
}

func TestOutputRuleToggle_EnforceResignsRewrittenContent(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	seeded := storeWriterSeedOutput(t, s)
	require.NoError(t, storeWriterVerify(t, seeded))
	require.False(t, storeWriterOutputEnabled(t, seeded))

	rec := storeWriterToggle(t, s)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	stored := storeWriterBundle(t, s, storeWriterOutputBundle)
	require.True(t, storeWriterOutputEnabled(t, stored))
	require.NoError(t, storeWriterVerify(t, stored))
	stale := map[string]any{"content": stored["content"], policyBundleSignatureKey: seeded[policyBundleSignatureKey]}
	require.Error(t, storeWriterVerify(t, stale))
	storeWriterRequireLoads(t, s)
}

func TestOutputRuleToggle_EnforceRefusesWithoutSigningKeyBeforeAnyWrite(t *testing.T) {
	for _, tc := range storeWriterRefusals {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			s, _, _ := newTestGateway(t)
			seeded := storeWriterSeedOutput(t, s)
			before := storeWriterKeyspace(t, s)

			storeWriterRefused(t, storeWriterToggle(t, s))

			require.Equal(t, seeded, storeWriterBundle(t, s, storeWriterOutputBundle))
			require.Equal(t, before, storeWriterKeyspace(t, s))
		})
	}
}

func TestOutputRuleToggle_ExplicitWarnRewritesAsBefore(t *testing.T) {
	for _, strict := range []string{"warn", "off"} {
		t.Run(strict, func(t *testing.T) {
			storeWriterEnv(t, true, strict)
			logs := storeWriterLogs(t)
			s, _, _ := newTestGateway(t)
			seeded := storeWriterSeedOutput(t, s)

			rec := storeWriterToggle(t, s)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			stored := storeWriterBundle(t, s, storeWriterOutputBundle)
			require.True(t, storeWriterOutputEnabled(t, stored))
			want := map[string]any{"content": stored["content"], "updated_at": stored["updated_at"]}
			for key, value := range seeded {
				if key != "content" {
					want[key] = value
				}
			}
			require.Equal(t, want, stored)
			require.NotEmpty(t, logs.records("WARN", "subject", "output rule out-secret"))
		})
	}
}

const storeWriterAllow = "default_decision: allow\n"

// storeWriterSeedLegacy stores bundles in system/default beside sibling data,
// and returns them as read back from the store.
func storeWriterSeedLegacy(t *testing.T, s *server, bundles map[string]any) map[string]any {
	t.Helper()
	require.NoError(t, s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: "default",
		Data: map[string]any{"pools": map[string]any{"topics": map[string]any{}}, packs.PolicyConfigKey: bundles},
	}))
	legacy, ok := storeWriterDefault(t, s)[packs.PolicyConfigKey].(map[string]any)
	require.True(t, ok)
	return legacy
}

func storeWriterDefault(t *testing.T, s *server) map[string]any {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	require.NoError(t, err)
	return doc.Data
}

func storeWriterMigrate(t *testing.T, s *server) (bool, int) {
	t.Helper()
	migrated, count, err := migrateLegacyPolicyBundles(context.Background(), s.configSvc)
	require.NoError(t, err)
	return migrated, count
}

func storeWriterRequireUnverified(t *testing.T, logs *storeWriterLogBuffer, level string, unverified, total int) {
	t.Helper()
	records := logs.records(level, "code", "policy.legacy_migration_unverified")
	require.Len(t, records, 1)
	require.EqualValues(t, unverified, records[0]["unverified"])
	require.EqualValues(t, total, records[0]["legacy_bundles"])
}

func TestLegacyPolicyBundleMigration_EnforceCopiesVerifiedBundlesUnchanged(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	seeded := storeWriterSeedLegacy(t, s, map[string]any{
		"legacy/enabled":  storeWriterSigned(t, outputPolicyContent, true),
		"legacy/disabled": storeWriterSigned(t, storeWriterAllow, false),
	})

	migrated, count := storeWriterMigrate(t, s)
	require.True(t, migrated)
	require.Equal(t, 2, count)
	for id, bundle := range seeded {
		require.Equal(t, bundle, storeWriterBundle(t, s, id))
	}
	require.NotContains(t, storeWriterDefault(t, s), packs.PolicyConfigKey)
	storeWriterRequireLoads(t, s)
}

func TestLegacyPolicyBundleMigration_EnforceLeavesUnverifiedBundlesInPlace(t *testing.T) {
	unsigned := func(enabled bool) map[string]any {
		return map[string]any{"content": storeWriterAllow, "enabled": enabled}
	}
	cases := []struct {
		storeWriterCase
		legacy func(t *testing.T) map[string]any
	}{
		{storeWriterCase{name: "unsigned enabled bundle beside a signed one", production: true}, func(t *testing.T) map[string]any {
			return map[string]any{"legacy/signed": storeWriterSigned(t, outputPolicyContent, true), "legacy/unsigned": unsigned(true)}
		}},
		{storeWriterCase{name: "unsigned disabled bundle", production: true}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/unsigned": unsigned(false)}
		}},
		{storeWriterCase{name: "content changed after signing", production: true}, func(t *testing.T) map[string]any {
			changed := storeWriterSigned(t, storeWriterAllow, true)
			changed["content"] = storeWriterAllow + "default_decision: deny\n"
			return map[string]any{"legacy/changed": changed}
		}},
		{storeWriterCase{name: "raw string bundle", production: true}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/raw": storeWriterAllow}
		}},
		{storeWriterCase{name: "signature fields read as text only", production: true}, func(t *testing.T) map[string]any {
			// The store source reads every _signature field as text only, so a
			// key id of any other type names no trusted key.
			pub, key, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			t.Setenv(policysign.EnvPublicKeyPrefix+"7", base64.StdEncoding.EncodeToString(pub))
			sig, err := policysign.Sign(key, "7", []byte(storeWriterAllow))
			require.NoError(t, err)
			signature := signatureToMap(sig)
			signature["key_id"] = 7
			return map[string]any{"legacy/keyid": map[string]any{"content": storeWriterAllow, "enabled": true, policyBundleSignatureKey: signature}}
		}},
		{storeWriterCase{name: "unsigned text in the policy field", production: true}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/policy": map[string]any{"policy": storeWriterAllow, "enabled": true}}
		}},
		{storeWriterCase{name: "unsigned text in the data field", production: true}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/data": map[string]any{"data": storeWriterAllow, "enabled": true}}
		}},
		{storeWriterCase{name: "unsigned policy text after content that is not text", production: true}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/mixed": map[string]any{"content": 5, "policy": storeWriterAllow, "enabled": true}}
		}},
		{storeWriterCase{name: "development with an unrecognised mode", strict: "bogus"}, func(*testing.T) map[string]any {
			return map[string]any{"legacy/unsigned": unsigned(true)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.trust = "trusted"
			tc.setup(t)
			logs := storeWriterLogs(t)
			s, _, _ := newTestGateway(t)
			legacy := tc.legacy(t)
			storeWriterSeedLegacy(t, s, legacy)
			before := storeWriterKeyspace(t, s)

			migrated, count := storeWriterMigrate(t, s)

			require.False(t, migrated)
			require.Zero(t, count)
			require.Equal(t, before, storeWriterKeyspace(t, s))
			storeWriterRequireUnverified(t, logs, "ERROR", 1, len(legacy))
		})
	}
}

func TestLegacyPolicyBundleMigration_ExplicitWarnMigratesAsBefore(t *testing.T) {
	for _, strict := range []string{"warn", "off"} {
		t.Run(strict, func(t *testing.T) {
			storeWriterEnv(t, true, strict)
			storeWriterKey(t, "trusted")
			logs := storeWriterLogs(t)
			s, _, _ := newTestGateway(t)
			seeded := storeWriterSeedLegacy(t, s, map[string]any{"legacy/unsigned": map[string]any{"content": storeWriterAllow, "enabled": true}})

			migrated, count := storeWriterMigrate(t, s)

			require.True(t, migrated)
			require.Equal(t, 1, count)
			stored := storeWriterBundle(t, s, "legacy/unsigned")
			require.Equal(t, seeded["legacy/unsigned"], stored)
			require.NotContains(t, stored, policyBundleSignatureKey)
			require.NotContains(t, storeWriterDefault(t, s), packs.PolicyConfigKey)
			storeWriterRequireUnverified(t, logs, "WARN", 1, 1)
		})
	}
}
