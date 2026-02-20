package redisutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAddrListEnv(t *testing.T) {
	t.Setenv(envRedisClusterAddrs, "redis1:6379, redis2:6379 redis3:6379")
	addrs := parseAddrListEnv(envRedisClusterAddrs)
	if len(addrs) != 3 {
		t.Fatalf("expected 3 addrs, got %d", len(addrs))
	}
	if addrs[0] != "redis1:6379" || addrs[2] != "redis3:6379" {
		t.Fatalf("unexpected addr list: %v", addrs)
	}
}

func TestParseBoolEnv(t *testing.T) {
	t.Setenv(envRedisTLSInsecure, "true")
	if !parseBoolEnv(envRedisTLSInsecure) {
		t.Fatalf("expected true")
	}
	t.Setenv(envRedisTLSInsecure, "no")
	if parseBoolEnv(envRedisTLSInsecure) {
		t.Fatalf("expected false")
	}
}

func TestGetEnvIntDefault(t *testing.T) {
	// No env var set — should return default.
	t.Setenv(envRedisPoolSize, "")
	assert.Equal(t, 20, getEnvInt(envRedisPoolSize, 20))
}

func TestGetEnvIntCustomValue(t *testing.T) {
	t.Setenv(envRedisPoolSize, "50")
	assert.Equal(t, 50, getEnvInt(envRedisPoolSize, 20))
}

func TestGetEnvIntBadValue(t *testing.T) {
	t.Setenv(envRedisPoolSize, "abc")
	assert.Equal(t, 20, getEnvInt(envRedisPoolSize, 20))
}

func TestGetEnvIntZeroFallsBack(t *testing.T) {
	t.Setenv(envRedisPoolSize, "0")
	assert.Equal(t, 20, getEnvInt(envRedisPoolSize, 20))
}

func TestGetEnvIntNegativeFallsBack(t *testing.T) {
	t.Setenv(envRedisPoolSize, "-5")
	assert.Equal(t, 20, getEnvInt(envRedisPoolSize, 20))
}

func TestRedisMinIdleFromEnv(t *testing.T) {
	t.Setenv(envRedisMinIdleConns, "10")
	assert.Equal(t, 10, getEnvInt(envRedisMinIdleConns, 5))
}

func TestTLSConfigFromEnvErrors(t *testing.T) {
	t.Setenv("REDIS_TLS_CA", "")
	t.Setenv("REDIS_TLS_CERT", "")
	t.Setenv("REDIS_TLS_KEY", "")
	t.Setenv("REDIS_TLS_INSECURE", "")
	t.Setenv("REDIS_TLS_SERVER_NAME", "")

	t.Setenv("CORDUM_ENV", "production")
	if _, err := tlsConfigFromEnv(nil); err == nil {
		t.Fatalf("expected tls required error")
	}

	t.Setenv("CORDUM_ENV", "")
	t.Setenv(envRedisTLSCert, "/tmp/cert.pem")
	t.Setenv(envRedisTLSKey, "")
	if _, err := tlsConfigFromEnv(nil); err == nil {
		t.Fatalf("expected cert/key mismatch error")
	}
}
