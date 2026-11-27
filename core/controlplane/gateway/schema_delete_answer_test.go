package gateway

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/schema"
)

// A bound delete of an absent id that another tenant claims after the delete's
// owner read answers as the claim-first order does and leaves the claim intact.
func TestSchemaDeleteAnswerMatchesClaimFirst(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "control"
		if scheduled {
			name = "scheduled"
		}
		t.Run(name, func(t *testing.T) {
			sw := newCrossTenantSweep(t)
			client := sw.server.jobStore.Client()
			sw.server.schemaRegistry = schema.NewRegistryFromClient(client)
			id := "delete-answer-claim-first"
			var winner schemaStepState
			hook := &schemaStepSchedule{id: id}
			if scheduled {
				hook.after = func() {
					rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/schemas", schemaTenantBody(id, "winner-document"))
					schemaTenantWant(t, rr, "B claim", http.StatusNoContent)
					winner = schemaStepSnapshot(t, sw, id)
				}
				client.AddHook(hook)
			}
			rr := sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/schemas/"+id, "")
			if hook.after != nil {
				t.Fatal("owner-read schedule did not run")
			}
			schemaTenantWant(t, rr, "A delete", http.StatusNotFound)
			if !scheduled {
				if state := schemaStepSnapshot(t, sw, id); state.doc != "" || state.owner != "" || len(state.index) != 0 {
					t.Fatalf("absent control state: %+v", state)
				}
				return
			}
			if after := schemaStepSnapshot(t, sw, id); !reflect.DeepEqual(after, winner) {
				t.Fatalf("claimed state changed: before=%+v after=%+v", winner, after)
			}
			if strings.Contains(rr.Body.String(), "winner-document") {
				t.Fatal("response contains another owner's document")
			}
			sequential := sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/schemas/"+id, "")
			if rr.Code != sequential.Code || rr.Body.String() != sequential.Body.String() {
				t.Fatalf("response differs from claim-first order: %d %q, %d %q", rr.Code, rr.Body.String(), sequential.Code, sequential.Body.String())
			}
		})
	}
}
