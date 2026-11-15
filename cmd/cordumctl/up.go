package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/cordum/cordum/sdk/client"
	"github.com/cordum/cordum/tools/certgen"
)

const (
	defaultComposeTimeoutSeconds = "1800"
	defaultDevGatewayURL         = "https://localhost:8081"
	defaultDevDashboardURL       = "http://localhost:8082"
	defaultDevMCPReadyTimeout    = 45 * time.Second
)

type devBootstrapResult struct {
	EnvPath    string
	APIKey     string
	Tenant     string
	Gateway    string
	CACert     string
	AddedKeys  []string
	ConfigPath string
	ConfigNew  bool
}

func runUpCmd(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	file := fs.String("file", "docker-compose.yml", "compose file path")
	build := fs.Bool("build", true, "build images before starting")
	detach := fs.Bool("detach", true, "run in background")
	dev := fs.Bool("dev", false, "bootstrap local dev credentials, CLI context, and MCP policy gate")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	fileExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "file" {
			fileExplicit = true
		}
	})

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
		cfgPath, created, err := ensureDevCLIContext(composeDir, devBoot.Tenant)
		if err != nil {
			fail(fmt.Sprintf("dev bootstrap cordum.yaml: %v", err))
		}
		devBoot.ConfigPath = cfgPath
		devBoot.ConfigNew = created
		devBoot.Gateway = defaultDevGatewayURL
		devBoot.CACert = detectInitCACert("", devBoot.Gateway, composeDir)
		source = "dev-bootstrap"
	} else {
		source = apiKeySource(*file)
		if source == "" {
			fail("CORDUM_API_KEY is required (set it in the environment or .env before running, or pass --dev)")
		}
		ensureDevCerts(composeDir)
	}

	composeFiles := resolveUpComposeFiles(*file, *dev, fileExplicit)
	if err := runComposeFiles(composeFiles, *build, *detach); err != nil {
		fail(err.Error())
	}

	if *dev && *detach {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDevMCPReadyTimeout)
		defer cancel()
		if err := waitAndSeedDevMCP(ctx, devBoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not seed MCP policy gate config yet (%v); Gateway env defaults still enable MCP policy gate on dev builds\n", err)
		}
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
			if devBoot.ConfigNew {
				fmt.Printf("CLI context: wrote %s\n", devBoot.ConfigPath)
			} else {
				fmt.Printf("CLI context: preserved existing %s\n", devBoot.ConfigPath)
			}
			fmt.Println("Next steps:")
			fmt.Println("  cordumctl status")
			fmt.Println("  cordumctl edge claude --no-launch")
			fmt.Println("  ./tools/scripts/demo_guardrails_run.sh")
		} else {
			fmt.Println("Smoke (from repo root): CORDUM_API_KEY=<your-key> CORDUM_TENANT_ID=<tenant> ./tools/scripts/platform_smoke.sh")
		}
	}
}

func resolveUpComposeFiles(composeFile string, dev, fileExplicit bool) []string {
	files := []string{composeFile}
	if dev && !fileExplicit {
		devOverride := filepath.Join(filepath.Dir(composeFile), "docker-compose.dev.yml")
		if info, err := os.Stat(devOverride); err == nil && !info.IsDir() {
			files = append(files, devOverride)
		}
	}
	return files
}

func runCompose(composeFile string, build, detach bool) error {
	return runComposeFiles([]string{composeFile}, build, detach)
}

func runComposeFiles(composeFiles []string, build, detach bool) error {
	if len(composeFiles) == 0 || composeFiles[0] == "" {
		return fmt.Errorf("compose file required")
	}
	args := make([]string, 0, len(composeFiles)*2+4)
	for _, composeFile := range composeFiles {
		if _, err := os.Stat(composeFile); err != nil {
			return fmt.Errorf("compose file not found: %s", composeFile)
		}
		args = append(args, "-f", composeFile)
	}

	args = append(args, "up")
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
	// #nosec G304 -- local compose directory .env managed by CLI.
	data, err := os.ReadFile(envPath)
	if err == nil {
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
			existing, missing = fillEmptyEnvDefinitions(existing, missing)
			b.WriteString(existing)
			if len(missing) > 0 {
				if !strings.HasSuffix(existing, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("\n")
			}
		} else {
			b.WriteString("# Generated by `cordumctl up --dev`\n")
		}
		for _, line := range missing {
			b.WriteString(line)
			b.WriteString("\n")
		}
		// os.WriteFile keeps an existing file's mode, so restrict .env before it gets credentials.
		if err := os.Chmod(envPath, 0o600); err != nil && !os.IsNotExist(err) {
			return devBootstrapResult{}, fmt.Errorf("chmod %s: %w", envPath, err)
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
			return devBootstrapResult{}, fmt.Errorf("write %s: %w", envPath, err)
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

// fillEmptyEnvDefinitions writes each generated KEY=value line over every empty
// definition of KEY in content and returns the lines still to append.
// readEnvFile reads a key's first definition, so a value appended after an empty
// one (.env.example ships CORDUM_API_KEY=) would never reach the CLI.
func fillEmptyEnvDefinitions(content string, generated []string) (string, []string) {
	byKey := make(map[string]string, len(generated))
	for _, line := range generated {
		key, _, _ := strings.Cut(line, "=")
		byKey[key] = line
	}
	placed := map[string]bool{}
	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		key = strings.TrimSpace(key)
		gen, isGen := byKey[key]
		if !ok || !isGen || trimQuotes(strings.TrimSpace(val)) != "" {
			continue
		}
		lines[i] = gen + line[len(strings.TrimRight(line, "\r\n")):]
		placed[key] = true
	}
	var rest []string
	for _, line := range generated {
		if key, _, _ := strings.Cut(line, "="); !placed[key] {
			rest = append(rest, line)
		}
	}
	return strings.Join(lines, ""), rest
}

func randomHexString(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func ensureDevCLIContext(composeDir, tenant string) (string, bool, error) {
	cfgPath := filepath.Join(composeDir, "cordum.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return cfgPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("stat %s: %w", cfgPath, err)
	}
	if strings.TrimSpace(tenant) == "" {
		tenant = "default"
	}
	scaffold := edgeInitScaffold{
		Gateway:             defaultDevGatewayURL,
		APIKeyEnvVar:        "CORDUM_API_KEY",
		Tenant:              tenant,
		Principal:           devPrincipal(),
		PolicyMode:          "enforce",
		CACert:              detectInitCACert("", defaultDevGatewayURL, composeDir),
		DashboardURL:        defaultDevDashboardURL,
		AgentdPath:          detectInitBinary("", "cordum-agentd"),
		HookCommand:         detectInitBinary("", "cordum-hook"),
		ApprovalWaitTimeout: "30s",
	}
	if err := writeInitYAML(cfgPath, scaffold); err != nil {
		return "", false, err
	}
	return cfgPath, true, nil
}

func devPrincipal() string {
	if p := firstNonEmptyEnv("CORDUM_PRINCIPAL_ID", "CORDUM_EDGE_PRINCIPAL_ID"); p != "" {
		return p
	}
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
		return strings.TrimSpace(u.Username)
	}
	return "local-dev"
}

func waitAndSeedDevMCP(ctx context.Context, devBoot devBootstrapResult) error {
	tlsTransport, err := sdk.BuildTLSTransportErr(sdk.TLSOptions{CACertPath: devBoot.CACert})
	if err != nil {
		return fmt.Errorf("tls transport: %w", err)
	}
	client := newRestClient(devBoot.Gateway, devBoot.APIKey, devBoot.Tenant, tlsTransport)
	if err := waitForGatewayStatus(ctx, client); err != nil {
		return err
	}
	return seedDevMCPConfig(ctx, client, true)
}

func waitForGatewayStatus(ctx context.Context, client *restClient) error {
	var lastErr error
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		var status map[string]any
		err := client.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, &status)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for gateway status: %w", lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func seedDevMCPConfig(ctx context.Context, client *restClient, seedEnabled bool) error {
	doc, err := client.getConfig(ctx, "system", "default")
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		doc = &configDoc{
			Scope:   "system",
			ScopeID: "default",
			Data:    map[string]any{},
		}
	}
	if doc == nil {
		doc = &configDoc{}
	}
	doc.Scope = "system"
	doc.ScopeID = "default"
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}

	mcpCfg := map[string]any{}
	if existing, ok := doc.Data["mcp"].(map[string]any); ok {
		for k, v := range existing {
			mcpCfg[k] = v
		}
	}
	if _, ok := mcpCfg["enabled"]; !ok && seedEnabled {
		mcpCfg["enabled"] = true
	}
	if _, ok := mcpCfg["transport"]; !ok {
		mcpCfg["transport"] = "http"
	}
	if _, ok := mcpCfg["policy_gate_enabled"]; !ok {
		mcpCfg["policy_gate_enabled"] = true
	}
	doc.Data["mcp"] = mcpCfg

	req := map[string]any{
		"scope":    doc.Scope,
		"scope_id": doc.ScopeID,
		"data":     doc.Data,
		"meta":     doc.Meta,
	}
	return client.doJSON(ctx, http.MethodPost, "/api/v1/config", req, nil)
}
