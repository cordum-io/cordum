package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const (
	// bcryptCost is the cost factor for bcrypt hashing.
	bcryptCost = 12

	// userKeyPrefix is the Redis key prefix for user records.
	userKeyPrefix = "user:"

	// userEmailIndexPrefix is the Redis key prefix for email lookups.
	userEmailIndexPrefix = "user:email:"
)

// userRecord is the internal Redis storage representation that includes the password hash.
// The User struct uses json:"-" on PasswordHash to prevent API leakage, so we need
// a separate type for Redis serialization.
type userRecord struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"password_hash"`
	Tenant       string    `json:"tenant"`
	Role         string    `json:"role"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toUserRecord(u *User) *userRecord {
	return &userRecord{
		ID:           u.ID,
		Username:     u.Username,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		Tenant:       u.Tenant,
		Role:         u.Role,
		Disabled:     u.Disabled,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

func (r *userRecord) toUser() *User {
	return &User{
		ID:           r.ID,
		Username:     r.Username,
		Email:        r.Email,
		PasswordHash: r.PasswordHash,
		Tenant:       r.Tenant,
		Role:         r.Role,
		Disabled:     r.Disabled,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

// RedisUserStore implements UserStore using Redis for persistence.
type RedisUserStore struct {
	client *redis.Client
}

// NewRedisUserStore creates a new Redis-backed user store.
func NewRedisUserStore(redisURL string) (*RedisUserStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisUserStore{client: client}, nil
}

// userKey returns the Redis key for a user record.
func userKey(tenant, username string) string {
	return userKeyPrefix + tenant + ":" + strings.ToLower(username)
}

// userIDKey returns the Redis key for a user by ID.
func userIDKey(id string) string {
	return userKeyPrefix + "id:" + id
}

// userEmailKey returns the Redis key for email index.
func userEmailKey(tenant, email string) string {
	return userEmailIndexPrefix + tenant + ":" + strings.ToLower(email)
}

// GetByUsername retrieves a user by username within a tenant.
func (s *RedisUserStore) GetByUsername(ctx context.Context, username, tenant string) (*User, error) {
	if username == "" {
		return nil, ErrUserNotFound
	}
	if tenant == "" {
		tenant = "default"
	}
	data, err := s.client.Get(ctx, userKey(tenant, username)).Bytes()
	if err == redis.Nil {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get user: %w", err)
	}
	var rec userRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}
	return rec.toUser(), nil
}

// GetByEmail retrieves a user by email within a tenant.
func (s *RedisUserStore) GetByEmail(ctx context.Context, email, tenant string) (*User, error) {
	if email == "" {
		return nil, ErrUserNotFound
	}
	if tenant == "" {
		tenant = "default"
	}
	// Look up username from email index
	username, err := s.client.Get(ctx, userEmailKey(tenant, email)).Result()
	if err == redis.Nil {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get email index: %w", err)
	}
	return s.GetByUsername(ctx, username, tenant)
}

// GetByID retrieves a user by ID.
func (s *RedisUserStore) GetByID(ctx context.Context, id string) (*User, error) {
	if id == "" {
		return nil, ErrUserNotFound
	}
	// Look up tenant:username from ID index
	ref, err := s.client.Get(ctx, userIDKey(id)).Result()
	if err == redis.Nil {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get id index: %w", err)
	}
	// ref is "tenant:username"
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return nil, ErrUserNotFound
	}
	return s.GetByUsername(ctx, parts[1], parts[0])
}

// Create creates a new user with the given password.
func (s *RedisUserStore) Create(ctx context.Context, user *User, password string) error {
	if user == nil {
		return fmt.Errorf("user required")
	}
	if user.Username == "" {
		return fmt.Errorf("username required")
	}
	if password == "" {
		return fmt.Errorf("password required")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	if user.Tenant == "" {
		user.Tenant = "default"
	}
	if user.Role == "" {
		user.Role = "user"
	}
	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	// Check if user already exists
	key := userKey(user.Tenant, user.Username)
	exists, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("check user exists: %w", err)
	}
	if exists > 0 {
		return ErrUserAlreadyExists
	}

	// Check if email is already in use
	if user.Email != "" {
		emailKey := userEmailKey(user.Tenant, user.Email)
		emailExists, err := s.client.Exists(ctx, emailKey).Result()
		if err != nil {
			return fmt.Errorf("check email exists: %w", err)
		}
		if emailExists > 0 {
			return ErrUserAlreadyExists
		}
	}

	// Hash the password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user.PasswordHash = string(hash)

	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now

	data, err := json.Marshal(toUserRecord(user))
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	// Use a transaction to set user and indexes atomically
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, key, data, 0)
	pipe.Set(ctx, userIDKey(user.ID), user.Tenant+":"+user.Username, 0)
	if user.Email != "" {
		pipe.Set(ctx, userEmailKey(user.Tenant, user.Email), user.Username, 0)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis set user: %w", err)
	}
	return nil
}

// UpdatePassword updates a user's password.
func (s *RedisUserStore) UpdatePassword(ctx context.Context, userID, newPassword string) error {
	if userID == "" {
		return fmt.Errorf("user id required")
	}
	if newPassword == "" {
		return fmt.Errorf("new password required")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	user, err := s.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	// Hash the new password
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user.PasswordHash = string(hash)
	user.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(toUserRecord(user))
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	key := userKey(user.Tenant, user.Username)
	if err := s.client.Set(ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("redis set user: %w", err)
	}
	return nil
}

// ValidatePassword checks if the provided password matches the user's stored hash.
func (s *RedisUserStore) ValidatePassword(_ context.Context, user *User, password string) bool {
	if user == nil || user.PasswordHash == "" || password == "" {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// Close closes the Redis client connection.
func (s *RedisUserStore) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// Session token management
// ---------------------------------------------------------------------------

const sessionKeyPrefix = "session:"

// sessionData stores the auth context for a session token.
type sessionData struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Tenant   string `json:"tenant"`
	Role     string `json:"role"`
}

// StoreSession stores a session token in Redis with a TTL.
func (s *RedisUserStore) StoreSession(ctx context.Context, token string, user *User, ttl time.Duration) error {
	data, err := json.Marshal(sessionData{
		UserID:   user.ID,
		Username: user.Username,
		Tenant:   user.Tenant,
		Role:     user.Role,
	})
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return s.client.Set(ctx, sessionKeyPrefix+token, data, ttl).Err()
}

// DeleteSession removes a session token from Redis.
func (s *RedisUserStore) DeleteSession(ctx context.Context, token string) error {
	return s.client.Del(ctx, sessionKeyPrefix+token).Err()
}

// ValidateSession looks up a session token and returns the associated auth context.
func (s *RedisUserStore) ValidateSession(ctx context.Context, token string) (*AuthContext, error) {
	raw, err := s.client.Get(ctx, sessionKeyPrefix+token).Bytes()
	if err == redis.Nil {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get session: %w", err)
	}
	var sd sessionData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &AuthContext{
		Tenant:      sd.Tenant,
		PrincipalID: sd.UserID,
		Role:        sd.Role,
	}, nil
}

// ---------------------------------------------------------------------------

// seedDefaultAdminUser creates a default admin user from environment variables if configured.
// Environment variables:
//   - CORDUM_ADMIN_USERNAME (default: "admin")
//   - CORDUM_ADMIN_PASSWORD (required for user creation)
//   - CORDUM_ADMIN_EMAIL (optional)
func seedDefaultAdminUser(ctx context.Context, store UserStore, tenant string) error {
	username := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_USERNAME"))
	password := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_PASSWORD"))
	email := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_EMAIL"))

	if username == "" {
		username = "admin"
	}
	if password == "" {
		return fmt.Errorf("CORDUM_ADMIN_PASSWORD is required when user auth is enabled")
	}
	if tenant == "" {
		tenant = "default"
	}

	// Check if admin user already exists
	_, err := store.GetByUsername(ctx, username, tenant)
	if err == nil {
		// User already exists, skip
		return nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return fmt.Errorf("check admin user: %w", err)
	}

	// Create admin user
	user := &User{
		Username: username,
		Email:    email,
		Tenant:   tenant,
		Role:     "admin",
	}

	if err := store.Create(ctx, user, password); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}

	return nil
}
