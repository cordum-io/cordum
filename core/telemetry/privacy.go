package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	EnvTelemetryMode = "CORDUM_TELEMETRY_MODE"
	installIDKey     = "cordum:telemetry:install_id"

	// installPingedKey is set (SetNX) after the one-time install ping succeeds.
	installPingedKey = "cordum:telemetry:install_pinged"
	// firstUsePingedKey is set (SetNX) after the one-time first-use ping succeeds.
	firstUsePingedKey = "cordum:telemetry:firstuse_pinged"
	// UsedMarkerKey is set (SetNX) by the job/workflow pipeline the first time a
	// job or workflow completes, independent of telemetry mode. Exported so the
	// scheduler can set it without depending on a string literal.
	UsedMarkerKey = "cordum:telemetry:used"
)

// Mode controls whether telemetry collection and reporting are enabled.
type Mode string

const (
	ModeOff       Mode = "off"
	ModeLocalOnly Mode = "local_only"
	ModeAnonymous Mode = "anonymous"
)

var (
	randomReader     io.Reader = rand.Reader
	hostnameLookup             = os.Hostname
	processStartTime time.Time = time.Now().UTC()
)

// NormalizeMode converts an arbitrary string into a supported telemetry mode.
// Unknown or empty values default to local_only (collect to Redis, no remote
// reporting). Operators must explicitly set CORDUM_TELEMETRY_MODE=anonymous
// to enable remote reporting.
func NormalizeMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ModeOff), "disabled", "false", "0", "no":
		return ModeOff
	case string(ModeAnonymous), "anon":
		return ModeAnonymous
	case "", string(ModeLocalOnly), "local", "local-only":
		return ModeLocalOnly
	default:
		return ModeLocalOnly
	}
}

// ModeFromEnv returns the configured telemetry mode.
func ModeFromEnv() Mode {
	return NormalizeMode(os.Getenv(EnvTelemetryMode))
}

// Enabled reports whether local collection is enabled.
func (m Mode) Enabled() bool {
	return NormalizeMode(string(m)) != ModeOff
}

// ReportingEnabled reports whether remote reporting is enabled.
func (m Mode) ReportingEnabled() bool {
	return NormalizeMode(string(m)) == ModeAnonymous
}

// HashIdentifier returns a stable salted SHA-256 hex digest. Empty inputs
// return an empty hash so callers can skip optional identifiers safely.
func HashIdentifier(installID, raw string) string {
	installID = strings.TrimSpace(installID)
	raw = strings.TrimSpace(raw)
	if installID == "" || raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(installID + "\n" + raw))
	return hex.EncodeToString(sum[:])
}

// GetInstallID returns the stored anonymous install identifier, generating it
// if needed.
func GetInstallID(ctx context.Context, client redis.UniversalClient) (string, error) {
	if client == nil {
		return "", fmt.Errorf("telemetry install id client required")
	}
	value, err := client.Get(ctx, installIDKey).Result()
	if err == nil {
		return strings.TrimSpace(value), nil
	}
	if err != nil && err != redis.Nil {
		return "", fmt.Errorf("read telemetry install id: %w", err)
	}
	return GenerateInstallID(ctx, client)
}

// GenerateInstallID creates and persists a stable anonymous install
// identifier. Concurrent callers safely converge on the same stored value.
func GenerateInstallID(ctx context.Context, client redis.UniversalClient) (string, error) {
	if client == nil {
		return "", fmt.Errorf("telemetry install id client required")
	}
	candidate, err := newInstallID()
	if err != nil {
		return "", err
	}
	ok, err := client.SetNX(ctx, installIDKey, candidate, 0).Result()
	if err != nil {
		return "", fmt.Errorf("persist telemetry install id: %w", err)
	}
	if ok {
		return candidate, nil
	}
	existing, err := client.Get(ctx, installIDKey).Result()
	if err != nil {
		return "", fmt.Errorf("reload telemetry install id: %w", err)
	}
	return strings.TrimSpace(existing), nil
}

// MarkUsed records (idempotently, via SetNX) that a job or workflow has
// completed at least once on this install. Safe to call concurrently and on
// every completion; only the first call sets the marker.
func MarkUsed(ctx context.Context, client redis.UniversalClient) error {
	if client == nil {
		return nil
	}
	return client.SetNX(ctx, UsedMarkerKey, "1", 0).Err()
}

// keyExists reports whether the given Redis key is currently set.
func keyExists(ctx context.Context, client redis.UniversalClient, key string) (bool, error) {
	if client == nil {
		return false, nil
	}
	n, err := client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// pingClaimTTL bounds how long a claimed-but-not-yet-confirmed one-time ping
// flag lives. Winning the SetNX claim reserves the send so concurrent
// collectors/replicas converge to one sender; persistPinged then makes the flag
// permanent on a confirmed send. If the process dies after claiming but before
// the send is confirmed, the claim expires after this TTL and the ping retries
// (at-least-once on crash) instead of being lost forever.
const pingClaimTTL = 15 * time.Minute

// markPinged claims a one-time ping via SetNX with a bounded TTL. It reports
// whether this caller won the race (true) so concurrent collectors converge and
// never double-send within the claim window.
func markPinged(ctx context.Context, client redis.UniversalClient, key string) (bool, error) {
	if client == nil {
		return false, nil
	}
	return client.SetNX(ctx, key, "1", pingClaimTTL).Result()
}

// persistPinged promotes a claimed ping flag to permanent (no expiry) after a
// confirmed successful send, so the ping never fires again. Best-effort.
func persistPinged(ctx context.Context, client redis.UniversalClient, key string) error {
	if client == nil {
		return nil
	}
	return client.Set(ctx, key, "1", 0).Err()
}

func newInstallID() (string, error) {
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(randomReader, nonce); err != nil {
		return "", fmt.Errorf("generate telemetry nonce: %w", err)
	}
	hostname, err := hostnameLookup()
	if err != nil {
		hostname = ""
	}
	payload := fmt.Sprintf("%s|%s|%x",
		strings.TrimSpace(hostname),
		processStartTime.UTC().Format(time.RFC3339Nano),
		nonce,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:]), nil
}
