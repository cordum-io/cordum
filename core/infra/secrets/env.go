package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvSecretCacheTTL controls the TTL for resolved secret values.
// Accepts Go duration strings (e.g. "5m", "30s", "0" to disable).
// Default: 5m.
const EnvSecretCacheTTL = "SECRET_CACHE_TTL"

// NewResolverFromEnv builds a Resolver by probing environment variables
// for each supported provider.  Only providers whose required env vars
// are set are registered.  Returns (nil, nil) when no providers are
// configured — callers should treat a nil Resolver as "secrets
// resolution disabled" and fall back to redaction.
//
// Supported providers:
//
//	vault   — requires VAULT_ADDR and VAULT_TOKEN
//	aws-sm  — requires AWS_REGION, AWS_ACCESS_KEY_ID, and AWS_SECRET_ACCESS_KEY
//
// Optional:
//
//	SECRET_CACHE_TTL — cache TTL for resolved values (default "5m", "0" disables)
func NewResolverFromEnv(ctx context.Context) (*Resolver, error) {
	cacheTTL := parseCacheTTL(os.Getenv(EnvSecretCacheTTL))

	r := NewResolver(WithCacheTTL(cacheTTL))
	var registered []string
	var initErrors []string

	// --- Vault ---
	vaultAddr := strings.TrimSpace(os.Getenv(EnvVaultAddr))
	vaultToken := strings.TrimSpace(os.Getenv(EnvVaultToken))
	if vaultAddr != "" {
		if vaultToken == "" {
			initErrors = append(initErrors,
				fmt.Sprintf("vault: %s is set but %s is empty", EnvVaultAddr, EnvVaultToken))
		} else {
			vaultMount := strings.TrimSpace(os.Getenv(EnvVaultMount))
			vp, err := NewVaultProvider(vaultAddr, vaultToken, vaultMount)
			if err != nil {
				return nil, fmt.Errorf("secrets: init vault: %w", err)
			}
			r.Register(vp)
			registered = append(registered, "vault")
		}
	}

	// --- AWS Secrets Manager ---
	awsRegion := strings.TrimSpace(os.Getenv(EnvAWSRegion))
	awsAccessKey := strings.TrimSpace(os.Getenv(EnvAWSAccessKeyID))
	awsSecretKey := strings.TrimSpace(os.Getenv(EnvAWSSecretAccessKey))
	if awsRegion != "" && awsAccessKey != "" && awsSecretKey != "" {
		ap, err := NewAWSSecretsManagerProviderFromEnv()
		if err != nil {
			return nil, fmt.Errorf("secrets: init aws-sm: %w", err)
		}
		r.Register(ap)
		registered = append(registered, "aws-sm")
	} else if awsRegion != "" && (awsAccessKey == "" || awsSecretKey == "") {
		initErrors = append(initErrors,
			fmt.Sprintf("aws-sm: %s is set but credentials (%s, %s) are incomplete",
				EnvAWSRegion, EnvAWSAccessKeyID, EnvAWSSecretAccessKey))
	}

	// Log warnings for partial configurations.
	for _, e := range initErrors {
		slog.Warn("secrets provider partially configured", "detail", e)
	}

	if len(registered) == 0 {
		slog.Info("secrets resolver: no providers configured",
			"hint", fmt.Sprintf("set %s+%s for Vault or %s+%s+%s for AWS Secrets Manager",
				EnvVaultAddr, EnvVaultToken, EnvAWSRegion, EnvAWSAccessKeyID, EnvAWSSecretAccessKey))
		return nil, nil
	}

	slog.Info("secrets resolver initialized",
		"providers", registered,
		"cache_ttl", cacheTTL.String(),
	)
	return r, nil
}

// parseCacheTTL parses the cache TTL from a string.  Accepts Go
// duration strings ("5m", "30s") and plain seconds ("300").  Returns
// the default (5m) for empty or unparseable values.
func parseCacheTTL(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 5 * time.Minute
	}

	// Try as a Go duration string first.
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// Try as plain seconds.
	if secs, err := strconv.Atoi(s); err == nil {
		return time.Duration(secs) * time.Second
	}

	slog.Warn("secrets: invalid cache TTL, using default",
		"value", s, "default", "5m")
	return 5 * time.Minute
}
