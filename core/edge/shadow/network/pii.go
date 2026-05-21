package network

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// PIIMode selects how the raw workload identity / OIDC subject is
// transformed into the persisted principal_id. Operators choose the
// strictness level via CORDUM_EDGE_SHADOW_PII_MODE (or programmatic
// Config.PIIMode override). Mode semantics (binding governor ruling
// Q2 on parent task task-de50a293, comment-a17f4f1c):
//
//	pseudonymize: principal_id = first 3 chars of raw value +
//	              first 8 hex chars of SHA-256(raw). Stable
//	              identifier across runs that lets operators correlate
//	              repeat offenders without revealing the username.
//	hash:         principal_id = first 16 hex chars of SHA-256(raw).
//	              No prefix → not reversible by inspection; weakest
//	              correlation strength still preserved.
//	drop:         principal_id = DroppedPrincipalSentinel constant.
//	              Strictest mode — no identity propagation whatsoever.
type PIIMode string

// PII-mode enum values.
const (
	PIIModePseudonymize PIIMode = "pseudonymize"
	PIIModeHash         PIIMode = "hash"
	PIIModeDrop         PIIMode = "drop"
)

// PIIModeEnvVar names the env override.
const PIIModeEnvVar = "CORDUM_EDGE_SHADOW_PII_MODE"

// DroppedPrincipalSentinel is the placeholder value persisted in
// principal_id when PIIMode = drop, or when the raw input was empty.
// Non-empty so shadow.normalizeAndValidateCreate's required-field
// check passes; stable so dashboards can recognise the sentinel
// (rather than treating it as "just another opaque principal").
const DroppedPrincipalSentinel = "dropped"

// pseudonymizePrefixLen is the literal-prefix length on
// PIIModePseudonymize output. 3 chars chosen to align with the
// governor's worked example "github-actor" → "git…" and to give
// operators a stable correlation-friendly hint without revealing the
// full username.
const pseudonymizePrefixLen = 3

// pseudonymizeHashLen is the SHA-256 hex suffix length on
// PIIModePseudonymize output.
const pseudonymizeHashLen = 8

// hashPIDLen is the SHA-256 hex prefix length on PIIModeHash output.
const hashPIDLen = 16

// applyPIIMode transforms a raw workload-identity / OIDC-subject
// value into the persisted principal_id per the active mode. An empty
// raw value always collapses to DroppedPrincipalSentinel regardless
// of mode — there is no useful pseudonymization of "".
func applyPIIMode(raw string, mode PIIMode) string {
	if raw == "" {
		return DroppedPrincipalSentinel
	}
	switch mode {
	case PIIModeDrop:
		return DroppedPrincipalSentinel
	case PIIModeHash:
		sum := sha256.Sum256([]byte(raw))
		hexStr := hex.EncodeToString(sum[:])
		if len(hexStr) > hashPIDLen {
			hexStr = hexStr[:hashPIDLen]
		}
		return hexStr
	case PIIModePseudonymize:
		fallthrough
	default:
		prefix := raw
		if len(prefix) > pseudonymizePrefixLen {
			prefix = prefix[:pseudonymizePrefixLen]
		}
		sum := sha256.Sum256([]byte(raw))
		hexStr := hex.EncodeToString(sum[:])
		if len(hexStr) > pseudonymizeHashLen {
			hexStr = hexStr[:pseudonymizeHashLen]
		}
		return prefix + hexStr
	}
}

// resolvePIIMode reads the effective mode given an explicit Config
// override (preferred) or the env-var fallback. Returns an error for
// any non-empty value outside the enum so misconfiguration fails fast
// at NewDetector rather than silently defaulting at first emit.
func resolvePIIMode(explicit PIIMode) (PIIMode, error) {
	if explicit != "" {
		return validatePIIMode(string(explicit))
	}
	envVal := strings.TrimSpace(os.Getenv(PIIModeEnvVar))
	if envVal == "" {
		return PIIModePseudonymize, nil
	}
	return validatePIIMode(envVal)
}

func validatePIIMode(s string) (PIIMode, error) {
	m := PIIMode(strings.ToLower(strings.TrimSpace(s)))
	switch m {
	case PIIModePseudonymize, PIIModeHash, PIIModeDrop:
		return m, nil
	}
	return "", fmt.Errorf("network: invalid %s=%q (want pseudonymize|hash|drop)", PIIModeEnvVar, s)
}
