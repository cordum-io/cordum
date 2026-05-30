package mcp

import (
	"fmt"
	"os"
	"strings"
)

// upstreamSecretRefPrefix is the only accepted scheme for an upstream auth secret
// ref, mirroring edge.validateMCPUpstreamSecret which rejects inline tokens. The
// proxy never persists a literal credential — only this reference is stored in the
// UpstreamRegistry; the value is resolved at dial time.
const upstreamSecretRefPrefix = "secret://"

// SecretResolver resolves a `secret://<name>` reference to a credential string
// (used verbatim as the upstream Authorization header). It is injected so the
// proxy embeds no tokens and tests can stub it; returning an error fails the dial
// closed rather than dialing with an empty credential.
type SecretResolver func(ref string) (string, error)

// EnvSecretResolver returns a SecretResolver that maps `secret://<name>`
// references to environment-variable values. refToEnv supplies explicit
// ref->ENVVAR overrides (e.g. "secret://monday-token" -> "MONDAY_API_TOKEN");
// unmapped refs fall back to the convention secret://a-b -> A_B. A missing or
// empty variable fails closed so the proxy never dials with a blank credential.
func EnvSecretResolver(refToEnv map[string]string) SecretResolver {
	return func(ref string) (string, error) {
		key := strings.TrimSpace(ref)
		if !strings.HasPrefix(key, upstreamSecretRefPrefix) {
			return "", fmt.Errorf("mcp: auth secret ref must use %q scheme", upstreamSecretRefPrefix)
		}
		envVar := strings.TrimSpace(refToEnv[key])
		if envVar == "" {
			envVar = secretRefEnvName(key)
		}
		val := strings.TrimSpace(os.Getenv(envVar))
		if val == "" {
			return "", fmt.Errorf("mcp: secret %q unresolved: env %s is empty", key, envVar)
		}
		return val, nil
	}
}

// secretRefEnvName derives a conventional env-var name from a secret ref, e.g.
// secret://monday-token -> MONDAY_TOKEN.
func secretRefEnvName(ref string) string {
	name := strings.TrimPrefix(strings.TrimSpace(ref), upstreamSecretRefPrefix)
	name = strings.NewReplacer("-", "_", "/", "_", ".", "_", ":", "_").Replace(name)
	return strings.ToUpper(name)
}
