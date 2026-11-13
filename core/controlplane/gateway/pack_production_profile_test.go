package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
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
