package env

import (
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvMode           = "CORDUM_ENV"
	EnvProduction     = "CORDUM_PRODUCTION"
	EnvTLSMinVersion  = "CORDUM_TLS_MIN_VERSION"
	EnvGRPCReflection = "CORDUM_GRPC_REFLECTION"
)

// Bool returns true for common truthy env values.
func Bool(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// IsProduction reports whether Cordum should run in production mode.
func IsProduction() bool {
	if Bool(EnvProduction) {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(EnvMode)))
	return mode == "prod" || mode == "production"
}

// TLSMinVersion returns the configured TLS minimum version.
func TLSMinVersion() uint16 {
	raw := strings.TrimSpace(os.Getenv(EnvTLSMinVersion))
	if raw != "" {
		switch strings.ToLower(raw) {
		case "1.3", "tls1.3", "tls13":
			return tls.VersionTLS13
		case "1.2", "tls1.2", "tls12":
			return tls.VersionTLS12
		}
	}
	if IsProduction() {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}

// DurationOr reads a duration from an environment variable, falling back to
// the given default. Negative or zero values are rejected.
func DurationOr(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// IntOr reads an int from an environment variable, falling back to the given
// default. Non-positive values are rejected.
func IntOr(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

// requiredProductionVars lists environment variables that must be set in
// production mode. Missing any of these causes a startup failure to prevent
// silently connecting to localhost defaults.
var requiredProductionVars = []string{
	"NATS_URL",
	"REDIS_URL",
}

// ValidateProductionConfig checks that all required environment variables are
// set when running in production mode. Returns nil in non-production mode or
// when all required vars are present.
func ValidateProductionConfig() error {
	if !IsProduction() {
		return nil
	}
	var missing []string
	for _, key := range requiredProductionVars {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("production mode requires these environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Int64Or reads an int64 from an environment variable, falling back to the
// given default.
func Int64Or(key string, fallback int64) int64 {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}
