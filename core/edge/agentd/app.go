package agentd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

type RunOptions struct {
	Config     Config
	Metadata   LocalSessionMetadata
	Gateway    GatewayLifecycleClient
	StateStore StateStore
	Recorder   edgecore.Recorder
	Clock      Clock
	// Nonce, if non-empty, pre-seeds LocalServerConfig.Nonce; it must be
	// base64-encoded and decode to at least 32 raw bytes. Empty values trigger
	// auto-generation. NEVER persist this value or echo it in logs/responses.
	Nonce string
	// InheritedListener, when non-nil, is a launcher-held loopback listener
	// passed across exec to remove bind-then-close TOCTOU during startup.
	InheritedListener net.Listener
}

var errInvalidExternalNonce = errors.New("agentd: CORDUM_AGENTD_NONCE invalid: must be base64 encoding of >= 32 bytes")

// ValidateExternalNonce validates a trusted launcher-supplied nonce without
// echoing the value. Empty input is valid and means Run will auto-generate.
func ValidateExternalNonce(nonce string) (string, error) {
	return validateExternalNonce(nonce)
}

func Run(ctx context.Context, opts RunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := opts.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	nonce, err := validateExternalNonce(opts.Nonce)
	if err != nil {
		return err
	}
	gateway := opts.Gateway
	if gateway == nil {
		client, err := NewGatewayClient(GatewayClientConfig{
			BaseURL:     cfg.GatewayURL,
			APIKey:      cfg.APIKey,
			TenantID:    cfg.TenantID,
			PrincipalID: cfg.PrincipalID,
			Timeout:     cfg.GatewayTimeout,
			TLSCAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			return err
		}
		gateway = client
	}
	store := opts.StateStore
	if store == nil {
		fileStore, err := NewFileStateStore(cfg.StateDir)
		if err != nil {
			return err
		}
		store = fileStore
	}
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	meta := opts.Metadata
	if meta.TenantID == "" {
		meta.TenantID = cfg.TenantID
	}
	managerCfg := SessionManagerConfig{
		Gateway:    gateway,
		StateStore: store,
		Metadata:   meta,
		PolicyMode: cfg.PolicyMode,
		FailClosed: cfg.FailClosed,
		GatewayURL: cfg.GatewayURL,
		Clock:      clock,
	}
	if strings.TrimSpace(cfg.BindSessionID) != "" && strings.TrimSpace(cfg.BindExecutionID) != "" {
		// External owner pre-created an EdgeSession+AgentExecution via the
		// Gateway and is asking agentd to bind to those IDs instead of
		// spawning new ones. Seed InitialState; SessionManager.Start will
		// skip Gateway CreateSession and write hook events under these IDs.
		managerCfg.InitialState = &SessionState{
			SessionID:   strings.TrimSpace(cfg.BindSessionID),
			ExecutionID: strings.TrimSpace(cfg.BindExecutionID),
			TenantID:    meta.TenantID,
			PrincipalID: meta.PrincipalID,
			PolicyMode:  cfg.PolicyMode,
			Status:      edgecore.SessionStatusRunning,
			StartedAt:   clock.Now(),
		}
	}
	manager := NewSessionManager(managerCfg)
	state, err := manager.Start(ctx)
	if err != nil {
		return err
	}

	var eventWriter EventWriter
	if writer, ok := gateway.(EventWriter); ok {
		eventWriter = writer
	}
	var safeAllowCache *SafeAllowCache
	if cfg.SafeAllowCache.Enabled {
		safeAllowCache = NewSafeAllowCache(cfg.SafeAllowCache, clock)
	}
	var approvalWaiter ApprovalWaiter
	if cfg.InlineApprovalWaitEnabled {
		if waiter, ok := gateway.(ApprovalWaiter); ok {
			approvalWaiter = waiter
		}
	}
	var evaluator *Evaluator
	if evaluateClient, ok := gateway.(EvaluateClient); ok {
		evaluator = NewEvaluator(EvaluatorConfig{
			Client:         evaluateClient,
			EventWriter:    eventWriter,
			State:          *state,
			Cache:          safeAllowCache,
			ApprovalWaiter: approvalWaiter,
			Recorder:       opts.Recorder,
			ApprovalConfig: ApprovalDecisionConfig{
				InlineWaitEnabled: cfg.InlineApprovalWaitEnabled && approvalWaiter != nil,
				InlineWaitTimeout: cfg.InlineApprovalWaitTimeout,
				PolicyMode:        cfg.PolicyMode,
			},
			HookTimeout: cfg.HookTimeout,
		})
	}
	local, err := NewLocalServer(LocalServerConfig{
		BindURL:      cfg.BindURL,
		Nonce:        nonce,
		MaxBodyBytes: defaultMaxHookBodyBytes,
		Evaluator:    evaluator,
		State:        *state,
		EventWriter:  eventWriter,
		// EDGE-059 — share the recorder so the local hook handler can emit
		// agentd_response_write_aborted_total via the same registry as the
		// rest of agentd's metrics.
		Recorder: opts.Recorder,
	})
	if err != nil {
		return err
	}

	var httpServer *http.Server
	var listener net.Listener
	serverErr := make(chan error, 1)
	httpServer, listener, err = newHTTPServer(cfg, local, opts.InheritedListener)
	if err != nil {
		return err
	}
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	var heartbeatService *HeartbeatService
	heartbeatStatusErr := make(chan error, 1)
	sendHeartbeatStatusErr := func(err error) {
		select {
		case heartbeatStatusErr <- err:
		default:
		}
	}
	if heartbeat, ok := gateway.(HeartbeatClient); ok {
		ticker := time.NewTicker(cfg.HeartbeatInterval)
		defer ticker.Stop()
		degradedWriter, _ := gateway.(SessionDegradedWriter)
		service := NewHeartbeatService(HeartbeatConfig{
			Gateway:                heartbeat,
			SessionID:              state.SessionID,
			Timeout:                cfg.GatewayTimeout,
			MaxConsecutiveFailures: 3,
			PolicyMode:             cfg.PolicyMode,
			FailClosed:             cfg.FailClosed,
			OnStatus: func(status HeartbeatStatus) {
				// EDGE-063 — early-out if hbCtx is already cancelled (the
				// shutdown sequence is in progress). Without this guard, a
				// late OnStatus tick during shutdown could call
				// manager.RecordHeartbeatStatus while the manager is itself
				// tearing down, risking a deadlock-shaped wait. statusCtx
				// inherits from hbCtx, so any subsequent call would see a
				// pre-cancelled context anyway — making the early-out the
				// honest behavior.
				if hbCtx.Err() != nil {
					return
				}
				statusCtx, cancel := context.WithTimeout(hbCtx, cfg.GatewayTimeout)
				defer cancel()
				updated, err := manager.RecordHeartbeatStatus(statusCtx, status)
				if err != nil {
					sendHeartbeatStatusErr(err)
					return
				}
				if degradedWriter != nil && status.Degraded && strings.TrimSpace(updated.SessionID) != "" {
					_, _ = degradedWriter.MarkSessionDegraded(statusCtx, updated, status.Reason)
				}
				if status.FailClosed {
					sendHeartbeatStatusErr(fmt.Errorf("%w: %s", ErrFailClosed, status.Reason))
				}
			},
		})
		heartbeatService = service
		go service.Run(hbCtx, ticker.C)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			runErr = err
		}
	case err := <-heartbeatStatusErr:
		if err != nil {
			runErr = err
		}
	}

	hbCancel()
	if !waitForHeartbeatDrain(heartbeatService, cfg.GatewayTimeout) && opts.Recorder != nil {
		opts.Recorder.RecordAgentdShutdownForced("heartbeat_drain")
	}
	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HookTimeout)
		_ = httpServer.Shutdown(shutdownCtx)
		cancel()
		// EDGE-063 — wait for the Serve goroutine to fully exit so we
		// don't return Run while it still holds the listener. Bounded by
		// 2x HookTimeout (hard floor 5s for operator misconfig) so the
		// developer Ctrl-C path can't hang indefinitely.
		joinTimeout := agentdHTTPJoinTimeout(cfg.HookTimeout)
		select {
		case <-serverErr:
		case <-time.After(joinTimeout):
			slog.Warn("agentd HTTP server drain timed out", "timeout", joinTimeout)
			if opts.Recorder != nil {
				opts.Recorder.RecordAgentdShutdownForced("http_server_drain")
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GatewayTimeout)
	defer cancel()
	shutdownOpts := ShutdownOptions{}
	if errors.Is(runErr, ErrFailClosed) {
		shutdownOpts.ExecutionStatus = edgecore.ExecutionStatusFailed
		shutdownOpts.SessionStatus = edgecore.SessionStatusFailed
	}
	shutdownErr := manager.Shutdown(shutdownCtx, shutdownOpts)
	if runErr != nil && shutdownErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	if runErr != nil {
		return runErr
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	return nil
}

func waitForHeartbeatDrain(service *HeartbeatService, timeout time.Duration) bool {
	if service == nil {
		return true
	}
	if timeout <= 0 {
		service.Wait()
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(timeout, func() {
		slog.Warn("agentd heartbeat drain timed out during shutdown", "timeout", timeout)
		cancel()
	})
	defer func() {
		timer.Stop()
		cancel()
	}()
	return service.WaitContext(ctx)
}

// agentdHTTPJoinTimeout returns the bounded wait for the HTTP server's
// Serve goroutine to exit after Shutdown completes. Per EDGE-063: 2x the
// HookTimeout, with a hard floor of 5s so an operator misconfiguration
// (HookTimeout=0) cannot drop the wait below a useful threshold.
func agentdHTTPJoinTimeout(hookTimeout time.Duration) time.Duration {
	const hardFloor = 5 * time.Second
	candidate := hookTimeout * 2
	if candidate < hardFloor {
		return hardFloor
	}
	return candidate
}

func validateExternalNonce(raw string) (string, error) {
	nonce := strings.TrimSpace(raw)
	if nonce == "" {
		return "", nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := enc.DecodeString(nonce)
		if err == nil && len(decoded) >= 32 {
			return nonce, nil
		}
	}
	return "", errInvalidExternalNonce
}

func newHTTPServer(cfg Config, local *LocalServer, inherited ...net.Listener) (*http.Server, net.Listener, error) {
	u, err := url.Parse(cfg.BindURL)
	if err != nil {
		return nil, nil, err
	}
	var ln net.Listener
	if len(inherited) > 0 && inherited[0] != nil {
		ln = inherited[0]
		if err := validateInheritedHTTPListener(cfg.BindURL, ln); err != nil {
			_ = ln.Close()
			return nil, nil, err
		}
	} else {
		ln, err = net.Listen("tcp", u.Host)
		if err != nil {
			return nil, nil, fmt.Errorf("listen local agentd: %w", err)
		}
	}
	srv := &http.Server{
		Handler:           local.Handler(),
		ReadHeaderTimeout: cfg.HookTimeout,
		// EDGE-059 — slow-loris guard. WriteTimeout caps the connection-write
		// phase so a hanging Claude-hook client cannot pin a handler goroutine
		// indefinitely (pre-fix: 0 == infinite → goroutine pool exhaustion DoS
		// → every subsequent Claude tool call hangs/denies under enforce
		// mode). 2s is documented at types.go: 8x the per-evaluation write
		// budget (250ms) and 2.5x under the hook outer budget (5s) so agentd
		// writes finish inside the hook deadline.
		WriteTimeout: defaultLocalServerWriteTimeout,
		// IdleTimeout caps lurking-but-idle Keep-Alive connections so a
		// malicious client cannot reserve TCP slots without sending traffic.
		// agentd's hook is request/response — no streaming/long-poll — so
		// 30s is comfortably above any legitimate inter-request gap.
		IdleTimeout: defaultLocalServerIdleTimeout,
	}
	return srv, ln, nil
}

func validateInheritedHTTPListener(rawURL string, ln net.Listener) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("inherited agentd listener must be TCP loopback matching configured bind URL")
	}
	if addr.IP == nil || !addr.IP.IsLoopback() {
		return fmt.Errorf("inherited agentd listener must be bound to loopback")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid local agentd bind URL: %w", err)
	}
	wantHost, wantPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		return fmt.Errorf("invalid local agentd bind URL host: %w", err)
	}
	if strconv.Itoa(addr.Port) != wantPort || !loopbackHostMatchesIP(wantHost, addr.IP) {
		return fmt.Errorf("inherited agentd listener address does not match configured bind URL")
	}
	return nil
}

func loopbackHostMatchesIP(host string, ip net.IP) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return ip.IsLoopback()
	}
	want := net.ParseIP(h)
	return want != nil && want.Equal(ip)
}
