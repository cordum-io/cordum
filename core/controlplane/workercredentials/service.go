package workercredentials

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
)

const (
	scopeIDWorkers = "workers"

	defaultCreatedBy = "api"
	defaultTenantID  = "default"

	tokenBytes = 32
	saltBytes  = 16
	keyBytes   = 32

	argonMemoryKiB  = 64 * 1024
	argonIterations = 3
	argonParallel   = 1

	// Verification-time safety caps prevent hostile PHC inputs from forcing
	// excessive Argon2 CPU or memory use during authentication.
	maxPHCMemoryKiB    = uint64(1_048_576) // 1 GiB
	maxPHCIterations   = uint64(10_000)
	maxPHCParallelism  = uint64(16)
	maxPHCEncodedBytes = 1024

	// CreateWithLimit is intentionally exercised with many concurrent issuers:
	// use enough CAS retries for legitimate contenders to observe the filled
	// worker limit instead of surfacing transient config revision conflicts.
	workerCredentialCreateCASAttempts = 32
)

var ErrCredentialNotFound = errors.New("worker credential not found")

// Credential is the canonical worker identity record stored at cfg:system:workers.
type Credential struct {
	WorkerID       string   `json:"worker_id"`
	TenantID       string   `json:"tenant_id"`
	CredentialHash string   `json:"credential_hash"`
	AllowedPools   []string `json:"allowed_pools,omitempty"`
	AllowedTopics  []string `json:"allowed_topics,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	CreatedBy      string   `json:"created_by"`
	CreatedAt      string   `json:"created_at"`
	RevokedAt      string   `json:"revoked_at,omitempty"`
}

// IssueInput describes a new or rotated worker credential.
type IssueInput struct {
	TenantID      string
	WorkerID      string
	AllowedPools  []string
	AllowedTopics []string
	PackID        string
	AgentID       string
	CreatedBy     string
	CreatedAt     time.Time
}

type CreateLimit struct {
	Entitlements     licensing.Entitlements
	ConnectedWorkers int
}

// IssuedCredential returns the stored record plus the plaintext token that is
// shown only once to the caller.
type IssuedCredential struct {
	Credential Credential
	Token      string
}

// Service manages canonical worker credentials backed by configsvc.
type Service struct {
	config *configsvc.Service
}

func NewService(cfg *configsvc.Service) *Service {
	if cfg == nil {
		return nil
	}
	return &Service{config: cfg}
}

// Create issues a new plaintext token, hashes it with Argon2id, and stores the
// resulting canonical credential record. Existing records for the same worker ID
// are replaced, which allows credential rotation.
func (s *Service) Create(ctx context.Context, input IssueInput) (IssuedCredential, error) {
	return s.create(ctx, input, nil)
}

func (s *Service) CreateWithLimit(ctx context.Context, input IssueInput, limit CreateLimit) (IssuedCredential, error) {
	return s.create(ctx, input, &limit)
}

func (s *Service) create(ctx context.Context, input IssueInput, limit *CreateLimit) (IssuedCredential, error) {
	if s == nil || s.config == nil {
		return IssuedCredential{}, fmt.Errorf("worker credential store unavailable")
	}

	token, err := GenerateToken()
	if err != nil {
		return IssuedCredential{}, err
	}
	hash, err := hashToken(token)
	if err != nil {
		return IssuedCredential{}, err
	}

	record, err := credentialFromIssueInput(input, hash)
	if err != nil {
		return IssuedCredential{}, err
	}

	if err := s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDWorkers, workerCredentialCreateCASAttempts, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
		}
		if current, ok := existing[record.WorkerID]; ok && !credentialVisibleToTenant(current, record.TenantID) {
			return ErrCredentialNotFound
		}
		current, replacing := existing[record.WorkerID]
		if limit != nil && (!replacing || current.Revoked()) {
			if err := checkCreateLimit(existing, record.TenantID, *limit); err != nil {
				return err
			}
		}
		existing[record.WorkerID] = record
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDWorkers
		doc.Data = encodeDocument(existing)
		return nil
	}); err != nil {
		return IssuedCredential{}, err
	}

	return IssuedCredential{Credential: record, Token: token}, nil
}

// Get returns the stored worker credential, or nil when the worker has not
// been provisioned.
func (s *Service) Get(ctx context.Context, tenantID, workerID string) (*Credential, error) {
	if s == nil || s.config == nil {
		return nil, fmt.Errorf("worker credential store unavailable")
	}
	tenantID = strings.TrimSpace(tenantID)
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id required")
	}

	doc, err := s.config.Get(ctx, configsvc.ScopeSystem, scopeIDWorkers)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("get worker credentials: %w", err)
	}
	records, migrated, err := decodeDocumentWithLegacy(doc)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := s.persistMigratedTenants(ctx); err != nil {
			return nil, err
		}
	}
	record, ok := records[workerID]
	if !ok || !credentialVisibleToTenant(record, tenantID) {
		return nil, nil
	}
	out := record
	return &out, nil
}

// List returns all worker credentials sorted by worker ID.
func (s *Service) List(ctx context.Context, tenantID string) ([]Credential, error) {
	if s == nil || s.config == nil {
		return nil, fmt.Errorf("worker credential store unavailable")
	}
	tenantID = strings.TrimSpace(tenantID)

	doc, err := s.config.Get(ctx, configsvc.ScopeSystem, scopeIDWorkers)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []Credential{}, nil
		}
		return nil, fmt.Errorf("list worker credentials: %w", err)
	}

	records, migrated, err := decodeDocumentWithLegacy(doc)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := s.persistMigratedTenants(ctx); err != nil {
			return nil, err
		}
	}

	out := make([]Credential, 0, len(records))
	for _, record := range records {
		if credentialVisibleToTenant(record, tenantID) {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerID < out[j].WorkerID })
	return out, nil
}

// Revoke marks a worker credential as revoked while preserving the record for
// auditability and incident response.
func (s *Service) Revoke(ctx context.Context, tenantID, workerID string) error {
	if s == nil || s.config == nil {
		return fmt.Errorf("worker credential store unavailable")
	}
	tenantID = strings.TrimSpace(tenantID)
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker id required")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	found := false
	if err := s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDWorkers, 3, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
		}
		record, ok := existing[workerID]
		if !ok || !credentialVisibleToTenant(record, tenantID) {
			return ErrCredentialNotFound
		}
		found = true
		if record.RevokedAt == "" {
			record.RevokedAt = now
		}
		existing[workerID] = normalizeCredential(record)
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDWorkers
		doc.Data = encodeDocument(existing)
		return nil
	}); err != nil {
		if errors.Is(err, ErrCredentialNotFound) {
			return err
		}
		return fmt.Errorf("revoke worker credential: %w", err)
	}
	if !found {
		return ErrCredentialNotFound
	}
	return nil
}

// Verify checks a plaintext token against the stored Argon2id hash for the
// given worker ID. Revoked or missing records are treated as invalid.
func (s *Service) Verify(ctx context.Context, workerID, token string) (*Credential, bool, error) {
	record, err := s.Get(ctx, "", workerID)
	if err != nil || record == nil {
		return record, false, err
	}
	if record.Revoked() {
		return record, false, nil
	}
	ok, err := verifyToken(record.CredentialHash, token)
	if err != nil {
		return nil, false, err
	}
	return record, ok, nil
}

func (c Credential) Revoked() bool {
	return strings.TrimSpace(c.RevokedAt) != ""
}

// GenerateToken creates a cryptographically random 32-byte token encoded as
// lowercase hexadecimal.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate worker credential token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// VerifyHash validates a plaintext token against a PHC-formatted Argon2id hash.
func VerifyHash(phc, token string) (bool, error) {
	return verifyToken(phc, token)
}

func credentialFromIssueInput(input IssueInput, hash string) (Credential, error) {
	createdAt := input.CreatedAt.UTC()
	if input.CreatedAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	record := Credential{
		WorkerID:       strings.TrimSpace(input.WorkerID),
		TenantID:       tenantOrDefault(input.TenantID),
		CredentialHash: strings.TrimSpace(hash),
		AllowedPools:   normalizeStrings(input.AllowedPools),
		AllowedTopics:  normalizeStrings(input.AllowedTopics),
		PackID:         strings.TrimSpace(input.PackID),
		AgentID:        strings.TrimSpace(input.AgentID),
		CreatedBy:      strings.TrimSpace(input.CreatedBy),
		CreatedAt:      createdAt.Format(time.RFC3339),
	}
	if record.CreatedBy == "" {
		record.CreatedBy = defaultCreatedBy
	}
	return validateCredential(record)
}

func decodeDocument(doc *configsvc.Document) (map[string]Credential, error) {
	records, _, err := decodeDocumentWithLegacy(doc)
	return records, err
}

func decodeDocumentWithLegacy(doc *configsvc.Document) (map[string]Credential, bool, error) {
	if doc == nil || len(doc.Data) == 0 {
		return map[string]Credential{}, false, nil
	}

	out := make(map[string]Credential, len(doc.Data))
	migrated := false
	for workerID, raw := range doc.Data {
		blob, err := json.Marshal(raw)
		if err != nil {
			return nil, false, fmt.Errorf("marshal worker credential %s: %w", workerID, err)
		}
		var record Credential
		if err := json.Unmarshal(blob, &record); err != nil {
			return nil, false, fmt.Errorf("decode worker credential %s: %w", workerID, err)
		}
		if strings.TrimSpace(record.WorkerID) == "" {
			record.WorkerID = workerID
		}
		if strings.TrimSpace(record.TenantID) == "" {
			record.TenantID = defaultTenantID
			migrated = true
			slog.Warn("worker credential missing tenant_id; assigning default tenant",
				"worker_id", record.WorkerID, "tenant_id", record.TenantID)
		}
		record, err = validateCredential(record)
		if err != nil {
			return nil, false, fmt.Errorf("decode worker credential %s: %w", workerID, err)
		}
		out[record.WorkerID] = record
	}
	return out, migrated, nil
}

func encodeDocument(records map[string]Credential) map[string]any {
	if len(records) == 0 {
		return map[string]any{}
	}
	names := make([]string, 0, len(records))
	for workerID := range records {
		names = append(names, workerID)
	}
	sort.Strings(names)

	out := make(map[string]any, len(records))
	for _, workerID := range names {
		out[workerID] = normalizeCredential(records[workerID])
	}
	return out
}

func validateCredential(record Credential) (Credential, error) {
	record = normalizeCredential(record)
	if record.WorkerID == "" {
		return Credential{}, fmt.Errorf("worker id required")
	}
	if record.TenantID == "" {
		return Credential{}, fmt.Errorf("tenant id required")
	}
	if record.CredentialHash == "" {
		return Credential{}, fmt.Errorf("credential hash required")
	}
	if record.CreatedBy == "" {
		return Credential{}, fmt.Errorf("created_by required")
	}
	if _, err := time.Parse(time.RFC3339, record.CreatedAt); err != nil {
		return Credential{}, fmt.Errorf("created_at must be RFC3339: %w", err)
	}
	if record.RevokedAt != "" {
		if _, err := time.Parse(time.RFC3339, record.RevokedAt); err != nil {
			return Credential{}, fmt.Errorf("revoked_at must be RFC3339: %w", err)
		}
	}
	if _, _, _, err := parsePHC(record.CredentialHash); err != nil {
		return Credential{}, fmt.Errorf("credential hash invalid: %w", err)
	}
	return record, nil
}

func normalizeCredential(record Credential) Credential {
	record.WorkerID = strings.TrimSpace(record.WorkerID)
	record.TenantID = tenantOrDefault(record.TenantID)
	record.CredentialHash = strings.TrimSpace(record.CredentialHash)
	record.AllowedPools = normalizeStrings(record.AllowedPools)
	record.AllowedTopics = normalizeStrings(record.AllowedTopics)
	record.PackID = strings.TrimSpace(record.PackID)
	record.AgentID = strings.TrimSpace(record.AgentID)
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	record.CreatedAt = strings.TrimSpace(record.CreatedAt)
	record.RevokedAt = strings.TrimSpace(record.RevokedAt)
	return record
}

func tenantOrDefault(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return defaultTenantID
	}
	return tenantID
}

func credentialVisibleToTenant(record Credential, tenantID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return true
	}
	return tenantOrDefault(record.TenantID) == tenantID
}

func checkCreateLimit(records map[string]Credential, tenantID string, limit CreateLimit) error {
	active := 0
	for _, record := range records {
		if credentialVisibleToTenant(record, tenantID) && !record.Revoked() {
			active++
		}
	}
	projected := limit.ConnectedWorkers
	if active+1 > projected {
		projected = active + 1
	}
	if limitErr := licensing.CheckWorkerLimit(int64(projected), limit.Entitlements); limitErr != nil {
		return limitErr
	}
	return nil
}

func (s *Service) persistMigratedTenants(ctx context.Context) error {
	if s == nil || s.config == nil {
		return nil
	}
	return s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDWorkers, 3, func(doc *configsvc.Document) error {
		records, _, err := decodeDocumentWithLegacy(doc)
		if err != nil {
			return err
		}
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDWorkers
		doc.Data = encodeDocument(records)
		return nil
	})
}

func normalizeStrings(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func hashToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("worker credential token required")
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate worker credential salt: %w", err)
	}
	hash := argon2.IDKey([]byte(token), salt, argonIterations, argonMemoryKiB, argonParallel, keyBytes)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemoryKiB,
		argonIterations,
		argonParallel,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyToken(phc, token string) (bool, error) {
	params, salt, expected, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	if len(expected) > maxPHCEncodedBytes {
		return false, fmt.Errorf("argon2 hash too large")
	}
	keyLen := uint32(len(expected)) // #nosec G115 -- len(expected) is bounded by maxPHCEncodedBytes above.
	candidate := argon2.IDKey([]byte(strings.TrimSpace(token)), salt, params.iterations, params.memory, params.parallelism, keyLen)
	return subtle.ConstantTimeCompare(candidate, expected) == 1, nil
}

type argonParams struct {
	version     int
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parsePHC(phc string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, fmt.Errorf("expected argon2id PHC string")
	}

	versionPart := strings.TrimPrefix(parts[2], "v=")
	version, err := strconv.Atoi(versionPart)
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("parse argon2 version: %w", err)
	}

	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, field := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return argonParams{}, nil, nil, fmt.Errorf("parse argon2 params: invalid %q", field)
		}
		switch key {
		case "m":
			memory, err = strconv.ParseUint(value, 10, 32)
		case "t":
			iterations, err = strconv.ParseUint(value, 10, 32)
		case "p":
			parallelism, err = strconv.ParseUint(value, 10, 8)
		default:
			continue
		}
		if err != nil {
			return argonParams{}, nil, nil, fmt.Errorf("parse argon2 param %s: %w", key, err)
		}
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decode argon2 salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decode argon2 hash: %w", err)
	}

	if memory == 0 || iterations == 0 || parallelism == 0 {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 params incomplete")
	}
	if memory > maxPHCMemoryKiB {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 memory cost exceeds cap %d KiB", maxPHCMemoryKiB)
	}
	if iterations > maxPHCIterations {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 iterations exceed cap %d", maxPHCIterations)
	}
	if parallelism > maxPHCParallelism {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 parallelism exceeds cap %d", maxPHCParallelism)
	}
	if len(salt) == 0 || len(hash) == 0 {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 salt/hash required")
	}
	if len(hash) > maxPHCEncodedBytes {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 hash too large")
	}

	return argonParams{
		version:     version,
		memory:      uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
	}, salt, hash, nil
}
