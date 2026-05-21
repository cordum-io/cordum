package github

import (
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	gogithub "github.com/google/go-github/v74/github"

	"github.com/cordum/cordum/core/edge/shadow"
)

var secretLikePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-_\.]{8,}`),
}

var safeHostName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

func redactedConfigSummary(file *gogithub.RepositoryContent) string {
	if file == nil {
		return ""
	}
	content, err := file.GetContent()
	if err != nil || strings.TrimSpace(content) == "" {
		return "config present"
	}
	return sanitizeEvidenceText(shadow.RedactConfigSummary([]byte(content)), 256)
}

func redactCIPath(repoFull, rawPath string) string {
	path := stripURLQuery(rawPath)
	path = strings.TrimLeft(shadow.RedactPath(path), "/")
	if path == "" {
		return ""
	}
	return "github://" + sanitizeEvidenceText(repoFull, 128) + "/" + path
}

func sanitizeProviderHosts(rawHosts, allowlist []string) []string {
	allowed := make(map[string]bool, len(allowlist))
	for _, h := range allowlist {
		if host := sanitizeHost(h); host != "" {
			allowed[host] = true
		}
	}
	out := make([]string, 0, len(rawHosts))
	for _, h := range rawHosts {
		host := sanitizeHost(h)
		if host != "" && allowed[host] {
			out = append(out, host)
		}
	}
	return sortedUnique(out)
}

func safeRunRepo(run *gogithub.WorkflowRun) string {
	if run == nil || run.GetRepository() == nil {
		return ""
	}
	return sanitizeEvidenceText(run.GetRepository().GetFullName(), 128)
}

func sanitizeEnvKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if clean := sanitizeEvidenceText(k, 64); clean != "" {
			out = append(out, clean)
		}
	}
	return sortedUnique(out)
}

func sanitizeHost(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			s = u.Host
		}
	}
	s = stripURLQuery(s)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.Trim(s, " .")
	if !safeHostName.MatchString(s) || strings.Contains(s, "..") {
		return ""
	}
	return s
}

func stripURLQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func sanitizeEvidenceText(raw string, limit int) string {
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
		return s[:limit-len("…")] + "…"
	}
	return s
}

func sanitizeDegradedError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeEvidenceText(err.Error(), 160)
}

func capEvidenceSummary(raw string) string {
	const suffix = " …truncated"
	if len(raw) <= shadow.MaxEvidenceSummaryBytes {
		return raw
	}
	return raw[:shadow.MaxEvidenceSummaryBytes-len(suffix)] + suffix
}

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
