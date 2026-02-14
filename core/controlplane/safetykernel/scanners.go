package safetykernel

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	maxFindingsPerPattern = 10
	maxFindingsPerScanner = 32
)

// OutputScanner inspects output content and returns findings.
type OutputScanner interface {
	Name() string
	Scan(content []byte) []outputFinding
}

type outputFinding struct {
	Type           string
	Severity       string
	Detail         string
	Offset         int64
	Length         int64
	Scanner        string
	Confidence     float32
	MatchedPattern string
}

type regexPattern struct {
	Label      string
	Severity   string
	Pattern    string
	Expression *regexp.Regexp
	Confidence float32
}

type regexScanner struct {
	name        string
	findingType string
	patterns    []regexPattern
}

func newRegexScanner(name, findingType string, patterns []regexPattern) *regexScanner {
	return &regexScanner{name: name, findingType: findingType, patterns: patterns}
}

func (s *regexScanner) Name() string {
	return s.name
}

func (s *regexScanner) Scan(content []byte) []outputFinding {
	if len(content) == 0 || s == nil {
		return nil
	}
	text := string(content)
	findings := make([]outputFinding, 0, 8)
	for _, pattern := range s.patterns {
		hits := pattern.Expression.FindAllStringIndex(text, maxFindingsPerPattern)
		for _, hit := range hits {
			if len(hit) != 2 {
				continue
			}
			offset := hit[0]
			length := hit[1] - hit[0]
			snippet := text[hit[0]:hit[1]]
			findings = append(findings, outputFinding{
				Type:           s.findingType,
				Severity:       pattern.Severity,
				Detail:         pattern.Label,
				Offset:         int64(offset),
				Length:         int64(length),
				Scanner:        s.name,
				Confidence:     pattern.Confidence,
				MatchedPattern: truncateFinding(snippet, 160),
			})
			if len(findings) >= maxFindingsPerScanner {
				return findings
			}
		}
	}
	return findings
}

type piiScanner struct {
	email *regexp.Regexp
	ssn   *regexp.Regexp
	phone *regexp.Regexp
	card  *regexp.Regexp
}

func newPIIScanner() *piiScanner {
	return &piiScanner{
		email: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
		ssn:   regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		phone: regexp.MustCompile(`\b(?:\+?1[\s\-]?)?(?:\(?\d{3}\)?[\s\-]?)\d{3}[\s\-]?\d{4}\b`),
		card:  regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`),
	}
}

func (s *piiScanner) Name() string { return "pii" }

func (s *piiScanner) Scan(content []byte) []outputFinding {
	if len(content) == 0 || s == nil {
		return nil
	}
	text := string(content)
	findings := make([]outputFinding, 0, 8)
	addRegexFindings := func(re *regexp.Regexp, detail string) {
		hits := re.FindAllStringIndex(text, maxFindingsPerPattern)
		for _, hit := range hits {
			if len(hit) != 2 {
				continue
			}
			fragment := text[hit[0]:hit[1]]
			findings = append(findings, outputFinding{
				Type:           "pii",
				Severity:       "high",
				Detail:         detail,
				Offset:         int64(hit[0]),
				Length:         int64(hit[1] - hit[0]),
				Scanner:        s.Name(),
				Confidence:     0.9,
				MatchedPattern: truncateFinding(fragment, 160),
			})
			if len(findings) >= maxFindingsPerScanner {
				return
			}
		}
	}

	addRegexFindings(s.email, "email address detected")
	if len(findings) >= maxFindingsPerScanner {
		return findings
	}
	addRegexFindings(s.ssn, "ssn detected")
	if len(findings) >= maxFindingsPerScanner {
		return findings
	}
	addRegexFindings(s.phone, "phone number detected")
	if len(findings) >= maxFindingsPerScanner {
		return findings
	}

	cardMatches := s.card.FindAllStringIndex(text, maxFindingsPerPattern)
	for _, hit := range cardMatches {
		if len(hit) != 2 {
			continue
		}
		raw := text[hit[0]:hit[1]]
		digits := digitsOnly(raw)
		if len(digits) < 13 || len(digits) > 19 {
			continue
		}
		if !luhnValid(digits) {
			continue
		}
		findings = append(findings, outputFinding{
			Type:           "pii",
			Severity:       "high",
			Detail:         "payment card number detected",
			Offset:         int64(hit[0]),
			Length:         int64(hit[1] - hit[0]),
			Scanner:        s.Name(),
			Confidence:     0.95,
			MatchedPattern: truncateFinding(raw, 160),
		})
		if len(findings) >= maxFindingsPerScanner {
			return findings
		}
	}

	return findings
}

func defaultOutputScanners() map[string]OutputScanner {
	return map[string]OutputScanner{
		"secret_leak":    newSecretScanner(),
		"secret":         newSecretScanner(),
		"pii":            newPIIScanner(),
		"code_injection": newInjectionScanner(),
		"injection":      newInjectionScanner(),
	}
}

func newSecretScanner() *regexScanner {
	return newRegexScanner("secret_leak", "secret_leak", []regexPattern{
		{
			Label:      "aws access key id",
			Severity:   "critical",
			Pattern:    `AKIA[0-9A-Z]{16}`,
			Expression: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			Confidence: 0.99,
		},
		{
			Label:      "aws secret key style assignment",
			Severity:   "critical",
			Pattern:    `(?i)aws(.{0,20})?(secret|access)[^\\n]{0,20}[=:]\\s*['\"]?[A-Za-z0-9/+=]{40}`,
			Expression: regexp.MustCompile(`(?i)aws(.{0,20})?(secret|access)[^\n]{0,20}[=:]\s*['"]?[A-Za-z0-9/+=]{40}`),
			Confidence: 0.95,
		},
		{
			Label:      "generic credential assignment",
			Severity:   "high",
			Pattern:    `(?i)(api[_-]?key|token|password|secret)\\s*[:=]\\s*['\"][^'\\\"]{8,}['\"]`,
			Expression: regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)\s*[:=]\s*['"][^'"]{8,}['"]`),
			Confidence: 0.9,
		},
		{
			Label:      "private key material",
			Severity:   "critical",
			Pattern:    `-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`,
			Expression: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
			Confidence: 0.99,
		},
		{
			Label:      "github token",
			Severity:   "critical",
			Pattern:    `gh[pousr]_[A-Za-z0-9]{20,}`,
			Expression: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
			Confidence: 0.98,
		},
	})
}

func newInjectionScanner() *regexScanner {
	return newRegexScanner("injection", "code_injection", []regexPattern{
		{
			Label:      "sql injection fragment",
			Severity:   "high",
			Pattern:    `(?i)(union\\s+select|drop\\s+table|delete\\s+from|insert\\s+into|or\\s+1=1)`,
			Expression: regexp.MustCompile(`(?i)(union\s+select|drop\s+table|delete\s+from|insert\s+into|or\s+1=1)`),
			Confidence: 0.9,
		},
		{
			Label:      "shell injection fragment",
			Severity:   "high",
			Pattern:    `(?i)(\\brm\\s+-rf\\b|\\bcurl\\b[^\\n]{0,80}\\|\\s*(sh|bash)\\b|\\bwget\\b[^\\n]{0,80}\\|\\s*(sh|bash)\\b)`,
			Expression: regexp.MustCompile(`(?i)(\brm\s+-rf\b|\bcurl\b[^\n]{0,80}\|\s*(sh|bash)\b|\bwget\b[^\n]{0,80}\|\s*(sh|bash)\b)`),
			Confidence: 0.92,
		},
		{
			Label:      "prompt injection phrase",
			Severity:   "medium",
			Pattern:    `(?i)(ignore\\s+previous\\s+instructions|reveal\\s+system\\s+prompt|jailbreak)`,
			Expression: regexp.MustCompile(`(?i)(ignore\s+previous\s+instructions|reveal\s+system\s+prompt|jailbreak)`),
			Confidence: 0.8,
		},
	})
}

// keywordScanner matches output content against a list of case-insensitive keywords.
type keywordScanner struct {
	keywords []keywordEntry
}

type keywordEntry struct {
	original string
	lower    string
	severity string
}

func newKeywordScanner(keywords []string) *keywordScanner {
	entries := make([]keywordEntry, 0, len(keywords))
	for _, kw := range keywords {
		trimmed := strings.TrimSpace(kw)
		if trimmed == "" {
			continue
		}
		entries = append(entries, keywordEntry{
			original: trimmed,
			lower:    strings.ToLower(trimmed),
			severity: "high",
		})
	}
	return &keywordScanner{keywords: entries}
}

func (s *keywordScanner) Name() string { return "keyword" }

func (s *keywordScanner) Scan(content []byte) []outputFinding {
	if len(content) == 0 || s == nil || len(s.keywords) == 0 {
		return nil
	}
	text := strings.ToLower(string(content))
	findings := make([]outputFinding, 0, 4)
	for _, kw := range s.keywords {
		idx := 0
		for {
			pos := strings.Index(text[idx:], kw.lower)
			if pos < 0 {
				break
			}
			absPos := idx + pos
			findings = append(findings, outputFinding{
				Type:           "keyword_match",
				Severity:       kw.severity,
				Detail:         "keyword matched: " + kw.original,
				Offset:         int64(absPos),
				Length:         int64(len(kw.lower)),
				Scanner:        "keyword",
				Confidence:     1.0,
				MatchedPattern: truncateFinding(kw.original, 160),
			})
			if len(findings) >= maxFindingsPerScanner {
				return findings
			}
			idx = absPos + len(kw.lower)
			if idx >= len(text) {
				break
			}
		}
	}
	return findings
}

func truncateFinding(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func digitsOnly(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhnValid(raw string) bool {
	if raw == "" {
		return false
	}
	sum := 0
	double := false
	for i := len(raw) - 1; i >= 0; i-- {
		d, err := strconv.Atoi(string(raw[i]))
		if err != nil {
			return false
		}
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
