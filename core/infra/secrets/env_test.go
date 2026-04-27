package secrets

import (
	"context"
	"testing"
	"time"
)

func TestParseCacheTTL(t *testing.T) {
	cases := []struct {
		input string
		want  time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"30s", 30 * time.Second},
		{"0", 0},
		{"1h", 1 * time.Hour},
		{"300", 300 * time.Second},
		{"", 5 * time.Minute}, // default
		{"invalid", 5 * time.Minute}, // fallback to default
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseCacheTTL(tc.input)
			if got != tc.want {
				t.Fatalf("parseCacheTTL(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestNewResolverFromEnv_NoProviders(t *testing.T) {
	// Ensure none of the provider env vars are set.
	// This test relies on the CI/test environment not having VAULT_ADDR
	// or AWS_REGION set.  If they are, the test still passes since we
	// only need to verify the nil-resolver path.
	t.Setenv(EnvVaultAddr, "")
	t.Setenv(EnvVaultToken, "")
	t.Setenv(EnvAWSRegion, "")
	t.Setenv(EnvAWSAccessKeyID, "")
	t.Setenv(EnvAWSSecretAccessKey, "")

	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil resolver when no providers configured")
	}
}

func TestNewResolverFromEnv_VaultOnly(t *testing.T) {
	t.Setenv(EnvVaultAddr, "https://vault.example.com:8200")
	t.Setenv(EnvVaultToken, "s.test-token")
	t.Setenv(EnvAWSRegion, "")
	t.Setenv(EnvAWSAccessKeyID, "")
	t.Setenv(EnvAWSSecretAccessKey, "")

	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver with vault configured")
	}
	if !r.HasProvider("vault") {
		t.Fatal("expected vault provider")
	}
	if r.HasProvider("aws-sm") {
		t.Fatal("expected no aws-sm provider")
	}
	_ = r.Close()
}

func TestNewResolverFromEnv_AWSOnly(t *testing.T) {
	t.Setenv(EnvVaultAddr, "")
	t.Setenv(EnvAWSRegion, "us-east-1")
	t.Setenv(EnvAWSAccessKeyID, "AKID123")
	t.Setenv(EnvAWSSecretAccessKey, "SECRET456")

	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver with aws configured")
	}
	if r.HasProvider("vault") {
		t.Fatal("expected no vault provider")
	}
	if !r.HasProvider("aws-sm") {
		t.Fatal("expected aws-sm provider")
	}
	_ = r.Close()
}

func TestNewResolverFromEnv_Both(t *testing.T) {
	t.Setenv(EnvVaultAddr, "https://vault.example.com")
	t.Setenv(EnvVaultToken, "s.token")
	t.Setenv(EnvAWSRegion, "eu-west-1")
	t.Setenv(EnvAWSAccessKeyID, "AKID")
	t.Setenv(EnvAWSSecretAccessKey, "SECRET")

	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if !r.HasProvider("vault") {
		t.Fatal("expected vault provider")
	}
	if !r.HasProvider("aws-sm") {
		t.Fatal("expected aws-sm provider")
	}
	_ = r.Close()
}

func TestNewResolverFromEnv_VaultAddrNoToken(t *testing.T) {
	t.Setenv(EnvVaultAddr, "https://vault.example.com")
	t.Setenv(EnvVaultToken, "")
	t.Setenv(EnvAWSRegion, "")

	// Should return nil (no providers) with a warning log, not an error.
	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil resolver when vault token missing")
	}
}

func TestNewResolverFromEnv_AWSRegionNoCredentials(t *testing.T) {
	t.Setenv(EnvVaultAddr, "")
	t.Setenv(EnvAWSRegion, "us-east-1")
	t.Setenv(EnvAWSAccessKeyID, "")
	t.Setenv(EnvAWSSecretAccessKey, "")

	// Should return nil with a warning, not an error.
	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil resolver when aws credentials missing")
	}
}

func TestNewResolverFromEnv_CacheTTL(t *testing.T) {
	t.Setenv(EnvVaultAddr, "https://vault.example.com")
	t.Setenv(EnvVaultToken, "s.token")
	t.Setenv(EnvAWSRegion, "")
	t.Setenv(EnvSecretCacheTTL, "30s")

	r, err := NewResolverFromEnv(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if r.cache.ttl != 30*time.Second {
		t.Fatalf("cache TTL = %v, want 30s", r.cache.ttl)
	}
	_ = r.Close()
}
