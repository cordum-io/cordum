package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/cordum/cordum/core/infra/schema"
	"github.com/redis/go-redis/v9"
)

// schemaListSchedule completes one competing action after the registry's
// read of the schema index.
type schemaListSchedule struct{ after func() }

func (h *schemaListSchedule) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *schemaListSchedule) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *schemaListSchedule) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && h.after != nil && cmd.Name() == "zrevrange" && cmd.Args()[1] == "schema:index" {
			after := h.after
			h.after = nil
			after()
		}
		return err
	}
}

// A bound list keeps only ids that, at one moment, have a document and an
// owner of none or the caller, even when an id is deleted after the index read.
func TestSchemaListOwnerCheckedRead(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "control"
		if scheduled {
			name = "scheduled"
		}
		t.Run(name, func(t *testing.T) {
			sw := newCrossTenantSweep(t)
			client := sw.server.jobStore.Client()
			sw.server.schemaRegistry = schema.NewRegistryFromClient(client)
			x, y, p := "list-owner-b", "list-owner-a", "list-owner-platform"
			schemaTenantWant(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/schemas", schemaTenantBody(x, "b-document")), "seed B", http.StatusNoContent)
			schemaTenantWant(t, sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/schemas", schemaTenantBody(y, "a-document")), "seed A", http.StatusNoContent)
			if err := sw.server.schemaRegistry.Register(t.Context(), p, []byte(`{"type":"object"}`)); err != nil {
				t.Fatal(err)
			}
			hook := &schemaListSchedule{}
			if scheduled {
				hook.after = func() {
					schemaTenantWant(t, sw.as(t, sw.tenantB, http.MethodDelete, "/api/v1/schemas/"+x, ""), "B delete", http.StatusNoContent)
				}
				client.AddHook(hook)
			}
			rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/schemas", "")
			if hook.after != nil {
				t.Fatal("index-read schedule did not run")
			}
			schemaTenantWant(t, rr, "A list", http.StatusOK)
			var list struct{ Schemas []string }
			if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(list.Schemas, x) {
				t.Fatal("list shows an id the caller may not see")
			}
			if !slices.Contains(list.Schemas, p) || !slices.Contains(list.Schemas, y) {
				t.Fatalf("A list lacks the platform or own id: %s", rr.Body.String())
			}
			again := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/schemas", "")
			if again.Code != rr.Code || again.Body.String() != rr.Body.String() {
				t.Fatalf("second list differs: %d %q, %d %q", rr.Code, rr.Body.String(), again.Code, again.Body.String())
			}
		})
	}
}
