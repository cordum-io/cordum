package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOIDC_RejectsMetadataHostInAllEnvs locks the PR #276 audit fix:
// validateOIDCURL MUST refuse `metadata.google.internal` (and other
// cloud-metadata hostnames that resolve to link-local) in ALL
// environments unless CORDUM_OIDC_ALLOW_PRIVATE=true. Pre-fix the
// `ensurePublicHost` check was gated on env.IsProduction(), letting
// dev/staging deployments accept an OIDC issuer URL that pointed at
// the GCP instance-metadata service and leaked the discovery + JWKS
// fetch to the metadata IP at refresh time.
func TestOIDC_RejectsMetadataHostInAllEnvs(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "")
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "")

	cases := []string{
		"http://metadata.google.internal/.well-known/openid-configuration",
		"https://metadata.google.internal/",
		"http://169.254.169.254/",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := validateOIDCURL(raw)
			if err == nil {
				t.Fatalf("validateOIDCURL(%q) err=nil in non-prod; want rejection (DNS rebinding / cloud metadata SSRF)", raw)
			}
			// Any of: explicit "not allowed" (IP resolves private), DNS
			// "resolve failed" (fail-closed when host doesn't resolve),
			// or "must use https" (scheme guard). The point: non-prod
			// must NOT silently accept the metadata host.
			msg := err.Error()
			ok := strings.Contains(msg, "not allowed") ||
				strings.Contains(msg, "resolve failed") ||
				strings.Contains(msg, "must use https")
			if !ok {
				t.Fatalf("error %q does not show ensurePublicHost / scheme enforcement in non-prod", err)
			}
		})
	}
}

// TestOIDC_AllowsPrivateHostWhenOptedIn is the regression control: the
// dev/test escape hatch CORDUM_OIDC_ALLOW_PRIVATE=true continues to
// permit localhost/private issuers so existing test fixtures (httptest
// servers) keep working without ceremony.
func TestOIDC_AllowsPrivateHostWhenOptedIn(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "")
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")

	if _, err := validateOIDCURL("http://127.0.0.1:8080/issuer"); err != nil {
		t.Fatalf("CORDUM_OIDC_ALLOW_PRIVATE=true should permit loopback: %v", err)
	}
}

// TestNewOIDCHTTPClient_GuardsPrivateIPsAtConnect verifies the second
// layer of SSRF defense: even after validateOIDCURL passes (e.g.
// legitimate public hostname at registration), the HTTP client used
// for discovery + JWKS refresh MUST resolve the host at connect time
// and refuse connections to private/loopback/link-local addresses.
// Blocks DNS-rebinding attacks where the operator's issuer hostname
// is re-pointed at 169.254.169.254 between discovery and refresh.
func TestNewOIDCHTTPClient_GuardsPrivateIPsAtConnect(t *testing.T) {
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := newOIDCHTTPClient()
	resp, err := client.Get(srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("Get(loopback) err=nil; want connect-time refusal (DNS rebinding bypass)")
	}
	if !strings.Contains(err.Error(), "private") &&
		!strings.Contains(err.Error(), "loopback") &&
		!strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error %q does not cite private/loopback IP rejection", err)
	}
}

// TestNewOIDCHTTPClient_AllowsPrivateWhenOptedIn ensures
// CORDUM_OIDC_ALLOW_PRIVATE=true opens the dialer for the dev/test
// escape hatch (parallels validateOIDCURL behavior).
func TestNewOIDCHTTPClient_AllowsPrivateWhenOptedIn(t *testing.T) {
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := newOIDCHTTPClient()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("ALLOW_PRIVATE=true should permit loopback dial: %v", err)
	}
	_ = resp.Body.Close()
}
