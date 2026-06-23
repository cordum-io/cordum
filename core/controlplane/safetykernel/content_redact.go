package safetykernel

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// ContentFinding is the exported, package-agnostic shape of one detection hit
// in arbitrary content. It mirrors the internal outputFinding so callers
// (notably the LLM-proxy ingest path) can reuse the SHIPPED scanners — the same
// piiScanner / secretScanner / keywordScanner the Safety Kernel uses — without
// importing the unexported types or duplicating any pattern. This is the
// content analogue of ScanForPromptInjection.
type ContentFinding struct {
	// Type is the scanner finding type: "pii", "secret_leak", "keyword_match",
	// or "custom" (operator pattern).
	Type string
	// Detail is the human-readable rule label (e.g. "email address detected").
	Detail string
	// Severity is the scanner severity ("critical" | "high" | "medium").
	Severity string
	// Confidence is the scanner confidence in [0,1].
	Confidence float64
	// Offset and Length locate the matched span within the scanned content so a
	// caller can redact exactly that region.
	Offset int
	Length int
}

// NamedContentPattern is an operator-supplied regex (e.g. a salary format or
// project codename) layered on top of the built-in scanners.
type NamedContentPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// ContentScanOptions selects which shipped scanners run plus any operator
// additions. Zero value scans nothing; the LLM-proxy path enables PII+secrets
// by default via ContentScanOptionsFromEnv.
type ContentScanOptions struct {
	IncludePII     bool
	IncludeSecrets bool
	Keywords       []string
	ExtraPatterns  []NamedContentPattern
}

// ScanContent runs the selected SHIPPED scanners over content and returns every
// finding, sorted by offset. It is read-only and side-effect-free; it reuses
// newPIIScanner / newSecretScanner / newKeywordScanner so there is exactly one
// detection source of truth across the control plane and the edge.
func ScanContent(content []byte, opts ContentScanOptions) []ContentFinding {
	if len(content) == 0 {
		return nil
	}
	var scanners []OutputScanner
	if opts.IncludeSecrets {
		scanners = append(scanners, newSecretScanner())
	}
	if opts.IncludePII {
		scanners = append(scanners, newPIIScanner())
	}
	if len(opts.Keywords) > 0 {
		scanners = append(scanners, newKeywordScanner(opts.Keywords))
	}
	if len(opts.ExtraPatterns) > 0 {
		pats := make([]regexPattern, 0, len(opts.ExtraPatterns))
		for _, np := range opts.ExtraPatterns {
			if np.Pattern == nil {
				continue
			}
			pats = append(pats, regexPattern{
				Label:      np.Name,
				Severity:   "high",
				Pattern:    np.Pattern.String(),
				Expression: np.Pattern,
				Confidence: 0.9,
			})
		}
		if len(pats) > 0 {
			scanners = append(scanners, newRegexScanner("custom", "custom", pats))
		}
	}
	var findings []ContentFinding
	for _, sc := range scanners {
		for _, f := range sc.Scan(content) {
			findings = append(findings, ContentFinding{
				Type:       f.Type,
				Detail:     f.Detail,
				Severity:   f.Severity,
				Confidence: float64(f.Confidence),
				Offset:     int(f.Offset),
				Length:     int(f.Length),
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Offset != findings[j].Offset {
			return findings[i].Offset < findings[j].Offset
		}
		return findings[i].Length > findings[j].Length // longer span first on a tie
	})
	return findings
}

// RedactContent scans content with the SHIPPED scanners and masks each matched
// span in place with "<redacted:type>", preserving the surrounding text so an
// LLM prompt/response stays usable. Returns the redacted string and the
// findings (offsets refer to the ORIGINAL content). Overlapping spans are
// merged (earliest finding keeps its type).
func RedactContent(content []byte, opts ContentScanOptions) (string, []ContentFinding) {
	findings := ScanContent(content, opts)
	if len(findings) == 0 {
		return string(content), nil
	}
	text := string(content)
	var b strings.Builder
	cursor := 0
	for _, f := range findings {
		if f.Offset < cursor || f.Offset < 0 || f.Offset+f.Length > len(text) {
			continue // overlaps a prior span, or out of range — skip
		}
		b.WriteString(text[cursor:f.Offset])
		b.WriteString("<redacted:")
		b.WriteString(f.Type)
		b.WriteString(">")
		cursor = f.Offset + f.Length
	}
	b.WriteString(text[cursor:])
	return b.String(), findings
}

// ContentScanOptionsFromEnv builds ContentScanOptions from CORDUM_EDGE_LLM_*
// environment variables for the LLM-proxy path. PII and secret detection are ON
// by default (the whole point of the proxy); operators add org-specific terms.
//
//	CORDUM_EDGE_LLM_DETECT_PII      = "false" to disable built-in PII (default on)
//	CORDUM_EDGE_LLM_DETECT_SECRETS  = "false" to disable secrets   (default on)
//	CORDUM_EDGE_LLM_REDACT_KEYWORDS = comma-separated literal terms
//	CORDUM_EDGE_LLM_REDACT_PATTERNS = JSON array of {"name","pattern"}
//
// Invalid custom regexes are dropped (best-effort).
func ContentScanOptionsFromEnv(getenv func(string) string) ContentScanOptions {
	if getenv == nil {
		return ContentScanOptions{IncludePII: true, IncludeSecrets: true}
	}
	notFalse := func(v string) bool {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0", "no":
			return false
		}
		return true
	}
	opts := ContentScanOptions{
		IncludePII:     notFalse(getenv("CORDUM_EDGE_LLM_DETECT_PII")),
		IncludeSecrets: notFalse(getenv("CORDUM_EDGE_LLM_DETECT_SECRETS")),
	}
	if raw := strings.TrimSpace(getenv("CORDUM_EDGE_LLM_REDACT_KEYWORDS")); raw != "" {
		for _, kw := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(kw); t != "" {
				opts.Keywords = append(opts.Keywords, t)
			}
		}
	}
	if raw := strings.TrimSpace(getenv("CORDUM_EDGE_LLM_REDACT_PATTERNS")); raw != "" {
		var specs []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal([]byte(raw), &specs); err == nil {
			for _, s := range specs {
				name := strings.TrimSpace(s.Name)
				if name == "" || strings.TrimSpace(s.Pattern) == "" {
					continue
				}
				re, err := regexp.Compile(s.Pattern)
				if err != nil {
					continue
				}
				opts.ExtraPatterns = append(opts.ExtraPatterns, NamedContentPattern{Name: name, Pattern: re})
			}
		}
	}
	return opts
}
