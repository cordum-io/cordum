package scheduler

import (
	"context"
	"strings"
	"sync"

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

	mu      sync.RWMutex
	records map[string]workercredentials.Credential
}

func NewWorkerCredentialCache(service *workercredentials.Service) *WorkerCredentialCache {
	return &WorkerCredentialCache{
		service: service,
		records: map[string]workercredentials.Credential{},
	}
}

func (c *WorkerCredentialCache) Refresh(ctx context.Context) error {
	if c == nil || c.service == nil {
		return nil
	}
	records, err := c.service.List(ctx)
	if err != nil {
		return err
	}

	next := make(map[string]workercredentials.Credential, len(records))
	for _, record := range records {
		next[record.WorkerID] = record
	}

	c.mu.Lock()
	c.records = next
	c.mu.Unlock()
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
	c.mu.RUnlock()
	if !ok || record.Revoked() {
		return nil, false, nil
	}

	ok, err := workercredentials.VerifyHash(record.CredentialHash, token)
	if err != nil {
		return nil, false, err
	}
	out := record
	return &out, ok, nil
}
