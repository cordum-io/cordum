package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/safetykernel"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// The kernel process reads its Redis, listen address and policy path here.
const signedE2EKernelRedisEnv = "SIGNED_E2E_KERNEL_REDIS_URL"
const signedE2EKernelAddrEnv = "SIGNED_E2E_KERNEL_ADDR"
const signedE2EKernelPolicyEnv = "SIGNED_E2E_KERNEL_POLICY"

const signedE2EReloadInterval = 100 * time.Millisecond
const signedE2EProbeTopic = "job.matrix-pack.collect"
const signedE2ESnapshotsKey = "cordum:safety:snapshots"

// signedE2EBasePolicy denies the probe topic for tenant default by default.
const signedE2EBasePolicy = "default_decision: deny\ntenants:\n  default:\n    allow_topics:\n      - job.signed-e2e.base\n"

var signedE2ESources = []string{"upload", "marketplace", "cordumctl"}

// signedE2EKernelBinary is this test binary, which each test starts again as
// its safety kernel process: a kernel in this process could not be stopped
// and would keep reloading after its test. In the kernel process the
// initializer runs the kernel and never returns. Package initialization
// precedes TestMain, so the kernel keeps exactly the environment its test set.
var signedE2EKernelBinary = signedE2ERunKernelProcess()

func signedE2ERunKernelProcess() string {
	redisURL := os.Getenv(signedE2EKernelRedisEnv)
	if redisURL == "" {
		binary, err := os.Executable()
		if err != nil {
			return ""
		}
		return binary
	}
	go func() {
		// The test holds stdin open for as long as it needs the kernel.
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()
	err := safetykernel.RunWithEntitlements(&runtimeconfig.Config{
		NatsURL:          "nats://127.0.0.1:1",
		RedisURL:         redisURL,
		SafetyKernelAddr: os.Getenv(signedE2EKernelAddrEnv),
		SafetyPolicyPath: os.Getenv(signedE2EKernelPolicyEnv),
	}, nil)
	_, _ = fmt.Fprintln(os.Stderr, "safety kernel exited:", err)
	os.Exit(1)
	return ""
}

// signedE2EHarness is a production-profile gateway and the real safety
// kernel, sharing one Redis.
type signedE2EHarness struct {
	s       *server
	packKey ed25519.PrivateKey
	kernel  pb.SafetyKernelClient
	logPath string
}

// signedE2EIdentity is the kernel's active policy as the tests observe it:
// its snapshot history in Redis and its answer to the probe.
type signedE2EIdentity struct {
	length   int64
	head     string
	decision pb.DecisionType
	snapshot string
}

// signedE2EStack starts the gateway in the production profile, applies seed
// to its store, then starts the kernel on the same Redis with TLS, a signed
// base policy and a 100ms reload.
func signedE2EStack(t *testing.T, seed func(*server)) *signedE2EHarness {
	t.Helper()
	h := &signedE2EHarness{packKey: setPackSignatureProfile(t, "production")}
	h.s, _, _ = newTestGateway(t)
	require.NoError(t, h.s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope: "system", ScopeID: "default", Data: map[string]any{"pools": map[string]any{}},
	}))
	if seed != nil {
		seed(h.s)
	}
	dir := t.TempDir()
	pool := signedE2ETLS(t, dir)
	policyPath := signedE2EPolicy(t, dir)
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", signedE2EReloadInterval.String())
	t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", reserveLocalAddr(t))
	addr := reserveLocalAddr(t)
	h.start(t, dir, addr, policyPath)
	h.dial(t, addr, pool)
	return h
}

// start runs the kernel process and stops it when the test ends.
func (h *signedE2EHarness) start(t *testing.T, dir, addr, policyPath string) {
	t.Helper()
	require.NotEmpty(t, signedE2EKernelBinary, "test binary path")
	opts := h.s.redisClient().(*redis.Client).Options()
	h.logPath = filepath.Join(dir, "kernel.log")
	logFile, err := os.Create(h.logPath)
	require.NoError(t, err)
	cmd := exec.Command(signedE2EKernelBinary)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=redis://%s/%d", signedE2EKernelRedisEnv, opts.Addr, opts.DB),
		signedE2EKernelAddrEnv+"="+addr, signedE2EKernelPolicyEnv+"="+policyPath)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// The kernel exits when this pipe closes, so it never outlives the test binary.
	_, err = cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("kernel output:\n%s", h.output())
		}
		_ = logFile.Close()
	})
}

// dial connects over TLS and waits up to 10s for the kernel's first answer.
func (h *signedE2EHarness) dial(t *testing.T, addr string, pool *x509.CertPool) {
	t.Helper()
	creds := credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	h.kernel = pb.NewSafetyKernelClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = h.kernel.Check(ctx, signedE2EProbe(), grpc.WaitForReady(true))
	require.NoError(t, err, "kernel did not answer")
}

func (h *signedE2EHarness) output() string {
	raw, err := os.ReadFile(h.logPath)
	if err != nil {
		return "kernel log unreadable: " + err.Error()
	}
	return string(raw[max(0, len(raw)-8192):])
}

func (h *signedE2EHarness) identity(t *testing.T) signedE2EIdentity {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	length, err := h.s.redisClient().LLen(ctx, signedE2ESnapshotsKey).Result()
	require.NoError(t, err)
	head, err := h.s.redisClient().LIndex(ctx, signedE2ESnapshotsKey, 0).Result()
	require.NoError(t, err)
	resp, err := h.kernel.Check(ctx, signedE2EProbe())
	require.NoError(t, err)
	return signedE2EIdentity{length: length, head: head, decision: resp.GetDecision(), snapshot: resp.GetPolicySnapshot()}
}

// waitFor polls the kernel until done holds, failing after 10s.
func (h *signedE2EHarness) waitFor(t *testing.T, what string, done func(signedE2EIdentity) bool) signedE2EIdentity {
	t.Helper()
	ticker := time.NewTicker(signedE2EReloadInterval / 2)
	defer ticker.Stop()
	for deadline := time.Now().Add(10 * time.Second); ; <-ticker.C {
		got := h.identity(t)
		if done(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("kernel did not reach %s within 10s; last %+v", what, got)
		}
	}
}

// requireSteady asserts the kernel shows want for six reload intervals.
func (h *signedE2EHarness) requireSteady(t *testing.T, want signedE2EIdentity) {
	t.Helper()
	ticker := time.NewTicker(signedE2EReloadInterval / 2)
	defer ticker.Stop()
	for end := time.Now().Add(6 * signedE2EReloadInterval); time.Now().Before(end); <-ticker.C {
		require.Equal(t, want, h.identity(t))
	}
}

// requireNewSnapshot waits for the kernel to record a snapshot after before
// and for the probe to answer decision from it.
func (h *signedE2EHarness) requireNewSnapshot(t *testing.T, before signedE2EIdentity, decision pb.DecisionType) {
	t.Helper()
	h.waitFor(t, "a new snapshot answering "+decision.String(), func(id signedE2EIdentity) bool {
		return id.length > before.length && id.head != before.head && id.snapshot == id.head && id.decision == decision
	})
}

func signedE2EProbe() *pb.PolicyCheckRequest {
	return &pb.PolicyCheckRequest{Topic: signedE2EProbeTopic, Tenant: "default"}
}

// signedE2ETLS writes a self-signed kernel certificate, points the kernel at
// it, and returns a pool trusting it.
func signedE2ETLS(t *testing.T, dir string) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "signed-e2e-kernel"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	certPath, keyPath := filepath.Join(dir, "kernel.crt"), filepath.Join(dir, "kernel.key")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	t.Setenv("SAFETY_KERNEL_TLS_CERT", certPath)
	t.Setenv("SAFETY_KERNEL_TLS_KEY", keyPath)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// signedE2EPolicy writes the base policy with the signature sidecar the
// kernel's file source verifies, and returns its path.
func signedE2EPolicy(t *testing.T, dir string) string {
	t.Helper()
	key, keyID, err := policysign.LoadPrivateKeyFromEnv()
	require.NoError(t, err)
	sig, err := policysign.Sign(key, keyID, []byte(signedE2EBasePolicy))
	require.NoError(t, err)
	raw, err := json.Marshal(sig)
	require.NoError(t, err)
	path := filepath.Join(dir, "safety.yaml")
	require.NoError(t, os.WriteFile(path, []byte(signedE2EBasePolicy), 0o600))
	require.NoError(t, os.WriteFile(path+".sig", raw, 0o600))
	return path
}

// signedE2EInstall prepares source to install bundle and returns the install.
func signedE2EInstall(t *testing.T, s *server, source string, bundle packSignatureBundle) func() *httptest.ResponseRecorder {
	t.Helper()
	switch source {
	case "upload":
		return func() *httptest.ResponseRecorder { return uploadPackSignatureBundle(t, s, bundle) }
	case "marketplace":
		return preparePackMarketplace(t, s, bundle)
	case "cordumctl":
		return func() *httptest.ResponseRecorder { return installPackLikeCordumctl(t, s, bundle) }
	}
	t.Fatalf("unknown source %q", source)
	return nil
}

func TestSignedContentEndToEnd_SignedPackFragmentActivatesInKernel(t *testing.T) {
	for _, source := range signedE2ESources {
		t.Run(source, func(t *testing.T) {
			h := signedE2EStack(t, nil)
			install := signedE2EInstall(t, h.s, source, buildPackSignatureBundle(t, "signed", h.packKey))
			before := h.identity(t)
			require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, before.decision)
			resp := install()
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			assertPackFragmentSignatureVerifies(t, h.s, matrixPackID, "safety")
			h.requireNewSnapshot(t, before, pb.DecisionType_DECISION_TYPE_ALLOW)
		})
	}
}

func TestSignedContentEndToEnd_UnsignedPackLeavesKernelPolicyUnchanged(t *testing.T) {
	for _, source := range signedE2ESources {
		t.Run(source, func(t *testing.T) {
			h := signedE2EStack(t, nil)
			bundle := buildPackSignatureBundle(t, "unsigned", h.packKey)
			install := signedE2EInstall(t, h.s, source, bundle)
			before := h.identity(t)
			require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, before.decision)
			resp := install()
			require.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
			var body struct{ Code, Error string }
			require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
			require.Equal(t, "PACK_INSTALL_INVALID", body.Code)
			require.True(t, strings.HasPrefix(body.Error, "pack.unsigned: "), body.Error)
			assertPackAbsent(t, h.s)
			h.requireSteady(t, before)
			// The kernel kept reloading throughout: the same fragment, signed, activates.
			storeWriterSeed(t, h.s, map[string]any{"signed-e2e/control": storeWriterSigned(t, bundle.content, true)})
			h.requireNewSnapshot(t, before, pb.DecisionType_DECISION_TYPE_ALLOW)
		})
	}
}

func TestSignedContentEndToEnd_StoreWritersKeepStoreLoadable(t *testing.T) {
	t.Run("velocity-rule-create", func(t *testing.T) {
		h := signedE2EStack(t, nil)
		setTestEntitlements(t, h.s, licensing.PlanTeam, func(e *licensing.Entitlements) { e.VelocityRules = true })
		before := h.identity(t)
		rec := storeWriterVelocity(t, h.s, http.MethodPost, velocityRulePayload("signed-e2e-burst"))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		h.requireNewSnapshot(t, before, pb.DecisionType_DECISION_TYPE_DENY)
	})
	t.Run("output-rule-toggle", func(t *testing.T) {
		h := signedE2EStack(t, func(s *server) {
			storeWriterSeed(t, s, map[string]any{"signed-e2e/output": storeWriterSigned(t, outputPolicyContent, true)})
		})
		before := h.identity(t)
		rec := storeWriterToggle(t, h.s)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		h.requireNewSnapshot(t, before, pb.DecisionType_DECISION_TYPE_DENY)
	})
}
