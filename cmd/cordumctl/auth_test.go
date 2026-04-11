package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunAuthSSOStatusShowsPublishedEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/config" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"password_enabled":true,
			"user_auth_enabled":true,
			"saml_enabled":true,
			"saml_enterprise":true,
			"saml_login_url":"https://gateway.cordum.test/api/v1/auth/sso/saml/login",
			"saml_metadata_url":"https://gateway.cordum.test/api/v1/auth/sso/saml/metadata",
			"session_ttl":"12h",
			"default_tenant":"default",
			"oidc_enabled":false
		}`))
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runAuthSSOStatusE([]string{"--gateway", srv.URL}); err != nil {
			t.Fatalf("runAuthSSOStatusE() error = %v", err)
		}
	})

	for _, want := range []string{"SAML runtime", "enabled", "Metadata URL", "Login URL", "12h"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected auth sso status output to contain %q, got %q", want, stdout)
		}
	}
}
