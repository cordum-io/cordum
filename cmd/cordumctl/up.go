package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultComposeTimeoutSeconds = "1800"
)

func runUpCmd(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	file := fs.String("file", "docker-compose.yml", "compose file path")
	build := fs.Bool("build", true, "build images before starting")
	detach := fs.Bool("detach", true, "run in background")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	source := apiKeySource(*file)
	if source == "" {
		fail("CORDUM_API_KEY is required (set it in the environment or .env before running)")
	}

	if err := runCompose(*file, *build, *detach); err != nil {
		fail(err.Error())
	}

	fmt.Println("Cordum stack started.")
	fmt.Println("Gateway: http://localhost:8081")
	fmt.Println("Dashboard: http://localhost:8082")
	if source != "" {
		fmt.Println("API Key: configured (value hidden)")
		fmt.Println("Status: curl -sS http://localhost:8081/api/v1/status -H \"X-API-Key: <your-key>\" -H \"X-Tenant-ID: <tenant>\"")
		fmt.Println("Smoke (from repo root): CORDUM_API_KEY=<your-key> CORDUM_TENANT_ID=<tenant> ./tools/scripts/platform_smoke.sh")
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
