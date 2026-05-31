package auth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestValidateSecretStrength_WeakPasswords(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		minLen int
		errMsg string
	}{
		{"too short", "abc", 16, "too short"},
		{"contains cordum", "CordumAdmin2026!", 16, "weak pattern"},
		{"contains admin", "MyAdminPass123!!", 16, "weak pattern"},
		{"contains password", "MyPassword1234!!", 16, "weak pattern"},
		{"contains changeme", "changeme12345!!!", 16, "weak pattern"},
		{"contains 12345", "secure12345pass!", 16, "weak pattern"},
		{"all same char", "aaaaaaaaaaaaaaaa", 16, "low entropy"},
		{"only digits", "1234567890123456", 16, "weak pattern"},
		{"only lowercase", "abcdefghijklmnop", 16, "character classes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecretStrength("TEST_SECRET", tt.value, tt.minLen)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tt.value)
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Fatalf("expected error containing %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

func TestValidateSecretStrength_StrongPasswords(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		minLen int
	}{
		{"random base64", "kX7mP2vQ9wR3tY5uI8oL1jHg", 16},
		{"hex string", "3f52861812d3584dcfa9b42dcd64fdefe7c00136", 32},
		{"mixed strong", "Tr0ub4dor&3Horse!Batt3ry", 16},
		{"long random", "aB3$xY7!mN9@pQ2%", 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecretStrength("TEST_SECRET", tt.value, tt.minLen)
			if err != nil {
				t.Fatalf("expected nil error for strong password, got: %v", err)
			}
		})
	}
}

func TestValidateSecretStrength_EmptyPassword(t *testing.T) {
	err := ValidateSecretStrength("TEST", "", 16)
	if err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		input   string
		minBits float64
		maxBits float64
	}{
		{"", 0, 0},
		{"aaaa", 0, 0.1},       // all same char -> 0 entropy
		{"ab", 0.9, 1.1},       // 2 unique chars -> ~1.0 bit
		{"abcd", 1.9, 2.1},     // 4 unique chars -> ~2.0 bits
		{"abcdefgh", 2.9, 3.1}, // 8 unique chars -> ~3.0 bits
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			e := shannonEntropy(tt.input)
			if e < tt.minBits || e > tt.maxBits {
				t.Fatalf("shannonEntropy(%q) = %.2f, expected [%.1f, %.1f]",
					tt.input, e, tt.minBits, tt.maxBits)
			}
		})
	}
}

func TestValidateStartupSecrets_SkipValidation(t *testing.T) {
	t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "true")
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "weak")

	err := ValidateStartupSecrets()
	if err != nil {
		t.Fatalf("expected skip validation to pass, got: %v", err)
	}
}

func TestValidateStartupSecrets_DevModeSkips(t *testing.T) {
	t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "weak")

	err := ValidateStartupSecrets()
	if err != nil {
		t.Fatalf("expected dev mode to skip validation, got: %v", err)
	}
}

func TestValidateStartupSecrets_ProductionRejectsWeak(t *testing.T) {
	t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "CordumAdmin2026!")

	err := ValidateStartupSecrets()
	if err == nil {
		t.Fatal("expected weak admin password to be rejected in production")
	}
}

func TestValidateStartupSecrets_EmptyRedisPasswordAllowed(t *testing.T) {
	t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("REDIS_PASSWORD", "")

	err := ValidateStartupSecrets()
	if err != nil {
		t.Fatalf("expected empty Redis password to be allowed, got: %v", err)
	}
}

// BUG-014: production with user auth disabled emits a one-time warn so the
// silent posture is visible at boot. Non-prod is silent; auth-enabled is
// silent. ValidateStartupSecrets runs once per boot so a plain warning is
// inherently one-shot.
func TestValidateStartupSecrets_WarnsOnUserAuthDisabled(t *testing.T) {
	captureLogger := func(t *testing.T) (*slog.Logger, *bytes.Buffer) {
		t.Helper()
		var buf bytes.Buffer
		return slog.New(slog.NewTextHandler(&buf, nil)), &buf
	}

	const warnText = "user auth disabled"

	t.Run("production_user_auth_disabled_warns", func(t *testing.T) {
		t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
		t.Setenv("CORDUM_ENV", "production")
		t.Setenv("CORDUM_USER_AUTH_ENABLED", "")
		t.Setenv("CORDUM_ADMIN_PASSWORD", "")
		t.Setenv("CORDUM_API_KEY", "")
		t.Setenv("REDIS_PASSWORD", "")
		logger, buf := captureLogger(t)
		if err := validateStartupSecrets(logger); err != nil {
			t.Fatalf("validator err = %v", err)
		}
		if !strings.Contains(buf.String(), warnText) {
			t.Fatalf("expected warn containing %q, got: %s", warnText, buf.String())
		}
	})

	t.Run("production_user_auth_enabled_silent", func(t *testing.T) {
		t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
		t.Setenv("CORDUM_ENV", "production")
		t.Setenv("CORDUM_USER_AUTH_ENABLED", "true")
		t.Setenv("CORDUM_ADMIN_PASSWORD", "")
		t.Setenv("CORDUM_API_KEY", "")
		t.Setenv("REDIS_PASSWORD", "")
		logger, buf := captureLogger(t)
		if err := validateStartupSecrets(logger); err != nil {
			t.Fatalf("validator err = %v", err)
		}
		if strings.Contains(buf.String(), warnText) {
			t.Fatalf("expected NO warn when user auth enabled, got: %s", buf.String())
		}
	})

	t.Run("non_production_silent", func(t *testing.T) {
		t.Setenv("CORDUM_SKIP_SECRET_VALIDATION", "")
		t.Setenv("CORDUM_ENV", "development")
		t.Setenv("CORDUM_USER_AUTH_ENABLED", "")
		logger, buf := captureLogger(t)
		if err := validateStartupSecrets(logger); err != nil {
			t.Fatalf("validator err = %v", err)
		}
		if strings.Contains(buf.String(), warnText) {
			t.Fatalf("expected NO warn outside production, got: %s", buf.String())
		}
	})
}
