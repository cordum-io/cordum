package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/memory"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/secrets"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	wf "github.com/cordum/cordum/core/workflow"
)

const (
	defaultGrpcAddr       = ":8080"
	defaultHttpAddr       = ":8081"
	defaultMetricsAddr    = ":9092"
	maxJobPayloadBytes    = 2 << 20 // 2 MiB limit for incoming job payloads
	maxPromptChars        = 100000
	defaultRateLimitRPS   = 50
	defaultRateLimitBurst = 100
	// #nosec G101 -- protocol label, not a credential.
	wsAPIKeyProtocol = "cordum-api-key"
)

const (
	envGatewayGrpcAddr    = "GATEWAY_GRPC_ADDR"
	envGatewayHTTPAddr    = "GATEWAY_HTTP_ADDR"
	envGatewayMetricsAddr = "GATEWAY_METRICS_ADDR"
)

const (
	microsPerSecond      = int64(1_000_000)
	microsPerMillisecond = int64(1_000)
	secondsThreshold     = int64(1_000_000_000_000)
	millisThreshold      = int64(1_000_000_000_000_000)
	microsThreshold      = int64(1_000_000_000_000_000_000)
)

type server struct {
	pb.UnimplementedCordumApiServer
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
	started time.Time
	auth    AuthProvider

	workflowStore  *wf.RedisStore
	workflowEng    *wf.Engine
	configSvc      *configsvc.Service
	dlqStore       *memory.DLQStore
	artifactStore  artifacts.Store
	lockStore      locks.Store
	schemaRegistry *schema.Registry
	safetyConn     *grpc.ClientConn
	safetyClient   pb.SafetyKernelClient

	marketplaceMu    sync.Mutex
	marketplaceCache marketplaceCache
}

var upgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return isAllowedOrigin(r) },
	Subprotocols: []string{wsAPIKeyProtocol},
}

type tokenBucket struct {
	tokens chan struct{}
}

func newTokenBucket(rps, burst int) *tokenBucket {
	if rps <= 0 || burst <= 0 {
		return nil
	}
	tb := &tokenBucket{tokens: make(chan struct{}, burst)}
	for i := 0; i < burst; i++ {
		tb.tokens <- struct{}{}
	}
	interval := time.Second / time.Duration(rps)
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case tb.tokens <- struct{}{}:
			default:
			}
		}
	}()
	return tb
}

func newTokenBucketFromEnv() *tokenBucket {
	rps := defaultRateLimitRPS
	burst := defaultRateLimitBurst
	if val := os.Getenv("API_RATE_LIMIT_RPS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			rps = parsed
		}
	}
	if val := os.Getenv("API_RATE_LIMIT_BURST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			burst = parsed
		}
	}
	return newTokenBucket(rps, burst)
}

func (tb *tokenBucket) Allow() bool {
	if tb == nil {
		return true
	}
	select {
	case <-tb.tokens:
		return true
	default:
		return false
	}
}

var apiLimiter = newTokenBucketFromEnv()

type submitJobRequest struct {
	Prompt             string            `json:"prompt"`
	Topic              string            `json:"topic"`
	AdapterId          string            `json:"adapter_id"`
	Priority           string            `json:"priority"`
	Context            any               `json:"context"`
	MemoryId           string            `json:"memory_id"`
	Mode               string            `json:"context_mode"`
	TenantId           string            `json:"tenant_id"`
	PrincipalId        string            `json:"principal_id"`
	ActorId            string            `json:"actor_id"`
	ActorType          string            `json:"actor_type"`
	IdempotencyKey     string            `json:"idempotency_key"`
	PackId             string            `json:"pack_id"`
	Capability         string            `json:"capability"`
	RiskTags           []string          `json:"risk_tags"`
	Requires           []string          `json:"requires"`
	OrgId              string            `json:"org_id"`
	TeamId             string            `json:"team_id"`
	ProjectId          string            `json:"project_id"`
	Labels             map[string]string `json:"labels"`
	MaxInputTokens     int32             `json:"max_input_tokens"`
	AllowSummarization bool              `json:"allow_summarization"`
	AllowRetrieval     bool              `json:"allow_retrieval"`
	Tags               []string          `json:"tags"`
	MaxOutputTokens    int64             `json:"max_output_tokens"`
	MaxTotalTokens     int64             `json:"max_total_tokens"`
	DeadlineMs         int64             `json:"deadline_ms"`
}

type policyMetaRequest struct {
	TenantId       string            `json:"tenant_id"`
	ActorId        string            `json:"actor_id"`
	ActorType      string            `json:"actor_type"`
	IdempotencyKey string            `json:"idempotency_key"`
	Capability     string            `json:"capability"`
	RiskTags       []string          `json:"risk_tags"`
	Requires       []string          `json:"requires"`
	PackId         string            `json:"pack_id"`
	Labels         map[string]string `json:"labels"`
}

type policyCheckRequest struct {
	JobId           string             `json:"job_id"`
	Topic           string             `json:"topic"`
	Tenant          string             `json:"tenant"`
	OrgId           string             `json:"org_id"`
	TeamId          string             `json:"team_id"`
	WorkflowId      string             `json:"workflow_id"`
	StepId          string             `json:"step_id"`
	PrincipalId     string             `json:"principal_id"`
	Priority        string             `json:"priority"`
	EstimatedCost   float64            `json:"estimated_cost"`
	Budget          *pb.Budget         `json:"budget"`
	Labels          map[string]string  `json:"labels"`
	MemoryId        string             `json:"memory_id"`
	EffectiveConfig any                `json:"effective_config"`
	Meta            *policyMetaRequest `json:"meta"`
}

func (r *submitJobRequest) applyDefaults(defaultTenant string) {
	if r.MaxInputTokens == 0 {
		r.MaxInputTokens = 8000
	}
	if r.MaxOutputTokens == 0 {
		r.MaxOutputTokens = 1024
	}
	if r.Topic == "" {
		r.Topic = "job.default"
	}
	// Prioritize OrgId, then TenantId, then default
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	r.TenantId = r.OrgId // Ensure TenantId is consistent with OrgId
}

func (r *submitJobRequest) validate(defaultTenant string) error {
	if r == nil {
		return errors.New("request required")
	}
	if len(r.Prompt) == 0 {
		return errors.New("prompt is required")
	}
	if len(r.Prompt) > maxPromptChars {
		return fmt.Errorf("prompt too long (>%d chars)", maxPromptChars)
	}
	if r.Topic == "" {
		return errors.New("topic is required")
	}
	if !strings.HasPrefix(r.Topic, "job.") {
		return errors.New("topic must start with job.")
	}
	if r.MaxInputTokens < 0 || r.MaxOutputTokens < 0 || r.MaxTotalTokens < 0 {
		return errors.New("token limits must be non-negative")
	}
	if r.DeadlineMs < 0 {
		return errors.New("deadline_ms must be non-negative")
	}
	if r.ActorType != "" && parseActorType(r.ActorType) == pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		return errors.New("actor_type must be 'human' or 'service'")
	}
	if len(r.Tags) > 50 {
		return errors.New("too many tags (max 50)")
	}
	if len(r.Labels) > 50 {
		return errors.New("too many labels (max 50)")
	}
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	return nil
}

func buildJobMetadata(metaReq *policyMetaRequest, tenant, principal string) *pb.JobMetadata {
	if metaReq == nil && tenant == "" && principal == "" {
		return nil
	}
	meta := &pb.JobMetadata{
		TenantId: tenant,
	}
	if metaReq != nil {
		if metaReq.TenantId != "" {
			meta.TenantId = metaReq.TenantId
		}
		meta.ActorId = strings.TrimSpace(metaReq.ActorId)
		meta.ActorType = parseActorType(metaReq.ActorType)
		meta.IdempotencyKey = strings.TrimSpace(metaReq.IdempotencyKey)
		meta.Capability = strings.TrimSpace(metaReq.Capability)
		meta.RiskTags = append(meta.RiskTags, metaReq.RiskTags...)
		meta.Requires = append(meta.Requires, metaReq.Requires...)
		meta.PackId = strings.TrimSpace(metaReq.PackId)
		if len(metaReq.Labels) > 0 {
			meta.Labels = metaReq.Labels
		}
	}
	if meta.ActorId == "" {
		meta.ActorId = principal
	}
	return meta
}

func buildPolicyCheckRequest(ctx context.Context, req *policyCheckRequest, cfgSvc *configsvc.Service, defaultTenant string) (*pb.PolicyCheckRequest, error) {
	if req == nil {
		return nil, errors.New("request required")
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = strings.TrimSpace(req.OrgId)
	}
	if tenant == "" {
		tenant = defaultTenant
	}
	meta := buildJobMetadata(req.Meta, tenant, strings.TrimSpace(req.PrincipalId))

	checkReq := &pb.PolicyCheckRequest{
		JobId:         strings.TrimSpace(req.JobId),
		Topic:         topic,
		Tenant:        tenant,
		Priority:      parsePriority(req.Priority),
		EstimatedCost: req.EstimatedCost,
		Budget:        req.Budget,
		PrincipalId:   strings.TrimSpace(req.PrincipalId),
		Labels:        req.Labels,
		MemoryId:      strings.TrimSpace(req.MemoryId),
		Meta:          meta,
	}

	if req.EffectiveConfig != nil {
		if data, err := json.Marshal(req.EffectiveConfig); err == nil {
			checkReq.EffectiveConfig = data
		}
	} else if cfgSvc != nil {
		orgID := req.OrgId
		if orgID == "" {
			orgID = tenant
		}
		if snap, err := cfgSvc.EffectiveSnapshot(ctx, orgID, req.TeamId, req.WorkflowId, req.StepId); err == nil && snap != nil {
			if data, err := json.Marshal(snap); err == nil {
				checkReq.EffectiveConfig = data
			}
		}
	}

	return checkReq, nil
}

func Run(cfg *config.Config) error {
	return RunWithAuth(cfg, nil)
}

// RunWithAuth starts the gateway with a custom auth provider. When nil, a basic
// single-tenant provider is used.
func RunWithAuth(cfg *config.Config, provider AuthProvider) error {
	if cfg == nil {
		cfg = config.Load()
	}
	grpcAddr := addrFromEnv(envGatewayGrpcAddr, defaultGrpcAddr)
	httpAddr := addrFromEnv(envGatewayHTTPAddr, defaultHttpAddr)
	metricsAddr := addrFromEnv(envGatewayMetricsAddr, defaultMetricsAddr)

	tenantID := strings.TrimSpace(os.Getenv("TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}

	gwMetrics := infraMetrics.NewGatewayProm("cordum_api_gateway")
	if provider == nil {
		basic, err := newBasicAuthProvider(tenantID)
		if err != nil {
			return fmt.Errorf("init auth: %w", err)
		}
		provider = basic
	}

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer memStore.Close()

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis job store: %w", err)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer natsBus.Close()

	workflowStore, err := wf.NewRedisWorkflowStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis workflow store: %w", err)
	}
	defer workflowStore.Close()
	workflowEng := wf.NewEngine(workflowStore, natsBus)

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis config service: %w", err)
	}
	defer configSvc.Close()
	if err := seedDefaultPackCatalogs(context.Background(), configSvc); err != nil {
		logging.Error("api-gateway", "seed pack catalogs failed", "error", err)
	}
	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis schema registry: %w", err)
	}
	defer schemaRegistry.Close()
	workflowEng = workflowEng.WithMemory(memStore).WithConfig(configSvc).WithSchemaRegistry(schemaRegistry)

	dlqStore, err := memory.NewDLQStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis dlq store: %w", err)
	}
	defer dlqStore.Close()

	artifactStore, err := artifacts.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis artifact store: %w", err)
	}
	defer artifactStore.Close()

	lockStore, err := locks.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis lock store: %w", err)
	}
	defer lockStore.Close()

	var safetyConn *grpc.ClientConn
	var safetyClient pb.SafetyKernelClient
	if cfg.SafetyKernelAddr != "" {
		conn, client, err := dialSafetyKernel(cfg.SafetyKernelAddr)
		if err != nil {
			if os.Getenv("SAFETY_KERNEL_TLS_REQUIRED") == "true" {
				return fmt.Errorf("safety kernel dial failed: %w", err)
			}
			logging.Error("api-gateway", "safety kernel dial failed", "error", err)
		} else {
			safetyConn = conn
			safetyClient = client
			defer safetyConn.Close()
		}
	}

	s := &server{
		memStore:       memStore,
		jobStore:       jobStore,
		bus:            natsBus,
		workers:        make(map[string]*pb.Heartbeat),
		clients:        make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh:       make(chan *pb.BusPacket, 512),
		metrics:        gwMetrics,
		tenant:         tenantID,
		auth:           provider,
		started:        time.Now().UTC(),
		workflowStore:  workflowStore,
		workflowEng:    workflowEng,
		configSvc:      configSvc,
		dlqStore:       dlqStore,
		artifactStore:  artifactStore,
		lockStore:      lockStore,
		schemaRegistry: schemaRegistry,
		safetyConn:     safetyConn,
		safetyClient:   safetyClient,
	}

	s.startBusTaps()

	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc (%s): %w", grpcAddr, err)
	}
	grpcCreds := insecure.NewCredentials()
	if certFile := os.Getenv("GRPC_TLS_CERT"); certFile != "" {
		keyFile := os.Getenv("GRPC_TLS_KEY")
		if keyFile == "" {
			logging.Error("api-gateway", "grpc tls key missing", "cert", certFile)
		} else if creds, err := credentials.NewServerTLSFromFile(certFile, keyFile); err != nil {
			logging.Error("api-gateway", "grpc tls setup failed", "error", err)
		} else {
			grpcCreds = creds
		}
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(grpcCreds),
		grpc.UnaryInterceptor(apiKeyUnaryInterceptor(s.auth)),
	)
	pb.RegisterCordumApiServer(grpcServer, s)
	reflection.Register(grpcServer)

	go func() {
		logging.Info("api-gateway", "grpc listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			logging.Error("api-gateway", "grpc server error", "error", err)
		}
	}()

	return startHTTPServer(s, httpAddr, metricsAddr)
}

// startBusTaps subscribes to heartbeats and system events once for the lifetime of the gateway.
func (s *server) startBusTaps() {
	// Heartbeats -> worker registry snapshot
	if err := s.bus.Subscribe(capsdk.SubjectHeartbeat, "", func(p *pb.BusPacket) error {
		if hb := p.GetHeartbeat(); hb != nil {
			s.workerMu.Lock()
			s.workers[hb.WorkerId] = hb
			s.workerMu.Unlock()
			// Also stream heartbeats to WS listeners (best effort).
			select {
			case s.eventsCh <- p:
			default:
			}
		}
		return nil
	}); err != nil {
		logging.Error("api-gateway", "bus subscribe failed", "subject", capsdk.SubjectHeartbeat, "error", err)
	}

	// DLQ tap to persist entries
	if s.dlqStore != nil {
		if err := s.bus.Subscribe(capsdk.SubjectDLQ, "", func(p *pb.BusPacket) error {
			if jr := p.GetJobResult(); jr != nil {
				jobID := strings.TrimSpace(jr.JobId)
				topic := ""
				lastState := ""
				attempts := 0
				if s.jobStore != nil && jobID != "" {
					if t, err := s.jobStore.GetTopic(context.Background(), jobID); err == nil {
						topic = t
					}
					if st, err := s.jobStore.GetState(context.Background(), jobID); err == nil {
						lastState = string(st)
					}
					if a, err := s.jobStore.GetAttempts(context.Background(), jobID); err == nil {
						attempts = a
					}
				}
				_ = s.dlqStore.Add(context.Background(), memory.DLQEntry{
					JobID:      jobID,
					Topic:      topic,
					Status:     jr.Status.String(),
					Reason:     jr.ErrorMessage,
					ReasonCode: strings.TrimSpace(jr.ErrorCode),
					LastState:  lastState,
					Attempts:   attempts,
					CreatedAt:  time.Now().UTC(),
				})

				// Best effort: ensure a result exists for failed-to-dispatch jobs so clients can inspect `res:<job_id>`.
				if s.memStore != nil && s.jobStore != nil && jobID != "" {
					resKey := memory.MakeResultKey(jobID)
					resPtr := memory.PointerForKey(resKey)
					body := map[string]any{
						"job_id":       jobID,
						"status":       jr.Status.String(),
						"error":        map[string]any{"message": jr.ErrorMessage},
						"processed_by": "cordum-scheduler",
						"completed_at": time.Now().UTC().Format(time.RFC3339),
					}
					if data, err := json.Marshal(body); err == nil {
						_ = s.memStore.PutResult(context.Background(), resKey, data)
					}
					if existing, err := s.jobStore.GetResultPtr(context.Background(), jobID); err != nil || strings.TrimSpace(existing) == "" {
						_ = s.jobStore.SetResultPtr(context.Background(), jobID, resPtr)
					}
				}
			}
			return nil
		}); err != nil {
			logging.Error("api-gateway", "bus subscribe failed", "subject", capsdk.SubjectDLQ, "error", err)
		}
	}

	// Event taps -> broadcast channel
	for _, subj := range []string{"sys.job.>", "sys.audit.>"} {
		subject := subj
		if err := s.bus.Subscribe(subject, "", func(p *pb.BusPacket) error {
			if subject == "sys.job.>" {
				s.handleWorkflowJobResult(context.Background(), p.GetJobResult())
			}
			select {
			case s.eventsCh <- p:
			default:
			}
			return nil
		}); err != nil {
			logging.Error("api-gateway", "bus subscribe failed", "subject", subject, "error", err)
		}
	}

	// Broadcast loop to WS clients
	go func() {
		for evt := range s.eventsCh {
			var slowClients []*websocket.Conn
			s.clientsMu.RLock()
			for conn, ch := range s.clients {
				select {
				case ch <- evt:
				default:
					slowClients = append(slowClients, conn)
				}
			}
			s.clientsMu.RUnlock()

			if len(slowClients) > 0 {
				s.clientsMu.Lock()
				for _, conn := range slowClients {
					delete(s.clients, conn)
				}
				s.clientsMu.Unlock()
				for _, conn := range slowClients {
					if err := conn.Close(); err != nil {
						logging.Error("api-gateway", "ws client close failed", "error", err)
					}
				}
			}
		}
	}()
}

func (s *server) handleWorkflowJobResult(ctx context.Context, jr *pb.JobResult) {
	if s == nil || s.workflowEng == nil || jr == nil || jr.JobId == "" {
		return
	}
	runID, _ := splitWorkflowJobID(jr.JobId)
	if runID == "" {
		return
	}

	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		ok, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
		if err != nil || !ok {
			return
		}
		defer func() { _ = s.jobStore.ReleaseLock(context.Background(), lockKey) }()
	}

	s.workflowEng.HandleJobResult(ctx, jr)
}

func splitWorkflowJobID(jobID string) (runID, stepID string) {
	parts := strings.Split(jobID, ":")
	if len(parts) < 2 {
		return "", ""
	}
	runID = strings.Join(parts[:len(parts)-1], ":")
	stepID = parts[len(parts)-1]
	return runID, stepID
}

func startHTTPServer(s *server, httpAddr, metricsAddr string) error {
	mux := http.NewServeMux()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", infraMetrics.Handler())
	go func() {
		srv := &http.Server{
			Addr:         metricsAddr,
			Handler:      metricsMux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		logging.Info("api-gateway", "metrics listening", "addr", metricsAddr+"/metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("api-gateway", "metrics server error", "error", err)
		}
	}()

	// 1. Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 2. Workers (RPC via NATS)
	mux.HandleFunc("GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))

	// 2.5 Status snapshot (Redis/NATS/workers/uptime)
	mux.HandleFunc("GET /api/v1/status", s.instrumented("/api/v1/status", s.handleStatus))

	// 3. Jobs (Redis ZSet)
	mux.HandleFunc("GET /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleListJobs))

	// 4. Job Details
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.instrumented("/api/v1/jobs/{id}", s.handleGetJob))
	mux.HandleFunc("GET /api/v1/jobs/{id}/decisions", s.instrumented("/api/v1/jobs/{id}/decisions", s.handleListJobDecisions))
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.instrumented("/api/v1/jobs/{id}/cancel", s.handleCancelJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/remediate", s.instrumented("/api/v1/jobs/{id}/remediate", s.handleRemediateJob))

	// 4.5 Memory pointers (debug)
	mux.HandleFunc("GET /api/v1/memory", s.instrumented("/api/v1/memory", s.handleGetMemory))
	// 4.6 Artifact store
	mux.HandleFunc("POST /api/v1/artifacts", s.instrumented("/api/v1/artifacts", s.handlePutArtifact))
	mux.HandleFunc("GET /api/v1/artifacts/{ptr}", s.instrumented("/api/v1/artifacts/{ptr}", s.handleGetArtifact))

	// 5. Submit Job (REST)
	mux.HandleFunc("POST /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleSubmitJobHTTP))

	// 6. Trace Details
	mux.HandleFunc("GET /api/v1/traces/{id}", s.instrumented("/api/v1/traces/{id}", s.handleGetTrace))

	// 8. Workflows
	mux.HandleFunc("GET /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleListWorkflows))
	mux.HandleFunc("POST /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleCreateWorkflow))
	mux.HandleFunc("GET /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleGetWorkflow))
	mux.HandleFunc("DELETE /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleDeleteWorkflow))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleStartRun))
	mux.HandleFunc("GET /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleListRuns))
	mux.HandleFunc("GET /api/v1/workflow-runs", s.instrumented("/api/v1/workflow-runs", s.handleListAllRuns))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleGetRun))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}/timeline", s.instrumented("/api/v1/workflow-runs/{id}/timeline", s.handleGetRunTimeline))
	mux.HandleFunc("DELETE /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleDeleteRun))
	mux.HandleFunc("POST /api/v1/workflow-runs/{id}/rerun", s.instrumented("/api/v1/workflow-runs/{id}/rerun", s.handleRerunRun))

	// 9. Config
	mux.HandleFunc("GET /api/v1/config", s.instrumented("/api/v1/config", s.handleGetConfig))
	mux.HandleFunc("GET /api/v1/config/effective", s.instrumented("/api/v1/config/effective", s.handleGetEffectiveConfig))
	mux.HandleFunc("POST /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))

	// 9.25 Packs
	mux.HandleFunc("GET /api/v1/packs", s.instrumented("/api/v1/packs", s.handleListPacks))
	mux.HandleFunc("GET /api/v1/packs/{id}", s.instrumented("/api/v1/packs/{id}", s.handleGetPack))
	mux.HandleFunc("POST /api/v1/packs/install", s.instrumented("/api/v1/packs/install", s.handleInstallPack))
	mux.HandleFunc("POST /api/v1/packs/{id}/uninstall", s.instrumented("/api/v1/packs/{id}/uninstall", s.handleUninstallPack))
	mux.HandleFunc("POST /api/v1/packs/{id}/verify", s.instrumented("/api/v1/packs/{id}/verify", s.handleVerifyPack))
	mux.HandleFunc("GET /api/v1/marketplace/packs", s.instrumented("/api/v1/marketplace/packs", s.handleMarketplacePacks))
	mux.HandleFunc("POST /api/v1/marketplace/install", s.instrumented("/api/v1/marketplace/install", s.handleMarketplaceInstall))

	// 9.5 Schemas
	mux.HandleFunc("POST /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleRegisterSchema))
	mux.HandleFunc("GET /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleListSchemas))
	mux.HandleFunc("GET /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleGetSchema))
	mux.HandleFunc("DELETE /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleDeleteSchema))

	// 9.6 Resource locks
	mux.HandleFunc("GET /api/v1/locks", s.instrumented("/api/v1/locks", s.handleGetLock))
	mux.HandleFunc("POST /api/v1/locks/acquire", s.instrumented("/api/v1/locks/acquire", s.handleAcquireLock))
	mux.HandleFunc("POST /api/v1/locks/release", s.instrumented("/api/v1/locks/release", s.handleReleaseLock))
	mux.HandleFunc("POST /api/v1/locks/renew", s.instrumented("/api/v1/locks/renew", s.handleRenewLock))

	// 10. DLQ
	mux.HandleFunc("GET /api/v1/dlq", s.instrumented("/api/v1/dlq", s.handleListDLQ))
	mux.HandleFunc("GET /api/v1/dlq/page", s.instrumented("/api/v1/dlq/page", s.handleListDLQPage))
	mux.HandleFunc("DELETE /api/v1/dlq/{job_id}", s.instrumented("/api/v1/dlq/{job_id}", s.handleDeleteDLQ))
	mux.HandleFunc("POST /api/v1/dlq/{job_id}/retry", s.instrumented("/api/v1/dlq/{job_id}/retry", s.handleRetryDLQ))

	// 11. Workflow approvals
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.handleApproveStep))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/cancel", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/cancel", s.handleCancelRun))

	// 11.5 Job approvals
	mux.HandleFunc("GET /api/v1/approvals", s.instrumented("/api/v1/approvals", s.handleListApprovals))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/approve", s.instrumented("/api/v1/approvals/{job_id}/approve", s.handleApproveJob))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/reject", s.instrumented("/api/v1/approvals/{job_id}/reject", s.handleRejectJob))

	// 12. Policy endpoints
	mux.HandleFunc("POST /api/v1/policy/evaluate", s.instrumented("/api/v1/policy/evaluate", s.handlePolicyEvaluate))
	mux.HandleFunc("POST /api/v1/policy/simulate", s.instrumented("/api/v1/policy/simulate", s.handlePolicySimulate))
	mux.HandleFunc("POST /api/v1/policy/explain", s.instrumented("/api/v1/policy/explain", s.handlePolicyExplain))
	mux.HandleFunc("GET /api/v1/policy/snapshots", s.instrumented("/api/v1/policy/snapshots", s.handlePolicySnapshots))
	mux.HandleFunc("GET /api/v1/policy/rules", s.instrumented("/api/v1/policy/rules", s.handlePolicyRules))
	mux.HandleFunc("GET /api/v1/policy/bundles", s.instrumented("/api/v1/policy/bundles", s.handlePolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleGetPolicyBundle))
	mux.HandleFunc("PUT /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handlePutPolicyBundle))
	mux.HandleFunc("POST /api/v1/policy/bundles/{id}/simulate", s.instrumented("/api/v1/policy/bundles/{id}/simulate", s.handleSimulatePolicyBundle))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleListPolicyBundleSnapshots))
	mux.HandleFunc("POST /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleCapturePolicyBundleSnapshot))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots/{id}", s.instrumented("/api/v1/policy/bundles/snapshots/{id}", s.handleGetPolicyBundleSnapshot))
	mux.HandleFunc("POST /api/v1/policy/publish", s.instrumented("/api/v1/policy/publish", s.handlePublishPolicyBundles))
	mux.HandleFunc("POST /api/v1/policy/rollback", s.instrumented("/api/v1/policy/rollback", s.handleRollbackPolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/audit", s.instrumented("/api/v1/policy/audit", s.handleListPolicyAudit))

	// 7. Stream (WebSocket)
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))

	// CORS Middleware
	handler := corsMiddleware(rateLimitMiddleware(apiKeyMiddleware(s.auth, mux)))

	logging.Info("api-gateway", "http listening", "addr", httpAddr)
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		logging.Error("api-gateway", "http server error", "error", err)
		return err
	}
	return nil
}

// --- Handlers ---

func (s *server) handleGetWorkers(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	s.workerMu.RLock()
	out := make([]*pb.Heartbeat, 0, len(s.workers))
	for _, hb := range s.workers {
		out = append(out, hb)
	}
	s.workerMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	uptimeSeconds := int64(0)
	if !s.started.IsZero() {
		uptimeSeconds = int64(now.Sub(s.started).Seconds())
	}

	s.workerMu.RLock()
	workersCount := len(s.workers)
	s.workerMu.RUnlock()

	natsConnected := false
	natsStatus := "UNKNOWN"
	natsURL := ""
	if nb, ok := s.bus.(*bus.NatsBus); ok {
		natsConnected = nb.IsConnected()
		natsStatus = nb.Status()
		natsURL = nb.ConnectedURL()
	}

	redisOK := false
	redisErr := ""
	if s.jobStore == nil {
		redisErr = "job store unavailable"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		err := s.jobStore.Ping(ctx)
		cancel()
		if err != nil {
			redisErr = err.Error()
		} else {
			redisOK = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"time":           now.Format(time.RFC3339),
		"uptime_seconds": uptimeSeconds,
		"nats": map[string]any{
			"connected": natsConnected,
			"status":    natsStatus,
			"url":       natsURL,
		},
		"redis": map[string]any{
			"ok":    redisOK,
			"error": redisErr,
		},
		"workers": map[string]any{
			"count": workersCount,
		},
	})
}

func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		http.Error(w, "job store unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	stateFilter := strings.ToUpper(r.URL.Query().Get("state"))
	topicFilter := r.URL.Query().Get("topic")
	tenantFilter := r.URL.Query().Get("tenant")
	teamFilter := r.URL.Query().Get("team")
	traceFilter := r.URL.Query().Get("trace_id")
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	updatedAfter := int64(0)
	if q := r.URL.Query().Get("updated_after"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedAfter = v
		}
	}
	updatedBefore := int64(0)
	if q := r.URL.Query().Get("updated_before"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedBefore = v
		}
	}

	cursor = normalizeTimestampMicrosUpper(cursor)
	updatedAfter = normalizeTimestampMicrosLower(updatedAfter)
	updatedBefore = normalizeTimestampMicrosUpper(updatedBefore)

	resolvedTenant, err := s.resolveTenant(r, tenantFilter)
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	tenantFilter = resolvedTenant

	var jobs []scheduler.JobRecord
	if traceFilter != "" {
		jobs, err = s.jobStore.GetTraceJobs(r.Context(), traceFilter)
	} else if cursor > 0 {
		jobs, err = s.jobStore.ListRecentJobsByScore(r.Context(), cursor, limit)
	} else {
		jobs, err = s.jobStore.ListRecentJobs(r.Context(), limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// client-side filter to avoid changing store signature
	filtered := make([]scheduler.JobRecord, 0, len(jobs))
	for _, j := range jobs {
		if stateFilter != "" && strings.ToUpper(string(j.State)) != stateFilter {
			continue
		}
		if topicFilter != "" && j.Topic != topicFilter {
			continue
		}
		if tenantFilter != "" && j.Tenant != tenantFilter {
			continue
		}
		if teamFilter != "" && j.Team != teamFilter {
			continue
		}
		if updatedAfter > 0 && j.UpdatedAt < updatedAfter {
			continue
		}
		if updatedBefore > 0 && j.UpdatedAt > updatedBefore {
			continue
		}
		filtered = append(filtered, j)
	}
	w.Header().Set("Content-Type", "application/json")
	var nextCursor *int64
	if int64(len(filtered)) == limit {
		nc := filtered[len(filtered)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       filtered,
		"next_cursor": nextCursor,
	})
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
	safetyRecord, _ := s.jobStore.GetSafetyDecision(r.Context(), id)
	approvalRecord, _ := s.jobStore.GetApprovalRecord(r.Context(), id)
	topic, _ := s.jobStore.GetTopic(r.Context(), id)
	tenant, _ := s.jobStore.GetTenant(r.Context(), id)
	if err := s.requireTenantAccess(r, tenant); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	actorID, _ := s.jobStore.GetActorID(r.Context(), id)
	actorType, _ := s.jobStore.GetActorType(r.Context(), id)
	idempotencyKey, _ := s.jobStore.GetIdempotencyKey(r.Context(), id)
	capability, _ := s.jobStore.GetCapability(r.Context(), id)
	packID, _ := s.jobStore.GetPackID(r.Context(), id)
	riskTags, _ := s.jobStore.GetRiskTags(r.Context(), id)
	requires, _ := s.jobStore.GetRequires(r.Context(), id)
	attempts, _ := s.jobStore.GetAttempts(r.Context(), id)

	ctxPtr := memory.PointerForKey(memory.MakeContextKey(id))

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

	var contextData any
	if s.memStore != nil {
		if bytes, err := s.memStore.GetContext(r.Context(), memory.MakeContextKey(id)); err == nil {
			_ = json.Unmarshal(bytes, &contextData)
		}
	}

	traceID := ""
	if s.jobStore != nil {
		if val, err := s.jobStore.GetTraceID(r.Context(), id); err == nil {
			traceID = val
		}
	}
	labels := map[string]string{}
	workflowID := ""
	runID := ""
	stepID := ""
	if s.jobStore != nil {
		if req, err := s.jobStore.GetJobRequest(r.Context(), id); err == nil && req != nil {
			if req.WorkflowId != "" {
				workflowID = req.WorkflowId
			}
			if len(req.Labels) > 0 {
				for k, v := range req.Labels {
					labels[k] = v
				}
				if workflowID == "" {
					workflowID = req.Labels["workflow_id"]
				}
				runID = req.Labels["run_id"]
				stepID = req.Labels["step_id"]
			}
		}
	}

	errorMessage := ""
	errorStatus := ""
	errorCode := ""
	lastState := ""
	attemptsFromDLQ := 0
	if s.dlqStore != nil {
		if entry, err := s.dlqStore.Get(r.Context(), id); err == nil && entry != nil {
			errorMessage = strings.TrimSpace(entry.Reason)
			errorStatus = strings.TrimSpace(entry.Status)
			errorCode = strings.TrimSpace(entry.ReasonCode)
			lastState = strings.TrimSpace(entry.LastState)
			attemptsFromDLQ = entry.Attempts
		}
	}

	resp := map[string]any{
		"id":                  id,
		"state":               state,
		"trace_id":            traceID,
		"context_ptr":         ctxPtr,
		"context":             contextData,
		"result_ptr":          resPtr,
		"result":              resultData,
		"topic":               topic,
		"tenant":              tenant,
		"actor_id":            actorID,
		"actor_type":          actorType,
		"idempotency_key":     idempotencyKey,
		"capability":          capability,
		"pack_id":             packID,
		"risk_tags":           riskTags,
		"requires":            requires,
		"attempts":            attempts,
		"safety_decision":     string(safetyRecord.Decision),
		"safety_reason":       safetyRecord.Reason,
		"safety_rule_id":      safetyRecord.RuleID,
		"safety_snapshot":     safetyRecord.PolicySnapshot,
		"safety_constraints":  safetyRecord.Constraints,
		"safety_remediations": safetyRecord.Remediations,
		"safety_job_hash":     safetyRecord.JobHash,
		"approval_required":   safetyRecord.ApprovalRequired,
		"approval_ref":        safetyRecord.ApprovalRef,
		"labels":              labels,
		"workflow_id":         workflowID,
		"run_id":              runID,
		"step_id":             stepID,
	}
	if errorMessage != "" {
		resp["error_message"] = errorMessage
	}
	if errorStatus != "" {
		resp["error_status"] = errorStatus
	}
	if errorCode != "" {
		resp["error_code"] = errorCode
	}
	if lastState != "" {
		resp["last_state"] = lastState
	}
	if attemptsFromDLQ > 0 {
		resp["attempts"] = attemptsFromDLQ
	}
	if approvalRecord.ApprovedBy != "" {
		resp["approval_by"] = approvalRecord.ApprovedBy
	}
	if approvalRecord.ApprovedRole != "" {
		resp["approval_role"] = approvalRecord.ApprovedRole
	}
	if approvalRecord.ApprovedAt != 0 {
		resp["approval_at"] = approvalRecord.ApprovedAt
	}
	if approvalRecord.Reason != "" {
		resp["approval_reason"] = approvalRecord.Reason
	}
	if approvalRecord.Note != "" {
		resp["approval_note"] = approvalRecord.Note
	}
	if approvalRecord.PolicySnapshot != "" {
		resp["approval_policy_snapshot"] = approvalRecord.PolicySnapshot
	}
	if approvalRecord.JobHash != "" {
		resp["approval_job_hash"] = approvalRecord.JobHash
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleListJobDecisions(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		http.Error(w, "job store unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), id); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	decisions, err := s.jobStore.ListSafetyDecisions(r.Context(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(decisions)
}

func (s *server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	if s.memStore == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	ptr := strings.TrimSpace(r.URL.Query().Get("ptr"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))

	if ptr == "" && key == "" {
		http.Error(w, "missing ptr or key", http.StatusBadRequest)
		return
	}

	if ptr != "" {
		ptr = strings.Trim(ptr, "\"'")
	}

	if key != "" {
		key = strings.Trim(key, "\"'")
		if strings.HasPrefix(key, "redis://") {
			ptr = key
			parsedKey, err := memory.KeyFromPointer(key)
			if err != nil {
				http.Error(w, "invalid key pointer", http.StatusBadRequest)
				return
			}
			key = parsedKey
		}
	}

	if key == "" {
		parsedKey, err := memory.KeyFromPointer(ptr)
		if err != nil {
			http.Error(w, "invalid pointer", http.StatusBadRequest)
			return
		}
		key = parsedKey
	}
	if ptr == "" {
		ptr = memory.PointerForKey(key)
	}

	var (
		data []byte
		err  error
		kind string
	)
	switch {
	case strings.HasPrefix(key, "ctx:"):
		kind = "context"
		data, err = s.memStore.GetContext(r.Context(), key)
	case strings.HasPrefix(key, "res:"):
		kind = "result"
		data, err = s.memStore.GetResult(r.Context(), key)
	case strings.HasPrefix(key, "mem:"):
		kind = "memory"

		rs, ok := s.memStore.(*memory.RedisStore)
		if !ok || rs.Client() == nil {
			http.Error(w, "memory inspection unavailable", http.StatusNotImplemented)
			return
		}
		client := rs.Client()
		redisType, typeErr := client.Type(r.Context(), key).Result()
		if typeErr != nil {
			err = typeErr
			break
		}
		if redisType == "none" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		decodeMaybeJSON := func(v string) any {
			if strings.TrimSpace(v) == "" {
				return v
			}
			var parsed any
			if json.Unmarshal([]byte(v), &parsed) == nil {
				return parsed
			}
			return v
		}

		var payload any
		switch redisType {
		case "string":
			raw, getErr := client.Get(r.Context(), key).Bytes()
			if getErr != nil {
				err = getErr
				break
			}
			if utf8.Valid(raw) {
				payload = map[string]any{
					"redis_type": redisType,
					"value":      decodeMaybeJSON(string(raw)),
				}
			} else {
				payload = map[string]any{
					"redis_type": redisType,
					"base64":     base64.StdEncoding.EncodeToString(raw),
				}
			}
		case "list":
			items, lErr := client.LRange(r.Context(), key, 0, -1).Result()
			if lErr != nil {
				err = lErr
				break
			}
			decoded := make([]any, 0, len(items))
			for _, item := range items {
				decoded = append(decoded, decodeMaybeJSON(item))
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		case "set":
			items, sErr := client.SMembers(r.Context(), key).Result()
			if sErr != nil {
				err = sErr
				break
			}
			decoded := make([]any, 0, len(items))
			for _, item := range items {
				decoded = append(decoded, decodeMaybeJSON(item))
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		case "hash":
			items, hErr := client.HGetAll(r.Context(), key).Result()
			if hErr != nil {
				err = hErr
				break
			}
			decoded := make(map[string]any, len(items))
			for k, v := range items {
				decoded[k] = decodeMaybeJSON(v)
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		default:
			http.Error(w, fmt.Sprintf("unsupported redis key type: %s", redisType), http.StatusBadRequest)
			return
		}
		if err != nil {
			break
		}
		data, err = json.Marshal(payload)
	default:
		http.Error(w, "unsupported pointer key (only ctx:*, res:*, or mem:*)", http.StatusBadRequest)
		return
	}
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"pointer":    ptr,
		"key":        key,
		"kind":       kind,
		"size_bytes": len(data),
		"base64":     base64.StdEncoding.EncodeToString(data),
	}

	if utf8.Valid(data) {
		resp["text"] = string(data)
	}

	var jsonVal any
	if json.Unmarshal(data, &jsonVal) == nil {
		resp["json"] = jsonVal
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type artifactPutRequest struct {
	ContentBase64 string            `json:"content_base64"`
	Content       string            `json:"content"`
	ContentType   string            `json:"content_type"`
	Retention     string            `json:"retention"`
	Labels        map[string]string `json:"labels"`
}

func (s *server) handlePutArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifactStore == nil {
		http.Error(w, "artifact store unavailable", http.StatusServiceUnavailable)
		return
	}
	var req artifactPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	var content []byte
	if req.ContentBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			http.Error(w, "invalid base64 content", http.StatusBadRequest)
			return
		}
		content = data
	} else {
		content = []byte(req.Content)
	}
	if len(content) == 0 {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}
	maxBytes := int64(0)
	if raw := r.URL.Query().Get("max_bytes"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			maxBytes = v
		}
	}
	if raw := r.Header.Get("X-Max-Artifact-Bytes"); raw != "" && maxBytes == 0 {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			maxBytes = v
		}
	}
	if maxBytes > 0 && int64(len(content)) > maxBytes {
		http.Error(w, "artifact too large", http.StatusRequestEntityTooLarge)
		return
	}
	meta := artifacts.Metadata{
		ContentType: strings.TrimSpace(req.ContentType),
		Retention:   parseRetention(req.Retention),
		Labels:      req.Labels,
	}
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		if meta.Labels == nil {
			meta.Labels = map[string]string{}
		}
		if _, exists := meta.Labels["tenant_id"]; !exists {
			meta.Labels["tenant_id"] = auth.Tenant
		}
	}
	ptr, err := s.artifactStore.Put(r.Context(), content, meta)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"artifact_ptr": ptr,
		"size_bytes":   len(content),
	})
}

func (s *server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifactStore == nil {
		http.Error(w, "artifact store unavailable", http.StatusServiceUnavailable)
		return
	}
	ptr := strings.TrimSpace(r.PathValue("ptr"))
	if ptr == "" {
		http.Error(w, "artifact pointer required", http.StatusBadRequest)
		return
	}
	content, meta, err := s.artifactStore.Get(r.Context(), ptr)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "artifact not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		if meta.Labels != nil {
			if tenant := strings.TrimSpace(meta.Labels["tenant_id"]); tenant != "" && tenant != auth.Tenant {
				http.Error(w, "tenant access denied", http.StatusForbidden)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"artifact_ptr":   ptr,
		"content_base64": base64.StdEncoding.EncodeToString(content),
		"metadata":       meta,
	})
}

func (s *server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), id); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}

	state, err := s.jobStore.CancelJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("failed to cancel job: %v", err), http.StatusInternalServerError)
		return
	}
	if state == "" {
		state = scheduler.JobStateCancelled
	}
	if state != scheduler.JobStateCancelled {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     id,
			"state":  state,
			"reason": "job already terminal",
		})
		return
	}

	// Broadcast a synthetic cancellation event for listeners.
	cancelPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
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
	_ = s.bus.Publish(capsdk.SubjectResult, cancelPacket)

	// Best-effort cancel broadcast to workers.
	cancelReq := &pb.JobCancel{
		JobId:       id,
		Reason:      "cancelled via api",
		RequestedBy: "api-gateway",
	}
	cancelBusPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobCancel{JobCancel: cancelReq},
	}
	_ = s.bus.Publish(capsdk.SubjectCancel, cancelBusPacket)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":    id,
		"state": scheduler.JobStateCancelled,
	})
}

type remediateJobRequest struct {
	RemediationID string `json:"remediation_id"`
}

func (s *server) handleRemediateJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil || s.memStore == nil {
		http.Error(w, "job store or bus unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	jobID := r.PathValue("id")
	if jobID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	var body remediateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	origReq, err := s.jobStore.GetJobRequest(r.Context(), jobID)
	if err != nil || origReq == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if tenant := strings.TrimSpace(origReq.GetTenantId()); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	safetyRecord, err := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	if err != nil || len(safetyRecord.Remediations) == 0 {
		http.Error(w, "no remediations available", http.StatusConflict)
		return
	}
	var remediation *pb.PolicyRemediation
	if id := strings.TrimSpace(body.RemediationID); id != "" {
		for _, rem := range safetyRecord.Remediations {
			if rem != nil && rem.GetId() == id {
				remediation = rem
				break
			}
		}
		if remediation == nil {
			http.Error(w, "remediation not found", http.StatusNotFound)
			return
		}
	} else if len(safetyRecord.Remediations) == 1 {
		remediation = safetyRecord.Remediations[0]
	} else {
		http.Error(w, "remediation_id required", http.StatusBadRequest)
		return
	}

	newJobID := uuid.NewString()
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	if traceID == "" {
		traceID = uuid.NewString()
	}

	ctxPtr := origReq.GetContextPtr()
	if ctxPtr != "" {
		if key, err := memory.KeyFromPointer(ctxPtr); err == nil {
			if data, err := s.memStore.GetContext(r.Context(), key); err == nil {
				newKey := memory.MakeContextKey(newJobID)
				if err := s.memStore.PutContext(r.Context(), newKey, data); err == nil {
					ctxPtr = memory.PointerForKey(newKey)
				}
			}
		}
	}

	newReq, ok := proto.Clone(origReq).(*pb.JobRequest)
	if !ok || newReq == nil {
		http.Error(w, "failed to clone job request", http.StatusInternalServerError)
		return
	}
	newReq.JobId = newJobID
	newReq.ParentJobId = origReq.GetJobId()
	if ctxPtr != "" {
		newReq.ContextPtr = ctxPtr
	}
	if remediation.GetReplacementTopic() != "" {
		newReq.Topic = remediation.GetReplacementTopic()
	}
	if newReq.Meta == nil {
		newReq.Meta = &pb.JobMetadata{}
	}
	if remediation.GetReplacementCapability() != "" {
		newReq.Meta.Capability = remediation.GetReplacementCapability()
	}

	labels := map[string]string{}
	for key, value := range origReq.GetLabels() {
		lower := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(lower, "approval_") || key == bus.LabelBusMsgID {
			continue
		}
		labels[key] = value
	}
	labels["remediation_of"] = origReq.GetJobId()
	if remediation.GetId() != "" {
		labels["remediation_id"] = remediation.GetId()
	}
	for key, value := range remediation.GetAddLabels() {
		if strings.TrimSpace(key) == "" {
			continue
		}
		labels[key] = value
	}
	for _, key := range remediation.GetRemoveLabels() {
		delete(labels, key)
	}
	if len(labels) > 0 {
		newReq.Labels = labels
		newReq.Meta.Labels = labels
	} else {
		newReq.Labels = nil
		newReq.Meta.Labels = nil
	}

	if err := s.jobStore.SetState(r.Context(), newJobID, scheduler.JobStatePending); err != nil {
		http.Error(w, "failed to initialize job state", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetTopic(r.Context(), newJobID, newReq.GetTopic()); err != nil {
		http.Error(w, "failed to set job topic", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetTenant(r.Context(), newJobID, newReq.GetTenantId()); err != nil {
		http.Error(w, "failed to set job tenant", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, newJobID); err != nil {
		http.Error(w, "failed to add job to trace", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetJobMeta(r.Context(), newReq); err != nil {
		http.Error(w, "failed to persist job metadata", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetJobRequest(r.Context(), newReq); err != nil {
		http.Error(w, "failed to persist job request", http.StatusServiceUnavailable)
		return
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: newReq,
		},
	}
	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		http.Error(w, "failed to enqueue job", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": newJobID, "trace_id": traceID})
}

func (s *server) handleSubmitJobHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJobPayloadBytes)

	var req submitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	req.applyDefaults(s.tenant)
	if err := req.validate(s.tenant); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	orgID, err := s.resolveTenant(r, req.OrgId)
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	req.OrgId = orgID
	req.TenantId = orgID
	principalID, err := s.resolvePrincipal(r, req.PrincipalId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" && s.jobStore != nil {
		reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(r.Context(), orgID, key, jobID)
		if err != nil {
			http.Error(w, "idempotency reservation failed", http.StatusInternalServerError)
			return
		}
		if !reserved {
			if existingID == "" {
				existingID, err = s.jobStore.GetJobByIdempotencyKeyScoped(r.Context(), orgID, key)
			}
			if err == nil && existingID != "" {
				traceID, _ := s.jobStore.GetTraceID(r.Context(), existingID)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"job_id":   existingID,
					"trace_id": traceID,
				})
				return
			}
			if err != nil && !errors.Is(err, redis.Nil) {
				logging.Error("api-gateway", "idempotency lookup failed", "error", err)
			}
			http.Error(w, "idempotency key already used", http.StatusConflict)
			return
		}
	}
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.Priority)

	teamID := req.TeamId
	projectID := req.ProjectId

	secretsPresent := secrets.ContainsSecretRefs(req.Prompt) || secrets.ContainsSecretRefs(req.Context)
	if secretsPresent {
		req.RiskTags = appendUniqueTag(req.RiskTags, "secrets")
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels["secrets_present"] = "true"
	}

	memoryID := req.MemoryId
	if memoryID == "" {
		memoryID = deriveMemoryIDFromReq(req.Topic, "", jobID)
	}

	envVars := map[string]string{
		"tenant_id": orgID, // Use OrgId as tenant_id in env for now
	}
	if teamID != "" {
		envVars["team_id"] = teamID
	}
	if projectID != "" {
		envVars["project_id"] = projectID
	}
	if memoryID != "" {
		envVars["memory_id"] = memoryID
	}
	if req.Mode != "" {
		envVars["context_mode"] = req.Mode
	}
	envVars["max_input_tokens"] = fmt.Sprintf("%d", req.MaxInputTokens)
	envVars["max_output_tokens"] = fmt.Sprintf("%d", req.MaxOutputTokens)

	actorID := strings.TrimSpace(req.ActorId)
	if actorID == "" {
		actorID = principalID
	}
	meta := &pb.JobMetadata{
		TenantId:       orgID,
		ActorId:        actorID,
		ActorType:      parseActorType(req.ActorType),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		Capability:     strings.TrimSpace(req.Capability),
		RiskTags:       append([]string{}, req.RiskTags...),
		Requires:       append([]string{}, req.Requires...),
		PackId:         strings.TrimSpace(req.PackId),
	}
	if len(req.Labels) > 0 {
		meta.Labels = req.Labels
	}

	payload := map[string]any{
		"prompt":     req.Prompt,
		"adapter_id": req.AdapterId,
		"priority":   req.Priority,
		"topic":      req.Topic,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  orgID,
	}
	if req.Context != nil {
		payload["context"] = req.Context
	}
	payloadBytes, _ := json.Marshal(payload)
	if s.memStore == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.memStore.PutContext(r.Context(), ctxKey, payloadBytes); err != nil {
		logging.Error("api-gateway", "failed to persist job context", "job_id", jobID, "error", err)
		http.Error(w, "failed to persist job context", http.StatusServiceUnavailable)
		return
	}

	// Set initial state
	if err := s.jobStore.SetState(r.Context(), jobID, scheduler.JobStatePending); err != nil {
		logging.Error("api-gateway", "failed to initialize job state", "job_id", jobID, "error", err)
		http.Error(w, "failed to initialize job state", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetTopic(r.Context(), jobID, req.Topic); err != nil {
		logging.Error("api-gateway", "failed to set job topic", "job_id", jobID, "error", err)
		http.Error(w, "failed to initialize job metadata", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetTenant(r.Context(), jobID, orgID); err != nil {
		logging.Error("api-gateway", "failed to set job tenant", "job_id", jobID, "error", err)
		http.Error(w, "failed to initialize job metadata", http.StatusServiceUnavailable)
		return
	} // Use OrgId here too
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, jobID); err != nil {
		logging.Error("api-gateway", "failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
		http.Error(w, "failed to initialize job metadata", http.StatusServiceUnavailable)
		return
	}

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       req.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   req.AdapterId,
		Env:         envVars,
		MemoryId:    memoryID,
		TenantId:    orgID,       // Use OrgId here
		PrincipalId: principalID, // Populated from new field
		Labels:      req.Labels,
		Meta:        meta,
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
		if err := s.jobStore.SetJobMeta(r.Context(), jobReq); err != nil {
			logging.Error("api-gateway", "failed to persist job metadata", "job_id", jobID, "error", err)
			http.Error(w, "failed to persist job metadata", http.StatusServiceUnavailable)
			return
		}
		if err := s.jobStore.SetJobRequest(r.Context(), jobReq); err != nil {
			logging.Error("api-gateway", "failed to persist job request", "job_id", jobID, "error", err)
			http.Error(w, "failed to persist job metadata", http.StatusServiceUnavailable)
			return
		}
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway-http",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		logging.Error("api-gateway", "job publish failed", "job_id", jobID, "error", err)
		_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStateFailed)
		http.Error(w, "failed to enqueue job", http.StatusServiceUnavailable)
		return
	}

	logging.Info("api-gateway", "job submitted http", "job_id", jobID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
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
	filtered := make([]scheduler.JobRecord, 0, len(jobs))
	for _, job := range jobs {
		if err := s.requireTenantAccess(r, job.Tenant); err != nil {
			continue
		}
		filtered = append(filtered, job)
	}
	// Enrich with details if needed, but for now list is enough
	_ = json.NewEncoder(w).Encode(filtered)
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if err := s.requireRole(r, "admin"); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
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
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isAllowedOrigin(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowedOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients often omit Origin; treat as allowed.
		return true
	}

	allowed, allowAll := allowedOriginsFromEnv()
	if allowAll {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	if len(allowed) == 0 {
		host := strings.ToLower(u.Hostname())
		switch host {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
		reqHost := strings.ToLower(requestHostname(r.Host))
		if reqHost != "" && host == reqHost {
			return true
		}
		return false
	}

	_, ok := allowed[origin]
	return ok
}

func allowedOriginsFromEnv() (map[string]struct{}, bool) {
	for _, key := range []string{
		"CORDUM_ALLOWED_ORIGINS",
		"CORDUM_CORS_ALLOW_ORIGINS",
		"CORS_ALLOW_ORIGINS",
	} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		if raw == "*" {
			return nil, true
		}
		set := make(map[string]struct{})
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			set[p] = struct{}{}
		}
		return set, false
	}
	return nil, false
}

func requestHostname(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil && host != "" {
		return host
	}
	return hostport
}

func normalizeAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	// Common .env mistake: quoting values (e.g. "super-secret-key").
	key = strings.Trim(key, "\"'")
	return strings.TrimSpace(key)
}

func apiKeyFromWebSocket(r *http.Request) string {
	if r == nil {
		return ""
	}
	protocols := websocket.Subprotocols(r)
	for i, protocol := range protocols {
		if strings.EqualFold(protocol, wsAPIKeyProtocol) && i+1 < len(protocols) {
			return decodeWSAPIKey(protocols[i+1])
		}
		prefix := strings.ToLower(wsAPIKeyProtocol) + "."
		if strings.HasPrefix(strings.ToLower(protocol), prefix) {
			token := protocol[len(prefix):]
			return decodeWSAPIKey(token)
		}
	}
	return ""
}

func decodeWSAPIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	return raw
}

func apiKeyUnaryInterceptor(auth AuthProvider) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if auth == nil {
			return handler(ctx, req)
		}
		authCtx, err := auth.AuthenticateGRPC(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		ctx = context.WithValue(ctx, authContextKey{}, authCtx)
		return handler(ctx, req)
	}
}

func addrFromEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func dialSafetyKernel(addr string) (*grpc.ClientConn, pb.SafetyKernelClient, error) {
	if os.Getenv("SAFETY_KERNEL_TLS_REQUIRED") == "true" && strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_CA")) == "" {
		return nil, nil, fmt.Errorf("SAFETY_KERNEL_TLS_CA required")
	}
	creds := safetyTransportCredentials()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewSafetyKernelClient(conn), nil
}

func safetyTransportCredentials() credentials.TransportCredentials {
	if caPath := os.Getenv("SAFETY_KERNEL_TLS_CA"); caPath != "" {
		if creds, err := credentials.NewClientTLSFromFile(caPath, ""); err == nil {
			return creds
		}
	}
	if os.Getenv("SAFETY_KERNEL_INSECURE") == "true" {
		return insecure.NewCredentials()
	}
	return insecure.NewCredentials()
}

func rateLimitMiddleware(next http.Handler) http.Handler {
	if apiLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !apiLimiter.Allow() {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiKeyMiddleware enforces API key auth and injects auth context.
func apiKeyMiddleware(auth AuthProvider, next http.Handler) http.Handler {
	if auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		authCtx, err := auth.AuthenticateHTTP(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
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

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func idempotencyKeyFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	candidates := []string{
		r.Header.Get("Idempotency-Key"),
		r.Header.Get("X-Idempotency-Key"),
		r.URL.Query().Get("idempotency_key"),
		r.URL.Query().Get("idempotency-key"),
	}
	for _, raw := range candidates {
		if val := strings.TrimSpace(raw); val != "" {
			return val
		}
	}
	return ""
}

func (s *server) maxConcurrentRuns(ctx context.Context, orgID, teamID string) int {
	if s.configSvc == nil {
		return 0
	}
	cfg, err := s.configSvc.Effective(ctx, orgID, teamID, "", "")
	if err != nil || cfg == nil {
		return 0
	}
	if limit := lookupIntPath(cfg, "limits", "max_concurrent_runs"); limit > 0 {
		return limit
	}
	if limit := lookupIntPath(cfg, "rate_limits", "concurrent_workflows"); limit > 0 {
		return limit
	}
	return 0
}

func lookupIntPath(data map[string]any, keys ...string) int {
	if data == nil || len(keys) == 0 {
		return 0
	}
	var cur any = data
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[key]
	}
	switch v := cur.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

func parseActorType(raw string) pb.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "human":
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case "service":
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func appendUniqueTag(tags []string, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return tags
	}
	for _, existing := range tags {
		if strings.EqualFold(existing, tag) {
			return tags
		}
	}
	return append(tags, tag)
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
	return "raw"
}

func deriveMemoryIDFromReq(topic, explicit, jobID string) string {
	if explicit != "" {
		return explicit
	}
	return "mem:" + jobID
}

func normalizeTimestampMicrosLower(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts * microsPerSecond
	case ts < millisThreshold:
		return ts * microsPerMillisecond
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampMicrosUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts*microsPerSecond + (microsPerSecond - 1)
	case ts < millisThreshold:
		return ts*microsPerMillisecond + (microsPerMillisecond - 1)
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampSecondsUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts
	case ts < millisThreshold:
		return ts / 1_000
	case ts < microsThreshold:
		return ts / 1_000_000
	default:
		return ts / 1_000_000_000
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
// ---- Workflow REST Handlers ----

type createWorkflowRequest struct {
	ID          string             `json:"id"`
	OrgID       string             `json:"org_id"`
	TeamID      string             `json:"team_id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	TimeoutSec  int64              `json:"timeout_sec"`
	CreatedBy   string             `json:"created_by"`
	InputSchema map[string]any     `json:"input_schema"`
	Parameters  []map[string]any   `json:"parameters"`
	Steps       map[string]wf.Step `json:"steps"`
	Config      map[string]any     `json:"config"`
}

func (s *server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	orgID, err := s.resolveTenant(r, req.OrgID)
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	req.OrgID = orgID

	// Preserve existing fields on upsert for callers that send partial payloads.
	if existing, err := s.workflowStore.GetWorkflow(r.Context(), req.ID); err == nil && existing != nil {
		if existing.OrgID != "" && existing.OrgID != req.OrgID {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
		if req.OrgID == "" {
			req.OrgID = existing.OrgID
		}
		if req.TeamID == "" {
			req.TeamID = existing.TeamID
		}
		if req.Name == "" {
			req.Name = existing.Name
		}
		if req.Description == "" {
			req.Description = existing.Description
		}
		if req.Version == "" {
			req.Version = existing.Version
		}
		if req.TimeoutSec == 0 {
			req.TimeoutSec = existing.TimeoutSec
		}
		if req.CreatedBy == "" {
			req.CreatedBy = existing.CreatedBy
		}
		if req.InputSchema == nil && existing.InputSchema != nil {
			req.InputSchema = existing.InputSchema
		}
		if req.Parameters == nil && existing.Parameters != nil {
			req.Parameters = existing.Parameters
		}
		if req.Config == nil && existing.Config != nil {
			req.Config = existing.Config
		}
	}

	wfDef := &wf.Workflow{
		ID:          req.ID,
		OrgID:       req.OrgID,
		TeamID:      req.TeamID,
		Name:        req.Name,
		Description: req.Description,
		Version:     req.Version,
		TimeoutSec:  req.TimeoutSec,
		Config:      req.Config,
		InputSchema: req.InputSchema,
		Parameters:  req.Parameters,
		CreatedBy:   req.CreatedBy,
		Steps:       map[string]*wf.Step{},
	}
	for id, step := range req.Steps {
		s := step
		s.ID = id
		wfDef.Steps[id] = &s
	}
	if err := s.workflowStore.SaveWorkflow(r.Context(), wfDef); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": wfDef.ID})
}

func (s *server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wfDef)
}

func (s *server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), id); err == nil && wfDef != nil {
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	if err := s.workflowStore.DeleteWorkflow(r.Context(), id); err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	list, err := s.workflowStore.ListWorkflows(r.Context(), orgID, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		http.Error(w, "missing workflow id", http.StatusBadRequest)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wfDef != nil {
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	if wfDef != nil && len(wfDef.InputSchema) > 0 {
		if err := schema.ValidateMap(wfDef.InputSchema, payload); err != nil {
			http.Error(w, fmt.Sprintf("input schema validation failed: %v", err), http.StatusBadRequest)
			return
		}
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	if wfDef != nil && wfDef.OrgID != "" && orgID != "" && orgID != wfDef.OrgID {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	if orgID == "" && wfDef != nil {
		orgID = wfDef.OrgID
	}
	teamID := r.URL.Query().Get("team_id")
	dryRun := parseBool(r.URL.Query().Get("dry_run"))
	idempotencyKey := idempotencyKeyFromRequest(r)
	if idempotencyKey != "" {
		if existingID, err := s.workflowStore.GetRunByIdempotencyKey(r.Context(), idempotencyKey); err == nil && existingID != "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": existingID})
			return
		} else if err != nil && !errors.Is(err, redis.Nil) {
			logging.Error("api-gateway", "run idempotency lookup failed", "error", err)
		}
	}
	runID := uuid.NewString()
	reservedKey := false
	if idempotencyKey != "" {
		ok, err := s.workflowStore.TrySetRunIdempotencyKey(r.Context(), idempotencyKey, runID)
		if err != nil {
			http.Error(w, "idempotency reservation failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			if existingID, err := s.workflowStore.GetRunByIdempotencyKey(r.Context(), idempotencyKey); err == nil && existingID != "" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"run_id": existingID})
				return
			}
			http.Error(w, "idempotency key already used", http.StatusConflict)
			return
		}
		reservedKey = true
	}
	if limit := s.maxConcurrentRuns(r.Context(), orgID, teamID); limit > 0 {
		if count, err := s.workflowStore.CountActiveRuns(r.Context(), orgID); err == nil && count >= limit {
			http.Error(w, "max concurrent runs reached", http.StatusTooManyRequests)
			return
		}
	}
	run := &wf.WorkflowRun{
		ID:             runID,
		WorkflowID:     wfID,
		OrgID:          orgID,
		TeamID:         teamID,
		Input:          payload,
		Status:         wf.RunStatusPending,
		Steps:          map[string]*wf.StepRun{},
		DryRun:         dryRun,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		IdempotencyKey: idempotencyKey,
	}
	if dryRun {
		run.Metadata = map[string]string{"dry_run": "true"}
		run.Labels = map[string]string{"dry_run": "true"}
	}
	if err := s.workflowStore.CreateRun(r.Context(), run); err != nil {
		if reservedKey && idempotencyKey != "" {
			_ = s.workflowStore.DeleteRunIdempotencyKey(r.Context(), idempotencyKey)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Kick off execution
	if s.workflowEng != nil {
		if s.jobStore != nil {
			lockKey := "cordum:wf:run:lock:" + runID
			ok, err := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
			if err != nil {
				_ = s.workflowEng.StartRun(r.Context(), wfID, runID)
			} else if ok {
				_ = s.workflowEng.StartRun(r.Context(), wfID, runID)
				_ = s.jobStore.ReleaseLock(r.Context(), lockKey)
			}
		} else {
			_ = s.workflowEng.StartRun(r.Context(), wfID, runID)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"run_id": runID})
}

type rerunRequest struct {
	FromStep string `json:"from_step"`
	DryRun   bool   `json:"dry_run"`
}

func (s *server) handleRerunRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowEng == nil || s.workflowStore == nil {
		http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	origRun, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil || origRun == nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err := s.requireTenantAccess(r, origRun.OrgID); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	if limit := s.maxConcurrentRuns(r.Context(), origRun.OrgID, origRun.TeamID); limit > 0 {
		if count, err := s.workflowStore.CountActiveRuns(r.Context(), origRun.OrgID); err == nil && count >= limit {
			http.Error(w, "max concurrent runs reached", http.StatusTooManyRequests)
			return
		}
	}
	var req rerunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	newID, err := s.workflowEng.RerunFrom(r.Context(), runID, strings.TrimSpace(req.FromStep), req.DryRun)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	newRun, err := s.workflowStore.GetRun(r.Context(), newID)
	if err != nil || newRun == nil {
		http.Error(w, "new run not found", http.StatusInternalServerError)
		return
	}
	wfID := newRun.WorkflowID
	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + newID
		ok, err := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
		if err != nil {
			_ = s.workflowEng.StartRun(r.Context(), wfID, newID)
		} else if ok {
			_ = s.workflowEng.StartRun(r.Context(), wfID, newID)
			_ = s.jobStore.ReleaseLock(r.Context(), lockKey)
		}
	} else {
		_ = s.workflowEng.StartRun(r.Context(), wfID, newID)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"run_id": newID})
}

// Config handlers
type configUpsertRequest struct {
	Scope   string            `json:"scope"`
	ScopeID string            `json:"scope_id"`
	Data    map[string]any    `json:"data"`
	Meta    map[string]string `json:"meta,omitempty"`
}

func (s *server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req configUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	scope := configsvc.Scope(req.Scope)
	if scope == configsvc.ScopeSystem {
		if err := s.requireRole(r, "admin"); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	if scope == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, req.ScopeID)
		if err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
		req.ScopeID = tenant
	}
	doc := &configsvc.Document{
		Scope:   scope,
		ScopeID: req.ScopeID,
		Data:    req.Data,
		Meta:    req.Meta,
	}
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = string(configsvc.ScopeSystem)
	}
	scopeID := strings.TrimSpace(r.URL.Query().Get("scope_id"))
	if scope == string(configsvc.ScopeSystem) && scopeID == "" {
		scopeID = "default"
	}
	if configsvc.Scope(scope) == configsvc.ScopeSystem {
		if err := s.requireRole(r, "admin"); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	if configsvc.Scope(scope) == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, scopeID)
		if err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
		scopeID = tenant
	}
	doc, err := s.configSvc.Get(r.Context(), configsvc.Scope(scope), scopeID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "config not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (s *server) handleGetEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	teamID := r.URL.Query().Get("team_id")
	wfID := r.URL.Query().Get("workflow_id")
	stepID := r.URL.Query().Get("step_id")

	snap, err := s.configSvc.EffectiveSnapshot(r.Context(), orgID, teamID, wfID, stepID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if snap == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{})
		return
	}
	_ = json.NewEncoder(w).Encode(snap)
}

// Schema handlers
type schemaRegisterRequest struct {
	ID     string         `json:"id"`
	Schema map[string]any `json:"schema"`
}

func (s *server) handleRegisterSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		http.Error(w, "schema registry unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req schemaRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	data, err := json.Marshal(req.Schema)
	if err != nil {
		http.Error(w, "invalid schema", http.StatusBadRequest)
		return
	}
	if err := s.schemaRegistry.Register(r.Context(), req.ID, data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		http.Error(w, "schema registry unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := int64(100)
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	ids, err := s.schemaRegistry.List(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"schemas": ids})
}

func (s *server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		http.Error(w, "schema registry unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "schema id required", http.StatusBadRequest)
		return
	}
	data, err := s.schemaRegistry.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "schema not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		http.Error(w, "failed to decode schema", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "schema": payload})
}

func (s *server) handleDeleteSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		http.Error(w, "schema registry unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "schema id required", http.StatusBadRequest)
		return
	}
	if err := s.schemaRegistry.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Resource lock handlers
type lockRequest struct {
	Resource string `json:"resource"`
	Owner    string `json:"owner"`
	Mode     string `json:"mode"`
	TTLms    int64  `json:"ttl_ms"`
}

func (s *server) handleGetLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	if resource == "" {
		http.Error(w, "resource required", http.StatusBadRequest)
		return
	}
	lock, err := s.lockStore.Get(r.Context(), resource)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "lock not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(lock)
}

func (s *server) handleAcquireLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	mode := parseLockMode(req.Mode)
	lock, ok, err := s.lockStore.Acquire(r.Context(), req.Resource, req.Owner, mode, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "lock unavailable", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(lock)
}

func (s *server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	lock, ok, err := s.lockStore.Release(r.Context(), req.Resource, req.Owner)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "lock not held", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"lock": lock, "released": true})
}

func (s *server) handleRenewLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		http.Error(w, "lock store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	lock, ok, err := s.lockStore.Renew(r.Context(), req.Resource, req.Owner, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "lock not held", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(lock)
}

func parseLockMode(raw string) locks.Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared":
		return locks.ModeShared
	default:
		return locks.ModeExclusive
	}
}

func parseRetention(raw string) artifacts.RetentionClass {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short":
		return artifacts.RetentionShort
	case "audit":
		return artifacts.RetentionAudit
	default:
		return artifacts.RetentionStandard
	}
}

func (s *server) handlePolicyEvaluate(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "evaluate")
}

func (s *server) handlePolicySimulate(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "simulate")
}

func (s *server) handlePolicyExplain(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "explain")
}

func (s *server) handlePolicySnapshots(w http.ResponseWriter, r *http.Request) {
	if s.safetyClient == nil {
		http.Error(w, "safety kernel unavailable", http.StatusServiceUnavailable)
		return
	}
	resp, err := s.safetyClient.ListSnapshots(r.Context(), &pb.ListSnapshotsRequest{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *server) handlePolicyCheck(w http.ResponseWriter, r *http.Request, mode string) {
	if s.safetyClient == nil {
		http.Error(w, "safety kernel unavailable", http.StatusServiceUnavailable)
		return
	}
	var req policyCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	tenant, err := s.resolveTenant(r, req.Tenant)
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	req.Tenant = tenant
	req.OrgId = tenant
	principalID, err := s.resolvePrincipal(r, req.PrincipalId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	req.PrincipalId = principalID
	if req.Meta != nil {
		req.Meta.TenantId = tenant
	}
	checkReq, err := buildPolicyCheckRequest(r.Context(), &req, s.configSvc, s.tenant)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var resp *pb.PolicyCheckResponse
	switch mode {
	case "simulate":
		resp, err = s.safetyClient.Simulate(r.Context(), checkReq)
	case "explain":
		resp, err = s.safetyClient.Explain(r.Context(), checkReq)
	default:
		resp, err = s.safetyClient.Evaluate(r.Context(), checkReq)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// DLQ handlers
func (s *server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		http.Error(w, "dlq store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	entries, err := s.dlqStore.List(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.jobStore != nil {
		filtered := make([]memory.DLQEntry, 0, len(entries))
		for _, entry := range entries {
			tenant, _ := s.jobStore.GetTenant(r.Context(), entry.JobID)
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

func (s *server) handleListDLQPage(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		http.Error(w, "dlq store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	entries, err := s.dlqStore.ListByScore(r.Context(), cursor, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.jobStore != nil {
		filtered := make([]memory.DLQEntry, 0, len(entries))
		for _, entry := range entries {
			tenant, _ := s.jobStore.GetTenant(r.Context(), entry.JobID)
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}
	var nextCursor *int64
	if int64(len(entries)) == limit {
		last := entries[len(entries)-1]
		if !last.CreatedAt.IsZero() {
			nc := last.CreatedAt.Unix() - 1
			nextCursor = &nc
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       entries,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleDeleteDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		http.Error(w, "dlq store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	if s.jobStore != nil {
		if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				http.Error(w, "tenant access denied", http.StatusForbidden)
				return
			}
		}
	}
	if err := s.dlqStore.Delete(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRetryDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil || s.jobStore == nil || s.memStore == nil {
		http.Error(w, "dlq, job, or memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	entry, err := s.dlqStore.Get(r.Context(), jobID)
	if err != nil {
		http.Error(w, "dlq entry not found", http.StatusNotFound)
		return
	}
	topic := entry.Topic
	if topic == "" {
		if t, err := s.jobStore.GetTopic(r.Context(), jobID); err == nil {
			topic = t
		}
	}
	if topic == "" {
		http.Error(w, "missing topic for retry", http.StatusBadRequest)
		return
	}
	newJobID := jobID + "-retry-" + uuid.NewString()[:8]
	traceID := "dlq-retry-" + jobID
	var ctxPtr string
	origCtxKey := memory.MakeContextKey(jobID)
	if data, err := s.memStore.GetContext(r.Context(), origCtxKey); err == nil {
		newCtxKey := memory.MakeContextKey(newJobID)
		if err := s.memStore.PutContext(r.Context(), newCtxKey, data); err == nil {
			ctxPtr = memory.PointerForKey(newCtxKey)
		}
	}

	tenant, _ := s.jobStore.GetTenant(r.Context(), jobID)
	team, _ := s.jobStore.GetTeam(r.Context(), jobID)
	principal, _ := s.jobStore.GetPrincipal(r.Context(), jobID)

	jobReq := &pb.JobRequest{
		JobId:       newJobID,
		Topic:       topic,
		ContextPtr:  ctxPtr,
		TenantId:    tenant,
		PrincipalId: principal,
		Env: map[string]string{
			"tenant_id":    tenant,
			"team_id":      team,
			"retry_of_job": jobID,
		},
		Labels: map[string]string{
			"retry":        "true",
			"dlq_entry":    jobID,
			"retry_of_job": jobID,
		},
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.jobStore.SetJobMeta(r.Context(), jobReq); err != nil {
		http.Error(w, "failed to persist job metadata", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, newJobID); err != nil {
		http.Error(w, "failed to persist trace metadata", http.StatusServiceUnavailable)
		return
	}
	if err := s.jobStore.SetState(r.Context(), newJobID, scheduler.JobStatePending); err != nil {
		http.Error(w, "failed to initialize job state", http.StatusServiceUnavailable)
		return
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		http.Error(w, fmt.Sprintf("publish failed: %v", err), http.StatusInternalServerError)
		return
	}

	_ = s.dlqStore.Delete(r.Context(), jobID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": newJobID})
}

func (s *server) handleApproveStep(w http.ResponseWriter, r *http.Request) {
	if s.workflowEng == nil {
		http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	wfID := r.PathValue("id")
	runID := r.PathValue("run_id")
	stepID := r.PathValue("step_id")
	if wfID == "" || runID == "" || stepID == "" {
		http.Error(w, "missing identifiers", http.StatusBadRequest)
		return
	}
	if s.workflowStore != nil {
		if run, err := s.workflowStore.GetRun(r.Context(), runID); err == nil && run != nil {
			if err := s.requireTenantAccess(r, run.OrgID); err != nil {
				http.Error(w, "tenant access denied", http.StatusForbidden)
				return
			}
		}
	}

	// Serialize workflow run mutations with the same lock used by the workflow-engine reconciler.
	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		ok, err := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
		if err != nil || !ok {
			http.Error(w, "workflow run is busy, retry", http.StatusConflict)
			return
		}
		defer func() { _ = s.jobStore.ReleaseLock(context.Background(), lockKey) }()
	}

	var body struct {
		Approved bool `json:"approved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.workflowEng.ApproveStep(r.Context(), runID, stepID, body.Approved); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowEng == nil {
		http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	runID := r.PathValue("run_id")
	if runID == "" {
		http.Error(w, "missing run_id", http.StatusBadRequest)
		return
	}
	if s.workflowStore != nil {
		if run, err := s.workflowStore.GetRun(r.Context(), runID); err == nil && run != nil {
			if err := s.requireTenantAccess(r, run.OrgID); err != nil {
				http.Error(w, "tenant access denied", http.StatusForbidden)
				return
			}
		}
	}

	// Serialize workflow run mutations with the same lock used by the workflow-engine reconciler.
	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		ok, err := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
		if err != nil || !ok {
			http.Error(w, "workflow run is busy, retry", http.StatusConflict)
			return
		}
		defer func() { _ = s.jobStore.ReleaseLock(context.Background(), lockKey) }()
	}

	if err := s.workflowEng.CancelRun(r.Context(), runID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		http.Error(w, "job store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	cursor := time.Now().UnixNano() / int64(time.Microsecond)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	jobs, err := s.jobStore.ListJobsByState(r.Context(), scheduler.JobStateApproval, cursor, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		if err := s.requireTenantAccess(r, job.Tenant); err != nil {
			continue
		}
		record, _ := s.jobStore.GetSafetyDecision(r.Context(), job.ID)
		items = append(items, map[string]any{
			"job":               job,
			"decision":          record.Decision,
			"policy_snapshot":   record.PolicySnapshot,
			"policy_rule_id":    record.RuleID,
			"policy_reason":     record.Reason,
			"constraints":       record.Constraints,
			"job_hash":          record.JobHash,
			"approval_required": record.ApprovalRequired,
			"approval_ref":      record.ApprovalRef,
		})
	}
	var nextCursor *int64
	if int64(len(jobs)) == limit {
		nc := jobs[len(jobs)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleApproveJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil {
		http.Error(w, "job store or bus unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	state, err := s.jobStore.GetState(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if state != scheduler.JobStateApproval {
		http.Error(w, "job not awaiting approval", http.StatusConflict)
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	req, err := s.jobStore.GetJobRequest(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job request not found", http.StatusNotFound)
		return
	}
	safetyRecord, err := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	if err != nil {
		http.Error(w, "safety decision unavailable", http.StatusServiceUnavailable)
		return
	}
	if !safetyRecord.ApprovalRequired && safetyRecord.Decision != scheduler.SafetyRequireApproval {
		http.Error(w, "job not awaiting approval", http.StatusConflict)
		return
	}
	if safetyRecord.JobHash == "" {
		http.Error(w, "approval job hash unavailable", http.StatusConflict)
		return
	}
	if safetyRecord.PolicySnapshot == "" {
		http.Error(w, "approval policy snapshot unavailable", http.StatusConflict)
		return
	}
	if s.safetyClient == nil {
		http.Error(w, "safety kernel unavailable", http.StatusServiceUnavailable)
		return
	}
	snapResp, err := s.safetyClient.ListSnapshots(r.Context(), &pb.ListSnapshotsRequest{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	currentSnapshot := ""
	if snapResp != nil && len(snapResp.Snapshots) > 0 {
		currentSnapshot = strings.TrimSpace(snapResp.Snapshots[0])
	}
	if currentSnapshot == "" || currentSnapshot != safetyRecord.PolicySnapshot {
		http.Error(w, "policy snapshot changed; re-evaluate before approving", http.StatusConflict)
		return
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		http.Error(w, "failed to hash job request", http.StatusInternalServerError)
		return
	}
	if hash != safetyRecord.JobHash {
		http.Error(w, "job request changed; approval rejected", http.StatusConflict)
		return
	}
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	req.Labels["approval_granted"] = "true"
	reason := strings.TrimSpace(body.Reason)
	note := strings.TrimSpace(body.Note)
	if reason != "" {
		req.Labels["approval_reason"] = reason
	}
	if note != "" {
		req.Labels["approval_note"] = note
	}
	req.Labels[bus.LabelBusMsgID] = "approval:" + uuid.NewString()
	if err := s.jobStore.SetJobRequest(r.Context(), req); err != nil {
		http.Error(w, "failed to persist approval request", http.StatusInternalServerError)
		return
	}
	approvedBy := strings.TrimSpace(policyActorID(r))
	if approvedBy == "" {
		approvedBy = strings.TrimSpace(req.PrincipalId)
	}
	approvalRole := strings.TrimSpace(policyRole(r))
	if err := s.jobStore.SetApprovalRecord(r.Context(), jobID, memory.ApprovalRecord{
		ApprovedBy:     approvedBy,
		ApprovedRole:   approvalRole,
		Reason:         reason,
		Note:           note,
		PolicySnapshot: safetyRecord.PolicySnapshot,
		JobHash:        safetyRecord.JobHash,
	}); err != nil {
		http.Error(w, "failed to persist approval record", http.StatusInternalServerError)
		return
	}
	if err := s.jobStore.SetState(r.Context(), jobID, scheduler.JobStatePending); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}
	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": jobID, "trace_id": traceID})
}

func (s *server) handleRejectJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil {
		http.Error(w, "job store or bus unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	state, err := s.jobStore.GetState(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if state != scheduler.JobStateApproval {
		http.Error(w, "job not awaiting approval", http.StatusConflict)
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	var principalID string
	if req, err := s.jobStore.GetJobRequest(r.Context(), jobID); err == nil && req != nil {
		principalID = strings.TrimSpace(req.PrincipalId)
	}
	safetyRecord, _ := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	reason := strings.TrimSpace(body.Reason)
	note := strings.TrimSpace(body.Note)
	approvedBy := strings.TrimSpace(policyActorID(r))
	if approvedBy == "" {
		approvedBy = principalID
	}
	approvalRole := strings.TrimSpace(policyRole(r))
	if err := s.jobStore.SetApprovalRecord(r.Context(), jobID, memory.ApprovalRecord{
		ApprovedBy:     approvedBy,
		ApprovedRole:   approvalRole,
		Reason:         reason,
		Note:           note,
		PolicySnapshot: safetyRecord.PolicySnapshot,
		JobHash:        safetyRecord.JobHash,
	}); err != nil {
		http.Error(w, "failed to persist approval record", http.StatusInternalServerError)
		return
	}
	if err := s.jobStore.SetState(r.Context(), jobID, scheduler.JobStateDenied); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	errorMessage := "approval rejected"
	if reason != "" {
		errorMessage = reason
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:        jobID,
				Status:       pb.JobStatus_JOB_STATUS_DENIED,
				ErrorCode:    "approval_rejected",
				ErrorMessage: errorMessage,
			},
		},
	}
	_ = s.bus.Publish(capsdk.SubjectDLQ, packet)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
}

func (s *server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		http.Error(w, "missing workflow id", http.StatusBadRequest)
		return
	}
	if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID); err == nil && wfDef != nil {
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	runs, err := s.workflowStore.ListRunsByWorkflow(r.Context(), wfID, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runs)
}

func (s *server) handleListAllRuns(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	workflowFilter := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
	orgFilter := strings.TrimSpace(r.URL.Query().Get("org_id"))
	teamFilter := strings.TrimSpace(r.URL.Query().Get("team_id"))
	updatedAfter := int64(0)
	if q := r.URL.Query().Get("updated_after"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedAfter = v
		}
	}
	updatedBefore := int64(0)
	if q := r.URL.Query().Get("updated_before"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedBefore = v
		}
	}

	cursor = normalizeTimestampSecondsUpper(cursor)
	updatedAfter = normalizeTimestampSecondsUpper(updatedAfter)
	updatedBefore = normalizeTimestampSecondsUpper(updatedBefore)

	resolvedOrg, err := s.resolveTenant(r, orgFilter)
	if err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	orgFilter = resolvedOrg

	runs, err := s.workflowStore.ListRuns(r.Context(), cursor, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filtered := make([]*wf.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		if run == nil {
			continue
		}
		if statusFilter != "" && string(run.Status) != statusFilter {
			continue
		}
		if workflowFilter != "" && run.WorkflowID != workflowFilter {
			continue
		}
		if orgFilter != "" && run.OrgID != orgFilter {
			continue
		}
		if teamFilter != "" && run.TeamID != teamFilter {
			continue
		}
		updatedAt := run.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = run.CreatedAt
		}
		if updatedAfter > 0 && updatedAt.Unix() < updatedAfter {
			continue
		}
		if updatedBefore > 0 && updatedAt.Unix() > updatedBefore {
			continue
		}
		filtered = append(filtered, run)
	}
	var nextCursor *int64
	if int64(len(runs)) == limit {
		last := runs[len(runs)-1]
		if last != nil {
			ts := last.UpdatedAt
			if ts.IsZero() {
				ts = last.CreatedAt
			}
			if !ts.IsZero() {
				nc := ts.Unix() - 1
				nextCursor = &nc
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       filtered,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(run)
}

func (s *server) handleGetRunTimeline(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	if run, err := s.workflowStore.GetRun(r.Context(), id); err == nil && run != nil {
		if err := s.requireTenantAccess(r, run.OrgID); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	limit := int64(200)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	events, err := s.workflowStore.ListTimelineEvents(r.Context(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

func (s *server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if run, err := s.workflowStore.GetRun(r.Context(), id); err == nil && run != nil {
		if err := s.requireTenantAccess(r, run.OrgID); err != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
	}
	if err := s.workflowStore.DeleteRun(r.Context(), id); err != nil {
		if errors.Is(err, redis.Nil) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	// The incoming gRPC request (req) directly contains the new identity fields.
	// We'll use them to populate the pb.JobRequest.

	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}

	// Use tenant from auth context when available.
	orgID := req.GetOrgId()
	principalID := req.GetPrincipalId()
	if auth := authFromContext(ctx); auth != nil {
		if auth.Tenant != "" {
			orgID = auth.Tenant
		}
		if auth.PrincipalID != "" {
			principalID = auth.PrincipalID
		}
	}
	if orgID == "" {
		orgID = s.tenant
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	if key := strings.TrimSpace(req.GetIdempotencyKey()); key != "" && s.jobStore != nil {
		reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(ctx, orgID, key, jobID)
		if err != nil {
			return nil, status.Error(codes.Internal, "idempotency reservation failed")
		}
		if !reserved {
			if existingID == "" {
				existingID, err = s.jobStore.GetJobByIdempotencyKeyScoped(ctx, orgID, key)
			}
			if err == nil && existingID != "" {
				traceID, _ := s.jobStore.GetTraceID(ctx, existingID)
				return &pb.SubmitJobResponse{JobId: existingID, TraceId: traceID}, nil
			}
			if err != nil && !errors.Is(err, redis.Nil) {
				logging.Error("api-gateway", "idempotency lookup failed", "error", err)
			}
			return nil, status.Error(codes.AlreadyExists, "idempotency key already used")
		}
	}

	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.GetPriority())

	payloadReq := submitJobRequest{
		Prompt:         req.GetPrompt(),
		Topic:          req.GetTopic(),
		AdapterId:      req.GetAdapterId(),
		Priority:       req.GetPriority(),
		TenantId:       orgID, // Use OrgId for TenantId in payloadReq
		PrincipalId:    principalID,
		OrgId:          orgID,
		ActorId:        req.GetActorId(),
		ActorType:      req.GetActorType(),
		IdempotencyKey: req.GetIdempotencyKey(),
		PackId:         req.GetPackId(),
		Capability:     req.GetCapability(),
		RiskTags:       req.GetRiskTags(),
		Requires:       req.GetRequires(),
		Labels:         req.GetLabels(),
		MemoryId:       req.GetMemoryId(),
		// SubmitJobRequest does not carry budget limits yet; defaults are applied below.
	}
	// For gRPC, validation of basic fields like prompt, topic happens earlier via protobuf definition
	// For complex validation rules, we can still use a simplified applyDefaults and validate for payloadReq.
	payloadReq.applyDefaults(s.tenant)
	// Basic validation, primarily for prompt length and topic prefix
	if err := payloadReq.validate(s.tenant); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	payload := map[string]any{
		"prompt":     payloadReq.Prompt,
		"adapter_id": payloadReq.AdapterId,
		"priority":   payloadReq.Priority,
		"topic":      payloadReq.Topic,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  orgID, // Use OrgId here
	}
	// Context is not directly passed in SubmitJobRequest, but could be added
	payloadBytes, _ := json.Marshal(payload)
	if s.memStore == nil {
		return nil, status.Error(codes.Unavailable, "memory store unavailable")
	}
	if err := s.memStore.PutContext(ctx, ctxKey, payloadBytes); err != nil {
		logging.Error("api-gateway", "failed to persist job context", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to persist job context")
	}

	// Set initial state
	if err := s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		logging.Error("api-gateway", "failed to initialize job state", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job state")
	}
	if err := s.jobStore.SetTopic(ctx, jobID, payloadReq.Topic); err != nil {
		logging.Error("api-gateway", "failed to set job topic", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	}
	if err := s.jobStore.SetTenant(ctx, jobID, orgID); err != nil {
		logging.Error("api-gateway", "failed to set job tenant", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	} // Use OrgId here

	secretsPresent := secrets.ContainsSecretRefs(payloadReq.Prompt)
	if secretsPresent {
		payloadReq.RiskTags = appendUniqueTag(payloadReq.RiskTags, "secrets")
		if payloadReq.Labels == nil {
			payloadReq.Labels = map[string]string{}
		}
		payloadReq.Labels["secrets_present"] = "true"
	}

	maxInput := int64(8000)
	maxOutput := int64(1024)
	memoryID := payloadReq.MemoryId
	if memoryID == "" {
		memoryID = deriveMemoryIDFromReq(payloadReq.Topic, "", jobID)
	}
	envVars := map[string]string{
		"tenant_id":         orgID,
		"memory_id":         memoryID,
		"context_mode":      "",
		"max_input_tokens":  fmt.Sprintf("%d", maxInput),
		"max_output_tokens": fmt.Sprintf("%d", maxOutput),
	}
	if team := req.GetTeamId(); team != "" {
		envVars["team_id"] = team
	}
	if project := req.GetProjectId(); project != "" {
		envVars["project_id"] = project
	}
	if mode := parseContextMode(payloadReq.Topic, ""); mode != "" {
		envVars["context_mode"] = mode
	}

	actorID := strings.TrimSpace(payloadReq.ActorId)
	if actorID == "" {
		actorID = principalID
	}
	meta := &pb.JobMetadata{
		TenantId:       orgID,
		ActorId:        actorID,
		ActorType:      parseActorType(payloadReq.ActorType),
		IdempotencyKey: strings.TrimSpace(payloadReq.IdempotencyKey),
		Capability:     strings.TrimSpace(payloadReq.Capability),
		RiskTags:       append([]string{}, payloadReq.RiskTags...),
		Requires:       append([]string{}, payloadReq.Requires...),
		PackId:         strings.TrimSpace(payloadReq.PackId),
	}
	if len(payloadReq.Labels) > 0 {
		meta.Labels = payloadReq.Labels
	}

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       payloadReq.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   payloadReq.AdapterId,
		Env:         envVars,
		MemoryId:    memoryID,
		TenantId:    orgID,       // Use OrgId here
		PrincipalId: principalID, // Populated from new field
		Labels:      payloadReq.Labels,
		Meta:        meta,
		ContextHints: &pb.ContextHints{
			MaxInputTokens:     int32(maxInput),
			AllowSummarization: false,
			AllowRetrieval:     false,
			Tags:               nil,
		},
		Budget: &pb.Budget{
			MaxInputTokens:  maxInput,
			MaxOutputTokens: maxOutput,
			MaxTotalTokens:  0,
			DeadlineMs:      0,
		},
	}

	if s.jobStore != nil {
		if err := s.jobStore.SetJobMeta(ctx, jobReq); err != nil {
			logging.Error("api-gateway", "failed to persist job metadata", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.SetJobRequest(ctx, jobReq); err != nil {
			logging.Error("api-gateway", "failed to persist job request", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
			logging.Error("api-gateway", "failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist trace metadata")
		}
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		_ = s.jobStore.SetState(ctx, jobID, scheduler.JobStateFailed)
		logging.Error("api-gateway", "job publish failed", "job_id", jobID, "error", err)
		return nil, status.Errorf(codes.Unavailable, "failed to enqueue job")
	}

	logging.Info("api-gateway", "job submitted", "job_id", jobID)
	return &pb.SubmitJobResponse{JobId: jobID, TraceId: traceID}, nil
}

func (s *server) GetJobStatus(ctx context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	if auth := authFromContext(ctx); auth != nil && auth.Tenant != "" && s.jobStore != nil {
		if tenant, _ := s.jobStore.GetTenant(ctx, req.GetJobId()); tenant != "" && tenant != auth.Tenant && !auth.AllowCrossTenant {
			return nil, status.Error(codes.PermissionDenied, "tenant access denied")
		}
	}
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
