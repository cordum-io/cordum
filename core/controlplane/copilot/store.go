package copilot

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("copilot session not found")
	ErrNotImplemented = errors.New("copilot session store not implemented")
	ErrCrossTenant    = errors.New("copilot session tenant access denied")
)

// CopilotMessage is one persisted chat transcript entry in a Copilot session.
type CopilotMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	JobIDs    []string  `json:"jobIds,omitempty"`
}

// CopilotSession is the session-level transcript and metadata returned by the
// Context Engine backed store. The concrete Redis/audit implementation is
// intentionally wired in by a later phase; the gateway depends only on Store.
type CopilotSession struct {
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	UserID    string            `json:"userId"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Messages  []CopilotMessage  `json:"messages"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Store resolves Copilot session transcript details for the requesting user.
type Store interface {
	GetSession(ctx context.Context, sessionID, userID string) (*CopilotSession, error)
}

// NotImplementedStore is the default gateway dependency until the Context
// Engine-backed session store lands. It keeps the API shape stable while the UI
// can show a graceful backend-wiring-pending state.
type NotImplementedStore struct{}

func (NotImplementedStore) GetSession(context.Context, string, string) (*CopilotSession, error) {
	return nil, ErrNotImplemented
}
