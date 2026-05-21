package ci_test

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow/ci"
)

func TestRedactCIPath_StripsQueryString(t *testing.T) {
	got := ci.RedactCIPath(ci.ProviderGitLab, "acme/web", "/some/path?token=ohnoitsasecret")
	if strings.Contains(got, "token=") {
		t.Errorf("query string leaked: %q", got)
	}
	if strings.Contains(got, "ohnoitsasecret") {
		t.Errorf("query value leaked: %q", got)
	}
}

func TestRedactCIPath_EmitsProviderSchemeAndRepo(t *testing.T) {
	for _, tc := range []struct {
		provider ci.Provider
		prefix   string
	}{
		{ci.ProviderGitLab, "gitlab://"},
		{ci.ProviderJenkins, "jenkins://"},
		{ci.ProviderBuildkite, "buildkite://"},
		{ci.ProviderCircleCI, "circleci://"},
	} {
		got := ci.RedactCIPath(tc.provider, "acme/web", ".gitlab-ci.yml")
		if !strings.HasPrefix(got, tc.prefix) {
			t.Errorf("provider %s: path %q missing %q prefix", tc.provider, got, tc.prefix)
		}
		if !strings.Contains(got, "acme/web") {
			t.Errorf("provider %s: path %q missing repo", tc.provider, got)
		}
	}
}

func TestSanitizeEvidenceText_RedactsSecretLikeTokens(t *testing.T) {
	for _, in := range []string{
		"export ANTHROPIC_API_KEY=cordum_fake_sk-ant-fakefakefakefake123456789012",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
		"ghp_testAAAAAAAAAAAAAAAA",
	} {
		got := ci.SanitizeEvidenceText(in, 256)
		if strings.Contains(got, "sk-ant-") && strings.Contains(got, "fakefake") {
			t.Errorf("input %q: secret-shape pattern leaked: %q", in, got)
		}
		if strings.Contains(got, "Bearer abcdef") {
			t.Errorf("input %q: Bearer token leaked: %q", in, got)
		}
		if strings.Contains(got, "ghp_AAAAAAAA") {
			t.Errorf("input %q: ghp_ token leaked: %q", in, got)
		}
	}
}

func TestSanitizeEvidenceText_BoundsLength(t *testing.T) {
	huge := strings.Repeat("abcd", 1000)
	got := ci.SanitizeEvidenceText(huge, 64)
	if len(got) > 64 {
		t.Errorf("expected output <=64 bytes, got %d", len(got))
	}
}

func TestSanitizeEnvKeys_DedupesAndCaps(t *testing.T) {
	keys := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", strings.Repeat("X", 200)}
	got := ci.SanitizeEnvKeys(keys)
	seen := make(map[string]int)
	for _, k := range got {
		seen[k]++
	}
	if seen["ANTHROPIC_API_KEY"] != 1 {
		t.Errorf("dup not collapsed: %v", got)
	}
	for _, k := range got {
		if len(k) > 64 {
			t.Errorf("env key not capped at 64 bytes: %q (%d bytes)", k, len(k))
		}
	}
}
