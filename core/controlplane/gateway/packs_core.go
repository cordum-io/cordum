package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/locks"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

const (
	packRegistryScope = "system"
	packRegistryID    = "packs"

	packCatalogScope        = "system"
	packCatalogID           = "pack_catalogs"
	defaultPackCatalogID    = "official"
	defaultPackCatalogTitle = "Cordum Official"
	defaultPackCatalogURL   = "https://packs.cordum.io/catalog.json"

	envPackCatalogID             = "CORDUM_PACK_CATALOG_ID"
	envPackCatalogTitle          = "CORDUM_PACK_CATALOG_TITLE"
	envPackCatalogURL            = "CORDUM_PACK_CATALOG_URL"
	envPackCatalogDisableDefault = "CORDUM_PACK_CATALOG_DEFAULT_DISABLED"
	envMarketplaceAllowHTTP      = "CORDUM_MARKETPLACE_ALLOW_HTTP"
	envMarketplaceHTTPTimeout    = "CORDUM_MARKETPLACE_HTTP_TIMEOUT"

	policyConfigScope = "system"
	policyConfigID    = "policy"
	policyConfigKey   = "bundles"

	maxPackUploadBytes       = 64 << 20
	maxPackFiles             = 2048
	maxPackFileBytes         = 32 << 20
	maxPackUncompressedBytes = 256 << 20
	maxCatalogBytes          = 8 << 20

	marketplaceCacheTTL           = 30 * time.Second
	defaultMarketplaceHTTPTimeout = 15 * time.Second
)

type packManifest struct {
	APIVersion    string            `yaml:"apiVersion"`
	Kind          string            `yaml:"kind"`
	Metadata      packMetadata      `yaml:"metadata"`
	Compatibility packCompatibility `yaml:"compatibility"`
	Topics        []packTopic       `yaml:"topics"`
	Resources     packResources     `yaml:"resources"`
	Overlays      packOverlays      `yaml:"overlays"`
	Tests         packTests         `yaml:"tests"`
}

type packMetadata struct {
	ID          string `yaml:"id" json:"id"`
	Version     string `yaml:"version" json:"version"`
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description"`
}

type packCompatibility struct {
	ProtocolVersion int    `yaml:"protocolVersion" json:"protocolVersion"`
	MinCoreVersion  string `yaml:"minCoreVersion" json:"minCoreVersion"`
	MaxCoreVersion  string `yaml:"maxCoreVersion" json:"maxCoreVersion"`
}

type packTopic struct {
	Name       string   `yaml:"name" json:"name"`
	Requires   []string `yaml:"requires" json:"requires"`
	RiskTags   []string `yaml:"riskTags" json:"riskTags"`
	Capability string   `yaml:"capability" json:"capability"`
}

type packResources struct {
	Schemas   []packResource `yaml:"schemas" json:"schemas"`
	Workflows []packResource `yaml:"workflows" json:"workflows"`
}

type packResource struct {
	ID   string `yaml:"id" json:"id"`
	Path string `yaml:"path" json:"path"`
}

type packOverlays struct {
	Config []packConfigOverlay `yaml:"config" json:"config"`
	Policy []packPolicyOverlay `yaml:"policy" json:"policy"`
}

type packConfigOverlay struct {
	Name     string `yaml:"name" json:"name"`
	Scope    string `yaml:"scope" json:"scope"`
	ScopeID  string `yaml:"scope_id" json:"scope_id"`
	Key      string `yaml:"key" json:"key"`
	Format   string `yaml:"format" json:"format"`
	Strategy string `yaml:"strategy" json:"strategy"`
	Path     string `yaml:"path" json:"path"`
}

type packPolicyOverlay struct {
	Name     string `yaml:"name" json:"name"`
	Strategy string `yaml:"strategy" json:"strategy"`
	Path     string `yaml:"path" json:"path"`
}

type packTests struct {
	PolicySimulations []packPolicySimulation `yaml:"policySimulations" json:"policySimulations"`
}

type packPolicySimulation struct {
	Name           string                      `yaml:"name" json:"name"`
	Request        packPolicySimulationRequest `yaml:"request" json:"request"`
	ExpectDecision string                      `yaml:"expectDecision" json:"expectDecision"`
}

type packPolicySimulationRequest struct {
	TenantId   string   `yaml:"tenantId" json:"tenantId"`
	Topic      string   `yaml:"topic" json:"topic"`
	Capability string   `yaml:"capability" json:"capability"`
	RiskTags   []string `yaml:"riskTags" json:"riskTags"`
	Requires   []string `yaml:"requires" json:"requires"`
	PackId     string   `yaml:"packId" json:"packId"`
	ActorId    string   `yaml:"actorId" json:"actorId"`
	ActorType  string   `yaml:"actorType" json:"actorType"`
}

type packRecord struct {
	ID          string              `json:"id"`
	Version     string              `json:"version"`
	Status      string              `json:"status"`
	InstalledAt string              `json:"installed_at,omitempty"`
	InstalledBy string              `json:"installed_by,omitempty"`
	Manifest    packRecordManifest  `json:"manifest,omitempty"`
	Resources   packRecordResources `json:"resources,omitempty"`
	Overlays    packRecordOverlays  `json:"overlays,omitempty"`
	Tests       packTests           `json:"tests,omitempty"`
}

type packRecordManifest struct {
	Metadata      packMetadata      `json:"metadata"`
	Compatibility packCompatibility `json:"compatibility,omitempty"`
	Topics        []packTopic       `json:"topics,omitempty"`
}

type packRecordResources struct {
	Schemas   map[string]string `json:"schemas,omitempty"`
	Workflows map[string]string `json:"workflows,omitempty"`
}

type packRecordOverlays struct {
	Config []packAppliedConfigOverlay `json:"config,omitempty"`
	Policy []packAppliedPolicyOverlay `json:"policy,omitempty"`
}

type packAppliedConfigOverlay struct {
	Name    string         `json:"name"`
	Scope   string         `json:"scope"`
	ScopeID string         `json:"scope_id"`
	Key     string         `json:"key"`
	Patch   map[string]any `json:"patch"`
}

type packAppliedPolicyOverlay struct {
	Name       string `json:"name"`
	FragmentID string `json:"fragment_id"`
}

type schemaPlan struct {
	ID          string
	Schema      map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

type workflowPlan struct {
	ID          string
	Workflow    map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

type appliedConfigChange struct {
	Overlay  packAppliedConfigOverlay
	Previous any
}

type appliedPolicyChange struct {
	Overlay     packAppliedPolicyOverlay
	Previous    any
	HadPrevious bool
}

type packVerifyResult struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Got      string `json:"got"`
	Reason   string `json:"reason"`
	Ok       bool   `json:"ok"`
}

type packInstallOptions struct {
	Force       bool
	Upgrade     bool
	Inactive    bool
	Owner       string
	InstalledBy string
}

type packInstallError struct {
	Status int
	Err    error
}

func (e *packInstallError) Error() string {
	if e == nil || e.Err == nil {
		return "pack install failed"
	}
	return e.Err.Error()
}

func (s *server) handleListPacks(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]packRecord, 0, len(records))
	for _, rec := range records {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

func (s *server) handleGetPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pack id required")
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec, ok := records[packID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, rec)
}

func (s *server) handleInstallPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.schemaRegistry == nil || s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "pack dependencies unavailable")
		return
	}
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackUploadBytes)
	if err := r.ParseMultipartForm(maxPackUploadBytes); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "bundle file required")
		return
	}
	defer file.Close()
	if header != nil && header.Filename != "" && !isTarGz(header.Filename) {
		writeErrorJSON(w, http.StatusBadRequest, "bundle must be .tgz")
		return
	}
	bundleDir, cleanup, err := loadPackBundleFromReader(file)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	record, err := s.installPackFromDir(r.Context(), bundleDir, packInstallOptions{
		Force:       parseBool(r.FormValue("force")),
		Upgrade:     parseBool(r.FormValue("upgrade")),
		Inactive:    parseBool(r.FormValue("inactive")),
		Owner:       packLockOwner(r),
		InstalledBy: strings.TrimSpace(policyActorID(r)),
	})
	if err != nil {
		status := http.StatusBadRequest
		var installErr *packInstallError
		if errors.As(err, &installErr) {
			status = installErr.Status
		}
		writeErrorJSON(w, status, err.Error())
		return
	}

	s.appendAuditEntryNamed(r.Context(), "install", "pack", record.ID, record.Manifest.Metadata.Title, policyActorID(r), policyRole(r), "install pack "+record.ID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, record)
}

func (s *server) installPackFromDir(ctx context.Context, bundleDir string, opts packInstallOptions) (packRecord, error) {
	if s == nil {
		return packRecord{}, &packInstallError{Status: http.StatusServiceUnavailable, Err: errors.New("gateway unavailable")}
	}
	manifest, err := loadPackManifest(bundleDir)
	if err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if err := validatePackManifest(manifest); err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if err := ensureProtocolCompatible(manifest); err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if manifest.Compatibility.MinCoreVersion != "" && !opts.Force {
		if err := ensureCoreVersionCompatible(manifest.Compatibility.MinCoreVersion); err != nil {
			return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
		}
	}
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = packLockOwner(nil)
	}
	release, err := acquirePackLocks(ctx, s.lockStore, manifest.Metadata.ID, owner)
	if err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusConflict, Err: err}
	}
	defer release()

	schemaPlans, err := s.planSchemas(ctx, bundleDir, manifest, opts.Upgrade)
	if err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
	}
	workflowPlans, err := s.planWorkflows(ctx, bundleDir, manifest, opts.Upgrade)
	if err != nil {
		return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
	}

	appliedConfig := []packAppliedConfigOverlay{}
	appliedPolicy := []packAppliedPolicyOverlay{}
	appliedConfigChanges := []appliedConfigChange{}
	appliedPolicyChanges := []appliedPolicyChange{}
	appliedSchemas := []schemaPlan{}
	appliedWorkflows := []workflowPlan{}
	appliedSchemaDigests := map[string]string{}
	appliedWorkflowDigests := map[string]string{}
	for _, plan := range schemaPlans {
		appliedSchemaDigests[plan.ID] = plan.Digest
	}
	for _, plan := range workflowPlans {
		appliedWorkflowDigests[plan.ID] = plan.Digest
	}

	rollback := func() {
		for i := len(appliedConfigChanges) - 1; i >= 0; i-- {
			_ = s.restoreConfigOverlay(ctx, appliedConfigChanges[i])
		}
		for i := len(appliedPolicyChanges) - 1; i >= 0; i-- {
			_ = s.restorePolicyOverlay(ctx, appliedPolicyChanges[i])
		}
		for i := len(appliedWorkflows) - 1; i >= 0; i-- {
			_ = s.rollbackWorkflow(ctx, appliedWorkflows[i])
		}
		for i := len(appliedSchemas) - 1; i >= 0; i-- {
			_ = s.rollbackSchema(ctx, appliedSchemas[i])
		}
	}

	for _, plan := range schemaPlans {
		if plan.Noop {
			continue
		}
		if err := s.registerSchema(ctx, plan.ID, plan.Schema); err != nil {
			rollback()
			return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
		}
		appliedSchemas = append(appliedSchemas, plan)
	}
	for _, plan := range workflowPlans {
		if plan.Noop {
			continue
		}
		if err := s.registerWorkflow(ctx, plan.Workflow); err != nil {
			rollback()
			return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
		}
		appliedWorkflows = append(appliedWorkflows, plan)
	}

	for _, overlay := range manifest.Overlays.Config {
		if shouldSkipConfigOverlay(opts.Inactive, overlay) {
			continue
		}
		applied, err := s.applyConfigOverlay(ctx, overlay, manifest.Metadata.ID, bundleDir)
		if err != nil {
			rollback()
			return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
		}
		if applied.Overlay.Name != "" {
			appliedConfig = append(appliedConfig, applied.Overlay)
			appliedConfigChanges = append(appliedConfigChanges, applied)
		}
	}
	for _, overlay := range manifest.Overlays.Policy {
		applied, err := s.applyPolicyOverlay(ctx, overlay, manifest.Metadata.ID, manifest.Metadata.Version, bundleDir)
		if err != nil {
			rollback()
			return packRecord{}, &packInstallError{Status: http.StatusBadRequest, Err: err}
		}
		if applied.Overlay.Name != "" {
			appliedPolicy = append(appliedPolicy, applied.Overlay)
			appliedPolicyChanges = append(appliedPolicyChanges, applied)
		}
	}

	status := "ACTIVE"
	if opts.Inactive || !hasPoolOverlay(appliedConfig) {
		status = "INACTIVE"
	}
	installedBy := strings.TrimSpace(opts.InstalledBy)

	record := packRecord{
		ID:          manifest.Metadata.ID,
		Version:     manifest.Metadata.Version,
		Status:      status,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		InstalledBy: installedBy,
		Manifest: packRecordManifest{
			Metadata:      manifest.Metadata,
			Compatibility: manifest.Compatibility,
			Topics:        manifest.Topics,
		},
		Resources: packRecordResources{
			Schemas:   appliedSchemaDigests,
			Workflows: appliedWorkflowDigests,
		},
		Overlays: packRecordOverlays{
			Config: appliedConfig,
			Policy: appliedPolicy,
		},
		Tests: manifest.Tests,
	}
	if err := s.updatePackRegistry(ctx, record); err != nil {
		rollback()
		return packRecord{}, &packInstallError{Status: http.StatusInternalServerError, Err: err}
	}
	return record, nil
}

func (s *server) handleUninstallPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.workflowStore == nil || s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "pack dependencies unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pack id required")
		return
	}
	purge := false
	if r.Method == http.MethodPost {
		var body struct {
			Purge bool `json:"purge"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		purge = body.Purge
	}
	owner := packLockOwner(r)
	release, err := acquirePackLocks(r.Context(), s.lockStore, packID, owner)
	if err != nil {
		writeErrorJSON(w, http.StatusConflict, err.Error())
		return
	}
	defer release()

	records, doc, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec, ok := records[packID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "pack not installed")
		return
	}
	for i := len(rec.Overlays.Config) - 1; i >= 0; i-- {
		if err := s.removeConfigOverlay(r.Context(), rec.Overlays.Config[i]); err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, overlay := range rec.Overlays.Policy {
		if err := s.removePolicyOverlay(r.Context(), overlay); err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if purge {
		for wfID := range rec.Resources.Workflows {
			_ = s.workflowStore.DeleteWorkflow(r.Context(), wfID)
		}
		for schemaID := range rec.Resources.Schemas {
			_ = s.schemaRegistry.Delete(r.Context(), schemaID)
		}
	}
	rec.Status = "DISABLED"
	records[packID] = rec
	if err := s.savePackRegistry(r.Context(), records, doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendAuditEntryNamed(r.Context(), "uninstall", "pack", packID, rec.Manifest.Metadata.Title, policyActorID(r), policyRole(r), "uninstall pack "+packID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, rec)
}

func (s *server) handleVerifyPack(w http.ResponseWriter, r *http.Request) {
	if s.safetyClient == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pack id required")
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec, ok := records[packID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "pack not installed")
		return
	}
	results := make([]packVerifyResult, 0, len(rec.Tests.PolicySimulations))
	for _, test := range rec.Tests.PolicySimulations {
		result := packVerifyResult{Name: test.Name, Expected: normalizeDecision(test.ExpectDecision)}
		got, reason, err := s.runPolicySimulation(r.Context(), test, packID)
		if err != nil {
			result.Got = "ERROR"
			result.Reason = err.Error()
			result.Ok = false
		} else {
			result.Got = normalizeDecision(got)
			result.Reason = reason
			result.Ok = result.Got == result.Expected
		}
		results = append(results, result)
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"pack_id": packID,
		"results": results,
	})
}

func (s *server) planSchemas(ctx context.Context, dir string, manifest *packManifest, upgrade bool) ([]schemaPlan, error) {
	plans := make([]schemaPlan, 0, len(manifest.Resources.Schemas))
	for _, ref := range manifest.Resources.Schemas {
		schemaMap, digest, err := loadSchemaFile(dir, ref.Path)
		if err != nil {
			return nil, err
		}
		plan := schemaPlan{ID: ref.ID, Schema: schemaMap, Digest: digest}
		if s.schemaRegistry != nil {
			if existing, err := s.schemaRegistry.Get(ctx, ref.ID); err == nil {
				var existingMap map[string]any
				if err := json.Unmarshal(existing, &existingMap); err == nil {
					if normalized, ok := normalizeJSON(existingMap).(map[string]any); ok {
						plan.Existing = normalized
						plan.HadExisting = true
						existingDigest, err := hashValue(plan.Existing)
						if err != nil {
							return nil, err
						}
						if existingDigest == digest {
							plan.Noop = true
						} else if !upgrade {
							return nil, fmt.Errorf("schema %s exists; rerun with upgrade", ref.ID)
						}
					}
				}
			} else if err != nil && !errors.Is(err, redis.Nil) {
				return nil, err
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (s *server) planWorkflows(ctx context.Context, dir string, manifest *packManifest, upgrade bool) ([]workflowPlan, error) {
	plans := make([]workflowPlan, 0, len(manifest.Resources.Workflows))
	for _, ref := range manifest.Resources.Workflows {
		workflowMap, digest, err := loadWorkflowFile(dir, ref.Path, ref.ID)
		if err != nil {
			return nil, err
		}
		plan := workflowPlan{ID: ref.ID, Workflow: workflowMap, Digest: digest}
		existing, err := s.workflowStore.GetWorkflow(ctx, ref.ID)
		if err == nil {
			plan.HadExisting = true
			plan.Existing = workflowToMap(existing)
			existingDigest, err := hashWorkflow(plan.Existing)
			if err != nil {
				return nil, err
			}
			if existingDigest == digest {
				plan.Noop = true
			} else if !upgrade {
				return nil, fmt.Errorf("workflow %s exists; rerun with upgrade", ref.ID)
			}
		} else if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (s *server) registerSchema(ctx context.Context, id string, schemaMap map[string]any) error {
	if s.schemaRegistry == nil {
		return errors.New("schema registry unavailable")
	}
	payload, err := json.Marshal(schemaMap)
	if err != nil {
		return err
	}
	return s.schemaRegistry.Register(ctx, id, payload)
}

func (s *server) registerWorkflow(ctx context.Context, workflowMap map[string]any) error {
	if s.workflowStore == nil {
		return errors.New("workflow store unavailable")
	}
	data, err := json.Marshal(workflowMap)
	if err != nil {
		return err
	}
	var req createWorkflowRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return err
	}
	return s.saveWorkflowRequest(ctx, &req)
}

func (s *server) saveWorkflowRequest(ctx context.Context, req *createWorkflowRequest) error {
	if req == nil {
		return errors.New("workflow request required")
	}
	if req.ID == "" {
		return errors.New("workflow id required")
	}
	if existing, err := s.workflowStore.GetWorkflow(ctx, req.ID); err == nil && existing != nil {
		if req.OrgID == "" {
			req.OrgID = existing.OrgID
		}
		if req.TeamID == "" {
			req.TeamID = existing.TeamID
		}
		if req.Name == "" {
			req.Name = existing.Name
		}
		if req.Description == "" {
			req.Description = existing.Description
		}
		if req.Version == "" {
			req.Version = existing.Version
		}
		if req.TimeoutSec == 0 {
			req.TimeoutSec = existing.TimeoutSec
		}
		if req.CreatedBy == "" {
			req.CreatedBy = existing.CreatedBy
		}
		if req.InputSchema == nil && existing.InputSchema != nil {
			req.InputSchema = existing.InputSchema
		}
		if req.Parameters == nil && existing.Parameters != nil {
			req.Parameters = existing.Parameters
		}
		if req.Config == nil && existing.Config != nil {
			req.Config = existing.Config
		}
	}
	wfDef := &wf.Workflow{
		ID:          req.ID,
		OrgID:       req.OrgID,
		TeamID:      req.TeamID,
		Name:        req.Name,
		Description: req.Description,
		Version:     req.Version,
		TimeoutSec:  req.TimeoutSec,
		Config:      req.Config,
		InputSchema: req.InputSchema,
		Parameters:  req.Parameters,
		CreatedBy:   req.CreatedBy,
		Steps:       map[string]*wf.Step{},
	}
	for id, step := range req.Steps {
		s := step
		s.ID = id
		wfDef.Steps[id] = &s
	}
	return s.workflowStore.SaveWorkflow(ctx, wfDef)
}

func (s *server) rollbackSchema(ctx context.Context, plan schemaPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return s.registerSchema(ctx, plan.ID, plan.Existing)
	}
	return s.schemaRegistry.Delete(ctx, plan.ID)
}

func (s *server) rollbackWorkflow(ctx context.Context, plan workflowPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return s.registerWorkflow(ctx, plan.Existing)
	}
	return s.workflowStore.DeleteWorkflow(ctx, plan.ID)
}

func (s *server) applyConfigOverlay(ctx context.Context, overlay packConfigOverlay, packID, dir string) (appliedConfigChange, error) {
	key := strings.TrimSpace(overlay.Key)
	if key == "" {
		return appliedConfigChange{}, errors.New("config overlay key required")
	}
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "json_merge_patch" {
		return appliedConfigChange{}, fmt.Errorf("unsupported config overlay strategy %q", strategy)
	}
	patch, err := loadPatchFile(dir, overlay.Path)
	if err != nil {
		return appliedConfigChange{}, err
	}
	patchMap, ok := patch.(map[string]any)
	if !ok {
		return appliedConfigChange{}, errors.New("config overlay patch must be a map")
	}
	scope := strings.TrimSpace(overlay.Scope)
	if scope == "" {
		scope = "system"
	}
	scopeID := strings.TrimSpace(overlay.ScopeID)
	if scope == "system" && scopeID == "" {
		scopeID = "default"
	}
	doc, err := getConfigDoc(ctx, s.configSvc, scope, scopeID)
	if err != nil {
		return appliedConfigChange{}, err
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	current := normalizeJSON(doc.Data[key])
	if err := validateConfigPatch(key, patchMap, packID, current); err != nil {
		return appliedConfigChange{}, err
	}
	before := deepCopy(current)
	updated := mergePatch(current, patchMap)
	doc.Data[key] = updated
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return appliedConfigChange{}, err
	}
	return appliedConfigChange{
		Overlay: packAppliedConfigOverlay{
			Name:    overlay.Name,
			Scope:   scope,
			ScopeID: scopeID,
			Key:     key,
			Patch:   patchMap,
		},
		Previous: before,
	}, nil
}

func (s *server) removeConfigOverlay(ctx context.Context, overlay packAppliedConfigOverlay) error {
	doc, err := getConfigDoc(ctx, s.configSvc, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}
	if doc.Data == nil {
		return nil
	}
	current := normalizeJSON(doc.Data[overlay.Key])
	if current == nil {
		return nil
	}
	deletePatch := buildDeletePatch(overlay.Patch)
	updated := mergePatch(current, deletePatch)
	doc.Data[overlay.Key] = updated
	return s.configSvc.Set(ctx, doc)
}

func (s *server) restoreConfigOverlay(ctx context.Context, change appliedConfigChange) error {
	overlay := change.Overlay
	doc, err := getConfigDoc(ctx, s.configSvc, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(overlay.Scope), ScopeID: overlay.ScopeID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if change.Previous == nil {
		delete(doc.Data, overlay.Key)
	} else {
		doc.Data[overlay.Key] = deepCopy(change.Previous)
	}
	return s.configSvc.Set(ctx, doc)
}

func (s *server) applyPolicyOverlay(ctx context.Context, overlay packPolicyOverlay, packID, packVersion, dir string) (appliedPolicyChange, error) {
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "bundle_fragment" {
		return appliedPolicyChange{}, fmt.Errorf("unsupported policy overlay strategy %q", strategy)
	}
	path, err := safeJoin(dir, overlay.Path)
	if err != nil {
		return appliedPolicyChange{}, err
	}
	// #nosec G304 -- path is validated by safeJoin.
	content, err := os.ReadFile(path)
	if err != nil {
		return appliedPolicyChange{}, err
	}
	fragmentID := policyFragmentID(packID, overlay.Name)
	doc, err := getConfigDoc(ctx, s.configSvc, policyConfigScope, policyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return appliedPolicyChange{}, err
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(policyConfigScope), ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	previous, hadPrevious := bundles[fragmentID]
	installedAt := time.Now().UTC().Format(time.RFC3339)
	sum := sha256.Sum256(content)
	bundles[fragmentID] = map[string]any{
		"content":      string(content),
		"version":      packVersion,
		"sha256":       hex.EncodeToString(sum[:]),
		"installed_at": installedAt,
	}
	doc.Data[policyConfigKey] = bundles
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return appliedPolicyChange{}, err
	}
	return appliedPolicyChange{
		Overlay: packAppliedPolicyOverlay{
			Name:       overlay.Name,
			FragmentID: fragmentID,
		},
		Previous:    deepCopy(previous),
		HadPrevious: hadPrevious,
	}, nil
}

func (s *server) removePolicyOverlay(ctx context.Context, overlay packAppliedPolicyOverlay) error {
	doc, err := getConfigDoc(ctx, s.configSvc, policyConfigScope, policyConfigID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, ok := rawBundles.(map[string]any)
	if !ok || bundles == nil {
		return nil
	}
	delete(bundles, overlay.FragmentID)
	doc.Data[policyConfigKey] = bundles
	return s.configSvc.Set(ctx, doc)
}

func (s *server) restorePolicyOverlay(ctx context.Context, change appliedPolicyChange) error {
	doc, err := getConfigDoc(ctx, s.configSvc, policyConfigScope, policyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(policyConfigScope), ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	if !change.HadPrevious {
		delete(bundles, change.Overlay.FragmentID)
	} else {
		bundles[change.Overlay.FragmentID] = deepCopy(change.Previous)
	}
	doc.Data[policyConfigKey] = bundles
	return s.configSvc.Set(ctx, doc)
}

func (s *server) runPolicySimulation(ctx context.Context, test packPolicySimulation, packID string) (string, string, error) {
	if test.Request.Topic == "" {
		return "", "", fmt.Errorf("policy simulation %q missing topic", test.Name)
	}
	request := policyCheckRequest{
		Topic:  test.Request.Topic,
		Tenant: test.Request.TenantId,
		Meta: &policyMetaRequest{
			TenantId:   test.Request.TenantId,
			Capability: test.Request.Capability,
			RiskTags:   test.Request.RiskTags,
			Requires:   test.Request.Requires,
			PackId:     test.Request.PackId,
			ActorId:    test.Request.ActorId,
			ActorType:  test.Request.ActorType,
		},
	}
	if auth := authFromContext(ctx); auth != nil {
		if auth.Tenant != "" {
			request.Tenant = auth.Tenant
			if request.Meta != nil {
				request.Meta.TenantId = auth.Tenant
			}
		}
		if auth.PrincipalID != "" && request.Meta != nil && request.Meta.ActorId == "" {
			request.Meta.ActorId = auth.PrincipalID
		}
	}
	if request.Meta != nil {
		if request.Meta.PackId == "" {
			request.Meta.PackId = packID
		}
		if request.Meta.TenantId == "" {
			request.Meta.TenantId = s.tenant
		}
	}
	checkReq, err := buildPolicyCheckRequest(ctx, &request, s.configSvc, s.tenant)
	if err != nil {
		return "", "", err
	}
	resp, err := s.safetyClient.Simulate(ctx, checkReq)
	if err != nil {
		return "", "", err
	}
	decision := resp.GetDecision().String()
	return decision, resp.GetReason(), nil
}

func normalizeDecision(raw string) string {
	val := strings.ToUpper(strings.TrimSpace(raw))
	switch val {
	case "DECISION_TYPE_ALLOW", "ALLOW":
		return "ALLOW"
	case "DECISION_TYPE_DENY", "DENY":
		return "DENY"
	case "DECISION_TYPE_REQUIRE_HUMAN", "REQUIRE_APPROVAL", "REQUIRE_HUMAN":
		return "REQUIRE_APPROVAL"
	case "DECISION_TYPE_ALLOW_WITH_CONSTRAINTS", "ALLOW_WITH_CONSTRAINTS":
		return "ALLOW_WITH_CONSTRAINTS"
	case "DECISION_TYPE_THROTTLE", "THROTTLE":
		return "THROTTLE"
	default:
		return val
	}
}

func (s *server) loadPackRegistry(ctx context.Context) (map[string]packRecord, *configsvc.Document, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packRegistryScope), packRegistryID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]packRecord{}, nil, nil
		}
		return nil, nil, err
	}
	records := map[string]packRecord{}
	raw := normalizeJSON(doc.Data["installed"])
	if raw == nil {
		return records, doc, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, nil, err
	}
	return records, doc, nil
}

func (s *server) updatePackRegistry(ctx context.Context, record packRecord) error {
	records, doc, err := s.loadPackRegistry(ctx)
	if err != nil {
		return err
	}
	records[record.ID] = record
	return s.savePackRegistry(ctx, records, doc)
}

func (s *server) savePackRegistry(ctx context.Context, records map[string]packRecord, doc *configsvc.Document) error {
	if doc == nil {
		doc = &configsvc.Document{Scope: configsvc.Scope(packRegistryScope), ScopeID: packRegistryID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	doc.Data["installed"] = recordsToAny(records)
	return s.configSvc.Set(ctx, doc)
}

func recordsToAny(records map[string]packRecord) map[string]any {
	data, err := json.Marshal(records)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func getConfigDoc(ctx context.Context, svc *configsvc.Service, scope, scopeID string) (*configsvc.Document, error) {
	if svc == nil {
		return nil, errors.New("config service unavailable")
	}
	if scope == "" {
		scope = "system"
	}
	if scope == "system" && scopeID == "" {
		scopeID = "default"
	}
	doc, err := svc.Get(ctx, configsvc.Scope(scope), scopeID)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func policyFragmentID(packID, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	return fmt.Sprintf("%s/%s", packID, name)
}

func packLockOwner(r *http.Request) string {
	if r == nil {
		return "api-gateway"
	}
	return fmt.Sprintf("api-gateway:%d", time.Now().UnixNano())
}

func acquirePackLocks(ctx context.Context, store locks.Store, packID, owner string) (func(), error) {
	if store == nil {
		return func() {}, errors.New("lock store unavailable")
	}
	global := "packs:global"
	if _, ok, err := store.Acquire(ctx, global, owner, locks.ModeExclusive, 60*time.Second); err != nil || !ok {
		if err != nil {
			return func() {}, err
		}
		return func() {}, errors.New("global pack lock held")
	}
	packLock := "pack:" + packID
	if _, ok, err := store.Acquire(ctx, packLock, owner, locks.ModeExclusive, 60*time.Second); err != nil || !ok {
		_, _, _ = store.Release(ctx, global, owner)
		if err != nil {
			return func() {}, err
		}
		return func() {}, errors.New("pack lock held")
	}
	return func() {
		_, _, _ = store.Release(context.Background(), packLock, owner)
		_, _, _ = store.Release(context.Background(), global, owner)
	}, nil
}
