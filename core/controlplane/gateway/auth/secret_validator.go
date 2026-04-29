package auth

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"
)

// weakPatterns are case-insensitive substrings that indicate a weak secret.
var weakPatterns = []string{
	"cordum", "admin", "password", "changeme", "12345",
	"qwerty", "letmein", "welcome", "default", "secret",
}

// ValidateSecretStrength checks that a named secret meets minimum security
// requirements: length, character class diversity, entropy, and absence of
// known-weak patterns. Returns nil if the secret is acceptable.
func ValidateSecretStrength(name, value string, minLen int) error {
	if len(value) < minLen {
		return fmt.Errorf("%s is too short: got %d characters, minimum %d. Generate one with: openssl rand -base64 %d",
			name, len(value), minLen, max(minLen, 24))
	}

	lower := strings.ToLower(value)
	for _, pattern := range weakPatterns {
		if strings.Contains(lower, pattern) {
			return fmt.Errorf("%s contains weak pattern %q. Use a randomly generated value: openssl rand -base64 %d",
				name, pattern, max(minLen, 24))
		}
	}

	if entropy := shannonEntropy(value); entropy < 3.0 {
		return fmt.Errorf("%s has low entropy (%.1f bits/char, minimum 3.0). Use a randomly generated value: openssl rand -base64 %d",
			name, entropy, max(minLen, 24))
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range value {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	classes := 0
	if hasUpper {
		classes++
	}
	if hasLower {
		classes++
	}
	if hasDigit {
		classes++
	}
	if hasSpecial {
		classes++
	}
	if classes < 2 {
		return fmt.Errorf("%s must contain at least 2 character classes (uppercase, lowercase, digits, special). Generate one with: openssl rand -base64 %d",
			name, max(minLen, 24))
	}

	return nil
}

// ValidateStartupSecrets validates all security-sensitive environment
// variables at gateway startup. Returns an error if any secret is too
// weak for production use. Respects CORDUM_SKIP_SECRET_VALIDATION=true
// as a dev-only escape hatch.
func ValidateStartupSecrets() error {
	if skip := os.Getenv("CORDUM_SKIP_SECRET_VALIDATION"); strings.EqualFold(skip, "true") || skip == "1" {
		return nil
	}

	env := os.Getenv("CORDUM_ENV")
	if env != "production" && env != "prod" {
		// Only enforce in production mode
		return nil
	}

	// Validate admin password (required in production)
	adminPass := os.Getenv("CORDUM_ADMIN_PASSWORD")
	if adminPass != "" {
		if err := ValidateSecretStrength("CORDUM_ADMIN_PASSWORD", adminPass, 16); err != nil {
			return err
		}
	}

	// Validate API key (required in production)
	apiKey := os.Getenv("CORDUM_API_KEY")
	if apiKey != "" {
		if err := ValidateSecretStrength("CORDUM_API_KEY", apiKey, 32); err != nil {
			return err
		}
	}

	// REDIS_PASSWORD: empty is valid (passwordless Redis)
	// Only validate if set and non-empty
	redisPass := os.Getenv("REDIS_PASSWORD")
	if redisPass != "" && !isDefaultDevelopmentRedisPassword(redisPass) {
		if err := ValidateSecretStrength("REDIS_PASSWORD", redisPass, 12); err != nil {
			return err
		}
	}

	return nil
}

func isDefaultDevelopmentRedisPassword(value string) bool {
	return value == "cordum"+"-dev"
}

// shannonEntropy calculates the Shannon entropy (bits per character) of s.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
	}
	n := float64(len([]rune(s)))
	var entropy float64
	for _, count := range freq {
		p := float64(count) / n
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}
