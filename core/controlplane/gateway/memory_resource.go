package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	maxMemoryResolveRequestBytes = 16 << 10
	maxResolvedMemoryBytes       = 1 << 20
)

var (
	errMemoryResolveBodyTooLarge = errors.New("memory resolve request body too large")
	errGatewayResourceMalformed  = errors.New("gateway resource content is not valid JSON")
	errStructuredRemediation     = errors.New("structured remediation context requires a resource writer")
	errMemoryResourceTooLarge    = errors.New("memory resource exceeds inspection bounds")
)

type memoryResolveRequest struct {
	JobID     string          `json:"job_id"`
	Reference json.RawMessage `json:"reference"`
}

type parsedMemoryResolveRequest struct {
	jobID     string
	reference *agentv1.ResourceRef
}

type gatewayJobResourceRequest struct {
	JobID         string
	TenantID      string
	Reference     *agentv1.ResourceRef
	LegacyPointer string
	LegacyKind    resourceio.LegacyKind
	Component     string
}

// WithMemoryResourceRegistry installs the operator-controlled resolver set.
// A nil registry leaves structured resolution unavailable and fail closed.
func (s *server) WithMemoryResourceRegistry(registry *resource.Registry) *server {
	if s != nil {
		s.memoryResourceReader.Resolver = registry
	}
	return s
}

// WithLegacyMemoryCompatibility explicitly enables migration-only Redis
// pointer reads. The observer deliberately receives no pointer value.
func (s *server) WithLegacyMemoryCompatibility(observe func(resourceio.LegacyUse)) *server {
	if s != nil {
		s.memoryResourceReader.Compatibility = resourceio.LegacyCompatibility{
			Enabled: true,
			Observe: observe,
		}
	}
	return s
}

func (s *server) handleResolveMemory(w http.ResponseWriter, r *http.Request) {
	identity, ok := requireAuthenticatedMemoryTenant(w, r)
	if !ok || !s.requirePermissionOrRole(w, r, auth.PermMemoryRead, "admin") {
		return
	}
	request, err := decodeMemoryResolveRequest(w, r)
	if err != nil {
		writeMemoryResolveDecodeError(w, err)
		return
	}
	if !s.authorizeMemoryJob(w, r, request.jobID, identity.Tenant) {
		return
	}
	resolved, err := s.readGatewayJobResource(r.Context(), gatewayJobResourceRequest{
		JobID: request.jobID, TenantID: identity.Tenant,
		Reference: request.reference, Component: "gateway.memory",
	})
	if err != nil {
		writeMemoryResolveError(w, err)
		return
	}
	if len(resolved.Content) > maxResolvedMemoryBytes {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "resolved resource too large")
		return
	}
	writeJSON(w, map[string]any{
		"media_type": resolved.MediaType,
		"size_bytes": len(resolved.Content),
		"base64":     base64.StdEncoding.EncodeToString(resolved.Content),
	})
}

func decodeMemoryResolveRequest(w http.ResponseWriter, r *http.Request) (parsedMemoryResolveRequest, error) {
	if r == nil || r.Body == nil {
		return parsedMemoryResolveRequest{}, errors.New("request body required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMemoryResolveRequestBytes))
	decoder.DisallowUnknownFields()
	var body memoryResolveRequest
	if err := decoder.Decode(&body); err != nil {
		return parsedMemoryResolveRequest{}, normalizeMemoryBodyError(err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return parsedMemoryResolveRequest{}, errors.New("single JSON document required")
	}
	if body.JobID == "" || body.JobID != strings.TrimSpace(body.JobID) {
		return parsedMemoryResolveRequest{}, errors.New("invalid job_id")
	}
	if len(body.Reference) == 0 || string(body.Reference) == "null" {
		return parsedMemoryResolveRequest{}, errors.New("reference required")
	}
	ref := new(agentv1.ResourceRef)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body.Reference, ref); err != nil {
		return parsedMemoryResolveRequest{}, errors.New("invalid resource reference")
	}
	trusted := resource.TrustedContext{TenantID: "placeholder", JobID: body.JobID}
	if resource.ValidateTrustedContext(trusted) != nil {
		return parsedMemoryResolveRequest{}, errors.New("invalid job_id")
	}
	return parsedMemoryResolveRequest{jobID: body.JobID, reference: ref}, nil
}

func normalizeMemoryBodyError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errMemoryResolveBodyTooLarge
	}
	return err
}

func writeMemoryResolveDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMemoryResolveBodyTooLarge) {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
}

func writeMemoryResolveError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMemoryResourceTooLarge) {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "resolved resource too large")
		return
	}
	if errors.Is(err, resourceio.ErrResolverUnavailable) || errors.Is(err, resource.ErrUnavailable) {
		writeErrorJSON(w, http.StatusServiceUnavailable, "resource resolver unavailable")
		return
	}
	writeErrorJSON(w, http.StatusBadRequest, "resource resolution failed")
}

func requireAuthenticatedMemoryTenant(w http.ResponseWriter, r *http.Request) (*auth.AuthContext, bool) {
	identity := auth.FromRequest(r)
	if identity == nil || identity.Tenant == "" || identity.Tenant != strings.TrimSpace(identity.Tenant) {
		writeErrorJSON(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	trusted := resource.TrustedContext{TenantID: identity.Tenant, JobID: "authority-check"}
	if resource.ValidateTrustedContext(trusted) != nil {
		writeErrorJSON(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return identity, true
}

func (s *server) authorizeMemoryJob(w http.ResponseWriter, r *http.Request, jobID, tenantID string) bool {
	if s == nil || s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return false
	}
	jobTenant, err := s.jobStore.GetTenant(r.Context(), jobID)
	if err != nil || jobTenant == "" || jobTenant != tenantID {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return false
	}
	return true
}

func (s *server) readGatewayJobResource(
	ctx context.Context,
	request gatewayJobResourceRequest,
) (resource.ResolvedResource, error) {
	if request.Reference != nil && request.Reference.GetSizeBytes() > uint64(maxResolvedMemoryBytes) {
		return resource.ResolvedResource{}, errMemoryResourceTooLarge
	}
	trusted := resource.TrustedContext{TenantID: request.TenantID, JobID: request.JobID}
	return s.memoryResourceReader.Read(ctx, resourceio.ReadRequest{
		Reference:     request.Reference,
		LegacyPointer: strings.TrimSpace(request.LegacyPointer),
		Trusted:       trusted,
		Component:     request.Component,
		LoadLegacy: func(loadCtx context.Context, pointer string) ([]byte, error) {
			return s.loadBoundLegacyJobResource(loadCtx, pointer, request.LegacyKind, trusted)
		},
	})
}

func (s *server) readGatewayJSONResource(
	ctx context.Context,
	request gatewayJobResourceRequest,
	target any,
) error {
	resolved, err := s.readGatewayJobResource(ctx, request)
	if err != nil {
		return err
	}
	if len(resolved.Content) == 0 || len(resolved.Content) > maxResolvedMemoryBytes {
		return errGatewayResourceMalformed
	}
	if request.Reference != nil && !isJSONResourceMediaType(resolved.MediaType) {
		return errGatewayResourceMalformed
	}
	if err := json.Unmarshal(resolved.Content, target); err != nil {
		return errGatewayResourceMalformed
	}
	return nil
}

func isJSONResourceMediaType(mediaType string) bool {
	return mediaType == "application/json" ||
		(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))
}

func (s *server) cloneRemediationContext(
	ctx context.Context,
	request *agentv1.JobRequest,
	newJobID string,
	tenantID string,
) (string, error) {
	if request.GetContextRef() != nil {
		return "", errStructuredRemediation
	}
	pointer := strings.TrimSpace(request.GetContextPtr())
	if pointer == "" {
		return "", nil
	}
	resolved, err := s.readGatewayJobResource(ctx, gatewayJobResourceRequest{
		JobID: request.GetJobId(), TenantID: tenantID, LegacyPointer: pointer,
		LegacyKind: resourceio.LegacyContext, Component: "gateway.remediation.context",
	})
	if err != nil {
		return "", err
	}
	if len(resolved.Content) == 0 || len(resolved.Content) > maxResolvedMemoryBytes {
		return "", errGatewayResourceMalformed
	}
	key := store.MakeContextKey(newJobID)
	if err := s.memStore.PutContext(ctx, key, resolved.Content); err != nil {
		return "", err
	}
	return store.PointerForKey(key), nil
}

func (s *server) loadBoundLegacyJobResource(
	ctx context.Context,
	pointer string,
	kind resourceio.LegacyKind,
	trusted resource.TrustedContext,
) ([]byte, error) {
	if s == nil || isNilStore(s.memStore) {
		return nil, resourceio.ErrLegacyLoaderMissing
	}
	key, err := resourceio.BoundLegacyKey(pointer, kind, trusted)
	if err != nil {
		return nil, err
	}
	if kind == resourceio.LegacyContext {
		return s.memStore.GetContext(ctx, key)
	}
	if kind == resourceio.LegacyResult {
		return s.memStore.GetResult(ctx, key)
	}
	return nil, resourceio.ErrLegacyScopeMismatch
}
