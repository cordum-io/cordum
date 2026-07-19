package scheduler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/redis/go-redis/v9"
)

// BoundTrustCredentialResolver supplies the canonical worker credential used
// to derive tenant, agent, and proof-key authority for a dispatch decision.
type BoundTrustCredentialResolver interface {
	GetByWorkerID(context.Context, string) (*workercredentials.Credential, error)
}

// NewBoundTrustResolver constructs the only resolver suitable for session-
// authority dispatch. It never scans Redis or reads the tenantless legacy key.
func NewBoundTrustResolver(rdb redis.UniversalClient, credentials BoundTrustCredentialResolver) (*TrustResolver, error) {
	if missingTrustAuthority(reflect.ValueOf(rdb)) || missingTrustAuthority(reflect.ValueOf(credentials)) {
		return nil, errors.New("scheduler: bound trust Redis and credential authorities required")
	}
	return &TrustResolver{redis: rdb, credentials: credentials, boundOnly: true, now: time.Now}, nil
}

func missingTrustAuthority(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// BoundAuthorityReady reports whether tenant-bound dispatch reads are fully
// configured. DispatchGate uses this to fail closed in session modes.
func (r *TrustResolver) BoundAuthorityReady() bool {
	return r != nil && r.boundOnly && !missingTrustAuthority(reflect.ValueOf(r.redis)) &&
		!missingTrustAuthority(reflect.ValueOf(r.credentials))
}

func (r *TrustResolver) resolveBoundTrust(ctx context.Context, workerID string) (WorkerTrustState, error) {
	credential, err := r.credentials.GetByWorkerID(ctx, workerID)
	if err != nil {
		return WorkerTrustState{Reason: TrustReasonStoreUnready}, fmt.Errorf("scheduler: resolve worker credential: %w", err)
	}
	if !validTrustCredential(credential, workerID) {
		return WorkerTrustState{Reason: TrustReasonCredentialInvalid}, nil
	}
	rec, err := r.loadActiveKey(ctx, boundWorkerKey(credential.TenantID, workerID))
	if err != nil {
		return WorkerTrustState{Reason: TrustReasonStoreUnready}, err
	}
	if rec == nil {
		return WorkerTrustState{Reason: TrustReasonNoSession}, nil
	}
	if err := validateBoundTrustRecord(credential, rec); err != nil {
		return WorkerTrustState{Reason: TrustReasonCredentialInvalid}, err
	}
	return r.stateFromRecord(ctx, rec)
}

func validTrustCredential(credential *workercredentials.Credential, workerID string) bool {
	return credential != nil && credential.WorkerID == workerID &&
		exactTrustValue(credential.TenantID) && exactTrustValue(credential.AgentID) &&
		exactTrustValue(credential.ProofKeyID) && !credential.Revoked()
}

func validateBoundTrustRecord(credential *workercredentials.Credential, rec *activeRecord) error {
	if err := rec.validateStoredBinding(credential.TenantID, credential.WorkerID); err != nil {
		return err
	}
	if rec.Audience != WorkerHandshakeAudience || credential.AgentID != rec.AgentID ||
		credential.ProofKeyID != rec.ProofKeyID {
		return ErrSessionTokenBindingMismatch
	}
	return nil
}

func exactTrustValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
