package redisutil

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"log/slog"
)

// syncBuffer is a thread-safe bytes.Buffer for capturing log output in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *syncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *syncBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func TestParseOptionsNoTLS(t *testing.T) {
	opts, err := ParseOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig != nil {
		t.Fatalf("expected nil TLS config")
	}
}

func TestParseOptionsInsecureTLS(t *testing.T) {
	t.Setenv(envRedisTLSInsecure, "true")
	opts, err := ParseOptions("rediss://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig == nil || !opts.TLSConfig.InsecureSkipVerify {
		t.Fatalf("expected insecure TLS config")
	}
}

func TestParseOptionsPlainURLIgnoresTLSEnv(t *testing.T) {
	// TLS env vars should NOT affect plain redis:// connections (e.g. miniredis).
	t.Setenv(envRedisTLSInsecure, "true")
	opts, err := ParseOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig != nil {
		t.Fatalf("expected nil TLS config for plain redis:// URL")
	}
}

func TestParseOptionsTLSCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	t.Setenv(envRedisTLSCA, certPath)
	t.Setenv(envRedisTLSCert, certPath)
	t.Setenv(envRedisTLSKey, keyPath)

	opts, err := ParseOptions("rediss://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.RootCAs == nil {
		t.Fatalf("expected root CAs set")
	}
	if len(opts.TLSConfig.Certificates) != 1 {
		t.Fatalf("expected client certificate")
	}
}

func TestParseOptionsMissingKey(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := writeTempCert(t, dir)
	t.Setenv(envRedisTLSCert, certPath)

	_, err := ParseOptions("rediss://localhost:6379")
	if err == nil {
		t.Fatalf("expected error for missing key")
	}
}

func TestNewClient_DefaultTimeouts(t *testing.T) {
	opts, err := newUniversalOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("newUniversalOptions error: %v", err)
	}
	if opts.DialTimeout != defaultDialTimeout {
		t.Fatalf("expected dial timeout %v, got %v", defaultDialTimeout, opts.DialTimeout)
	}
	if opts.ReadTimeout != defaultReadTimeout {
		t.Fatalf("expected read timeout %v, got %v", defaultReadTimeout, opts.ReadTimeout)
	}
	if opts.WriteTimeout != defaultWriteTimeout {
		t.Fatalf("expected write timeout %v, got %v", defaultWriteTimeout, opts.WriteTimeout)
	}
	if opts.ConnMaxIdleTime != defaultIdleTimeout {
		t.Fatalf("expected idle timeout %v, got %v", defaultIdleTimeout, opts.ConnMaxIdleTime)
	}
	if opts.ConnMaxLifetime != defaultConnMaxLife {
		t.Fatalf("expected max lifetime %v, got %v", defaultConnMaxLife, opts.ConnMaxLifetime)
	}
}

func TestNewClient_EnvOverrides(t *testing.T) {
	t.Setenv(envRedisDialTimeout, "7s")
	t.Setenv(envRedisReadTimeout, "4s")
	t.Setenv(envRedisWriteTimeout, "9s")
	t.Setenv(envRedisIdleTimeout, "2m")
	t.Setenv(envRedisConnMaxLife, "45m")

	opts, err := newUniversalOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("newUniversalOptions error: %v", err)
	}
	if opts.DialTimeout != 7*time.Second {
		t.Fatalf("expected dial timeout 7s, got %v", opts.DialTimeout)
	}
	if opts.ReadTimeout != 4*time.Second {
		t.Fatalf("expected read timeout 4s, got %v", opts.ReadTimeout)
	}
	if opts.WriteTimeout != 9*time.Second {
		t.Fatalf("expected write timeout 9s, got %v", opts.WriteTimeout)
	}
	if opts.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("expected idle timeout 2m, got %v", opts.ConnMaxIdleTime)
	}
	if opts.ConnMaxLifetime != 45*time.Minute {
		t.Fatalf("expected max lifetime 45m, got %v", opts.ConnMaxLifetime)
	}
}

func TestPoolStatsLogging(t *testing.T) {
	prevLogger := slog.Default()
	var logBuf syncBuffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	prevInterval := redisPoolStatsLogInterval
	redisPoolStatsLogInterval = 10 * time.Millisecond
	t.Cleanup(func() { redisPoolStatsLogInterval = prevInterval })

	t.Setenv(envRedisPoolStatsLog, "true")

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer srv.Close()

	client, err := NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "redis pool stats") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected redis pool stats log entry, got %q", logBuf.String())
}

func writeTempCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}
