package env

import (
	"os"
	"testing"
)

func TestIsProduction(t *testing.T) {
	t.Setenv(EnvMode, "")
	t.Setenv(EnvProduction, "")
	if IsProduction() {
		t.Fatalf("expected non-production by default")
	}

	t.Setenv(EnvMode, "production")
	t.Setenv(EnvProduction, "")
	if !IsProduction() {
		t.Fatalf("expected production for CORDUM_ENV")
	}

	t.Setenv(EnvMode, "")
	t.Setenv(EnvProduction, "true")
	if !IsProduction() {
		t.Fatalf("expected production for CORDUM_PRODUCTION")
	}
}

func TestTLSMinVersion(t *testing.T) {
	t.Setenv(EnvMode, "")
	t.Setenv(EnvProduction, "")
	t.Setenv(EnvTLSMinVersion, "")
	if got := TLSMinVersion(); got == 0 {
		t.Fatalf("expected default TLS min version")
	}

	t.Setenv(EnvTLSMinVersion, "1.3")
	if got := TLSMinVersion(); got == 0 {
		t.Fatalf("expected TLS min version override")
	}
}

func TestBool(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"1":     true,
		"yes":   true,
		"on":    true,
		"false": false,
		"0":     false,
		"":      false,
	}
	for raw, expect := range cases {
		if err := os.Setenv("ENV_BOOL_TEST", raw); err != nil {
			t.Fatalf("setenv: %v", err)
		}
		if got := Bool("ENV_BOOL_TEST"); got != expect {
			t.Fatalf("Bool(%q)=%v want %v", raw, got, expect)
		}
	}
}
