package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/logging"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultListenAddr = ":8080"
)

type server struct {
	pb.UnimplementedCortexApiServer
	memStore memory.Store
	jobStore scheduler.JobStore
	bus      *bus.NatsBus
}

func main() {
	cfg := config.Load()

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
	}

	go serveHTTPHealth()

	lis, err := net.Listen("tcp", defaultListenAddr)
	if err != nil {
		logging.Error("api-gateway", "failed to listen for grpc", "addr", defaultListenAddr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterCortexApiServer(grpcServer, s)
	reflection.Register(grpcServer)

	logging.Info("api-gateway", "listening",
		"grpc_addr", defaultListenAddr,
		"health_addr", ":8081",
		"nats_url", cfg.NatsURL,
		"redis_url", cfg.RedisURL,
	)
	if err := grpcServer.Serve(lis); err != nil {
		logging.Error("api-gateway", "grpc server error", "error", err)
		os.Exit(1)
	}
}

func serveHTTPHealth() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if err := http.ListenAndServe(":8081", mux); err != nil {
		logging.Error("api-gateway", "health server error", "error", err)
	}
}

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	jobID := uuid.NewString()
	traceID := uuid.NewString()

	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)

	priority := pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	switch req.GetPriority() {
	case "batch":
		priority = pb.JobPriority_JOB_PRIORITY_BATCH
	case "critical":
		priority = pb.JobPriority_JOB_PRIORITY_CRITICAL
	}

	payload := map[string]any{
		"prompt":     req.GetPrompt(),
		"adapter_id": req.GetAdapterId(),
		"priority":   req.GetPriority(),
		"topic":      req.GetTopic(),
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	if err := s.memStore.PutContext(ctx, ctxKey, payloadBytes); err != nil {
		return nil, err
	}

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      req.GetTopic(),
		Priority:   priority,
		ContextPtr: ctxPtr,
		AdapterId:  req.GetAdapterId(),
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
		return nil, err
	}

	logging.Info("api-gateway", "job submitted",
		"job_id", jobID,
		"trace_id", traceID,
		"topic", req.GetTopic(),
		"context_ptr", ctxPtr,
	)

	return &pb.SubmitJobResponse{
		JobId:   jobID,
		TraceId: traceID,
	}, nil
}

func (s *server) GetJobStatus(ctx context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	if s.jobStore == nil {
		return &pb.GetJobStatusResponse{
			JobId:     req.GetJobId(),
			Status:    "UNKNOWN",
			ResultPtr: "",
		}, nil
	}

	state, err := s.jobStore.GetState(ctx, req.GetJobId())
	if err != nil {
		state = "UNKNOWN"
	}

	resultPtr, err := s.jobStore.GetResultPtr(ctx, req.GetJobId())
	if err != nil {
		resultPtr = ""
	}

	resp := &pb.GetJobStatusResponse{
		JobId:     req.GetJobId(),
		Status:    string(state),
		ResultPtr: resultPtr,
	}

	logging.Info("api-gateway", "job status fetched",
		"job_id", req.GetJobId(),
		"status", resp.Status,
		"result_ptr", resp.ResultPtr,
	)

	return resp, nil
}
