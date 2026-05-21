package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	gogithub "github.com/google/go-github/v74/github"

	ghdetector "github.com/cordum/cordum/core/edge/shadow/github"
)

func TestOIDCClaimsProvider_VerifiesSignedGitHubJWT(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	token := issuer.sign(t, "cordum-edge", time.Now().Add(time.Hour), issuer.key)
	provider := newOIDCProviderForToken(t, issuer.url, "cordum-edge", token)

	claims, err := provider(context.Background(), &gogithub.WorkflowRun{})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if claims.Subject != "repo:acme/web:ref:refs/heads/main" {
		t.Fatalf("Subject = %q, want GitHub Actions subject", claims.Subject)
	}
	if claims.Repo != "acme/web" || claims.Ref != "refs/heads/main" || claims.Actor != "alice" {
		t.Fatalf("projected claims = %#v", claims)
	}
}

func TestGHDetector_OIDC_TokenProviderVerifiedBeforeMapping(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	token := issuer.sign(t, "cordum-edge", time.Now().Add(time.Hour), issuer.key)
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCIssuer:   issuer.url,
		OIDCAudience: "cordum-edge",
		OIDCTokenProvider: func(context.Context, *gogithub.WorkflowRun) (string, error) {
			return token, nil
		},
	}
	f := newFixture(t, cfg)
	registerBasicRun(t, f, 2101, "acme", "web", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].PrincipalID != "repo:acme/web:ref:refs/heads/main" {
		t.Fatalf("PrincipalID = %q, want verified OIDC subject", findings[0].PrincipalID)
	}
	if findings[0].TenantSource != ghdetector.TenantSourceOIDC {
		t.Fatalf("TenantSource = %q, want oidc", findings[0].TenantSource)
	}
}

func TestOIDCClaimsProvider_RejectsSignatureExpiryAndAudience(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	otherKey := newRSAKey(t)
	cases := map[string]string{
		"bad_signature": issuer.sign(t, "cordum-edge", time.Now().Add(time.Hour), otherKey),
		"expired":       issuer.sign(t, "cordum-edge", time.Now().Add(-time.Hour), issuer.key),
		"bad_audience":  issuer.sign(t, "other-audience", time.Now().Add(time.Hour), issuer.key),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			provider := newOIDCProviderForToken(t, issuer.url, "cordum-edge", token)
			if _, err := provider(context.Background(), &gogithub.WorkflowRun{}); err == nil {
				t.Fatal("provider returned nil error for invalid token")
			}
		})
	}
}

func TestOIDCClaimsProvider_RejectsBadDiscoveryIssuer(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	provider := newOIDCProviderForToken(t, server.URL, "cordum-edge", "x.y.z")
	_, err := provider(context.Background(), &gogithub.WorkflowRun{})
	if err == nil || !strings.Contains(err.Error(), "oidc discovery") {
		t.Fatalf("error = %v, want discovery failure", err)
	}
}

type testOIDCIssuer struct {
	url string
	kid string
	key *rsa.PrivateKey
}

func newTestOIDCIssuer(t *testing.T) *testOIDCIssuer {
	t.Helper()
	issuer := &testOIDCIssuer{kid: "cordum-test-key", key: newRSAKey(t)}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", issuer.discovery)
	mux.HandleFunc("/keys", issuer.keys)
	server := httptest.NewServer(mux)
	issuer.url = server.URL
	t.Cleanup(server.Close)
	return issuer
}

func (i *testOIDCIssuer) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]interface{}{
		"issuer":                                i.url,
		"jwks_uri":                              i.url + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (i *testOIDCIssuer) keys(w http.ResponseWriter, _ *http.Request) {
	jwk := jose.JSONWebKey{Key: &i.key.PublicKey, KeyID: i.kid, Algorithm: "RS256", Use: "sig"}
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
}

func (i *testOIDCIssuer) sign(t *testing.T, aud string, exp time.Time, key *rsa.PrivateKey) string {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", i.kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, opts)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	token, err := josejwt.Signed(signer).Claims(josejwt.Claims{
		Issuer: i.url, Subject: "repo:acme/web:ref:refs/heads/main",
		Audience: josejwt.Audience{aud}, Expiry: josejwt.NewNumericDate(exp),
		IssuedAt: josejwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}).Claims(map[string]interface{}{
		"repository": "acme/web",
		"ref":        "refs/heads/main",
		"actor":      "alice",
	}).Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return token
}

func newOIDCProviderForToken(t *testing.T, issuer, aud, token string) ghdetector.OIDCClaimsProvider {
	t.Helper()
	provider, err := ghdetector.NewOIDCClaimsProvider(ghdetector.OIDCVerifierConfig{
		Issuer: issuer, Audience: aud,
		TokenProvider: func(context.Context, *gogithub.WorkflowRun) (string, error) {
			return token, nil
		},
	})
	if err != nil {
		t.Fatalf("NewOIDCClaimsProvider: %v", err)
	}
	return provider
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
