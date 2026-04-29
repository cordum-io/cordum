package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	envLicenseFile      = "CORDUM_LICENSE_FILE"
	envLicenseToken     = "CORDUM_LICENSE_TOKEN"           // #nosec G101 -- environment variable name, not a secret value.
	envLicensePublicKey = "CORDUM_LICENSE_PUBLIC_KEY"      // #nosec G101 -- environment variable name, not a secret value.
	envLicenseKeyPath   = "CORDUM_LICENSE_PUBLIC_KEY_PATH" // #nosec G101 -- environment variable name, not a secret value.
)

// License wraps signed claims with a detached signature.
type License struct {
	KID         string      `json:"kid,omitempty"`
	Payload     Claims      `json:"payload"`
	Signature   string      `json:"signature"`
	ExpiryState ExpiryState `json:"-"`
	Grace       *GraceError `json:"-"`

	signedPayload []byte
}

// Claims define the rights and runtime entitlements of a license.
type Claims struct {
	OrgID          string        `json:"org_id"`
	LicenseID      string        `json:"license_id"`
	Plan           string        `json:"plan"`
	Rights         *Rights       `json:"rights,omitempty"`
	Entitlements   *Entitlements `json:"entitlements,omitempty"`
	DeploymentType string        `json:"deployment_type,omitempty"`
	IssuedAt       string        `json:"issued_at,omitempty"`
	NotBefore      string        `json:"not_before,omitempty"`
	ExpiresAt      string        `json:"expires_at,omitempty"`
	GraceDays      *int          `json:"grace_days,omitempty"`
	InstallID      string        `json:"install_id,omitempty"`
	ClusterID      string        `json:"cluster_id,omitempty"`
}

// Rights capture contractual/commercial rights that are not runtime-enforced.
type Rights struct {
	HostedService bool `json:"hosted_service,omitempty"`
	Embedding     bool `json:"embedding,omitempty"`
	Resale        bool `json:"resale,omitempty"`
	WhiteLabel    bool `json:"white_label,omitempty"`
	SupportSLA    bool `json:"support_sla,omitempty"`
}

// Entitlements capture runtime-enforced capabilities and numeric limits.
type Entitlements struct {
	ApprovalMode       string           `json:"approval_mode,omitempty"`
	TelemetryMode      string           `json:"telemetry_mode,omitempty"`
	MaxWorkers         int64            `json:"max_workers,omitempty"`
	RequestsPerSecond  int64            `json:"requests_per_second,omitempty"`
	MaxConcurrentJobs  int64            `json:"max_concurrent_jobs,omitempty"`
	MaxWorkflowSteps   int64            `json:"max_workflow_steps,omitempty"`
	MaxActiveWorkflows int64            `json:"max_active_workflows,omitempty"`
	MaxTenants         int64            `json:"max_tenants,omitempty"`
	MaxSchemaCount     int64            `json:"max_schema_count,omitempty"`
	MaxPromptChars     int64            `json:"max_prompt_chars,omitempty"`
	MaxBodyBytes       int64            `json:"max_body_bytes,omitempty"`
	MaxArtifactBytes   int64            `json:"max_artifact_bytes,omitempty"`
	MaxPolicyBundles   int64            `json:"max_policy_bundles,omitempty"`
	AuditRetentionDays int64            `json:"audit_retention_days,omitempty"`
	SSO                bool             `json:"sso,omitempty"`
	SAML               bool             `json:"saml,omitempty"`
	SCIM               bool             `json:"scim,omitempty"`
	RBAC               bool             `json:"rbac,omitempty"`
	Audit              bool             `json:"audit,omitempty"`
	AuditExport        bool             `json:"audit_export,omitempty"`
	SIEMExport         bool             `json:"siem_export,omitempty"`
	LegalHold          bool             `json:"legal_hold,omitempty"`
	VelocityRules      bool             `json:"velocity_rules,omitempty"`
	BreakGlassAdmin    bool             `json:"break_glass_admin,omitempty"`
	AgentIdentity      bool             `json:"agent_identity,omitempty"`
	Features           map[string]bool  `json:"features,omitempty"`
	Limits             map[string]int64 `json:"limits,omitempty"`
}

func (c Claims) FeatureEnabled(name string) bool {
	if c.Entitlements == nil {
		return false
	}
	return c.Entitlements.FeatureEnabled(name)
}

func (c Claims) EffectiveGraceDays() int {
	if c.GraceDays == nil {
		return defaultGraceDays
	}
	if *c.GraceDays == 0 {
		return defaultGraceDays
	}
	if *c.GraceDays < 0 {
		return 0
	}
	return *c.GraceDays
}

func (e *Entitlements) FeatureEnabled(name string) bool {
	if e == nil {
		return false
	}
	switch normalizeName(name) {
	case "sso":
		return e.SSO
	case "saml":
		return e.SAML
	case "scim":
		return e.SCIM
	case "rbac":
		return e.RBAC
	case "audit":
		return e.Audit
	case "audit_export":
		return e.AuditExport
	case "siem_export":
		return e.SIEMExport
	case "legal_hold":
		return e.LegalHold
	case "velocity_rules":
		return e.VelocityRules
	case "break_glass_admin":
		return e.BreakGlassAdmin
	case "agent_identity":
		return e.AgentIdentity
	default:
		if e.Features == nil {
			return false
		}
		return e.Features[normalizeName(name)]
	}
}

// LoadFile reads and parses a license JSON file from disk.
func LoadFile(path string) (*License, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- license file path is explicit operator input.
	if err != nil {
		return nil, fmt.Errorf("read license file: %w", err)
	}
	return parseLicense(data)
}

// StandardLicensePaths returns the standard on-disk locations used when no
// explicit CORDUM_LICENSE_FILE or CORDUM_LICENSE_TOKEN is configured.
func StandardLicensePaths() []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 3)
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if dir, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(dir, "Cordum", "license.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".cordum", "license.json"))
	}
	if runtime.GOOS != "windows" {
		add(filepath.Join(string(filepath.Separator), "etc", "cordum", "license.json"))
	}

	return paths
}

// PreferredLicenseFilePath resolves the default destination used by
// cordumctl license install. CORDUM_LICENSE_FILE wins when explicitly set.
func PreferredLicenseFilePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(envLicenseFile)); path != "" {
		return filepath.Clean(path), nil
	}
	paths := StandardLicensePaths()
	if len(paths) == 0 {
		return "", fmt.Errorf("resolve default license path: no standard location available")
	}
	return paths[0], nil
}

// LoadFromEnv reads a license from file or token env vars.
func LoadFromEnv() (*License, error) {
	if path := strings.TrimSpace(os.Getenv(envLicenseFile)); path != "" {
		return LoadFile(path)
	}
	if token := strings.TrimSpace(os.Getenv(envLicenseToken)); token != "" {
		data := []byte(token)
		if !strings.HasPrefix(token, "{") {
			if decoded, err := base64.StdEncoding.DecodeString(token); err == nil {
				data = decoded
			} else if decoded, err := base64.RawStdEncoding.DecodeString(token); err == nil {
				data = decoded
			}
		}
		return parseLicense(data)
	}
	for _, path := range StandardLicensePaths() {
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from documented license search paths.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read license file: %w", err)
		}
		return parseLicense(data)
	}
	return nil, nil
}

func PublicKeyFromEnv() (ed25519.PublicKey, error) {
	if raw := strings.TrimSpace(os.Getenv(envLicensePublicKey)); raw != "" {
		return decodePublicKey(raw)
	}
	if path := strings.TrimSpace(os.Getenv(envLicenseKeyPath)); path != "" {
		data, err := os.ReadFile(path) // #nosec G304 -- public key path is explicit operator configuration.
		if err != nil {
			return nil, fmt.Errorf("read license public key: %w", err)
		}
		return decodePublicKey(string(data))
	}
	if raw := strings.TrimSpace(embeddedLicensePublicKey); raw != "" {
		return decodePublicKey(raw)
	}
	return nil, ErrLicensePublicKeyMissing
}

func (l *License) Verify(pub ed25519.PublicKey, now time.Time) error {
	if l == nil {
		return ErrLicenseRequired
	}
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	payload, err := l.marshalSignedPayload()
	if err != nil {
		return err
	}
	sig, err := decodeSignature(l.Signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrLicenseSignatureInvalid
	}
	state, grace, err := validateWindow(l.Payload, now)
	l.ExpiryState = state
	l.Grace = grace
	return err
}

func (l *License) marshalSignedPayload() ([]byte, error) {
	if l == nil {
		return nil, ErrLicenseRequired
	}
	if len(l.signedPayload) > 0 {
		return append([]byte(nil), l.signedPayload...), nil
	}
	payload, err := json.Marshal(l.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return payload, nil
}

func parseLicense(data []byte) (*License, error) {
	var raw struct {
		KID       string          `json:"kid,omitempty"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse license: %w", err)
	}
	if len(raw.Payload) == 0 {
		return nil, ErrLicensePayloadMissing
	}
	if strings.TrimSpace(raw.Signature) == "" {
		return nil, ErrLicenseSignatureMissing
	}

	payload, err := normalizeJSON(raw.Payload)
	if err != nil {
		return nil, fmt.Errorf("compact license payload: %w", err)
	}

	if isLegacyLicenseEnvelope(raw.Payload) {
		logLegacyLicenseFormatRejected(raw.KID, raw.Payload)
		return nil, ErrUnsupportedLegacyLicenseFormat
	}

	var claims Claims
	if err := json.Unmarshal(raw.Payload, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	return &License{
		KID:           strings.TrimSpace(raw.KID),
		Payload:       claims,
		Signature:     strings.TrimSpace(raw.Signature),
		ExpiryState:   ExpiryStateValid,
		signedPayload: payload,
	}, nil
}

func logLegacyLicenseFormatRejected(kid string, payload json.RawMessage) {
	var legacyMeta struct {
		OrgID     string `json:"org_id"`
		LicenseID string `json:"license_id"`
	}
	_ = json.Unmarshal(payload, &legacyMeta)
	slog.Error(
		"legacy license format rejected",
		"kid", strings.TrimSpace(kid),
		"org_id", strings.TrimSpace(legacyMeta.OrgID),
		"license_id", strings.TrimSpace(legacyMeta.LicenseID),
		"suggested_action", "regenerate with cordum-tools license-generator in the current schema",
		"error", ErrUnsupportedLegacyLicenseFormat,
	)
}

func decodePublicKey(raw string) (ed25519.PublicKey, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "ed25519:"))
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("decode public key: %w", err)
		}
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	return ed25519.PublicKey(data), nil
}

func decodeSignature(raw string) ([]byte, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "ed25519:"))
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("decode signature: %w", err)
		}
	}
	if len(data) != ed25519.SignatureSize {
		return nil, ErrLicenseSignatureInvalid
	}
	return data, nil
}

func parseTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty time")
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), nil
	}
	if unix, err := parseUnix(raw); err == nil {
		return unix, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q", raw)
}

func parseUnix(raw string) (time.Time, error) {
	var seconds int64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return time.Time{}, fmt.Errorf("invalid unix time %q", raw)
		}
		seconds = seconds*10 + int64(r-'0')
	}
	return time.Unix(seconds, 0).UTC(), nil
}
