package secrets

import (
	"net/url"
	"strings"
)

// SecretRef is a parsed secret:// URI pointing to a value in an external
// secrets manager.  The Provider field selects the backend ("vault",
// "aws-sm", "k8s"), Path encodes the backend-specific address (e.g. a
// Vault KV path or an AWS SecretId), and Key is an optional JSON field
// selector extracted from the URI fragment.
//
// Examples:
//
//	secret://vault/database/creds#password
//	secret://aws-sm/prod/api-key
//	secret://k8s/default/my-secret#token
type SecretRef struct {
	Provider string // backend identifier: "vault", "aws-sm", "k8s"
	Path     string // provider-specific path (no leading slash)
	Key      string // optional field within the secret (from URI fragment)
	Raw      string // original URI string
}

// ParseSecretRef parses a secret:// URI into a SecretRef.
//
// The URI format is:
//
//	secret://<provider>/<path>[#<key>]
//
// Returns (ref, true) for valid secret URIs with a non-empty provider and
// path.  Returns (SecretRef{}, false) for anything else — callers should
// not treat the result as an error, just as "this string is not a secret
// reference".
func ParseSecretRef(s string) (SecretRef, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, secretPrefix) {
		return SecretRef{}, false
	}

	// Use net/url for robust parsing.  The secret:// scheme maps cleanly
	// onto a hierarchical URI: scheme=secret, host=provider, path=/<path>.
	u, err := url.Parse(s)
	if err != nil {
		return SecretRef{}, false
	}

	provider := u.Host
	if provider == "" {
		return SecretRef{}, false
	}

	// Trim the leading "/" that url.Parse adds to the path.
	path := strings.TrimPrefix(u.Path, "/")
	if path == "" {
		return SecretRef{}, false
	}

	return SecretRef{
		Provider: provider,
		Path:     path,
		Key:      u.Fragment,
		Raw:      s,
	}, true
}

// IsSecretRef is a convenience predicate that returns true when s is a
// syntactically valid secret:// URI.  It does not validate that the
// referenced provider is registered or that the path exists.
func IsSecretRef(s string) bool {
	_, ok := ParseSecretRef(s)
	return ok
}
