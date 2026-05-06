package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/redis/go-redis/v9"
)

// EDGE-052 — unified Global policy authority.
//
// The /api/v1/policy/global endpoint exposes the same five sections that
// the four evaluators (Cordum job, Edge action, MCP tool, output scan)
// consult via safetykernel.GlobalPolicy. The endpoint is the dashboard's
// canonical authoring surface; the legacy /api/v1/policy/bundles
// endpoints remain as backward-compat aliases for per-bundle CRUD.
//
// Each section is persisted as a dedicated studio bundle key under the
// "secops/" prefix. Pack-contributed rules continue to install into
// pack-named bundles; the global view stays clean of pack provenance.

const (
	globalSectionInputRules      = "input_rules"
	globalSectionOutputRules     = "output_rules"
	globalSectionEdgeActionRules = "edge_action_rules"
	globalSectionMCPToolRules    = "mcp_tool_rules"
	globalSectionInvariants      = "invariants"

	globalBundleKeyInput      = policybundles.PolicyStudioPrefix + "global-input"
	globalBundleKeyOutput     = policybundles.PolicyStudioPrefix + "global-output"
	globalBundleKeyEdgeAction = policybundles.PolicyStudioPrefix + "global-edge-action"
	globalBundleKeyMCPTool    = policybundles.PolicyStudioPrefix + "global-mcp-tool"
)

// globalSectionBundleKey returns the bundle key persisting a given section.
// Returns "" when name is unknown.
func globalSectionBundleKey(name string) string {
	switch name {
	case globalSectionInputRules:
		return globalBundleKeyInput
	case globalSectionOutputRules:
		return globalBundleKeyOutput
	case globalSectionEdgeActionRules:
		return globalBundleKeyEdgeAction
	case globalSectionMCPToolRules:
		return globalBundleKeyMCPTool
	case globalSectionInvariants:
		return policybundles.PolicyInvariantsBundleKey
	default:
		return ""
	}
}

// globalSectionsInOrder lists the canonical section ordering for response
// shape stability — clients can rely on the iteration order of the
// "sections" map staying consistent.
var globalSectionsInOrder = []string{
	globalSectionInputRules,
	globalSectionOutputRules,
	globalSectionEdgeActionRules,
	globalSectionMCPToolRules,
	globalSectionInvariants,
}

type globalPolicySection struct {
	BundleID string `json:"bundle_id"`
	Content  string `json:"content"`
	Sha256   string `json:"sha256,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type globalPolicyResponse struct {
	SnapshotVersion string                         `json:"snapshot_version"`
	SnapshotHash    string                         `json:"snapshot_hash"`
	UpdatedAt       string                         `json:"updated_at,omitempty"`
	Sections        map[string]globalPolicySection `json:"sections"`
}

type globalPolicyPutRequestSection struct {
	Content string `json:"content"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type globalPolicyPutRequest struct {
	// SnapshotVersion is the snapshot the client based its edit on. When
	// non-empty and != the current snapshot, the request is rejected with
	// 409 Conflict (optimistic concurrency).
	SnapshotVersion string                                   `json:"snapshot_version,omitempty"`
	Sections        map[string]globalPolicyPutRequestSection `json:"sections"`
	Author          string                                   `json:"author,omitempty"`
	Message         string                                   `json:"message,omitempty"`
}

func (s *server) handleGetPolicyGlobal(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, s.configSvc) {
		return
	}
	bundles, updatedAt, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}
	resp, err := buildGlobalPolicyResponse(bundles, updatedAt)
	if err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handlePutPolicyGlobal(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}
	var body globalPolicyPutRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if len(body.Sections) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "sections map required")
		return
	}
	// Validate every requested section name + parse its content.
	for name, section := range body.Sections {
		if globalSectionBundleKey(name) == "" {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("unknown section %q", name))
			return
		}
		content := strings.TrimSpace(section.Content)
		if content == "" {
			// Empty section content is allowed: writers can clear a section by
			// PUTting `content: ""`. The loop below will delete the bundle.
			continue
		}
		sanitized := policybundles.SanitizePolicyBundleYAML(content)
		if _, err := config.ParseSafetyPolicy([]byte(sanitized)); err != nil {
			writeErrorJSON(w, http.StatusBadRequest,
				fmt.Sprintf("invalid policy content in section %q: %v", name, err))
			return
		}
	}

	// Read-modify-write of the policy config doc. configsvc.Set is the
	// atomic write boundary — concurrent writers (pack install/uninstall,
	// the legacy bundles PUT) race against this Set the same way they
	// race each other. Optimistic concurrency via SnapshotVersion guards
	// the common dashboard case (read, edit, save).
	doc, err := getConfigDoc(r.Context(), s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			writeInternalError(w, r, "policy operation", err)
			return
		}
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(packs.PolicyConfigScope),
			ScopeID: packs.PolicyConfigID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}

	if body.SnapshotVersion != "" {
		_, currentSnap, snapErr := policybundles.BuildPolicyFromBundles(bundles)
		if snapErr != nil {
			writeInternalError(w, r, "policy operation", snapErr)
			return
		}
		if currentSnap != "" && currentSnap != body.SnapshotVersion {
			writeErrorJSON(w, http.StatusConflict,
				fmt.Sprintf("snapshot_version mismatch: have %q, want %q", currentSnap, body.SnapshotVersion))
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, name := range globalSectionsInOrder {
		section, ok := body.Sections[name]
		if !ok {
			continue
		}
		bundleID := globalSectionBundleKey(name)
		content := policybundles.SanitizePolicyBundleYAML(strings.TrimSpace(section.Content))
		if content == "" {
			delete(bundles, bundleID)
			continue
		}
		// Sign each section's content the same way handlePutPolicyBundle
		// does so the strict-mode policy is uniform across endpoints.
		outcome := signPolicyBundleContent(r.Context(), []byte(content))
		if outcome.Status != 0 {
			writeErrorJSON(w, outcome.Status, outcome.Message)
			return
		}
		existing, _ := bundles[bundleID].(map[string]any)
		if existing == nil {
			existing = map[string]any{"created_at": now}
		}
		existing["content"] = content
		existing["updated_at"] = now
		if body.Author != "" {
			existing["author"] = strings.TrimSpace(body.Author)
		}
		if body.Message != "" {
			existing["message"] = strings.TrimSpace(body.Message)
		}
		if section.Enabled != nil {
			existing["enabled"] = *section.Enabled
		}
		if outcome.Signature != nil {
			existing[policyBundleSignatureKey] = outcome.Signature
		} else {
			delete(existing, policyBundleSignatureKey)
		}
		bundles[bundleID] = existing
	}

	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}
	s.appendAuditEntryNamed(r.Context(), "edit", "policy", "global", "global",
		policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "edit unified Global policy")

	resp, err := buildGlobalPolicyResponse(bundles, now)
	if err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// buildGlobalPolicyResponse projects a bundle map into the typed five-
// section view exposed by /api/v1/policy/global. Pure function — used by
// both GET and PUT response paths so the wire shape is identical.
func buildGlobalPolicyResponse(bundles map[string]any, updatedAt string) (globalPolicyResponse, error) {
	resp := globalPolicyResponse{
		Sections:  make(map[string]globalPolicySection, len(globalSectionsInOrder)),
		UpdatedAt: updatedAt,
	}
	for _, name := range globalSectionsInOrder {
		bundleID := globalSectionBundleKey(name)
		content, sha, enabled := readSectionFromBundles(bundles, bundleID)
		resp.Sections[name] = globalPolicySection{
			BundleID: bundleID,
			Content:  content,
			Sha256:   sha,
			Enabled:  enabled,
		}
	}
	_, snapshot, err := policybundles.BuildPolicyFromBundles(bundles)
	if err != nil {
		return resp, err
	}
	resp.SnapshotHash = snapshot
	resp.SnapshotVersion = snapshot
	return resp, nil
}

// readSectionFromBundles extracts (content, sha256, enabled) for a single
// section bundle. Absent bundle yields ("", "", true) — empty section is
// the natural "no studio overrides" state, treated as enabled by default
// per BundleEnabled semantics.
func readSectionFromBundles(bundles map[string]any, bundleID string) (string, string, bool) {
	raw, ok := bundles[bundleID]
	if !ok {
		return "", "", true
	}
	bundle, _ := raw.(map[string]any)
	if bundle == nil {
		// String-shaped legacy bundle entries fall through here.
		if str, isString := raw.(string); isString {
			content := strings.TrimSpace(str)
			if content == "" {
				return "", "", true
			}
			sum := sha256.Sum256([]byte(content))
			return content, hex.EncodeToString(sum[:]), true
		}
		return "", "", true
	}
	content := strings.TrimSpace(policybundles.StringFromAny(bundle["content"]))
	sha := strings.TrimSpace(policybundles.StringFromAny(bundle["sha256"]))
	if sha == "" && content != "" {
		sum := sha256.Sum256([]byte(content))
		sha = hex.EncodeToString(sum[:])
	}
	return content, sha, policybundles.BundleEnabled(bundle)
}

// sortedBundleKeys returns the bundles map keys in stable order — used
// in tests + audit metadata so iteration order is reproducible.
func sortedBundleKeys(bundles map[string]any) []string {
	keys := make([]string, 0, len(bundles))
	for k := range bundles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
