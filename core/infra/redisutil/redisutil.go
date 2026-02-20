package redisutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/redis/go-redis/v9"
)

const (
	envRedisTLSCA         = "REDIS_TLS_CA"
	envRedisTLSCert       = "REDIS_TLS_CERT"
	envRedisTLSKey        = "REDIS_TLS_KEY"
	envRedisTLSInsecure   = "REDIS_TLS_INSECURE"
	envRedisTLSServerName = "REDIS_TLS_SERVER_NAME"
	envRedisClusterAddrs  = "REDIS_CLUSTER_ADDRESSES"
	envRedisPoolSize      = "REDIS_POOL_SIZE"
	envRedisMinIdleConns  = "REDIS_MIN_IDLE_CONNS"
	defaultPoolSize       = 20
	defaultMinIdleConns   = 5
)

// NewClient creates a Redis universal client with optional TLS and clustering support.
func NewClient(url string) (redis.UniversalClient, error) {
	opts, err := ParseOptions(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis options: %w", err)
	}
	addrs := parseAddrListEnv(envRedisClusterAddrs)
	if len(addrs) == 0 {
		addrs = []string{opts.Addr}
	}
	poolSize := getEnvInt(envRedisPoolSize, defaultPoolSize)
	minIdle := getEnvInt(envRedisMinIdleConns, defaultMinIdleConns)
	uopts := &redis.UniversalOptions{
		Addrs:        addrs,
		Username:     opts.Username,
		Password:     opts.Password,
		DB:           opts.DB,
		TLSConfig:    opts.TLSConfig,
		PoolSize:     poolSize,
		MinIdleConns: minIdle,
	}
	slog.Info("redis pool configured", "pool_size", poolSize, "min_idle", minIdle, "addrs", len(addrs))
	return redis.NewUniversalClient(uopts), nil
}

// ParseOptions parses a Redis URL and applies TLS settings from the environment.
// TLS env vars (REDIS_TLS_CA, etc.) are only applied when the URL uses the
// rediss:// scheme, so plain redis:// connections (e.g. miniredis in tests)
// are not affected by ambient TLS environment variables.
func ParseOptions(rawURL string) (*redis.Options, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if strings.HasPrefix(rawURL, "rediss://") {
		if err := applyTLSFromEnv(opts); err != nil {
			return nil, fmt.Errorf("apply redis tls config: %w", err)
		}
	}
	return opts, nil
}

func applyTLSFromEnv(opts *redis.Options) error {
	if opts == nil {
		return nil
	}
	tlsConfig, err := tlsConfigFromEnv(opts.TLSConfig)
	if err != nil {
		return fmt.Errorf("build tls config: %w", err)
	}
	if tlsConfig != nil {
		opts.TLSConfig = tlsConfig
	}
	return nil
}

func tlsConfigFromEnv(existing *tls.Config) (*tls.Config, error) {
	caPath := strings.TrimSpace(os.Getenv(envRedisTLSCA))
	certPath := strings.TrimSpace(os.Getenv(envRedisTLSCert))
	keyPath := strings.TrimSpace(os.Getenv(envRedisTLSKey))
	serverName := strings.TrimSpace(os.Getenv(envRedisTLSServerName))
	insecure := parseBoolEnv(envRedisTLSInsecure)
	production := env.IsProduction()

	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		if production {
			return nil, fmt.Errorf("redis tls required in production")
		}
		return existing, nil
	}

	if production && insecure {
		return nil, fmt.Errorf("redis tls insecure not allowed in production")
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if existing != nil {
		cfg = existing.Clone()
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true
	}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}

	if caPath != "" {
		// #nosec G304 -- CA path is configured by the operator.
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("redis tls ca read: %w", err)
		}
		pool := cfg.RootCAs
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("redis tls ca parse: %s", caPath)
		}
		cfg.RootCAs = pool
	}

	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("redis tls cert/key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("redis tls keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}

	return cfg, nil
}

func parseBoolEnv(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseAddrListEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if addr := strings.TrimSpace(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func getEnvInt(key string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid int env var, using default", "key", sanitizeLogValue(key), "value", sanitizeLogValue(raw), "default", defaultVal)
		return defaultVal
	}
	return v
}

// sanitizeLogValue truncates and strips control characters from a string before
// logging to prevent log injection via newlines, carriage returns, or ANSI escapes.
func sanitizeLogValue(s string) string {
	const maxLen = 64
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
