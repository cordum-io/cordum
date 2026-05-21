package ci_test

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow/ci"
)

func TestLoadOIDCConfigFromEnv_GitLabSaaS_DefaultIssuer(t *testing.T) {
	t.Setenv(ci.EnvOIDCTrustGitLab, "")
	t.Setenv(ci.EnvOIDCAudienceGitLab, "")

	cfg, err := ci.LoadOIDCConfigFromEnv(ci.ProviderGitLab, ci.OIDCConfig{
		GitLabBaseURL: "https://gitlab.com",
	})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if cfg.Disabled {
		t.Fatalf("expected GitLab.com SaaS to have OIDC enabled by default")
	}
	if cfg.Issuer != ci.DefaultGitLabSaaSIssuer {
		t.Errorf("Issuer = %q, want %q (Q6: GitLab.com SaaS default)", cfg.Issuer, ci.DefaultGitLabSaaSIssuer)
	}
	if cfg.Audience != ci.DefaultOIDCAudience {
		t.Errorf("Audience = %q, want %q (Cordum default)", cfg.Audience, ci.DefaultOIDCAudience)
	}
}

func TestLoadOIDCConfigFromEnv_GitLabSelfHosted_RequiresOperatorOverride(t *testing.T) {
	t.Setenv(ci.EnvOIDCTrustGitLab, "")

	cfg, err := ci.LoadOIDCConfigFromEnv(ci.ProviderGitLab, ci.OIDCConfig{
		GitLabBaseURL: "https://gitlab.acme.internal",
	})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	// Self-hosted with no operator override must skip OIDC (Q6).
	if !cfg.Disabled {
		t.Errorf("self-hosted GitLab without operator override should fall through to tier-2; got Disabled=false issuer=%q", cfg.Issuer)
	}
}

// TestLoadOIDCConfigFromEnv_GitLabSaaS_HandlesPortAndUserinfo regression-
// tests the adversarial-review finding: `https://gitlab.com:443` and
// `https://user@gitlab.com` must still be recognised as SaaS so the
// Cordum default issuer applies.
func TestLoadOIDCConfigFromEnv_GitLabSaaS_HandlesPortAndUserinfo(t *testing.T) {
	t.Setenv(ci.EnvOIDCTrustGitLab, "")
	for _, base := range []string{
		"https://gitlab.com:443",
		"https://gitlab.com:443/api/v4",
		"https://user@gitlab.com",
		"https://www.gitlab.com",
	} {
		cfg, err := ci.LoadOIDCConfigFromEnv(ci.ProviderGitLab, ci.OIDCConfig{GitLabBaseURL: base})
		if err != nil {
			t.Fatalf("LoadOIDCConfigFromEnv(%q): %v", base, err)
		}
		if cfg.Disabled {
			t.Errorf("base %q should be recognised as SaaS (Q6 default)", base)
		}
		if cfg.Issuer != ci.DefaultGitLabSaaSIssuer {
			t.Errorf("base %q: Issuer = %q, want SaaS default", base, cfg.Issuer)
		}
	}
}

func TestLoadOIDCConfigFromEnv_GitLab_OperatorOverride(t *testing.T) {
	t.Setenv(ci.EnvOIDCTrustGitLab, "https://gitlab.acme.internal")
	t.Setenv(ci.EnvOIDCAudienceGitLab, "custom-aud")

	cfg, err := ci.LoadOIDCConfigFromEnv(ci.ProviderGitLab, ci.OIDCConfig{
		GitLabBaseURL: "https://gitlab.acme.internal",
	})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if cfg.Issuer != "https://gitlab.acme.internal" {
		t.Errorf("Issuer = %q, want operator override", cfg.Issuer)
	}
	if cfg.Audience != "custom-aud" {
		t.Errorf("Audience = %q, want operator override", cfg.Audience)
	}
}

func TestLoadOIDCConfigFromEnv_GitLab_DisabledLiteral_FallsBackToTier2(t *testing.T) {
	t.Setenv(ci.EnvOIDCTrustGitLab, "disabled")

	cfg, err := ci.LoadOIDCConfigFromEnv(ci.ProviderGitLab, ci.OIDCConfig{
		GitLabBaseURL: "https://gitlab.com",
	})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if !cfg.Disabled {
		t.Errorf("expected Disabled=true when env=disabled")
	}
}

func TestLoadOIDCConfigFromEnv_OperatorOnlyProviders_NoDefaults(t *testing.T) {
	for _, tc := range []struct {
		provider ci.Provider
		envTrust string
	}{
		{ci.ProviderJenkins, ci.EnvOIDCTrustJenkins},
		{ci.ProviderBuildkite, ci.EnvOIDCTrustBuildkite},
		{ci.ProviderCircleCI, ci.EnvOIDCTrustCircleCI},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Setenv(tc.envTrust, "")
			cfg, err := ci.LoadOIDCConfigFromEnv(tc.provider, ci.OIDCConfig{})
			if err != nil {
				t.Fatalf("LoadOIDCConfigFromEnv(%s): %v", tc.provider, err)
			}
			if !cfg.Disabled {
				t.Errorf("%s without operator override should be Disabled (Q6: operator-only); got Disabled=false issuer=%q", tc.provider, cfg.Issuer)
			}
		})
	}
}

func TestLoadOIDCConfigFromEnv_OperatorOnlyProviders_AcceptOverride(t *testing.T) {
	for _, tc := range []struct {
		provider ci.Provider
		envTrust string
		envAud   string
		issuer   string
	}{
		{ci.ProviderJenkins, ci.EnvOIDCTrustJenkins, ci.EnvOIDCAudienceJenkins, "https://jenkins.acme.internal"},
		{ci.ProviderBuildkite, ci.EnvOIDCTrustBuildkite, ci.EnvOIDCAudienceBuildkite, "https://agent.buildkite.com"},
		{ci.ProviderCircleCI, ci.EnvOIDCTrustCircleCI, ci.EnvOIDCAudienceCircleCI, "https://oidc.circleci.com/org/orgid"},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Setenv(tc.envTrust, tc.issuer)
			t.Setenv(tc.envAud, "cordum-edge")
			cfg, err := ci.LoadOIDCConfigFromEnv(tc.provider, ci.OIDCConfig{})
			if err != nil {
				t.Fatalf("LoadOIDCConfigFromEnv(%s): %v", tc.provider, err)
			}
			if cfg.Disabled {
				t.Errorf("%s with operator override should have OIDC enabled; got Disabled=true", tc.provider)
			}
			if !strings.EqualFold(cfg.Issuer, tc.issuer) {
				t.Errorf("%s Issuer = %q, want %q", tc.provider, cfg.Issuer, tc.issuer)
			}
		})
	}
}

func TestNewOIDCClaimsProvider_RequiresIssuerAudienceTokenProvider(t *testing.T) {
	if _, err := ci.NewOIDCClaimsProvider(ci.OIDCVerifierConfig{}); err == nil {
		t.Errorf("expected error for empty issuer")
	}
	if _, err := ci.NewOIDCClaimsProvider(ci.OIDCVerifierConfig{Issuer: "https://gitlab.com"}); err == nil {
		t.Errorf("expected error for empty audience")
	}
	if _, err := ci.NewOIDCClaimsProvider(ci.OIDCVerifierConfig{Issuer: "https://gitlab.com", Audience: "cordum-edge"}); err == nil {
		t.Errorf("expected error for nil token provider")
	}
}
