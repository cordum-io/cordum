package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	envNATSTLSCA         = "NATS_TLS_CA"
	envNATSTLSCert       = "NATS_TLS_CERT"
	envNATSTLSKey        = "NATS_TLS_KEY"
	envNATSTLSInsecure   = "NATS_TLS_INSECURE"
	envNATSTLSServerName = "NATS_TLS_SERVER_NAME"
)

// NATSTLSConfigFromEnv builds a [tls.Config] from NATS_TLS_* environment
// variables. It mirrors the logic in core/infra/bus/nats.go but is a
// standalone implementation so the SDK runtime has no dependency on core/.
//
// Returns (nil, nil) when no TLS variables are set, allowing plain
// connections in environments that don't use TLS.
func NATSTLSConfigFromEnv() (*tls.Config, error) {
	caPath := strings.TrimSpace(os.Getenv(envNATSTLSCA))
	certPath := strings.TrimSpace(os.Getenv(envNATSTLSCert))
	keyPath := strings.TrimSpace(os.Getenv(envNATSTLSKey))
	serverName := strings.TrimSpace(os.Getenv(envNATSTLSServerName))
	insecure := parseBoolEnv(envNATSTLSInsecure)

	// No TLS vars set at all — caller should use a plain connection.
	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		return nil, nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec // min version is set.

	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true //nolint:gosec // operator opt-in for dev/testing.
	}

	if caPath != "" {
		data, err := os.ReadFile(filepath.Clean(caPath))
		if err != nil {
			return nil, fmt.Errorf("nats tls ca read: %w", err)
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(data); !ok {
			return nil, fmt.Errorf("nats tls ca parse: %s", caPath)
		}
		cfg.RootCAs = pool
	}

	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("nats tls cert/key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(filepath.Clean(certPath), filepath.Clean(keyPath))
		if err != nil {
			return nil, fmt.Errorf("nats tls keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

const (
	envRedisTLSCA         = "REDIS_TLS_CA"
	envRedisTLSCert       = "REDIS_TLS_CERT"
	envRedisTLSKey        = "REDIS_TLS_KEY"
	envRedisTLSInsecure   = "REDIS_TLS_INSECURE"
	envRedisTLSServerName = "REDIS_TLS_SERVER_NAME"
)

// RedisTLSConfigFromEnv builds a [tls.Config] from REDIS_TLS_* environment
// variables. Returns (nil, nil) when no TLS variables are set.
func RedisTLSConfigFromEnv() (*tls.Config, error) {
	caPath := strings.TrimSpace(os.Getenv(envRedisTLSCA))
	certPath := strings.TrimSpace(os.Getenv(envRedisTLSCert))
	keyPath := strings.TrimSpace(os.Getenv(envRedisTLSKey))
	serverName := strings.TrimSpace(os.Getenv(envRedisTLSServerName))
	insecure := parseBoolEnv(envRedisTLSInsecure)

	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		return nil, nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec // min version is set.

	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true //nolint:gosec // operator opt-in for dev/testing.
	}

	if caPath != "" {
		data, err := os.ReadFile(filepath.Clean(caPath))
		if err != nil {
			return nil, fmt.Errorf("redis tls ca read: %w", err)
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(data); !ok {
			return nil, fmt.Errorf("redis tls ca parse: %s", caPath)
		}
		cfg.RootCAs = pool
	}

	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("redis tls cert/key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(filepath.Clean(certPath), filepath.Clean(keyPath))
		if err != nil {
			return nil, fmt.Errorf("redis tls keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

func parseBoolEnv(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true")
}
