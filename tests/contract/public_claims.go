package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	publicNameRE = regexp.MustCompile(`^(cordum|site)-[a-z0-9]+(-[a-z0-9]+)*$`)
	publicLineRE = regexp.MustCompile(`^[1-9][0-9]*$`)
	publicSepRE  = regexp.MustCompile(`^:?-{3,}:?$`)
	publicRefs   = []string{"none:bench", "none:support", "none:maturity"}
	publicClass  = []string{"accurate", "overstated", "unverifiable"}
	publicCodes  = []string{"ev:test", "ev:bench", "ev:release", "ev:attest", "ev:code", "ev:policy"}
	publicReason = []string{"instr", "code", "nav", "plan", "limit", "ext", "process"}
)

type publicSurface struct {
	name, repo string
	hits       map[string]bool
	entries    map[string]map[string]bool
	counts     map[string]int
	claims     map[string]Claim
	is         issues
}

// ValidatePublicClaims checks paired hit manifests and classification tables.
// Only I/O failures return an error; format and cross-reference failures are issues.
func ValidatePublicClaims(dir string, reg *ClaimRegister) ([]Issue, map[string]map[string]int, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read public claims: %w", err)
	}
	pairs := map[string]map[string]bool{}
	for _, f := range files {
		ext := filepath.Ext(f.Name())
		if f.IsDir() || (ext != ".md" && ext != ".hits") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ext)
		if pairs[name] == nil {
			pairs[name] = map[string]bool{}
		}
		pairs[name][ext] = true
	}
	var is issues
	counts := map[string]map[string]int{}
	if len(pairs) == 0 {
		is.add("surface-none", "", "public-claims: no surface files")
	}
	for _, name := range publicKeys(pairs) {
		if !publicNameRE.MatchString(name) {
			is.add("surface-name", "", name+": invalid surface name")
			continue
		}
		if !pairs[name][".md"] || !pairs[name][".hits"] {
			is.add("surface-pair", "", name+": requires "+name+".md and "+name+".hits")
			continue
		}
		s, err := readPublicSurface(dir, name, reg)
		if err != nil {
			return nil, nil, err
		}
		is = append(is, s.is...)
		counts[name] = s.counts
	}
	return is, counts, nil
}

func publicKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (s *publicSurface) issue(rule, ref, location string) {
	if !idRE.MatchString(ref) {
		ref = ""
	}
	s.is.add(rule, ref, s.name+": "+location)
}

func readPublicSurface(dir, name string, reg *ClaimRegister) (*publicSurface, error) {
	s := &publicSurface{name: name, repo: "cordum", hits: map[string]bool{},
		entries: map[string]map[string]bool{}, claims: map[string]Claim{},
		counts: map[string]int{"hits": 0, "accurate": 0, "overstated": 0, "unverifiable": 0, "not-a-claim": 0}}
	if strings.HasPrefix(name, "site-") {
		s.repo = "Cordum-site"
	}
	if reg != nil {
		for _, c := range reg.Claims {
			s.claims[c.ID] = c
		}
	}
	for _, ext := range []string{".hits", ".md"} {
		b, err := os.ReadFile(filepath.Join(dir, name+ext))
		if err != nil {
			return nil, fmt.Errorf("%s: read %s: %w", name, ext, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if ext == ".hits" {
				s.readHit(line)
			} else if strings.HasPrefix(line, "|") {
				s.readRow(line)
			}
		}
	}
	s.counts["hits"] = len(s.hits)
	s.checkCoverage()
	return s, nil
}

func (s *publicSurface) readHit(line string) {
	if line == "" {
		return
	}
	i := strings.LastIndexByte(line, ':')
	if i < 0 {
		s.issue("hits-format", "", line)
		return
	}
	_, err := repoFile("", line[:i])
	if err != nil || !publicLineRE.MatchString(line[i+1:]) {
		s.issue("hits-format", "", line)
		return
	}
	s.hits[line] = true
}

func (s *publicSurface) readRow(line string) {
	cells := strings.Split(line, "|")
	if len(cells) != 4 || cells[0] != "" || strings.TrimSpace(cells[3]) != "" {
		s.issue("row-format", "", line)
		return
	}
	path, entries := strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
	if (path == "File" && entries == "Entries") || (publicSepRE.MatchString(path) && publicSepRE.MatchString(entries)) {
		return
	}
	if _, err := repoFile("", path); err != nil {
		s.issue("row-format", "", path)
		return
	}
	for _, entry := range strings.Split(entries, ";") {
		s.readEntry(path, strings.Fields(entry))
	}
}

func publicEntryValid(f []string) bool {
	if len(f) < 3 || !publicLineRE.MatchString(f[0]) {
		return false
	}
	if f[1] == "not-a-claim" {
		return len(f) == 3 && slices.Contains(publicReason, f[2])
	}
	if (!idRE.MatchString(f[1]) && !slices.Contains(publicRefs, f[1])) || !slices.Contains(publicClass, f[2]) {
		return false
	}
	for _, code := range f[3:] {
		if !findRE.MatchString(code) && !slices.Contains(publicCodes, code) {
			return false
		}
	}
	return true
}

func (s *publicSurface) readEntry(path string, f []string) {
	location, ref := path, ""
	if len(f) > 0 {
		location += ":" + f[0]
	}
	if len(f) > 1 {
		ref = f[1]
	}
	if !publicEntryValid(f) {
		s.issue("entry-format", ref, location)
		return
	}
	seen := s.entries[location]
	if seen[ref] || seen["not-a-claim"] || (ref == "not-a-claim" && len(seen) > 0) {
		s.issue("entry-duplicate", ref, location)
	}
	if seen == nil {
		seen = map[string]bool{}
		s.entries[location] = seen
	}
	seen[ref] = true
	if ref == "not-a-claim" {
		s.counts[ref]++
		return
	}
	s.counts[f[2]]++
	if (f[2] == "accurate") != (len(f) == 3) {
		s.issue("class-codes", ref, location)
	}
	if idRE.MatchString(ref) {
		c, ok := s.claims[ref]
		if !ok {
			s.issue("claim-unknown", ref, location)
		} else if !s.hasPublicSource(c, path) {
			s.issue("source-unfilled", ref, location)
		}
	}
}

func (s *publicSurface) hasPublicSource(c Claim, path string) bool {
	for _, source := range deref(c.PublicSources) {
		m := srcRE.FindStringSubmatch(source)
		if m != nil && m[1] == s.repo && m[2] == path {
			return true
		}
	}
	return false
}

func (s *publicSurface) checkCoverage() {
	for _, hit := range publicKeys(s.hits) {
		if len(s.entries[hit]) == 0 {
			s.issue("hit-missing", "", hit)
		}
	}
	for _, hit := range publicKeys(s.entries) {
		if !s.hits[hit] {
			s.issue("hit-extra", "", hit)
		}
	}
	for _, id := range publicKeys(s.claims) {
		for _, source := range deref(s.claims[id].PublicSources) {
			m := srcRE.FindStringSubmatch(source)
			if m == nil || m[1] != s.repo {
				continue
			}
			hit := strings.TrimPrefix(source, s.repo+":")
			if s.hits[hit] && !s.entries[hit][id] {
				s.issue("source-unmatched", id, hit)
			}
		}
	}
}
