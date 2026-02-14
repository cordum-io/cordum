package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/mcp"
	mcpresources "github.com/cordum/cordum/core/mcp/resources"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
)

type mcpGatewayConfig struct {
	Enabled   bool
	Transport string
	Port      int
	Raw       map[string]any
}

type mcpRuntimeState struct {
	startedAt        time.Time
	transport        string
	httpTransport    *mcp.HTTPTransport
	toolRegistry     *mcp.ToolRegistry
	resourceRegistry *mcp.ResourceRegistry
	server           *mcp.MCPServer
}

var gatewayMCPState sync.Map // map[*server]*mcpRuntimeState

func (s *server) registerMCPRoutes(mux *http.ServeMux) error {
	if s == nil || mux == nil {
		return nil
	}

	// Always expose MCP routes so clients get explicit disabled/unavailable responses
	// instead of startup-time 404s when MCP config loads after route registration.
	mux.HandleFunc("GET /mcp/sse", s.instrumented("/mcp/sse", s.mcpAuth(s.handleMCPSSE)))
	mux.HandleFunc("POST /mcp/message", s.instrumented("/mcp/message", s.mcpAuth(s.handleMCPMessage)))
	mux.HandleFunc("GET /mcp/status", s.instrumented("/mcp/status", s.mcpAuth(s.handleMCPStatus)))
	mux.HandleFunc("GET /api/v1/mcp/sse", s.instrumented("/api/v1/mcp/sse", s.mcpAuth(s.handleMCPSSE)))
	mux.HandleFunc("POST /api/v1/mcp/message", s.instrumented("/api/v1/mcp/message", s.mcpAuth(s.handleMCPMessage)))
	mux.HandleFunc("GET /api/v1/mcp/status", s.instrumented("/api/v1/mcp/status", s.mcpAuth(s.handleMCPStatus)))

	cfg := s.loadMCPConfig(context.Background())
	if !cfg.Enabled {
		logging.Info("api-gateway", "mcp runtime disabled by config")
		return nil
	}
	if cfg.Transport != "http" {
		logging.Info("api-gateway", "mcp http runtime disabled", "transport", cfg.Transport)
		return nil
	}

	transport := mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, mcp.DefaultHTTPResponseTimeout)
	toolRegistry := mcp.NewToolRegistry()
	resourceRegistry := mcp.NewResourceRegistry()
	toolRegistry.SetConfig(cfg.Raw)
	resourceRegistry.SetConfig(cfg.Raw)

	if err := mcptools.RegisterWithBridge(toolRegistry, s.newMCPServiceBridge()); err != nil {
		return fmt.Errorf("register mcp tools: %w", err)
	}
	if err := mcpresources.RegisterWithBridge(resourceRegistry, s.newMCPDataBridge()); err != nil {
		return fmt.Errorf("register mcp resources: %w", err)
	}

	mcpServer := mcp.NewServer(transport, toolRegistry, resourceRegistry, mcp.ServerConfig{
		Name:            "cordum",
		Version:         buildinfo.Version,
		ProtocolVersion: mcp.DefaultProtocolVersion,
		RequestTimeout:  30 * time.Second,
	})
	s.setMCPRuntime(&mcpRuntimeState{
		startedAt:        time.Now().UTC(),
		transport:        cfg.Transport,
		httpTransport:    transport,
		toolRegistry:     toolRegistry,
		resourceRegistry: resourceRegistry,
		server:           mcpServer,
	})
	go func() {
		if err := mcpServer.Serve(); err != nil {
			logging.Error("api-gateway", "mcp server loop failed", "error", err)
		}
	}()
	if s.shutdownCh != nil {
		go func() {
			<-s.shutdownCh
			if err := transport.Close(); err != nil {
				logging.Warn("api-gateway", "mcp transport close failed", "error", err)
			}
			s.clearMCPRuntime()
		}()
	}

	logging.Info(
		"api-gateway",
		"mcp routes enabled",
		"transport", cfg.Transport,
		"port", cfg.Port,
	)
	return nil
}

func (s *server) mcpAuth(next http.HandlerFunc) http.HandlerFunc {
	if next == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.auth == nil {
			next(w, r)
			return
		}
		authCtx, err := s.auth.AuthenticateHTTP(r)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authCtx)
		r = r.WithContext(ctx)

		tenantID := tenantFromRequest(r)
		if tenantID == "" {
			writeErrorJSON(w, http.StatusForbidden, "tenant id required")
			return
		}
		if authCtx.Tenant != "" && !authCtx.AllowCrossTenant {
			if strings.TrimSpace(authCtx.Tenant) != tenantID {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
		next(w, r)
	}
}

func (s *server) loadMCPConfig(ctx context.Context) mcpGatewayConfig {
	cfg := mcpGatewayConfig{
		Enabled:   false,
		Transport: "http",
		Port:      0,
		Raw:       nil,
	}
	if s == nil || s.configSvc == nil {
		return cfg
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	effective, err := s.configSvc.Effective(cctx, "", "", "", "")
	if err != nil || effective == nil {
		return cfg
	}
	cfg.Raw = effective
	if enabled, ok := lookupBoolPath(effective, "mcp", "enabled"); ok {
		cfg.Enabled = enabled
	}
	if transport, ok := lookupStringPath(effective, "mcp", "transport"); ok && transport != "" {
		cfg.Transport = transport
	}
	if port := lookupIntPath(effective, "mcp", "port"); port > 0 {
		cfg.Port = port
	}
	return cfg
}

func (s *server) mcpHTTPTransport() *mcp.HTTPTransport {
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.transport != "http" || runtime.httpTransport == nil || runtime.httpTransport.IsClosed() {
		return nil
	}
	return runtime.httpTransport
}

func (s *server) handleMCPSSE(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "mcp http transport unavailable")
		return
	}
	transport.HandleSSE(w, r)
}

func (s *server) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "mcp http transport unavailable")
		return
	}
	transport.HandleMessage(w, r)
}

func (s *server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadMCPConfig(r.Context())
	resp := map[string]any{
		"running":           false,
		"connected_clients": 0,
		"uptime_seconds":    int64(0),
		"transport":         cfg.Transport,
		"enabled_tools":     0,
		"enabled_resources": 0,
	}
	if runtime := s.getMCPRuntime(); runtime != nil {
		running := runtime.server != nil
		if runtime.httpTransport != nil {
			running = running && !runtime.httpTransport.IsClosed()
			resp["connected_clients"] = runtime.httpTransport.ActiveSessionCount()
		}
		if !runtime.startedAt.IsZero() && running {
			resp["uptime_seconds"] = int64(time.Since(runtime.startedAt).Seconds())
		}
		if runtime.transport != "" {
			resp["transport"] = runtime.transport
		}
		if runtime.toolRegistry != nil {
			resp["enabled_tools"] = len(runtime.toolRegistry.List())
		}
		if runtime.resourceRegistry != nil {
			resp["enabled_resources"] = len(runtime.resourceRegistry.List()) + len(runtime.resourceRegistry.ListTemplates())
		}
		resp["running"] = running
	}
	writeJSON(w, resp)
}

func (s *server) setMCPRuntime(state *mcpRuntimeState) {
	if s == nil {
		return
	}
	if state == nil {
		gatewayMCPState.Delete(s)
		return
	}
	gatewayMCPState.Store(s, state)
}

func (s *server) getMCPRuntime() *mcpRuntimeState {
	if s == nil {
		return nil
	}
	raw, ok := gatewayMCPState.Load(s)
	if !ok {
		return nil
	}
	state, _ := raw.(*mcpRuntimeState)
	return state
}

func (s *server) clearMCPRuntime() {
	if s == nil {
		return
	}
	gatewayMCPState.Delete(s)
}

func mcpConfigTouched(data map[string]any) bool {
	if len(data) == 0 {
		return false
	}
	if _, ok := data["mcp"]; ok {
		return true
	}
	for key := range data {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "mcp.") {
			return true
		}
	}
	return false
}

func (s *server) reloadMCPConfig(ctx context.Context) {
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		return
	}
	cfg := s.loadMCPConfig(ctx)
	if runtime.toolRegistry != nil {
		runtime.toolRegistry.SetConfig(cfg.Raw)
	}
	if runtime.resourceRegistry != nil {
		runtime.resourceRegistry.SetConfig(cfg.Raw)
	}
	runtime.server.ReloadConfig(cfg.Raw)
}

func lookupBoolPath(data map[string]any, keys ...string) (bool, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	}
	return false, false
}

func lookupStringPath(data map[string]any, keys ...string) (string, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), true
	case []byte:
		return strings.TrimSpace(string(v)), true
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", raw)), true
	}
}

func lookupAnyPath(data map[string]any, keys ...string) (any, bool) {
	if data == nil || len(keys) == 0 {
		return nil, false
	}
	var cur any = data
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// ---------------------------------------------------------------------------
// MCP service bridge — closure-based bridge from MCP tools to HTTP handlers
// ---------------------------------------------------------------------------

func (s *server) newMCPServiceBridge() mcp.ServiceBridge {
	if s == nil {
		return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{})
	}
	return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{
		SubmitJobFunc: func(ctx context.Context, req mcp.SubmitJobInput) (*mcp.SubmitJobOutput, error) {
			body := map[string]any{
				"prompt":   req.Prompt,
				"topic":    req.Topic,
				"priority": req.Priority,
			}
			if strings.TrimSpace(req.Capability) != "" {
				body["capability"] = strings.TrimSpace(req.Capability)
			}
			if len(req.RiskTags) > 0 {
				body["risk_tags"] = append([]string{}, req.RiskTags...)
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			if strings.TrimSpace(req.MemoryID) != "" {
				body["memory_id"] = strings.TrimSpace(req.MemoryID)
			}
			if strings.TrimSpace(req.PackID) != "" {
				body["pack_id"] = strings.TrimSpace(req.PackID)
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/jobs", nil, nil, body, s.handleSubmitJobHTTP)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			jobID := strings.TrimSpace(mcpBridgeString(payload["job_id"]))
			if jobID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing job_id in submit response", payload)
			}
			return &mcp.SubmitJobOutput{
				JobID:   jobID,
				TraceID: strings.TrimSpace(mcpBridgeString(payload["trace_id"])),
			}, nil
		},
		CancelJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{}
			if strings.TrimSpace(reason) != "" {
				body["reason"] = strings.TrimSpace(reason)
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/jobs/"+jobID+"/cancel",
				nil,
				map[string]string{"id": jobID},
				body,
				s.handleCancelJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if state := strings.TrimSpace(mcpBridgeString(payload["state"])); state != "" && !strings.EqualFold(state, "cancelled") {
				return mcp.NewBridgeError(http.StatusConflict, "job_already_completed", "job already completed", payload)
			}
			return nil
		},
		TriggerWorkflowFunc: func(ctx context.Context, req mcp.TriggerWorkflowInput) (*mcp.TriggerOutput, error) {
			target := "/api/v1/workflows/" + req.WorkflowID + "/runs"
			if req.DryRun {
				target += "?dry_run=true"
			}
			headers := map[string]string{}
			if strings.TrimSpace(req.IdempotencyKey) != "" {
				headers["Idempotency-Key"] = strings.TrimSpace(req.IdempotencyKey)
			}
			input := req.Input
			if input == nil {
				input = map[string]any{}
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				target,
				headers,
				map[string]string{"id": req.WorkflowID},
				input,
				s.handleStartRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				be := mcp.NewBridgeErrorFromHTTP(status, raw)
				if status == http.StatusBadRequest && strings.Contains(strings.ToLower(be.Message), "input schema validation failed") {
					return nil, mcp.NewBridgeError(http.StatusUnprocessableEntity, "input_validation_failed", be.Message, be.Details)
				}
				return nil, be
			}
			runID := strings.TrimSpace(mcpBridgeString(payload["run_id"]))
			if runID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing run_id in workflow response", payload)
			}
			return &mcp.TriggerOutput{
				RunID:      runID,
				WorkflowID: strings.TrimSpace(req.WorkflowID),
			}, nil
		},
		ApproveJobFunc: func(ctx context.Context, jobID string, note string) error {
			body := map[string]any{}
			if strings.TrimSpace(note) != "" {
				body["note"] = strings.TrimSpace(note)
			}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/approve",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleApproveJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		RejectJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{"reason": strings.TrimSpace(reason)}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/reject",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleRejectJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		SimulatePolicyFunc: func(ctx context.Context, req mcp.PolicySimInput) (*mcp.PolicySimOutput, error) {
			tenantID := s.mcpTenantFromContext(ctx)
			body := map[string]any{
				"topic":    req.Topic,
				"tenant":   tenantID,
				"org_id":   tenantID,
				"priority": req.Priority,
				"meta": map[string]any{
					"tenant_id":  tenantID,
					"capability": strings.TrimSpace(req.Capability),
					"risk_tags":  append([]string{}, req.RiskTags...),
					"labels":     req.Labels,
				},
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/policy/simulate", nil, nil, body, s.handlePolicySimulate)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := &mcp.PolicySimOutput{
				Decision: strings.TrimSpace(mcpBridgeString(payload["decision"])),
				Reason:   strings.TrimSpace(mcpBridgeString(payload["reason"])),
				RuleID:   strings.TrimSpace(mcpBridgeFirstString(payload, "ruleId", "rule_id")),
			}
			if constraints, ok := payload["constraints"].(map[string]any); ok {
				out.Constraints = constraints
			} else {
				out.Constraints = map[string]any{}
			}
			if rems, ok := payload["remediations"].([]any); ok {
				out.Remediations = make([]map[string]any, 0, len(rems))
				for _, item := range rems {
					if m, ok := item.(map[string]any); ok {
						out.Remediations = append(out.Remediations, m)
					}
				}
			} else {
				out.Remediations = []map[string]any{}
			}
			return out, nil
		},
	})
}

func (s *server) invokeMCPJSONHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, map[string]any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", s.mcpTenantFromContext(ctx))
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	decoded := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) invokeMCPAnyHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", s.mcpTenantFromContext(ctx))
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	var decoded any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) mcpTenantFromContext(ctx context.Context) string {
	if auth := authFromContext(ctx); auth != nil {
		if tenant := strings.TrimSpace(auth.Tenant); tenant != "" {
			return tenant
		}
	}
	if tenant := strings.TrimSpace(s.tenant); tenant != "" {
		return tenant
	}
	return "default"
}

func mcpBridgeString(v any) string {
	if v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", v)
	}
}

func mcpBridgeFirstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(mcpBridgeString(data[key])); val != "" {
			return val
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// MCP data bridge — closure-based bridge from MCP resources to HTTP handlers
// ---------------------------------------------------------------------------

func (s *server) newMCPDataBridge() mcp.DataBridge {
	if s == nil {
		return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{})
	}
	return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{
		GetJobFunc: func(ctx context.Context, id string) (*mcp.JobDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/jobs/"+strings.TrimSpace(id),
				nil,
				map[string]string{"id": strings.TrimSpace(id)},
				nil,
				s.handleGetJob,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.JobDetail(payload)
			return &out, nil
		},
		ListJobsFunc: func(ctx context.Context, opts mcp.JobListOpts) (*mcp.JobList, error) {
			values := []string{}
			if opts.Limit > 0 {
				values = append(values, "limit="+strconv.Itoa(opts.Limit))
			}
			if status := strings.TrimSpace(opts.Status); status != "" {
				values = append(values, "state="+status)
			}
			if opts.Cursor > 0 {
				values = append(values, "cursor="+strconv.FormatInt(opts.Cursor, 10))
			}
			target := "/api/v1/jobs"
			if len(values) > 0 {
				target += "?" + strings.Join(values, "&")
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, target, nil, nil, nil, s.handleListJobs)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			out := &mcp.JobList{Items: items}
			if next, ok := mcpGatewayToInt64(payload["next_cursor"]); ok {
				out.NextCursor = &next
			}
			return out, nil
		},
		ListWorkflowRunsFunc: func(ctx context.Context, wfID string, limit int) (*mcp.RunList, error) {
			target := "/api/v1/workflows/" + strings.TrimSpace(wfID) + "/runs"
			if limit > 0 {
				target += "?limit=" + strconv.Itoa(limit)
			}
			status, payload, raw, err := s.invokeMCPAnyHandler(
				ctx,
				http.MethodGet,
				target,
				nil,
				map[string]string{"id": strings.TrimSpace(wfID)},
				nil,
				s.handleListRuns,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := []map[string]any{}
			switch typed := payload.(type) {
			case []any:
				items = mcpGatewayToMapSlice(typed)
			case map[string]any:
				items = mcpGatewayToMapSlice(typed["items"])
			}
			return &mcp.RunList{
				WorkflowID: strings.TrimSpace(wfID),
				Items:      items,
			}, nil
		},
		GetWorkflowRunFunc: func(ctx context.Context, wfID, runID string) (*mcp.RunDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/workflow-runs/"+strings.TrimSpace(runID),
				nil,
				map[string]string{"id": strings.TrimSpace(runID)},
				nil,
				s.handleGetRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if expected := strings.TrimSpace(wfID); expected != "" {
				if actual := strings.TrimSpace(mcpBridgeString(payload["workflow_id"])); actual != "" && actual != expected {
					return nil, mcp.NewBridgeError(http.StatusNotFound, "not_found", "workflow run not found", nil)
				}
			}
			out := mcp.RunDetail(payload)
			return &out, nil
		},
		ListAuditEntriesFunc: func(ctx context.Context, limit int) ([]mcp.AuditEntry, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/audit", nil, nil, nil, s.handleListPolicyAudit)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			if limit <= 0 {
				limit = 50
			}
			if len(items) > limit {
				items = items[:limit]
			}
			out := make([]mcp.AuditEntry, 0, len(items))
			for _, item := range items {
				out = append(out, mcp.AuditEntry(item))
			}
			return out, nil
		},
		GetSystemHealthFunc: func(ctx context.Context) (*mcp.HealthStatus, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/status", nil, nil, nil, s.handleStatus)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.HealthStatus(payload)
			return &out, nil
		},
		ListPoliciesSummaryFn: func(ctx context.Context) (*mcp.PolicySummary, error) {
			status, bundlesPayload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/bundles", nil, nil, nil, s.handlePolicyBundles)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(bundlesPayload["items"])

			currentSnapshot := ""
			if status, snapshotsPayload, _, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/snapshots", nil, nil, nil, s.handlePolicySnapshots); err == nil {
				if status >= 200 && status < 300 {
					if snapshots, ok := snapshotsPayload["snapshots"].([]any); ok && len(snapshots) > 0 {
						currentSnapshot = strings.TrimSpace(mcpBridgeString(snapshots[0]))
					}
				}
			}

			summary := mcp.PolicySummary{
				"active_bundles":      items,
				"current_snapshot_id": currentSnapshot,
				"safety_stance":       mcpGatewayInferSafetyStance(items),
			}
			return &summary, nil
		},
	})
}

func mcpGatewayToMapSlice(raw any) []map[string]any {
	if raw == nil {
		return []map[string]any{}
	}
	list, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func mcpGatewayToInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func mcpGatewayInferSafetyStance(items []map[string]any) string {
	if len(items) == 0 {
		return "permissive"
	}
	enabled := 0
	var totalRules int64
	for _, item := range items {
		if v, ok := item["enabled"].(bool); ok && v {
			enabled++
		}
		if rc, ok := mcpGatewayToInt64(item["rule_count"]); ok {
			totalRules += rc
		}
	}
	if enabled == 0 || totalRules == 0 {
		return "permissive"
	}
	if totalRules >= 20 {
		return "strict"
	}
	return "balanced"
}
