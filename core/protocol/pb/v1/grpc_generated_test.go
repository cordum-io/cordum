package v1

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type cordumAPITestServer struct {
	UnimplementedCordumApiServer
}

func (s *cordumAPITestServer) SubmitJob(ctx context.Context, req *SubmitJobRequest) (*SubmitJobResponse, error) {
	return &SubmitJobResponse{JobId: "job-1", TraceId: "trace-1"}, nil
}

func (s *cordumAPITestServer) GetJobStatus(ctx context.Context, req *GetJobStatusRequest) (*GetJobStatusResponse, error) {
	return &GetJobStatusResponse{JobId: req.GetJobId(), Status: "ok", ResultPtr: "redis://res:1"}, nil
}

type contextEngineTestServer struct {
	UnimplementedContextEngineServer
}

func (s *contextEngineTestServer) BuildWindow(ctx context.Context, req *BuildWindowRequest) (*BuildWindowResponse, error) {
	return &BuildWindowResponse{Messages: []*ModelMessage{{Role: "assistant", Content: "ok"}}, InputTokens: 1, OutputTokens: 2}, nil
}

func (s *contextEngineTestServer) UpdateMemory(ctx context.Context, req *UpdateMemoryRequest) (*UpdateMemoryResponse, error) {
	return &UpdateMemoryResponse{}, nil
}

func TestGRPCGeneratedClientsAndServers(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	RegisterCordumApiServer(srv, &cordumAPITestServer{})
	RegisterContextEngineServer(srv, &contextEngineTestServer{})

	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	defer conn.Close()

	apiClient := NewCordumApiClient(conn)
	if _, err := apiClient.SubmitJob(context.Background(), &SubmitJobRequest{Prompt: "hi", Topic: "job.test"}); err != nil {
		t.Fatalf("submit job: %v", err)
	}
	if _, err := apiClient.GetJobStatus(context.Background(), &GetJobStatusRequest{JobId: "job-1"}); err != nil {
		t.Fatalf("get job status: %v", err)
	}

	ctxClient := NewContextEngineClient(conn)
	if _, err := ctxClient.BuildWindow(context.Background(), &BuildWindowRequest{MemoryId: "mem", Mode: ContextMode_CONTEXT_MODE_CHAT}); err != nil {
		t.Fatalf("build window: %v", err)
	}
	if _, err := ctxClient.UpdateMemory(context.Background(), &UpdateMemoryRequest{MemoryId: "mem"}); err != nil {
		t.Fatalf("update memory: %v", err)
	}
}

func TestProtoDescriptors(t *testing.T) {
	_ = file_api_proto_rawDescGZIP()
	_ = file_context_proto_rawDescGZIP()

	var req SubmitJobRequest
	req.ProtoMessage()
	req.Descriptor()

	var bw BuildWindowResponse
	bw.ProtoMessage()
	bw.Descriptor()
}
