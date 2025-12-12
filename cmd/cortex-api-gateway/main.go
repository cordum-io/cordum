package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	infraMetrics "github.com/yaront1111/coretex-os/core/infra/metrics"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
)

const (
	defaultGrpcAddr = ":8080"
	defaultHttpAddr = ":8081"
)

type server struct {
	pb.UnimplementedCoretexApiServer
	memStore memory.Store
	jobStore *memory.RedisJobStore // Typed for ListRecentJobs
	bus      scheduler.Bus
	workers  map[string]*pb.Heartbeat
	workerMu sync.RWMutex

	clients   map[*websocket.Conn]chan *pb.BusPacket
	clientsMu sync.RWMutex
	eventsCh  chan *pb.BusPacket

	metrics infraMetrics.GatewayMetrics
	tenant  string
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	cfg := config.Load()

	gwMetrics := infraMetrics.NewGatewayProm("coretex_api_gateway")

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		logging.Error("api-gateway", "failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer memStore.Close()

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		logging.Error("api-gateway", "failed to connect to redis for job store", "error", err)
		os.Exit(1)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		logging.Error("api-gateway", "failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer natsBus.Close()

	s := &server{
		memStore: memStore,
		jobStore: jobStore,
		bus:      natsBus,
		workers:  make(map[string]*pb.Heartbeat),
		clients:  make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh: make(chan *pb.BusPacket, 512),
		metrics:  gwMetrics,
		tenant:   os.Getenv("TENANT_ID"),
	}
	if s.tenant == "" {
		s.tenant = "default"
	}

	s.startBusTaps()

	// Start gRPC
	go func() {
		lis, err := net.Listen("tcp", defaultGrpcAddr)
		if err != nil {
			logging.Error("api-gateway", "failed to listen for grpc", "error", err)
			os.Exit(1)
		}
		grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
		pb.RegisterCoretexApiServer(grpcServer, s)
		reflection.Register(grpcServer)
		logging.Info("api-gateway", "grpc listening", "addr", defaultGrpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			logging.Error("api-gateway", "grpc server error", "error", err)
		}
	}()

	// Start HTTP API + WS
	startHTTPServer(s)
}

// startBusTaps subscribes to heartbeats and system events once for the lifetime of the gateway.
func (s *server) startBusTaps() {
	// Heartbeats -> worker registry snapshot
	_ = s.bus.Subscribe("sys.heartbeat", "", func(p *pb.BusPacket) {
		if hb := p.GetHeartbeat(); hb != nil {
			s.workerMu.Lock()
			s.workers[hb.WorkerId] = hb
			s.workerMu.Unlock()
		}
	})

	// Event taps -> broadcast channel
	for _, subj := range []string{"sys.job.>", "sys.audit.>"} {
		subject := subj
		_ = s.bus.Subscribe(subject, "", func(p *pb.BusPacket) {
			select {
			case s.eventsCh <- p:
			default:
			}
		})
	}

	// Broadcast loop to WS clients
	go func() {
		for evt := range s.eventsCh {
			s.clientsMu.RLock()
			for conn, ch := range s.clients {
				select {
				case ch <- evt:
				default:
					// drop slow client
					conn.Close()
				}
			}
			s.clientsMu.RUnlock()
		}
	}()
}

func startHTTPServer(s *server) {
	mux := http.NewServeMux()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", infraMetrics.Handler())
	go func() {
		srv := &http.Server{
			Addr:         ":9092",
			Handler:      metricsMux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		logging.Info("api-gateway", "metrics listening", "addr", ":9092/metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("api-gateway", "metrics server error", "error", err)
		}
	}()

	// 1. Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// 2. Workers (RPC via NATS)
	mux.HandleFunc("GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))

	// 3. Jobs (Redis ZSet)
	mux.HandleFunc("GET /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleListJobs))

	// 4. Job Details
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.instrumented("/api/v1/jobs/{id}", s.handleGetJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.instrumented("/api/v1/jobs/{id}/cancel", s.handleCancelJob))

	// 5. Submit Job (REST)
	mux.HandleFunc("POST /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleSubmitJobHTTP))
	mux.HandleFunc("POST /api/v1/repo-review", s.instrumented("/api/v1/repo-review", s.handleSubmitRepoReview))

	// 6. Trace Details
	mux.HandleFunc("GET /api/v1/traces/{id}", s.instrumented("/api/v1/traces/{id}", s.handleGetTrace))

	// 7. Stream (WebSocket)
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))

	// CORS Middleware
	handler := corsMiddleware(apiKeyMiddleware(mux))

	logging.Info("api-gateway", "http listening", "addr", defaultHttpAddr)
	if err := http.ListenAndServe(defaultHttpAddr, handler); err != nil {
		logging.Error("api-gateway", "http server error", "error", err)
	}
}

// --- Handlers ---

func (s *server) handleGetWorkers(w http.ResponseWriter, r *http.Request) {
	s.workerMu.RLock()
	out := make([]*pb.Heartbeat, 0, len(s.workers))
	for _, hb := range s.workers {
		out = append(out, hb)
	}
	s.workerMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobStore.ListRecentJobs(r.Context(), 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(jobs)
}

func (s *server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	state, err := s.jobStore.GetState(r.Context(), id)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	safetyDecision, safetyReason, _ := s.jobStore.GetSafetyDecision(r.Context(), id)
	topic, _ := s.jobStore.GetTopic(r.Context(), id)
	tenant, _ := s.jobStore.GetTenant(r.Context(), id)

	resPtr, _ := s.jobStore.GetResultPtr(r.Context(), id)

	var resultData any
	if resPtr != "" {
		// Attempt to fetch result payload
		if key, err := memory.KeyFromPointer(resPtr); err == nil {
			if bytes, err := s.memStore.GetResult(r.Context(), key); err == nil {
				_ = json.Unmarshal(bytes, &resultData)
			}
		}
	}

	resp := map[string]any{
		"id":              id,
		"state":           state,
		"result_ptr":      resPtr,
		"result":          resultData,
		"topic":           topic,
		"tenant":          tenant,
		"safety_decision": safetyDecision,
		"safety_reason":   safetyReason,
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	state, err := s.jobStore.GetState(r.Context(), id)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	switch state {
	case scheduler.JobStateSucceeded, scheduler.JobStateFailed, scheduler.JobStateCancelled, scheduler.JobStateTimeout, scheduler.JobStateDenied:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     id,
			"state":  state,
			"reason": "job already terminal",
		})
		return
	}

	if err := s.jobStore.SetState(r.Context(), id, scheduler.JobStateCancelled); err != nil {
		// Make cancel idempotent and non-fatal: return current state with reason instead of 409.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     id,
			"state":  state,
			"reason": fmt.Sprintf("failed to cancel: %v", err),
		})
		return
	}

	// Broadcast a synthetic cancellation event for dashboards and listeners.
	cancelPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:  id,
				Status: pb.JobStatus_JOB_STATUS_CANCELLED,
			},
		},
	}
	select {
	case s.eventsCh <- cancelPacket:
	default:
	}
	// Best-effort publish so scheduler/system listeners can observe the cancel.
	_ = s.bus.Publish("sys.job.result", cancelPacket)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":    id,
		"state": scheduler.JobStateCancelled,
	})
}

func (s *server) handleSubmitJobHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt             string            `json:"prompt"`
		Topic              string            `json:"topic"`
		AdapterId          string            `json:"adapter_id"`
		Priority           string            `json:"priority"`
		Context            any               `json:"context"`
		MemoryId           string            `json:"memory_id"`
		Mode               string            `json:"context_mode"`
		TenantId           string            `json:"tenant_id"`
		PrincipalId        string            `json:"principal_id"`
		Labels             map[string]string `json:"labels"`
		MaxInputTokens     int32             `json:"max_input_tokens"`
		AllowSummarization bool              `json:"allow_summarization"`
		AllowRetrieval     bool              `json:"allow_retrieval"`
		Tags               []string          `json:"tags"`
		MaxOutputTokens    int64             `json:"max_output_tokens"`
		MaxTotalTokens     int64             `json:"max_total_tokens"`
		DeadlineMs         int64             `json:"deadline_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.MaxInputTokens == 0 {
		req.MaxInputTokens = 8000
	}
	if req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = 1024
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.Priority)

	// Default topic if missing
	if req.Topic == "" {
		req.Topic = "job.chat.simple"
	}

	memoryID := req.MemoryId
	if memoryID == "" {
		if h := r.Header.Get("X-Session-Id"); h != "" {
			memoryID = h
		} else {
			memoryID = "session:" + jobID
		}
	}

	tenantID := req.TenantId
	if tenantID == "" {
		tenantID = s.tenant
	}

	envVars := map[string]string{
		"tenant_id": tenantID,
	}
	if memoryID != "" {
		envVars["memory_id"] = memoryID
	}
	if req.Mode != "" {
		envVars["context_mode"] = req.Mode
	}
	envVars["max_input_tokens"] = fmt.Sprintf("%d", req.MaxInputTokens)
	envVars["max_output_tokens"] = fmt.Sprintf("%d", req.MaxOutputTokens)

	payload := map[string]any{
		"prompt":     req.Prompt,
		"adapter_id": req.AdapterId,
		"priority":   req.Priority,
		"topic":      req.Topic,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  s.tenant,
	}
	if req.Context != nil {
		payload["context"] = req.Context
	}
	payloadBytes, _ := json.Marshal(payload)
	_ = s.memStore.PutContext(r.Context(), ctxKey, payloadBytes)

	// Set initial state
	_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStatePending)
	_ = s.jobStore.SetTopic(r.Context(), jobID, req.Topic)
	_ = s.jobStore.SetTenant(r.Context(), jobID, tenantID)

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       req.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   req.AdapterId,
		Env:         envVars,
		MemoryId:    memoryID,
		TenantId:    tenantID,
		PrincipalId: req.PrincipalId,
		Labels:      req.Labels,
		ContextHints: &pb.ContextHints{
			MaxInputTokens:     req.MaxInputTokens,
			AllowSummarization: req.AllowSummarization,
			AllowRetrieval:     req.AllowRetrieval,
			Tags:               req.Tags,
		},
		Budget: &pb.Budget{
			MaxInputTokens:  int64(req.MaxInputTokens),
			MaxOutputTokens: req.MaxOutputTokens,
			MaxTotalTokens:  req.MaxTotalTokens,
			DeadlineMs:      req.DeadlineMs,
		},
	}

	if s.jobStore != nil {
		_ = s.jobStore.SetJobMeta(r.Context(), jobReq)
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway-http",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish("sys.job.submit", packet); err != nil {
		logging.Error("api-gateway", "job publish failed", "job_id", jobID, "error", err)
		_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStateFailed)
		http.Error(w, "failed to enqueue job", http.StatusServiceUnavailable)
		return
	}

	logging.Info("api-gateway", "job submitted http", "job_id", jobID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"job_id":   jobID,
		"trace_id": traceID,
	})
}

func (s *server) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing trace id", http.StatusBadRequest)
		return
	}

	jobs, err := s.jobStore.GetTraceJobs(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Enrich with details if needed, but for now list is enough
	json.NewEncoder(w).Encode(jobs)
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Honor API key on WS as well
	required := os.Getenv("API_KEY")
	if required != "" {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		if key != required {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	logging.Info("gateway", "ws connection attempt", "remote", r.RemoteAddr)
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Error("gateway", "ws upgrade failed", "error", err)
		return
	}
	defer ws.Close()
	logging.Info("gateway", "ws connected", "remote", r.RemoteAddr)

	clientCh := make(chan *pb.BusPacket, 100)
	s.clientsMu.Lock()
	s.clients[ws] = clientCh
	s.clientsMu.Unlock()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ws)
		s.clientsMu.Unlock()
		close(clientCh)
	}()

	for {
		select {
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			// Use protojson to correctly handle oneof fields and proto semantics
			data, err := protojson.Marshal(msg)
			if err != nil {
				logging.Error("gateway", "protojson marshal failed", "error", err)
				continue
			}
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == "OPTIONS" {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Submit a repo review workflow job with explicit repo context.
func (s *server) handleSubmitRepoReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoURL      string   `json:"repo_url"`
		Branch       string   `json:"branch"`
		LocalPath    string   `json:"local_path"`
		IncludeGlobs []string `json:"include_globs"`
		ExcludeGlobs []string `json:"exclude_globs"`
		MaxFiles     int      `json:"max_files"`
		BatchSize    int      `json:"batch_size"`
		MaxBatches   int      `json:"max_batches"`
		RunTests     bool     `json:"run_tests"`
		TestCommand  string   `json:"test_command"`
		Priority     string   `json:"priority"`
		MemoryId     string   `json:"memory_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.RepoURL == "" && req.LocalPath == "" {
		http.Error(w, "repo_url or local_path required", http.StatusBadRequest)
		return
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.Priority)

	if len(req.IncludeGlobs) == 0 {
		req.IncludeGlobs = []string{
			"**/*.go", "**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.py",
			"**/*.rs", "**/*.java", "**/*.cs", "**/*.rb", "**/*.php", "**/*.c", "**/*.h", "**/*.cpp", "**/*.cxx", "**/*.hpp", "**/*.hxx",
			"**/*.sh", "**/*.bash", "**/*.zsh", "**/*.ps1",
			"**/*.json", "**/*.yaml", "**/*.yml", "**/*.toml", "**/*.ini", "**/*.cfg", "**/*.conf",
			"**/*.md", "**/*.txt", "**/*.sql",
		}
	}
	if len(req.ExcludeGlobs) == 0 {
		req.ExcludeGlobs = []string{
			"vendor/**", "node_modules/**", "dist/**", ".git/**", "build/**", "bin/**", "obj/**", "target/**", ".venv/**", "venv/**",
		}
	}
	if req.TestCommand == "" {
		req.TestCommand = "go test ./..."
	}

	envVars := map[string]string{
		"tenant_id":         s.tenant,
		"memory_id":         "",
		"context_mode":      "rag",
		"max_input_tokens":  "12000",
		"max_output_tokens": "2048",
	}

	payload := map[string]any{
		"repo_url":      req.RepoURL,
		"branch":        req.Branch,
		"local_path":    req.LocalPath,
		"include_globs": req.IncludeGlobs,
		"exclude_globs": req.ExcludeGlobs,
		"max_files":     req.MaxFiles,
		"batch_size":    req.BatchSize,
		"max_batches":   req.MaxBatches,
		"run_tests":     req.RunTests,
		"test_command":  req.TestCommand,
		"created_at":    time.Now().UTC().Format(time.RFC3339),
		"tenant_id":     s.tenant,
	}
	payloadBytes, _ := json.Marshal(payload)
	_ = s.memStore.PutContext(r.Context(), ctxKey, payloadBytes)

	_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStatePending)

	memoryID := req.MemoryId
	if memoryID == "" {
		if req.RepoURL != "" {
			memoryID = "repo:" + req.RepoURL
			if req.Branch != "" {
				memoryID += "#" + req.Branch
			}
		} else {
			memoryID = "repo:" + jobID
		}
	}
	envVars["memory_id"] = memoryID

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.workflow.repo.code_review",
		Priority:   jobPriority,
		ContextPtr: ctxPtr,
		Env:        envVars,
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway-repo-review",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish("sys.job.submit", packet); err != nil {
		logging.Error("api-gateway", "repo review publish failed", "job_id", jobID, "error", err)
		_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStateFailed)
		http.Error(w, "failed to enqueue job", http.StatusServiceUnavailable)
		return
	}

	logging.Info("api-gateway", "repo review job submitted", "job_id", jobID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"job_id":   jobID,
		"trace_id": traceID,
	})
}

// apiKeyMiddleware enforces a static API key if API_KEY is set.
func apiKeyMiddleware(next http.Handler) http.Handler {
	required := os.Getenv("API_KEY")
	if required == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		if key != required {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parsePriority(priority string) pb.JobPriority {
	switch strings.ToLower(priority) {
	case "batch":
		return pb.JobPriority_JOB_PRIORITY_BATCH
	case "critical":
		return pb.JobPriority_JOB_PRIORITY_CRITICAL
	case "interactive":
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	default:
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	}
}

func parseContextMode(topic, explicit string) string {
	switch strings.ToLower(explicit) {
	case "chat":
		return "chat"
	case "rag":
		return "rag"
	case "raw":
		return "raw"
	}
	if strings.HasPrefix(topic, "job.chat") {
		return "chat"
	}
	if strings.HasPrefix(topic, "job.code") || strings.HasPrefix(topic, "job.workflow.repo") {
		return "rag"
	}
	return "raw"
}

func deriveMemoryIDFromReq(topic, explicit, jobID string) string {
	if explicit != "" {
		return explicit
	}
	if strings.HasPrefix(topic, "job.chat") {
		return "session:" + jobID
	}
	if strings.HasPrefix(topic, "job.code") || strings.HasPrefix(topic, "job.workflow.repo") {
		return "repo:" + jobID
	}
	return "mem:" + jobID
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack forwards websocket hijacking support to the underlying writer when available.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker not supported")
	}
	return hj.Hijack()
}

// Flush preserves streaming support if the wrapped writer implements it.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// instrumented wraps handlers to record metrics.
func (s *server) instrumented(route string, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fn(rec, r)
		if s.metrics != nil {
			s.metrics.ObserveRequest(r.Method, route, fmt.Sprintf("%d", rec.status), time.Since(start).Seconds())
		}
	}
}

// --- gRPC Implementations (unchanged mostly) ---

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.GetPriority())

	payload := map[string]any{
		"prompt":     req.GetPrompt(),
		"adapter_id": req.GetAdapterId(),
		"priority":   req.GetPriority(),
		"topic":      req.GetTopic(),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  s.tenant,
	}
	payloadBytes, _ := json.Marshal(payload)
	_ = s.memStore.PutContext(ctx, ctxKey, payloadBytes)

	// Set initial state
	_ = s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending)

	maxInput := int32(8000)
	maxOutput := int64(1024)
	envVars := map[string]string{
		"tenant_id":         s.tenant,
		"memory_id":         deriveMemoryIDFromReq(req.GetTopic(), "", jobID),
		"context_mode":      "",
		"max_input_tokens":  fmt.Sprintf("%d", maxInput),
		"max_output_tokens": fmt.Sprintf("%d", maxOutput),
	}
	if mode := parseContextMode(req.GetTopic(), ""); mode != "" {
		envVars["context_mode"] = mode
	}

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      req.GetTopic(),
		Priority:   jobPriority,
		ContextPtr: ctxPtr,
		AdapterId:  req.GetAdapterId(),
		Env:        envVars,
		MemoryId:   envVars["memory_id"],
		TenantId:   s.tenant,
		Labels:     nil,
		ContextHints: &pb.ContextHints{
			MaxInputTokens: maxInput,
		},
		Budget: &pb.Budget{
			MaxInputTokens:  int64(maxInput),
			MaxOutputTokens: maxOutput,
		},
	}

	if s.jobStore != nil {
		_ = s.jobStore.SetJobMeta(ctx, jobReq)
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish("sys.job.submit", packet); err != nil {
		_ = s.jobStore.SetState(ctx, jobID, scheduler.JobStateFailed)
		logging.Error("api-gateway", "job publish failed", "job_id", jobID, "error", err)
		return nil, status.Errorf(codes.Unavailable, "failed to enqueue job")
	}

	logging.Info("api-gateway", "job submitted", "job_id", jobID)
	return &pb.SubmitJobResponse{JobId: jobID, TraceId: traceID}, nil
}

func (s *server) GetJobStatus(ctx context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	state, err := s.jobStore.GetState(ctx, req.GetJobId())
	if err != nil {
		state = "UNKNOWN"
	}
	resPtr, _ := s.jobStore.GetResultPtr(ctx, req.GetJobId())
	return &pb.GetJobStatusResponse{
		JobId:     req.GetJobId(),
		Status:    string(state),
		ResultPtr: resPtr,
	}, nil
}
