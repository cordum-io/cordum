package edge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRegistryCreate(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)

	want := validMCPUpstream("tenant-a", "tenant-tools")
	if err := registry.Create(ctx, &want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := registry.Get(ctx, "tenant-a", "tenant-tools")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got == nil {
		t.Fatalf("Get found=%v got=%#v", ok, got)
	}
	assertMCPUpstreamEqual(t, *got, want)
}

func TestRegistryCreateRejectsRawSecret(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)

	upstream := validMCPUpstream("tenant-a", "raw-secret")
	upstream.AuthSecretRef = "sk-test-raw-token"
	err := registry.Create(ctx, &upstream)
	if !errors.Is(err, ErrSecretMustUseRef) {
		t.Fatalf("Create error = %v, want ErrSecretMustUseRef", err)
	}
	if got, ok, err := registry.Get(ctx, "tenant-a", "raw-secret"); err != nil || ok || got != nil {
		t.Fatalf("raw secret record persisted: got=%#v ok=%v err=%v", got, ok, err)
	}
}

func TestRegistryCreateAcceptsSecretRef(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)

	upstream := validMCPUpstream("tenant-a", "secret-ref")
	upstream.AuthSecretRef = "secret://vault/api-key"
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create with secret ref: %v", err)
	}
	got, ok, err := registry.Get(ctx, "tenant-a", "secret-ref")
	if err != nil || !ok || got.AuthSecretRef != upstream.AuthSecretRef {
		t.Fatalf("Get AuthSecretRef = %q ok=%v err=%v", got.AuthSecretRef, ok, err)
	}
}

func TestRegistryList(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)
	for _, upstream := range []UpstreamServer{
		validMCPUpstream("tenant-a", "tenant-a-tools"),
		validMCPUpstream("tenant-b", "tenant-b-tools"),
		validMCPUpstream("*", "system-tools"),
	} {
		if err := registry.Create(ctx, &upstream); err != nil {
			t.Fatalf("Create %s/%s: %v", upstream.TenantID, upstream.Name, err)
		}
	}

	got, err := registry.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := mcpUpstreamNames(got)
	if !sameStringSet(names, []string{"system-tools", "tenant-a-tools"}) {
		t.Fatalf("List tenant-a names = %v", names)
	}
}

func TestRegistryListTenantIsolation(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)
	tenantB := validMCPUpstream("tenant-b", "tenant-b-only")
	if err := registry.Create(ctx, &tenantB); err != nil {
		t.Fatalf("Create tenant-b: %v", err)
	}

	got, err := registry.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List tenant-a: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("tenant-a saw tenant-b records: %#v", got)
	}
}

func TestRegistryListRedactsSecrets(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-a", "redacted")
	upstream.AuthSecretRef = "secret://vault/mcp-token"
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := registry.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(payload)
	if !strings.Contains(body, "secret://vault/mcp-token") {
		t.Fatalf("response omitted secret ref contract: %s", body)
	}
	if strings.Contains(body, "sk-test-raw-token") || strings.Contains(body, "bearer raw") {
		t.Fatalf("response leaked raw secret material: %s", body)
	}
}

func TestRegistryGetByName(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-a", "tenant-tools")
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, ok, err := registry.Get(ctx, "tenant-b", "tenant-tools"); err != nil || ok || got != nil {
		t.Fatalf("cross-tenant Get = got=%#v ok=%v err=%v", got, ok, err)
	}
	got, ok, err := registry.Get(ctx, "tenant-a", "tenant-tools")
	if err != nil || !ok || got.Name != "tenant-tools" {
		t.Fatalf("same-tenant Get = got=%#v ok=%v err=%v", got, ok, err)
	}
}

func TestRegistryDisable(t *testing.T) {
	ctx, registry, _ := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-a", "disable-me")
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := registry.Disable(ctx, "tenant-a", "disable-me"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	got, ok, err := registry.Get(ctx, "tenant-a", "disable-me")
	if err != nil || !ok || got.Enabled {
		t.Fatalf("Get disabled = got=%#v ok=%v err=%v", got, ok, err)
	}
}

func TestRegistryUpdateBacksUp(t *testing.T) {
	ctx, registry, client := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-a", "tenant-tools")
	upstream.Endpoint = "https://old.example/mcp"
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	upstream.Endpoint = "https://new.example/mcp"
	if err := registry.Update(ctx, &upstream); err != nil {
		t.Fatalf("Update: %v", err)
	}

	keys, err := client.Keys(ctx, "edge:mcp:upstream:bak:tenant-a:tenant-tools:*").Result()
	if err != nil {
		t.Fatalf("Keys backup: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("backup keys = %v, want one", keys)
	}
	backup, err := client.Get(ctx, keys[0]).Result()
	if err != nil {
		t.Fatalf("Get backup: %v", err)
	}
	if !strings.Contains(backup, "https://old.example/mcp") || strings.Contains(backup, "https://new.example/mcp") {
		t.Fatalf("backup payload = %s", backup)
	}
}

func TestRegistryValidationRejectsLoopbackInStrict(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "local")
	upstream.Endpoint = "http://localhost:8080"
	err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeEnterpriseStrict), []string{"local"})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("Validate error = %v, want ErrUnsafeEndpoint", err)
	}
}

func TestRegistryValidationRejectsShellMetacharsInStdioCommand(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "bad-stdio")
	upstream.Transport = "stdio"
	upstream.Endpoint = ""
	upstream.Command = []string{"cordum-mcp; rm -rf /"}
	err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeObserve), []string{"bad-stdio"})
	if !errors.Is(err, ErrShellMetacharsRejected) {
		t.Fatalf("Validate error = %v, want ErrShellMetacharsRejected", err)
	}
}

func TestRegistryEnterpriseStrictAllowlistGate(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "not-approved")
	err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeEnterpriseStrict), []string{"approved"})
	if !errors.Is(err, ErrUpstreamNotAllowlisted) {
		t.Fatalf("Validate error = %v, want ErrUpstreamNotAllowlisted", err)
	}
}

func newMCPUpstreamRegistryForTest(t *testing.T) (context.Context, MCPUpstreamRegistry, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close() })
	return context.Background(), NewRedisMCPUpstreamRegistryFromClient(client), client
}

func validMCPUpstream(tenantID, name string) UpstreamServer {
	return UpstreamServer{
		Name:          name,
		Transport:     "http",
		Endpoint:      "https://mcp.example.com/" + name,
		TenantID:      tenantID,
		AuthSecretRef: "secret://vault/" + name,
		Labels:        map[string]string{"team": "platform"},
		Risk:          "medium",
		Enabled:       true,
	}
}

func assertMCPUpstreamEqual(t *testing.T, got, want UpstreamServer) {
	t.Helper()
	if got.Name != want.Name || got.Transport != want.Transport || got.Endpoint != want.Endpoint || got.TenantID != want.TenantID || got.AuthSecretRef != want.AuthSecretRef || got.Risk != want.Risk || got.Enabled != want.Enabled {
		t.Fatalf("upstream mismatch got=%#v want=%#v", got, want)
	}
	if got.Labels["team"] != want.Labels["team"] {
		t.Fatalf("labels mismatch got=%v want=%v", got.Labels, want.Labels)
	}
}

func mcpUpstreamNames(items []UpstreamServer) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
