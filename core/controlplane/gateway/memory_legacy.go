package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
)

var (
	errMemoryInspectionUnavailable = errors.New("memory inspection unavailable")
	errUnsupportedMemoryType       = errors.New("unsupported memory type")
)

type legacyMemoryOwnerKind uint8

const (
	legacyMemoryJobOwner legacyMemoryOwnerKind = iota + 1
	legacyMemoryRunOwner
	maxLegacyMemoryEntries = 1000
)

type legacyMemoryLocation struct {
	key       string
	kind      string
	ownerID   string
	ownerKind legacyMemoryOwnerKind
}

func (s *server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	identity, ok := requireAuthenticatedMemoryTenant(w, r)
	if !ok || !s.requirePermissionOrRole(w, r, auth.PermMemoryRead, "admin") {
		return
	}
	if !s.memoryResourceReader.Compatibility.Enabled {
		writeErrorJSON(w, http.StatusBadRequest, "structured resource reference required")
		return
	}
	if isNilStore(s.memStore) {
		writeErrorJSON(w, http.StatusServiceUnavailable, "memory store unavailable")
		return
	}
	location, err := parseLegacyMemoryLocation(r)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid memory location")
		return
	}
	if !s.authorizeLegacyMemoryOwner(w, r, identity.Tenant, location) {
		return
	}
	s.observeLegacyMemory(identity.Tenant, location.ownerID)
	data, err := s.loadLegacyMemory(r.Context(), location)
	if err != nil {
		writeLegacyMemoryError(w, err)
		return
	}
	writeLegacyMemoryResponse(w, data, location.kind)
}

func parseLegacyMemoryLocation(r *http.Request) (legacyMemoryLocation, error) {
	if r == nil || r.URL == nil {
		return legacyMemoryLocation{}, errors.New("request required")
	}
	values := r.URL.Query()
	for key := range values {
		if key != "key" && key != "ptr" {
			return legacyMemoryLocation{}, errors.New("unknown query parameter")
		}
	}
	keys, pointers := values["key"], values["ptr"]
	if len(keys)+len(pointers) != 1 || len(keys) > 1 || len(pointers) > 1 {
		return legacyMemoryLocation{}, errors.New("exactly one location required")
	}
	var key string
	var err error
	if len(pointers) == 1 {
		if pointers[0] == "" || pointers[0] != strings.TrimSpace(pointers[0]) {
			return legacyMemoryLocation{}, errors.New("invalid pointer")
		}
		key, err = store.KeyFromPointer(pointers[0])
	} else {
		key, err = validateLegacyRawKey(keys[0])
	}
	if err != nil {
		return legacyMemoryLocation{}, err
	}
	return classifyLegacyMemoryKey(key)
}

func validateLegacyRawKey(key string) (string, error) {
	if key == "" || key != strings.TrimSpace(key) || strings.HasPrefix(key, "redis://") {
		return "", errors.New("invalid raw key")
	}
	validated, err := store.KeyFromPointer("redis://" + key)
	if err != nil || validated != key {
		return "", errors.New("invalid raw key")
	}
	return key, nil
}

func classifyLegacyMemoryKey(key string) (legacyMemoryLocation, error) {
	switch {
	case strings.HasPrefix(key, "ctx:"):
		return newJobMemoryLocation(key, "context", strings.TrimPrefix(key, "ctx:"))
	case strings.HasPrefix(key, "res:"):
		return newJobMemoryLocation(key, "result", strings.TrimPrefix(key, "res:"))
	case strings.HasPrefix(key, "mem:run:"):
		runID := strings.SplitN(strings.TrimPrefix(key, "mem:run:"), ":", 2)[0]
		if !validMemoryOwnerID(runID) {
			return legacyMemoryLocation{}, errors.New("invalid run owner")
		}
		return legacyMemoryLocation{key: key, kind: "memory", ownerID: runID, ownerKind: legacyMemoryRunOwner}, nil
	case strings.HasPrefix(key, "mem:"):
		jobID := strings.SplitN(strings.TrimPrefix(key, "mem:"), ":", 2)[0]
		return newJobMemoryLocation(key, "memory", jobID)
	default:
		return legacyMemoryLocation{}, errors.New("unsupported namespace")
	}
}

func newJobMemoryLocation(key, kind, jobID string) (legacyMemoryLocation, error) {
	if !validMemoryOwnerID(jobID) {
		return legacyMemoryLocation{}, errors.New("invalid job owner")
	}
	return legacyMemoryLocation{key: key, kind: kind, ownerID: jobID, ownerKind: legacyMemoryJobOwner}, nil
}

func validMemoryOwnerID(ownerID string) bool {
	return resource.ValidateTrustedContext(resource.TrustedContext{
		TenantID: "authority-check",
		JobID:    ownerID,
	}) == nil
}

func (s *server) authorizeLegacyMemoryOwner(
	w http.ResponseWriter,
	r *http.Request,
	tenantID string,
	location legacyMemoryLocation,
) bool {
	if location.ownerKind == legacyMemoryJobOwner {
		return s.authorizeMemoryJob(w, r, location.ownerID, tenantID)
	}
	if s == nil || s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return false
	}
	run, err := s.workflowStore.GetRun(r.Context(), location.ownerID)
	if err != nil || run == nil || run.OrgID == "" || run.OrgID != tenantID {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return false
	}
	return true
}

func (s *server) observeLegacyMemory(tenantID, ownerID string) {
	if observer := s.memoryResourceReader.Compatibility.Observe; observer != nil {
		observer(resourceio.LegacyUse{
			Component: "gateway.memory",
			TenantID:  tenantID,
			JobID:     ownerID,
		})
	}
}

func (s *server) loadLegacyMemory(ctx context.Context, location legacyMemoryLocation) ([]byte, error) {
	switch location.kind {
	case "context":
		return s.memStore.GetContext(ctx, location.key)
	case "result":
		return s.memStore.GetResult(ctx, location.key)
	case "memory":
		return s.loadRedisMemory(ctx, location.key)
	default:
		return nil, errUnsupportedMemoryType
	}
}
