package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"

	gogithub "github.com/google/go-github/v74/github"
)

// OIDCVerifierConfig configures the go-oidc-backed verifier used for
// GitHub Actions trust-root validation.
type OIDCVerifierConfig struct {
	Issuer        string
	Audience      string
	TokenProvider OIDCTokenProvider
}

type oidcVerifier struct {
	issuer        string
	audience      string
	tokenProvider OIDCTokenProvider

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewOIDCClaimsProvider returns an OIDCClaimsProvider that validates JWT
// signature, issuer, expiry, and audience before projecting GitHub claims.
func NewOIDCClaimsProvider(cfg OIDCVerifierConfig) (OIDCClaimsProvider, error) {
	v := &oidcVerifier{
		issuer:        strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/"),
		audience:      strings.TrimSpace(cfg.Audience),
		tokenProvider: cfg.TokenProvider,
	}
	if v.issuer == "" {
		return nil, errors.New("github detector: OIDC issuer is required")
	}
	if v.audience == "" {
		return nil, errors.New("github detector: OIDC audience is required")
	}
	if v.tokenProvider == nil {
		return nil, errors.New("github detector: OIDC token provider is required")
	}
	return v.Claims, nil
}

func (v *oidcVerifier) Claims(ctx context.Context, run *gogithub.WorkflowRun) (*OIDCClaims, error) {
	raw, err := v.tokenProvider(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("oidc token provider: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("oidc token provider returned empty token")
	}
	verifier, err := v.idTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	var extra oidcGitHubClaims
	if err := idToken.Claims(&extra); err != nil {
		return nil, fmt.Errorf("oidc claims: %w", err)
	}
	repo := strings.TrimSpace(extra.Repository)
	if repo == "" {
		owner, name := parseOIDCRepo(&OIDCClaims{Subject: idToken.Subject})
		repo = joinRepo(owner, name)
	}
	return &OIDCClaims{
		Subject:   strings.TrimSpace(idToken.Subject),
		Repo:      repo,
		Ref:       strings.TrimSpace(extra.Ref),
		Actor:     strings.TrimSpace(extra.Actor),
		Audience:  strings.Join(idToken.Audience, ","),
		Audiences: append([]string(nil), idToken.Audience...),
		Issuer:    strings.TrimRight(strings.TrimSpace(idToken.Issuer), "/"),
	}, nil
}

func (v *oidcVerifier) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	provider, err := oidc.NewProvider(ctx, v.issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %q: %w", v.issuer, err)
	}
	v.verifier = provider.VerifierContext(ctx, &oidc.Config{ClientID: v.audience})
	return v.verifier, nil
}

type oidcGitHubClaims struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Actor      string `json:"actor"`
}

func joinRepo(owner, name string) string {
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}
