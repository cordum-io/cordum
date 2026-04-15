package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cordum/cordum/core/contextwindow/engine"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	infraHealth "github.com/cordum/cordum/core/infra/health"
	"github.com/cordum/cordum/core/infra/logging"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/tlsreload"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

const (
	envGRPCServerKeepaliveTime      = "CORDUM_GRPC_SERVER_KEEPALIVE_TIME"
	envGRPCServerKeepaliveTimeout   = "CORDUM_GRPC_SERVER_KEEPALIVE_TIMEOUT"
	envGRPCServerMaxConnectionAge   = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE"
	envGRPCServerMaxConnectionGrace = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE_GRACE"
	envGRPCServerEnforcementMinTime = "CORDUM_GRPC_SERVER_ENFORCEMENT_MIN_TIME"
)

func main() {
	logging.Init("context-engine")
	cfg := config.Load()
	buildinfo.Log("cordum-context-engine")

	infraMetrics.NewProm("cordum_context_engine")
	metricsAddr := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_METRICS_ADDR"))
	if metricsAddr == "" {
		metricsAddr = ":9094"
	}
	if env.IsProduction() {
		if err := infraMetrics.ValidateBindAddr(metricsAddr, env.Bool("CONTEXT_ENGINE_METRICS_PUBLIC")); err != nil {
			slog.Error("metrics bind rejected", "error", err)
			os.Exit(1)
		}
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	ceProbes := infraHealth.New()
	ceProbes.Register(metricsMux)
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		slog.Info("context engine metrics started", "addr", metricsAddr+"/metrics")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	svc, err := engine.NewService(cfg.RedisURL)
	if err != nil {
		slog.Error("context engine init failed", "error", err)
		os.Exit(1)
	}

	addr := cfg.ContextEngineAddr
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("failed to listen", "addr", addr, "error", err)
		os.Exit(1)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	certFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_KEY"))
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			slog.Error("context engine tls requires both CONTEXT_ENGINE_TLS_CERT and CONTEXT_ENGINE_TLS_KEY")
			os.Exit(1)
		}
		reloader, err := tlsreload.NewCertReloader(certFile, keyFile, "context-engine")
		if err != nil {
			slog.Error("context engine tls keypair failed", "error", err)
			os.Exit(1)
		}
		go reloader.WatchLoop(context.Background(), 30*time.Second)
		tlsCfg := &tls.Config{
			GetCertificate: reloader.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			tlsCfg.MinVersion = tls.VersionTLS13
		}
		serverCreds = grpc.Creds(credentials.NewTLS(tlsCfg))
	}
	if env.IsProduction() && certFile == "" {
		slog.Error("context engine tls required in production")
		os.Exit(1)
	}

	server := grpc.NewServer(
		serverCreds,
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge:      env.DurationOr(envGRPCServerMaxConnectionAge, 2*time.Hour),
			MaxConnectionAgeGrace: env.DurationOr(envGRPCServerMaxConnectionGrace, 30*time.Second),
			Time:                  env.DurationOr(envGRPCServerKeepaliveTime, 30*time.Second),
			Timeout:               env.DurationOr(envGRPCServerKeepaliveTimeout, 10*time.Second),
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             env.DurationOr(envGRPCServerEnforcementMinTime, 15*time.Second),
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterContextEngineServer(server, svc)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(server, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if env.Bool(env.EnvGRPCReflection) {
		reflection.Register(server)
	}

	ceProbes.SetStartupComplete()
	slog.Info("context engine listening", "addr", addr, "redis", cfg.RedisURL)

	// Graceful shutdown: on SIGINT/SIGTERM, drain in-flight RPCs then stop.
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	go func() {
		<-sigCtx.Done()
		slog.Info("context-engine shutting down gracefully...")

		const shutdownTimeout = 15 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// GracefulStop drains in-flight RPCs. Force-stop if it takes too long.
		grpcDone := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(grpcDone)
		}()
		select {
		case <-grpcDone:
			slog.Info("context-engine gRPC server drained")
		case <-shutdownCtx.Done():
			slog.Warn("context-engine gRPC graceful stop timed out, forcing")
			server.Stop()
		}

		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("context-engine metrics shutdown error", "error", err)
		}
	}()

	if err := server.Serve(lis); err != nil {
		slog.Error("context engine server error", "error", err)
		os.Exit(1)
	}
}
