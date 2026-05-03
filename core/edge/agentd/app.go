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
	})
	if err != nil {
		return err
	}

	var httpServer *http.Server
	var listener net.Listener
	serverErr := make(chan error, 1)
	httpServer, listener, err = newHTTPServer(cfg, local)
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
	waitForHeartbeatDrain(heartbeatService, cfg.GatewayTimeout)
	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HookTimeout)
		_ = httpServer.Shutdown(shutdownCtx)
		cancel()
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

func newHTTPServer(cfg Config, local *LocalServer) (*http.Server, net.Listener, error) {
	u, err := url.Parse(cfg.BindURL)
	if err != nil {
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return nil, nil, fmt.Errorf("listen local agentd: %w", err)
	}
	srv := &http.Server{
		Handler:           local.Handler(),
		ReadHeaderTimeout: cfg.HookTimeout,
	}
	return srv, ln, nil
}
