package gateway

import (
	"context"
	"errors"
	"time"
)

// User represents an authenticated user in the system.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"-"` // Never exposed in JSON
	Tenant       string    `json:"tenant"`
	Role         string    `json:"role"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserStore defines the interface for user persistence and authentication.
type UserStore interface {
	// GetByUsername retrieves a user by username within a tenant.
	GetByUsername(ctx context.Context, username, tenant string) (*User, error)

	// GetByEmail retrieves a user by email within a tenant.
	GetByEmail(ctx context.Context, email, tenant string) (*User, error)

	// GetByID retrieves a user by ID.
	GetByID(ctx context.Context, id string) (*User, error)

	// Create creates a new user with the given password.
	Create(ctx context.Context, user *User, password string) error

	// UpdatePassword updates a user's password.
	UpdatePassword(ctx context.Context, userID, newPassword string) error

	// ValidatePassword checks if the provided password matches the user's stored hash.
	ValidatePassword(ctx context.Context, user *User, password string) bool

	// Close closes any underlying connections.
	Close() error
}

// User store errors.
var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrUserDisabled      = errors.New("user is disabled")
)

// CreateUserRequest is the request body for creating a user.
type CreateUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Password string `json:"password"`
	Tenant   string `json:"tenant,omitempty"`
	Role     string `json:"role,omitempty"`
}

// ChangePasswordRequest is the request body for changing a password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
