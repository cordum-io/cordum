package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/packs/signing"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func prodPackEnv(t *testing.T, production bool) ed25519.PrivateKey {
	t.Helper()
	resetGatewayPackStrictCache()
	t.Cleanup(resetGatewayPackStrictCache)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, gatewayPackTrustedKeyEnvPrefix) || strings.HasPrefix(name, gatewayPackPublisherEnvPrefix) {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		} else if strings.HasPrefix(name, "CORDUM_POLICY_PUBLIC_KEY_") {
			t.Setenv(name, "")
		}
	}
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", gatewayPackStrictEnv, "CORDUM_POLICY_STRICT", "SAFETY_POLICY_SIGNATURE_REQUIRED", "CORDUM_POLICY_SIGNING_KEY", "CORDUM_POLICY_SIGNING_KEY_PATH", "CORDUM_POLICY_SIGNING_KEY_ID", "CORDUM_POLICY_DEV_SIGNING_SEED", "SAFETY_POLICY_PUBLIC_KEY", "SAFETY_POLICY_PUBLIC_KEY_ID", "CORDUM_MARKETPLACE_ALLOW_HTTP"} {
		t.Setenv(name, "")
	}
	if production {
		t.Setenv("CORDUM_ENV", "production")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv(gatewayPackTrustedKeyEnvPrefix+"MATRIX_PUB", base64.StdEncoding.EncodeToString(pub))
	return key
}

func prodPackPolicyKey(t *testing.T, trusted bool) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv("CORDUM_POLICY_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("CORDUM_POLICY_SIGNING_KEY_ID", "prod-policy")
	if trusted {
		t.Setenv("CORDUM_POLICY_PUBLIC_KEY_PROD-POLICY", base64.StdEncoding.EncodeToString(pub))
	}
}

type prodPackHarness struct {
	s      *server
	bundle packSignatureBundle
	drive  func() *httptest.ResponseRecorder
	before map[string]string
}

// Reuse the published matrix's complete pack fixture and per-DB SCAN oracle.
// Marketplace setup primes the catalog before taking the state snapshot.
func prodPackPrepare(t *testing.T, key ed25519.PrivateKey, state, source string) prodPackHarness {
	t.Helper()
	s, _, _ := newTestGateway(t)
	require.NoError(t, s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope: "system", ScopeID: "default", Data: map[string]any{"pools": map[string]any{}},
	}))
	bundle := buildPackSignatureBundle(t, state, key)
	drive := func() *httptest.ResponseRecorder { return uploadPackSignatureBundle(t, s, bundle) }
	if source == "marketplace" {
		drive = preparePackMarketplace(t, s, bundle)
	}
	return prodPackHarness{s, bundle, drive, snapshotPackKeyspace(t, s)}
}

func prodPackRefused(t *testing.T, h prodPackHarness, prefix string) {
	t.Helper()
	assertPackRefused(t, h.s, h.before, h.drive(), packMatrixRefusal("PACK_INSTALL_INVALID", prefix))
	after := snapshotPackKeyspace(t, h.s)
	require.Equal(t, h.before["cfg:system:workers"], after["cfg:system:workers"])
}

func TestPackInstallProductionProfile_RefusesUnsignedUploadWithoutWrites(t *testing.T) {
	key := prodPackEnv(t, true)
	prodPackPolicyKey(t, true)
	prodPackRefused(t, prodPackPrepare(t, key, "unsigned", "upload"), "pack.unsigned: ")
}

func TestPackInstallProductionProfile_RefusesUnsignedMarketplaceInstallWithoutWrites(t *testing.T) {
	key := prodPackEnv(t, true)
	prodPackPolicyKey(t, true)
	prodPackRefused(t, prodPackPrepare(t, key, "unsigned", "marketplace"), "pack.unsigned: ")
}

func prodPackRedis(t *testing.T, raw string) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	if raw != "" {
		require.NoError(t, mr.Set(gatewayStrictRedisKey, raw))
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	if raw == "closed" {
		mr.Close()
	}
	return client
}

func prodPackResolution(t *testing.T, client redis.UniversalClient, strict, downgraded bool, source string) {
	t.Helper()
	got := resolveGatewayPackStrictMode(context.Background(), client)
	require.Equal(t, gatewayPackStrictResolution{strict, downgraded, source}, got)
	require.Equal(t, strict, resolveGatewayPackStrict(context.Background(), client))
}

func TestGatewayPackStrictResolution_ProductionDefaultsToStrict(t *testing.T) {
	for _, setting := range []string{"profile", "flag", "invalid-env", "invalid-redis", "closed", "nil"} {
		t.Run(setting, func(t *testing.T) {
			prodPackEnv(t, true)
			raw := ""
			if setting == "flag" {
				t.Setenv("CORDUM_ENV", "")
				t.Setenv("CORDUM_PRODUCTION", "true")
			} else if setting == "invalid-env" {
				t.Setenv(gatewayPackStrictEnv, "invalid")
			} else if setting == "invalid-redis" || setting == "closed" {
				raw = setting
			}
			var client redis.UniversalClient
			if setting != "nil" {
				client = prodPackRedis(t, raw)
			}
			prodPackResolution(t, client, true, false, "production-default")
		})
	}
}

func TestGatewayPackStrictResolution_ExplicitFalseDowngradesProduction(t *testing.T) {
	for _, raw := range []string{"false", "0", "off", " No ", "redis"} {
		t.Run(raw, func(t *testing.T) {
			prodPackEnv(t, true)
			var client redis.UniversalClient
			source := gatewayPackStrictEnv
			if raw == "redis" {
				client, source = prodPackRedis(t, "false"), gatewayStrictRedisKey
			} else {
				t.Setenv(gatewayPackStrictEnv, raw)
			}
			prodPackResolution(t, client, false, true, source)
		})
	}
}

func TestGatewayPackStrictResolution_ExplicitTrueWins(t *testing.T) {
	for _, tc := range []struct {
		production         bool
		env, redis, source string
	}{
		{true, "true", "false", gatewayPackStrictEnv}, {true, "false", "true", gatewayStrictRedisKey},
		{false, "", "true", gatewayStrictRedisKey}, {true, " 1 ", "false", gatewayPackStrictEnv},
		{true, "YES", "false", gatewayPackStrictEnv}, {true, "on", "false", gatewayPackStrictEnv},
	} {
		t.Run(tc.env+tc.redis, func(t *testing.T) {
			prodPackEnv(t, tc.production)
			t.Setenv(gatewayPackStrictEnv, tc.env)
			prodPackResolution(t, prodPackRedis(t, tc.redis), true, false, tc.source)
		})
	}
}

func TestGatewayPackStrictResolution_DevelopmentDefaultUnchanged(t *testing.T) {
	prodPackEnv(t, false)
	client := prodPackRedis(t, "")
	prodPackResolution(t, client, false, false, "development-default")
	t.Setenv(gatewayPackStrictEnv, "false")
	prodPackResolution(t, client, false, false, gatewayPackStrictEnv)
	// The cache must not retain the environment or deployment profile.
	t.Setenv("CORDUM_ENV", "production")
	prodPackResolution(t, client, false, true, gatewayPackStrictEnv)
	t.Setenv(gatewayPackStrictEnv, "")
	prodPackResolution(t, client, true, false, "production-default")
}

func prodPackLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestGatewayPackStrictResolution_DowngradeIsLogged(t *testing.T) {
	for _, source := range []string{gatewayPackStrictEnv, gatewayStrictRedisKey, "production", "development"} {
		t.Run(source, func(t *testing.T) {
			key := prodPackEnv(t, source != "development")
			client := prodPackRedis(t, "")
			downgraded := source == gatewayPackStrictEnv || source == gatewayStrictRedisKey
			if source == gatewayPackStrictEnv {
				t.Setenv(gatewayPackStrictEnv, "false")
			} else if source == gatewayStrictRedisKey {
				require.NoError(t, client.Set(context.Background(), gatewayStrictRedisKey, "false", 0).Err())
			}
			logs := prodPackLogs(t)
			logGatewayPackStrictPosture(context.Background(), client)
			require.Equal(t, downgraded, strings.Contains(logs.String(), "level=WARN"))
			if downgraded {
				require.Contains(t, logs.String(), source)
			}
			bundle := buildPackSignatureBundle(t, "unsigned", key)
			for range 2 {
				logs.Reset()
				_, err := verifyPackInstallBundle(context.Background(), client, bundle.dir, matrixPackID)
				if source == "production" {
					require.NotNil(t, err)
				} else {
					require.Nil(t, err)
				}
				require.Equal(t, downgraded, strings.Contains(logs.String(), "level=WARN"))
				if downgraded {
					require.Contains(t, logs.String(), "pack_id="+matrixPackID)
					require.Contains(t, logs.String(), "override="+source)
				}
			}
		})
	}
}

func TestPackInstallProductionProfile_ExplicitDowngradeInstallsUnsigned(t *testing.T) {
	for _, source := range []string{"env", "redis"} {
		t.Run(source, func(t *testing.T) {
			key := prodPackEnv(t, true)
			prodPackPolicyKey(t, true)
			h := prodPackPrepare(t, key, "unsigned", "upload")
			if source == "env" {
				t.Setenv(gatewayPackStrictEnv, "false")
			} else {
				require.NoError(t, h.s.redisClient().Set(context.Background(), gatewayStrictRedisKey, "false", 0).Err())
			}
			assertPackAccepted(t, h.s, h.before, h.drive(), packSignatureWant{status: 200}, h.bundle.content, "upload")
		})
	}
}

func TestPackInstallDevelopmentProfile_InstallsUnsignedAsBefore(t *testing.T) {
	for _, source := range []string{"upload", "marketplace"} {
		t.Run(source, func(t *testing.T) {
			key := prodPackEnv(t, false)
			h := prodPackPrepare(t, key, "unsigned", source)
			assertPackAccepted(t, h.s, h.before, h.drive(), packSignatureWant{status: 200}, h.bundle.content, source)
			records, _, err := h.s.loadPackRegistry(context.Background())
			require.NoError(t, err)
			require.Equal(t, []string{"pack accepted unsigned — gateway strict mode disabled"}, records[matrixPackID].Verification.Warnings)
			require.NotContains(t, packMatrixFragment(t, h.s), "_signature")
		})
	}
}

func TestPackPolicyFragment_StoredSignatureVerifiesUnderTrustStore(t *testing.T) {
	for _, profile := range []bool{true, false} {
		for _, source := range []string{"upload", "marketplace"} {
			t.Run(source+map[bool]string{true: "-production", false: "-development"}[profile], func(t *testing.T) {
				key := prodPackEnv(t, profile)
				prodPackPolicyKey(t, true)
				h := prodPackPrepare(t, key, "signed", source)
				assertPackAccepted(t, h.s, h.before, h.drive(), packSignatureWant{status: 200, signed: true}, h.bundle.content, source)
				fragment := packMatrixFragment(t, h.s)
				var sig policysign.Signature
				require.NoError(t, json.Unmarshal(fragment["_signature"], &sig))
				require.Equal(t, "prod-policy", sig.KeyID)
				var content string
				require.NoError(t, json.Unmarshal(fragment["content"], &content))
				trust, err := policysign.LoadTrustStoreFromEnv()
				require.NoError(t, err)
				pub, found := trust.Lookup(sig.KeyID)
				require.True(t, found)
				require.NoError(t, policysign.Verify(pub, []byte(content), sig))
				require.Error(t, policysign.Verify(pub, []byte(content+"x"), sig))
				signer, err := resolvePolicyStoreSigner("test pack")
				require.NoError(t, err)
				empty, err := signer.sign(nil)
				require.ErrorIs(t, err, policysign.ErrEmptyPayload)
				require.Nil(t, empty)
			})
		}
	}
}

func TestPackPolicyFragment_EnforceRefusesWithoutSigningKeyBeforeAnyWrite(t *testing.T) {
	for _, source := range []string{"upload", "marketplace"} {
		t.Run(source, func(t *testing.T) {
			key := prodPackEnv(t, true)
			prodPackRefused(t, prodPackPrepare(t, key, "signed", source), "pack.policy_unsigned: ")
		})
	}
}

func TestPackPolicyFragment_EnforceRefusesUntrustedSigningKey(t *testing.T) {
	for _, reason := range []string{"absent", "other", "malformed-trust", "malformed-key", "inconsistent-key"} {
		t.Run(reason, func(t *testing.T) {
			key := prodPackEnv(t, true)
			prodPackPolicyKey(t, false)
			switch reason {
			case "other":
				t.Setenv("CORDUM_POLICY_PUBLIC_KEY_PROD-POLICY", base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
			case "malformed-trust":
				t.Setenv("CORDUM_POLICY_PUBLIC_KEY_PROD-POLICY", "not-a-key")
			case "malformed-key":
				t.Setenv("CORDUM_POLICY_SIGNING_KEY", "not-a-key")
			case "inconsistent-key":
				prodPackPolicyKey(t, true)
				raw, err := base64.StdEncoding.DecodeString(os.Getenv("CORDUM_POLICY_SIGNING_KEY"))
				require.NoError(t, err)
				raw[0] ^= 1
				t.Setenv("CORDUM_POLICY_SIGNING_KEY", base64.StdEncoding.EncodeToString(raw))
			}
			prodPackRefused(t, prodPackPrepare(t, key, "signed", "upload"), "pack.policy_unsigned: ")
		})
	}
}

func TestPackPolicyFragment_UndeterminedModeTreatedAsEnforce(t *testing.T) {
	for _, profile := range []bool{true, false} {
		t.Run(map[bool]string{true: "production", false: "development"}[profile], func(t *testing.T) {
			key := prodPackEnv(t, profile)
			t.Setenv("CORDUM_POLICY_STRICT", "bogus")
			prodPackRefused(t, prodPackPrepare(t, key, "signed", "upload"), "pack.policy_unsigned: ")
		})
	}
}

func TestPackPolicyFragment_ExplicitWarnStoresUnsignedFragment(t *testing.T) {
	for _, mode := range []string{"warn", "off"} {
		t.Run(mode, func(t *testing.T) {
			key := prodPackEnv(t, true)
			t.Setenv("CORDUM_POLICY_STRICT", mode)
			h := prodPackPrepare(t, key, "signed", "upload")
			logs := prodPackLogs(t)
			assertPackAccepted(t, h.s, h.before, h.drive(), packSignatureWant{status: 200, signed: true}, h.bundle.content, "upload")
			require.NotContains(t, packMatrixFragment(t, h.s), "_signature")
			require.Equal(t, 1, strings.Count(logs.String(), "level=WARN"))
			require.Contains(t, logs.String(), "policy_mode="+mode)
			require.Contains(t, logs.String(), matrixPackID)
		})
	}
}

func TestPackPolicyFragment_PackWithoutPolicyOverlayNeedsNoSigningKey(t *testing.T) {
	key := prodPackEnv(t, true)
	h := prodPackPrepare(t, key, "signed", "upload")
	files := packMatrixFiles()
	files["pack.yaml"] = strings.Split(files["pack.yaml"], "  policy:\n")[0]
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(h.bundle.dir, filepath.FromSlash(name)), []byte(content), 0600))
	}
	manifest, err := signing.BuildManifest(h.bundle.dir)
	require.NoError(t, err)
	envelope, err := signing.SignManifest(manifest, key, "matrix-pub")
	require.NoError(t, err)
	raw, err := signing.EncodeEnvelopeYAML(envelope)
	require.NoError(t, err)
	files["pack.yaml.sig"] = string(raw)
	h.bundle.tgz = buildTarGz(t, files)
	response := uploadPackSignatureBundle(t, h.s, h.bundle)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Nil(t, packMatrixFragment(t, h.s))
}
