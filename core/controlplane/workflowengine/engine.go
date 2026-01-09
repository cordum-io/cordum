package workflowengine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/memory"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

const (
	defaultHTTPAddr        = ":9093"
	defaultReadTimeout     = 5 * time.Second
	defaultWriteTimeout    = 5 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 3 * time.Second
	workflowEngineQueue    = "cordum-workflow-engine"
)

// Run starts the workflow engine control-plane component.
func Run(cfg *config.Config) error {
	if cfg == nil {
		cfg = config.Load()
	}

	httpAddr := os.Getenv("WORKFLOW_ENGINE_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}
	scanInterval := 5 * time.Second
	if v := os.Getenv("WORKFLOW_ENGINE_SCAN_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			scanInterval = d
		}
	}
	runScanLimit := int64(200)
	if v := os.Getenv("WORKFLOW_ENGINE_RUN_SCAN_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			runScanLimit = n
		}
	}

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis memory store: %w", err)
	}
	defer memStore.Close()

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis job store: %w", err)
	}
	defer jobStore.Close()

	workflowStore, err := wf.NewRedisWorkflowStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis workflow store: %w", err)
	}
	defer workflowStore.Close()

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis config service: %w", err)
	}
	defer configSvc.Close()

	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis schema registry: %w", err)
	}
	defer schemaRegistry.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer natsBus.Close()

	engine := wf.NewEngine(workflowStore, natsBus).WithMemory(memStore).WithConfig(configSvc).WithSchemaRegistry(schemaRegistry)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rec := newReconciler(workflowStore, engine, jobStore, scanInterval, runScanLimit)
	go rec.Start(ctx)

	if err := natsBus.Subscribe(capsdk.SubjectResult, workflowEngineQueue, func(p *pb.BusPacket) error {
		if jr := p.GetJobResult(); jr != nil {
			return rec.HandleJobResult(context.Background(), jr)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("subscribe %s: %w", capsdk.SubjectResult, err)
	}

	srv := startHealthServer(httpAddr)
	logging.Info("workflow-engine", "started", "http", httpAddr, "scan_interval", scanInterval.String(), "run_scan_limit", runScanLimit)

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	logging.Info("workflow-engine", "stopped")
	return nil
}

func startHealthServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
		IdleTimeout:  defaultIdleTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("workflow-engine", "http server error", "error", err)
		}
	}()
	return srv
}
