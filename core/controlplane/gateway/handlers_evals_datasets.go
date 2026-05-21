package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

// Eval dataset CRUD surface.
//
// These handlers serve the governance regression-suite store: curated,
// immutable policy-test fixtures that the sibling eval-runner task will
// replay through the policy engine. Datasets are immutable, so the PUT
// surface does not mutate in place — it creates a successor version from
// an existing dataset id. That keeps the CRUD contract while preserving
// the epic rail that historical versions stay queryable forever.
//
// Payload envelopes follow the existing gateway idioms: list responses
// carry `{items, nextCursor}`; errors are plain `{error: <msg>}` JSON via
// writeErrorJSON; all routes are tenant-scoped using the standard
// tenantFromRequest helper.

// maxEvalDatasetRequestBytes bounds the POST body we accept. It mirrors
// the model-level MaxEvalDatasetBytes cap plus a small envelope margin so
// a caller hitting the exact cap of canonical content still lands under
// the body limit with envelope whitespace and field names included.
const maxEvalDatasetRequestBytes = model.MaxEvalDatasetBytes + (256 * 1024)

// evalDatasetQueryTimestampFormats are the formats the list endpoint
// accepts for the `created_after` / `created_before` query params. We
// take both RFC3339 with second precision and nanosecond precision so a
// JS `new Date().toISOString()` value works out of the box.
var evalDatasetQueryTimestampFormats = []string{time.RFC3339Nano, time.RFC3339}

const evalDatasetRoutePrefix = "/api/v1/evals/datasets/"

type createEvalDatasetRequest struct {
	Name        string            `json:"name"`
	Version     int               `json:"version"`
	Description string            `json:"description,omitempty"`
	Entries     []model.EvalEntry `json:"entries"`
}

type updateEvalDatasetRequest struct {
	Version     *int              `json:"version,omitempty"`
	Description *string           `json:"description,omitempty"`
	Entries     []model.EvalEntry `json:"entries"`
}

type evalDatasetsListResponse struct {
	Items      []model.EvalDataset `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type evalDatasetVersionsResponse struct {
	Items []model.EvalDataset `json:"items"`
}

func (s *server) handleEvalDatasetSubroutes(w http.ResponseWriter, r *http.Request) {
	subpath := strings.Trim(strings.TrimPrefix(r.URL.Path, evalDatasetRoutePrefix), "/")
	if subpath == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(subpath, "/")
	switch {
	case len(parts) == 2 && parts[0] == "by-name":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		r.SetPathValue("name", parts[1])
		s.handleListEvalDatasetVersions(w, r)
		return
	case len(parts) == 4 && parts[0] == "by-name" && parts[2] == "versions":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		r.SetPathValue("name", parts[1])
		r.SetPathValue("version", parts[3])
		s.handleGetEvalDatasetByNameVersion(w, r)
		return
	case len(parts) == 1:
		r.SetPathValue("id", parts[0])
		switch r.Method {
		case http.MethodGet:
			s.handleGetEvalDataset(w, r)
		case http.MethodPut:
			s.handleUpdateEvalDataset(w, r)
		case http.MethodDelete:
			s.handleDeleteEvalDataset(w, r)
		default:
			writeMethodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
		return
	case len(parts) == 2 && parts[1] == "run":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		r.SetPathValue("id", parts[0])
		s.handleRunEvalDataset(w, r)
		return
	case len(parts) == 2 && parts[1] == "runs":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		r.SetPathValue("id", parts[0])
		s.handleListEvalRuns(w, r)
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	if len(methods) > 0 {
		w.Header().Set("Allow", strings.Join(methods, ", "))
	}
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

func decodeEvalDatasetJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Enforce the body cap before decoding so a pathologically large body
	// returns 413 rather than OOMing the decoder. http.MaxBytesReader
	// both caps the read and surfaces a typed error on overflow.
	r.Body = http.MaxBytesReader(w, r.Body, maxEvalDatasetRequestBytes)

	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, errorCodeEvalDatasetValidationFailed,
				"eval dataset request body exceeds the 16 MiB cap; split the dataset along a meaningful axis (tenant, topic, risk tier) rather than raising the limit")
			return false
		}
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalDatasetValidationFailed, "invalid json: "+err.Error())
		return false
	}
	return true
}

func (s *server) handleCreateEvalDataset(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsWrite, "admin") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	var req createEvalDatasetRequest
	if !decodeEvalDatasetJSON(w, r, &req) {
		return
	}

	dataset := model.EvalDataset{
		Name:        req.Name,
		Version:     req.Version,
		Tenant:      tenant,
		Description: req.Description,
		Entries:     req.Entries,
		CreatedBy:   policybundles.PolicyActorID(r),
	}

	created, err := s.evalDatasetStore.CreateEvalDataset(r.Context(), dataset)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetVersionExists) {
			writeJSONError(w, http.StatusConflict, errorCodeEvalDatasetVersionConflict,
				"eval dataset with that (name, version) already exists — create a new version to change entries")
			return
		}
		// Validate errors + marshaling errors reach this path. We
		// surface them as 400 because they indicate caller-visible
		// input problems; pure server errors (redis down, etc.) would
		// have been wrapped with infrastructure context upstream.
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalDatasetValidationFailed, err.Error())
		return
	}

	slog.Info("eval dataset created",
		"dataset_id", created.ID,
		"name", created.Name,
		"version", created.Version,
		"tenant", tenant,
		"entry_count", created.EntryCount,
		"content_hash", created.ContentHash,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "create", "eval_dataset", created.ID, created.Name,
		policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		"create eval dataset "+created.Name+" v"+strconv.Itoa(created.Version))

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (s *server) handleUpdateEvalDataset(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsWrite, "admin") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "eval dataset id required")
		return
	}

	base, err := s.evalDatasetStore.GetEvalDataset(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset (pre-update)", err)
		return
	}

	var req updateEvalDatasetRequest
	if !decodeEvalDatasetJSON(w, r, &req) {
		return
	}

	targetVersion := base.Version + 1
	if req.Version != nil {
		targetVersion = *req.Version
	}
	if targetVersion <= base.Version {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalDatasetValidationFailed, "version must be greater than the base dataset version")
		return
	}

	description := base.Description
	if req.Description != nil {
		description = *req.Description
	}

	dataset := model.EvalDataset{
		Name:        base.Name,
		Version:     targetVersion,
		Tenant:      tenant,
		Description: description,
		Entries:     req.Entries,
		CreatedBy:   policybundles.PolicyActorID(r),
	}

	created, err := s.evalDatasetStore.CreateEvalDataset(r.Context(), dataset)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetVersionExists) {
			writeJSONError(w, http.StatusConflict, errorCodeEvalDatasetVersionConflict,
				"eval dataset successor version already exists — choose a higher version or update from the latest dataset")
			return
		}
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalDatasetValidationFailed, err.Error())
		return
	}

	slog.Info("eval dataset successor version created",
		"base_dataset_id", base.ID,
		"base_version", base.Version,
		"dataset_id", created.ID,
		"name", created.Name,
		"version", created.Version,
		"tenant", tenant,
		"entry_count", created.EntryCount,
		"content_hash", created.ContentHash,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "update", "eval_dataset", created.ID, created.Name,
		policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		"create successor version for eval dataset "+created.Name+" v"+strconv.Itoa(created.Version))

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (s *server) handleListEvalDatasets(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	q := r.URL.Query()
	cursor := strings.TrimSpace(q.Get("cursor"))
	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeErrorJSON(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > 200 {
			writeErrorJSON(w, http.StatusBadRequest, "limit must be <= 200")
			return
		}
		limit = n
	}

	filter := model.EvalDatasetFilter{
		Tenant:     tenant,
		NamePrefix: strings.TrimSpace(q.Get("name_prefix")),
	}

	if raw := strings.TrimSpace(q.Get("created_after")); raw != "" {
		ms, err := parseEvalDatasetQueryTimestamp(raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "created_after must be RFC3339")
			return
		}
		filter.CreatedAfterMS = ms
	}
	if raw := strings.TrimSpace(q.Get("created_before")); raw != "" {
		ms, err := parseEvalDatasetQueryTimestamp(raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "created_before must be RFC3339")
			return
		}
		filter.CreatedBeforeMS = ms
	}
	if filter.CreatedAfterMS > 0 && filter.CreatedBeforeMS > 0 && filter.CreatedAfterMS > filter.CreatedBeforeMS {
		writeErrorJSON(w, http.StatusBadRequest, "created_after must be <= created_before")
		return
	}

	page, err := s.evalDatasetStore.ListEvalDatasets(r.Context(), tenant, filter, cursor, limit)
	if err != nil {
		writeInternalError(w, r, "list eval datasets", err)
		return
	}

	resp := evalDatasetsListResponse{
		Items:      page.Items,
		NextCursor: page.NextCursor,
	}
	if resp.Items == nil {
		resp.Items = []model.EvalDataset{}
	}
	writeJSON(w, resp)
}

func (s *server) handleGetEvalDataset(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "eval dataset id required")
		return
	}

	dataset, err := s.evalDatasetStore.GetEvalDataset(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset", err)
		return
	}
	writeJSON(w, dataset)
}

func (s *server) handleListEvalDatasetVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "eval dataset name required")
		return
	}

	versions, err := s.evalDatasetStore.ListEvalDatasetVersions(r.Context(), tenant, name)
	if err != nil {
		writeInternalError(w, r, "list eval dataset versions", err)
		return
	}
	if versions == nil {
		versions = []model.EvalDataset{}
	}
	writeJSON(w, evalDatasetVersionsResponse{Items: versions})
}

func (s *server) handleGetEvalDatasetByNameVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "eval dataset name required")
		return
	}

	versionRaw := strings.TrimSpace(r.PathValue("version"))
	version, err := strconv.Atoi(versionRaw)
	if err != nil || version < 1 {
		writeErrorJSON(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}

	dataset, err := s.evalDatasetStore.GetEvalDatasetByNameVersion(r.Context(), tenant, name, version)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset by name", err)
		return
	}
	writeJSON(w, dataset)
}

func (s *server) handleDeleteEvalDataset(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsDelete, "admin") {
		return
	}
	if s.evalDatasetStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval dataset store unavailable")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "eval dataset id required")
		return
	}

	// The immutability rail forbids casual deletion. The `force=true`
	// query flag is the explicit admin escape hatch operators must
	// supply — it is intentionally awkward so an accidental curl never
	// wipes curated history. Any value other than exactly "true" is
	// rejected.
	force := strings.TrimSpace(r.URL.Query().Get("force"))
	if force != "true" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalDatasetValidationFailed,
			"eval dataset delete requires force=true; datasets are immutable by design — create a new version instead, or supply force=true to wipe the record")
		return
	}

	// Look up before delete so we have the name/version to include in the
	// audit event. Unknown id returns 404 rather than a silent no-op so
	// operators notice typos.
	existing, err := s.evalDatasetStore.GetEvalDataset(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset (pre-delete)", err)
		return
	}

	if err := s.evalDatasetStore.DeleteEvalDataset(r.Context(), tenant, id); err != nil {
		writeInternalError(w, r, "delete eval dataset", err)
		return
	}

	slog.Info("eval dataset deleted (force)",
		"dataset_id", id,
		"name", existing.Name,
		"version", existing.Version,
		"tenant", tenant,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "delete", "eval_dataset", id, existing.Name,
		policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		"force-delete eval dataset "+existing.Name+" v"+strconv.Itoa(existing.Version))

	w.WriteHeader(http.StatusNoContent)
}

// parseEvalDatasetQueryTimestamp accepts both RFC3339 and RFC3339Nano and
// returns the unix-milli representation used by the store filter.
func parseEvalDatasetQueryTimestamp(raw string) (int64, error) {
	for _, layout := range evalDatasetQueryTimestampFormats {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().UnixMilli(), nil
		}
	}
	return 0, errors.New("invalid timestamp")
}
