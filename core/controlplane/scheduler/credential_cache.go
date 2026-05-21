package scheduler

import (
	"context"
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

	mu      sync.RWMutex
	records map[string]workercredentials.Credential

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
		records: map[string]workercredentials.Credential{},
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

func cloneCredentialRecord(record workercredentials.Credential) workercredentials.Credential {
	record.AllowedPools = append([]string(nil), record.AllowedPools...)
	record.AllowedTopics = append([]string(nil), record.AllowedTopics...)
	return record
}
