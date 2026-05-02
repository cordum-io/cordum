package agentd

import (
	"context"
	"errors"
	"fmt"
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
	Clock      Clock
}

func Run(ctx context.Context, opts RunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := opts.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	gateway := opts.Gateway
	if gateway == nil {
		client, err := NewGatewayClient(GatewayClientConfig{
			BaseURL:  cfg.GatewayURL,
			APIKey:   cfg.APIKey,
			TenantID: cfg.TenantID,
			Timeout:  cfg.GatewayTimeout,
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
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:    gateway,
		StateStore: store,
		Metadata:   meta,
		PolicyMode: cfg.PolicyMode,
		FailClosed: cfg.FailClosed,
		GatewayURL: cfg.GatewayURL,
		Clock:      clock,
	})
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
				statusCtx, cancel := context.WithTimeout(context.Background(), cfg.GatewayTimeout)
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
		go service.Run(hbCtx, ticker.C)
		defer service.Wait()
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
