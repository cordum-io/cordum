package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordum/cordum/tools/certgen"
)

const (
	defaultComposeTimeoutSeconds = "1800"
)

type devBootstrapResult struct {
	EnvPath   string
	APIKey    string
	Tenant    string
	AddedKeys []string
}

func runUpCmd(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	file := fs.String("file", "docker-compose.yml", "compose file path")
	build := fs.Bool("build", true, "build images before starting")
	detach := fs.Bool("detach", true, "run in background")
	dev := fs.Bool("dev", false, "bootstrap local dev credentials in .env")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	composeDir := filepath.Dir(*file)
	var source string
	var devBoot devBootstrapResult

	if *dev {
		ensureDevCerts(composeDir)
		var err error
		devBoot, err = ensureDevBootstrapEnv(composeDir)
		if err != nil {
			fail(fmt.Sprintf("dev bootstrap .env: %v", err))
		}
		source = "dev-bootstrap"
	} else {
		source = apiKeySource(*file)
		if source == "" {
			fail("CORDUM_API_KEY is required (set it in the environment or .env before running, or pass --dev)")
		}
		ensureDevCerts(composeDir)
	}

	if err := runCompose(*file, *build, *detach); err != nil {
		fail(err.Error())
	}

	caPath := filepath.ToSlash(filepath.Join(filepath.Dir(*file), "certs", "ca", "ca.crt"))
	fmt.Println("Cordum stack started.")
	fmt.Println("Gateway: " + defaultGateway + " (CA: " + caPath + ")")
	fmt.Println("Dashboard: http://localhost:8082")
	if source != "" {
		fmt.Println("API Key: configured (value hidden)")
		fmt.Println("Status: curl -sS --cacert " + caPath + " " + defaultGateway + "/api/v1/status -H \"X-API-Key: <your-key>\" -H \"X-Tenant-ID: <tenant>\"")
		if *dev {
			if len(devBoot.AddedKeys) > 0 {
				fmt.Printf("Dev .env: initialized %s (%s)\n", devBoot.EnvPath, strings.Join(devBoot.AddedKeys, ", "))
			} else {
				fmt.Printf("Dev .env: preserved existing values in %s\n", devBoot.EnvPath)
			}
		} else {
			fmt.Println("Smoke (from repo root): CORDUM_API_KEY=<your-key> CORDUM_TENANT_ID=<tenant> ./tools/scripts/platform_smoke.sh")
		}
	}
}

func runCompose(composeFile string, build, detach bool) error {
	if composeFile == "" {
		return fmt.Errorf("compose file required")
	}
	if _, err := os.Stat(composeFile); err != nil {
		return fmt.Errorf("compose file not found: %s", composeFile)
	}

	args := []string{"-f", composeFile, "up"}
	if detach {
		args = append(args, "-d")
	}
	if build {
		args = append(args, "--build")
	}

	if err := runDockerCompose(args); err == nil {
		return nil
	} else if _, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("docker compose failed: %w", err)
	}

	return fmt.Errorf("docker compose unavailable")
}

func runDockerCompose(args []string) error {
	if path, err := exec.LookPath("docker"); err == nil {
		// #nosec G204 -- args are constructed from validated CLI flags.
		cmd := exec.Command(path, append([]string{"compose"}, args...)...)
		cmd.Env = composeEnv()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if path, err := exec.LookPath("docker-compose"); err == nil {
		// #nosec G204 -- args are constructed from validated CLI flags.
		cmd := exec.Command(path, args...)
		cmd.Env = composeEnv()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("docker compose binary not found")
}

func composeEnv() []string {
	env := os.Environ()
	hasComposeTimeout := os.Getenv("COMPOSE_HTTP_TIMEOUT") != ""
	hasDockerTimeout := os.Getenv("DOCKER_CLIENT_TIMEOUT") != ""
	if !hasComposeTimeout {
		env = append(env, "COMPOSE_HTTP_TIMEOUT="+defaultComposeTimeoutSeconds)
	}
	if !hasDockerTimeout {
		env = append(env, "DOCKER_CLIENT_TIMEOUT="+defaultComposeTimeoutSeconds)
	}
	return env
}

func apiKeySource(composeFile string) string {
	if val := firstNonEmptyEnv("CORDUM_API_KEY", "API_KEY"); val != "" {
		return "env"
	}
	envPath := ".env"
	if composeFile != "" {
		envPath = filepath.Join(filepath.Dir(composeFile), ".env")
	}
	if val := readEnvFile(envPath, "CORDUM_API_KEY", "API_KEY"); val != "" {
		return "file"
	}
	return ""
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			return val
		}
	}
	return ""
}

func readEnvFile(path string, keys ...string) string {
	// #nosec G304 -- reads local .env file for CLI convenience.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		_ = f.Close()
	}()
	allowed := map[string]struct{}{}
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if _, ok := allowed[key]; !ok {
			continue
		}
		return trimQuotes(val)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("readEnvFile: scan error", "path", path, "error", err)
	}
	return ""
}

func trimQuotes(val string) string {
	if len(val) < 2 {
		return val
	}
	if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
		return val[1 : len(val)-1]
	}
	return val
}

// ensureDevCerts generates TLS certificates if they don't already exist.
func ensureDevCerts(composeDir string) {
	certsDir := filepath.Join(composeDir, "certs")
	caPath := filepath.Join(certsDir, "ca", "ca.crt")
	if _, err := os.Stat(caPath); err == nil {
		return // certs already exist
	}
	fmt.Printf("Generating TLS certificates in %s ...\n", certsDir)
	if err := certgen.GenerateAll(certgen.Options{BaseDir: certsDir}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: certificate generation failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "  run 'cordumctl generate-certs' manually")
	}
}

func ensureDevBootstrapEnv(composeDir string) (devBootstrapResult, error) {
	envPath := filepath.Join(composeDir, ".env")
	var existing string
	if data, err := os.ReadFile(envPath); err == nil { // #nosec G304 -- local compose directory .env managed by CLI.
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return devBootstrapResult{}, fmt.Errorf("read %s: %w", envPath, err)
	}

	existingMap := parseEnvContent(existing)
	hasKey := func(k string) (string, bool) {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, true
		}
		if v := strings.TrimSpace(existingMap[k]); v != "" {
			return v, true
		}
		return "", false
	}

	apiKey, ok := hasKey("CORDUM_API_KEY")
	var missing []string
	var added []string
	if !ok {
		if fallback, okFallback := hasKey("API_KEY"); okFallback {
			apiKey = fallback
		} else {
			hexPart, err := randomHexString(16)
			if err != nil {
				return devBootstrapResult{}, fmt.Errorf("generate CORDUM_API_KEY: %w", err)
			}
			apiKey = "cordum_dev_" + hexPart
		}
		missing = append(missing, "CORDUM_API_KEY="+apiKey)
		added = append(added, "CORDUM_API_KEY")
	}

	type kvDefault struct {
		key string
		val func() (string, error)
	}
	defaults := []kvDefault{
		{
			key: "REDIS_PASSWORD",
			val: func() (string, error) {
				hexPart, err := randomHexString(12)
				if err != nil {
					return "", err
				}
				return "cordum_redis_" + hexPart, nil
			},
		},
		{
			key: "CORDUM_ADMIN_USERNAME",
			val: func() (string, error) { return "admin", nil },
		},
		{
			key: "CORDUM_ADMIN_PASSWORD",
			val: func() (string, error) {
				hexPart, err := randomHexString(12)
				if err != nil {
					return "", err
				}
				return "cordum_admin_" + hexPart, nil
			},
		},
		{
			key: "CORDUM_MCP_ENABLED",
			val: func() (string, error) { return "true", nil },
		},
		{
			key: "CORDUM_MCP_POLICY_GATE_ENABLED",
			val: func() (string, error) { return "true", nil },
		},
	}

	for _, item := range defaults {
		if _, exists := hasKey(item.key); exists {
			continue
		}
		val, err := item.val()
		if err != nil {
			return devBootstrapResult{}, fmt.Errorf("generate %s: %w", item.key, err)
		}
		missing = append(missing, item.key+"="+val)
		added = append(added, item.key)
	}

	if len(missing) > 0 {
		var b strings.Builder
		if existing != "" {
			b.WriteString(existing)
			if !strings.HasSuffix(existing, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		} else {
			b.WriteString("# Generated by `cordumctl up --dev`\n")
		}
		for _, line := range missing {
			b.WriteString(line)
			b.WriteString("\n")
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
			return devBootstrapResult{}, fmt.Errorf("write %s: %w", envPath, err)
		}
		if err := os.Chmod(envPath, 0o600); err != nil {
			return devBootstrapResult{}, fmt.Errorf("chmod %s: %w", envPath, err)
		}
	}

	tenant, okTenant := hasKey("CORDUM_TENANT_ID")
	if !okTenant || tenant == "" {
		tenant = "default"
	}

	return devBootstrapResult{
		EnvPath:   envPath,
		APIKey:    apiKey,
		Tenant:    tenant,
		AddedKeys: added,
	}, nil
}

func parseEnvContent(content string) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = trimQuotes(strings.TrimSpace(val))
	}
	return out
}

func randomHexString(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
