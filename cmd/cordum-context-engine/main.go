package main

import (
	"context"
	"crypto/tls"
	"log"
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
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()
	buildinfo.Log("cordum-context-engine")

	infraMetrics.NewProm("cordum_context_engine")
	metricsAddr := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_METRICS_ADDR"))
	if metricsAddr == "" {
		metricsAddr = ":9094"
	}
	if env.IsProduction() {
		if err := infraMetrics.ValidateBindAddr(metricsAddr, env.Bool("CONTEXT_ENGINE_METRICS_PUBLIC")); err != nil {
			log.Fatalf("metrics bind rejected: %v", err)
		}
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
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
		log.Printf("context engine metrics on %s/metrics", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	svc, err := engine.NewService(cfg.RedisURL)
	if err != nil {
		log.Fatalf("context engine init failed: %v", err)
	}

	addr := cfg.ContextEngineAddr
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	certFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_KEY"))
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			log.Fatalf("context engine tls requires both CONTEXT_ENGINE_TLS_CERT and CONTEXT_ENGINE_TLS_KEY")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			log.Fatalf("context engine tls keypair: %v", err)
		}
		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			cfg.MinVersion = tls.VersionTLS13
		}
		serverCreds = grpc.Creds(credentials.NewTLS(cfg))
	}
	if env.IsProduction() && certFile == "" {
		log.Fatalf("context engine tls required in production")
	}

	server := grpc.NewServer(serverCreds)
	pb.RegisterContextEngineServer(server, svc)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(server, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if env.Bool(env.EnvGRPCReflection) {
		reflection.Register(server)
	}

	log.Printf("context engine listening on %s (redis=%s)", addr, cfg.RedisURL)

	// Graceful shutdown: on SIGINT/SIGTERM, drain in-flight RPCs then stop.
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	go func() {
		<-sigCtx.Done()
		log.Println("context-engine shutting down gracefully...")

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
			log.Println("context-engine gRPC server drained")
		case <-shutdownCtx.Done():
			log.Println("context-engine gRPC graceful stop timed out, forcing")
			server.Stop()
		}

		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("context-engine metrics shutdown error: %v", err)
		}
	}()

	if err := server.Serve(lis); err != nil {
		log.Fatalf("context engine server error: %v", err)
	}
}
