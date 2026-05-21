package shadow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// summaryByteCap bounds the size of RedactConfigSummary output regardless
// of input size, so a malicious oversized config can never blow up SIEM
// ingestion (matches task rail #1 'do not over-collect private data').
const (
	summaryByteCap = 2048

	// encodedSecretCandidateMinBytes avoids decoding short ordinary words.
	encodedSecretCandidateMinBytes = 16
	// encodedSecretCandidateMaxBytes bounds attacker-controlled decode CPU/memory.
	encodedSecretCandidateMaxBytes = 4096
)

// secretMarkerPatterns matches values whose shape suggests live credentials.
// The patterns are intentionally aggressive — false positives are a
// privacy win, false negatives are a privacy loss. Order: longest /
// most-specific first so partial matches inside longer markers are not
// produced.
var secretMarkerPatterns = []*regexp.Regexp{
	// PGP / OpenSSH key blocks (whole header line).
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY[-A-Z]*-----`),
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+CERTIFICATE-----`),
	// Anthropic, OpenAI, GitHub, etc. token shapes — alnum bodies of >=16
	// chars after a recognised prefix.
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`xoxb-[A-Za-z0-9\-]{16,}`),
	// HTTP Authorization headers with bearer tokens.
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-_\.]{8,}`),
}

var (
	base64SecretCandidatePattern = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}`)
	homoglyphHyphenReplacer      = strings.NewReplacer(
		"\u2010", "-",
		"\u2011", "-",
		"\u2012", "-",
		"\u2013", "-",
		"\u2014", "-",
		"\uff0d", "-",
	)
	sensitiveMetadataKeyReplacer = strings.NewReplacer("-", "_", ".", "_", " ", "_")
	sensitiveMetadataKeyMarkers  = []string{
		"authorization",
		"bearer",
		"token",
		"secret",
		"api_key",
		"apikey",
		"private_key",
		"credential",
		"password",
	}
)

// RedactPath returns a path safe to record in an audit finding. It strips
// the developer's home prefix (cross-platform), strips drive letters, and
// never returns an absolute filesystem path.
func RedactPath(path string) string {
	if path == "" {
		return ""
	}
	// Canonicalise separators so the rest of the function is one branch.
	// filepath.ToSlash only swaps the host's separator, so on Linux CI a
	// Windows path like `C:\Users\yaron\...` still contains backslashes —
	// strip them explicitly so the same input redacts to the same shape
	// regardless of which platform the redactor runs on.
	slashed := strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)

	// Strip a Windows drive letter (e.g. "C:/Users/...") up to and
	// including the slash that follows it.
	if len(slashed) >= 3 && slashed[1] == ':' && slashed[2] == '/' {
		slashed = slashed[3:]
	} else if len(slashed) >= 2 && slashed[1] == ':' {
		// Bare drive letter without separator — treat the rest as path.
		slashed = slashed[2:]
	}

	// Recognise the three platform "users" prefixes and replace with "~/".
	for _, prefix := range []string{"/home/", "/Users/", "Users/", "home/"} {
		if idx := strings.Index(slashed, prefix); idx == 0 || (idx == 1 && slashed[0] == '/') {
			rest := slashed[idx+len(prefix):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				return "~/" + rest[slash+1:]
			}
			return "~/"
		}
	}

	// Already-relative paths pass through as-is.
	if !strings.HasPrefix(slashed, "/") {
		return slashed
	}
	// Last resort: strip the leading slash so we never emit an absolute
	// system path. Operators can correlate via the dirname tail.
	return strings.TrimPrefix(slashed, "/")
}

// RedactConfigSummary returns a one-line, secret-free summary of an MCP-
// client configuration. It accepts either JSON (Claude Code / Cursor) or
// TOML-ish (Codex) shapes; unknown formats degrade to a structural
// summary that records nothing of substance. The output is guaranteed to
// be ≤summaryByteCap and to never contain a value that matches any
// secretMarkerPatterns regex.
func RedactConfigSummary(content []byte) string {
	if len(content) == 0 {
		return "empty config"
	}

	servers, transports, hosts := extractMCPSummary(content)

	if len(servers) == 0 {
		// Unknown shape but non-empty — emit a bare structural fingerprint
		// so the finding is still informative.
		return capSummary(fmt.Sprintf("config present (%d bytes; no recognised mcp-server entries)", len(content)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d mcp servers configured", len(servers))
	if len(transports) > 0 {
		fmt.Fprintf(&b, " (transports: %s", strings.Join(sortedSet(transports), ", "))
	}
	if len(hosts) > 0 {
		if len(transports) > 0 {
			b.WriteString("; ")
		} else {
			b.WriteString(" (")
		}
		fmt.Fprintf(&b, "hosts: %s", strings.Join(sortedSet(hosts), ", "))
	}
	if len(transports) > 0 || len(hosts) > 0 {
		b.WriteString(")")
	}

	out := b.String()
	// Defence-in-depth: even though the extractor only reads structural
	// fields, regex-strip every secretMarkerPatterns match before emit.
	out = stripSecretMarkers(out)
	return capSummary(out)
}

// extractMCPSummary inspects a config blob and returns the set of mcp
// server names + transports + endpoint hostnames it can confidently
// identify. It deliberately does NOT capture command lines, env values,
// auth tokens, paths, or prompt content.
func extractMCPSummary(content []byte) (servers, transports, hosts []string) {
	// Try JSON first — most-common shape.
	var jsonCfg map[string]any
	if err := json.Unmarshal(content, &jsonCfg); err == nil {
		if raw, ok := jsonCfg["mcpServers"]; ok {
			servers, transports, hosts = readMCPServerBag(raw)
			return
		}
		if raw, ok := jsonCfg["mcp_servers"]; ok {
			servers, transports, hosts = readMCPServerBag(raw)
			return
		}
	}

	// Fall back to a minimal TOML scrape — Codex config uses TOML; rather
	// than pull a TOML parser into this package's defence-in-depth path,
	// detect `[mcp_servers.<name>]` section headers and `transport = "..."`
	// values via regex. This is intentionally loose — anything we miss
	// degrades to a less-informative summary, not a privacy leak.
	tomlSection := regexp.MustCompile(`(?m)^\[mcp_servers\.([^\]]+)\]`)
	for _, m := range tomlSection.FindAllStringSubmatch(string(content), -1) {
		servers = append(servers, m[1])
	}
	tomlTransport := regexp.MustCompile(`(?m)^\s*transport\s*=\s*"([A-Za-z0-9_\-]+)"`)
	for _, m := range tomlTransport.FindAllStringSubmatch(string(content), -1) {
		transports = append(transports, m[1])
	}
	tomlEndpoint := regexp.MustCompile(`(?m)^\s*endpoint\s*=\s*"([^"]+)"`)
	for _, m := range tomlEndpoint.FindAllStringSubmatch(string(content), -1) {
		if h := hostFromURL(m[1]); h != "" {
			hosts = append(hosts, h)
		}
	}
	return
}

// readMCPServerBag pulls server-name keys + transport + endpoint host
// values out of a JSON mcpServers object. Other fields are ignored — by
// design we never look at command/args/env, those are the high-leak
// surfaces.
func readMCPServerBag(raw any) (servers, transports, hosts []string) {
	bag, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for name, entry := range bag {
		servers = append(servers, name)
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := obj["transport"].(string); ok && t != "" {
			transports = append(transports, t)
		}
		if ep, ok := obj["endpoint"].(string); ok {
			if h := hostFromURL(ep); h != "" {
				hosts = append(hosts, h)
			}
		}
	}
	return
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host != "" {
		return u.Hostname()
	}
	return ""
}

func sortedSet(in []string) []string {
	seen := map[string]struct{}{}
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

func stripSecretMarkers(s string) string {
	s = normalizeHomoglyphHyphens(s)
	s = replaceSecretMarkers(s)
	s = redactROT13SecretMarkers(s)
	s = redactBase64SecretMarkers(s)
	return s
}

// StripSecretMarkers is the exported entry point for the encoding-aware
// secret redactor. Callers in sibling subpackages (e.g.
// core/edge/shadow/k8s) compose this primitive instead of duplicating
// the regex + homoglyph + ROT13 + base64 pipeline. Returns s with every
// recognised credential shape replaced by "<REDACTED>".
func StripSecretMarkers(s string) string {
	return stripSecretMarkers(s)
}

// sanitizeFindingMetadata is the store-boundary guard for free-form finding
// metadata. Sensitive key names are rejected entirely; safe keys remain
// available while values pass through the shared secret-shape redactor.
func sanitizeFindingMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for key, value := range m {
		if isSensitiveMetadataKey(key) {
			continue
		}
		out[key] = StripSecretMarkers(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSensitiveMetadataKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	normalized := strings.ToLower(sensitiveMetadataKeyReplacer.Replace(key))
	for _, marker := range sensitiveMetadataKeyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeHomoglyphHyphens(s string) string {
	if !strings.ContainsAny(s, "\u2010\u2011\u2012\u2013\u2014\uff0d") {
		return s
	}
	return homoglyphHyphenReplacer.Replace(s)
}

func replaceSecretMarkers(s string) string {
	for _, re := range secretMarkerPatterns {
		s = re.ReplaceAllString(s, "<REDACTED>")
	}
	return s
}

func redactROT13SecretMarkers(s string) string {
	return replaceByteRanges(s, secretMarkerRanges(rot13ASCII(s)))
}

func rot13ASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune('A' + (r-'A'+13)%26)
		case r >= 'a' && r <= 'z':
			b.WriteRune('a' + (r-'a'+13)%26)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func redactBase64SecretMarkers(s string) string {
	matches := base64SecretCandidatePattern.FindAllStringIndex(s, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][0], matches[i][1]
		token := s[start:end]
		if len(token) < encodedSecretCandidateMinBytes || len(token) > encodedSecretCandidateMaxBytes {
			continue
		}
		if base64TokenContainsSecret(token) {
			s = s[:start] + "<REDACTED>" + s[end:]
		}
	}
	return s
}

func base64TokenContainsSecret(token string) bool {
	// False-positive redaction is safer than leaking, but decoding must stay
	// bounded: malformed and oversized attacker strings are skipped.
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded, err := enc.DecodeString(token)
		if err == nil && hasSecretMarker(string(decoded)) {
			return true
		}
	}
	return false
}

func hasSecretMarker(s string) bool {
	s = normalizeHomoglyphHyphens(s)
	for _, re := range secretMarkerPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

type byteRange struct {
	start int
	end   int
}

func secretMarkerRanges(s string) []byteRange {
	ranges := []byteRange{}
	for _, re := range secretMarkerPatterns {
		for _, match := range re.FindAllStringIndex(s, -1) {
			candidate := byteRange{start: match[0], end: match[1]}
			if !overlapsAny(candidate, ranges) {
				ranges = append(ranges, candidate)
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start > ranges[j].start })
	return ranges
}

func overlapsAny(candidate byteRange, ranges []byteRange) bool {
	for _, existing := range ranges {
		if candidate.start < existing.end && existing.start < candidate.end {
			return true
		}
	}
	return false
}

func replaceByteRanges(s string, ranges []byteRange) string {
	for _, r := range ranges {
		if r.start >= 0 && r.start <= r.end && r.end <= len(s) {
			s = s[:r.start] + "<REDACTED>" + s[r.end:]
		}
	}
	return s
}

func capSummary(s string) string {
	if len(s) > summaryByteCap {
		return s[:summaryByteCap-len(" …truncated")] + " …truncated"
	}
	return s
}
