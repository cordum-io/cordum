package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/schema"
	"github.com/redis/go-redis/v9"
)

// schemaStepSchedule completes one competing action after a real owner read.
type schemaStepSchedule struct {
	id    string
	after func()
}

func (h *schemaStepSchedule) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *schemaStepSchedule) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *schemaStepSchedule) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if h.after == nil || (err != nil && !errors.Is(err, redis.Nil)) || (cmd.Name() != "get" && cmd.Name() != "hget") {
			return err
		}
		if cmd.Args()[1] == "schema:"+h.id {
			return err
		}
		for _, arg := range cmd.Args()[1:] {
			if key, ok := arg.(string); ok && strings.Contains(key, h.id) {
				after := h.after
				h.after = nil
				after()
				break
			}
		}
		return err
	}
}

func TestSchemaOwnerCheckedStep(t *testing.T) {
	for _, action := range []string{"delete", "read", "update", "claim"} {
		t.Run(action, func(t *testing.T) {
			for _, scheduled := range []bool{false, true} {
				name := "control"
				if scheduled {
					name = "scheduled"
				}
				t.Run(name, func(t *testing.T) { schemaStepCase(t, action, scheduled) })
			}
		})
	}
}

func schemaStepCase(t *testing.T, action string, scheduled bool) {
	t.Helper()
	sw := newCrossTenantSweep(t)
	client := sw.server.jobStore.Client()
	sw.server.schemaRegistry = schema.NewRegistryFromClient(client)
	id := "owner-step-" + action
	if action == "delete" || action == "update" {
		schemaTenantWant(t, sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/schemas", schemaTenantBody(id, "initial-a")), "seed", http.StatusNoContent)
	}
	var winner schemaStepState
	hook := &schemaStepSchedule{id: id}
	if scheduled {
		hook.after = func() {
			schemaStepWinner(t, sw, action, id)
			winner = schemaStepSnapshot(t, sw, id)
		}
		client.AddHook(hook)
	}
	rr := schemaStepRequest(t, sw, action, id)
	if !scheduled {
		schemaStepControl(t, sw, action, id, rr)
		return
	}
	if hook.after != nil {
		t.Fatal("owner-read schedule did not run")
	}
	if after := schemaStepSnapshot(t, sw, id); !reflect.DeepEqual(after, winner) {
		t.Fatalf("completed action state changed: before=%+v after=%+v", winner, after)
	}
	if strings.Contains(rr.Body.String(), "winner-document") {
		t.Fatal("response contains another owner's document")
	}
	want := http.StatusForbidden
	if action == "delete" || action == "read" {
		want = http.StatusNotFound
	}
	schemaTenantWant(t, rr, action, want)
	sequential := schemaStepRequest(t, sw, action, id)
	if rr.Body.String() != sequential.Body.String() || rr.Code != sequential.Code {
		t.Fatalf("response differs from winner-first order: %d %q, %d %q", rr.Code, rr.Body.String(), sequential.Code, sequential.Body.String())
	}
}

func schemaStepWinner(t *testing.T, sw *crossTenantSweep, action, id string) {
	t.Helper()
	if action == "claim" {
		if err := sw.server.schemaRegistry.Register(t.Context(), id, []byte(`{"type":"object","description":"winner-document"}`)); err != nil {
			t.Fatal(err)
		}
		return
	}
	if action != "read" {
		schemaTenantWant(t, sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/schemas/"+id, ""), "owner delete", http.StatusNoContent)
	}
	schemaTenantWant(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/schemas", schemaTenantBody(id, "winner-document")), "owner register", http.StatusNoContent)
}

func schemaStepRequest(t *testing.T, sw *crossTenantSweep, action, id string) *httptest.ResponseRecorder {
	t.Helper()
	if action == "delete" {
		return sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/schemas/"+id, "")
	}
	if action == "read" {
		return sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/schemas/"+id, "")
	}
	return sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/schemas", schemaTenantBody(id, "caller-document"))
}

type schemaStepState struct {
	doc, owner string
	index      []redis.Z
}

func schemaStepSnapshot(t *testing.T, sw *crossTenantSweep, id string) schemaStepState {
	t.Helper()
	reg := sw.server.schemaRegistry
	doc, err := reg.Get(t.Context(), id)
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	owner, err := reg.Owner(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	index, err := sw.server.jobStore.Client().ZRangeWithScores(t.Context(), "schema:index", 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return schemaStepState{string(doc), owner, index}
}

func schemaStepControl(t *testing.T, sw *crossTenantSweep, action, id string, rr *httptest.ResponseRecorder) {
	t.Helper()
	want := http.StatusNoContent
	if action == "read" {
		want = http.StatusNotFound
	}
	schemaTenantWant(t, rr, action+" control", want)
	state := schemaStepSnapshot(t, sw, id)
	if action == "delete" || action == "read" {
		if state.doc != "" || state.owner != "" || len(state.index) != 0 {
			t.Fatalf("absent control state: %+v", state)
		}
	} else if !strings.Contains(state.doc, "caller-document") || state.owner != sw.tenantA.tenantID || len(state.index) != 1 {
		t.Fatalf("registered control state: %+v", state)
	}
}
