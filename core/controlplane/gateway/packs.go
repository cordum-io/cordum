package gateway

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/locks"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
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

	policyConfigScope = "system"
	policyConfigID    = "policy"
	policyConfigKey   = "bundles"

	maxPackUploadBytes       = 64 << 20
	maxPackFiles             = 2048
	maxPackFileBytes         = 32 << 20
	maxPackUncompressedBytes = 256 << 20
	maxCatalogBytes          = 8 << 20

	marketplaceCacheTTL = 30 * time.Second
)

var errMarketplaceNotFound = errors.New("marketplace pack not found")

func seedDefaultPackCatalogs(ctx context.Context, svc *configsvc.Service) error {
	if svc == nil {
		return nil
	}
	disabled := strings.TrimSpace(os.Getenv(envPackCatalogDisableDefault))
	if disabled != "" {
		switch strings.ToLower(disabled) {
		case "1", "true", "yes":
			return nil
		}
	}
	catalogURL := strings.TrimSpace(os.Getenv(envPackCatalogURL))
	if catalogURL == "" {
		catalogURL = defaultPackCatalogURL
	}
	if catalogURL == "" {
		return nil
	}
	title := strings.TrimSpace(os.Getenv(envPackCatalogTitle))
	if title == "" {
		title = defaultPackCatalogTitle
	}
	catalogID := strings.TrimSpace(os.Getenv(envPackCatalogID))
	if catalogID == "" {
		catalogID = defaultPackCatalogID
	}

	doc, err := svc.Get(ctx, configsvc.Scope(packCatalogScope), packCatalogID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(packCatalogScope),
			ScopeID: packCatalogID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if existing, ok := doc.Data["catalogs"]; ok && existing != nil {
		switch typed := existing.(type) {
		case []any:
			if len(typed) > 0 {
				return nil
			}
		case []map[string]any:
			if len(typed) > 0 {
				return nil
			}
		default:
			return nil
		}
	}

	doc.Data["catalogs"] = []map[string]any{
		{
			"id":      catalogID,
			"title":   title,
			"url":     catalogURL,
			"enabled": true,
		},
	}
	return svc.Set(ctx, doc)
}

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

type marketplaceCatalogConfig struct {
	Catalogs []marketplaceCatalog `json:"catalogs"`
}

type marketplaceCatalog struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type marketplaceCatalogFile struct {
	UpdatedAt string                   `json:"updated_at"`
	Packs     []marketplaceCatalogPack `json:"packs"`
}

type marketplaceCatalogPack struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`
	Homepage     string   `json:"homepage"`
	Source       string   `json:"source"`
	License      string   `json:"license"`
	URL          string   `json:"url"`
	Sha256       string   `json:"sha256"`
	Capabilities []string `json:"capabilities"`
	Requires     []string `json:"requires"`
	RiskTags     []string `json:"risk_tags"`
}

type marketplaceCatalogStatus struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type marketplacePackItem struct {
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	Title            string   `json:"title,omitempty"`
	Description      string   `json:"description,omitempty"`
	Author           string   `json:"author,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	Source           string   `json:"source,omitempty"`
	License          string   `json:"license,omitempty"`
	URL              string   `json:"url,omitempty"`
	Sha256           string   `json:"sha256,omitempty"`
	CatalogID        string   `json:"catalog_id,omitempty"`
	CatalogTitle     string   `json:"catalog_title,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	Requires         []string `json:"requires,omitempty"`
	RiskTags         []string `json:"risk_tags,omitempty"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	InstalledStatus  string   `json:"installed_status,omitempty"`
	InstalledAt      string   `json:"installed_at,omitempty"`
}

type marketplaceResponse struct {
	Catalogs  []marketplaceCatalogStatus `json:"catalogs"`
	Items     []marketplacePackItem      `json:"items"`
	FetchedAt string                     `json:"fetched_at,omitempty"`
	Cached    bool                       `json:"cached,omitempty"`
}

type marketplaceCache struct {
	Response  marketplaceResponse
	FetchedAt time.Time
}

type marketplaceCatalogEntry struct {
	Pack         marketplaceCatalogPack
	CatalogID    string
	CatalogTitle string
}

type marketplaceInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	PackID    string `json:"pack_id"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Sha256    string `json:"sha256"`
	Force     bool   `json:"force"`
	Upgrade   bool   `json:"upgrade"`
	Inactive  bool   `json:"inactive"`
}

func (s *server) handleListPacks(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]packRecord, 0, len(records))
	for _, rec := range records {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (s *server) handleGetPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		http.Error(w, "pack id required", http.StatusBadRequest)
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, ok := records[packID]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

func (s *server) handleMarketplacePacks(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	resp, err := s.marketplaceSnapshot(r.Context(), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.schemaRegistry == nil || s.workflowStore == nil {
		http.Error(w, "pack dependencies unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req marketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}
	installURL := strings.TrimSpace(req.URL)
	expectedSha := strings.TrimSpace(req.Sha256)
	if installURL == "" {
		catalogID := strings.TrimSpace(req.CatalogID)
		packID := strings.TrimSpace(req.PackID)
		if catalogID == "" || packID == "" {
			http.Error(w, "catalog_id and pack_id required", http.StatusBadRequest)
			return
		}
		entry, err := s.findMarketplaceEntry(r.Context(), catalogID, packID, strings.TrimSpace(req.Version))
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errMarketplaceNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		installURL = strings.TrimSpace(entry.Pack.URL)
		expectedSha = strings.TrimSpace(entry.Pack.Sha256)
	}
	if installURL == "" {
		http.Error(w, "download url required", http.StatusBadRequest)
		return
	}
	if expectedSha == "" {
		http.Error(w, "sha256 required", http.StatusBadRequest)
		return
	}
	packFile, digest, cleanup, err := downloadPackBundle(r.Context(), installURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer cleanup()
	if !strings.EqualFold(digest, expectedSha) {
		http.Error(w, "sha256 mismatch", http.StatusBadRequest)
		return
	}
	fp, err := os.Open(packFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	bundleDir, cleanupDir, err := loadPackBundleFromReader(fp)
	_ = fp.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer cleanupDir()

	record, err := s.installPackFromDir(r.Context(), bundleDir, packInstallOptions{
		Force:       req.Force,
		Upgrade:     req.Upgrade,
		Inactive:    req.Inactive,
		Owner:       packLockOwner(r),
		InstalledBy: strings.TrimSpace(policyActorID(r)),
	})
	if err != nil {
		status := http.StatusBadRequest
		var installErr *packInstallError
		if errors.As(err, &installErr) {
			status = installErr.Status
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}

func (s *server) handleInstallPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.schemaRegistry == nil || s.workflowStore == nil {
		http.Error(w, "pack dependencies unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackUploadBytes)
	if err := r.ParseMultipartForm(maxPackUploadBytes); err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		http.Error(w, "bundle file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if header != nil && header.Filename != "" && !isTarGz(header.Filename) {
		http.Error(w, "bundle must be .tgz", http.StatusBadRequest)
		return
	}
	bundleDir, cleanup, err := loadPackBundleFromReader(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
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
		http.Error(w, "pack dependencies unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		http.Error(w, "pack id required", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer release()

	records, doc, err := s.loadPackRegistry(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, ok := records[packID]
	if !ok {
		http.Error(w, "pack not installed", http.StatusNotFound)
		return
	}
	for i := len(rec.Overlays.Config) - 1; i >= 0; i-- {
		if err := s.removeConfigOverlay(r.Context(), rec.Overlays.Config[i]); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	for _, overlay := range rec.Overlays.Policy {
		if err := s.removePolicyOverlay(r.Context(), overlay); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

func (s *server) handleVerifyPack(w http.ResponseWriter, r *http.Request) {
	if s.safetyClient == nil {
		http.Error(w, "safety kernel unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		http.Error(w, "pack id required", http.StatusBadRequest)
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, ok := records[packID]
	if !ok {
		http.Error(w, "pack not installed", http.StatusNotFound)
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pack_id": packID,
		"results": results,
	})
}

func (s *server) marketplaceSnapshot(ctx context.Context, refresh bool) (marketplaceResponse, error) {
	if s == nil {
		return marketplaceResponse{}, errors.New("marketplace unavailable")
	}
	if !refresh {
		s.marketplaceMu.Lock()
		cache := s.marketplaceCache
		if !cache.FetchedAt.IsZero() && time.Since(cache.FetchedAt) < marketplaceCacheTTL {
			resp := cache.Response
			resp.Cached = true
			if resp.FetchedAt == "" {
				resp.FetchedAt = cache.FetchedAt.UTC().Format(time.RFC3339)
			}
			s.marketplaceMu.Unlock()
			return resp, nil
		}
		s.marketplaceMu.Unlock()
	}
	catalogs, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return marketplaceResponse{}, err
	}
	resp, err := s.buildMarketplaceResponse(ctx, catalogs, entries)
	if err != nil {
		return marketplaceResponse{}, err
	}
	fetchedAt := time.Now().UTC()
	resp.FetchedAt = fetchedAt.Format(time.RFC3339)
	s.marketplaceMu.Lock()
	s.marketplaceCache = marketplaceCache{Response: resp, FetchedAt: fetchedAt}
	s.marketplaceMu.Unlock()
	return resp, nil
}

func (s *server) loadMarketplaceEntries(ctx context.Context) ([]marketplaceCatalogStatus, []marketplaceCatalogEntry, error) {
	catalogs, err := s.loadPackCatalogs(ctx)
	if err != nil {
		return nil, nil, err
	}
	statuses := make([]marketplaceCatalogStatus, 0, len(catalogs))
	entries := []marketplaceCatalogEntry{}
	for idx, catalog := range catalogs {
		id := strings.TrimSpace(catalog.ID)
		if id == "" {
			id = fmt.Sprintf("catalog-%d", idx+1)
		}
		enabled := true
		if catalog.Enabled != nil {
			enabled = *catalog.Enabled
		}
		status := marketplaceCatalogStatus{
			ID:      id,
			Title:   strings.TrimSpace(catalog.Title),
			URL:     strings.TrimSpace(catalog.URL),
			Enabled: enabled,
		}
		if !enabled {
			statuses = append(statuses, status)
			continue
		}
		catalogFile, err := fetchMarketplaceCatalog(ctx, status.URL)
		if err != nil {
			status.Error = err.Error()
			statuses = append(statuses, status)
			continue
		}
		status.UpdatedAt = catalogFile.UpdatedAt
		statuses = append(statuses, status)
		for _, pack := range catalogFile.Packs {
			entries = append(entries, marketplaceCatalogEntry{
				Pack:         pack,
				CatalogID:    id,
				CatalogTitle: status.Title,
			})
		}
	}
	return statuses, entries, nil
}

func (s *server) loadPackCatalogs(ctx context.Context) ([]marketplaceCatalog, error) {
	if s.configSvc == nil {
		return nil, errors.New("config service unavailable")
	}
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packCatalogScope), packCatalogID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if doc == nil || doc.Data == nil {
		return nil, nil
	}
	payload, err := json.Marshal(normalizeJSON(doc.Data))
	if err != nil {
		return nil, err
	}
	var cfg marketplaceCatalogConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, err
	}
	return cfg.Catalogs, nil
}

func fetchMarketplaceCatalog(ctx context.Context, catalogURL string) (*marketplaceCatalogFile, error) {
	if _, err := validateMarketplaceURL(catalogURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog fetch failed: %s", resp.Status)
	}
	limit := int64(maxCatalogBytes) + 1
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > int64(maxCatalogBytes) {
		return nil, fmt.Errorf("catalog exceeds max size (%d bytes)", maxCatalogBytes)
	}
	var out marketplaceCatalogFile
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *server) buildMarketplaceResponse(ctx context.Context, catalogs []marketplaceCatalogStatus, entries []marketplaceCatalogEntry) (marketplaceResponse, error) {
	records := map[string]packRecord{}
	if s.configSvc != nil {
		var err error
		records, _, err = s.loadPackRegistry(ctx)
		if err != nil {
			return marketplaceResponse{}, err
		}
	}
	latest := map[string]marketplaceCatalogEntry{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.Pack.ID)
		version := strings.TrimSpace(entry.Pack.Version)
		url := strings.TrimSpace(entry.Pack.URL)
		sha := strings.TrimSpace(entry.Pack.Sha256)
		if id == "" || version == "" || url == "" || sha == "" {
			continue
		}
		if existing, ok := latest[id]; ok {
			if compareVersions(version, existing.Pack.Version) <= 0 {
				continue
			}
		}
		latest[id] = entry
	}
	items := make([]marketplacePackItem, 0, len(latest))
	for _, entry := range latest {
		pack := entry.Pack
		item := marketplacePackItem{
			ID:           pack.ID,
			Version:      pack.Version,
			Title:        pack.Title,
			Description:  pack.Description,
			Author:       pack.Author,
			Homepage:     pack.Homepage,
			Source:       pack.Source,
			License:      pack.License,
			URL:          pack.URL,
			Sha256:       pack.Sha256,
			CatalogID:    entry.CatalogID,
			CatalogTitle: entry.CatalogTitle,
			Capabilities: pack.Capabilities,
			Requires:     pack.Requires,
			RiskTags:     pack.RiskTags,
		}
		if rec, ok := records[pack.ID]; ok {
			item.InstalledVersion = rec.Version
			item.InstalledStatus = rec.Status
			item.InstalledAt = rec.InstalledAt
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return marketplaceResponse{
		Catalogs: catalogs,
		Items:    items,
	}, nil
}

func (s *server) findMarketplaceEntry(ctx context.Context, catalogID, packID, version string) (marketplaceCatalogEntry, error) {
	catalogID = strings.TrimSpace(catalogID)
	packID = strings.TrimSpace(packID)
	version = strings.TrimSpace(version)
	if packID == "" {
		return marketplaceCatalogEntry{}, errMarketplaceNotFound
	}
	_, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return marketplaceCatalogEntry{}, err
	}
	var best marketplaceCatalogEntry
	found := false
	for _, entry := range entries {
		if catalogID != "" && entry.CatalogID != catalogID {
			continue
		}
		if strings.TrimSpace(entry.Pack.ID) != packID {
			continue
		}
		if strings.TrimSpace(entry.Pack.URL) == "" || strings.TrimSpace(entry.Pack.Sha256) == "" {
			continue
		}
		if version != "" {
			if strings.TrimSpace(entry.Pack.Version) != version {
				continue
			}
			return entry, nil
		}
		if !found || compareVersions(entry.Pack.Version, best.Pack.Version) > 0 {
			best = entry
			found = true
		}
	}
	if !found {
		return marketplaceCatalogEntry{}, errMarketplaceNotFound
	}
	return best, nil
}

func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if oka && okb {
		max := len(pa)
		if len(pb) > max {
			max = len(pb)
		}
		for i := 0; i < max; i++ {
			ai := 0
			bi := 0
			if i < len(pa) {
				ai = pa[i]
			}
			if i < len(pb) {
				bi = pb[i]
			}
			if ai > bi {
				return 1
			}
			if ai < bi {
				return -1
			}
		}
		return 0
	}
	na := normalizeVersion(a)
	nb := normalizeVersion(b)
	if na == nb {
		return 0
	}
	if na > nb {
		return 1
	}
	return -1
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}

func parseVersion(version string) ([]int, bool) {
	version = normalizeVersion(version)
	if version == "" {
		return nil, false
	}
	if strings.ContainsAny(version, "+-") {
		return nil, false
	}
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func downloadPackBundle(ctx context.Context, rawURL string) (string, string, func(), error) {
	parsed, err := validateMarketplaceURL(rawURL)
	if err != nil {
		return "", "", func() {}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", "", func() {}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", func() {}, fmt.Errorf("download failed: %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp("", "cordum-pack-*.tgz")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(tmpFile.Name()) }
	hasher := sha256.New()
	limit := int64(maxPackUploadBytes) + 1
	limited := &io.LimitedReader{R: resp.Body, N: limit}
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), limited)
	if err != nil {
		_ = tmpFile.Close()
		cleanup()
		return "", "", func() {}, err
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	if written > int64(maxPackUploadBytes) {
		cleanup()
		return "", "", func() {}, fmt.Errorf("pack download exceeds max size (%d bytes)", maxPackUploadBytes)
	}
	return tmpFile.Name(), hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

func validateMarketplaceURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, errors.New("url required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("url host required")
	}
	return parsed, nil
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
	content, err := os.ReadFile(filepath.Join(dir, overlay.Path))
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

func loadPackBundleFromReader(src io.Reader) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "cordum-pack-*")
	if err != nil {
		return "", func() {}, err
	}
	if err := extractTarGzReader(src, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, err
	}
	root, err := findPackRoot(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, err
	}
	return root, func() { _ = os.RemoveAll(tmpDir) }, nil
}

func loadPackManifest(dir string) (*packManifest, error) {
	paths := []string{
		filepath.Join(dir, "pack.yaml"),
		filepath.Join(dir, "pack.yml"),
	}
	var data []byte
	var err error
	for _, path := range paths {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("pack.yaml not found: %w", err)
	}
	var manifest packManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse pack.yaml: %w", err)
	}
	return &manifest, nil
}

func validatePackManifest(manifest *packManifest) error {
	if manifest == nil {
		return errors.New("pack manifest required")
	}
	id := strings.TrimSpace(manifest.Metadata.ID)
	if id == "" {
		return errors.New("metadata.id required")
	}
	idPattern := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !idPattern.MatchString(id) {
		return fmt.Errorf("metadata.id must match %s", idPattern.String())
	}
	if strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("metadata.version required")
	}
	for _, topic := range manifest.Topics {
		if topic.Name == "" {
			return errors.New("topic name required")
		}
		if !strings.HasPrefix(topic.Name, "job."+id+".") {
			return fmt.Errorf("topic %q must be namespaced under job.%s.*", topic.Name, id)
		}
	}
	for _, res := range manifest.Resources.Schemas {
		if res.ID == "" || res.Path == "" {
			return errors.New("schema id and path required")
		}
		if !strings.HasPrefix(res.ID, id+"/") {
			return fmt.Errorf("schema id %q must be namespaced under %s/", res.ID, id)
		}
	}
	for _, res := range manifest.Resources.Workflows {
		if res.ID == "" || res.Path == "" {
			return errors.New("workflow id and path required")
		}
		if !strings.HasPrefix(res.ID, id+".") {
			return fmt.Errorf("workflow id %q must be namespaced under %s.", res.ID, id)
		}
	}
	return nil
}

func ensureProtocolCompatible(manifest *packManifest) error {
	if manifest.Compatibility.ProtocolVersion == 0 {
		return errors.New("compatibility.protocolVersion required")
	}
	if manifest.Compatibility.ProtocolVersion != capsdk.DefaultProtocolVersion {
		return fmt.Errorf("protocolVersion %d not supported (expected %d)", manifest.Compatibility.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
	return nil
}

func ensureCoreVersionCompatible(minCoreVersion string) error {
	minCoreVersion = strings.TrimSpace(minCoreVersion)
	if minCoreVersion == "" {
		return nil
	}
	minParsed, ok := parseSemver(minCoreVersion)
	if !ok {
		return fmt.Errorf("invalid minCoreVersion %q", minCoreVersion)
	}
	coreParsed, ok := parseSemver(buildinfo.Version)
	if !ok {
		// Allow installs on dev/unknown builds; use --force to bypass explicitly.
		return nil
	}
	if compareSemver(coreParsed, minParsed) < 0 {
		return fmt.Errorf("core version %s does not satisfy minCoreVersion %s", buildinfo.Version, minCoreVersion)
	}
	return nil
}

func parseSemver(raw string) ([3]int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return [3]int{}, false
	}
	raw = strings.TrimPrefix(raw, "v")
	raw = strings.SplitN(raw, "-", 2)[0]
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			out[i] = 0
			continue
		}
		if parts[i] == "" {
			return [3]int{}, false
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func compareSemver(left, right [3]int) int {
	for i := 0; i < 3; i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

func shouldSkipConfigOverlay(inactive bool, overlay packConfigOverlay) bool {
	if !inactive {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(overlay.Key), "pools")
}

func hasPoolOverlay(overlays []packAppliedConfigOverlay) bool {
	for _, overlay := range overlays {
		if strings.EqualFold(overlay.Key, "pools") {
			return true
		}
	}
	return false
}

func validateConfigPatch(key string, patch map[string]any, packID string, current any) error {
	switch strings.ToLower(key) {
	case "pools":
		return validatePoolsPatch(patch, packID, current)
	case "timeouts":
		return validateTimeoutsPatch(patch, packID)
	default:
		return fmt.Errorf("unsupported config overlay key %q", key)
	}
}

func validatePoolsPatch(patch map[string]any, packID string, current any) error {
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("pools.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("pools topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawPools := normalizeJSON(patch["pools"])
	if rawPools != nil {
		pools, ok := rawPools.(map[string]any)
		if !ok {
			return errors.New("pools.pools must be a map")
		}
		existingPools := extractPools(current)
		for poolName := range pools {
			if _, ok := existingPools[poolName]; ok {
				continue
			}
			if !strings.HasPrefix(poolName, packID) {
				return fmt.Errorf("pool %q must be prefixed with %s", poolName, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "pools" {
			return fmt.Errorf("unsupported pools overlay key %q", key)
		}
	}
	return nil
}

func extractPools(current any) map[string]struct{} {
	out := map[string]struct{}{}
	currentMap, ok := normalizeJSON(current).(map[string]any)
	if !ok || currentMap == nil {
		return out
	}
	rawPools := normalizeJSON(currentMap["pools"])
	pools, ok := rawPools.(map[string]any)
	if !ok || pools == nil {
		return out
	}
	for name := range pools {
		out[name] = struct{}{}
	}
	return out
}

func validateTimeoutsPatch(patch map[string]any, packID string) error {
	if patch == nil {
		return nil
	}
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("timeouts.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("timeouts topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawWorkflows := normalizeJSON(patch["workflows"])
	if rawWorkflows != nil {
		workflows, ok := rawWorkflows.(map[string]any)
		if !ok {
			return errors.New("timeouts.workflows must be a map")
		}
		for wf := range workflows {
			if !strings.HasPrefix(wf, packID+".") {
				return fmt.Errorf("timeout workflow %q must be namespaced under %s.", wf, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "workflows" {
			return fmt.Errorf("unsupported timeouts overlay key %q", key)
		}
	}
	return nil
}

func loadSchemaFile(dir, relPath string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	schemaMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("schema file must be an object")
	}
	digest, err := hashValue(schemaMap)
	if err != nil {
		return nil, "", err
	}
	return schemaMap, digest, nil
}

func loadWorkflowFile(dir, relPath, id string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	workflowMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("workflow file must be an object")
	}
	if id != "" {
		workflowMap["id"] = id
	}
	normalized := normalizeWorkflowMap(workflowMap)
	digest, err := hashValue(normalized)
	if err != nil {
		return nil, "", err
	}
	return workflowMap, digest, nil
}

func normalizeWorkflowMap(workflow map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range workflow {
		switch k {
		case "created_at", "updated_at":
			continue
		default:
			out[k] = v
		}
	}
	return out
}

func hashWorkflow(workflow map[string]any) (string, error) {
	return hashValue(normalizeWorkflowMap(workflow))
}

func workflowToMap(workflow *wf.Workflow) map[string]any {
	if workflow == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(workflow)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func loadDataFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload any
	if json.Unmarshal(data, &payload) == nil {
		return normalizeJSON(payload), nil
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return normalizeJSON(payload), nil
}

func loadPatchFile(dir, relPath string) (any, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, err
	}
	return loadDataFile(path)
}

func normalizeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = normalizeJSON(child)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, child := range v {
			key := fmt.Sprint(k)
			out[key] = normalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeJSON(child)
		}
		return out
	default:
		return v
	}
}

func deepCopy(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}

func mergePatch(current any, patch map[string]any) any {
	if patch == nil {
		return current
	}
	currentMap, _ := normalizeJSON(current).(map[string]any)
	if currentMap == nil {
		currentMap = map[string]any{}
	}
	for key, value := range patch {
		switch v := value.(type) {
		case nil:
			delete(currentMap, key)
		case map[string]any:
			currentMap[key] = mergePatch(currentMap[key], v)
		default:
			currentMap[key] = v
		}
	}
	return currentMap
}

func buildDeletePatch(patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range patch {
		switch v := value.(type) {
		case map[string]any:
			out[key] = buildDeletePatch(v)
		default:
			out[key] = nil
		}
	}
	return out
}

func hashValue(value any) (string, error) {
	encoded, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	buf := &strings.Builder{}
	if err := appendCanonical(buf, value); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func appendCanonical(buf *strings.Builder, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case map[string]any:
		return appendCanonicalMap(buf, v)
	case []any:
		return appendCanonicalSlice(buf, v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(encoded)
		return nil
	}
}

func appendCanonicalMap(buf *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := appendCanonical(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendCanonicalSlice(buf *strings.Builder, items []any) error {
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := appendCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func isTarGz(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findPackRoot(dir string) (string, error) {
	if exists(filepath.Join(dir, "pack.yaml")) || exists(filepath.Join(dir, "pack.yml")) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", errors.New("pack.yaml not found in archive root")
	}
	if !entries[0].IsDir() {
		return "", errors.New("pack.yaml not found in archive root")
	}
	subdir := filepath.Join(dir, entries[0].Name())
	if exists(filepath.Join(subdir, "pack.yaml")) || exists(filepath.Join(subdir, "pack.yml")) {
		return subdir, nil
	}
	return "", errors.New("pack.yaml not found in archive")
}

func extractTarGzReader(src io.Reader, dest string) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var (
		files   int
		totalSz int64
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			files++
			if files > maxPackFiles {
				return fmt.Errorf("pack archive exceeds max files (%d)", maxPackFiles)
			}
			if hdr.Size < 0 || hdr.Size > maxPackFileBytes {
				return fmt.Errorf("pack file too large: %s", hdr.Name)
			}
			totalSz += hdr.Size
			if totalSz > maxPackUncompressedBytes {
				return fmt.Errorf("pack archive exceeds max size (%d bytes)", maxPackUncompressedBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute archive path: %s", name)
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	return target, nil
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
