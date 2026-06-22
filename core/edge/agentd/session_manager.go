package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/model"
)

type SessionManagerConfig struct {
	Gateway          GatewayLifecycleClient
	StateStore       StateStore
	Metadata         LocalSessionMetadata
	PolicyMode       edgecore.PolicyMode
	FailClosed       bool
	InitialState     *SessionState
	RestoreSessionID string
	GatewayURL       string
	Clock            Clock
}

type SessionManager struct {
	mu         sync.Mutex
	gateway    GatewayLifecycleClient
	store      StateStore
	metadata   LocalSessionMetadata
	policyMode edgecore.PolicyMode
	failClosed bool
	restoreID  string
	gatewayURL string
	clock      Clock
	state      *SessionState
	shutdown   bool
}

func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	store := cfg.StateStore
	if store == nil {
		store = NewMemoryStateStore()
	}
	policyMode := cfg.PolicyMode
	if policyMode == "" {
		policyMode = edgecore.PolicyModeObserve
	}
	var state *SessionState
	if cfg.InitialState != nil {
		copied := *cfg.InitialState
		state = &copied
	}
	return &SessionManager{
		gateway:    cfg.Gateway,
		store:      store,
		metadata:   cfg.Metadata,
		policyMode: policyMode,
		failClosed: cfg.FailClosed,
		restoreID:  strings.TrimSpace(cfg.RestoreSessionID),
		gatewayURL: strings.TrimRight(strings.TrimSpace(cfg.GatewayURL), "/"),
		clock:      clock,
		state:      state,
	}
}

func (m *SessionManager) Start(ctx context.Context) (*SessionState, error) {
	if m == nil {
		return nil, errors.New("session manager is nil")
	}
	if restored, ok := m.restore(ctx); ok {
		return restored, nil
	}
	m.mu.Lock()
	if m.state != nil && strings.TrimSpace(m.state.SessionID) != "" {
		// External owner (cordumctl wrapper, integration script) pre-bound
		// an EdgeSession+AgentExecution via InitialState; skip CreateSession
		// and write hook events under those IDs. The Gateway records are
		// expected to exist already.
		seeded := *m.state
		m.mu.Unlock()
		if err := m.store.Save(ctx, seeded); err != nil {
			return nil, fmt.Errorf("persist bound session state: %w", err)
		}
		return &seeded, nil
	}
	m.mu.Unlock()
	req := createSessionRequestFromMetadata(m.metadata, m.policyMode)
	resp, err := m.gateway.CreateSession(ctx, req)
	if err != nil {
		if isTimeoutLike(err) && !m.shouldFailClosed() {
			state := m.degradedState("gateway timeout while creating edge session")
			if err := m.store.Save(ctx, state); err != nil {
				return nil, fmt.Errorf("persist degraded session state: %w", err)
			}
			m.mu.Lock()
			m.state = &state
			m.mu.Unlock()
			return &state, nil
		}
		if m.shouldFailClosed() {
			return nil, fmt.Errorf("%w: create edge session: %v", ErrFailClosed, err)
		}
		return nil, err
	}
	state := SessionState{
		SessionID:                resp.SessionID,
		ExecutionID:              resp.ExecutionID,
		TraceID:                  resp.TraceID,
		TenantID:                 nonEmpty(resp.Session.TenantID, req.TenantID),
		PrincipalID:              nonEmpty(resp.Session.PrincipalID, req.PrincipalID),
		PolicySnapshot:           resp.PolicySnapshot,
		WorkflowOverrideSnapshot: resp.WorkflowOverrideSnapshot,
		JobOverrideSnapshot:      resp.JobOverrideSnapshot,
		DashboardURL:             resp.DashboardURL,
		PolicyMode:               nonEmptyPolicyMode(resp.Session.PolicyMode, req.PolicyMode),
		Status:                   edgecore.SessionStatusRunning,
		StartedAt:                m.clock.Now(),
		Metadata:                 m.stateMetadata(),
	}
	if !resp.Session.StartedAt.IsZero() {
		state.StartedAt = resp.Session.StartedAt
	}
	if err := m.store.Save(ctx, state); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.state = &state
	m.mu.Unlock()
	return &state, nil
}

func (m *SessionManager) Shutdown(ctx context.Context, opts ShutdownOptions) error {
	if m == nil {
		return errors.New("session manager is nil")
	}
	m.mu.Lock()
	if m.shutdown {
		m.mu.Unlock()
		return nil
	}
	if m.state == nil {
		m.shutdown = true
		m.mu.Unlock()
		return nil
	}
	state := *m.state
	m.shutdown = true
	m.mu.Unlock()

	now := m.clock.Now()
	execStatus := opts.ExecutionStatus
	if execStatus == "" {
		execStatus = edgecore.ExecutionStatusSucceeded
	}
	sessStatus := opts.SessionStatus
	if sessStatus == "" {
		sessStatus = edgecore.SessionStatusEnded
	}
	if strings.TrimSpace(state.ExecutionID) != "" {
		if err := m.gateway.EndExecution(ctx, state.ExecutionID, EndExecutionRequest{Status: execStatus, EndedAt: &now}); err != nil {
			if saveErr := m.recordShutdownFailure(ctx, state, err, now); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			return err
		}
	}
	if strings.TrimSpace(state.SessionID) != "" {
		if err := m.gateway.EndSession(ctx, state.SessionID, EndSessionRequest{Status: sessStatus, EndedAt: &now}); err != nil {
			if saveErr := m.recordShutdownFailure(ctx, state, err, now); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			return err
		}
	}
	state.Status = sessStatus
	state.EndedAt = &now
	state.PendingGatewayEnd = false
	if err := m.store.Save(ctx, state); err != nil {
		m.mu.Lock()
		m.state = &state
		m.shutdown = true
		m.mu.Unlock()
		return fmt.Errorf("persist final session state: %w", err)
	}
	m.mu.Lock()
	m.state = &state
	m.shutdown = true
	m.mu.Unlock()
	return nil
}

func (m *SessionManager) State() SessionState {
	if m == nil {
		return SessionState{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return SessionState{}
	}
	return *m.state
}

func (m *SessionManager) RecordHeartbeatStatus(ctx context.Context, status HeartbeatStatus) (SessionState, error) {
	if m == nil {
		return SessionState{}, errors.New("session manager is nil")
	}
	if !status.Degraded && !status.FailClosed {
		return m.State(), nil
	}
	reason := strings.TrimSpace(status.Reason)
	if reason == "" {
		reason = "gateway heartbeat status degraded"
	}
	if status.ConsecutiveFailures > 0 {
		reason = fmt.Sprintf("%s (consecutive failures: %d)", reason, status.ConsecutiveFailures)
	}
	if ctx != nil && ctx.Err() != nil {
		return m.State(), nil
	}
	m.mu.Lock()
	if m.state == nil {
		m.mu.Unlock()
		return SessionState{}, nil
	}
	state := *m.state
	if m.shutdown {
		m.mu.Unlock()
		return state, nil
	}
	if state.Status == edgecore.SessionStatusEnded || state.EndedAt != nil {
		m.mu.Unlock()
		return state, nil
	}
	state.Status = edgecore.SessionStatusDegraded
	state.DegradedReason = reason
	state.FailClosed = status.FailClosed
	if status.FailClosed {
		state.Status = edgecore.SessionStatusFailed
		state.PendingGatewayEnd = true
	}
	m.mu.Unlock()

	if ctx != nil && ctx.Err() != nil {
		return state, nil
	}
	if err := m.store.Save(ctx, state); err != nil {
		return state, fmt.Errorf("persist heartbeat session state: %w", err)
	}
	m.mu.Lock()
	if m.state != nil && m.state.SessionID == state.SessionID {
		m.state = &state
	}
	m.mu.Unlock()
	return state, nil
}

func (m *SessionManager) recordShutdownFailure(ctx context.Context, state SessionState, err error, now time.Time) error {
	state.Status = edgecore.SessionStatusFailed
	state.EndedAt = &now
	state.PendingGatewayEnd = true
	state.DegradedReason = "gateway timeout during shutdown"
	if !isTimeoutLike(err) {
		state.DegradedReason = "gateway error during shutdown"
	}
	saveErr := m.store.Save(ctx, state)
	m.mu.Lock()
	m.state = &state
	m.mu.Unlock()
	if saveErr != nil {
		return fmt.Errorf("persist failed session state: %w", saveErr)
	}
	return nil
}

func (m *SessionManager) restore(ctx context.Context) (*SessionState, bool) {
	if strings.TrimSpace(m.restoreID) == "" || m.store == nil {
		return nil, false
	}
	state, ok, err := m.store.Load(ctx, m.restoreID)
	if err != nil || !ok {
		return nil, false
	}
	if state.Status == edgecore.SessionStatusEnded || state.Status == edgecore.SessionStatusFailed || state.EndedAt != nil {
		return nil, false
	}
	if strings.TrimSpace(state.TenantID) != "" && strings.TrimSpace(m.metadata.TenantID) != "" && state.TenantID != m.metadata.TenantID {
		return nil, false
	}
	meta := state.Metadata
	if meta == nil {
		return nil, false
	}
	if m.gatewayURL != "" && strings.TrimRight(meta["gateway_url"], "/") != m.gatewayURL {
		return nil, false
	}
	if strings.TrimSpace(m.metadata.CWD) != "" && meta["cwd"] != m.metadata.CWD {
		return nil, false
	}
	m.mu.Lock()
	m.state = &state
	m.mu.Unlock()
	return &state, true
}

func (m *SessionManager) degradedState(reason string) SessionState {
	return SessionState{
		SessionID:      fmt.Sprintf("local-degraded-%d", m.clock.Now().UnixNano()),
		ExecutionID:    "",
		TenantID:       m.metadata.TenantID,
		PrincipalID:    m.metadata.PrincipalID,
		PolicyMode:     m.policyMode,
		Status:         edgecore.SessionStatusDegraded,
		StartedAt:      m.clock.Now(),
		DegradedReason: reason,
		FailClosed:     false,
		Metadata:       m.stateMetadata(),
	}
}

func (m *SessionManager) stateMetadata() map[string]string {
	meta := map[string]string{
		"cwd":         m.metadata.CWD,
		"gateway_url": m.gatewayURL,
	}
	if m.metadata.Repo != "" {
		meta["repo"] = m.metadata.Repo
	}
	if m.metadata.GitBranch != "" {
		meta["git_branch"] = m.metadata.GitBranch
	}
	if m.metadata.GitSHA != "" {
		meta["git_sha"] = m.metadata.GitSHA
	}
	if m.metadata.GitRemote != "" {
		meta["git_remote"] = m.metadata.GitRemote
	}
	return meta
}

func (m *SessionManager) shouldFailClosed() bool {
	return m.failClosed || m.policyMode == edgecore.PolicyModeEnterpriseStrict
}

func createSessionRequestFromMetadata(meta LocalSessionMetadata, policy edgecore.PolicyMode) CreateSessionRequest {
	if policy == "" {
		policy = edgecore.PolicyModeObserve
	}
	mode := meta.Mode
	if mode == "" {
		mode = edgecore.SessionModeLocalDev
	}
	principalType := meta.PrincipalType
	if principalType == "" {
		principalType = edgecore.PrincipalTypeUnknown
	}
	return CreateSessionRequest{
		TenantID:             meta.TenantID,
		PrincipalID:          meta.PrincipalID,
		PrincipalType:        principalType,
		AgentProduct:         nonEmpty(meta.AgentProduct, "claude-code"),
		AgentVersion:         meta.AgentVersion,
		AgentName:            meta.AgentName,
		PrincipalDisplayName: meta.PrincipalDisplayName,
		Mode:                 mode,
		Repo:                 meta.Repo,
		GitRemote:            meta.GitRemote,
		GitBranch:            meta.GitBranch,
		GitSHA:               meta.GitSHA,
		CWD:                  meta.CWD,
		HostID:               meta.HostID,
		DeviceID:             meta.DeviceID,
		PolicyMode:           policy,
		EnforcementLayers:    edgecore.EnforcementLayers{"hook": true, "agentd": true},
		Labels:               meta.Labels,
	}
}

func isTimeoutLike(err error) bool {
	return errors.Is(err, ErrGatewayTimeout) || errors.Is(err, context.DeadlineExceeded)
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func nonEmptyPolicyMode(value, fallback edgecore.PolicyMode) edgecore.PolicyMode {
	if value != "" {
		return value
	}
	return fallback
}

type LocalMetadataOptions struct {
	Env map[string]string
	CWD string
}

func GatherLocalMetadata(opts LocalMetadataOptions) LocalSessionMetadata {
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		if got, err := os.Getwd(); err == nil {
			cwd = got
		}
	}
	host, _ := os.Hostname()
	mode := edgecore.SessionMode(envString(opts.Env, "CORDUM_EDGE_SESSION_MODE"))
	if mode == "" {
		mode = edgecore.SessionModeLocalDev
	}
	principalType := edgecore.PrincipalType(envString(opts.Env, "CORDUM_PRINCIPAL_TYPE"))
	if principalType == "" {
		principalType = edgecore.PrincipalTypeHuman
	}
	return LocalSessionMetadata{
		TenantID:      strings.TrimSpace(envString(opts.Env, "CORDUM_TENANT_ID")),
		PrincipalID:   strings.TrimSpace(envString(opts.Env, "CORDUM_PRINCIPAL_ID")),
		PrincipalType: principalType,
		AgentProduct:  nonEmpty(strings.TrimSpace(envString(opts.Env, "CORDUM_AGENT_PRODUCT")), "claude-code"),
		AgentVersion:  strings.TrimSpace(envString(opts.Env, "CORDUM_AGENT_VERSION")),
		Mode:          mode,
		Repo:          strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_REPO")),
		GitRemote:     strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_GIT_REMOTE")),
		GitBranch:     strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_GIT_BRANCH")),
		GitSHA:        strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_GIT_SHA")),
		CWD:           cwd,
		HostID:        nonEmpty(strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_HOST_ID")), host),
		DeviceID:      strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_DEVICE_ID")),
		// Explicit, operator-provided display labels (task-c8d4b056). Sanitized
		// locally for clean transport; the Gateway re-sanitizes on receipt and
		// derives principal identity from authenticated context, never these
		// labels. No Claude token/auth files are read — env/flags only.
		AgentName: model.SanitizeAgentName(nonEmpty(
			strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_AGENT_NAME")),
			strings.TrimSpace(envString(opts.Env, "CORDUM_AGENT_NAME")),
		)),
		PrincipalDisplayName: model.SanitizeAgentName(
			strings.TrimSpace(envString(opts.Env, "CORDUM_EDGE_PRINCIPAL_DISPLAY_NAME")),
		),
	}
}
