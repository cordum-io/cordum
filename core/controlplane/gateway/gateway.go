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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/configsvc"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	infraMetrics "github.com/yaront1111/coretex-os/core/infra/metrics"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	wf "github.com/yaront1111/coretex-os/core/workflow"
)

const (
	defaultGrpcAddr       = ":8080"
	defaultHttpAddr       = ":8081"
	defaultMetricsAddr    = ":9092"
	maxJobPayloadBytes    = 2 << 20 // 2 MiB limit for incoming job payloads
	maxPromptChars        = 100000
	defaultRateLimitRPS   = 50
	defaultRateLimitBurst = 100
)

const (
	envGatewayGrpcAddr    = "GATEWAY_GRPC_ADDR"
	envGatewayHTTPAddr    = "GATEWAY_HTTP_ADDR"
	envGatewayMetricsAddr = "GATEWAY_METRICS_ADDR"
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
	started time.Time

	workflowStore *wf.RedisStore
	workflowEng   *wf.Engine
	configSvc     *configsvc.Service
	dlqStore      *memory.DLQStore
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
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

func (r *submitJobRequest) applyDefaults(defaultTenant string) {
	if r.MaxInputTokens == 0 {
		r.MaxInputTokens = 8000
	}
	if r.MaxOutputTokens == 0 {
		r.MaxOutputTokens = 1024
	}
	if r.Topic == "" {
		r.Topic = "job.chat.simple"
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

type repoReviewRequest struct {
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

func (r *repoReviewRequest) validate() error {
	if r == nil {
		return errors.New("request required")
	}
	if r.RepoURL == "" && r.LocalPath == "" {
		return errors.New("repo_url or local_path required")
	}
	if r.MaxFiles < 0 || r.BatchSize < 0 || r.MaxBatches < 0 {
		return errors.New("limits must be non-negative")
	}
	return nil
}

func Run(cfg *config.Config) error {
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

	gwMetrics := infraMetrics.NewGatewayProm("coretex_api_gateway")

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
	workflowEng = workflowEng.WithMemory(memStore).WithConfig(configSvc)

	if err := ensureRepoCodeReviewWorkflow(context.Background(), workflowStore, tenantID); err != nil {
		logging.Error("api-gateway", "failed to seed repo code review workflow", "error", err)
	}
	if err := ensureCodeReviewPatchWorkflow(context.Background(), workflowStore, tenantID); err != nil {
		logging.Error("api-gateway", "failed to seed code review patch workflow", "error", err)
	}

	dlqStore, err := memory.NewDLQStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis dlq store: %w", err)
	}
	defer dlqStore.Close()

	s := &server{
		memStore:      memStore,
		jobStore:      jobStore,
		bus:           natsBus,
		workers:       make(map[string]*pb.Heartbeat),
		clients:       make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh:      make(chan *pb.BusPacket, 512),
		metrics:       gwMetrics,
		tenant:        tenantID,
		started:       time.Now().UTC(),
		workflowStore: workflowStore,
		workflowEng:   workflowEng,
		configSvc:     configSvc,
		dlqStore:      dlqStore,
	}

	s.startBusTaps()

	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc (%s): %w", grpcAddr, err)
	}
	serverOpts := []grpc.ServerOption{grpc.Creds(insecure.NewCredentials())}
	if certFile := os.Getenv("GRPC_TLS_CERT"); certFile != "" {
		keyFile := os.Getenv("GRPC_TLS_KEY")
		if keyFile == "" {
			logging.Error("api-gateway", "grpc tls key missing", "cert", certFile)
		} else if creds, err := credentials.NewServerTLSFromFile(certFile, keyFile); err != nil {
			logging.Error("api-gateway", "grpc tls setup failed", "error", err)
		} else {
			serverOpts = []grpc.ServerOption{grpc.Creds(creds)}
		}
	}

	grpcServer := grpc.NewServer(serverOpts...)
	pb.RegisterCoretexApiServer(grpcServer, s)
	reflection.Register(grpcServer)

	go func() {
		logging.Info("api-gateway", "grpc listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			logging.Error("api-gateway", "grpc server error", "error", err)
		}
	}()

	return startHTTPServer(s, httpAddr, metricsAddr)
}

func ensureRepoCodeReviewWorkflow(ctx context.Context, store *wf.RedisStore, orgID string) error {
	if store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	const workflowID = "repo_code_review"
	_, err := store.GetWorkflow(ctx, workflowID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, redis.Nil) {
		return err
	}
	def := &wf.Workflow{
		ID:          workflowID,
		OrgID:       orgID,
		Name:        "Repo Code Review",
		Description: "Runs the repo code review pipeline (scan + SAST + partition + lint + optional tests + report).",
		Version:     "1",
		TimeoutSec:  3600,
		CreatedBy:   "system",
		Steps: map[string]*wf.Step{
			"code_review": {
				ID:         "code_review",
				Name:       "Code review (repo pipeline)",
				Type:       wf.StepTypeWorker,
				Topic:      "job.workflow.repo.code_review",
				TimeoutSec: 3600,
			},
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo_url":      map[string]any{"type": "string"},
				"branch":        map[string]any{"type": "string"},
				"local_path":    map[string]any{"type": "string"},
				"include_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"exclude_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"max_files":     map[string]any{"type": "integer"},
				"batch_size":    map[string]any{"type": "integer"},
				"max_batches":   map[string]any{"type": "integer"},
				"run_tests":     map[string]any{"type": "boolean"},
				"test_command":  map[string]any{"type": "string"},
				"priority":      map[string]any{"type": "string"},
				"memory_id":     map[string]any{"type": "string"},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"repo_url"}},
				map[string]any{"required": []string{"local_path"}},
			},
		},
	}
	return store.SaveWorkflow(ctx, def)
}

func ensureCodeReviewPatchWorkflow(ctx context.Context, store *wf.RedisStore, orgID string) error {
	if store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	const workflowID = "code_review_patch"
	_, err := store.GetWorkflow(ctx, workflowID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, redis.Nil) {
		return err
	}

	def := &wf.Workflow{
		ID:          workflowID,
		OrgID:       orgID,
		Name:        "Code Review (Patch)",
		Description: "Multi-step code review: propose a plan, generate a patch, and explain the result.",
		Version:     "1",
		TimeoutSec:  900,
		CreatedBy:   "system",
		Steps: map[string]*wf.Step{
			"plan_review": {
				ID:         "plan_review",
				Name:       "Plan review",
				Type:       wf.StepTypeWorker,
				Topic:      "job.chat.advanced",
				TimeoutSec: 180,
				Retry: &wf.RetryConfig{
					MaxRetries:        1,
					InitialBackoffSec: 2,
					MaxBackoffSec:     10,
					Multiplier:        2,
				},
				Input: map[string]any{
					"file_path":    "${input.file_path}",
					"code_snippet": "${input.code_snippet}",
					"instruction":  "${input.instruction}",
					"prompt": "You are a senior code reviewer.\n\n" +
						"Goal: propose a concise plan of changes.\n\n" +
						"File: ${input.file_path}\n\n" +
						"Instruction:\n${input.instruction}\n\n" +
						"Code:\n${input.code_snippet}\n",
				},
			},
			"generate_patch": {
				ID:         "generate_patch",
				Name:       "Generate patch",
				Type:       wf.StepTypeWorker,
				Topic:      "job.code.llm",
				TimeoutSec: 300,
				DependsOn:  []string{"plan_review"},
				Retry: &wf.RetryConfig{
					MaxRetries:        1,
					InitialBackoffSec: 2,
					MaxBackoffSec:     10,
					Multiplier:        2,
				},
				Input: map[string]any{
					"file_path":    "${input.file_path}",
					"code_snippet": "${input.code_snippet}",
					"instruction": "Apply this review plan and generate a unified diff patch.\n\n" +
						"Plan:\n${steps.plan_review.output.response}\n\n" +
						"Original instruction:\n${input.instruction}\n",
				},
			},
			"explain_patch": {
				ID:         "explain_patch",
				Name:       "Explain patch",
				Type:       wf.StepTypeWorker,
				Topic:      "job.chat.advanced",
				TimeoutSec: 180,
				DependsOn:  []string{"generate_patch"},
				Retry: &wf.RetryConfig{
					MaxRetries:        1,
					InitialBackoffSec: 2,
					MaxBackoffSec:     10,
					Multiplier:        2,
				},
				Input: map[string]any{
					"prompt": "Explain the following patch and summarize the key changes and risks.\n\n" +
						"File: ${input.file_path}\n\n" +
						"Patch:\n${steps.generate_patch.output.patch.content}\n",
				},
			},
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":    "string",
					"default": "src/main.go",
				},
				"code_snippet": map[string]any{
					"type":    "string",
					"default": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
				},
				"instruction": map[string]any{
					"type":    "string",
					"default": "Refactor for readability and add basic error handling where appropriate.",
				},
				"priority":  map[string]any{"type": "string", "default": "interactive"},
				"memory_id": map[string]any{"type": "string"},
			},
			"required": []string{"file_path", "code_snippet", "instruction"},
		},
	}
	return store.SaveWorkflow(ctx, def)
}

// startBusTaps subscribes to heartbeats and system events once for the lifetime of the gateway.
func (s *server) startBusTaps() {
	// Heartbeats -> worker registry snapshot
	_ = s.bus.Subscribe("sys.heartbeat", "", func(p *pb.BusPacket) {
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
	})

	// DLQ tap to persist entries
	if s.dlqStore != nil {
		_ = s.bus.Subscribe("sys.job.dlq", "", func(p *pb.BusPacket) {
			if jr := p.GetJobResult(); jr != nil {
				jobID := strings.TrimSpace(jr.JobId)
				_ = s.dlqStore.Add(context.Background(), memory.DLQEntry{
					JobID:     jobID,
					Topic:     "", // topic unknown here; stored in DLQ entry payload if needed
					Status:    jr.Status.String(),
					Reason:    jr.ErrorMessage,
					CreatedAt: time.Now().UTC(),
				})

				// Best effort: ensure a result exists for failed-to-dispatch jobs so dashboards can inspect `res:<job_id>`.
				if s.memStore != nil && s.jobStore != nil && jobID != "" {
					resKey := memory.MakeResultKey(jobID)
					resPtr := memory.PointerForKey(resKey)
					body := map[string]any{
						"job_id":       jobID,
						"status":       jr.Status.String(),
						"error":        map[string]any{"message": jr.ErrorMessage},
						"processed_by": "coretex-scheduler",
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
		})
	}

	// Event taps -> broadcast channel
	for _, subj := range []string{"sys.job.>", "sys.audit.>"} {
		subject := subj
		_ = s.bus.Subscribe(subject, "", func(p *pb.BusPacket) {
			if subject == "sys.job.>" {
				s.handleWorkflowJobResult(context.Background(), p.GetJobResult())
			}
			select {
			case s.eventsCh <- p:
			default:
			}
		})
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
					conn.Close()
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
		lockKey := "coretex:wf:run:lock:" + runID
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
		w.Write([]byte("ok"))
	})

	// 2. Workers (RPC via NATS)
	mux.HandleFunc("GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))

	// 2.5 Status snapshot (Redis/NATS/workers/uptime)
	mux.HandleFunc("GET /api/v1/status", s.instrumented("/api/v1/status", s.handleStatus))

	// 3. Jobs (Redis ZSet)
	mux.HandleFunc("GET /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleListJobs))

	// 4. Job Details
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.instrumented("/api/v1/jobs/{id}", s.handleGetJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.instrumented("/api/v1/jobs/{id}/cancel", s.handleCancelJob))

	// 4.5 Memory pointers (debug)
	mux.HandleFunc("GET /api/v1/memory", s.instrumented("/api/v1/memory", s.handleGetMemory))

	// 5. Submit Job (REST)
	mux.HandleFunc("POST /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleSubmitJobHTTP))
	mux.HandleFunc("POST /api/v1/repo-review", s.instrumented("/api/v1/repo-review", s.handleSubmitRepoReview))

	// 6. Trace Details
	mux.HandleFunc("GET /api/v1/traces/{id}", s.instrumented("/api/v1/traces/{id}", s.handleGetTrace))

	// 8. Workflows
	mux.HandleFunc("GET /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleListWorkflows))
	mux.HandleFunc("POST /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleCreateWorkflow))
	mux.HandleFunc("GET /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleGetWorkflow))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleStartRun))
	mux.HandleFunc("GET /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleListRuns))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleGetRun))

	// 9. Config
	mux.HandleFunc("GET /api/v1/config/effective", s.instrumented("/api/v1/config/effective", s.handleGetEffectiveConfig))
	mux.HandleFunc("POST /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))

	// 10. DLQ
	mux.HandleFunc("GET /api/v1/dlq", s.instrumented("/api/v1/dlq", s.handleListDLQ))
	mux.HandleFunc("DELETE /api/v1/dlq/{job_id}", s.instrumented("/api/v1/dlq/{job_id}", s.handleDeleteDLQ))
	mux.HandleFunc("POST /api/v1/dlq/{job_id}/retry", s.instrumented("/api/v1/dlq/{job_id}/retry", s.handleRetryDLQ))

	// 11. Workflow approvals
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.handleApproveStep))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/cancel", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/cancel", s.handleCancelRun))

	// 7. Stream (WebSocket)
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))

	// CORS Middleware
	handler := corsMiddleware(rateLimitMiddleware(apiKeyMiddleware(mux)))

	logging.Info("api-gateway", "http listening", "addr", httpAddr)
	if err := http.ListenAndServe(httpAddr, handler); err != nil {
		logging.Error("api-gateway", "http server error", "error", err)
		return err
	}
	return nil
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

	var jobs []scheduler.JobRecord
	var err error
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
	if len(filtered) == int(limit) {
		nc := filtered[len(filtered)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	json.NewEncoder(w).Encode(map[string]any{
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
	safetyDecision, safetyReason, _ := s.jobStore.GetSafetyDecision(r.Context(), id)
	topic, _ := s.jobStore.GetTopic(r.Context(), id)
	tenant, _ := s.jobStore.GetTenant(r.Context(), id)

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

	errorMessage := ""
	errorStatus := ""
	if s.dlqStore != nil {
		if entry, err := s.dlqStore.Get(r.Context(), id); err == nil && entry != nil {
			errorMessage = strings.TrimSpace(entry.Reason)
			errorStatus = strings.TrimSpace(entry.Status)
		}
	}

	resp := map[string]any{
		"id":              id,
		"state":           state,
		"trace_id":        traceID,
		"context_ptr":     ctxPtr,
		"context":         contextData,
		"result_ptr":      resPtr,
		"result":          resultData,
		"topic":           topic,
		"tenant":          tenant,
		"safety_decision": safetyDecision,
		"safety_reason":   safetyReason,
	}
	if errorMessage != "" {
		resp["error_message"] = errorMessage
	}
	if errorStatus != "" {
		resp["error_status"] = errorStatus
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	if s.memStore == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
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

func (s *server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	state, err := s.jobStore.CancelJob(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to cancel job: %v", err), http.StatusInternalServerError)
		return
	}
	if state == "" {
		state = scheduler.JobStateCancelled
	}
	if state != scheduler.JobStateCancelled {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     id,
			"state":  state,
			"reason": "job already terminal",
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

	// Best-effort cancel broadcast to workers.
	cancelReq := &pb.JobRequest{
		JobId: id,
		Topic: "sys.job.cancel",
		Env: map[string]string{
			"cancel_reason": "cancelled via api",
		},
	}
	cancelBusPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: cancelReq},
	}
	_ = s.bus.Publish("sys.job.cancel", cancelBusPacket)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":    id,
		"state": scheduler.JobStateCancelled,
	})
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

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.Priority)

	// Use OrgId from request, or server's tenant fallback
	orgID := req.OrgId
	if orgID == "" {
		orgID = s.tenant
	}
	teamID := req.TeamId
	projectID := req.ProjectId
	principalID := req.PrincipalId

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
	_ = s.memStore.PutContext(r.Context(), ctxKey, payloadBytes)

	// Set initial state
	_ = s.jobStore.SetState(r.Context(), jobID, scheduler.JobStatePending)
	_ = s.jobStore.SetTopic(r.Context(), jobID, req.Topic)
	_ = s.jobStore.SetTenant(r.Context(), jobID, orgID) // Use OrgId here too

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
	required := requiredAPIKeyFromEnv()
	if required != "" {
		key := normalizeAPIKey(r.Header.Get("X-API-Key"))
		if key == "" {
			key = normalizeAPIKey(r.URL.Query().Get("api_key"))
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

func normalizeAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	// Common .env mistake: quoting values (e.g. "super-secret-key").
	key = strings.Trim(key, "\"'")
	return strings.TrimSpace(key)
}

func requiredAPIKeyFromEnv() string {
	if v := normalizeAPIKey(os.Getenv("CORETEX_SUPER_SECRET_API_TOKEN")); v != "" {
		return v
	}
	if v := normalizeAPIKey(os.Getenv("CORETEX_API_KEY")); v != "" {
		return v
	}
	return normalizeAPIKey(os.Getenv("API_KEY"))
}

func addrFromEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
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

// Submit a repo review workflow job with explicit repo context.
func (s *server) handleSubmitRepoReview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJobPayloadBytes)
	var req repoReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	required := requiredAPIKeyFromEnv()
	if required == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		key := normalizeAPIKey(r.Header.Get("X-API-Key"))
		if key == "" {
			key = normalizeAPIKey(r.URL.Query().Get("api_key"))
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
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}

	// Preserve existing fields on upsert for callers that send partial payloads.
	if existing, err := s.workflowStore.GetWorkflow(r.Context(), req.ID); err == nil && existing != nil {
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wfDef)
}

func (s *server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	orgID := r.URL.Query().Get("org_id")
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
	runID := uuid.NewString()
	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		orgID = s.tenant
	}
	teamID := r.URL.Query().Get("team_id")
	run := &wf.WorkflowRun{
		ID:         runID,
		WorkflowID: wfID,
		OrgID:      orgID,
		TeamID:     teamID,
		Input:      payload,
		Status:     wf.RunStatusPending,
		Steps:      map[string]*wf.StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(r.Context(), run); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Kick off execution
	if s.workflowEng != nil {
		if s.jobStore != nil {
			lockKey := "coretex:wf:run:lock:" + runID
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
	var req configUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	doc := &configsvc.Document{
		Scope:   configsvc.Scope(req.Scope),
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

func (s *server) handleGetEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	orgID := r.URL.Query().Get("org_id")
	teamID := r.URL.Query().Get("team_id")
	wfID := r.URL.Query().Get("workflow_id")
	stepID := r.URL.Query().Get("step_id")

	eff, err := s.configSvc.Effective(r.Context(), orgID, teamID, wfID, stepID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(eff)
}

// DLQ handlers
func (s *server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		http.Error(w, "dlq store unavailable", http.StatusServiceUnavailable)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

func (s *server) handleDeleteDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		http.Error(w, "dlq store unavailable", http.StatusServiceUnavailable)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}
	if err := s.dlqStore.Delete(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRetryDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil || s.jobStore == nil {
		http.Error(w, "dlq or job store unavailable", http.StatusServiceUnavailable)
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
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
		TraceId:         "dlq-retry-" + jobID,
		SenderId:        "api-gateway",
		ProtocolVersion: 1,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	_ = s.jobStore.SetTopic(r.Context(), newJobID, topic)
	_ = s.jobStore.SetTenant(r.Context(), newJobID, tenant)
	if team != "" {
		_ = s.jobStore.SetTeam(r.Context(), newJobID, team)
	}
	if principal != "" {
		_ = s.jobStore.SetPrincipal(r.Context(), newJobID, principal)
	}
	_ = s.jobStore.SetState(r.Context(), newJobID, scheduler.JobStatePending)

	if err := s.bus.Publish("sys.job.submit", packet); err != nil {
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
	wfID := r.PathValue("id")
	runID := r.PathValue("run_id")
	stepID := r.PathValue("step_id")
	if wfID == "" || runID == "" || stepID == "" {
		http.Error(w, "missing identifiers", http.StatusBadRequest)
		return
	}

	// Serialize workflow run mutations with the same lock used by the workflow-engine reconciler.
	if s.jobStore != nil {
		lockKey := "coretex:wf:run:lock:" + runID
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
	runID := r.PathValue("run_id")
	if runID == "" {
		http.Error(w, "missing run_id", http.StatusBadRequest)
		return
	}

	// Serialize workflow run mutations with the same lock used by the workflow-engine reconciler.
	if s.jobStore != nil {
		lockKey := "coretex:wf:run:lock:" + runID
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
	runs, err := s.workflowStore.ListRunsByWorkflow(r.Context(), wfID, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runs)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(run)
}

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	// The incoming gRPC request (req) directly contains the new identity fields.
	// We'll use them to populate the pb.JobRequest.

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.GetPriority())

	// Use OrgId from request, or server's tenant fallback
	orgID := req.GetOrgId()
	if orgID == "" {
		orgID = s.tenant
	}
	principalID := req.GetPrincipalId()

	payloadReq := submitJobRequest{
		Prompt:      req.GetPrompt(),
		Topic:       req.GetTopic(),
		AdapterId:   req.GetAdapterId(),
		Priority:    req.GetPriority(),
		TenantId:    orgID, // Use OrgId for TenantId in payloadReq
		PrincipalId: principalID,
		OrgId:       orgID,
		// MaxInputTokens and MaxOutputTokens are part of the Budget message in SubmitJobRequest
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
	_ = s.memStore.PutContext(ctx, ctxKey, payloadBytes)

	// Set initial state
	_ = s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending)
	_ = s.jobStore.SetTopic(ctx, jobID, payloadReq.Topic)
	_ = s.jobStore.SetTenant(ctx, jobID, orgID) // Use OrgId here

	maxInput := int64(8000)
	maxOutput := int64(1024)
	envVars := map[string]string{
		"tenant_id":         orgID,
		"memory_id":         deriveMemoryIDFromReq(payloadReq.Topic, "", jobID),
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

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       payloadReq.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   payloadReq.AdapterId,
		Env:         envVars,
		MemoryId:    envVars["memory_id"],
		TenantId:    orgID,       // Use OrgId here
		PrincipalId: principalID, // Populated from new field
		Labels:      nil,         // SubmitJobRequest does not include labels
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
