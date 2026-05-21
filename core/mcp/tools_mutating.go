package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Mutating tool registration + handlers.
//
// Every mutating tool:
//   - sets RequiresApproval=true and ApprovalScope to 'mcp_write_admin'
//     or 'mcp_write' depending on blast radius.
//   - Tags include 'mutating' so scope-filter rules can target the
//     whole family in a single glob.
//   - RiskTier is 'high' (identity / policy surface) or 'medium'
//     (workflow / pack surface). An AgentIdentity with a lower risk
//     tier gets the tool hidden + calls refused.
//   - Description is outcome-first and explicitly tells the LLM
//     how to react to the -32099 approval-required protocol.
//
// The approval gate itself (see registry.go + mcp_gate.go) intercepts
// tools/call BEFORE the handler runs. When an approval is required
// and missing, the gate returns -32099 with the approval_id; the
// handlers below only execute AFTER a human approves and the MCP
// client retries.

const mutatingApprovalHint = "This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve."

// mutatingTags is the common tag set applied to every mutating tool
// so runtime scope-filter rules can refer to the family as a whole.
var mutatingTags = []string{"mutating", "administrative"}

// ---------------------------------------------------------------------------
// Arg schemas
// ---------------------------------------------------------------------------

type createWorkflowArgs struct {
	ID             string           `json:"id,omitempty" description:"Stable workflow identifier. Generated server-side when omitted."`
	Name           string           `json:"name,omitempty" description:"Human-readable workflow name."`
	Description    string           `json:"description,omitempty" description:"Free-form description."`
	OrgID          string           `json:"org_id,omitempty" description:"Tenant to bind the workflow to. Defaults to the caller's tenant."`
	TeamID         string           `json:"team_id,omitempty" description:"Team label for the workflow."`
	Version        string           `json:"version,omitempty" description:"Workflow version tag."`
	TimeoutSec     int64            `json:"timeout_sec,omitempty" description:"Soft timeout for the whole run (seconds)."`
	Steps          map[string]any   `json:"steps" required:"true" description:"Step definitions keyed by step id. Top-level field — do NOT wrap in a 'spec' object."`
	Config         map[string]any   `json:"config,omitempty" description:"Workflow-wide configuration object."`
	Parameters     []map[string]any `json:"parameters,omitempty" description:"Declared input parameters."`
	InputSchema    map[string]any   `json:"input_schema,omitempty" description:"JSON Schema for run inputs (gateway validates each run against this)."`
	IdempotencyKey string           `json:"idempotency_key,omitempty" description:"Retry-safe key. If the same key is seen twice the gateway returns the prior result instead of creating a second workflow."`
}

type installPackArgs struct {
	CatalogID      string `json:"catalog_id,omitempty" description:"Marketplace catalog to resolve the pack from. Optional when the default catalogue can find the pack_id."`
	PackID         string `json:"pack_id,omitempty" description:"Canonical pack ID (e.g. 'cordum/slack'). Required when installing from a catalogue rather than a direct URL."`
	Version        string `json:"version,omitempty" description:"Pinned version. Defaults to the marketplace's latest stable."`
	URL            string `json:"url,omitempty" description:"Direct pack bundle URL (private / air-gapped installs). sha256 is required when url is set."`
	Sha256         string `json:"sha256,omitempty" description:"Expected SHA-256 digest of the bundle at url. Required when url is provided."`
	Force          bool   `json:"force,omitempty" description:"Overwrite an existing installation."`
	Upgrade        bool   `json:"upgrade,omitempty" description:"Allow version bump over an existing install."`
	Inactive       bool   `json:"inactive,omitempty" description:"Install the pack but leave it disabled until explicitly activated."`
	IdempotencyKey string `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

type uninstallPackArgs struct {
	PackID         string `json:"pack_id" required:"true" description:"Pack to uninstall."`
	Reason         string `json:"reason,omitempty" description:"Audit-visible justification for the uninstall."`
	IdempotencyKey string `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

type registerAgentArgs struct {
	Name                string   `json:"name" required:"true" description:"Human-readable name for the agent (e.g. 'release-bot')."`
	Description         string   `json:"description,omitempty" description:"What this agent does."`
	Owner               string   `json:"owner" required:"true" description:"Owning team / org for audit attribution."`
	Team                string   `json:"team,omitempty" description:"Team/department label."`
	RiskTier            string   `json:"risk_tier" required:"true" enum:"low,medium,high,critical" description:"Risk tier for this identity."`
	AllowedTopics       []string `json:"allowed_topics,omitempty" description:"Topics this agent may drive jobs into."`
	AllowedPools        []string `json:"allowed_pools,omitempty" description:"Worker pools this agent may schedule against."`
	AllowedServers      []string `json:"allowed_servers,omitempty" description:"MCP server-name globs this agent may call. Omit to fail closed on server-guarded MCP actions until set."`
	AllowedTools        []string `json:"allowed_tools,omitempty" description:"MCP tool names the agent may call. Omit for default scope."`
	AllowedResources    []string `json:"allowed_resources,omitempty" description:"cordum:// resource URI globs this agent may target. Omit to fail closed on resource-guarded MCP actions until set."`
	Entitlements        []string `json:"entitlements,omitempty" description:"Capability tokens this identity holds for MCP actions that declare required_entitlement."`
	DataClassifications []string `json:"data_classifications,omitempty" description:"Data sensitivity labels this agent is cleared for."`
	IdempotencyKey      string   `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

type updatePolicyBundleArgs struct {
	BundleID       string `json:"bundle_id" required:"true" description:"Policy bundle ID (e.g. 'secops/core')."`
	Content        string `json:"content" required:"true" description:"Full YAML content of the new bundle. Gateway signs with the tenant policy-signing key before persisting — MCP client never holds the key."`
	Author         string `json:"author,omitempty" description:"Audit actor label. Defaults to the calling principal."`
	Message        string `json:"message,omitempty" description:"Commit-style message stored on the audit entry."`
	Enabled        *bool  `json:"enabled,omitempty" description:"Whether the bundle is active after save."`
	IdempotencyKey string `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

type revokeWorkerSessionArgs struct {
	WorkerID       string `json:"worker_id" required:"true" description:"Worker whose credential should be revoked."`
	Reason         string `json:"reason,omitempty" description:"Incident or rotation justification. Audit-visible."`
	IdempotencyKey string `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

type setAgentScopeArgs struct {
	AgentID                  string   `json:"agent_id" required:"true" description:"Agent identity to update."`
	AllowedTools             []string `json:"allowed_tools" description:"Full replacement list of MCP tools the agent may call. Pass an empty array to remove all read-only allowances."`
	AllowedTopics            []string `json:"allowed_topics,omitempty" description:"Full replacement list of topics the agent may drive jobs into."`
	AllowedPools             []string `json:"allowed_pools,omitempty" description:"Full replacement list of worker pools the agent may schedule against."`
	AllowedServers           []string `json:"allowed_servers,omitempty" description:"Full replacement list of MCP server-name globs this agent may call. Pass an empty array to remove all server allowances."`
	AllowedResources         []string `json:"allowed_resources,omitempty" description:"Full replacement list of cordum:// resource URI globs this agent may target. Pass an empty array to remove all resource allowances."`
	Entitlements             []string `json:"entitlements,omitempty" description:"Full replacement list of capability tokens this identity holds for required-entitlement checks."`
	DataClassifications      []string `json:"data_classifications,omitempty" description:"Full replacement list of data classifications the agent is cleared for."`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools" description:"Mutating tools this agent may call WITHOUT human approval. Use for CI-CD bots only — documented in docs/mcp/scope-preapproval.md. Pass empty array to require approval for every mutating call."`
	Status                   string   `json:"status,omitempty" enum:"active,suspended,revoked" description:"Identity lifecycle state."`
	IdempotencyKey           string   `json:"idempotency_key,omitempty" description:"Retry-safe key."`
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

type mutatingToolSpec struct {
	tool    Tool
	handler ToolHandler
}

// mutatingToolSpecs returns every mutating tool bound to the given
// ServiceBridge. Exposed separately so tests can assert the metadata
// shape (RequiresApproval + ApprovalScope + RiskTier) without wiring
// the whole registry.
func mutatingToolSpecs(bridge ServiceBridge) []mutatingToolSpec {
	return []mutatingToolSpec{
		{
			tool: Tool{
				Name: ToolCreateWorkflow,
				Description: "Create a new workflow definition from a spec. " +
					"Use this when: the operator describes a multi-step automation and wants it registered so it can be triggered by `cordum_trigger_workflow` later. " +
					"Returns the new workflow_id. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(createWorkflowArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWrite,
				Tags:             mutatingTags,
				RiskTier:         "medium",
			},
			handler: createWorkflowHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolInstallPack,
				Description: "Install a marketplace pack so its capabilities become available to agents. " +
					"Use this when: the operator asks to 'install X' or 'add the X integration'. " +
					"Returns {pack_id, version, installed}. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(installPackArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWrite,
				Tags:             mutatingTags,
				RiskTier:         "medium",
			},
			handler: installPackHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolUninstallPack,
				Description: "Uninstall a previously installed pack, revoking its capabilities. " +
					"Use this when: the operator asks to 'remove' or 'uninstall' a pack or flags one as compromised. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(uninstallPackArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWriteAdmin,
				Tags:             mutatingTags,
				RiskTier:         "high",
			},
			handler: uninstallPackHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolRegisterAgent,
				Description: "Register a new AI agent identity so it can authenticate against the MCP gateway. " +
					"Use this when: the operator wants to bring a new CI bot, agent framework, or LLM client onto the platform. " +
					"Returns the stable agent ID. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(registerAgentArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWriteAdmin,
				Tags:             mutatingTags,
				RiskTier:         "high",
			},
			handler: registerAgentHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolUpdatePolicyBundle,
				Description: "Save a new version of a policy bundle. The gateway signs the content with the tenant's policy-signing key before persisting — the MCP client never holds the private key. " +
					"Use this when: the operator wants to tighten / loosen a rule set or deploy a drafted bundle. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(updatePolicyBundleArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWriteAdmin,
				Tags:             mutatingTags,
				RiskTier:         "high",
			},
			handler: updatePolicyBundleHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolRevokeWorkerSession,
				Description: "Revoke a worker's active session credential, forcing it to re-authenticate. " +
					"Use this when: a credential has been compromised, rotated, or the worker is being decommissioned. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(revokeWorkerSessionArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWriteAdmin,
				Tags:             mutatingTags,
				RiskTier:         "high",
			},
			handler: revokeWorkerSessionHandler(bridge),
		},
		{
			tool: Tool{
				Name: ToolSetAgentScope,
				Description: "Update an agent's authorized tool list and mutating-tool preapproval allowlist. " +
					"Use this when: the operator wants to grant / revoke capabilities for an existing identity. " +
					"The `preapproved_mutating_tools` field is high-privilege — agents on that list bypass human approval for the listed mutating calls. Reserve for CI bots. " + mutatingApprovalHint,
				InputSchema:      jsonSchema(setAgentScopeArgs{}),
				RequiresApproval: true,
				ApprovalScope:    ApprovalScopeWriteAdmin,
				Tags:             mutatingTags,
				RiskTier:         "high",
			},
			handler: setAgentScopeHandler(bridge),
		},
	}
}

// ---------------------------------------------------------------------------
// Handlers — each decodes → validates → forwards → maps error → emits JSON.
// ---------------------------------------------------------------------------

func createWorkflowHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args createWorkflowArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if len(args.Steps) == 0 {
			return nil, fmt.Errorf("%w: steps are required (at least one)", ErrInvalidParams)
		}
		out, err := bridge.CreateWorkflow(ctx, CreateWorkflowInput{
			ID:             strings.TrimSpace(args.ID),
			Name:           strings.TrimSpace(args.Name),
			Description:    strings.TrimSpace(args.Description),
			OrgID:          strings.TrimSpace(args.OrgID),
			TeamID:         strings.TrimSpace(args.TeamID),
			Version:        strings.TrimSpace(args.Version),
			TimeoutSec:     args.TimeoutSec,
			Steps:          args.Steps,
			Config:         args.Config,
			Parameters:     args.Parameters,
			InputSchema:    args.InputSchema,
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"workflow_id": out.WorkflowID,
			"version":     out.Version,
			"status":      "created",
		}), nil
	}
}

func installPackHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args installPackArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		packID := strings.TrimSpace(args.PackID)
		url := strings.TrimSpace(args.URL)
		if packID == "" && url == "" {
			return nil, fmt.Errorf("%w: either pack_id or url is required", ErrInvalidParams)
		}
		if url != "" && strings.TrimSpace(args.Sha256) == "" {
			return nil, fmt.Errorf("%w: sha256 is required when url is provided", ErrInvalidParams)
		}
		out, err := bridge.InstallPack(ctx, InstallPackInput{
			CatalogID:      strings.TrimSpace(args.CatalogID),
			PackID:         packID,
			Version:        strings.TrimSpace(args.Version),
			URL:            url,
			Sha256:         strings.TrimSpace(args.Sha256),
			Force:          args.Force,
			Upgrade:        args.Upgrade,
			Inactive:       args.Inactive,
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"pack_id":   out.PackID,
			"version":   out.Version,
			"installed": out.Installed,
		}), nil
	}
}

func uninstallPackHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args uninstallPackArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if strings.TrimSpace(args.PackID) == "" {
			return nil, fmt.Errorf("%w: pack_id is required", ErrInvalidParams)
		}
		if err := bridge.UninstallPack(ctx, UninstallPackInput{
			PackID:         strings.TrimSpace(args.PackID),
			Reason:         strings.TrimSpace(args.Reason),
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		}); err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"pack_id":     strings.TrimSpace(args.PackID),
			"uninstalled": true,
		}), nil
	}
}

func registerAgentHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args registerAgentArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return nil, fmt.Errorf("%w: name is required", ErrInvalidParams)
		}
		if strings.TrimSpace(args.Owner) == "" {
			return nil, fmt.Errorf("%w: owner is required", ErrInvalidParams)
		}
		if strings.TrimSpace(args.RiskTier) == "" {
			return nil, fmt.Errorf("%w: risk_tier is required", ErrInvalidParams)
		}
		out, err := bridge.RegisterAgent(ctx, RegisterAgentInput{
			Name:                strings.TrimSpace(args.Name),
			Description:         strings.TrimSpace(args.Description),
			Owner:               strings.TrimSpace(args.Owner),
			Team:                strings.TrimSpace(args.Team),
			RiskTier:            strings.TrimSpace(args.RiskTier),
			AllowedTopics:       append([]string{}, args.AllowedTopics...),
			AllowedPools:        append([]string{}, args.AllowedPools...),
			AllowedServers:      append([]string{}, args.AllowedServers...),
			AllowedTools:        append([]string{}, args.AllowedTools...),
			AllowedResources:    append([]string{}, args.AllowedResources...),
			Entitlements:        append([]string{}, args.Entitlements...),
			DataClassifications: append([]string{}, args.DataClassifications...),
			IdempotencyKey:      strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"id":         out.ID,
			"name":       out.Name,
			"owner":      out.Owner,
			"risk_tier":  out.RiskTier,
			"registered": out.Registered,
		}), nil
	}
}

func updatePolicyBundleHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args updatePolicyBundleArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if strings.TrimSpace(args.BundleID) == "" {
			return nil, fmt.Errorf("%w: bundle_id is required", ErrInvalidParams)
		}
		if strings.TrimSpace(args.Content) == "" {
			return nil, fmt.Errorf("%w: content is required", ErrInvalidParams)
		}
		out, err := bridge.UpdatePolicyBundle(ctx, UpdatePolicyBundleInput{
			BundleID:       strings.TrimSpace(args.BundleID),
			Content:        args.Content,
			Author:         strings.TrimSpace(args.Author),
			Message:        strings.TrimSpace(args.Message),
			Enabled:        args.Enabled,
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"bundle_id":  out.BundleID,
			"updated_at": out.UpdatedAt,
			"signed":     out.Signed,
			"key_id":     out.KeyID,
		}), nil
	}
}

func revokeWorkerSessionHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args revokeWorkerSessionArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if strings.TrimSpace(args.WorkerID) == "" {
			return nil, fmt.Errorf("%w: worker_id is required", ErrInvalidParams)
		}
		if err := bridge.RevokeWorkerSession(ctx, RevokeWorkerSessionInput{
			WorkerID:       strings.TrimSpace(args.WorkerID),
			Reason:         strings.TrimSpace(args.Reason),
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		}); err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"worker_id": strings.TrimSpace(args.WorkerID),
			"revoked":   true,
		}), nil
	}
}

func setAgentScopeHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args setAgentScopeArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if strings.TrimSpace(args.AgentID) == "" {
			return nil, fmt.Errorf("%w: agent_id is required", ErrInvalidParams)
		}
		out, err := bridge.SetAgentScope(ctx, SetAgentScopeInput{
			AgentID:                  strings.TrimSpace(args.AgentID),
			AllowedTools:             append([]string{}, args.AllowedTools...),
			AllowedTopics:            args.AllowedTopics,
			AllowedPools:             args.AllowedPools,
			AllowedServers:           args.AllowedServers,
			AllowedResources:         args.AllowedResources,
			Entitlements:             args.Entitlements,
			DataClassifications:      args.DataClassifications,
			PreapprovedMutatingTools: append([]string{}, args.PreapprovedMutatingTools...),
			Status:                   strings.TrimSpace(args.Status),
			IdempotencyKey:           strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapMutatingBridgeError(err), nil
		}
		return jsonOK(map[string]any{
			"agent_id":                   out.AgentID,
			"allowed_tools":              out.AllowedTools,
			"allowed_topics":             out.AllowedTopics,
			"allowed_pools":              out.AllowedPools,
			"allowed_servers":            out.AllowedServers,
			"allowed_resources":          out.AllowedResources,
			"entitlements":               out.Entitlements,
			"data_classifications":       out.DataClassifications,
			"preapproved_mutating_tools": out.PreapprovedMutatingTools,
			"status":                     out.Status,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// Error mapping — translates BridgeError into a ToolCallResult with
// IsError=true and a JSON body carrying the gateway code + details.
// The approval gate sits ABOVE the handler so we never see a 403-ish
// pending-approval error here; everything that gets this far is a
// genuine gateway-level problem.
// ---------------------------------------------------------------------------

func mapMutatingBridgeError(err error) *ToolCallResult {
	if err == nil {
		return nil
	}
	var be *BridgeError
	if errors.As(err, &be) {
		payload := map[string]any{
			"error":       be.Message,
			"code":        be.Code,
			"status_code": be.StatusCode,
		}
		if be.Details != nil {
			payload["details"] = be.Details
		}
		raw, _ := json.Marshal(payload)
		return &ToolCallResult{
			IsError: true,
			Content: []ContentItem{{
				Type: "text",
				Text: string(raw),
			}},
		}
	}
	// Non-BridgeError — wrap in a generic error payload.
	raw, _ := json.Marshal(map[string]any{
		"error": err.Error(),
		"code":  "internal_error",
	})
	return &ToolCallResult{
		IsError: true,
		Content: []ContentItem{{Type: "text", Text: string(raw)}},
	}
}

func jsonOK(payload map[string]any) *ToolCallResult {
	raw, _ := json.Marshal(payload)
	return &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: string(raw)}},
	}
}
