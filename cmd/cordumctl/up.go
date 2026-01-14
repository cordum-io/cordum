package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

const defaultComposeTimeoutSeconds = "1800"

func runUpCmd(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	file := fs.String("file", "docker-compose.yml", "compose file path")
	build := fs.Bool("build", true, "build images before starting")
	detach := fs.Bool("detach", true, "run in background")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	if err := runCompose(*file, *build, *detach); err != nil {
		fail(err.Error())
	}

	fmt.Println("Cordum stack started.")
	fmt.Println("Gateway: http://localhost:8081")
	fmt.Println("Dashboard: http://localhost:8082")
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
