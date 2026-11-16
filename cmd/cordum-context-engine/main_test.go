package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/tools/certgen"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type serverTLSFixture struct {
	cert, key, ca string
	roots         *x509.CertPool
	client        tls.Certificate
}

func newServerTLSFixture(t *testing.T) serverTLSFixture {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, certgen.GenerateAll(certgen.Options{BaseDir: dir}))
	f := serverTLSFixture{
		cert:  filepath.Join(dir, "server", "tls.crt"),
		key:   filepath.Join(dir, "server", "tls.key"),
		ca:    filepath.Join(dir, "ca", "ca.crt"),
		roots: x509.NewCertPool(),
	}
	raw, err := os.ReadFile(f.ca)
	require.NoError(t, err)
	require.True(t, f.roots.AppendCertsFromPEM(raw))
	f.client, err = tls.LoadX509KeyPair(filepath.Join(dir, "client", "tls.crt"), filepath.Join(dir, "client", "tls.key"))
	require.NoError(t, err)
	return f
}

func TestServerTransportCredentials(t *testing.T) {
	t.Setenv(env.EnvProduction, "false")
	t.Setenv(env.EnvTLSMinVersion, "")
	f := newServerTLSFixture(t)
	t.Run("configuration", func(t *testing.T) { testServerTLSConfiguration(t, f) })
	other := newServerTLSFixture(t)
	for _, tc := range []struct {
		name, mode string
		client     *tls.Certificate
		requireCA  bool
		refused    bool
	}{
		{"trusted client", "production", &f.client, true, false},
		{"missing client", "production", nil, true, true},
		{"untrusted client", "production", &other.client, true, true},
		{"development without client CA", "development", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(env.EnvMode, tc.mode)
			t.Setenv("CONTEXT_ENGINE_TLS_CERT", f.cert)
			t.Setenv("CONTEXT_ENGINE_TLS_KEY", f.key)
			t.Setenv("CONTEXT_ENGINE_TLS_CLIENT_CA", "")
			if tc.requireCA {
				t.Setenv("CONTEXT_ENGINE_TLS_CLIENT_CA", f.ca)
			}
			creds, err := serverTransportCredentials(t.Context())
			require.NoError(t, err)
			testServerTLSCall(t, creds, f.roots, tc.client, tc.refused)
		})
	}
}

func testServerTLSConfiguration(t *testing.T, f serverTLSFixture) {
	t.Helper()
	invalidCA := filepath.Join(t.TempDir(), "invalid.pem")
	require.NoError(t, os.WriteFile(invalidCA, []byte("not a certificate"), 0o600))
	emptyCA := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(emptyCA, nil, 0o600))
	for _, tc := range []struct {
		name, mode, cert, key, ca, want string
	}{
		{"production requires client CA", "production", f.cert, f.key, "", "CONTEXT_ENGINE_TLS_CLIENT_CA"},
		{"production requires server TLS", "production", "", "", "", "tls required in production"},
		{"client CA requires server TLS", "development", "", "", f.ca, "CONTEXT_ENGINE_TLS_CERT"},
		{"certificate without key", "development", f.cert, "", "", "CONTEXT_ENGINE_TLS_KEY"},
		{"key without certificate", "development", "", f.key, "", "CONTEXT_ENGINE_TLS_CERT"},
		{"unreadable client CA", "development", f.cert, f.key, filepath.Join(t.TempDir(), "missing.pem"), "CONTEXT_ENGINE_TLS_CLIENT_CA"},
		{"unparsable client CA", "development", f.cert, f.key, invalidCA, "CONTEXT_ENGINE_TLS_CLIENT_CA"},
		{"empty client CA", "development", f.cert, f.key, emptyCA, "CONTEXT_ENGINE_TLS_CLIENT_CA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(env.EnvMode, tc.mode)
			t.Setenv("CONTEXT_ENGINE_TLS_CERT", tc.cert)
			t.Setenv("CONTEXT_ENGINE_TLS_KEY", tc.key)
			t.Setenv("CONTEXT_ENGINE_TLS_CLIENT_CA", tc.ca)
			_, err := serverTransportCredentials(t.Context())
			require.ErrorContains(t, err, tc.want)
		})
	}
}

type serverTLSHealthProbe struct {
	healthpb.UnimplementedHealthServer
	calls atomic.Int32
}

func (s *serverTLSHealthProbe) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	s.calls.Add(1)
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func testServerTLSCall(t *testing.T, creds grpc.ServerOption, roots *x509.CertPool, client *tls.Certificate, refused bool) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer(creds)
	probe := &serverTLSHealthProbe{}
	healthpb.RegisterHealthServer(s, probe)
	done := make(chan error, 1)
	go func() { done <- s.Serve(lis) }()
	t.Cleanup(func() { s.Stop(); require.NoError(t, <-done) })
	cfg := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	if client != nil {
		// Send even an untrusted certificate, rather than letting CA selection omit it.
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return client, nil }
	}
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if refused {
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.ErrorContains(t, err, "certificate")
		require.Nil(t, resp)
		require.Zero(t, probe.calls.Load(), "refusal must precede the handler")
		return
	}
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	require.EqualValues(t, 1, probe.calls.Load())
}

func TestPackageImports(t *testing.T) {
	// Verify package can be imported and compiled
	if buildinfo.Version == "" {
		t.Log("buildinfo not set (expected in dev)")
	}
}

func TestMainExists(t *testing.T) {
	// This test verifies that main() function exists and compiles
	// The actual main() is tested via integration tests
	t.Log("main function exists and compiles")
}
