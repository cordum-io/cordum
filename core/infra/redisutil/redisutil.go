package redisutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/tlsutil"
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
	envRedisDialTimeout   = "REDIS_DIAL_TIMEOUT"
	envRedisReadTimeout   = "REDIS_READ_TIMEOUT"
	envRedisWriteTimeout  = "REDIS_WRITE_TIMEOUT"
	envRedisIdleTimeout   = "REDIS_IDLE_TIMEOUT"
	envRedisConnMaxLife   = "REDIS_CONN_MAX_LIFETIME"
	envRedisPoolStatsLog  = "REDIS_POOL_STATS_LOG"
	defaultPoolSize       = 20
	defaultMinIdleConns   = 5
	defaultDialTimeout    = 5 * time.Second
	defaultReadTimeout    = 3 * time.Second
	defaultWriteTimeout   = 3 * time.Second
	defaultIdleTimeout    = 5 * time.Minute
	defaultConnMaxLife    = 30 * time.Minute
)

var redisPoolStatsLogInterval = 60 * time.Second

// NewClient creates a Redis universal client with optional TLS and clustering support.
func NewClient(url string) (redis.UniversalClient, error) {
	uopts, err := newUniversalOptions(url)
	if err != nil {
		return nil, err
	}
	slog.Info("redis pool configured",
		"pool_size", uopts.PoolSize,
		"min_idle", uopts.MinIdleConns,
		"addrs", len(uopts.Addrs),
		"dial_timeout", uopts.DialTimeout,
		"read_timeout", uopts.ReadTimeout,
		"write_timeout", uopts.WriteTimeout,
		"conn_max_idle_time", uopts.ConnMaxIdleTime,
		"conn_max_lifetime", uopts.ConnMaxLifetime)
	client := redis.NewUniversalClient(uopts)
	if parseBoolEnv(envRedisPoolStatsLog) {
		startPoolStatsLogger(client)
	}
	return client, nil
}

func startPoolStatsLogger(client redis.UniversalClient) {
	if client == nil {
		return
	}
	interval := redisPoolStatsLogInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			stats := client.PoolStats()
			slog.Debug("redis pool stats",
				"hits", stats.Hits,
				"misses", stats.Misses,
				"timeouts", stats.Timeouts,
				"total_conns", stats.TotalConns,
				"idle_conns", stats.IdleConns,
				"stale_conns", stats.StaleConns)
		}
	}()
}

func newUniversalOptions(url string) (*redis.UniversalOptions, error) {
	opts, err := ParseOptions(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis options: %w", err)
	}
	addrs := parseAddrListEnv(envRedisClusterAddrs)
	if len(addrs) == 0 {
		addrs = []string{opts.Addr}
	}
	poolSize := getEnvIntAtLeast(envRedisPoolSize, defaultPoolSize, 1)
	minIdle := getEnvIntAtLeast(envRedisMinIdleConns, defaultMinIdleConns, 0)
	return &redis.UniversalOptions{
		Addrs:           addrs,
		Username:        opts.Username,
		Password:        opts.Password,
		DB:              opts.DB,
		TLSConfig:       opts.TLSConfig,
		PoolSize:        poolSize,
		MinIdleConns:    minIdle,
		DialTimeout:     durationFromEnv(envRedisDialTimeout, defaultDialTimeout),
		ReadTimeout:     durationFromEnv(envRedisReadTimeout, defaultReadTimeout),
		WriteTimeout:    durationFromEnv(envRedisWriteTimeout, defaultWriteTimeout),
		ConnMaxIdleTime: durationFromEnv(envRedisIdleTimeout, defaultIdleTimeout),
		ConnMaxLifetime: durationFromEnv(envRedisConnMaxLife, defaultConnMaxLife),
	}, nil
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

// firstLeafFromTLSCert parses the leaf of a tls.Certificate for metric
// emission. Nil on failure — emission is observability-only, so a nil return
// just means the gauge doesn't get populated this run; it doesn't fail the
// connection.
func firstLeafFromTLSCert(cert tls.Certificate) *x509.Certificate {
	if len(cert.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	return leaf
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
		// Chain-verify so CA-rotated-without-client drift surfaces with a
		// rich error (issuer DNs, validity windows, remediation) instead of
		// bubbling up as an opaque "certificate required" handshake failure.
		if caPath != "" && !insecure {
			if verr := tlsutil.VerifyChain(certPath, caPath, tlsutil.RoleClient); verr != nil {
				if leaf := firstLeafFromTLSCert(cert); leaf != nil {
					tlsutil.EmitCertMetrics("redis", "client", certPath, leaf.NotAfter, false)
				}
				return nil, fmt.Errorf("redis tls: %w", verr)
			}
		}
		// Emit the success gauge. chainValid MUST reflect whether chain
		// verification actually ran — when caPath is empty or insecure
		// is set, VerifyChain was skipped, so we cannot claim chain
		// validity and must emit false to avoid false-positive TLS
		// health signals in cordum_cert_chain_valid.
		chainValid := caPath != "" && !insecure
		if leaf := firstLeafFromTLSCert(cert); leaf != nil {
			tlsutil.EmitCertMetrics("redis", "client", certPath, leaf.NotAfter, chainValid)
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

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
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

func getEnvIntAtLeast(key string, defaultVal int, minVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minVal {
		slog.Warn(
			"invalid int env var, using default",
			"key",
			sanitizeLogValue(key),
			"value",
			sanitizeLogValue(raw),
			"default",
			defaultVal,
			"min_allowed",
			minVal,
		)
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
