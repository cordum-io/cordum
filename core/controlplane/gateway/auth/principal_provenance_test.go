package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
)

const headerPrincipalUnboundKey = "provenance-unbound-static-key-0000000000000"

const headerPrincipalBoundKey = "provenance-static-key-bound-to-worker-a-000"

const headerPrincipalManagedKey = "provenance-managed-runtime-key-00000000000"

const headerPrincipalFlag = "CORDUM_ALLOW_HEADER_PRINCIPAL"

// headerPrincipalPinEnv pins every variable NewBasicAuthProvider reads, so the
// host environment cannot change the profile, the flag or the key set. The
// static keys are one unbound key and one key bound to worker-a.
func headerPrincipalPinEnv(t *testing.T, mode, flag, insecure string) {
	t.Helper()
	keys := `[{"key":"` + headerPrincipalUnboundKey + `"},` +
		`{"key":"` + headerPrincipalBoundKey + `","principal_id":"worker-a"}]`
	for name, value := range map[string]string{
		"CORDUM_ENV":                    mode,
		"CORDUM_PRODUCTION":             "",
		headerPrincipalFlag:             flag,
		"CORDUM_API_KEYS":               keys,
		"CORDUM_API_KEY":                "",
		"CORDUM_API_KEYS_PATH":          "",
		"CORDUM_ALLOW_INSECURE_NO_AUTH": insecure,
		"CORDUM_JWT_HMAC_SECRET":        "",
		"CORDUM_JWT_PUBLIC_KEY":         "",
		"CORDUM_JWT_PUBLIC_KEY_PATH":    "",
		"CORDUM_JWT_REQUIRED":           "",
	} {
		t.Setenv(name, value)
	}
}

// headerPrincipalProvider builds the provider from the pinned environment, as
// the gateway does, and gives it a managed key store holding one key.
func headerPrincipalProvider(t *testing.T) *BasicAuthProvider {
	t.Helper()
	b, err := NewBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("NewBasicAuthProvider() error = %v, want nil", err)
	}
	b.SetKeyStore(&fakeKeyStore{
		validateFn: func(_ context.Context, rawKey string) (*ManagedKey, error) {
			if rawKey != headerPrincipalManagedKey {
				return nil, errors.New("unknown managed key")
			}
			return &ManagedKey{ID: "mk-provenance", Name: "provenance", Tenant: "default"}, nil
		},
		recordFn: func(context.Context, string) error { return nil },
	})
	t.Cleanup(b.DrainUsage)
	return b
}

// headerPrincipalHTTP authenticates one request. An empty key or principal
// leaves that header out; any other principal is sent verbatim.
func headerPrincipalHTTP(t *testing.T, b *BasicAuthProvider, key, principal string) *AuthContext {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	if key != "" {
		r.Header.Set("X-API-Key", key)
	}
	if principal != "" {
		r.Header.Set("X-Principal-Id", principal)
	}
	ac, err := b.AuthenticateHTTP(r)
	if err != nil {
		t.Fatalf("AuthenticateHTTP() error = %v, want nil", err)
	}
	return ac
}

// headerPrincipalGRPC is headerPrincipalHTTP over gRPC metadata.
func headerPrincipalGRPC(t *testing.T, b *BasicAuthProvider, key, principal string) *AuthContext {
	t.Helper()
	md := metadata.MD{}
	if key != "" {
		md.Set("x-api-key", key)
	}
	if principal != "" {
		md.Set("x-principal-id", principal)
	}
	ac, err := b.AuthenticateGRPC(metadata.NewIncomingContext(context.Background(), md))
	if err != nil {
		t.Fatalf("AuthenticateGRPC() error = %v, want nil", err)
	}
	return ac
}

// headerPrincipalAssert checks the principal, its provenance, and what
// AuthenticatedPrincipal reports for it.
func headerPrincipalAssert(t *testing.T, ac *AuthContext, wantPrincipal string, wantAsserted bool) {
	t.Helper()
	if ac.PrincipalID != wantPrincipal {
		t.Errorf("PrincipalID = %q, want %q", ac.PrincipalID, wantPrincipal)
	}
	if ac.PrincipalAsserted != wantAsserted {
		t.Errorf("PrincipalAsserted = %v, want %v", ac.PrincipalAsserted, wantAsserted)
	}
	wantAuthenticated := wantPrincipal
	if wantAsserted {
		wantAuthenticated = ""
	}
	if got := ac.AuthenticatedPrincipal(); got != wantAuthenticated {
		t.Errorf("AuthenticatedPrincipal() = %q, want %q", got, wantAuthenticated)
	}
}

// headerPrincipalCaptureLogs routes slog to a buffer until the test ends.
func headerPrincipalCaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// headerPrincipalFlagRecords returns the captured log records naming the flag.
func headerPrincipalFlagRecords(buf *bytes.Buffer) []string {
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, headerPrincipalFlag) {
			out = append(out, line)
		}
	}
	return out
}

func TestPrincipalProvenance_HeaderPrincipalIsAsserted(t *testing.T) {
	headerPrincipalPinEnv(t, "development", "", "true")
	b := headerPrincipalProvider(t)
	for _, tc := range []struct{ name, key string }{
		{"anonymous", ""},
		{"unbound-static-key", headerPrincipalUnboundKey},
		{"managed-key", headerPrincipalManagedKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headerPrincipalAssert(t, headerPrincipalHTTP(t, b, tc.key, "worker-b"), "worker-b", true)
			headerPrincipalAssert(t, headerPrincipalHTTP(t, b, tc.key, ""), "", false)
			headerPrincipalAssert(t, headerPrincipalHTTP(t, b, tc.key, " \t "), "", false)
		})
	}
}

func TestPrincipalProvenance_BoundCredentialsAreNotAsserted(t *testing.T) {
	headerPrincipalPinEnv(t, "development", "", "")
	b := headerPrincipalProvider(t)
	t.Run("bound-static-key-http", func(t *testing.T) {
		headerPrincipalAssert(t, headerPrincipalHTTP(t, b, headerPrincipalBoundKey, "worker-b"), "worker-a", false)
	})
	t.Run("bound-static-key-grpc", func(t *testing.T) {
		headerPrincipalAssert(t, headerPrincipalGRPC(t, b, headerPrincipalBoundKey, "worker-b"), "worker-a", false)
	})
	t.Run("managed-key-without-header", func(t *testing.T) {
		headerPrincipalAssert(t, headerPrincipalHTTP(t, b, headerPrincipalManagedKey, ""), "", false)
		headerPrincipalAssert(t, headerPrincipalGRPC(t, b, headerPrincipalManagedKey, ""), "", false)
	})
	t.Run("nil-and-zero-value-contexts", func(t *testing.T) {
		var nilCtx *AuthContext
		if got := nilCtx.AuthenticatedPrincipal(); got != "" {
			t.Errorf("nil AuthenticatedPrincipal() = %q, want \"\"", got)
		}
		// A literal without PrincipalAsserted keeps meaning a credential-backed principal.
		if got := (&AuthContext{PrincipalID: " worker-a "}).AuthenticatedPrincipal(); got != "worker-a" {
			t.Errorf("zero-value AuthenticatedPrincipal() = %q, want %q", got, "worker-a")
		}
	})
}

func TestPrincipalProvenance_ProductionManagedKeyIgnoresHeaderByDefault(t *testing.T) {
	headerPrincipalPinEnv(t, "production", "", "")
	logs := headerPrincipalCaptureLogs(t)
	b := headerPrincipalProvider(t)
	headerPrincipalAssert(t, headerPrincipalHTTP(t, b, headerPrincipalManagedKey, "worker-b"), "", false)
	headerPrincipalAssert(t, headerPrincipalGRPC(t, b, headerPrincipalManagedKey, "worker-b"), "", false)
	headerPrincipalAssert(t, headerPrincipalHTTP(t, b, headerPrincipalUnboundKey, "worker-b"), "", false)
	if records := headerPrincipalFlagRecords(logs); len(records) != 0 {
		t.Errorf("log records naming %s = %q, want none while the flag is unset", headerPrincipalFlag, records)
	}
}

func TestPrincipalProvenance_ProductionExplicitFlagKeepsHeaderAsserted(t *testing.T) {
	headerPrincipalPinEnv(t, "production", "true", "")
	logs := headerPrincipalCaptureLogs(t)
	b := headerPrincipalProvider(t)
	for _, key := range []string{headerPrincipalManagedKey, headerPrincipalUnboundKey} {
		headerPrincipalAssert(t, headerPrincipalHTTP(t, b, key, "worker-b"), "worker-b", true)
	}
	headerPrincipalAssert(t, headerPrincipalGRPC(t, b, headerPrincipalManagedKey, "worker-b"), "worker-b", true)
	headerPrincipalAssert(t, headerPrincipalHTTP(t, b, headerPrincipalBoundKey, "worker-b"), "worker-a", false)
	records := headerPrincipalFlagRecords(logs)
	if len(records) != 1 || !strings.Contains(records[0], "level=WARN") || !strings.Contains(records[0], "X-Principal-Id") {
		t.Fatalf("log records naming %s = %q, want exactly one level=WARN record mentioning X-Principal-Id", headerPrincipalFlag, records)
	}
}

func TestPrincipalProvenance_GRPCMetadataPrincipalIsAsserted(t *testing.T) {
	headerPrincipalPinEnv(t, "development", "", "true")
	b := headerPrincipalProvider(t)
	for _, tc := range []struct{ name, key string }{
		{"anonymous", ""},
		{"unbound-static-key", headerPrincipalUnboundKey},
		{"managed-key", headerPrincipalManagedKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headerPrincipalAssert(t, headerPrincipalGRPC(t, b, tc.key, "worker-b"), "worker-b", true)
		})
	}
	t.Run("bound-static-key", func(t *testing.T) {
		headerPrincipalAssert(t, headerPrincipalGRPC(t, b, headerPrincipalBoundKey, "worker-b"), "worker-a", false)
	})
}
