package safeexec

import "testing"

func TestDevAllowEnvCannotWeakenProdLock(t *testing.T) {
	dev, err := SanitizeEnv([]string{
		"CORDUM_DEV_ALLOW_ENV=SECRET_TOKEN",
		"SECRET_TOKEN=dev-ok",
	}, nil)
	if err != nil {
		t.Fatalf("dev SanitizeEnv returned error: %v", err)
	}
	if got := envMap(dev)["SECRET_TOKEN"]; got != "dev-ok" {
		t.Fatalf("dev SECRET_TOKEN=%q, want dev-ok", got)
	}

	prod, err := SanitizeEnv([]string{
		"CORDUM_DEV_ALLOW_ENV=*",
		"CORDUM_HOOK_PROD_LOCK=1",
		"SECRET_TOKEN=must-strip",
	}, nil)
	if err != nil {
		t.Fatalf("prod SanitizeEnv returned error: %v", err)
	}
	if _, ok := envMap(prod)["SECRET_TOKEN"]; ok {
		t.Fatalf("prod lock kept SECRET_TOKEN: %#v", prod)
	}
}

func TestProdLockStripsPathEvenWhenDevAllowsIt(t *testing.T) {
	dev, err := SanitizeEnv([]string{
		"CORDUM_DEV_ALLOW_ENV=PATH",
		"PATH=/tmp/dev-bin",
	}, nil)
	if err != nil {
		t.Fatalf("dev SanitizeEnv returned error: %v", err)
	}
	if got := envMap(dev)["PATH"]; got != "/tmp/dev-bin" {
		t.Fatalf("dev PATH=%q, want /tmp/dev-bin", got)
	}

	prod, err := SanitizeEnv([]string{
		"CORDUM_DEV_ALLOW_ENV=PATH",
		"CORDUM_HOOK_PROD_LOCK=1",
		"PATH=/tmp/evil-bin",
	}, nil)
	if err != nil {
		t.Fatalf("prod SanitizeEnv returned error: %v", err)
	}
	if _, ok := envMap(prod)["PATH"]; ok {
		t.Fatalf("prod lock kept PATH: %#v", prod)
	}
}
