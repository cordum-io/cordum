package planner

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	plannerWorkerID   = "worker-planner-1"
	plannerQueueGroup = "workers-planner"
	plannerSubject    = "job.workflow.plan"
)

type plan struct {
	Workflow string     `json:"workflow"`
	Steps    []planStep `json:"steps"`
}

type planStep struct {
	ID        string   `json:"id"`
	Topic     string   `json:"topic"`
	AdapterID string   `json:"adapter_id,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type repoFile struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
}

type planRequest struct {
	Workflow   string     `json:"workflow"`
	Files      []repoFile `json:"files,omitempty"`
	MaxFiles   int        `json:"max_files,omitempty"`
	MaxBatches int        `json:"max_batches,omitempty"`
}

type planResponse struct {
	Workflow string     `json:"workflow"`
	Steps    []planStep `json:"steps,omitempty"`
	Files    []string   `json:"files,omitempty"`
}

// A minimal planner that returns the current hard-coded plan for code review.
func buildDefaultPlan(workflow string) plan {
	return plan{
		Workflow: workflow,
		Steps: []planStep{
			{
				ID:        "patch",
				Topic:     "job.code.llm",
				AdapterID: "refactor",
			},
			{
				ID:        "explain",
				Topic:     "job.chat.simple",
				AdapterID: "explain",
				DependsOn: []string{"patch"},
			},
		},
	}
}

// Run starts the planner worker.
func Run() {
	cfg := config.Load()

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("planner: failed to connect to Redis: %v", err)
	}
	defer memStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("planner: failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	if err := natsBus.Subscribe(plannerSubject, plannerQueueGroup, handlePlan(natsBus, memStore)); err != nil {
		log.Fatalf("planner: failed to subscribe: %v", err)
	}

	log.Println("planner worker running on subject", plannerSubject)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func handlePlan(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		ctx := context.Background()
		var planReq planRequest
		if req.ContextPtr != "" {
			if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
				if data, err := store.GetContext(ctx, key); err == nil {
					_ = json.Unmarshal(data, &planReq)
				}
			}
		}
		if planReq.Workflow == "" {
			planReq.Workflow = req.GetWorkflowId()
		}
		if planReq.Workflow == "" {
			planReq.Workflow = "code_review_demo"
		}

		var planPayload planResponse
		if len(planReq.Files) > 0 {
			planPayload = buildRepoPlan(planReq)
		} else {
			p := buildDefaultPlan(planReq.Workflow)
			planPayload = planResponse{Workflow: p.Workflow, Steps: p.Steps}
		}

		planBytes, _ := json.Marshal(planPayload)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, planBytes); err != nil {
			log.Printf("planner: failed to store plan job_id=%s err=%v", req.JobId, err)
			return
		}
		resultPtr := memory.PointerForKey(resKey)

		res := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr:   resultPtr,
			WorkerId:    plannerWorkerID,
			ExecutionMs: 0,
		}

		resp := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        plannerWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload:         &pb.BusPacket_JobResult{JobResult: res},
		}

		if err := b.Publish("sys.job.result", resp); err != nil {
			log.Printf("planner: failed to publish result job_id=%s: %v", req.JobId, err)
		}
	}
}

// buildRepoPlan prioritizes files based on language and size to guide downstream steps.
func buildRepoPlan(req planRequest) planResponse {
	weights := map[string]int64{
		"cpp": 5, "c": 4, "c++": 5, "cxx": 5, "hpp": 5, "hxx": 5,
		"go": 4, "python": 4, "rust": 4,
		"typescript": 3, "javascript": 3, "java": 3, "csharp": 3,
		"ruby": 2, "php": 2, "shell": 2, "bash": 2,
	}

	type scored struct {
		path  string
		score int64
	}
	var items []scored
	for _, f := range req.Files {
		lang := strings.ToLower(f.Language)
		w := weights[lang]
		if w == 0 {
			w = 1
		}
		score := w*1000 + f.Loc*2 + f.Bytes
		items = append(items, scored{path: f.Path, score: score})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })

	maxFiles := req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 200
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.path)
		if len(out) >= maxFiles {
			break
		}
	}
	return planResponse{
		Workflow: req.Workflow,
		Files:    out,
	}
}
