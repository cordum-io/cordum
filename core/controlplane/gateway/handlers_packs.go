package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/locks"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

type packInstallResponse struct {
	packs.PackRecord
	WorkerCredential *packWorkerCredentialResponse `json:"worker_credential,omitempty"`
}

type packWorkerCredentialResponse struct {
	WorkerID      string   `json:"worker_id"`
	Token         string   `json:"token,omitempty"`
	AllowedPools  []string `json:"allowed_pools,omitempty"`
	AllowedTopics []string `json:"allowed_topics,omitempty"`
	PackID        string   `json:"pack_id,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
}

func (s *server) handleListPacks(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksRead, []string{"admin"}, s.configSvc) {
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeInternalError(w, r, "pack operation", err)
		return
	}
	items := make([]packs.PackRecord, 0, len(records))
	for _, rec := range records {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

func (s *server) handleGetPack(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksRead, []string{"admin"}, s.configSvc) {
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pack id required")
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeInternalError(w, r, "pack operation", err)
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksInstall, []string{"admin"}, s.lockStore) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, packs.MaxPackUploadBytes)
	if err := r.ParseMultipartForm(packs.MaxPackUploadBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePackInstallInvalid, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePackInstallInvalid, "bundle file required")
		return
	}
	defer func() { _ = file.Close() }()
	if header != nil && header.Filename != "" && !packs.IsTarGz(header.Filename) {
		writeJSONError(w, http.StatusBadRequest, errorCodePackInstallInvalid, "bundle must be .tgz")
		return
	}
	bundleDir, cleanup, err := packs.LoadPackBundleFromReader(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePackInstallInvalid, err.Error())
		return
	}
	defer cleanup()

	record, workerCredential, err := s.installPackFromDir(r.Context(), bundleDir, packs.PackInstallOptions{
		Force:       parseBool(r.FormValue("force")),
		Upgrade:     parseBool(r.FormValue("upgrade")),
		Inactive:    parseBool(r.FormValue("inactive")),
		Owner:       packLockOwner(r),
		InstalledBy: strings.TrimSpace(policybundles.PolicyActorID(r)),
	})
	if err != nil {
		var installErr *packs.PackInstallError
		if errors.As(err, &installErr) {
			writeJSONError(w, installErr.Status, packInstallErrorCode(installErr), installErr.Error())
			return
		}
		writeInternalError(w, r, "install pack", err)
		return
	}

	s.appendAuditEntryNamed(r.Context(), "install", "pack", record.ID, record.Manifest.Metadata.Title, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "install pack "+record.ID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, packInstallResponse{
		PackRecord:       record,
		WorkerCredential: workerCredential,
	})
}

func (s *server) installPackFromDir(ctx context.Context, bundleDir string, opts packs.PackInstallOptions) (packs.PackRecord, *packWorkerCredentialResponse, error) {
	if s == nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusServiceUnavailable, Err: errors.New("gateway unavailable")}
	}
	manifest, err := packs.LoadPackManifest(bundleDir)
	if err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if err := packs.ValidatePackManifest(manifest); err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if err := packs.EnsureProtocolCompatible(manifest); err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
	}
	if manifest.Compatibility.MinCoreVersion != "" && !opts.Force {
		if err := packs.EnsureCoreVersionCompatible(manifest.Compatibility.MinCoreVersion); err != nil {
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
		}
	}

	// Zero-trust rail: re-verify the pack signature server-side even
	// when cordumctl verified locally. The client is untrusted.
	verifyResult, verifyErr := verifyPackInstallBundle(ctx, s.redisClient(), bundleDir, manifest.Metadata.ID)
	if verifyErr != nil {
		s.appendAuditEntryNamed(ctx, PackVerificationEventRejected, "pack", manifest.Metadata.ID, manifest.Metadata.Title, opts.InstalledBy, "", verifyErr.Error())
		return packs.PackRecord{}, nil, &packs.PackInstallError{
			Status: http.StatusBadRequest,
			Err:    fmt.Errorf("%s: %s", verifyErr.Code, verifyErr.Message),
		}
	}
	s.appendAuditEntryNamed(ctx, PackVerificationEventVerified, "pack", manifest.Metadata.ID, manifest.Metadata.Title, opts.InstalledBy, "", fmt.Sprintf("signed=%t kid=%s cordum=%t", verifyResult.Signed, verifyResult.KID, verifyResult.HasCordumCounterSig))
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = packLockOwner(nil)
	}
	release, err := acquirePackLocks(ctx, s.lockStore, manifest.Metadata.ID, owner)
	if err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusConflict, Err: err}
	}
	defer release()

	installed, _, err := s.loadPackRegistry(ctx)
	if err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusInternalServerError, Err: fmt.Errorf("load pack registry: %w", err)}
	}
	if err := packs.ValidateAliasOwnership(manifest.Metadata.ID, manifest.Metadata.Aliases, manifest.Topics, installed); err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusConflict, Err: err}
	}

	schemaPlans, err := s.planSchemas(ctx, bundleDir, manifest, opts.Upgrade)
	if err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
	}
	workflowPlans, err := s.planWorkflows(ctx, bundleDir, manifest, opts.Upgrade)
	if err != nil {
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
	}

	appliedConfig := []packs.PackAppliedConfigOverlay{}
	appliedPolicy := []packs.PackAppliedPolicyOverlay{}
	appliedConfigChanges := []packs.AppliedConfigChange{}
	appliedPolicyChanges := []packs.AppliedPolicyChange{}
	appliedSchemas := []packs.SchemaPlan{}
	appliedWorkflows := []packs.WorkflowPlan{}
	appliedSchemaDigests := map[string]string{}
	appliedWorkflowDigests := map[string]string{}
	for _, plan := range schemaPlans {
		appliedSchemaDigests[plan.ID] = plan.Digest
	}
	for _, plan := range workflowPlans {
		appliedWorkflowDigests[plan.ID] = plan.Digest
	}

	rollback := func() {
		var rollbackErrs []string
		for i := len(appliedConfigChanges) - 1; i >= 0; i-- {
			if err := s.restoreConfigOverlay(ctx, appliedConfigChanges[i]); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("config overlay %d: %v", i, err))
			}
		}
		for i := len(appliedPolicyChanges) - 1; i >= 0; i-- {
			if err := s.restorePolicyOverlay(ctx, appliedPolicyChanges[i]); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("policy overlay %d: %v", i, err))
			}
		}
		for i := len(appliedWorkflows) - 1; i >= 0; i-- {
			if err := s.rollbackWorkflow(ctx, appliedWorkflows[i]); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("workflow %d: %v", i, err))
			}
		}
		for i := len(appliedSchemas) - 1; i >= 0; i-- {
			if err := s.rollbackSchema(ctx, appliedSchemas[i]); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("schema %d: %v", i, err))
			}
		}
		if len(rollbackErrs) > 0 {
			slog.Warn("pack install rollback had errors", "errors", strings.Join(rollbackErrs, "; "))
		}
	}

	for _, plan := range schemaPlans {
		if plan.Noop {
			continue
		}
		if err := s.registerSchema(ctx, plan.ID, plan.Schema); err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
		}
		appliedSchemas = append(appliedSchemas, plan)
	}
	for _, plan := range workflowPlans {
		if plan.Noop {
			continue
		}
		if err := s.registerWorkflow(ctx, plan.Workflow); err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
		}
		appliedWorkflows = append(appliedWorkflows, plan)
	}

	for _, overlay := range manifest.Overlays.Config {
		if packs.ShouldSkipConfigOverlay(opts.Inactive, overlay) {
			continue
		}
		applied, err := s.applyConfigOverlay(ctx, overlay, manifest.Metadata.ID, manifest.Metadata.Aliases, bundleDir)
		if err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
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
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
		}
		if applied.Overlay.Name != "" {
			appliedPolicy = append(appliedPolicy, applied.Overlay)
			appliedPolicyChanges = append(appliedPolicyChanges, applied)
		}
	}

	status := "ACTIVE"
	if opts.Inactive || !packs.HasPoolOverlay(appliedConfig) {
		status = "INACTIVE"
	}
	installedBy := strings.TrimSpace(opts.InstalledBy)

	record := packs.PackRecord{
		ID:          manifest.Metadata.ID,
		Version:     manifest.Metadata.Version,
		Status:      status,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		InstalledBy: installedBy,
		Manifest: packs.PackRecordManifest{
			Metadata:      manifest.Metadata,
			Compatibility: manifest.Compatibility,
			Topics:        manifest.Topics,
		},
		Resources: packs.PackRecordResources{
			Schemas:   appliedSchemaDigests,
			Workflows: appliedWorkflowDigests,
		},
		Overlays: packs.PackRecordOverlays{
			Config: appliedConfig,
			Policy: appliedPolicy,
		},
		Tests:        manifest.Tests,
		Verification: verificationFromServerResult(verifyResult),
	}
	registeredTopicNames := []string{}
	var packCredential *packWorkerCredentialResponse
	if s.topicRegistry != nil {
		regs, err := s.packTopicRegistrations(ctx, manifest, status)
		if err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusBadRequest, Err: err}
		}
		snapshot, err := s.topicRegistry.List(ctx)
		if err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusInternalServerError, Err: fmt.Errorf("load topic registry: %w", err)}
		}
		if err := validatePackTopicRegistryOwnership(manifest.Metadata.ID, manifest.Metadata.Aliases, regs, snapshot.Items); err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusConflict, Err: err}
		}
		if err := s.topicRegistry.SetMany(ctx, regs); err != nil {
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusInternalServerError, Err: err}
		}
		for _, reg := range regs {
			registeredTopicNames = append(registeredTopicNames, reg.Name)
		}
		packCredential, err = s.issuePackWorkerCredential(ctx, manifest.Metadata.ID, regs)
		if err != nil {
			_ = s.topicRegistry.DeleteMany(ctx, registeredTopicNames)
			rollback()
			return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusInternalServerError, Err: err}
		}
	}
	if err := s.updatePackRegistry(ctx, record); err != nil {
		if s.workerCredentialStore != nil && packCredential != nil {
			_ = s.workerCredentialStore.Revoke(ctx, packCredentialTenant(ctx, s.tenant), packCredential.WorkerID)
		}
		if s.topicRegistry != nil {
			_ = s.topicRegistry.DeleteMany(ctx, registeredTopicNames)
		}
		rollback()
		return packs.PackRecord{}, nil, &packs.PackInstallError{Status: http.StatusInternalServerError, Err: err}
	}
	if len(registeredTopicNames) > 0 {
		s.publishConfigChanged("system", "topics")
		s.emitTopicRegisteredAudit(ctx, manifest.Metadata.ID, registeredTopicNames, opts.InstalledBy)
	}
	if packCredential != nil {
		s.publishConfigChanged("system", "workers")
	}
	if len(appliedPolicyChanges) > 0 {
		s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	}
	return record, packCredential, nil
}

func (s *server) handleUninstallPack(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.workflowStore == nil || s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "pack dependencies unavailable")
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksUninstall, []string{"admin"}, s.lockStore) {
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
		_ = decodeJSONBody(w, r, &body)
		purge = body.Purge
	}
	owner := packLockOwner(r)
	release, err := acquirePackLocks(r.Context(), s.lockStore, packID, owner)
	if err != nil {
		slog.Warn("pack lock conflict", "method", r.Method, "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusConflict, errorCodePackAlreadyInstalled, "resource is locked")
		return
	}
	defer release()

	records, doc, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeInternalError(w, r, "pack operation", err)
		return
	}
	rec, ok := records[packID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "pack not installed")
		return
	}
	for i := len(rec.Overlays.Config) - 1; i >= 0; i-- {
		if err := s.removeConfigOverlay(r.Context(), rec.Overlays.Config[i]); err != nil {
			writeInternalError(w, r, "pack operation", err)
			return
		}
	}
	for _, overlay := range rec.Overlays.Policy {
		if err := s.removePolicyOverlay(r.Context(), overlay); err != nil {
			writeInternalError(w, r, "pack operation", err)
			return
		}
	}
	if purge {
		for wfID := range rec.Resources.Workflows {
			_ = s.workflowStore.DeleteWorkflow(r.Context(), wfID)
		}
		for schemaID := range rec.Resources.Schemas {
			if raw, err := s.schemaRegistry.Get(r.Context(), schemaID); err == nil {
				var sm map[string]any
				if json.Unmarshal(raw, &sm) == nil {
					if dollarID, ok := sm["$id"].(string); ok && dollarID != "" {
						_ = s.schemaRegistry.DeleteURL(r.Context(), dollarID)
					}
				}
			}
			_ = s.schemaRegistry.Delete(r.Context(), schemaID)
		}
	}
	rec.Status = "DISABLED"
	records[packID] = rec
	if err := s.savePackRegistry(r.Context(), records, doc); err != nil {
		writeInternalError(w, r, "pack operation", err)
		return
	}
	if s.topicRegistry != nil {
		names := make([]string, 0, len(rec.Manifest.Topics))
		for _, topic := range rec.Manifest.Topics {
			names = append(names, topic.Name)
		}
		if err := s.topicRegistry.DeleteMany(r.Context(), names); err != nil {
			writeInternalError(w, r, "pack topic cleanup", err)
			return
		}
		if len(names) > 0 {
			s.publishConfigChanged("system", "topics")
			s.emitTopicUnregisteredAudit(r.Context(), packID, names, packOpActor(r))
		}
	}
	if s.workerCredentialStore != nil {
		tenant := packCredentialTenant(r.Context(), s.tenant)
		if err := s.workerCredentialStore.Revoke(r.Context(), tenant, packWorkerID(packID)); err != nil && !errors.Is(err, workercredentials.ErrCredentialNotFound) {
			writeInternalError(w, r, "pack worker credential cleanup", err)
			return
		}
		s.publishConfigChanged("system", "workers")
	}
	s.appendAuditEntryNamed(r.Context(), "uninstall", "pack", packID, rec.Manifest.Metadata.Title, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "uninstall pack "+packID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, rec)
}

func (s *server) packTopicRegistrations(ctx context.Context, manifest *packs.PackManifest, packStatus string) ([]topicregistry.Registration, error) {
	if manifest == nil {
		return nil, errors.New("pack manifest required")
	}
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("load pool mappings: %w", err)
	}
	topics, _, err := extractPoolsFromConfig(doc)
	if err != nil {
		return nil, err
	}
	status := topicregistry.StatusActive
	if strings.EqualFold(packStatus, "INACTIVE") {
		status = topicregistry.StatusDisabled
	}
	out := make([]topicregistry.Registration, 0, len(manifest.Topics))
	for _, topic := range manifest.Topics {
		pool := ""
		if mapped := topics[topic.Name]; len(mapped) > 0 {
			pool = strings.TrimSpace(mapped[0])
		}
		out = append(out, topicregistry.Registration{
			Name:           topic.Name,
			Pool:           pool,
			InputSchemaID:  strings.TrimSpace(topic.InputSchemaID),
			OutputSchemaID: strings.TrimSpace(topic.OutputSchemaID),
			PackID:         manifest.Metadata.ID,
			Requires:       topic.Requires,
			RiskTags:       topic.RiskTags,
			Status:         status,
			RiskTagDeriver: strings.TrimSpace(topic.RiskTagDeriver),
		})
	}
	return out, nil
}

func validatePackTopicRegistryOwnership(packID string, aliases []string, regs []topicregistry.Registration, existing []topicregistry.Registration) error {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return errors.New("pack id required")
	}
	prefixes := packs.PackTopicPrefixes(packID, aliases)
	candidateNames := make(map[string]struct{}, len(regs))
	for _, reg := range regs {
		if name := strings.TrimSpace(reg.Name); name != "" {
			candidateNames[name] = struct{}{}
		}
	}
	for _, reg := range existing {
		name := strings.TrimSpace(reg.Name)
		if name == "" {
			continue
		}
		owner := strings.TrimSpace(reg.PackID)
		if owner == "" || owner == packID {
			continue
		}
		_, exact := candidateNames[name]
		if !exact && !packs.HasAnyPrefix(name, prefixes) {
			continue
		}
		return fmt.Errorf("topic %q is already registered by %s", name, owner)
	}
	return nil
}

func (s *server) issuePackWorkerCredential(ctx context.Context, packID string, regs []topicregistry.Registration) (*packWorkerCredentialResponse, error) {
	if s == nil || s.workerCredentialStore == nil {
		return nil, nil
	}

	workerID := packWorkerID(packID)
	allowedPools := make([]string, 0, len(regs))
	allowedTopics := make([]string, 0, len(regs))
	for _, reg := range regs {
		allowedTopics = append(allowedTopics, reg.Name)
		if reg.Pool != "" {
			allowedPools = append(allowedPools, reg.Pool)
		}
	}

	issued, err := s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{
		TenantID:      packCredentialTenant(ctx, s.tenant),
		WorkerID:      workerID,
		AllowedPools:  allowedPools,
		AllowedTopics: allowedTopics,
		PackID:        packID,
		CreatedBy:     "pack-install",
	})
	if err != nil {
		return nil, fmt.Errorf("issue pack worker credential: %w", err)
	}

	return &packWorkerCredentialResponse{
		WorkerID:      issued.Credential.WorkerID,
		Token:         issued.Token,
		AllowedPools:  issued.Credential.AllowedPools,
		AllowedTopics: issued.Credential.AllowedTopics,
		PackID:        issued.Credential.PackID,
		CreatedAt:     issued.Credential.CreatedAt,
	}, nil
}

func packCredentialTenant(ctx context.Context, fallback string) string {
	if authCtx := auth.FromContext(ctx); authCtx != nil && strings.TrimSpace(authCtx.Tenant) != "" {
		return strings.TrimSpace(authCtx.Tenant)
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "default"
	}
	return fallback
}

func packWorkerID(packID string) string {
	return strings.TrimSpace(packID)
}

func (s *server) handleVerifyPack(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksVerify, []string{"admin"}, s.safetyClient) {
		return
	}
	packID := strings.TrimSpace(r.PathValue("id"))
	if packID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pack id required")
		return
	}
	records, _, err := s.loadPackRegistry(r.Context())
	if err != nil {
		writeInternalError(w, r, "pack operation", err)
		return
	}
	rec, ok := records[packID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "pack not installed")
		return
	}
	results := make([]packs.PackVerifyResult, 0, len(rec.Tests.PolicySimulations))
	for _, test := range rec.Tests.PolicySimulations {
		result := packs.PackVerifyResult{Name: test.Name, Expected: normalizeDecision(test.ExpectDecision)}
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

func (s *server) planSchemas(ctx context.Context, dir string, manifest *packs.PackManifest, upgrade bool) ([]packs.SchemaPlan, error) {
	plans := make([]packs.SchemaPlan, 0, len(manifest.Resources.Schemas))
	for _, ref := range manifest.Resources.Schemas {
		schemaMap, digest, err := packs.LoadSchemaFile(dir, ref.Path)
		if err != nil {
			return nil, err
		}
		plan := packs.SchemaPlan{ID: ref.ID, Schema: schemaMap, Digest: digest}
		if s.schemaRegistry != nil {
			if existing, err := s.schemaRegistry.Get(ctx, ref.ID); err == nil {
				var existingMap map[string]any
				if err := json.Unmarshal(existing, &existingMap); err == nil {
					if normalized, ok := packs.NormalizeJSON(existingMap).(map[string]any); ok {
						plan.Existing = normalized
						plan.HadExisting = true
						existingDigest, err := packs.HashValue(plan.Existing)
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
			} else if !errors.Is(err, redis.Nil) {
				return nil, err
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (s *server) planWorkflows(ctx context.Context, dir string, manifest *packs.PackManifest, upgrade bool) ([]packs.WorkflowPlan, error) {
	plans := make([]packs.WorkflowPlan, 0, len(manifest.Resources.Workflows))
	for _, ref := range manifest.Resources.Workflows {
		workflowMap, digest, err := packs.LoadWorkflowFile(dir, ref.Path, ref.ID)
		if err != nil {
			return nil, err
		}
		plan := packs.WorkflowPlan{ID: ref.ID, Workflow: workflowMap, Digest: digest}
		existing, err := s.workflowStore.GetWorkflow(ctx, ref.ID)
		if err == nil {
			plan.HadExisting = true
			plan.Existing = packs.WorkflowToMap(existing)
			existingDigest, err := packs.HashWorkflow(plan.Existing)
			if err != nil {
				return nil, err
			}
			if existingDigest == digest {
				plan.Noop = true
			} else if !upgrade {
				return nil, fmt.Errorf("workflow %s exists; rerun with upgrade", ref.ID)
			}
		} else if !errors.Is(err, redis.Nil) {
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
		return fmt.Errorf("register schema %s: marshal: %w", id, err)
	}
	if err := s.schemaRegistry.Register(ctx, id, payload); err != nil {
		return fmt.Errorf("register schema %s: %w", id, err)
	}
	if dollarID, ok := schemaMap["$id"].(string); ok && dollarID != "" {
		if err := s.schemaRegistry.RegisterURL(ctx, dollarID, payload); err != nil {
			return fmt.Errorf("register schema %s url alias %s: %w", id, dollarID, err)
		}
	}
	return nil
}

func (s *server) registerWorkflow(ctx context.Context, workflowMap map[string]any) error {
	if s.workflowStore == nil {
		return errors.New("workflow store unavailable")
	}
	data, err := json.Marshal(workflowMap)
	if err != nil {
		return fmt.Errorf("register workflow: marshal: %w", err)
	}
	var req createWorkflowRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("register workflow: unmarshal: %w", err)
	}
	if err := s.saveWorkflowRequest(ctx, &req); err != nil {
		return fmt.Errorf("register workflow %s: %w", req.ID, err)
	}
	return nil
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
	if err := validateDAG(req.Steps); err != nil {
		return fmt.Errorf("save workflow request %s: validate dag: %w", req.ID, err)
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

func (s *server) rollbackSchema(ctx context.Context, plan packs.SchemaPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return s.registerSchema(ctx, plan.ID, plan.Existing)
	}
	if dollarID, ok := plan.Schema["$id"].(string); ok && dollarID != "" {
		_ = s.schemaRegistry.DeleteURL(ctx, dollarID)
	}
	return s.schemaRegistry.Delete(ctx, plan.ID)
}

func (s *server) rollbackWorkflow(ctx context.Context, plan packs.WorkflowPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return s.registerWorkflow(ctx, plan.Existing)
	}
	return s.workflowStore.DeleteWorkflow(ctx, plan.ID)
}

func (s *server) applyConfigOverlay(ctx context.Context, overlay packs.PackConfigOverlay, packID string, packAliases []string, dir string) (packs.AppliedConfigChange, error) {
	key := strings.TrimSpace(overlay.Key)
	if key == "" {
		return packs.AppliedConfigChange{}, errors.New("config overlay key required")
	}
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "json_merge_patch" {
		return packs.AppliedConfigChange{}, fmt.Errorf("unsupported config overlay strategy %q", strategy)
	}
	patch, err := packs.LoadPatchFile(dir, overlay.Path)
	if err != nil {
		return packs.AppliedConfigChange{}, err
	}
	patchMap, ok := patch.(map[string]any)
	if !ok {
		return packs.AppliedConfigChange{}, errors.New("config overlay patch must be a map")
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
		return packs.AppliedConfigChange{}, err
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	current := packs.NormalizeJSON(doc.Data[key])
	if err := packs.ValidateConfigPatch(key, patchMap, packID, packAliases, current); err != nil {
		return packs.AppliedConfigChange{}, err
	}
	before := packs.DeepCopy(current)
	updated := packs.MergePatch(current, patchMap)
	doc.Data[key] = updated
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return packs.AppliedConfigChange{}, err
	}
	s.publishConfigChanged(scope, scopeID)
	return packs.AppliedConfigChange{
		Overlay: packs.PackAppliedConfigOverlay{
			Name:    overlay.Name,
			Scope:   scope,
			ScopeID: scopeID,
			Key:     key,
			Patch:   patchMap,
		},
		Previous: before,
	}, nil
}

func (s *server) removeConfigOverlay(ctx context.Context, overlay packs.PackAppliedConfigOverlay) error {
	doc, err := getConfigDoc(ctx, s.configSvc, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("remove config overlay %s: %w", overlay.Name, err)
	}
	if doc.Data == nil {
		return nil
	}
	current := packs.NormalizeJSON(doc.Data[overlay.Key])
	if current == nil {
		return nil
	}
	deletePatch := packs.BuildDeletePatch(overlay.Patch)
	updated := packs.MergePatch(current, deletePatch)
	doc.Data[overlay.Key] = updated
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return err
	}
	s.publishConfigChanged(overlay.Scope, overlay.ScopeID)
	return nil
}

func (s *server) restoreConfigOverlay(ctx context.Context, change packs.AppliedConfigChange) error {
	overlay := change.Overlay
	doc, err := getConfigDoc(ctx, s.configSvc, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return fmt.Errorf("restore config overlay %s: %w", overlay.Name, err)
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(overlay.Scope), ScopeID: overlay.ScopeID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if change.Previous == nil {
		delete(doc.Data, overlay.Key)
	} else {
		doc.Data[overlay.Key] = packs.DeepCopy(change.Previous)
	}
	return s.configSvc.Set(ctx, doc)
}

func (s *server) applyPolicyOverlay(ctx context.Context, overlay packs.PackPolicyOverlay, packID, packVersion, dir string) (packs.AppliedPolicyChange, error) {
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "bundle_fragment" {
		return packs.AppliedPolicyChange{}, fmt.Errorf("unsupported policy overlay strategy %q", strategy)
	}
	path, err := packs.SafeJoin(dir, overlay.Path)
	if err != nil {
		return packs.AppliedPolicyChange{}, err
	}
	// #nosec G304 -- path is validated by packs.SafeJoin.
	content, err := os.ReadFile(path)
	if err != nil {
		return packs.AppliedPolicyChange{}, err
	}

	// Pre-flight: validate the fragment against the SAME schema the safety
	// kernel uses. Without this, a malformed fragment is happily persisted
	// here, the install reports ACTIVE, and the kernel later silently rejects
	// it on reload — leaving the operator with `policy/rules` returning the
	// rules but every job hitting `default policy: deny`. See issue #311.
	//
	// ParseSafetyPolicy enforces the schema; for the "wrong rule-type" case
	// (e.g., keywords in rules[].match) it also returns the enriched
	// "did you mean: input_rules[].match" hint from issue #312.
	if _, err := config.ParseSafetyPolicy(content); err != nil {
		return packs.AppliedPolicyChange{}, fmt.Errorf(
			"policy overlay %q (fragment file %q): %w", overlay.Name, overlay.Path, err)
	}

	fragmentID := policyFragmentID(packID, overlay.Name)
	doc, err := getConfigDoc(ctx, s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return packs.AppliedPolicyChange{}, err
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
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
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return packs.AppliedPolicyChange{}, err
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	return packs.AppliedPolicyChange{
		Overlay: packs.PackAppliedPolicyOverlay{
			Name:       overlay.Name,
			FragmentID: fragmentID,
		},
		Previous:    packs.DeepCopy(previous),
		HadPrevious: hadPrevious,
	}, nil
}

func (s *server) removePolicyOverlay(ctx context.Context, overlay packs.PackAppliedPolicyOverlay) error {
	doc, err := getConfigDoc(ctx, s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("remove policy overlay %s: %w", overlay.Name, err)
	}
	rawBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
	bundles, ok := rawBundles.(map[string]any)
	if !ok || bundles == nil {
		return nil
	}
	delete(bundles, overlay.FragmentID)
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return err
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	return nil
}

func (s *server) restorePolicyOverlay(ctx context.Context, change packs.AppliedPolicyChange) error {
	doc, err := getConfigDoc(ctx, s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return fmt.Errorf("restore policy overlay %s: %w", change.Overlay.Name, err)
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	if !change.HadPrevious {
		delete(bundles, change.Overlay.FragmentID)
	} else {
		bundles[change.Overlay.FragmentID] = packs.DeepCopy(change.Previous)
	}
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return err
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	return nil
}

func (s *server) runPolicySimulation(ctx context.Context, test packs.PackPolicySimulation, packID string) (string, string, error) {
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
	if auth := auth.FromContext(ctx); auth != nil {
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

func (s *server) loadPackRegistry(ctx context.Context) (map[string]packs.PackRecord, *configsvc.Document, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packs.PackRegistryScope), packs.PackRegistryID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]packs.PackRecord{}, nil, nil
		}
		return nil, nil, err
	}
	records := map[string]packs.PackRecord{}
	raw := packs.NormalizeJSON(doc.Data["installed"])
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

func (s *server) updatePackRegistry(ctx context.Context, record packs.PackRecord) error {
	records, doc, err := s.loadPackRegistry(ctx)
	if err != nil {
		return fmt.Errorf("update pack registry: load: %w", err)
	}
	records[record.ID] = record
	if err := s.savePackRegistry(ctx, records, doc); err != nil {
		return fmt.Errorf("update pack registry: save: %w", err)
	}
	return nil
}

func (s *server) savePackRegistry(ctx context.Context, records map[string]packs.PackRecord, doc *configsvc.Document) error {
	if doc == nil {
		doc = &configsvc.Document{Scope: configsvc.Scope(packs.PackRegistryScope), ScopeID: packs.PackRegistryID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	doc.Data["installed"] = recordsToAny(records)
	return s.configSvc.Set(ctx, doc)
}

func recordsToAny(records map[string]packs.PackRecord) map[string]any {
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
		rCtx, rCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, _, rErr := store.Release(rCtx, global, owner); rErr != nil {
			slog.Error("pack lock cleanup: failed to release global lock",
				"pack_id", packID, "owner", owner, "error", rErr)
		}
		rCancel()
		if err != nil {
			return func() {}, err
		}
		return func() {}, errors.New("pack lock held")
	}
	return func() {
		rCtx, rCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rCancel()
		if _, _, err := store.Release(rCtx, packLock, owner); err != nil {
			slog.Error("pack lock release failed, lock will expire after TTL",
				"resource", packLock, "owner", owner, "error", err)
		}
		if _, _, err := store.Release(rCtx, global, owner); err != nil {
			slog.Error("pack lock release failed, lock will expire after TTL",
				"resource", global, "owner", owner, "error", err)
		}
	}, nil
}

// ---------------------------------------------------------------------------
// Marketplace handlers and helpers
// ---------------------------------------------------------------------------

func (s *server) handleMarketplacePacks(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksRead, []string{"admin"}, s.configSvc) {
		return
	}
	resp, err := s.marketplaceSnapshot(r.Context(), false)
	if err != nil {
		slog.Error("marketplace snapshot failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "marketplace operation failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.schemaRegistry == nil || s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "marketplace operation failed")
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPacksInstall, []string{"admin"}, s.lockStore) {
		return
	}
	var req packs.MarketplaceInstallRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json payload")
		return
	}
	allowedHosts, err := s.marketplaceAllowedHosts(r.Context())
	if err != nil {
		slog.Error("marketplace allowed hosts lookup failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "marketplace operation failed")
		return
	}
	installURL := strings.TrimSpace(req.URL)
	expectedSha := strings.TrimSpace(req.Sha256)
	fromCatalog := false
	if installURL != "" {
		if expectedSha == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "sha256 required")
			return
		}
		entry, err := s.findMarketplaceEntryByURL(r.Context(), installURL)
		if err != nil {
			if errors.Is(err, packs.ErrMarketplaceNotFound) {
				writeJSONError(w, http.StatusNotFound, errorCodePackDependencyMissing, "marketplace pack not found")
			} else {
				slog.Error("marketplace entry lookup failed", "error", err, "url", installURL)
				writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "marketplace lookup failed")
			}
			return
		}
		entryURL := strings.TrimSpace(entry.Pack.URL)
		entrySha := strings.TrimSpace(entry.Pack.Sha256)
		if entryURL == "" || entrySha == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "marketplace entry missing url or sha256")
			return
		}
		if !strings.EqualFold(expectedSha, entrySha) {
			writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "sha256 mismatch")
			return
		}
		installURL = packs.ResolvePackURL(entryURL, entry.CatalogURL)
		expectedSha = entrySha
		fromCatalog = true
	} else {
		catalogID := strings.TrimSpace(req.CatalogID)
		packID := strings.TrimSpace(req.PackID)
		if catalogID == "" || packID == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "catalog_id and pack_id required")
			return
		}
		entry, err := s.findMarketplaceEntry(r.Context(), catalogID, packID, strings.TrimSpace(req.Version))
		if err != nil {
			if errors.Is(err, packs.ErrMarketplaceNotFound) {
				writeJSONError(w, http.StatusNotFound, errorCodePackDependencyMissing, "marketplace pack not found")
			} else {
				slog.Error("marketplace entry lookup failed", "error", err, "catalog_id", catalogID, "pack_id", packID)
				writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "marketplace lookup failed")
			}
			return
		}
		installURL = packs.ResolvePackURL(strings.TrimSpace(entry.Pack.URL), entry.CatalogURL)
		expectedSha = strings.TrimSpace(entry.Pack.Sha256)
		fromCatalog = true
	}
	if installURL == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "download url required")
		return
	}
	if expectedSha == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "sha256 required")
		return
	}
	if fromCatalog {
		if _, err := validateMarketplaceURL(installURL, nil); err != nil {
			slog.Error("marketplace url validation failed", "error", err, "url", installURL) // #nosec -- URL is validated and used for diagnostics.
			writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "invalid pack url")
			return
		}
		if host := packs.HostFromURL(installURL); host != "" {
			allowedHosts[host] = struct{}{}
		}
	}
	parsed, err := validateMarketplaceURL(installURL, allowedHosts)
	if err != nil {
		slog.Error("marketplace url validation failed", "error", err, "url", installURL) // #nosec -- URL is validated and used for diagnostics.
		writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "invalid pack url")
		return
	}
	packFile, digest, cleanup, err := downloadPackBundle(r.Context(), parsed, allowedHosts)
	if err != nil {
		slog.Error("pack download failed", "error", err)
		writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "pack download failed")
		return
	}
	defer cleanup()
	if !strings.EqualFold(digest, expectedSha) {
		writeJSONError(w, http.StatusBadRequest, errorCodePackMarketplaceInvalid, "sha256 mismatch")
		return
	}
	// #nosec G304,G703 -- packFile is a temp file path created by this process.
	fp, err := os.Open(packFile)
	if err != nil {
		slog.Error("pack file open failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "pack processing failed")
		return
	}
	bundleDir, cleanupDir, err := packs.LoadPackBundleFromReader(fp)
	_ = fp.Close()
	if err != nil {
		slog.Error("pack bundle load failed", "error", err)
		writeJSONError(w, http.StatusBadRequest, errorCodePackInstallInvalid, "invalid pack bundle")
		return
	}
	defer cleanupDir()

	record, workerCredential, err := s.installPackFromDir(r.Context(), bundleDir, packs.PackInstallOptions{
		Force:       req.Force,
		Upgrade:     req.Upgrade,
		Inactive:    req.Inactive,
		Owner:       packLockOwner(r),
		InstalledBy: strings.TrimSpace(policybundles.PolicyActorID(r)),
	})
	if err != nil {
		var installErr *packs.PackInstallError
		if errors.As(err, &installErr) {
			writeJSONError(w, installErr.Status, packInstallErrorCode(installErr), installErr.Error())
		} else {
			slog.Error("pack install failed", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "pack installation failed")
		}
		return
	}

	s.appendAuditEntryNamed(r.Context(), "install", "pack", record.ID, record.Manifest.Metadata.Title, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "install marketplace pack "+record.ID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, packInstallResponse{
		PackRecord:       record,
		WorkerCredential: workerCredential,
	})
}

func (s *server) marketplaceSnapshot(ctx context.Context, refresh bool) (packs.MarketplaceResponse, error) {
	if s == nil {
		return packs.MarketplaceResponse{}, errors.New("marketplace unavailable")
	}
	if !refresh {
		// L1: in-memory cache (30s TTL, per-replica).
		s.marketplaceMu.Lock()
		cache := s.marketplaceCache
		if !cache.FetchedAt.IsZero() && time.Since(cache.FetchedAt) < packs.MarketplaceCacheTTL {
			resp := packs.CloneMarketplaceResponse(cache.Response)
			resp.Cached = true
			if resp.FetchedAt == "" {
				resp.FetchedAt = cache.FetchedAt.UTC().Format(time.RFC3339)
			}
			s.marketplaceMu.Unlock()
			return resp, nil
		}
		s.marketplaceMu.Unlock()

		// L2: Redis cache (30min TTL, shared across replicas).
		if resp, err := s.marketplaceFromRedis(ctx); err == nil {
			// Populate L1 from Redis hit.
			s.marketplaceMu.Lock()
			s.marketplaceCache = packs.MarketplaceCache{Response: resp, FetchedAt: time.Now().UTC()}
			s.marketplaceMu.Unlock()
			resp.Cached = true
			return resp, nil
		}
	}

	// L3: upstream fetch (catalogs + HTTP).
	catalogs, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return packs.MarketplaceResponse{}, err
	}
	resp, err := s.buildMarketplaceResponse(ctx, catalogs, entries)
	if err != nil {
		return packs.MarketplaceResponse{}, err
	}
	fetchedAt := time.Now().UTC()
	resp.FetchedAt = fetchedAt.Format(time.RFC3339)

	// Populate L1.
	s.marketplaceMu.Lock()
	s.marketplaceCache = packs.MarketplaceCache{Response: resp, FetchedAt: fetchedAt}
	s.marketplaceMu.Unlock()

	// Populate L2 (best-effort, don't block on failure).
	s.marketplaceToRedis(ctx, resp)

	return resp, nil
}

// marketplaceFromRedis reads the shared marketplace cache from Redis.
func (s *server) marketplaceFromRedis(ctx context.Context) (packs.MarketplaceResponse, error) {
	if s.jobStore == nil {
		return packs.MarketplaceResponse{}, errors.New("no redis client")
	}
	rc := s.jobStore.Client()
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := rc.Get(readCtx, packs.MarketplaceRedisCacheKey).Bytes()
	if err != nil {
		return packs.MarketplaceResponse{}, err
	}
	var resp packs.MarketplaceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return packs.MarketplaceResponse{}, err
	}
	return resp, nil
}

// marketplaceToRedis writes the marketplace response to the shared Redis cache.
func (s *server) marketplaceToRedis(ctx context.Context, resp packs.MarketplaceResponse) {
	if s.jobStore == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("marketplace redis cache marshal failed", "error", err)
		return
	}
	rc := s.jobStore.Client()
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rc.Set(writeCtx, packs.MarketplaceRedisCacheKey, data, packs.MarketplaceRedisCacheTTL).Err(); err != nil {
		slog.Warn("marketplace redis cache write failed", "error", err)
	}
}

func (s *server) loadMarketplaceEntries(ctx context.Context) ([]packs.MarketplaceCatalogStatus, []packs.MarketplaceCatalogEntry, error) {
	catalogs, err := s.loadPackCatalogs(ctx)
	if err != nil {
		return nil, nil, err
	}
	statuses := make([]packs.MarketplaceCatalogStatus, 0, len(catalogs))
	entries := []packs.MarketplaceCatalogEntry{}
	for idx, catalog := range catalogs {
		id := strings.TrimSpace(catalog.ID)
		if id == "" {
			id = fmt.Sprintf("catalog-%d", idx+1)
		}
		enabled := true
		if catalog.Enabled != nil {
			enabled = *catalog.Enabled
		}
		status := packs.MarketplaceCatalogStatus{
			ID:      id,
			Title:   strings.TrimSpace(catalog.Title),
			URL:     strings.TrimSpace(catalog.URL),
			Enabled: enabled,
		}
		if !enabled {
			statuses = append(statuses, status)
			continue
		}
		allowedHosts := map[string]struct{}{}
		if host := packs.HostFromURL(status.URL); host != "" {
			allowedHosts[host] = struct{}{}
		}
		fetchCtx, cancel := context.WithTimeout(ctx, packs.MarketplaceCatalogFetchTimeout)
		catalogFile, err := fetchMarketplaceCatalog(fetchCtx, status.URL, allowedHosts)
		cancel()
		if err != nil {
			slog.Error("marketplace catalog fetch failed", "catalog_id", id, "url", status.URL, "error", err)
			status.Error = "catalog fetch failed"
			statuses = append(statuses, status)
			continue
		}
		status.UpdatedAt = catalogFile.UpdatedAt
		statuses = append(statuses, status)
		for _, pack := range catalogFile.Packs {
			entries = append(entries, packs.MarketplaceCatalogEntry{
				Pack:         pack,
				CatalogID:    id,
				CatalogTitle: status.Title,
				CatalogURL:   status.URL,
			})
		}
	}
	return statuses, entries, nil
}

func (s *server) loadPackCatalogs(ctx context.Context) ([]packs.MarketplaceCatalog, error) {
	if s.configSvc == nil {
		return nil, errors.New("marketplace configuration unavailable")
	}
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packs.PackCatalogScope), packs.PackCatalogID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if doc == nil || doc.Data == nil {
		return nil, nil
	}
	payload, err := json.Marshal(packs.NormalizeJSON(doc.Data))
	if err != nil {
		return nil, err
	}
	var cfg packs.MarketplaceCatalogConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, err
	}
	return cfg.Catalogs, nil
}

func fetchMarketplaceCatalog(ctx context.Context, catalogURL string, allowedHosts map[string]struct{}) (*packs.MarketplaceCatalogFile, error) {
	parsed, err := validateMarketplaceURL(catalogURL, allowedHosts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	client := marketplaceHTTPClient(allowedHosts, parsed.Hostname())
	resp, err := client.Do(req) // #nosec G704 -- URL validated by validateMarketplaceURL
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("catalog fetch failed: %s", resp.Status)
	}
	limit := int64(packs.MaxCatalogBytes) + 1
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > int64(packs.MaxCatalogBytes) {
		return nil, fmt.Errorf("catalog exceeds max size (%d bytes)", packs.MaxCatalogBytes)
	}
	var out packs.MarketplaceCatalogFile
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *server) buildMarketplaceResponse(ctx context.Context, catalogs []packs.MarketplaceCatalogStatus, entries []packs.MarketplaceCatalogEntry) (packs.MarketplaceResponse, error) {
	records := map[string]packs.PackRecord{}
	if s.configSvc != nil {
		var err error
		records, _, err = s.loadPackRegistry(ctx)
		if err != nil {
			return packs.MarketplaceResponse{}, err
		}
	}
	latest := map[string]packs.MarketplaceCatalogEntry{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.Pack.ID)
		version := strings.TrimSpace(entry.Pack.Version)
		url := strings.TrimSpace(entry.Pack.URL)
		sha := strings.TrimSpace(entry.Pack.Sha256)
		if id == "" || version == "" || url == "" || sha == "" {
			continue
		}
		if existing, ok := latest[id]; ok {
			if packs.CompareVersions(version, existing.Pack.Version) <= 0 {
				continue
			}
		}
		latest[id] = entry
	}
	items := make([]packs.MarketplacePackItem, 0, len(latest))
	for _, entry := range latest {
		pack := entry.Pack
		item := packs.MarketplacePackItem{
			ID:           pack.ID,
			Version:      pack.Version,
			Title:        pack.Title,
			Description:  pack.Description,
			Author:       pack.Author,
			Homepage:     pack.Homepage,
			Source:       pack.Source,
			Image:        pack.Image,
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
	return packs.MarketplaceResponse{
		Catalogs: catalogs,
		Items:    items,
	}, nil
}

func (s *server) findMarketplaceEntry(ctx context.Context, catalogID, packID, version string) (packs.MarketplaceCatalogEntry, error) {
	catalogID = strings.TrimSpace(catalogID)
	packID = strings.TrimSpace(packID)
	version = strings.TrimSpace(version)
	if packID == "" {
		return packs.MarketplaceCatalogEntry{}, packs.ErrMarketplaceNotFound
	}
	_, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return packs.MarketplaceCatalogEntry{}, err
	}
	var best packs.MarketplaceCatalogEntry
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
		if !found || packs.CompareVersions(entry.Pack.Version, best.Pack.Version) > 0 {
			best = entry
			found = true
		}
	}
	if !found {
		return packs.MarketplaceCatalogEntry{}, packs.ErrMarketplaceNotFound
	}
	return best, nil
}

func (s *server) findMarketplaceEntryByURL(ctx context.Context, rawURL string) (packs.MarketplaceCatalogEntry, error) {
	urlTrim := strings.TrimSpace(rawURL)
	if urlTrim == "" {
		return packs.MarketplaceCatalogEntry{}, packs.ErrMarketplaceNotFound
	}
	_, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return packs.MarketplaceCatalogEntry{}, err
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Pack.URL) == urlTrim {
			return entry, nil
		}
	}
	return packs.MarketplaceCatalogEntry{}, packs.ErrMarketplaceNotFound
}

func downloadPackBundle(ctx context.Context, parsed *url.URL, allowedHosts map[string]struct{}) (string, string, func(), error) {
	if parsed == nil {
		return "", "", func() {}, errors.New("url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", "", func() {}, err
	}
	client := marketplaceHTTPClient(allowedHosts, "")
	resp, err := client.Do(req) // #nosec G704 -- URL validated by validateMarketplaceURL
	if err != nil {
		return "", "", func() {}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", "", func() {}, fmt.Errorf("download failed: %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp("", "cordum-pack-*.tgz")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(tmpFile.Name()) } // #nosec G703 -- temp file path created by os.CreateTemp
	hasher := sha256.New()
	limit := int64(packs.MaxPackUploadBytes) + 1
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
	if written > int64(packs.MaxPackUploadBytes) {
		cleanup()
		return "", "", func() {}, fmt.Errorf("pack download exceeds max size (%d bytes)", packs.MaxPackUploadBytes)
	}
	return tmpFile.Name(), hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

// skipPrivateIPCheck disables SSRF protection. Only set in tests.
var skipPrivateIPCheck atomic.Bool

// lookupHostIPs resolves hostnames for SSRF checks. Overridden in tests.
var lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			ips = append(ips, addr.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("no resolved IPs")
	}
	return ips, nil
}

// isPrivateIP returns true if host is a private/loopback/link-local IP address
// or a well-known hostname that resolves to one.
func isPrivateIP(host string) bool {
	if skipPrivateIPCheck.Load() {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if packs.PrivateHostnames[host] {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return packs.IsPrivateNet(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), marketplaceHTTPTimeout())
	defer cancel()
	ips, err := lookupHostIPs(ctx, host)
	if err != nil {
		return true
	}
	for _, ip := range ips {
		if packs.IsPrivateNet(ip) {
			return true
		}
	}
	return false
}

func resolveMarketplaceIPs(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, errors.New("host required")
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return lookupHostIPs(ctx, host)
}

func validateMarketplaceHost(ctx context.Context, host string, allowedHosts map[string]struct{}) ([]net.IP, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, errors.New("url host required")
	}
	if allowedHosts != nil {
		if len(allowedHosts) == 0 {
			return nil, errors.New("invalid pack url")
		}
		if _, ok := allowedHosts[host]; !ok {
			slog.Warn("marketplace URL blocked: host not in allowlist", "host", host) // #nosec -- host is validated and used for diagnostics.
			return nil, errors.New("invalid pack url")
		}
	}
	if skipPrivateIPCheck.Load() {
		return resolveMarketplaceIPs(ctx, host)
	}
	if packs.PrivateHostnames[host] {
		slog.Warn("marketplace URL blocked: private address", "host", host) // #nosec -- host is validated and used for diagnostics.
		return nil, errors.New("invalid pack url")
	}
	ips, err := resolveMarketplaceIPs(ctx, host)
	if err != nil {
		slog.Warn("marketplace URL blocked: host resolution failed", "host", host, "error", err) // #nosec -- host is validated and used for diagnostics.
		return nil, errors.New("invalid pack url")
	}
	for _, ip := range ips {
		if packs.IsPrivateNet(ip) {
			slog.Warn("marketplace URL blocked: private address", "host", host, "ip", ip.String()) // #nosec -- host is validated and used for diagnostics.
			return nil, errors.New("invalid pack url")
		}
	}
	return ips, nil
}

func validateMarketplaceURL(rawURL string, allowedHosts map[string]struct{}) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, errors.New("url required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "https":
		// ok
	case "http":
		if env.IsProduction() && !env.Bool(packs.EnvMarketplaceAllowHTTP) {
			return nil, fmt.Errorf("http scheme not allowed")
		}
	default:
		return nil, fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil, errors.New("url host required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), marketplaceHTTPTimeout())
	defer cancel()
	if _, err := validateMarketplaceHost(ctx, host, allowedHosts); err != nil {
		return nil, err
	}
	return parsed, nil
}

func marketplaceHTTPTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(packs.EnvMarketplaceHTTPTimeout)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return packs.DefaultMarketplaceHTTPTimeout
}

func marketplaceHTTPClient(allowedHosts map[string]struct{}, initialHost string) *http.Client {
	initialHost = strings.ToLower(strings.TrimSpace(initialHost))
	// DefaultTransport is *http.Transport in stdlib; if tests or a future
	// runtime injected a different transport, fall back to a fresh one
	// rather than panic on the type assertion.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.DialContext = marketplaceDialContext(allowedHosts)
	return &http.Client{
		Timeout:   marketplaceHTTPTimeout(),
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			redirectHost := strings.ToLower(req.URL.Hostname())
			if initialHost != "" && redirectHost != "" && redirectHost != initialHost {
				if allowedHosts == nil {
					return errors.New("redirect not allowed")
				}
				if _, ok := allowedHosts[redirectHost]; !ok {
					return errors.New("redirect not allowed")
				}
			}
			if _, err := validateMarketplaceURL(req.URL.String(), allowedHosts); err != nil {
				return err
			}
			return nil
		},
	}
}

func marketplaceDialContext(allowedHosts map[string]struct{}) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		resolveCtx, cancel := context.WithTimeout(ctx, marketplaceHTTPTimeout())
		ips, err := validateMarketplaceHost(resolveCtx, host, allowedHosts)
		cancel()
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("no resolved IPs")
	}
}

func (s *server) marketplaceAllowedHosts(ctx context.Context) (map[string]struct{}, error) {
	hosts := map[string]struct{}{}
	catalogs, err := s.loadPackCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	if len(catalogs) == 0 {
		disabled := strings.TrimSpace(os.Getenv(packs.EnvPackCatalogDisableDefault))
		if disabled != "" {
			switch strings.ToLower(disabled) {
			case "1", "true", "yes":
				return hosts, nil
			}
		}
		catalogURL := strings.TrimSpace(os.Getenv(packs.EnvPackCatalogURL))
		if catalogURL == "" {
			catalogURL = packs.DefaultPackCatalogURL
		}
		if host := packs.HostFromURL(catalogURL); host != "" {
			if isPrivateIP(host) {
				slog.WarnContext(ctx, "skipping default catalog with private IP", "host", host)
			} else {
				hosts[host] = struct{}{}
			}
		}
		return hosts, nil
	}
	for _, catalog := range catalogs {
		enabled := true
		if catalog.Enabled != nil {
			enabled = *catalog.Enabled
		}
		if !enabled {
			continue
		}
		if host := packs.HostFromURL(catalog.URL); host != "" {
			if isPrivateIP(host) {
				slog.WarnContext(ctx, "skipping catalog with private IP", "host", host, "url", catalog.URL)
				continue
			}
			hosts[host] = struct{}{}
		}
	}
	return hosts, nil
}
