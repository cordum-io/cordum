package main

import (
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
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/logging"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	infraMetrics "github.com/yaront1111/cortex-os/core/internal/infrastructure/metrics"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
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
	pb.UnimplementedCortexApiServer
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

	gwMetrics := infraMetrics.NewGatewayProm("cortex_api_gateway")

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
		pb.RegisterCortexApiServer(grpcServer, s)
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
	_ = s.bus.Subscribe("sys.heartbeat.>", "", func(p *pb.BusPacket) {
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
		"id":         id,
		"state":      state,
		"result_ptr": resPtr,
		"result":     resultData,
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *server) handleSubmitJobHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt    string `json:"prompt"`
		Topic     string `json:"topic"`
		AdapterId string `json:"adapter_id"`
		Priority  string `json:"priority"`
		Context   any    `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
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

	envVars := map[string]string{
		"tenant_id": s.tenant,
	}

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

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      req.Topic,
		Priority:   jobPriority,
		ContextPtr: ctxPtr,
		AdapterId:  req.AdapterId,
		EnvVars:    envVars,
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
		req.IncludeGlobs = []string{"**/*.go", "**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.py"}
	}
	if len(req.ExcludeGlobs) == 0 {
		req.ExcludeGlobs = []string{"vendor/**", "node_modules/**", "dist/**", ".git/**", "build/**"}
	}
	if req.TestCommand == "" {
		req.TestCommand = "go test ./..."
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

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.workflow.repo.code_review",
		Priority:   jobPriority,
		ContextPtr: ctxPtr,
		EnvVars:    map[string]string{"tenant_id": s.tenant},
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
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

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      req.GetTopic(),
		Priority:   jobPriority,
		ContextPtr: ctxPtr,
		AdapterId:  req.GetAdapterId(),
		EnvVars:    map[string]string{"tenant_id": s.tenant},
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
