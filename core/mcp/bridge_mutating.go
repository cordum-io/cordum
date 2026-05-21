package mcp

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Mutating bridge methods.
//
// Each HTTPServiceBridge method forwards to the corresponding gateway
// REST endpoint. Idempotency-Key is passed through on every mutating
// call so the gateway's idempotency middleware can dedupe retries
// (an LLM typically retries after a human approves the MCP approval
// record, and the retry should be a no-op at the gateway layer).
//
// The DirectServiceBridge implementations delegate to function hooks
// configured in DirectServiceBridgeConfig so in-process tests can
// stub the behaviour without standing up a gateway.

// ---------------------------------------------------------------------------
// HTTPServiceBridge
// ---------------------------------------------------------------------------

func (b *HTTPServiceBridge) CreateWorkflow(ctx context.Context, req CreateWorkflowInput) (*CreateWorkflowOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	if len(req.Steps) == 0 {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "steps are required (at least one)", nil)
	}
	body := map[string]any{
		"steps": req.Steps,
	}
	if v := strings.TrimSpace(req.ID); v != "" {
		body["id"] = v
	}
	if v := strings.TrimSpace(req.Name); v != "" {
		body["name"] = v
	}
	if v := strings.TrimSpace(req.Description); v != "" {
		body["description"] = v
	}
	if v := strings.TrimSpace(req.OrgID); v != "" {
		body["org_id"] = v
	}
	if v := strings.TrimSpace(req.TeamID); v != "" {
		body["team_id"] = v
	}
	if v := strings.TrimSpace(req.Version); v != "" {
		body["version"] = v
	}
	if req.TimeoutSec > 0 {
		body["timeout_sec"] = req.TimeoutSec
	}
	if len(req.Config) > 0 {
		body["config"] = req.Config
	}
	if len(req.Parameters) > 0 {
		body["parameters"] = req.Parameters
	}
	if len(req.InputSchema) > 0 {
		body["input_schema"] = req.InputSchema
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	var raw map[string]any
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/workflows", headers, body, &raw); err != nil {
		return nil, err
	}
	out := &CreateWorkflowOutput{
		WorkflowID: firstString(raw, "id", "workflow_id"),
		Version:    firstString(raw, "version"),
	}
	if out.WorkflowID == "" {
		return nil, NewBridgeError(http.StatusBadGateway, "invalid_response", "workflow id missing from response", raw)
	}
	return out, nil
}

func (b *HTTPServiceBridge) InstallPack(ctx context.Context, req InstallPackInput) (*InstallPackOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	packID := strings.TrimSpace(req.PackID)
	url := strings.TrimSpace(req.URL)
	sha := strings.TrimSpace(req.Sha256)
	// The gateway requires EITHER (pack_id via catalog lookup) OR
	// (url + sha256). Rejecting both-missing up-front gives a clean
	// 400 without a round-trip.
	if packID == "" && url == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request",
			"either pack_id (with catalog_id) or url + sha256 is required", nil)
	}
	if url != "" && sha == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request",
			"sha256 is required when url is provided", nil)
	}
	body := map[string]any{}
	if v := strings.TrimSpace(req.CatalogID); v != "" {
		body["catalog_id"] = v
	}
	if packID != "" {
		body["pack_id"] = packID
	}
	if v := strings.TrimSpace(req.Version); v != "" {
		body["version"] = v
	}
	if url != "" {
		body["url"] = url
	}
	if sha != "" {
		body["sha256"] = sha
	}
	if req.Force {
		body["force"] = true
	}
	if req.Upgrade {
		body["upgrade"] = true
	}
	if req.Inactive {
		body["inactive"] = true
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	var raw map[string]any
	// POST /api/v1/marketplace/install is the JSON install path.
	// /api/v1/packs/install expects multipart bundle upload — wrong
	// shape for an LLM-driven call.
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/marketplace/install", headers, body, &raw); err != nil {
		return nil, err
	}
	return &InstallPackOutput{
		PackID:    firstString(raw, "pack_id", "id"),
		Version:   firstString(raw, "version"),
		Installed: asBool(raw["installed"], true),
	}, nil
}

func (b *HTTPServiceBridge) UninstallPack(ctx context.Context, req UninstallPackInput) error {
	if b == nil {
		return ErrBridgeUnavailable
	}
	packID := strings.TrimSpace(req.PackID)
	if packID == "" {
		return NewBridgeError(http.StatusBadRequest, "invalid_request", "pack_id is required", nil)
	}
	body := map[string]any{}
	if v := strings.TrimSpace(req.Reason); v != "" {
		body["reason"] = v
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	return b.doJSON(ctx, http.MethodPost, "/api/v1/packs/"+url.PathEscape(packID)+"/uninstall", headers, body, nil)
}

func (b *HTTPServiceBridge) RegisterAgent(ctx context.Context, req RegisterAgentInput) (*RegisterAgentOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	name := strings.TrimSpace(req.Name)
	owner := strings.TrimSpace(req.Owner)
	riskTier := strings.TrimSpace(req.RiskTier)
	if name == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "name is required", nil)
	}
	if owner == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "owner is required", nil)
	}
	if riskTier == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "risk_tier is required (one of low|medium|high|critical)", nil)
	}
	body := map[string]any{
		"name":      name,
		"owner":     owner,
		"risk_tier": riskTier,
	}
	if v := strings.TrimSpace(req.Description); v != "" {
		body["description"] = v
	}
	if v := strings.TrimSpace(req.Team); v != "" {
		body["team"] = v
	}
	if len(req.AllowedTopics) > 0 {
		body["allowed_topics"] = append([]string{}, req.AllowedTopics...)
	}
	if len(req.AllowedPools) > 0 {
		body["allowed_pools"] = append([]string{}, req.AllowedPools...)
	}
	if len(req.AllowedServers) > 0 {
		body["allowed_servers"] = append([]string{}, req.AllowedServers...)
	}
	if len(req.AllowedTools) > 0 {
		body["allowed_tools"] = append([]string{}, req.AllowedTools...)
	}
	if len(req.AllowedResources) > 0 {
		body["allowed_resources"] = append([]string{}, req.AllowedResources...)
	}
	if len(req.Entitlements) > 0 {
		body["entitlements"] = append([]string{}, req.Entitlements...)
	}
	if len(req.DataClassifications) > 0 {
		body["data_classifications"] = append([]string{}, req.DataClassifications...)
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	var raw map[string]any
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/agents", headers, body, &raw); err != nil {
		return nil, err
	}
	return &RegisterAgentOutput{
		ID:         firstString(raw, "id"),
		Name:       firstString(raw, "name"),
		Owner:      firstString(raw, "owner"),
		RiskTier:   firstString(raw, "risk_tier"),
		Registered: true,
	}, nil
}

func (b *HTTPServiceBridge) UpdatePolicyBundle(ctx context.Context, req UpdatePolicyBundleInput) (*UpdatePolicyBundleOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	bundleID := strings.TrimSpace(req.BundleID)
	if bundleID == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "bundle_id is required", nil)
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "content is required", nil)
	}
	body := map[string]any{"content": req.Content}
	if v := strings.TrimSpace(req.Author); v != "" {
		body["author"] = v
	}
	if v := strings.TrimSpace(req.Message); v != "" {
		body["message"] = v
	}
	if req.Enabled != nil {
		body["enabled"] = *req.Enabled
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	// Gateway re-encodes the bundle id using ~ for embedded slashes
	// (see policybundles.BundleIDFromRequest). The HTTP bridge keeps
	// to the canonical /api/v1/policy/bundles/{id} shape.
	encoded := strings.ReplaceAll(bundleID, "/", "~")
	var raw map[string]any
	if err := b.doJSON(ctx, http.MethodPut, "/api/v1/policy/bundles/"+url.PathEscape(encoded), headers, body, &raw); err != nil {
		return nil, err
	}
	out := &UpdatePolicyBundleOutput{
		BundleID:  firstString(raw, "id", "bundle_id"),
		UpdatedAt: firstString(raw, "updated_at"),
	}
	if out.BundleID == "" {
		out.BundleID = bundleID
	}
	// The gateway surfaces the signature envelope under `signature`
	// when it signed the content on the operator's behalf.
	if sigRaw, ok := raw["signature"].(map[string]any); ok {
		out.Signed = strings.TrimSpace(asString(sigRaw["value"])) != ""
		out.KeyID = firstString(sigRaw, "key_id")
	}
	return out, nil
}

func (b *HTTPServiceBridge) RevokeWorkerSession(ctx context.Context, req RevokeWorkerSessionInput) error {
	if b == nil {
		return ErrBridgeUnavailable
	}
	workerID := strings.TrimSpace(req.WorkerID)
	if workerID == "" {
		return NewBridgeError(http.StatusBadRequest, "invalid_request", "worker_id is required", nil)
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	body := map[string]any{}
	if v := strings.TrimSpace(req.Reason); v != "" {
		body["reason"] = v
	}
	// POST /api/v1/workers/{id}/revoke-session is the session-
	// specific endpoint. An earlier version targeted DELETE
	// /api/v1/workers/credentials/{id}, which revokes the whole
	// credential (a strictly broader operation than session revoke).
	var sendBody any
	if len(body) > 0 {
		sendBody = body
	}
	return b.doJSON(ctx, http.MethodPost, "/api/v1/workers/"+url.PathEscape(workerID)+"/revoke-session", headers, sendBody, nil)
}

func (b *HTTPServiceBridge) SetAgentScope(ctx context.Context, req SetAgentScopeInput) (*SetAgentScopeOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "agent_id is required", nil)
	}
	body := map[string]any{}
	// The gateway's updateAgentRequest treats nil slices as "don't
	// change" but empty slices as "clear". Explicit []string{} below
	// gives operators a deterministic way to strip scope entirely —
	// required for 'revoke all privileges' flows.
	body["allowed_tools"] = append([]string{}, req.AllowedTools...)
	body["preapproved_mutating_tools"] = append([]string{}, req.PreapprovedMutatingTools...)
	if req.AllowedTopics != nil {
		body["allowed_topics"] = append([]string{}, req.AllowedTopics...)
	}
	if req.AllowedPools != nil {
		body["allowed_pools"] = append([]string{}, req.AllowedPools...)
	}
	if req.AllowedServers != nil {
		body["allowed_servers"] = append([]string{}, req.AllowedServers...)
	}
	if req.AllowedResources != nil {
		body["allowed_resources"] = append([]string{}, req.AllowedResources...)
	}
	if req.Entitlements != nil {
		body["entitlements"] = append([]string{}, req.Entitlements...)
	}
	if req.DataClassifications != nil {
		body["data_classifications"] = append([]string{}, req.DataClassifications...)
	}
	if v := strings.TrimSpace(req.Status); v != "" {
		body["status"] = v
	}
	headers := idempotencyHeaders(req.IdempotencyKey)
	var raw map[string]any
	if err := b.doJSON(ctx, http.MethodPut, "/api/v1/agents/"+url.PathEscape(agentID), headers, body, &raw); err != nil {
		return nil, err
	}
	out := &SetAgentScopeOutput{
		AgentID: firstString(raw, "id"),
		Status:  firstString(raw, "status"),
	}
	if out.AgentID == "" {
		out.AgentID = agentID
	}
	collectStrings := func(key string) []string {
		raw, ok := raw[key].([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if s := strings.TrimSpace(asString(v)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	out.AllowedTools = collectStrings("allowed_tools")
	out.AllowedTopics = collectStrings("allowed_topics")
	out.AllowedPools = collectStrings("allowed_pools")
	out.AllowedServers = collectStrings("allowed_servers")
	out.AllowedResources = collectStrings("allowed_resources")
	out.Entitlements = collectStrings("entitlements")
	out.DataClassifications = collectStrings("data_classifications")
	out.PreapprovedMutatingTools = collectStrings("preapproved_mutating_tools")
	return out, nil
}

// ---------------------------------------------------------------------------
// DirectServiceBridge
// ---------------------------------------------------------------------------

func (b *DirectServiceBridge) CreateWorkflow(ctx context.Context, req CreateWorkflowInput) (*CreateWorkflowOutput, error) {
	if b == nil || b.createWorkflow == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.createWorkflow(ctx, req)
}

func (b *DirectServiceBridge) InstallPack(ctx context.Context, req InstallPackInput) (*InstallPackOutput, error) {
	if b == nil || b.installPack == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.installPack(ctx, req)
}

func (b *DirectServiceBridge) UninstallPack(ctx context.Context, req UninstallPackInput) error {
	if b == nil || b.uninstallPack == nil {
		return ErrBridgeUnavailable
	}
	return b.uninstallPack(ctx, req)
}

func (b *DirectServiceBridge) RegisterAgent(ctx context.Context, req RegisterAgentInput) (*RegisterAgentOutput, error) {
	if b == nil || b.registerAgent == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.registerAgent(ctx, req)
}

func (b *DirectServiceBridge) UpdatePolicyBundle(ctx context.Context, req UpdatePolicyBundleInput) (*UpdatePolicyBundleOutput, error) {
	if b == nil || b.updatePolicyBundle == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.updatePolicyBundle(ctx, req)
}

func (b *DirectServiceBridge) RevokeWorkerSession(ctx context.Context, req RevokeWorkerSessionInput) error {
	if b == nil || b.revokeWorkerSession == nil {
		return ErrBridgeUnavailable
	}
	return b.revokeWorkerSession(ctx, req)
}

func (b *DirectServiceBridge) SetAgentScope(ctx context.Context, req SetAgentScopeInput) (*SetAgentScopeOutput, error) {
	if b == nil || b.setAgentScope == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.setAgentScope(ctx, req)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func idempotencyHeaders(key string) map[string]string {
	h := map[string]string{}
	if v := strings.TrimSpace(key); v != "" {
		h["Idempotency-Key"] = v
	}
	return h
}

func asBool(value any, def bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "true" || s == "yes" || s == "1" {
			return true
		}
		if s == "false" || s == "no" || s == "0" {
			return false
		}
	}
	return def
}
