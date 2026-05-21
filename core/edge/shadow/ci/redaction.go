package ci

import (
	"regexp"
	"sort"
	"strings"

	"github.com/cordum/cordum/core/edge/shadow"
)

// secretLikePatterns mirrors the github detector's regex set. Order
// matters — longest / most-specific first so partial matches inside
// longer markers are not produced. Kept local to the package (rather
// than re-using shadow.StripSecretMarkers verbatim) so CI-specific
// shapes can be appended without touching the cross-cutting shadow
// redactor.
var secretLikePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{16,}`), // GitLab personal access token
	regexp.MustCompile(`xoxb-[A-Za-z0-9\-]{16,}`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-_\.]{8,}`),
}

// providerScheme maps the Provider enum to a URI scheme used for the
// emitted RedactedPath. The scheme + repo identifier lets SIEM filters
// pivot between findings by provider without parsing the path further.
var providerScheme = map[Provider]string{
	ProviderGitLab:    "gitlab://",
	ProviderJenkins:   "jenkins://",
	ProviderBuildkite: "buildkite://",
	ProviderCircleCI:  "circleci://",
}

// RedactCIPath returns a path safe to record in an audit finding.
// Query strings are stripped (they routinely carry credentials), the
// path is run through shadow.RedactPath for cross-platform safety, and
// the result is prefixed with the provider's URI scheme + repo
// identifier so downstream consumers can pivot per provider.
func RedactCIPath(p Provider, repoFull, rawPath string) string {
	path := stripURLQuery(rawPath)
	path = strings.TrimLeft(shadow.RedactPath(path), "/")
	if path == "" {
		return ""
	}
	scheme := providerScheme[p]
	if scheme == "" {
		scheme = "ci://"
	}
	return scheme + SanitizeEvidenceText(repoFull, 128) + "/" + path
}

// stripURLQuery removes everything from the first `?` or `#` onwards.
// CI paths sometimes include query fragments when fetched via raw-file
// HTTP endpoints — those fragments occasionally carry private_token
// query parameters.
func stripURLQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// SanitizeEvidenceText normalises a free-text evidence blob: control
// characters stripped, whitespace collapsed, secret-shape tokens
// redacted, length capped. Mirrors the github detector's helper so
// CI findings carry the same redaction posture.
func SanitizeEvidenceText(raw string, limit int) string {
	s := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, raw)
	for _, re := range secretLikePatterns {
		s = re.ReplaceAllString(s, "<REDACTED>")
	}
	s = strings.Join(strings.Fields(s), " ")
	if limit > 0 && len(s) > limit {
		const suffix = "…"
		if limit <= len(suffix) {
			return s[:limit]
		}
		return s[:limit-len(suffix)] + suffix
	}
	return s
}

// SanitizeEnvKeys takes a slice of env-var names, drops blanks, dedups,
// caps each to 64 bytes, and returns a sorted slice. Values are never
// touched — by contract this function only sees NAMES.
func SanitizeEnvKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if clean := SanitizeEvidenceText(k, 64); clean != "" {
			out = append(out, clean)
		}
	}
	return sortedUnique(out)
}

// sortedUnique returns the de-duplicated, sorted set of non-empty
// values in `in`. Used for stable evidence-summary segments so
// findings hash identically across re-runs.
func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// capEvidenceSummary applies a final defense-in-depth secret strip via
// shadow.StripSecretMarkers (which catches base64/ROT13 obfuscated
// shapes the local regex set misses) and bounds output at the store's
// MaxEvidenceSummaryBytes ceiling. The shadow store runs the same
// strip on write; running it client-side keeps audit logs and slog
// emits secret-free before the store touches them.
func capEvidenceSummary(raw string) string {
	clean := shadow.StripSecretMarkers(raw)
	const suffix = " …truncated"
	if len(clean) <= shadow.MaxEvidenceSummaryBytes {
		return clean
	}
	return clean[:shadow.MaxEvidenceSummaryBytes-len(suffix)] + suffix
}
