package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	isoTenantA = "tenant-iso-a"
	isoTenantB = "tenant-iso-b"
	isoMarkerA = "MARKER-TENANT-ISO-A"
	isoMarkerB = "MARKER-TENANT-ISO-B"
)

func isoEvent(tenant, marker string, i int) SIEMEvent {
	return SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  tenant,
		JobID:     fmt.Sprintf("%s-job-%d", marker, i),
		Action:    fmt.Sprintf("%s-action-%d", marker, i),
		Decision:  "allow",
		Reason:    marker + " reason",
	}
}

// seedIsoChains interleaves both tenants in one time window.
func seedIsoChains(t *testing.T, chainer *Chainer, n int) (a, b []SIEMEvent) {
	t.Helper()
	for i := 0; i < n; i++ {
		for _, side := range []struct {
			tenant, marker string
			out            *[]SIEMEvent
		}{{isoTenantA, isoMarkerA, &a}, {isoTenantB, isoMarkerB, &b}} {
			ev := isoEvent(side.tenant, side.marker, i)
			if err := chainer.Append(context.Background(), &ev); err != nil {
				t.Fatalf("append %s[%d]: %v", side.tenant, i, err)
			}
			*side.out = append(*side.out, ev)
		}
	}
	return a, b
}

func eventHashes(events []SIEMEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.EventHash)
	}
	sort.Strings(out)
	return out
}

func eventActions(events []SIEMEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Action)
	}
	sort.Strings(out)
	return out
}

func assertChainHead(t *testing.T, client redis.UniversalClient, chainer *Chainer, tenant string, want []SIEMEvent) {
	t.Helper()
	raw, err := client.Get(context.Background(), chainer.HeadKey(tenant)).Result()
	if err != nil {
		t.Fatalf("read head %s: %v", tenant, err)
	}
	seq, hash, err := parseChainHead(raw)
	if err != nil {
		t.Fatalf("parse head %s: %v", tenant, err)
	}
	last := want[len(want)-1]
	if seq != int64(len(want)) || hash != last.EventHash {
		t.Fatalf("head %s = %d:%s, want %d:%s (its own last event)", tenant, seq, hash, len(want), last.EventHash)
	}
}

func exportedEvents(t *testing.T, client redis.UniversalClient, chainer *Chainer, tenant string) (string, []SIEMEvent, string) {
	t.Helper()
	var buf bytes.Buffer
	_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  tenant,
		StreamKey: chainer.StreamKey(tenant),
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-time.Hour),
		To:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("export %s: %v", tenant, err)
	}
	raw := buf.String()
	var manifestTenant string
	var events []SIEMEvent
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var line struct {
			Type     string `json:"type"`
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("export %s line: %v", tenant, err)
		}
		switch line.Type {
		case "manifest":
			manifestTenant = line.TenantID
		case "event":
			var ev SIEMEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				t.Fatalf("export %s event: %v", tenant, err)
			}
			events = append(events, ev)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read export %s: %v", tenant, err)
	}
	return manifestTenant, events, raw
}

func TestTenantIsolationComplianceExportChain(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	seededA, seededB := seedIsoChains(t, chainer, 3)

	assertChainHead(t, client, chainer, isoTenantA, seededA)
	assertChainHead(t, client, chainer, isoTenantB, seededB)

	for _, tc := range []struct {
		tenant, otherMarker string
		own, other          []SIEMEvent
	}{
		{isoTenantA, isoMarkerB, seededA, seededB},
		{isoTenantB, isoMarkerA, seededB, seededA},
	} {
		manifestTenant, events, raw := exportedEvents(t, client, chainer, tc.tenant)
		if manifestTenant != tc.tenant {
			t.Fatalf("export %s manifest tenant = %q", tc.tenant, manifestTenant)
		}
		if got, want := eventHashes(events), eventHashes(tc.own); !reflect.DeepEqual(got, want) {
			t.Fatalf("export %s event hashes = %v, want exactly its own %v", tc.tenant, got, want)
		}
		if got, want := eventActions(events), eventActions(tc.own); !reflect.DeepEqual(got, want) {
			t.Fatalf("export %s actions = %v, want exactly its own %v", tc.tenant, got, want)
		}
		for _, ev := range events {
			if ev.TenantID != tc.tenant {
				t.Fatalf("export %s carries an event of tenant %q", tc.tenant, ev.TenantID)
			}
		}
		for _, leak := range append([]string{tc.otherMarker}, eventHashes(tc.other)...) {
			if strings.Contains(raw, leak) {
				t.Fatalf("export %s contains the other tenant's %q", tc.tenant, leak)
			}
		}
	}
}

func holdIDs(holds []*LegalHold) []string {
	out := make([]string, 0, len(holds))
	for _, hold := range holds {
		out = append(out, hold.ID)
	}
	sort.Strings(out)
	return out
}

func listHoldIDs(t *testing.T, store *LegalHoldStore, tenant string) []string {
	t.Helper()
	holds, err := store.ListHolds(context.Background(), tenant)
	if err != nil {
		t.Fatalf("list holds %q: %v", tenant, err)
	}
	return holdIDs(holds)
}

func assertUnderHold(t *testing.T, store *LegalHoldStore, tenant string, want bool) {
	t.Helper()
	held, err := store.IsUnderHold(context.Background(), tenant)
	if err != nil || held != want {
		t.Fatalf("IsUnderHold(%s) = %v (err %v), want %v", tenant, held, err, want)
	}
}

func TestTenantIsolationLegalHoldStore(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	holdA, err := store.CreateHold(ctx, isoTenantA, isoMarkerA+" litigation", "admin-a")
	if err != nil {
		t.Fatalf("create hold A: %v", err)
	}
	holdB, err := store.CreateHold(ctx, isoTenantB, isoMarkerB+" litigation", "admin-b")
	if err != nil {
		t.Fatalf("create hold B while A holds one: %v", err)
	}
	if _, err := store.CreateHold(ctx, isoTenantA, "second", "admin-a"); !errors.Is(err, ErrHoldAlreadyExists) {
		t.Fatalf("second hold on A err = %v, want ErrHoldAlreadyExists", err)
	}
	beforeB, err := store.GetHold(ctx, holdB.ID)
	if err != nil {
		t.Fatalf("get hold B: %v", err)
	}

	if got := listHoldIDs(t, store, isoTenantA); !reflect.DeepEqual(got, []string{holdA.ID}) {
		t.Fatalf("list A = %v, want only A's hold %s", got, holdA.ID)
	}
	if got := listHoldIDs(t, store, isoTenantB); !reflect.DeepEqual(got, []string{holdB.ID}) {
		t.Fatalf("list B = %v, want only B's hold %s", got, holdB.ID)
	}
	// Scope fact: the unscoped store list spans tenants; the gateway list
	// handler filters it (TestTenantIsolationLegalHoldHandlers).
	if got, want := listHoldIDs(t, store, ""), holdIDs([]*LegalHold{holdA, holdB}); !reflect.DeepEqual(got, want) {
		t.Fatalf("unscoped list = %v, want both holds %v", got, want)
	}

	if err := store.ReleaseHold(ctx, holdA.ID, "admin-a"); err != nil {
		t.Fatalf("release A: %v", err)
	}
	assertUnderHold(t, store, isoTenantA, false)
	assertUnderHold(t, store, isoTenantB, true)
	afterB, err := store.GetHold(ctx, holdB.ID)
	if err != nil {
		t.Fatalf("get hold B after A's release: %v", err)
	}
	if !reflect.DeepEqual(afterB, beforeB) || !afterB.IsActive() || afterB.Reason != isoMarkerB+" litigation" {
		t.Fatalf("hold B after A's release = %+v, want unchanged %+v", afterB, beforeB)
	}
}

func chainStreamEvents(t *testing.T, client *redis.Client, chainer *Chainer, tenant string) []SIEMEvent {
	t.Helper()
	entries, err := client.XRange(context.Background(), chainer.StreamKey(tenant), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange %s: %v", tenant, err)
	}
	out := make([]SIEMEvent, 0, len(entries))
	for _, entry := range entries {
		raw, _ := entry.Values["event"].(string)
		var ev SIEMEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("decode %s stream entry: %v", tenant, err)
		}
		out = append(out, ev)
	}
	return out
}

// One install-level SIEM sink receives both tenants; only the chains are tenant-scoped.
func TestTenantIsolationSIEMConsumerChainsPerTenant(t *testing.T) {
	bus := &mockAuditBus{}
	sink := &mockExporter{}
	chainer, client := newConsumerChainer(t)
	if _, err := NewNATSAuditConsumer(bus, sink, WithChainer(chainer)); err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	handler := subscribedHandler(t, bus)
	for i := 0; i < 2; i++ {
		for _, side := range []struct{ tenant, marker string }{{isoTenantA, isoMarkerA}, {isoTenantB, isoMarkerB}} {
			if err := handler(packetFor(t, isoEvent(side.tenant, side.marker, i))); err != nil {
				t.Fatalf("handle %s[%d]: %v", side.tenant, i, err)
			}
		}
	}

	byTenant := map[string][]SIEMEvent{}
	for i := 0; i < 4; i++ {
		ev := exportedEvent(t, sink, i)
		byTenant[ev.TenantID] = append(byTenant[ev.TenantID], ev)
	}
	for _, tc := range []struct{ tenant, marker, otherMarker string }{
		{isoTenantA, isoMarkerA, isoMarkerB},
		{isoTenantB, isoMarkerB, isoMarkerA},
	} {
		exported := byTenant[tc.tenant]
		if len(exported) != 2 || exported[0].Seq != 1 || exported[1].Seq != 2 ||
			exported[0].PrevHash != "" || exported[1].PrevHash != exported[0].EventHash {
			t.Fatalf("sink events of %s = %+v, want seq 1,2 linked only to each other", tc.tenant, exported)
		}
		stream := chainStreamEvents(t, client, chainer, tc.tenant)
		if got, want := eventHashes(stream), eventHashes(exported); !reflect.DeepEqual(got, want) {
			t.Fatalf("chain %s hashes = %v, want exactly its exported events %v", tc.tenant, got, want)
		}
		for _, ev := range stream {
			if ev.TenantID != tc.tenant || !strings.HasPrefix(ev.Action, tc.marker) || strings.Contains(ev.Reason, tc.otherMarker) {
				t.Fatalf("chain %s holds a foreign event %+v", tc.tenant, ev)
			}
		}
	}
}
