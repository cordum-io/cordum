//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	infraStore "github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type jobAuthorityBus struct{}

type jobAuthoritySafety struct{}

func (jobAuthoritySafety) Check(context.Context, *pb.JobRequest) (scheduler.SafetyDecisionRecord, error) {
	return scheduler.SafetyDecisionRecord{Decision: scheduler.SafetyAllow}, nil
}

type jobAuthorityStrategy struct{}

func (jobAuthorityStrategy) PickSubject(*pb.JobRequest, map[string]*pb.Heartbeat,
	map[string]scheduler.WorkerReadiness) (string, error) {
	return "", errors.New("no test worker")
}

func (jobAuthorityBus) Publish(string, *pb.BusPacket) error { return nil }
func (jobAuthorityBus) Subscribe(string, string, func(*pb.BusPacket) error) error {
	return nil
}

func (h *interopHarness) TestJobRequestAuthority(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	store := infraStore.NewRedisJobStoreFromClient(redisClient)
	engine := scheduler.NewEngine(jobAuthorityBus{}, jobAuthoritySafety{}, scheduler.NewMemoryRegistry(),
		jobAuthorityStrategy{}, store, nil)
	engine.WithSessionMiddleware(scheduler.NewSessionTokenMiddleware(h.server.issuer,
		scheduler.HandshakeModeEnforce, scheduler.NewHandshakeMissingTracker()))

	unsignedID := randomID(t)
	if err := engine.HandlePacket(jobRequestPacket(unsignedID, "attacker", "")); err != nil {
		t.Fatalf("unsigned HandlePacket: %v", err)
	}
	if _, err := store.GetState(context.Background(), unsignedID); !errors.Is(err, redis.Nil) {
		t.Fatalf("unsigned request mutated job state: %v", err)
	}

	token, err := h.server.issuer.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		t.Fatalf("mint gateway service token: %v", err)
	}
	signedID := randomID(t)
	_ = engine.HandlePacket(jobRequestPacket(signedID, servicetoken.IdentityGateway, token))
	if _, err := store.GetState(context.Background(), signedID); err != nil {
		t.Fatalf("authenticated request did not reach scheduler state machine: %v", err)
	}
}

func jobRequestPacket(jobID, senderID, token string) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId: randomTraceID(jobID), SenderId: senderID, AuthToken: token,
		ProtocolVersion: 1, CreatedAt: timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
			JobId: jobID, Topic: "job.interop", TenantId: "tenant-interop",
		}},
	}
}

func randomTraceID(jobID string) string { return "trace-" + jobID }
