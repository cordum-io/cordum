package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLicenseVerifyStates(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name      string
		claims    Claims
		verifyPub func(t *testing.T, good ed25519.PublicKey) ed25519.PublicKey
		wantErr   error
		wantState ExpiryState
		wantGrace bool
		wantRBAC  bool
	}{
		{
			name: "valid license",
			claims: Claims{
				OrgID:     "org-1",
				LicenseID: "lic-valid",
				Plan:      "enterprise",
				ExpiresAt: now.Add(45 * 24 * time.Hour).Format(time.RFC3339),
				Entitlements: &Entitlements{
					RBAC: true,
				},
			},
			wantState: ExpiryStateValid,
			wantRBAC:  true,
		},
		{
			name: "grace period default",
			claims: Claims{
				OrgID:     "org-1",
				LicenseID: "lic-grace",
				Plan:      "enterprise",
				ExpiresAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
				Entitlements: &Entitlements{
					SSO: true,
				},
			},
			wantState: ExpiryStateGrace,
			wantGrace: true,
		},
		{
			name: "expired after default grace",
			claims: Claims{
				OrgID:     "org-1",
				LicenseID: "lic-expired",
				Plan:      "enterprise",
				ExpiresAt: now.Add(-15 * 24 * time.Hour).Format(time.RFC3339),
			},
			wantErr:   ErrLicenseExpired,
			wantState: ExpiryStateDegraded,
		},
		{
			name: "not active yet",
			claims: Claims{
				OrgID:     "org-1",
				LicenseID: "lic-not-before",
				Plan:      "enterprise",
				NotBefore: now.Add(time.Hour).Format(time.RFC3339),
			},
			wantErr: ErrLicenseNotActive,
		},
		{
			name: "signature mismatch",
			claims: Claims{
				OrgID:     "org-1",
				LicenseID: "lic-badsig",
				Plan:      "enterprise",
				ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
			},
			verifyPub: func(t *testing.T, _ ed25519.PublicKey) ed25519.PublicKey {
				t.Helper()
				pub, _, err := ed25519.GenerateKey(nil)
				if err != nil {
					t.Fatalf("generate alternate key: %v", err)
				}
				return pub
			},
			wantErr: ErrLicenseSignatureInvalid,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pub, priv, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatalf("generate key: %v", err)
			}
			lic := signLicense(t, tc.claims, priv)

			verifyPub := pub
			if tc.verifyPub != nil {
				verifyPub = tc.verifyPub(t, pub)
			}

			err = lic.Verify(verifyPub, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantState != "" && lic.ExpiryState != tc.wantState {
				t.Fatalf("Verify() state = %q, want %q", lic.ExpiryState, tc.wantState)
			}
			if gotGrace := lic.Grace != nil; gotGrace != tc.wantGrace {
				t.Fatalf("Verify() grace = %v, want %v", gotGrace, tc.wantGrace)
			}
			if tc.wantRBAC != tc.claims.FeatureEnabled("rbac") {
				t.Fatalf("FeatureEnabled(rbac) = %v, want %v", tc.claims.FeatureEnabled("rbac"), tc.wantRBAC)
			}
		})
	}
}

func TestLoadFromEnvSources(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	licenseJSON := marshalLicenseJSON(t, signLicense(t, Claims{
		OrgID:     "org-1",
		LicenseID: "lic-env",
		Plan:      "enterprise",
		ExpiresAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}, priv))

	testCases := []struct {
		name      string
		setupEnv  func(t *testing.T)
		wantNil   bool
		wantErr   bool
		wantID    string
		verifyKey bool
	}{
		{
			name: "load from file",
			setupEnv: func(t *testing.T) {
				t.Helper()
				dir := t.TempDir()
				path := filepath.Join(dir, "license.json")
				if err := os.WriteFile(path, licenseJSON, 0o600); err != nil {
					t.Fatalf("write license file: %v", err)
				}
				t.Setenv(envLicenseFile, path)
				t.Setenv(envLicenseToken, "")
			},
			wantID:    "lic-env",
			verifyKey: true,
		},
		{
			name: "load from token",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv(envLicenseFile, "")
				t.Setenv(envLicenseToken, base64.StdEncoding.EncodeToString(licenseJSON))
			},
			wantID: "lic-env",
		},
		{
			name: "missing license",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv(envLicenseFile, "")
				t.Setenv(envLicenseToken, "")
			},
			wantNil: true,
		},
		{
			name: "corrupt license token",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv(envLicenseFile, "")
				t.Setenv(envLicenseToken, "{not-json")
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envLicensePublicKey, base64.StdEncoding.EncodeToString(pub))
			t.Setenv(envLicenseKeyPath, "")
			tc.setupEnv(t)

			lic, err := LoadFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("LoadFromEnv() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFromEnv() error = %v", err)
			}
			if tc.wantNil {
				if lic != nil {
					t.Fatalf("LoadFromEnv() license = %#v, want nil", lic)
				}
				return
			}
			if lic == nil {
				t.Fatalf("LoadFromEnv() license = nil")
			}
			if lic.Payload.LicenseID != tc.wantID {
				t.Fatalf("LoadFromEnv() license id = %q, want %q", lic.Payload.LicenseID, tc.wantID)
			}
			if tc.verifyKey {
				loadedPub, err := PublicKeyFromEnv()
				if err != nil {
					t.Fatalf("PublicKeyFromEnv() error = %v", err)
				}
				if err := lic.Verify(loadedPub, time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)); err != nil {
					t.Fatalf("Verify() after LoadFromEnv() error = %v", err)
				}
			}
		})
	}
}

func TestLoadFromEnvFallsBackToStandardPath(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	licenseJSON := marshalLicenseJSON(t, signLicense(t, Claims{
		OrgID:     "org-2",
		LicenseID: "lic-standard",
		Plan:      "team",
		ExpiresAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}, priv))

	t.Setenv(envLicenseFile, "")
	t.Setenv(envLicenseToken, "")
	t.Setenv(envLicensePublicKey, base64.StdEncoding.EncodeToString(pub))
	t.Setenv(envLicenseKeyPath, "")

	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("APPDATA", base)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("USERPROFILE", filepath.Join(base, "home"))

	path, err := PreferredLicenseFilePath()
	if err != nil {
		t.Fatalf("PreferredLicenseFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create preferred path dir: %v", err)
	}
	if err := os.WriteFile(path, licenseJSON, 0o600); err != nil {
		t.Fatalf("write preferred path license: %v", err)
	}

	lic, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if lic == nil {
		t.Fatal("LoadFromEnv() license = nil")
	}
	if lic.Payload.LicenseID != "lic-standard" {
		t.Fatalf("LoadFromEnv() license id = %q, want lic-standard", lic.Payload.LicenseID)
	}
}

func TestPreferredLicenseFilePathHonorsEnvOverride(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-license.json")
	t.Setenv(envLicenseFile, custom)

	got, err := PreferredLicenseFilePath()
	if err != nil {
		t.Fatalf("PreferredLicenseFilePath() error = %v", err)
	}
	if got != custom {
		t.Fatalf("PreferredLicenseFilePath() = %q, want %q", got, custom)
	}
}

func TestPublicKeyFromEnvFallbacks(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)

	testCases := []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{
			name: "env value",
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(envLicensePublicKey, encoded)
				t.Setenv(envLicenseKeyPath, "")
			},
			want: encoded,
		},
		{
			name: "env path",
			setup: func(t *testing.T) {
				t.Helper()
				dir := t.TempDir()
				path := filepath.Join(dir, "pub.key")
				if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
					t.Fatalf("write public key: %v", err)
				}
				t.Setenv(envLicensePublicKey, "")
				t.Setenv(envLicenseKeyPath, path)
			},
			want: encoded,
		},
		{
			name: "embedded fallback",
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv(envLicensePublicKey, "")
				t.Setenv(envLicenseKeyPath, "")
			},
			want: strings.TrimSpace(embeddedLicensePublicKey),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			loaded, err := PublicKeyFromEnv()
			if err != nil {
				t.Fatalf("PublicKeyFromEnv() error = %v", err)
			}
			got := base64.StdEncoding.EncodeToString(loaded)
			if got != tc.want {
				t.Fatalf("PublicKeyFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseLicenseLegacyCompat(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	graceDays := 21
	legacy := legacyClaims{
		OrgID:     "org-compat",
		LicenseID: "lic-compat",
		Plan:      "team",
		Features: map[string]bool{
			"rbac":        true,
			"white_label": true,
		},
		Limits: map[string]int64{
			"max_workers": 25,
		},
		GraceDays: &graceDays,
		ExpiresAt: now.Add(60 * 24 * time.Hour).Format(time.RFC3339),
	}

	doc := struct {
		KID       string       `json:"kid,omitempty"`
		Payload   legacyClaims `json:"payload"`
		Signature string       `json:"signature"`
	}{
		KID:       "kid-legacy",
		Payload:   legacy,
		Signature: signPayload(t, legacy, priv),
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy license: %v", err)
	}

	lic, err := parseLicense(raw)
	if err != nil {
		t.Fatalf("parseLicense() error = %v", err)
	}
	if lic.KID != "kid-legacy" {
		t.Fatalf("parseLicense() kid = %q, want %q", lic.KID, "kid-legacy")
	}
	if lic.Payload.Rights == nil || !lic.Payload.Rights.WhiteLabel {
		t.Fatalf("parseLicense() rights = %#v, want white_label=true", lic.Payload.Rights)
	}
	if lic.Payload.Entitlements == nil {
		t.Fatalf("parseLicense() entitlements = nil")
	}
	if !lic.Payload.FeatureEnabled("rbac") {
		t.Fatalf("FeatureEnabled(rbac) = false, want true")
	}
	if lic.Payload.Entitlements.MaxWorkers != 25 {
		t.Fatalf("migrated max_workers = %d, want 25", lic.Payload.Entitlements.MaxWorkers)
	}
	if lic.Payload.EffectiveGraceDays() != graceDays {
		t.Fatalf("EffectiveGraceDays() = %d, want %d", lic.Payload.EffectiveGraceDays(), graceDays)
	}
	if err := lic.Verify(pub, now); err != nil {
		t.Fatalf("Verify() after legacy compat parse error = %v", err)
	}
}

func signLicense(t *testing.T, claims Claims, priv ed25519.PrivateKey) *License {
	t.Helper()
	return &License{
		Payload:   claims,
		Signature: signPayload(t, claims, priv),
	}
}

func signPayload(t *testing.T, payload any, priv ed25519.PrivateKey) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw))
}

func marshalLicenseJSON(t *testing.T, lic *License) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(lic, "", "  ")
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	return raw
}
