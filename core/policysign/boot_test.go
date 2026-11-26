package policysign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func kernelBootLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	resetBootEnv(t)
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", EnvLegacyRequireSig} {
		t.Setenv(name, "")
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, EnvPublicKeyPrefix) {
			t.Setenv(name, "")
		}
	}
	var log bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &log
}

func TestCheckKernelBoot_ProductionDefaultReportsEnforce(t *testing.T) {
	for _, name := range []string{"profile", "flag", "invalid", "trusted"} {
		t.Run(name, func(t *testing.T) {
			log := kernelBootLog(t)
			t.Setenv("CORDUM_ENV", "production")
			if name == "flag" {
				t.Setenv("CORDUM_ENV", "")
				t.Setenv("CORDUM_PRODUCTION", "true")
			}
			if name == "invalid" {
				t.Setenv(EnvStrictMode, "unrecognised")
			}
			want := ErrEnforceMissingTrustStore
			if name == "trusted" {
				pub, _, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv(EnvPublicKeyPrefix+"BOOT", base64.StdEncoding.EncodeToString(pub))
				want = nil
			}
			if err := CheckKernelBoot(); !errors.Is(err, want) {
				t.Fatalf("boot error = %v, want %v", err, want)
			}
			if !strings.Contains(log.String(), "mode=enforce") || strings.Contains(log.String(), "verification will be skipped") {
				t.Fatalf("incorrect boot mode log: %s", log)
			}
			if name == "invalid" && !strings.Contains(log.String(), "falling back") {
				t.Fatalf("missing fallback warning: %s", log)
			}
			if name == "trusted" && !strings.Contains(log.String(), "level=INFO") {
				t.Fatalf("missing enabled info: %s", log)
			}
		})
	}
}

func TestCheckKernelBoot_ExplicitWarnDowngradeIsReported(t *testing.T) {
	for _, name := range []string{"explicit", "development", "invalid"} {
		t.Run(name, func(t *testing.T) {
			log := kernelBootLog(t)
			switch name {
			case "explicit":
				t.Setenv("CORDUM_ENV", "production")
				t.Setenv(EnvStrictMode, "warn")
			case "invalid":
				t.Setenv(EnvStrictMode, "unrecognised")
			}
			if err := CheckKernelBoot(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(log.String(), "mode=warn") || !strings.Contains(log.String(), "verification will be skipped") {
				t.Fatalf("missing downgrade warning: %s", log)
			}
			if name == "invalid" && !strings.Contains(log.String(), "falling back") {
				t.Fatalf("missing fallback warning: %s", log)
			}
		})
	}
}

func resetBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvStrictMode, "")
	t.Setenv(EnvSigningKey, "")
	t.Setenv(EnvSigningKeyPath, "")
	t.Setenv(EnvSigningKeyID, "")
	t.Setenv(envLegacyPublicKey, "")
	t.Setenv(envLegacyKeyID, "")
}

func TestCheckGatewayBoot_OffOK(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "off")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("off + no key should pass, got %v", err)
	}
}

func TestCheckGatewayBoot_WarnNoKeyWarns(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("warn + no key should pass (with log), got %v", err)
	}
}

func TestCheckGatewayBoot_EnforceNoKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	err := CheckGatewayBoot()
	if !errors.Is(err, ErrEnforceMissingSigningKey) {
		t.Fatalf("want ErrEnforceMissingSigningKey, got %v", err)
	}
}

func TestCheckGatewayBoot_EnforceWithKeyOK(t *testing.T) {
	resetBootEnv(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString(priv))
	t.Setenv(EnvSigningKeyID, "p1")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("enforce + key should pass, got %v", err)
	}
}

func TestCheckGatewayBoot_BadKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	t.Setenv(EnvSigningKey, "!!!malformed!!!")
	err := CheckGatewayBoot()
	if err == nil {
		t.Fatal("malformed key should fail at boot")
	}
}

func TestCheckKernelBoot_OffOK(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "off")
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("off + no trust store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_WarnEmptyStorePasses(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("warn + empty store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceEmptyStoreFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	err := CheckKernelBoot()
	if !errors.Is(err, ErrEnforceMissingTrustStore) {
		t.Fatalf("want ErrEnforceMissingTrustStore, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceWithStoreOK(t *testing.T) {
	resetBootEnv(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("enforce + trust store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceInvalidKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvPublicKeyPrefix+"BAD", "!!!not-base64!!!")
	err := CheckKernelBoot()
	if err == nil {
		t.Fatal("malformed trust key should fail")
	}
}
