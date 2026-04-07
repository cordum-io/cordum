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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
)

const (
	scopeIDWorkers = "workers"

	defaultCreatedBy = "api"

	tokenBytes = 32
	saltBytes  = 16
	keyBytes   = 32

	argonMemoryKiB  = 64 * 1024
	argonIterations = 3
	argonParallel   = 1
)

var ErrCredentialNotFound = errors.New("worker credential not found")

// Credential is the canonical worker identity record stored at cfg:system:workers.
type Credential struct {
	WorkerID       string   `json:"worker_id"`
	CredentialHash string   `json:"credential_hash"`
	AllowedPools   []string `json:"allowed_pools,omitempty"`
	AllowedTopics  []string `json:"allowed_topics,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	CreatedBy      string   `json:"created_by"`
	CreatedAt      string   `json:"created_at"`
	RevokedAt      string   `json:"revoked_at,omitempty"`
}

// IssueInput describes a new or rotated worker credential.
type IssueInput struct {
	WorkerID      string
	AllowedPools  []string
	AllowedTopics []string
	PackID        string
	CreatedBy     string
	CreatedAt     time.Time
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

	if err := s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDWorkers, 3, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
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
func (s *Service) Get(ctx context.Context, workerID string) (*Credential, error) {
	if s == nil || s.config == nil {
		return nil, fmt.Errorf("worker credential store unavailable")
	}
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
	records, err := decodeDocument(doc)
	if err != nil {
		return nil, err
	}
	record, ok := records[workerID]
	if !ok {
		return nil, nil
	}
	out := record
	return &out, nil
}

// List returns all worker credentials sorted by worker ID.
func (s *Service) List(ctx context.Context) ([]Credential, error) {
	if s == nil || s.config == nil {
		return nil, fmt.Errorf("worker credential store unavailable")
	}

	doc, err := s.config.Get(ctx, configsvc.ScopeSystem, scopeIDWorkers)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []Credential{}, nil
		}
		return nil, fmt.Errorf("list worker credentials: %w", err)
	}

	records, err := decodeDocument(doc)
	if err != nil {
		return nil, err
	}

	out := make([]Credential, 0, len(records))
	for _, record := range records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerID < out[j].WorkerID })
	return out, nil
}

// Revoke marks a worker credential as revoked while preserving the record for
// auditability and incident response.
func (s *Service) Revoke(ctx context.Context, workerID string) error {
	if s == nil || s.config == nil {
		return fmt.Errorf("worker credential store unavailable")
	}
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
		if !ok {
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
	record, err := s.Get(ctx, workerID)
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
		CredentialHash: strings.TrimSpace(hash),
		AllowedPools:   normalizeStrings(input.AllowedPools),
		AllowedTopics:  normalizeStrings(input.AllowedTopics),
		PackID:         strings.TrimSpace(input.PackID),
		CreatedBy:      strings.TrimSpace(input.CreatedBy),
		CreatedAt:      createdAt.Format(time.RFC3339),
	}
	if record.CreatedBy == "" {
		record.CreatedBy = defaultCreatedBy
	}
	return validateCredential(record)
}

func decodeDocument(doc *configsvc.Document) (map[string]Credential, error) {
	if doc == nil || len(doc.Data) == 0 {
		return map[string]Credential{}, nil
	}

	out := make(map[string]Credential, len(doc.Data))
	for workerID, raw := range doc.Data {
		blob, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal worker credential %s: %w", workerID, err)
		}
		var record Credential
		if err := json.Unmarshal(blob, &record); err != nil {
			return nil, fmt.Errorf("decode worker credential %s: %w", workerID, err)
		}
		if strings.TrimSpace(record.WorkerID) == "" {
			record.WorkerID = workerID
		}
		record, err = validateCredential(record)
		if err != nil {
			return nil, fmt.Errorf("decode worker credential %s: %w", workerID, err)
		}
		out[record.WorkerID] = record
	}
	return out, nil
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
	record.CredentialHash = strings.TrimSpace(record.CredentialHash)
	record.AllowedPools = normalizeStrings(record.AllowedPools)
	record.AllowedTopics = normalizeStrings(record.AllowedTopics)
	record.PackID = strings.TrimSpace(record.PackID)
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	record.CreatedAt = strings.TrimSpace(record.CreatedAt)
	record.RevokedAt = strings.TrimSpace(record.RevokedAt)
	return record
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
	candidate := argon2.IDKey([]byte(strings.TrimSpace(token)), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
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
	if len(salt) == 0 || len(hash) == 0 {
		return argonParams{}, nil, nil, fmt.Errorf("argon2 salt/hash required")
	}

	return argonParams{
		version:     version,
		memory:      uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
	}, salt, hash, nil
}
