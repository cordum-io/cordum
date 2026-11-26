// Package core is a docscheck fixture. It lives under testdata, so the go
// tool never builds it; docscheck only parses it.
package core

import (
	"flag"
	"os"
)

type config struct{}

func (config) String(name, def string) string { return envOrDefault(name, def) }

func envOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

var (
	_ = envOrDefault("FIX_VAR", "a")
	_ = envOrDefault("FIX_OK", "x")
	_ = firstEnv("FIX_OK", "FIX_OTHER_NAME")
	_ = envOrDefault("FIX_NONE", "")
	_ = envOrDefault("FIX_PROSE", "1")
	_ = os.Getenv("FIX_PLAIN")
	_ = envOrDefault("FIX_TWO", "p")
	_ = envOrDefault("FIX_TWO", "q")
	_ = flag.String("package-env", "FIX_ENV", "usage text")
)

func flagDefaults() {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	fs.String(
		"x-env", "FIX_ENV", "usage text",
	)
	var p string
	fs.StringVar(&p, "y-env", "FIX_ENV_VAR", "usage text")
	var cfg config
	cfg.String("FIX_HELPER", "h")
}
