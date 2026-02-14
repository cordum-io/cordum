package auth

import "testing"

func TestLoadAPIKeys(t *testing.T) {
	t.Setenv("CORDUM_API_KEY", "cordum")
	t.Setenv("API_KEY", "api")

	keys, required, _, _, _, err := loadBasicAPIKeys()
	if err != nil {
		t.Fatalf("load api keys: %v", err)
	}
	if !required {
		t.Fatalf("expected api key required")
	}
	if _, ok := keys["cordum"]; !ok {
		t.Fatalf("expected cordum api key in key map")
	}

	t.Setenv("CORDUM_API_KEY", "")
	keys, _, _, _, _, err = loadBasicAPIKeys()
	if err != nil {
		t.Fatalf("load api keys: %v", err)
	}
	if _, ok := keys["api"]; !ok {
		t.Fatalf("expected api key in key map")
	}
}
