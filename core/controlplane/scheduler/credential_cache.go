package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

type WorkerAttestationMode string

const (
	WorkerAttestationOff     WorkerAttestationMode = "off"
	WorkerAttestationWarn    WorkerAttestationMode = "warn"
	WorkerAttestationEnforce WorkerAttestationMode = "enforce"
	EnvWorkerAttestation                           = "WORKER_ATTESTATION"
)

func ParseWorkerAttestationMode(raw string) WorkerAttestationMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(WorkerAttestationEnforce):
		return WorkerAttestationEnforce
	case string(WorkerAttestationWarn):
		return WorkerAttestationWarn
	case "", string(WorkerAttestationOff):
		return WorkerAttestationOff
	default:
		return WorkerAttestationOff
	}
}

// ParseWorkerAttestationModeStrict validates scheduler boot configuration.
// The permissive parser remains for compatibility at non-boot call sites.
func ParseWorkerAttestationModeStrict(raw string) (WorkerAttestationMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return WorkerAttestationOff, nil
	}
	switch WorkerAttestationMode(normalized) {
	case WorkerAttestationOff, WorkerAttestationWarn, WorkerAttestationEnforce:
		return WorkerAttestationMode(normalized), nil
	default:
		return "", fmt.Errorf("%s must be off, warn, or enforce", EnvWorkerAttestation)
	}
}

func (m WorkerAttestationMode) Normalized() WorkerAttestationMode {
	return ParseWorkerAttestationMode(string(m))
}

func (m WorkerAttestationMode) Enabled() bool {
	return m.Normalized() != WorkerAttestationOff
}

func (m WorkerAttestationMode) Enforced() bool {
	return m.Normalized() == WorkerAttestationEnforce
}

type WorkerCredentialCache struct {
	service *workercredentials.Service
	list    func(context.Context) ([]workercredentials.Credential, error)

	mu             sync.RWMutex
	records        map[string]workercredentials.Credential
	authority      map[string]workercredentials.Credential
	authorityReady bool

	refreshing atomic.Bool
}

func NewWorkerCredentialCache(service *workercredentials.Service) *WorkerCredentialCache {
	return &WorkerCredentialCache{
		service: service,
		list: func(ctx context.Context) ([]workercredentials.Credential, error) {
			if service == nil {
				return nil, nil
			}
			return service.List(ctx, "")
		},
		records:   map[string]workercredentials.Credential{},
		authority: map[string]workercredentials.Credential{},
	}
}

func (c *WorkerCredentialCache) Refresh(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if !c.refreshing.CompareAndSwap(false, true) {
		return nil
	}
	defer c.refreshing.Store(false)
	c.clearAuthority()

	list := c.list
	if list == nil && c.service != nil {
		list = func(ctx context.Context) ([]workercredentials.Credential, error) {
			return c.service.List(ctx, "")
		}
	}
	if list == nil {
		return nil
	}
	records, err := list(ctx)
	if err != nil {
		slog.Warn("worker credential cache refresh failed; keeping existing entries", "error", err)
		return nil
	}

	next := make(map[string]workercredentials.Credential, len(records))
	for _, record := range records {
		next[record.WorkerID] = record
	}

	c.mu.Lock()
	c.authority = cloneCredentialMap(next)
	c.authorityReady = true
	if c.records == nil {
		c.records = make(map[string]workercredentials.Credential, len(next))
	}
	stale := make([]string, 0)
	for workerID := range c.records {
		if _, ok := next[workerID]; !ok {
			stale = append(stale, workerID)
		}
	}
	for workerID, record := range next {
		c.records[workerID] = record
	}
	c.mu.Unlock()
	if len(stale) > 0 {
		sort.Strings(stale)
		slog.Warn("worker credential cache refresh retained stale entries",
			"count", len(stale),
			"workers", stale,
		)
	}
	return nil
}

func (c *WorkerCredentialCache) Verify(workerID, token string) (*workercredentials.Credential, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	workerID = strings.TrimSpace(workerID)
	token = strings.TrimSpace(token)
	if workerID == "" || token == "" {
		return nil, false, nil
	}

	c.mu.RLock()
	record, ok := c.records[workerID]
	record = cloneCredentialRecord(record)
	c.mu.RUnlock()
	if !ok || record.Revoked() {
		return nil, false, nil
	}

	ok, err := workercredentials.VerifyHash(record.CredentialHash, token)
	if err != nil {
		return nil, false, err
	}
	return &record, ok, nil
}

func (c *WorkerCredentialCache) clearAuthority() {
	c.mu.Lock()
	c.authority = nil
	c.authorityReady = false
	c.mu.Unlock()
}

func cloneCredentialMap(records map[string]workercredentials.Credential) map[string]workercredentials.Credential {
	clone := make(map[string]workercredentials.Credential, len(records))
	for workerID, record := range records {
		clone[workerID] = cloneCredentialRecord(record)
	}
	return clone
}

// Lookup returns an immutable snapshot of the active worker credential.
// It is intentionally separate from Verify: authenticated session tokens bind
// to the enrolled proof authority rather than the legacy bearer credential.
func (c *WorkerCredentialCache) Lookup(workerID string) (*workercredentials.Credential, bool) {
	if c == nil || c.refreshing.Load() {
		return nil, false
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, false
	}
	c.mu.RLock()
	record, ok := c.authority[workerID]
	ready := c.authorityReady
	refreshing := c.refreshing.Load()
	record = cloneCredentialRecord(record)
	c.mu.RUnlock()
	if refreshing || !ready || !ok || record.Revoked() {
		return nil, false
	}
	return &record, true
}

// RefreshAuthority synchronously reloads the canonical authorization snapshot.
// A concurrent or failed refresh is unavailable rather than stale authority.
func (c *WorkerCredentialCache) RefreshAuthority(ctx context.Context) bool {
	if c == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.Refresh(ctx); err != nil || c.refreshing.Load() {
		return false
	}
	c.mu.RLock()
	ready := c.authorityReady
	refreshing := c.refreshing.Load()
	c.mu.RUnlock()
	return ready && !refreshing
}

func cloneCredentialRecord(record workercredentials.Credential) workercredentials.Credential {
	record.AllowedPools = append([]string(nil), record.AllowedPools...)
	record.AllowedTopics = append([]string(nil), record.AllowedTopics...)
	return record
}
