package sign

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EDGE-151-DOWNGRADE — version-floor enforcement.
//
// This file adds release-time semver awareness on top of the EDGE-151
// signature-only path. The install scripts and the CI release workflow
// share one comparator and one floor-file shape so an attacker who can
// replay a signed older release cannot ride the gate to a known-CVE'd
// binary; see docs/security/binary-signing.md §2(g)/§8A.

// FloorMetadata captures the auxiliary fields persisted alongside a
// version in the binary-version-floor.json state file. None of these are
// secret — they only describe how the floor was last advanced for audit
// log correlation.
type FloorMetadata struct {
	SigScheme   string `json:"sig_scheme,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Operator    string `json:"operator,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// floorFile is the on-disk shape of the persisted floor file.
type floorFile struct {
	Version    string `json:"version"`
	AdvancedAt string `json:"advanced_at"`
	FloorMetadata
}

// SemverCompare returns -1, 0, or +1 for a < b, a == b, a > b respectively.
// Inputs may be prefixed with `v`. Pre-release suffixes (`-rc1`, `-beta.2`)
// are honoured per the semver-2.0 ordering rules: a version with a
// pre-release suffix is *less than* the same MAJOR.MINOR.PATCH without one,
// and pre-release identifiers compare numerically when both sides parse as
// integers, otherwise lexicographically.
//
// Invalid versions (anything that doesn't parse into 3 numeric components)
// sort *after* every valid version, so callers that want strict rejection
// must validate with ParseSemver first; the install path does this via
// VerifyVersionFloor which returns ErrInvalidVersion on garbage input.
func SemverCompare(a, b string) int {
	aMaj, aMin, aPat, aPre, aOk := parseSemver(a)
	bMaj, bMin, bPat, bPre, bOk := parseSemver(b)
	switch {
	case !aOk && !bOk:
		// Stable tie-break: lexicographic on the raw strings.
		return strings.Compare(strings.TrimSpace(a), strings.TrimSpace(b))
	case !aOk:
		return 1
	case !bOk:
		return -1
	}
	if c := cmpInt(aMaj, bMaj); c != 0 {
		return c
	}
	if c := cmpInt(aMin, bMin); c != 0 {
		return c
	}
	if c := cmpInt(aPat, bPat); c != 0 {
		return c
	}
	// Same M.N.P — pre-release ordering.
	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "":
		// Release > pre-release.
		return 1
	case bPre == "":
		return -1
	}
	return comparePreRelease(aPre, bPre)
}

// ParseSemver returns the three numeric components and any pre-release
// suffix (without leading `-`) of v, plus ok=false when v is not a valid
// `vMAJOR.MINOR.PATCH[-PRE]` form. Exported for callers (e.g. the CI
// monotonicity gate driver) that need to reject malformed tags up front.
func ParseSemver(v string) (major, minor, patch int, pre string, ok bool) {
	return parseSemver(v)
}

func parseSemver(v string) (int, int, int, string, bool) {
	s := strings.TrimSpace(v)
	if s == "" {
		return 0, 0, 0, "", false
	}
	s = strings.TrimPrefix(s, "v")
	pre := ""
	if i := strings.Index(s, "-"); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	if i := strings.Index(s, "+"); i >= 0 {
		// Build metadata is ignored for ordering per semver-2.0.
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 4)
	if len(parts) != 3 {
		return 0, 0, 0, "", false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, "", false
	}
	if maj < 0 || min < 0 || pat < 0 {
		return 0, 0, 0, "", false
	}
	return maj, min, pat, pre, true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func comparePreRelease(a, b string) int {
	aFields := strings.Split(a, ".")
	bFields := strings.Split(b, ".")
	n := min(len(aFields), len(bFields))
	for i := 0; i < n; i++ {
		af, bf := aFields[i], bFields[i]
		an, aErr := strconv.Atoi(af)
		bn, bErr := strconv.Atoi(bf)
		switch {
		case aErr == nil && bErr == nil:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aErr == nil:
			// Numeric < alphanumeric per semver-2.0.
			return -1
		case bErr == nil:
			return 1
		default:
			// Natural-sort within a single field so rc2 < rc10 (matches
			// real-world release-tag conventions). When both fields share
			// the same alpha prefix and have numeric suffixes, compare the
			// numbers; otherwise fall back to lex.
			if c := compareNatural(af, bf); c != 0 {
				return c
			}
		}
	}
	// Common prefix matches; the longer list has higher precedence.
	return cmpInt(len(aFields), len(bFields))
}

// compareNatural compares two pre-release identifier fields under a
// natural-sort rule: when both fields share a common alpha prefix and
// end in distinct numeric suffixes, compare those suffixes numerically.
// Otherwise it falls back to plain lex comparison.
func compareNatural(a, b string) int {
	aAlpha, aNum, aOk := splitAlphaNum(a)
	bAlpha, bNum, bOk := splitAlphaNum(b)
	if aOk && bOk && aAlpha == bAlpha {
		return cmpInt(aNum, bNum)
	}
	return strings.Compare(a, b)
}

func splitAlphaNum(s string) (string, int, bool) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) || i == 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return "", 0, false
	}
	return s[:i], n, true
}

// VerifyVersionFloor returns nil when the candidate binary version is >=
// the persisted floor (or when the floor is empty, i.e. first install),
// ErrDowngradeAttempt when the candidate is strictly older, and
// ErrInvalidVersion when either argument is non-empty but unparseable.
func VerifyVersionFloor(candidate, persistedFloor string) error {
	candidate = strings.TrimSpace(candidate)
	persistedFloor = strings.TrimSpace(persistedFloor)
	if candidate == "" {
		return fmt.Errorf("%w: empty candidate", ErrInvalidVersion)
	}
	if _, _, _, _, ok := parseSemver(candidate); !ok {
		return fmt.Errorf("%w: candidate=%q", ErrInvalidVersion, candidate)
	}
	if persistedFloor == "" {
		// First install — anything goes.
		return nil
	}
	if _, _, _, _, ok := parseSemver(persistedFloor); !ok {
		return fmt.Errorf("%w: floor=%q", ErrInvalidVersion, persistedFloor)
	}
	if SemverCompare(candidate, persistedFloor) < 0 {
		return fmt.Errorf("%w: %s < %s", ErrDowngradeAttempt, candidate, persistedFloor)
	}
	return nil
}

// ReadFloor returns the persisted floor version, or "" when the file is
// missing (treated as a first-install state). A malformed file is an
// error — silent recovery would let an attacker who can clear the floor
// also bypass the gate.
func ReadFloor(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied install state
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("sign: read floor %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", nil
	}
	var ff floorFile
	if err := json.Unmarshal(body, &ff); err != nil {
		return "", fmt.Errorf("sign: parse floor %s: %w", path, err)
	}
	return ff.Version, nil
}

// AdvanceFloor writes the floor file atomically (write-tmp + rename) at the
// supplied path with the given version and metadata. AdvancedAt is set to
// time.Now().UTC().Format(time.RFC3339). Returns ErrFloorAdvanceFailed on
// any IO error.
//
// AdvanceFloor is the only mutator: both upgrade and operator-override
// rollback funnel through here, so audit-event correlation stays simple.
func AdvanceFloor(path string, newVersion string, meta FloorMetadata) error {
	newVersion = strings.TrimSpace(newVersion)
	if newVersion == "" {
		return fmt.Errorf("%w: empty version", ErrFloorAdvanceFailed)
	}
	if _, _, _, _, ok := parseSemver(newVersion); !ok {
		return fmt.Errorf("%w: invalid version %q", ErrFloorAdvanceFailed, newVersion)
	}
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("%w: mkdir %s: %v", ErrFloorAdvanceFailed, dir, err)
		}
	}
	ff := floorFile{
		Version:       newVersion,
		AdvancedAt:    time.Now().UTC().Format(time.RFC3339),
		FloorMetadata: meta,
	}
	body, err := json.Marshal(&ff)
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrFloorAdvanceFailed, err)
	}
	tmp, err := os.CreateTemp(dir, ".binary-version-floor.json.tmp.*")
	if err != nil {
		return fmt.Errorf("%w: tempfile: %v", ErrFloorAdvanceFailed, err)
	}
	tmpName := tmp.Name()
	closed := false
	success := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		// On any non-success path remove the temp file so we never leave litter.
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("%w: write: %v", ErrFloorAdvanceFailed, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("%w: sync: %v", ErrFloorAdvanceFailed, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: close: %v", ErrFloorAdvanceFailed, err)
	}
	closed = true
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("%w: rename: %v", ErrFloorAdvanceFailed, err)
	}
	success = true
	return nil
}

// EmbedVersion prepends `# version: <semver>\n` to the manifest file at
// path. The line is signature-covered when the manifest is detach-signed
// downstream. Idempotent: a manifest that already carries the exact same
// `# version:` line is returned unchanged. A *different* embedded version
// is rejected — we never silently rewrite trust-relevant metadata.
func EmbedVersion(manifestPath, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return errors.New("sign: EmbedVersion: empty version")
	}
	if _, _, _, _, ok := parseSemver(version); !ok {
		return fmt.Errorf("sign: EmbedVersion: invalid version %q", version)
	}
	body, err := os.ReadFile(manifestPath) //nolint:gosec // build-time release tooling
	if err != nil {
		return fmt.Errorf("sign: EmbedVersion: read %s: %w", manifestPath, err)
	}
	want := "# version: " + version
	// Inspect only the first line — version metadata is required to be the
	// first line of the manifest so install scripts can extract it without
	// scanning every entry.
	lines := strings.SplitN(string(body), "\n", 2)
	if len(lines) >= 1 && strings.HasPrefix(strings.TrimRight(lines[0], "\r"), "# version:") {
		got := strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(lines[0], "\r"), "# version:"))
		if got == version {
			return nil
		}
		return fmt.Errorf("sign: EmbedVersion: manifest already embeds version %q, refusing to overwrite with %q", got, version)
	}
	out := want + "\n" + string(body)
	if err := os.WriteFile(manifestPath, []byte(out), 0o644); err != nil {
		return fmt.Errorf("sign: EmbedVersion: write %s: %w", manifestPath, err)
	}
	return nil
}

// ParseVersion extracts the embedded version line from a SHA256SUMS-style
// manifest. Returns ErrNoVersionEmbedded when the manifest has no
// `# version:` line in its first non-empty line.
func ParseVersion(manifestPath string) (string, error) {
	body, err := os.ReadFile(manifestPath) //nolint:gosec // operator-supplied install path
	if err != nil {
		return "", fmt.Errorf("sign: ParseVersion: %w", err)
	}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "# version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# version:")), nil
		}
		// First non-empty, non-comment line means no version is embedded.
		return "", ErrNoVersionEmbedded
	}
	return "", ErrNoVersionEmbedded
}
