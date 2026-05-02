package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

func startLaunchAgentd(ctx context.Context, cfg launchConfig, opts LaunchOptions, meta LaunchMetadata, stderr io.Writer) (*launchAgentd, error) {
	agentdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(agentdCtx, cfg.AgentdPath)
	cmd.Dir = meta.CWD
	cmd.Env = cfg.agentdEnv(meta)
	cmd.Stderr = stderr
	if opts.Verbose {
		cmd.Stdout = stderr
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start cordum-agentd: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	return &launchAgentd{cmd: cmd, cancel: cancel, done: done}, nil
}

type launchAgentd struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
}

func (p *launchAgentd) stop() {
	if p == nil {
		return
	}
	p.cancel()
	select {
	case <-p.done:
		return
	case <-time.After(2 * time.Second):
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

func waitForAgentdReady(ctx context.Context, endpoint string, done <-chan error) error {
	host, err := endpointHost(endpoint)
	if err != nil {
		return err
	}
	deadline, cancel := context.WithTimeout(ctx, defaultLaunchAgentdReadyWait)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if dialLoopback(host) == nil {
			return nil
		}
		select {
		case err := <-done:
			if err == nil {
				return errors.New("cordum-agentd exited before becoming ready")
			}
			return fmt.Errorf("cordum-agentd exited before becoming ready: %w", err)
		case <-deadline.Done():
			return fmt.Errorf("timed out waiting for cordum-agentd at %s", endpoint)
		case <-ticker.C:
		}
	}
}

func runClaudeProcess(ctx context.Context, cfg launchConfig, opts LaunchOptions, meta LaunchMetadata, state launchSessionState, settingsPath, claudePath string) (int, error) {
	args := append([]string{"--settings", settingsPath}, opts.ClaudeArgs...)
	cmd := exec.CommandContext(ctx, claudePath, args...)
	cmd.Dir = meta.CWD
	cmd.Env = cfg.claudeEnv(meta, state)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 1, fmt.Errorf("run claude: %w", err)
}
