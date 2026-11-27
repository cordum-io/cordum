package gateway

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// The mini cluster does not reject cross-slot commands on a node-level Tx.
// This guard models that constraint for the sequential enqueue scenarios.
type mcpIndexSlotGuard struct {
	mu      sync.Mutex
	watched string
}

func (h *mcpIndexSlotGuard) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *mcpIndexSlotGuard) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		switch cmd.Name() {
		case "watch":
			if len(cmd.Args()) != 2 {
				h.mu.Unlock()
				return fmt.Errorf("WATCH must name one index key")
			}
			h.watched = fmt.Sprint(cmd.Args()[1])
		case "exec", "unwatch":
			h.watched = ""
		case "cluster", "hello", "client", "ping", "command":
		default:
			if len(cmd.Args()) > 1 && h.watched != "" && fmt.Sprint(cmd.Args()[1]) != h.watched {
				h.mu.Unlock()
				err := fmt.Errorf("command %s touches another key during WATCH", cmd.Name())
				cmd.SetErr(err)
				return err
			}
		}
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (h *mcpIndexSlotGuard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) == 0 || cmds[0].Name() != "multi" {
			return next(ctx, cmds)
		}
		h.mu.Lock()
		key := h.watched
		h.mu.Unlock()
		for _, cmd := range cmds {
			if cmd.Name() == "multi" || cmd.Name() == "exec" {
				continue
			}
			if key == "" && len(cmd.Args()) > 1 {
				key = fmt.Sprint(cmd.Args()[1])
			}
			if len(cmd.Args()) < 2 || fmt.Sprint(cmd.Args()[1]) != key {
				err := fmt.Errorf("MULTI touches more than the index key")
				for _, queued := range cmds {
					queued.SetErr(err)
				}
				return err
			}
		}
		err := next(ctx, cmds)
		h.mu.Lock()
		h.watched = "" // EXEC releases WATCH; go-redis need not send UNWATCH.
		h.mu.Unlock()
		return err
	}
}

func TestEnqueueMCPApprovalCluster(t *testing.T) {
	mr := miniredis.RunT(t)
	client := testredis.NewMiniClusterClient(t, mr.Addr())
	client.OnNewNode(func(node *redis.Client) { node.AddHook(&mcpIndexSlotGuard{}) })
	s, ctx := NewMCPApprovalStore(client), context.Background()
	var ids []string
	for _, requester := range []string{"A", "B", "A", "edge"} {
		req := testReq()
		req.Requester = requester
		if requester == "edge" {
			req.ID, req.EdgeApprovalRef = "paired-record", "edge-ref"
		}
		rec, err := s.EnqueueMCPApproval(ctx, req)
		if err != nil {
			t.Fatalf("enqueue %s: %v", requester, err)
		}
		ids = append(ids, rec.ID)
	}
	if ids[0] != ids[2] || ids[0] == ids[1] || ids[3] != "paired-record" {
		t.Fatalf("enqueue ids = %v; want A, B, A, paired-record", ids)
	}
	req := testReq()
	members, err := client.SMembers(ctx, mcpApprovalIndexKey(req.Tenant, req.AgentID, req.ToolName, req.ArgsHash)).Result()
	want := []string{ids[0], ids[1], ids[3]}
	slices.Sort(members)
	slices.Sort(want)
	if err != nil || !slices.Equal(members, want) {
		t.Fatalf("index = %v, %v; want %v", members, err, want)
	}
}
