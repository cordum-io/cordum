package gateway

// ActionHash tests (task-2cdf90ac, M1-R03B). An MCP approval binds the
// original canonical tool-call arguments, on the legacy registry gate
// (args_hash) and on the policy-gate pipeline (BuildMCPApprovalBinding's
// InputHash) alike. Every stored or served copy of a held call's arguments
// stays redacted: the legacy approval record, the mcp.tool.pre event, Redis
// and the audit stream never hold a value inside a redacted span.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

// actionHashSeed returns a fresh synthetic secret for a `password` field.
func actionHashSeed(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return "synthetic-" + hex.EncodeToString(b)
}

// redisKeysContaining returns the keys whose name or value, of any Redis
// type, contains needle.
func redisKeysContaining(t *testing.T, client redis.UniversalClient, needle string) []string {
	t.Helper()
	ctx := context.Background()
	var hits []string
	iter := client.Scan(ctx, 0, "*", 1000).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		val, err := redisValueText(ctx, client, key)
		if err != nil {
			t.Fatalf("read redis key %s: %v", key, err)
		}
		if strings.Contains(key, needle) || strings.Contains(val, needle) {
			hits = append(hits, key)
		}
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("scan redis: %v", err)
	}
	return hits
}

func redisValueText(ctx context.Context, c redis.UniversalClient, key string) (string, error) {
	typ, err := c.Type(ctx, key).Result()
	if err != nil {
		return "", err
	}
	var v any
	switch typ {
	case "none": // expired between SCAN and TYPE
		return "", nil
	case "string":
		v, err = c.Get(ctx, key).Result()
	case "hash":
		v, err = c.HGetAll(ctx, key).Result()
	case "list":
		v, err = c.LRange(ctx, key, 0, -1).Result()
	case "set":
		v, err = c.SMembers(ctx, key).Result()
	case "zset":
		v, err = c.ZRangeWithScores(ctx, key, 0, -1).Result()
	case "stream":
		v, err = c.XRange(ctx, key, "-", "+").Result()
	default:
		return "", fmt.Errorf("unhandled redis type %q", typ)
	}
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return fmt.Sprint(v), err
}

// auditEventsContaining counts the events whose JSON contains needle.
func auditEventsContaining(t *testing.T, events []audit.SIEMEvent, needle string) int {
	t.Helper()
	n := 0
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal audit event: %v", err)
		}
		if bytes.Contains(raw, []byte(needle)) {
			n++
		}
	}
	return n
}

// TestMCPActionHashLegacyGateMatchesPipeline: for the same call, the legacy
// gate's args_hash equals the pipeline's InputHash. Two calls that redact to
// the same descriptor but differ inside the redacted span get different
// identities on both paths; formatting and key order do not change it.
func TestMCPActionHashLegacyGateMatchesPipeline(t *testing.T) {
	t.Parallel()
	seedA, seedB := actionHashSeed(t), actionHashSeed(t)
	calls := map[string]string{
		"A":  `{"record":"fixture","password":"` + seedA + `"}`,
		"A2": "{ \"password\" : \"" + seedA + "\",\n  \"record\": \"fixture\" }",
		"B":  `{"record":"fixture","password":"` + seedB + `"}`,
	}
	legacy, pipeline := map[string]string{}, map[string]string{}
	for name, args := range calls {
		hash, err := canonicalArgsHash(json.RawMessage(args))
		if err != nil || hash == "" {
			t.Fatalf("legacy args_hash(%s) = %q, %v", name, hash, err)
		}
		_, inputHash := mcp.BuildMCPApprovalBinding(agrTenant, "cordum",
			mcp.ToolCallParams{Name: agrToolName, Arguments: json.RawMessage(args)}, "")
		legacy[name], pipeline[name] = hash, inputHash
		if hash != inputHash {
			t.Errorf("call %s: legacy args_hash %s != pipeline InputHash %s", name, hash, inputHash)
		}
	}
	for path, ids := range map[string]map[string]string{"legacy": legacy, "pipeline": pipeline} {
		if ids["A"] != ids["A2"] {
			t.Errorf("%s: reformatted call A2 identity %s != A %s", path, ids["A2"], ids["A"])
		}
		if ids["A"] == ids["B"] {
			t.Errorf("%s: calls A and B share identity %s; they differ inside the redacted span", path, ids["A"])
		}
	}

	redactor := mcp.DefaultRedactor()
	redA := redactor.Redact(json.RawMessage(calls["A"]))
	redB := redactor.Redact(json.RawMessage(calls["B"]))
	if !bytes.Equal(redA, redB) {
		t.Fatalf("redacted A %s != redacted B %s; the fixture must differ only inside a redacted span", redA, redB)
	}
	descriptorHash := func(redacted json.RawMessage) string {
		d, err := mcp.BuildActionDescriptorFromToolCall(mcp.CallMetadata{Tenant: agrTenant},
			mcp.ToolCallParams{Name: agrToolName, Arguments: redacted}, "cordum")
		if err != nil {
			t.Fatalf("descriptor: %v", err)
		}
		return actiongates.CanonicalActionHash(d)
	}
	if a, b := descriptorHash(redA), descriptorHash(redB); a == "" || a != b {
		t.Errorf("redacted descriptors of A and B hash %q and %q; want equal", a, b)
	}
}

// TestMCPActionHashApprovalRecordStoresRedactedArgs: the legacy gate stores
// the held call's arguments in redacted form while args_hash binds the
// original arguments, and the seeded value reaches neither Redis nor audit.
func TestMCPActionHashApprovalRecordStoresRedactedArgs(t *testing.T) {
	t.Parallel()
	seed := actionHashSeed(t)
	args := json.RawMessage(`{"record":"fixture","password":"` + seed + `"}`)
	var (
		mu     sync.Mutex
		events []audit.SIEMEvent
	)
	store := newTestMCPStore(t).WithAuditHook(func(e audit.SIEMEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t", AgentID: "a", Principal: "alice"})
	held, err := NewGatewayApprovalGate(store).Check(ctx, mcp.Tool{Name: "db.delete_record", RequiresApproval: true}, args)
	if err != nil || held == nil {
		t.Fatalf("Check = %+v, %v; want an approval hold", held, err)
	}
	rec, err := store.Get(ctx, held.ApprovalID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(rec.ArgsJSON, &stored); err != nil {
		t.Fatalf("stored args %s: %v", rec.ArgsJSON, err)
	}
	if stored["password"] != "[REDACTED:password]" || stored["record"] != "fixture" {
		t.Errorf("stored args = %s; want password [REDACTED:password] and record fixture", rec.ArgsJSON)
	}
	original, err := canonicalArgsHash(args)
	if err != nil {
		t.Fatalf("hash original: %v", err)
	}
	storedHash, err := canonicalArgsHash(rec.ArgsJSON)
	if err != nil {
		t.Fatalf("hash stored: %v", err)
	}
	if rec.ArgsHash != original || held.ArgsHash != original {
		t.Errorf("args_hash record=%s hold=%s; want the original arguments' %s", rec.ArgsHash, held.ArgsHash, original)
	}
	if rec.ArgsHash == storedHash {
		t.Errorf("args_hash %s binds the stored arguments; want the original arguments", rec.ArgsHash)
	}
	if keys := redisKeysContaining(t, store.client, seed); len(keys) != 0 {
		t.Errorf("seeded value stored in redis keys %q", keys)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no audit events recorded; want the enqueued event")
	}
	if n := auditEventsContaining(t, events, seed); n != 0 {
		t.Errorf("%d of %d audit events carry the seeded value", n, len(events))
	}
}

// TestMCPActionHashHeldCallKeepsArgsRedacted: on the policy-gate pipeline a
// held, approved and resumed call runs once, its mcp.tool.pre event shows the
// redacted value, and the seeded value reaches neither Redis nor audit.
// Not parallel: rpaNewServer uses t.Setenv.
func TestMCPActionHashHeldCallKeepsArgsRedacted(t *testing.T) {
	h := rpaNewServer(t)
	seed := actionHashSeed(t)
	args := `{"widget":"w-hash","password":"` + seed + `"}`
	ref := h.heldAndApprovedTool(t, rpaDeleteTool, args)
	if n := h.deletes.Load(); n != 0 {
		t.Fatalf("deletes after hold and approve = %d, want 0", n)
	}
	if r := h.resumeTool(t, rpaDeleteTool, args, ref); !r.succeeded(rpaDeleteTool + " ran") {
		t.Errorf("resume = %s; want the tool's own result", r)
	}
	if n := h.deletes.Load(); n != 1 {
		t.Errorf("deletes after resume = %d, want 1", n)
	}
	pre := 0
	for _, e := range h.events(t, edge.EventKindMCPToolPre) {
		if e.ToolName != rpaDeleteTool {
			continue
		}
		pre++
		if got := e.InputRedacted["password"]; got != "[REDACTED:password]" {
			t.Errorf("mcp.tool.pre %s password = %v, want [REDACTED:password]", e.EventID, got)
		}
	}
	if pre == 0 {
		t.Error("no mcp.tool.pre event for the held call")
	}
	if len(redisKeysContaining(t, h.redis, ref)) == 0 {
		t.Fatalf("no redis key holds approval %s; the scan does not see the Edge store", ref)
	}
	if keys := redisKeysContaining(t, h.redis, seed); len(keys) != 0 {
		t.Errorf("seeded value stored in redis keys %q", keys)
	}
	h.sink.mu.Lock()
	events := append([]audit.SIEMEvent(nil), h.sink.events...)
	h.sink.mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no audit events recorded")
	}
	if n := auditEventsContaining(t, events, seed); n != 0 {
		t.Errorf("%d of %d audit events carry the seeded value", n, len(events))
	}
}
