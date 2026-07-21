package scheduler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

func activeKeyForClaims(claims SessionTokenClaims) string {
	if claims.hasBindingClaims() {
		return boundWorkerKey(claims.Tenant, claims.Subject)
	}
	return workerKey(claims.Subject)
}

func boundWorkerKey(tenant, workerID string) string {
	return sessionWorkerKeyPrefix + "v2:" + encodeSessionKeyPart(tenant) + ":" + encodeSessionKeyPart(workerID)
}

func encodeSessionKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(value)))
}

func (i *SessionTokenIssuer) loadActiveForClaims(ctx context.Context, claims SessionTokenClaims) (*activeRecord, error) {
	return i.loadActiveKey(ctx, activeKeyForClaims(claims))
}

func (i *SessionTokenIssuer) loadActiveKey(ctx context.Context, key string) (*activeRecord, error) {
	raw, err := i.redis.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("scheduler: load active session: %w", err)
	}
	var rec activeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("scheduler: parse active session: %w", err)
	}
	return &rec, nil
}

func (i *SessionTokenIssuer) loadRevocationTargets(ctx context.Context, tenant, workerID string) ([]*activeRecord, error) {
	tenant = strings.TrimSpace(tenant)
	workerID = strings.TrimSpace(workerID)
	if tenant == "" || workerID == "" {
		return nil, fmt.Errorf("%w: revoke requires tenant + worker_id", ErrSessionTokenMissingClaims)
	}
	bound, err := i.loadActiveKey(ctx, boundWorkerKey(tenant, workerID))
	if err != nil {
		return nil, err
	}
	targets := make([]*activeRecord, 0, 2)
	if bound != nil {
		if err := bound.validateStoredBinding(tenant, workerID); err != nil {
			return nil, err
		}
		targets = append(targets, bound)
	}
	legacy, err := i.loadActive(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if legacy != nil && legacy.Tenant == tenant {
		targets = append(targets, legacy)
	}
	return targets, nil
}

func (r activeRecord) validateStoredBinding(tenant, workerID string) error {
	if r.Tenant != tenant || r.WorkerID != workerID || r.JTI == "" || r.ExpUnix <= 0 {
		return ErrSessionTokenBindingMismatch
	}
	if r.AgentID == "" || r.Audience == "" || r.ProofKeyID == "" || r.SDKVersion == "" {
		return ErrSessionTokenBindingMismatch
	}
	return nil
}

func newActiveRecord(claims SessionTokenClaims) activeRecord {
	return activeRecord{
		JTI: claims.JTI, ExpUnix: claims.ExpiresAt.Unix(), WorkerID: claims.Subject,
		AgentID: claims.AgentID, Tenant: claims.Tenant, Audience: claims.Audience,
		ProofKeyID: claims.ProofKeyID, SDKVersion: claims.SDKVersion,
	}
}

func (r activeRecord) validateClaims(claims SessionTokenClaims) error {
	if r.Tenant != claims.Tenant || r.ExpUnix != claims.ExpiresAt.Unix() {
		return ErrSessionTokenBindingMismatch
	}
	if !claims.hasBindingClaims() {
		return nil
	}
	if r.WorkerID != claims.Subject || r.AgentID != claims.AgentID ||
		r.Audience != claims.Audience || r.ProofKeyID != claims.ProofKeyID ||
		r.SDKVersion != claims.SDKVersion {
		return ErrSessionTokenBindingMismatch
	}
	return nil
}

func (i *SessionTokenIssuer) persistRenew(ctx context.Context, prior, fresh SessionTokenClaims) error {
	key := activeKeyForClaims(prior)
	revocation := revokedKey(prior.Tenant, prior.JTI)
	err := i.redis.Watch(ctx, func(tx *redis.Tx) error {
		return i.persistRenewTx(ctx, tx, key, revocation, prior, fresh)
	}, key, revocation)
	if errors.Is(err, redis.TxFailedErr) {
		if activeErr := i.assertActive(ctx, prior); activeErr != nil {
			return activeErr
		}
		return ErrSessionTokenSuperseded
	}
	if err != nil {
		return fmt.Errorf("scheduler: rotate active session: %w", err)
	}
	return nil
}

func (i *SessionTokenIssuer) persistRenewTx(ctx context.Context, tx *redis.Tx, key, revocation string, prior, fresh SessionTokenClaims) error {
	rec, err := loadActiveRecordTx(ctx, tx, key)
	if err != nil {
		return err
	}
	if rec == nil || rec.JTI != prior.JTI {
		return ErrSessionTokenSuperseded
	}
	if err := rec.validateClaims(prior); err != nil {
		return err
	}
	if revoked, err := tx.Exists(ctx, revocation).Result(); err != nil {
		return err
	} else if revoked != 0 {
		return ErrSessionTokenRevoked
	}
	return i.writeRenewTx(ctx, tx, key, revocation, prior, fresh)
}

func loadActiveRecordTx(ctx context.Context, tx *redis.Tx, key string) (*activeRecord, error) {
	raw, err := tx.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseActiveRecord(raw)
}

func (i *SessionTokenIssuer) writeRenewTx(ctx context.Context, tx *redis.Tx, key, revocation string, prior, fresh SessionTokenClaims) error {
	payload, err := json.Marshal(newActiveRecord(fresh))
	if err != nil {
		return err
	}
	now := i.now()
	freshTTL := fresh.ExpiresAt.Sub(now)
	if freshTTL <= 0 {
		return ErrSessionTokenExpired
	}
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		if priorTTL := prior.ExpiresAt.Sub(now); priorTTL > 0 {
			pipe.Set(ctx, revocation, "1", priorTTL)
		}
		pipe.Set(ctx, key, payload, freshTTL)
		return nil
	})
	return err
}
