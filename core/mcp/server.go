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
)

var (
	ErrMethodNotFound = errors.New("mcp method not found")
	ErrInvalidParams  = errors.New("mcp invalid params")
)

// ToolService provides tool listing and execution for MCP server handlers.
type ToolService interface {
	List() []Tool
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
	cfg       ServerConfig
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
		// Responses from clients are ignored by this server-side dispatcher.
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
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTimeout)
	defer cancel()

	result, rpcErr := s.dispatch(ctx, msg)
	if !messageHasID(msg.ID) {
		// JSON-RPC notifications must not receive a response.
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
		return s.handleToolsList()
	case MethodToolsCall:
		return s.handleToolsCall(ctx, msg.Params)
	case MethodResourcesList:
		return s.handleResourcesList()
	case MethodResourceTemplates:
		return s.handleResourceTemplatesList()
	case MethodResourcesRead:
		return s.handleResourcesRead(ctx, msg.Params)
	default:
		return nil, s.rpcError(jsonRPCMethodNotFoundCode, "method not found", msg.Method)
	}
}

func (s *MCPServer) handleInitialize(params json.RawMessage) (*InitializeResult, *JSONRPCError) {
	var req InitializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, s.rpcError(jsonRPCInvalidParamsCode, "invalid params", err.Error())
		}
	}
	return &InitializeResult{
		ProtocolVersion: s.cfg.ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{
				ListChanged: true,
			},
			Resources: &ResourcesCapability{
				ListChanged: true,
			},
		},
		ServerInfo: Implementation{
			Name:    s.cfg.Name,
			Version: s.cfg.Version,
		},
	}, nil
}

func (s *MCPServer) handlePing() (*PingResult, *JSONRPCError) {
	return &PingResult{}, nil
}

func (s *MCPServer) handleToolsList() (*ToolListResult, *JSONRPCError) {
	if s.tools == nil {
		return &ToolListResult{Tools: []Tool{}}, nil
	}
	return &ToolListResult{Tools: s.tools.List()}, nil
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
	result, err := s.tools.Call(ctx, req.Name, req.Arguments)
	if err != nil {
		return nil, s.mapHandlerError(err)
	}
	return result, nil
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

// ReloadConfig applies an updated config snapshot to tool/resource registries
// that support dynamic config updates.
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
