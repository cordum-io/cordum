package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
)

type authConfigStatusResponse struct {
	PasswordEnabled bool   `json:"password_enabled"`
	UserAuthEnabled bool   `json:"user_auth_enabled"`
	SAMLEnabled     bool   `json:"saml_enabled"`
	SAMLEnterprise  bool   `json:"saml_enterprise"`
	SAMLLoginURL    string `json:"saml_login_url"`
	SAMLMetadataURL string `json:"saml_metadata_url"`
	SessionTTL      string `json:"session_ttl"`
	DefaultTenant   string `json:"default_tenant"`
	OIDCEnabled     bool   `json:"oidc_enabled"`
	OIDCIssuer      string `json:"oidc_issuer"`
}

func runAuthCmd(args []string) {
	if len(args) < 1 {
		authUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "sso":
		runAuthSSOCmd(args[1:])
	default:
		authUsage()
		os.Exit(1)
	}
}

func runAuthSSOCmd(args []string) {
	if len(args) < 1 || args[0] != "status" {
		authUsage()
		os.Exit(1)
	}
	if err := runAuthSSOStatusE(args[1:]); err != nil {
		fail(err.Error())
	}
}

func authUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl auth <command>

Commands:
  sso status [--json]                            Show SSO/SAML runtime status from /api/v1/auth/config`)
}

func runAuthSSOStatusE(args []string) error {
	fs := newFlagSet("auth sso status")
	jsonOutput := fs.Bool("json", false, "emit raw auth/config JSON")
	fs.ParseArgs(args)

	client := restClientFromFlags(fs)
	ctx := context.Background()

	var cfg authConfigStatusResponse
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/auth/config", nil, &cfg); err != nil {
		return err
	}

	if *jsonOutput {
		printJSON(cfg)
		return nil
	}

	printAuthSSOStatus(os.Stdout, strings.TrimRight(*fs.gateway, "/"), cfg)
	return nil
}

func printAuthSSOStatus(w *os.File, gateway string, cfg authConfigStatusResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FIELD	VALUE")
	_, _ = fmt.Fprintln(tw, "Gateway	"+valueOrDash(gateway))
	_, _ = fmt.Fprintln(tw, "Default tenant	"+valueOrDash(cfg.DefaultTenant))
	_, _ = fmt.Fprintln(tw, "Password auth	"+formatEnabled(cfg.PasswordEnabled || cfg.UserAuthEnabled))
	_, _ = fmt.Fprintln(tw, "OIDC	"+formatEnabled(cfg.OIDCEnabled))
	_, _ = fmt.Fprintln(tw, "SAML feature tier	"+formatEnabled(cfg.SAMLEnterprise))
	_, _ = fmt.Fprintln(tw, "SAML runtime	"+samlRuntimeState(cfg))
	_, _ = fmt.Fprintln(tw, "Metadata URL	"+valueOrDash(strings.TrimSpace(cfg.SAMLMetadataURL)))
	_, _ = fmt.Fprintln(tw, "Login URL	"+valueOrDash(strings.TrimSpace(cfg.SAMLLoginURL)))
	_, _ = fmt.Fprintln(tw, "Session TTL	"+valueOrDash(strings.TrimSpace(cfg.SessionTTL)))
	_ = tw.Flush()
}

func samlRuntimeState(cfg authConfigStatusResponse) string {
	switch {
	case cfg.SAMLEnabled:
		return "enabled"
	case strings.TrimSpace(cfg.SAMLLoginURL) != "" || strings.TrimSpace(cfg.SAMLMetadataURL) != "":
		return "configured but gated"
	default:
		return "disabled"
	}
}
