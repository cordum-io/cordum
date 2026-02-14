package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const (
	// defaultBcryptCost is the cost factor for bcrypt hashing.
	defaultBcryptCost = 12

	// userKeyPrefix is the Redis key prefix for user records.
	userKeyPrefix = "user:"

	// userEmailIndexPrefix is the Redis key prefix for email lookups.
	userEmailIndexPrefix = "user:email:"

	// userTenantIndexPrefix is the Redis key prefix for the tenant user set.
	userTenantIndexPrefix = "user:tenant:"
)

// createUserLua atomically checks that the username key (KEYS[1]) and
// optional email key (KEYS[2]) don't exist, then creates all user records.
// Returns 1 on success, 0 if username or email already exists.
// ARGV: 1=userData, 2=idKey, 3=idVal, 4=emailVal, 5=tenantIdx, 6=userID
var createUserLua = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
if KEYS[2] ~= '' and redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', ARGV[2], ARGV[3])
if KEYS[2] ~= '' then redis.call('SET', KEYS[2], ARGV[4]) end
redis.call('SADD', ARGV[5], ARGV[6])
return 1
`)

// userRecord is the internal Redis storage representation that includes the password hash.
// The User struct uses json:"-" on PasswordHash to prevent API leakage, so we need
// a separate type for Redis serialization.
type userRecord struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
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
		DisplayName:  u.DisplayName,
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
		DisplayName:  r.DisplayName,
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
	store := &RedisUserStore{client: client}

	// Backfill tenant index for users created before the index was introduced.
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer bgCancel()
	if err := store.backfillTenantIndex(bgCtx); err != nil {
		slog.Warn("user store: tenant index backfill failed", "error", err)
	}

	return store, nil
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

// validatePassword checks that a password meets complexity requirements:
// at least 12 characters, 1 uppercase letter, 1 digit, and 1 special character.
func validatePassword(password string) error {
	if len(password) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	var hasUpper, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			hasSpecial = true
		}
	}
	var missing []string
	if !hasUpper {
		missing = append(missing, "uppercase letter")
	}
	if !hasDigit {
		missing = append(missing, "digit")
	}
	if !hasSpecial {
		missing = append(missing, "special character")
	}
	if len(missing) > 0 {
		return fmt.Errorf("password must include at least one %s", strings.Join(missing, ", "))
	}
	return nil
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
	if err := validatePassword(password); err != nil {
		return err
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

	// Hash the password before taking any locks — bcrypt is expensive.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCostFromEnv())
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

	key := userKey(user.Tenant, user.Username)
	idKey := userIDKey(user.ID)
	emailKey := ""
	emailVal := ""
	if user.Email != "" {
		emailKey = userEmailKey(user.Tenant, user.Email)
		emailVal = user.Username
	}
	tenantIdx := userTenantIndexPrefix + user.Tenant
	idVal := user.Tenant + ":" + user.Username

	// Atomically check username+email uniqueness and create all keys.
	result, err := createUserLua.Run(ctx, s.client,
		[]string{key, emailKey},
		string(data), idKey, idVal, emailVal, tenantIdx, user.ID,
	).Int64()
	if err != nil {
		return fmt.Errorf("redis create user: %w", err)
	}
	if result == 0 {
		return ErrUserAlreadyExists
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
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	user, err := s.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	// Hash the new password
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCostFromEnv())
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

// List returns all non-disabled users for a tenant.
func (s *RedisUserStore) List(ctx context.Context, tenant string) ([]*User, error) {
	if tenant == "" {
		tenant = "default"
	}

	ids, err := s.client.SMembers(ctx, userTenantIndexPrefix+tenant).Result()
	if err != nil {
		return nil, fmt.Errorf("redis smembers tenant index: %w", err)
	}
	if len(ids) == 0 {
		return []*User{}, nil
	}

	// Pipeline GET for each user ID ref
	pipe := s.client.Pipeline()
	refCmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		refCmds[i] = pipe.Get(ctx, userIDKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("redis pipeline id refs: %w", err)
	}

	// Pipeline GET for each user record
	pipe2 := s.client.Pipeline()
	type refEntry struct {
		cmd *redis.StringCmd
	}
	var entries []refEntry
	for _, cmd := range refCmds {
		ref, err := cmd.Result()
		if err != nil {
			continue // skip missing
		}
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 {
			continue
		}
		entries = append(entries, refEntry{cmd: pipe2.Get(ctx, userKey(parts[0], parts[1]))})
	}
	if len(entries) == 0 {
		return []*User{}, nil
	}
	if _, err := pipe2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("redis pipeline user records: %w", err)
	}

	var users []*User
	for _, e := range entries {
		data, err := e.cmd.Bytes()
		if err != nil {
			continue
		}
		var rec userRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		u := rec.toUser()
		if !u.Disabled {
			users = append(users, u)
		}
	}
	return users, nil
}

// Update updates mutable fields of an existing user (email, display name, role).
func (s *RedisUserStore) Update(ctx context.Context, user *User) error {
	if user == nil || user.ID == "" {
		return fmt.Errorf("user with ID required")
	}

	existing, err := s.GetByID(ctx, user.ID)
	if err != nil {
		return err
	}

	oldEmail := existing.Email

	// Merge fields
	if user.Email != "" {
		existing.Email = user.Email
	}
	if user.DisplayName != "" {
		existing.DisplayName = user.DisplayName
	}
	if user.Role != "" {
		existing.Role = user.Role
	}
	existing.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(toUserRecord(existing))
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, userKey(existing.Tenant, existing.Username), data, 0)

	// Update email index if changed
	if existing.Email != oldEmail {
		if oldEmail != "" {
			pipe.Del(ctx, userEmailKey(existing.Tenant, oldEmail))
		}
		if existing.Email != "" {
			pipe.Set(ctx, userEmailKey(existing.Tenant, existing.Email), existing.Username, 0)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis update user: %w", err)
	}
	return nil
}

// Delete soft-deletes a user by setting Disabled=true.
func (s *RedisUserStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("user id required")
	}

	user, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	user.Disabled = true
	user.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(toUserRecord(user))
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, userKey(user.Tenant, user.Username), data, 0)
	// Clean up email index so the address can be reused.
	if user.Email != "" {
		pipe.Del(ctx, userEmailKey(user.Tenant, user.Email))
	}
	// Remove from tenant index so the user no longer appears in listings.
	pipe.SRem(ctx, userTenantIndexPrefix+user.Tenant, user.Username)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis soft-delete user: %w", err)
	}
	return nil
}

// Login throttle constants.
const loginFailedPrefix = "login:failed:"

func maxLoginAttempts() int {
	return intFromEnv("MAX_LOGIN_ATTEMPTS", 5)
}

func loginLockoutPeriod() time.Duration {
	return durationFromEnv("LOGIN_LOCKOUT_PERIOD", 15*time.Minute)
}

func bcryptCostFromEnv() int {
	return intFromEnv("CORDUM_BCRYPT_COST", defaultBcryptCost)
}

// ErrLoginThrottled is returned when too many failed login attempts are detected.
var ErrLoginThrottled = errors.New("too many failed login attempts, try again later")

// loginThrottleKey returns the Redis key for tracking failed login attempts.
func loginThrottleKey(username string) string {
	return loginFailedPrefix + strings.ToLower(strings.TrimSpace(username))
}

// CheckLoginThrottle returns ErrLoginThrottled if the username has exceeded
// the maximum number of failed login attempts within the lockout period.
func (s *RedisUserStore) CheckLoginThrottle(ctx context.Context, username string) error {
	count, err := s.client.Get(ctx, loginThrottleKey(username)).Int()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return nil // fail open — don't block logins if Redis has issues
	}
	if count >= maxLoginAttempts() {
		return ErrLoginThrottled
	}
	return nil
}

// RecordFailedLogin increments the failed login counter for a username.
// Sets a TTL of loginLockoutPeriod() on the key.
func (s *RedisUserStore) RecordFailedLogin(ctx context.Context, username string) {
	key := loginThrottleKey(username)
	pipe := s.client.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, loginLockoutPeriod())
	if _, err := pipe.Exec(ctx); err != nil {
		slog.WarnContext(ctx, "failed to record login attempt", "username", username, "error", err)
	}
}

// ClearFailedLogins removes the failed login counter on successful auth.
func (s *RedisUserStore) ClearFailedLogins(ctx context.Context, username string) {
	if err := s.client.Del(ctx, loginThrottleKey(username)).Err(); err != nil {
		slog.WarnContext(ctx, "failed to clear login attempts", "username", username, "error", err)
	}
}

// Close closes the Redis client connection.
func (s *RedisUserStore) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// backfillTenantIndex scans for existing user:id:* keys and adds their IDs
// to the tenant user set. This is idempotent (SADD ignores duplicates).
func (s *RedisUserStore) backfillTenantIndex(ctx context.Context) error {
	var cursor uint64
	var count int
	for {
		keys, next, err := s.client.Scan(ctx, cursor, "user:id:*", 100).Result()
		if err != nil {
			return fmt.Errorf("redis scan: %w", err)
		}
		for _, key := range keys {
			ref, err := s.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			// ref is "tenant:username", key is "user:id:<uuid>"
			parts := strings.SplitN(ref, ":", 2)
			if len(parts) != 2 {
				continue
			}
			tenant := parts[0]
			userID := strings.TrimPrefix(key, "user:id:")
			s.client.SAdd(ctx, userTenantIndexPrefix+tenant, userID)
			count++
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if count > 0 {
		slog.Info("user store: backfilled tenant index", "users", count)
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

// SeedDefaultAdminUser creates a default admin user from environment variables if configured.
// Environment variables:
//   - CORDUM_ADMIN_USERNAME (default: "admin")
//   - CORDUM_ADMIN_PASSWORD (required for user creation)
//   - CORDUM_ADMIN_EMAIL (optional)
func SeedDefaultAdminUser(ctx context.Context, store UserStore, tenant string) error {
	username := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_USERNAME"))
	password := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_PASSWORD"))
	email := strings.TrimSpace(os.Getenv("CORDUM_ADMIN_EMAIL"))

	if username == "" {
		username = "admin"
	}
	if password == "" {
		return fmt.Errorf("cordum_admin_password is required when user auth is enabled")
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
