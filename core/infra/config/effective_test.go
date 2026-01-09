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
