package redisutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	envRedisTLSCA         = "REDIS_TLS_CA"
	envRedisTLSCert       = "REDIS_TLS_CERT"
	envRedisTLSKey        = "REDIS_TLS_KEY"
	envRedisTLSInsecure   = "REDIS_TLS_INSECURE"
	envRedisTLSServerName = "REDIS_TLS_SERVER_NAME"
	envRedisClusterAddrs  = "REDIS_CLUSTER_ADDRESSES"
)

// NewClient creates a Redis universal client with optional TLS and clustering support.
func NewClient(url string) (redis.UniversalClient, error) {
	opts, err := ParseOptions(url)
	if err != nil {
		return nil, err
	}
	addrs := parseAddrListEnv(envRedisClusterAddrs)
	if len(addrs) == 0 {
		addrs = []string{opts.Addr}
	}
	uopts := &redis.UniversalOptions{
		Addrs:     addrs,
		Username:  opts.Username,
		Password:  opts.Password,
		DB:        opts.DB,
		TLSConfig: opts.TLSConfig,
	}
	return redis.NewUniversalClient(uopts), nil
}

// ParseOptions parses a Redis URL and applies TLS settings from the environment.
func ParseOptions(url string) (*redis.Options, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	if err := applyTLSFromEnv(opts); err != nil {
		return nil, err
	}
	return opts, nil
}

func applyTLSFromEnv(opts *redis.Options) error {
	if opts == nil {
		return nil
	}
	tlsConfig, err := tlsConfigFromEnv(opts.TLSConfig)
	if err != nil {
		return err
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

	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		return existing, nil
	}

	cfg := &tls.Config{}
	if existing != nil {
		*cfg = *existing
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true
	}

	if caPath != "" {
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
