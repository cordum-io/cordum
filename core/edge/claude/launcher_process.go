package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cordum/cordum/core/edge/safeexec"
)

func startLaunchAgentd(ctx context.Context, cfg launchConfig, opts LaunchOptions, meta LaunchMetadata, stderr io.Writer) (*launchAgentd, error) {
	agentdCtx, cancel := context.WithCancel(ctx)
	env := cfg.agentdEnv(meta)
	var inheritedFile *os.File
	if cfg.AgentdListener != nil {
		file, err := listenerFileForInheritance(cfg.AgentdListener)
		if err != nil {
			cancel()
			return nil, err
		}
		inheritedFile = file
		env = append(env, "CORDUM_AGENTD_LISTENER_FD=3")
	}
	cmd, err := safeexec.CommandContext(agentdCtx, cfg.AgentdPath, nil, safeexec.Options{
		Dir:            meta.CWD,
		Env:            env,
		AllowEnv:       []string{"CORDUMCTL_*"},
		Stderr:         stderr,
		MaxStdoutBytes: 1 << 20,
		MaxStderrBytes: 1 << 20,
	})
	if err != nil {
		if inheritedFile != nil {
			_ = inheritedFile.Close()
		}
		cancel()
		return nil, fmt.Errorf("prepare cordum-agentd: %w", err)
	}
	if inheritedFile != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, inheritedFile)
	}
	if opts.Verbose {
		cmd.Stdout = safeexec.LimitWriter(stderr, 1<<20)
	}
	if err := cmd.Start(); err != nil {
		if inheritedFile != nil {
			_ = inheritedFile.Close()
		}
		cancel()
		return nil, fmt.Errorf("start cordum-agentd: %w", err)
	}
	if inheritedFile != nil {
		_ = inheritedFile.Close()
		_ = cfg.AgentdListener.Close()
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	return &launchAgentd{cmd: cmd, cancel: cancel, done: done}, nil
}

func listenerFileForInheritance(ln net.Listener) (*os.File, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("agentd listener fd inheritance is not supported on Windows")
	}
	tcp, ok := ln.(*net.TCPListener)
	if !ok {
		return nil, fmt.Errorf("agentd listener inheritance requires TCP listener, got %T", ln)
	}
	file, err := tcp.File()
	if err != nil {
		return nil, fmt.Errorf("prepare inherited agentd listener: %w", err)
	}
	return file, nil
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
		select {
		case err := <-done:
			return agentdExitedBeforeReadyError(err)
		default:
		}
		if dialLoopback(host) == nil {
			select {
			case err := <-done:
				return agentdExitedBeforeReadyError(err)
			default:
			}
			return nil
		}
		select {
		case err := <-done:
			return agentdExitedBeforeReadyError(err)
		case <-deadline.Done():
			return fmt.Errorf("timed out waiting for cordum-agentd at %s", endpoint)
		case <-ticker.C:
		}
	}
}

func agentdExitedBeforeReadyError(err error) error {
	if err == nil {
		return errors.New("cordum-agentd exited before becoming ready")
	}
	return fmt.Errorf("cordum-agentd exited before becoming ready: %w", err)
}

func runClaudeProcess(ctx context.Context, cfg launchConfig, opts LaunchOptions, meta LaunchMetadata, state launchSessionState, settingsPath, claudePath string) (int, error) {
	args := append([]string{"--settings", settingsPath}, opts.ClaudeArgs...)
	cmd, err := safeexec.CommandContext(ctx, claudePath, args, safeexec.Options{
		Dir:                    meta.CWD,
		Env:                    cfg.claudeEnv(meta, state),
		Stdin:                  opts.Stdin,
		Stdout:                 opts.Stdout,
		Stderr:                 opts.Stderr,
		AllowedArgPathPrefixes: []string{meta.CWD, filepath.Dir(settingsPath)},
	})
	if err != nil {
		return 1, fmt.Errorf("prepare claude: %w", err)
	}
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 1, fmt.Errorf("run claude: %w", err)
}
