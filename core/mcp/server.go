package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	jsonRPCParseErrorCode     = -32700
	jsonRPCInvalidRequestCode = -32600
	jsonRPCMethodNotFoundCode = -32601
	jsonRPCInvalidParamsCode  = -32602
	jsonRPCInternalErrorCode  = -32603
	// jsonRPCApprovalRequiredCode is the Cordum-reserved JSON-RPC code
	// returned when a tool call is gated by a human-approval rule.
	jsonRPCApprovalRequiredCode = -32099
	// jsonRPCNotAuthorizedCode is returned when the scope filter rejects
	// a tools/call. error.data carries {tool, sub_reason, agent_id}.
	jsonRPCNotAuthorizedCode = -32098
	// jsonRPCGatewayMisconfiguredCode is returned when the approval gate
	// (or another dependency wired at startup) is missing the context it
	// needs to evaluate a call — typically a middleware wiring bug such
	// as a missing WithMCPCallMetadata before dispatch. Distinct from
	// -32603 so operators can page on it specifically.
	jsonRPCGatewayMisconfiguredCode = -32097
)

var (
	ErrMethodNotFound = errors.New("mcp method not found")
	ErrInvalidParams  = errors.New("mcp invalid params")
	// ErrApprovalGateMisconfigured signals an ApprovalGate implementation
	// was invoked without the startup-time dependencies it needs (e.g.
	// request-scoped tenant/agent metadata not propagated by middleware).
	// Mapped to JSON-RPC -32097 so ops can distinguish a gateway wiring
	// defect from an ordinary handler failure (-32603).
	ErrApprovalGateMisconfigured = errors.New("mcp: approval gate misconfigured")
)

// ToolService provides tool listing and execution for MCP server handlers.
type ToolService interface {
	// ListTools returns the tools visible to the caller identified by ctx.
	// Implementations apply the scope filter (AllowedTools, RiskTier,
	// DataClassifications). When ctx has no identity, callers should see
	// an empty list — fail closed.
	ListTools(ctx context.Context) []Tool
	Call(ctx context.Context, name string, params json.RawMessage) (*ToolCallResult, error)
}

// ResourceService provides resource listing and reads for MCP server handlers.
type ResourceService interface {
	List() []Resource
	ListTemplates() []ResourceTemplate
	Read(ctx context.Context, uri string) (*ResourceContents, error)
}

// ServerConfig configures MCP server behavior.
type ServerConfig struct {
	Name            string
	Version         string
	ProtocolVersion string
	RequestTimeout  time.Duration
}

// MCPServer is the JSON-RPC 2.0 server implementation for MCP.
type MCPServer struct {
	transport Transport
	tools     ToolService
	resources ResourceService
	prompts   PromptService
	cfg       ServerConfig
	// auditor, when non-nil, brackets every tools/call with
	// StartInbound/FinishInbound so successful and failed handler
	// returns produce a mcp.tool_invocation SIEMEvent. Wire via
	// WithAuditor during gateway boot.
	auditor ToolInvocationAuditor
}

// WithAuditor attaches a ToolInvocationAuditor so the server emits
// mcp.tool_invocation events for every terminal tools/call. Returns
// the server for fluent chaining. Passing nil leaves the server as-is.
func (s *MCPServer) WithAuditor(a ToolInvocationAuditor) *MCPServer {
	if s == nil {
		return s
	}
	s.auditor = a
	return s
}

// WithPrompts attaches a PromptService so prompts/list + prompts/get
// return registered prompts. Nil leaves the server without prompts —
// older MCP clients see an empty list rather than method-not-found.
func (s *MCPServer) WithPrompts(p PromptService) *MCPServer {
	if s == nil {
		return s
	}
	s.prompts = p
	return s
}

// NewServer creates an MCP server instance.
func NewServer(transport Transport, tools ToolService, resources ResourceService, cfg ServerConfig) *MCPServer {
	if cfg.Name == "" {
		cfg.Name = "cordum"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.ProtocolVersion == "" {
		cfg.ProtocolVersion = DefaultProtocolVersion
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	return &MCPServer{
		transport: transport,
		tools:     tools,
		resources: resources,
		cfg:       cfg,
	}
}

// Serve runs the request loop until transport is closed.
func (s *MCPServer) Serve() error {
	if s == nil || s.transport == nil {
		return fmt.Errorf("transport required")
	}
	for {
		msg, err := s.transport.ReadMessage()
		if err != nil {
			if errors.Is(err, ErrInvalidMessage) {
				parseErr := &JSONRPCMessage{
					JSONRPC: JSONRPCVersion,
					Error: &JSONRPCError{
						Code:    jsonRPCParseErrorCode,
						Message: "parse error",
					},
				}
				if writeErr := s.transport.WriteMessage(parseErr); writeErr != nil && !errors.Is(writeErr, ErrTransportClosed) {
					return writeErr
				}
				continue
			}
			if errors.Is(err, io.EOF) || errors.Is(err, ErrTransportClosed) {
				return nil
			}
			return err
		}
		if msg == nil {
			continue
		}
		if strings.TrimSpace(msg.Method) == "" {
			continue
		}
		resp := s.handleMessage(msg)
		if resp == nil {
			continue
		}
		resp.sessionID = msg.sessionID
		if err := s.transport.WriteMessage(resp); err != nil && !errors.Is(err, ErrTransportClosed) {
			return err
		}
	}
}

func (s *MCPServer) handleMessage(msg *JSONRPCMessage) *JSONRPCMessage {
	if msg == nil {
		return nil
	}
	// Derive the dispatch ctx from the ORIGINAL request ctx when the
	// transport attached one. This preserves tenant (mcp.WithTenant),
	// MCPCallMetadata (approval gate key), and any request-scoped
	// values installed by the gateway middleware. Fall back to
	// context.Background() for transports that pre-date the requestCtx
	// field (stdio, older tests).
	parent := msg.requestCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, s.cfg.RequestTimeout)
	defer cancel()
	if msg.identity != nil {
		ctx = ContextWithIdentity(ctx, msg.identity)
	}

	result, rpcErr := s.dispatch(ctx, msg)
	if !messageHasID(msg.ID) {
		return nil
	}
	if rpcErr != nil {
		return &JSONRPCMessage{
			JSONRPC: JSONRPCVersion,
			ID:      msg.ID,
			Error:   rpcErr,
		}
	}
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      msg.ID,
		Result:  result,
	}
}

func (s *MCPServer) dispatch(ctx context.Context, msg *JSONRPCMessage) (any, *JSONRPCError) {
	if msg == nil {
		return nil, s.rpcError(jsonRPCInvalidRequestCode, "invalid request", nil)
	}
	switch msg.Method {
	case MethodInitialize:
		return s.handleInitialize(msg.Params)
	case MethodPing:
		return s.handlePing()
	case MethodToolsList:
		return s.handleToolsList(ctx)
	case MethodToolsCall:
		return s.handleToolsCall(ctx, msg.Params)
	case MethodResourcesList:
		return s.handleResourcesList()
	case MethodResourceTemplates:
		return s.handleResourceTemplatesList()
	case MethodResourcesRead:
		return s.handleResourcesRead(ctx, msg.Params)
	case MethodPromptsList:
		return s.handlePromptsList(ctx)
	case MethodPromptsGet:
		return s.handlePromptsGet(ctx, msg.Params)
	default:
		return nil, s.rpcError(jsonRPCMethodNotFoundCode, "method not found", msg.Method)
	}
}

// handlePromptsList returns the registered prompts. Empty list when no
// PromptRegistry is configured — mirrors tools/list behaviour so older
// MCP clients don't see a "method not found" on an empty server.
func (s *MCPServer) handlePromptsList(ctx context.Context) (*PromptListResult, *JSONRPCError) {
	if s.prompts == nil {
		return &PromptListResult{Prompts: []Prompt{}}, nil
	}
	prompts := s.prompts.List(ctx)
	if prompts == nil {
		prompts = []Prompt{}
	}
	return &PromptListResult{Prompts: prompts}, nil
}

// handlePromptsGet renders a registered prompt with the caller's
// arguments. Returns -32602 when arguments fail the prompt's schema,
// -32601 (method-not-found) when the named prompt is absent.
func (s *MCPServer) handlePromptsGet(ctx context.Context, params json.RawMessage) (*PromptGetResult, *JSONRPCError) {
	if s.prompts == nil {
		return nil, s.rpcError(jsonRPCMethodNotFoundCode, "prompt service unavailable", nil)
	}
	var req PromptGetParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", "name is required")
	}
	result, err := s.prompts.Render(ctx, req.Name, req.Arguments)
	if err != nil {
		if errors.Is(err, ErrPromptNotFound) {
			return nil, s.rpcError(jsonRPCMethodNotFoundCode, "prompt not found", req.Name)
		}
		if errors.Is(err, ErrPromptInvalidArgs) {
			return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
		}
		return nil, s.rpcError(jsonRPCInternalErrorCode, "prompt render failed", err.Error())
	}
	return result, nil
}

func (s *MCPServer) handleInitialize(params json.RawMessage) (*InitializeResult, *JSONRPCError) {
	var req InitializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
		}
	}
	caps := ServerCapabilities{
		Tools: &ToolsCapability{
			ListChanged: true,
		},
		Resources: &ResourcesCapability{
			ListChanged: true,
		},
	}
	if s.prompts != nil {
		caps.Prompts = &PromptsCapability{ListChanged: true}
	}
	return &InitializeResult{
		ProtocolVersion: s.cfg.ProtocolVersion,
		Capabilities:    caps,
		ServerInfo: Implementation{
			Name:    s.cfg.Name,
			Version: s.cfg.Version,
		},
	}, nil
}

func (s *MCPServer) handlePing() (*PingResult, *JSONRPCError) {
	return &PingResult{}, nil
}

func (s *MCPServer) handleToolsList(ctx context.Context) (*ToolListResult, *JSONRPCError) {
	if s.tools == nil {
		return &ToolListResult{Tools: []Tool{}}, nil
	}
	tools := s.tools.ListTools(ctx)
	if tools == nil {
		tools = []Tool{}
	}
	return &ToolListResult{Tools: tools}, nil
}

func (s *MCPServer) handleToolsCall(ctx context.Context, params json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if s.tools == nil {
		return nil, s.rpcError(jsonRPCInternalErrorCode, "tool service unavailable", nil)
	}
	var req ToolCallParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", "name is required")
	}
	// Bracket every terminal tools/call with the invocation auditor so
	// success + handler-error + approval-required + scope-deny all
	// land on the Merkle audit chain. Start happens BEFORE the call so
	// latency_ms measures everything downstream including the filter
	// + approval gate.
	var handle *InvocationHandle
	if s.auditor != nil {
		agentID, tenantID := identityForAudit(ctx)
		ctx, handle = s.auditor.StartInbound(ctx, agentID, tenantID, req.Name, req.Arguments)
	}
	result, err := s.tools.Call(ctx, req.Name, req.Arguments)
	if s.auditor != nil {
		s.auditor.FinishInbound(handle, result, err)
	}
	if err != nil {
		return nil, s.mapHandlerError(err)
	}
	return result, nil
}

// identityForAudit returns the (agentID, tenantID) pair the auditor
// stamps on the invocation event. Missing identity becomes empty
// strings; the auditor translates empty agentID to "unknown" and
// flags Extra.identity_missing="true".
func identityForAudit(ctx context.Context) (agentID, tenantID string) {
	if id := IdentityFromContext(ctx); id != nil {
		agentID = id.ID
	}
	tenantID = TenantFromContext(ctx)
	return agentID, tenantID
}

func (s *MCPServer) handleResourcesList() (*ResourceListResult, *JSONRPCError) {
	if s.resources == nil {
		return &ResourceListResult{Resources: []Resource{}}, nil
	}
	return &ResourceListResult{Resources: s.resources.List()}, nil
}

func (s *MCPServer) handleResourceTemplatesList() (*ResourceTemplatesResult, *JSONRPCError) {
	if s.resources == nil {
		return &ResourceTemplatesResult{ResourceTemplates: []ResourceTemplate{}}, nil
	}
	return &ResourceTemplatesResult{ResourceTemplates: s.resources.ListTemplates()}, nil
}

func (s *MCPServer) handleResourcesRead(ctx context.Context, params json.RawMessage) (*ResourceReadResult, *JSONRPCError) {
	if s.resources == nil {
		return nil, s.rpcError(jsonRPCInternalErrorCode, "resource service unavailable", nil)
	}
	var req ResourceReadParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
	}
	req.URI = strings.TrimSpace(req.URI)
	if req.URI == "" {
		return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", "uri is required")
	}
	content, err := s.resources.Read(ctx, req.URI)
	if err != nil {
		return nil, s.mapHandlerError(err)
	}
	if content == nil {
		return &ResourceReadResult{Contents: []ResourceContents{}}, nil
	}
	return &ResourceReadResult{Contents: []ResourceContents{*content}}, nil
}

func (s *MCPServer) mapHandlerError(err error) *JSONRPCError {
	if err == nil {
		return nil
	}
	// ApprovalRequired is checked FIRST so a gated tool call produces a
	// structured -32099 even if the wrapping err chain also matches one
	// of the generic buckets below.
	var gated *ApprovalRequired
	if errors.As(err, &gated) {
		return s.rpcError(jsonRPCApprovalRequiredCode, "approval required", gated)
	}
	// NotAuthorized is the scope-filter denial — carry the structured
	// sub_reason so clients can render a specific remediation.
	var denied *NotAuthorized
	if errors.As(err, &denied) {
		return s.rpcError(jsonRPCNotAuthorizedCode, "not authorized", denied)
	}
	// Gateway-misconfiguration signal — surfaces when middleware fails
	// to propagate request-scoped metadata into the approval gate.
	// Checked before the generic internal-error bucket so operators
	// page on a specific code instead of chasing "internal error".
	if errors.Is(err, ErrApprovalGateMisconfigured) {
		return s.rpcError(jsonRPCGatewayMisconfiguredCode, "gateway misconfigured", err.Error())
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return s.rpcError(jsonRPCInternalErrorCode, "request timeout", nil)
	case errors.Is(err, context.Canceled):
		return s.rpcError(jsonRPCInternalErrorCode, "request canceled", nil)
	case errors.Is(err, ErrInvalidParams):
		return s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
	case errors.Is(err, ErrMethodNotFound):
		return s.rpcError(jsonRPCMethodNotFoundCode, "method not found", err.Error())
	case errors.Is(err, ErrToolNotFound), errors.Is(err, ErrToolDisabled):
		return s.rpcError(jsonRPCMethodNotFoundCode, "method not found", err.Error())
	case errors.Is(err, ErrResourceNotFound), errors.Is(err, ErrResourceDisabled):
		return s.rpcError(jsonRPCMethodNotFoundCode, "method not found", err.Error())
	default:
		return s.rpcError(jsonRPCInternalErrorCode, "internal error", err.Error())
	}
}

func (s *MCPServer) rpcError(code int, message string, data any) *JSONRPCError {
	return &JSONRPCError{
		Code:    code,
		Message: message,
		Data:    data,
	}
}

// ReloadConfig applies an updated config snapshot to tool/resource registries.
func (s *MCPServer) ReloadConfig(cfg map[string]any) {
	if s == nil {
		return
	}
	if tools, ok := s.tools.(interface{ SetConfig(map[string]any) }); ok {
		tools.SetConfig(cfg)
	}
	if resources, ok := s.resources.(interface{ SetConfig(map[string]any) }); ok {
		resources.SetConfig(cfg)
	}
}
