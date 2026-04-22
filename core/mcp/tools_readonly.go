package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Read-only discovery tools (task-466b6a6a). This file holds the
// argument structs, handler builders, and tool descriptions for
// cordum_list_jobs through cordum_status. Descriptions are
// hand-written, outcome-first, and carry a "when to use" cue so an
// LLM planner can match natural-language operator questions without
// the intermediate step of reading the docs.
//
// The handlers always return the gateway's JSON shape verbatim on
// success — no re-labelling, no field renames — so the MCP client's
// downstream tools can chain on stable ids.

// ------------------------------------------------------------------
// Argument schemas. Every list tool accepts an identical envelope so
// LLM planners only have to learn one shape: {cursor, page_size,
// filter}. AuditQuery extends that with event_type/since/until.
// ------------------------------------------------------------------

type listEnvelopeArgs struct {
	Cursor   string            `json:"cursor,omitempty" description:"Opaque pagination cursor from a previous response. Leave empty for the first page."`
	PageSize int               `json:"page_size,omitempty" default:"50" description:"Records per page. Default 50, maximum 500."`
	Filter   map[string]string `json:"filter,omitempty" description:"Optional field filters (e.g. {\"state\":\"pending\", \"topic\":\"job.default\"}). Available filters vary per endpoint — see docs/mcp/tools.md."`
}

type getByIDArgs struct {
	ID string `json:"id" required:"true" description:"Unique identifier. For jobs pass the job_id, for runs the run_id, etc."`
}

type auditQueryArgs struct {
	Cursor    string            `json:"cursor,omitempty" description:"Opaque pagination cursor."`
	PageSize  int               `json:"page_size,omitempty" default:"50" description:"Records per page (max 500)."`
	Filter    map[string]string `json:"filter,omitempty" description:"Optional field filters."`
	Tenant    string            `json:"tenant,omitempty" description:"Tenant ID. Defaults to the caller's tenant; cross-tenant access requires admin role."`
	EventType string            `json:"event_type,omitempty" description:"Filter by SIEMEvent type (e.g. 'safety.decision', 'mcp.tool_approval')."`
	Since     string            `json:"since,omitempty" description:"RFC3339 lower bound on timestamp (inclusive). Example: 2026-04-17T00:00:00Z."`
	Until     string            `json:"until,omitempty" description:"RFC3339 upper bound on timestamp (exclusive)."`
}

type auditVerifyArgs struct {
	Tenant string `json:"tenant,omitempty" description:"Tenant whose audit chain to verify. Defaults to the caller's tenant."`
}

type noArgs struct{}

// ------------------------------------------------------------------
// LLM-optimised tool descriptions. Keep each under ~280 chars so they
// fit in the planning prompt's tool catalogue; every description opens
// with the outcome, then a "Use this when ..." cue, then a representative
// example.
// ------------------------------------------------------------------

const (
	descListJobs = "List jobs the caller's tenant has submitted to Cordum, newest first. Returns " +
		"job id, topic, state, submitter, and timestamps. Use this when the operator asks " +
		"'what jobs ran today?', 'any failures in the last hour?', or needs to find a job " +
		"id before cancelling or inspecting it."

	descGetJob = "Fetch the full record for a single job by id, including prompt, topic, policy " +
		"decision, retry history, and final state. Use this when the operator says 'show me " +
		"job X' or 'why did job X fail?' — the response includes the safety decision and " +
		"any denial reason."

	descListRuns = "List workflow runs the tenant has initiated, newest first. Includes run id, " +
		"workflow id, state, start/end timestamps. Use this when the operator asks 'which " +
		"workflows are running now?' or wants to page through recent runs before opening one."

	descGetRun = "Fetch a workflow run by id with its graph state, pending steps, and outputs. " +
		"Use this when the operator says 'what is run X doing now?' or 'did run X finish?'."

	descRunTimeline = "Return the ordered timeline of state transitions and step events for a " +
		"workflow run. Use this when debugging — the operator asks 'what happened in run X?' " +
		"or 'where did run X get stuck?'. Output is a list of {timestamp, event_type, step_id, details}."

	descListWorkflows = "List workflow definitions available to the tenant. Each entry has workflow " +
		"id, version, human title, and step count. Use this when the operator asks 'what " +
		"workflows do I have?' or needs a workflow id before triggering a run."

	descListPacks = "List installed integration packs (Slack, GitHub, AWS, etc.) and their status. " +
		"Use this when the operator asks 'what integrations are live?' or 'is the Slack pack " +
		"installed?'. Returns pack id, version, enabled, and install timestamp."

	descListTopics = "List job topics registered on the platform — the allow-listed channels jobs " +
		"can be published to. Use this when the operator asks 'what topics can I submit to?' " +
		"or is authoring a policy and needs the topic catalogue."

	descListWorkers = "List workers currently registered (both in-cluster and external). Each entry " +
		"has worker id, pool, capabilities, last-seen, status. Use this when the operator " +
		"asks 'which agents are online?' or 'is worker X reachable?'."

	descListAgents = "List agent identities configured in Cordum — their allowed tools, risk tier, " +
		"and data classifications. Use this when the operator asks 'which agents can call " +
		"tool X?' or is reviewing an agent before granting a new scope."

	descListPendingApprovals = "List approval requests currently waiting for a human decision across " +
		"both job approvals and MCP tool-call approvals. Use this when the operator says " +
		"'what needs my approval?' or before batch-approving with cordumctl."

	descAuditQuery = "Search the audit chain for SIEMEvents matching filters like tenant, event_type, " +
		"and time window. Use this when the operator asks 'who changed policy X?', 'what " +
		"happened around time T?', or 'did tool Y get called?'. Returns chain-verified events " +
		"(seq + event_hash + prev_hash) so callers can prove integrity downstream."

	descAuditVerify = "Walk the tenant's audit Merkle chain and report integrity: ok / compromised / " +
		"partial. Use this when the operator asks 'is our audit log clean?' or before handing " +
		"a compliance auditor evidence. Response includes any gaps with sequence numbers."

	descStatus = "Report platform health at a glance: queue depth, per-component readiness, last " +
		"policy snapshot, active worker count. Use this when the operator asks 'is Cordum " +
		"healthy?' or 'how far behind is the scheduler?'."
)

// ------------------------------------------------------------------
// Read-only tool specs. appendReadOnlyToolSpecs is called from
// RegisterAllTools so every transport that builds the registry gets
// the same catalogue.
// ------------------------------------------------------------------

type toolSpec struct {
	tool    Tool
	handler ToolHandler
}

func readOnlyToolSpecs(bridge ServiceBridge) []toolSpec {
	return []toolSpec{
		{
			tool: Tool{
				Name:        ToolListJobs,
				Description: descListJobs,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListJobs),
		},
		{
			tool: Tool{
				Name:        ToolGetJob,
				Description: descGetJob,
				InputSchema: jsonSchema(getByIDArgs{}),
			},
			handler: getByIDHandler(bridge.GetJob),
		},
		{
			tool: Tool{
				Name:        ToolListRuns,
				Description: descListRuns,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListRuns),
		},
		{
			tool: Tool{
				Name:        ToolGetRun,
				Description: descGetRun,
				InputSchema: jsonSchema(getByIDArgs{}),
			},
			handler: getByIDHandler(bridge.GetRun),
		},
		{
			tool: Tool{
				Name:        ToolRunTimeline,
				Description: descRunTimeline,
				InputSchema: jsonSchema(getByIDArgs{}),
			},
			handler: getByIDHandler(bridge.GetRunTimeline),
		},
		{
			tool: Tool{
				Name:        ToolListWorkflows,
				Description: descListWorkflows,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListWorkflows),
		},
		{
			tool: Tool{
				Name:        ToolListPacks,
				Description: descListPacks,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListPacks),
		},
		{
			tool: Tool{
				Name:        ToolListTopics,
				Description: descListTopics,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListTopics),
		},
		{
			tool: Tool{
				Name:        ToolListWorkers,
				Description: descListWorkers,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListWorkers),
		},
		{
			tool: Tool{
				Name:        ToolListAgents,
				Description: descListAgents,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListAgents),
		},
		{
			tool: Tool{
				Name:        ToolListPendingApprovals,
				Description: descListPendingApprovals,
				InputSchema: jsonSchema(listEnvelopeArgs{}),
			},
			handler: listHandler(bridge.ListPendingApprovals),
		},
		{
			tool: Tool{
				Name:        ToolAuditQuery,
				Description: descAuditQuery,
				InputSchema: jsonSchema(auditQueryArgs{}),
			},
			handler: auditQueryHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolAuditVerify,
				Description: descAuditVerify,
				InputSchema: jsonSchema(auditVerifyArgs{}),
			},
			handler: auditVerifyHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolStatus,
				Description: descStatus,
				InputSchema: jsonSchema(noArgs{}),
			},
			handler: statusHandler(bridge),
		},
	}
}

// listHandler wraps a ServiceBridge.List method into a ToolHandler.
// Decodes listEnvelopeArgs, calls the method, returns the ListPage as
// JSON content.
func listHandler(fn func(context.Context, ListInput) (*ListPage, error)) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args listEnvelopeArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		page, err := fn(ctx, ListInput{
			Cursor:   strings.TrimSpace(args.Cursor),
			PageSize: args.PageSize,
			Filter:   args.Filter,
		})
		if err != nil {
			return nil, mapBridgeError(err)
		}
		return jsonResult(page)
	}
}

// getByIDHandler wraps a ServiceBridge.Get method into a ToolHandler.
func getByIDHandler(fn func(context.Context, string) (*ResourceItem, error)) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args getByIDArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return nil, fmt.Errorf("%w: id is required", ErrInvalidParams)
		}
		item, err := fn(ctx, id)
		if err != nil {
			return nil, mapBridgeError(err)
		}
		return jsonResult(item)
	}
}

func auditQueryHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args auditQueryArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		page, err := bridge.QueryAudit(ctx, AuditQueryInput{
			ListInput: ListInput{
				Cursor:   strings.TrimSpace(args.Cursor),
				PageSize: args.PageSize,
				Filter:   args.Filter,
				Tenant:   strings.TrimSpace(args.Tenant),
			},
			Tenant:    strings.TrimSpace(args.Tenant),
			EventType: strings.TrimSpace(args.EventType),
			Since:     strings.TrimSpace(args.Since),
			Until:     strings.TrimSpace(args.Until),
		})
		if err != nil {
			return nil, mapBridgeError(err)
		}
		return jsonResult(page)
	}
}

func auditVerifyHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args auditVerifyArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		item, err := bridge.VerifyAudit(ctx, strings.TrimSpace(args.Tenant))
		if err != nil {
			return nil, mapBridgeError(err)
		}
		return jsonResult(item)
	}
}

func statusHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		item, err := bridge.GetStatus(ctx)
		if err != nil {
			return nil, mapBridgeError(err)
		}
		return jsonResult(item)
	}
}

// jsonResult marshals any value into a single-text-item ToolCallResult.
// Every read-only tool uses this so the wire shape stays uniform.
func jsonResult(v any) (*ToolCallResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	return &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: string(data)}},
	}, nil
}

// mapBridgeError turns a *BridgeError into a user-friendly wrapped
// error. For 4xx we keep the error detail; for 5xx we add a generic
// hint so LLM clients don't reveal internal failure modes.
func mapBridgeError(err error) error {
	if err == nil {
		return nil
	}
	var berr *BridgeError
	for e := err; e != nil; {
		if v, ok := e.(*BridgeError); ok {
			berr = v
			break
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	if berr == nil {
		return err
	}
	if berr.StatusCode == 501 {
		return fmt.Errorf("%w: bridge does not support this read-only method", ErrBridgeUnavailable)
	}
	return err
}
