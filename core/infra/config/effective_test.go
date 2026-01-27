package config

import "testing"

func TestParseEffectiveSafety(t *testing.T) {
	cfg, ok := ParseEffectiveSafety([]byte(`{"safety":{"allowed_topics":["job.*"],"denied_topics":["job.bad"]}}`))
	if !ok {
		t.Fatalf("expected safety config")
	}
	if len(cfg.AllowedTopics) != 1 || cfg.AllowedTopics[0] != "job.*" {
		t.Fatalf("unexpected allowed topics: %#v", cfg.AllowedTopics)
	}
	if len(cfg.DeniedTopics) != 1 || cfg.DeniedTopics[0] != "job.bad" {
		t.Fatalf("unexpected denied topics: %#v", cfg.DeniedTopics)
	}

	nested, ok := ParseEffectiveSafety([]byte(`{"data":{"safety":{"allowed_topics":["job.nested"]}}}`))
	if !ok || len(nested.AllowedTopics) != 1 || nested.AllowedTopics[0] != "job.nested" {
		t.Fatalf("unexpected nested safety config: %#v", nested)
	}
}

func TestEffectiveSafetyFromEnv(t *testing.T) {
	env := map[string]string{
		EffectiveConfigEnvVar: `{"safety":{"denied_topics":["job.block"]}}`,
	}
	cfg, ok := EffectiveSafetyFromEnv(env)
	if !ok {
		t.Fatalf("expected safety config")
	}
	if len(cfg.DeniedTopics) != 1 || cfg.DeniedTopics[0] != "job.block" {
		t.Fatalf("unexpected denied topics: %#v", cfg.DeniedTopics)
	}

}

func TestParseEffectiveContext(t *testing.T) {
	cfg, ok := ParseEffectiveContext([]byte(`{"context":{"allowed_memory_ids":["repo:*"],"denied_memory_ids":["kb:secret"]}}`))
	if !ok {
		t.Fatalf("expected context config")
	}
	if len(cfg.AllowedMemoryIDs) != 1 || cfg.AllowedMemoryIDs[0] != "repo:*" {
		t.Fatalf("unexpected allowed memory ids")
	}
	if len(cfg.DeniedMemoryIDs) != 1 || cfg.DeniedMemoryIDs[0] != "kb:secret" {
		t.Fatalf("unexpected denied memory ids")
	}
}

func TestMemoryIDAllowed(t *testing.T) {
	cfg := ContextConfig{
		AllowedMemoryIDs: []string{"repo:*", "kb:public"},
		DeniedMemoryIDs:  []string{"repo:private/*"},
	}
	if ok, _ := MemoryIDAllowed(cfg, "repo:private/alpha"); ok {
		t.Fatalf("expected deny to win over allow")
	}
	if ok, _ := MemoryIDAllowed(cfg, "repo:public"); !ok {
		t.Fatalf("expected allow match")
	}
	if ok, _ := MemoryIDAllowed(cfg, "kb:secret"); ok {
		t.Fatalf("expected allowlist to block unmatched id")
	}
}
