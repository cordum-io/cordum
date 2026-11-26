package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/infra/config"
)

func TestTopicDeleteKeepsSharedPoolMapping(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		platform, other, global bool
	}{
		{"tenant-shared", false, true, false},
		{"tenant-last", false, false, false},
		{"platform-shared", true, true, false},
		{"platform-global", true, false, true},
		{"platform-last", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := newCrossTenantSweep(t)
			sw.server.tenant = "topic-mapping-platform"
			who := sw.tenantB
			if tc.platform || tc.other {
				who = sw.tenantA
			}
			if tc.platform {
				sw.server.tenant = who.tenantID
			}
			if tc.other {
				topicMappingRegister(t, sw, sw.tenantB)
			}
			topicMappingRegister(t, sw, who)
			if tc.global {
				err := sw.server.topicRegistry.Set(context.Background(), topicregistry.Registration{
					Name: "job.topic-mapping", Pool: "mapping-pool", Status: topicregistry.StatusActive,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			seedPoolConfig(t, sw.server, map[string]any{"job.topic-mapping": "mapping-pool"},
				map[string]config.PoolConfig{"mapping-pool": {}})
			topicMappingDelete(t, sw, who, tc.platform && !tc.other && !tc.global)
		})
	}
}

func topicMappingRegister(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	t.Helper()
	rr := sw.as(t, who, http.MethodPost, "/api/v1/topics", `{"name":"job.topic-mapping","status":"disabled"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register topic: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func topicMappingDelete(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, remove bool) {
	t.Helper()
	before := poolPlatformPools(t, sw)
	wantTopics := topicMappingDocument(t, sw, "topics")
	delete(wantTopics.Data, "tenant:"+who.tenantID+":job.topic-mapping")
	mark := sw.publishMark()
	rr := sw.as(t, who, http.MethodDelete, "/api/v1/topics/job.topic-mapping", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete topic: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := topicMappingDocument(t, sw, "topics"); !reflect.DeepEqual(got.Data, wantTopics.Data) {
		t.Errorf("stored registrations: got=%v want=%v", got.Data, wantTopics.Data)
	}
	want := before
	if remove {
		var pools struct {
			Pools  map[string]json.RawMessage `json:"pools"`
			Topics map[string]json.RawMessage `json:"topics"`
		}
		if err := json.Unmarshal([]byte(before), &pools); err != nil {
			t.Fatal(err)
		}
		delete(pools.Topics, "job.topic-mapping")
		data, err := json.Marshal(pools)
		if err != nil {
			t.Fatal(err)
		}
		want = string(data)
	}
	if got := poolPlatformPools(t, sw); got != want {
		t.Errorf("stored pools: got=%s want=%s", got, want)
	}
	wantMessages := 1
	if remove {
		wantMessages++
	}
	if got := len(sw.publishedSince(mark)); got != wantMessages {
		t.Errorf("published messages=%d want=%d", got, wantMessages)
	}
}

func topicMappingDocument(t *testing.T, sw *crossTenantSweep, id string) *configsvc.Document {
	t.Helper()
	doc, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeSystem, id)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
