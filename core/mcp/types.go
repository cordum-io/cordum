package mcp

import (
	"context"
	"encoding/json"
)

const (
	JSONRPCVersion          = "2.0"
	DefaultProtocolVersion  = "2024-11-05"
	MethodInitialize        = "initialize"
	MethodPing              = "ping"
	MethodToolsList         = "tools/list"
	MethodToolsCall         = "tools/call"
	MethodResourcesList     = "resources/list"
	MethodResourcesRead     = "resources/read"
	MethodResourceTemplates = "resources/templates/list"
)

// JSONRPCMessage is a transport-level envelope for JSON-RPC 2.0 payloads.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`

	// Transport metadata (not serialized on wire).
	sessionID  string
	responseCh chan *JSONRPCMessage
}

// JSONRPCRequest is a standard JSON-RPC 2.0 request object.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCNotification is a JSON-RPC request without an id.
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a standard JSON-RPC 2.0 response object.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is a standard JSON-RPC error payload.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Implementation describes a client or server implementation.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams is the parameter object for initialize requests.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    map[string]any  `json:"capabilities,omitempty"`
	ClientInfo      *Implementation `json:"clientInfo,omitempty"`
}

// InitializeResult is the result object for initialize responses.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ServerCapabilities announces server-supported MCP features.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Logging   map[string]any       `json:"logging,omitempty"`
}

// ToolsCapability describes tool-related capabilities.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability describes resource-related capabilities.
type ResourcesCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// Tool is an MCP tool descriptor.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolListResult is the result payload for tools/list.
type ToolListResult struct {
	Tools []Tool `json:"tools"`
}

// ToolCallParams is the params payload for tools/call.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ContentItem is an MCP content item used in tool/resource responses.
type ContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// ToolCallResult is the result payload for tools/call.
type ToolCallResult struct {
	Content           []ContentItem `json:"content"`
	IsError           bool          `json:"isError,omitempty"`
	StructuredContent any           `json:"structuredContent,omitempty"`
}

// Resource is an MCP resource descriptor.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceTemplate describes a URI-template based MCP resource.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceContents contains bytes/text for a specific resource URI.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ResourceListResult is the result payload for resources/list.
type ResourceListResult struct {
	Resources []Resource `json:"resources"`
}

// ResourceTemplatesResult is the result payload for resources/templates/list.
type ResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
}

// ResourceReadParams is the params payload for resources/read.
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceReadResult is the result payload for resources/read.
type ResourceReadResult struct {
	Contents []ResourceContents `json:"contents"`
}

// PingResult is returned for ping requests.
type PingResult struct{}

// ToolHandler executes a tool call.
type ToolHandler func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error)

// ResourceHandler reads a resource by URI.
type ResourceHandler func(ctx context.Context, uri string) (*ResourceContents, error)
