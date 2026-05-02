package agentd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
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
	local, err := NewLocalServer(LocalServerConfig{
		BindURL:      cfg.BindURL,
		MaxBodyBytes: defaultMaxHookBodyBytes,
		State:        *state,
		EventWriter:  eventWriter,
	})
	if err != nil {
		return err
	}

	var httpServer *http.Server
	var listener net.Listener
	serverErr := make(chan error, 1)
	if cfg.BindURL != "" {
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
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	if heartbeat, ok := gateway.(HeartbeatClient); ok {
		ticker := time.NewTicker(cfg.HeartbeatInterval)
		defer ticker.Stop()
		service := NewHeartbeatService(HeartbeatConfig{
			Gateway:                heartbeat,
			SessionID:              state.SessionID,
			Timeout:                cfg.GatewayTimeout,
			MaxConsecutiveFailures: 3,
			PolicyMode:             cfg.PolicyMode,
			FailClosed:             cfg.FailClosed,
		})
		go service.Run(hbCtx, ticker.C)
		defer service.Wait()
	}

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			return err
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
	if err := manager.Shutdown(shutdownCtx, ShutdownOptions{}); err != nil {
		return err
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
