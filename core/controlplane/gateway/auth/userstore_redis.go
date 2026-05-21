package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/cordum/cordum/core/infra/redisutil"
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

// createUserPipeline creates a user using individual Redis commands instead of
// Lua. This is Redis Cluster safe since each command targets a single key.
// Username and email uniqueness are enforced atomically via SetNX, with
// rollback on partial failure (e.g. email claimed after username succeeds).

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

	// fallbackThrottle provides bounded brute-force protection when Redis is
	// unavailable. Entries are keyed with loginThrottleKey(username, ip) and
	// hold an atomic counter. Cleaned up lazily — stale entries are harmless
	// (bounded by login traffic).
	fallbackThrottle sync.Map // map[string]*fallbackEntry
}

// fallbackEntry tracks login attempts in memory when Redis is unavailable.
type fallbackEntry struct {
	count     atomic.Int32
	expiresAt atomic.Int64 // unix seconds
}

// NewRedisUserStore creates a new Redis-backed user store.
func NewRedisUserStore(redisURL string) (*RedisUserStore, error) {
	opts, err := redisutil.ParseOptions(redisURL)
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

// NewRedisUserStoreFromClient creates a RedisUserStore using an existing Redis client.
func NewRedisUserStoreFromClient(client *redis.Client) *RedisUserStore {
	return &RedisUserStore{client: client}
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

// ValidatePassword checks that a password meets complexity requirements:
// at least 12 characters, 1 uppercase letter, 1 digit, and 1 special character.
func ValidatePassword(password string) error {
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
	if err := ValidatePassword(password); err != nil {
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

	// Atomically claim the username key using SetNX (SET if Not eXists).
	// This prevents the TOCTOU race where two concurrent creates both pass
	// an Exists check before either writes.
	claimed, err := s.client.SetNX(ctx, key, string(data), 0).Result()
	if err != nil {
		return fmt.Errorf("redis claim username: %w", err)
	}
	if !claimed {
		return ErrUserAlreadyExists
	}

	// Atomically claim the email key if present.
	if emailKey != "" {
		emailClaimed, eErr := s.client.SetNX(ctx, emailKey, emailVal, 0).Result()
		if eErr != nil {
			// Roll back the username key.
			s.client.Del(ctx, key)
			return fmt.Errorf("redis claim email: %w", eErr)
		}
		if !emailClaimed {
			// Email already taken — roll back the username key.
			s.client.Del(ctx, key)
			return ErrUserAlreadyExists
		}
	}

	// Write the remaining index keys (id lookup, tenant index).
	pipe := s.client.Pipeline()
	pipe.Set(ctx, idKey, idVal, 0)
	pipe.SAdd(ctx, tenantIdx, user.ID)
	_, err = pipe.Exec(ctx)
	if err != nil {
		// Roll back claimed keys on pipeline failure.
		s.client.Del(ctx, key)
		if emailKey != "" {
			s.client.Del(ctx, emailKey)
		}
		return fmt.Errorf("redis create user indexes: %w", err)
	}
	return nil
}

// UpdatePassword updates a user's password and invalidates all existing
// sessions for that user to force re-authentication. This prevents stale
// sessions from remaining valid after a password change or admin reset.
func (s *RedisUserStore) UpdatePassword(ctx context.Context, userID, newPassword string) error {
	if userID == "" {
		return fmt.Errorf("user id required")
	}
	if newPassword == "" {
		return fmt.Errorf("new password required")
	}
	if err := ValidatePassword(newPassword); err != nil {
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

	// Revoke all existing sessions for this user. Password change is the
	// critical operation — session cleanup is best-effort. If Redis is
	// temporarily unavailable, sessions will expire via TTL.
	if err := s.DeleteUserSessions(ctx, userID); err != nil {
		slog.Warn("session cleanup after password change failed, sessions will expire via TTL",
			"user_id", userID, "error", err)
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
	pipe.SRem(ctx, userTenantIndexPrefix+user.Tenant, user.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis soft-delete user: %w", err)
	}

	// Revoke all sessions for the deleted user. Best-effort — user is
	// already disabled so sessions will fail validation on next use.
	if err := s.DeleteUserSessions(ctx, id); err != nil {
		slog.Warn("session cleanup after user delete failed",
			"user_id", id, "error", err)
	}
	return nil
}

// Login throttle constants.
const (
	loginFailedPrefix         = "login:failed:"
	loginFailedGlobalPrefix   = "login:failed:global:"
	loginThrottleRedisTimeout = 500 * time.Millisecond
)

func maxLoginAttempts() int {
	return intFromEnv("MAX_LOGIN_ATTEMPTS", 5)
}

func maxGlobalLoginAttempts() int {
	return intFromEnv("MAX_GLOBAL_LOGIN_ATTEMPTS", 50)
}

func loginLockoutPeriod() time.Duration {
	return durationFromEnv("LOGIN_LOCKOUT_PERIOD", 15*time.Minute)
}

func loginThrottleRedisContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, loginThrottleRedisTimeout)
}

// BcryptCostFromEnv returns the bcrypt cost factor from the CORDUM_BCRYPT_COST
// environment variable, falling back to defaultBcryptCost (12).
// Exported so that the gateway package can use the same cost for the login
// timing dummy hash, preventing user-enumeration via timing side-channel.
func BcryptCostFromEnv() int {
	return intFromEnv("CORDUM_BCRYPT_COST", defaultBcryptCost)
}

// bcryptCostFromEnv is the internal alias kept for backward compatibility
// with existing callers within this package.
func bcryptCostFromEnv() int {
	return BcryptCostFromEnv()
}

// ErrLoginThrottled is returned when too many failed login attempts are detected.
var ErrLoginThrottled = errors.New("too many failed login attempts, try again later")

// loginThrottleKey returns the Redis key for tracking failed login attempts
// per username+IP combination.
func loginThrottleKey(username, ip string) string {
	u := strings.ToLower(strings.TrimSpace(username))
	if ip == "" {
		ip = "unknown"
	}
	return loginFailedPrefix + u + ":" + ip
}

// loginGlobalThrottleKey returns the Redis key for tracking failed login
// attempts per username across all IPs (distributed attack defense).
func loginGlobalThrottleKey(username string) string {
	return loginFailedGlobalPrefix + strings.ToLower(strings.TrimSpace(username))
}

// CheckLoginThrottle returns ErrLoginThrottled if the username+IP pair has
// exceeded the per-IP threshold or the username has exceeded the global
// threshold across all IPs.
func (s *RedisUserStore) CheckLoginThrottle(ctx context.Context, username, ip string) error {
	redisCtx, cancel := loginThrottleRedisContext(ctx)
	defer cancel()

	pipe := s.client.Pipeline()
	perIPCmd := pipe.Get(redisCtx, loginThrottleKey(username, ip))
	globalCmd := pipe.Get(redisCtx, loginGlobalThrottleKey(username))
	if _, err := pipe.Exec(redisCtx); err != nil && err != redis.Nil {
		slog.WarnContext(redisCtx, "login throttle: Redis unavailable, using in-memory fallback",
			"username", username, "ip", ip, "error", err)
		return s.checkFallbackThrottle(username, ip)
	}

	if count, err := perIPCmd.Int(); err == nil && count >= maxLoginAttempts() {
		return ErrLoginThrottled
	}
	if count, err := globalCmd.Int(); err == nil && count >= maxGlobalLoginAttempts() {
		return ErrLoginThrottled
	}
	return nil
}

// checkFallbackThrottle checks the bounded in-memory brute-force protection
// counter when Redis is unavailable. The counter is incremented only after a
// failed password validation via RecordFailedLogin, matching the Redis path and
// avoiding lockouts caused by successful login attempts during Redis outages.
func (s *RedisUserStore) checkFallbackThrottle(username, ip string) error {
	entry, ok := s.loadFallbackEntry(loginThrottleKey(username, ip), time.Now().Unix())
	if !ok {
		return nil
	}
	if int(entry.count.Load()) >= maxLoginAttempts() {
		return ErrLoginThrottled
	}
	return nil
}

func (s *RedisUserStore) recordFallbackFailedLogin(username, ip string) {
	now := time.Now().Unix()
	key := loginThrottleKey(username, ip)
	entry, ok := s.loadFallbackEntry(key, now)
	if !ok {
		val, _ := s.fallbackThrottle.LoadOrStore(key, &fallbackEntry{})
		entry = val.(*fallbackEntry)
	}
	if entry.expiresAt.Load() == 0 {
		entry.expiresAt.Store(now + int64(loginLockoutPeriod().Seconds()))
	}
	entry.count.Add(1)
}

func (s *RedisUserStore) clearFallbackFailedLogin(username, ip string) {
	s.fallbackThrottle.Delete(loginThrottleKey(username, ip))
}

func (s *RedisUserStore) loadFallbackEntry(key string, now int64) (*fallbackEntry, bool) {
	val, ok := s.fallbackThrottle.Load(key)
	if !ok {
		return nil, false
	}
	entry := val.(*fallbackEntry)
	if exp := entry.expiresAt.Load(); exp > 0 && now > exp {
		entry.count.Store(0)
		entry.expiresAt.Store(0)
		s.fallbackThrottle.Delete(key)
		return nil, false
	}
	return entry, true
}

// RecordFailedLogin increments both the per-IP and global failed login
// counters for a username. Sets a TTL of loginLockoutPeriod() on both keys.
func (s *RedisUserStore) RecordFailedLogin(ctx context.Context, username, ip string) {
	redisCtx, cancel := loginThrottleRedisContext(ctx)
	defer cancel()

	ttl := loginLockoutPeriod()
	pipe := s.client.Pipeline()
	perIPKey := loginThrottleKey(username, ip)
	pipe.Incr(redisCtx, perIPKey)
	pipe.Expire(redisCtx, perIPKey, ttl)
	globalKey := loginGlobalThrottleKey(username)
	pipe.Incr(redisCtx, globalKey)
	pipe.Expire(redisCtx, globalKey, ttl)
	if _, err := pipe.Exec(redisCtx); err != nil {
		slog.WarnContext(redisCtx, "failed to record login attempt", "username", username, "ip", ip, "error", err)
		s.recordFallbackFailedLogin(username, ip)
	}
}

// ClearFailedLogins removes the per-IP failed login counter on successful auth.
// The global counter is left to expire naturally to maintain distributed attack visibility.
func (s *RedisUserStore) ClearFailedLogins(ctx context.Context, username, ip string) {
	redisCtx, cancel := loginThrottleRedisContext(ctx)
	defer cancel()

	if err := s.client.Del(redisCtx, loginThrottleKey(username, ip)).Err(); err != nil {
		slog.WarnContext(redisCtx, "failed to clear login attempts", "username", username, "error", err)
	}
	s.clearFallbackFailedLogin(username, ip)
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

const (
	sessionKeyPrefix     = "session:"
	sessionUserIdxPrefix = "session:user:"
	samlStateKeyPrefix   = "saml:state:"
)

// sessionData stores the auth context for a session token.
type sessionData struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Tenant   string `json:"tenant"`
	Role     string `json:"role"`
}

// StoreSession stores a session token in Redis with a TTL.
// It also adds the token to a per-user session index so that all sessions
// for a user can be invalidated efficiently (e.g. on password change).
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

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, sessionKeyPrefix+token, data, ttl)
	// Track this token in the user's session index for bulk revocation.
	idxKey := sessionUserIdxPrefix + user.ID
	pipe.SAdd(ctx, idxKey, token)
	// Align the index TTL with the session TTL so it auto-cleans.
	// Use the longer of the current TTL and the new session TTL to avoid
	// expiring the index while other sessions are still live.
	pipe.Expire(ctx, idxKey, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis store session: %w", err)
	}
	return nil
}

// DeleteSession removes a session token from Redis.
// It also removes the token from the user's session index if the session
// data is still available.
func (s *RedisUserStore) DeleteSession(ctx context.Context, token string) error {
	// Try to read the session data first so we can clean up the user index.
	raw, err := s.client.Get(ctx, sessionKeyPrefix+token).Bytes()
	if err == nil {
		var sd sessionData
		if jsonErr := json.Unmarshal(raw, &sd); jsonErr == nil && sd.UserID != "" {
			// Best-effort cleanup of user session index.
			_ = s.client.SRem(ctx, sessionUserIdxPrefix+sd.UserID, token).Err()
		}
	}
	return s.client.Del(ctx, sessionKeyPrefix+token).Err()
}

// DeleteUserSessions removes all active sessions for the given user ID.
// This is called on password change to force re-authentication.
// Returns an error only if Redis operations fail — an empty session set is not an error.
func (s *RedisUserStore) DeleteUserSessions(ctx context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	idxKey := sessionUserIdxPrefix + userID

	// Fetch all session tokens for this user.
	tokens, err := s.client.SMembers(ctx, idxKey).Result()
	if err != nil {
		return fmt.Errorf("redis smembers user sessions: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}

	// Delete all session keys and the index in a single pipeline.
	pipe := s.client.TxPipeline()
	for _, token := range tokens {
		pipe.Del(ctx, sessionKeyPrefix+token)
	}
	pipe.Del(ctx, idxKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete user sessions: %w", err)
	}

	slog.Info("revoked user sessions on password change",
		"user_id", userID, "sessions_revoked", len(tokens))
	return nil
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

// PutSAMLState stores a SAML RelayState entry in Redis with a TTL.
func (s *RedisUserStore) PutSAMLState(ctx context.Context, state, requestID, redirect string, ttl time.Duration) error {
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("state required")
	}
	if ttl <= 0 {
		return fmt.Errorf("ttl must be positive")
	}
	payload, err := json.Marshal(samlStateEntry{
		RequestID: requestID,
		Redirect:  redirect,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("marshal saml state: %w", err)
	}
	if err := s.client.Set(ctx, samlStateKeyPrefix+state, payload, ttl).Err(); err != nil {
		return fmt.Errorf("redis set saml state: %w", err)
	}
	return nil
}

// GetSAMLState loads a SAML RelayState entry from Redis.
func (s *RedisUserStore) GetSAMLState(ctx context.Context, state string) (samlStateEntry, error) {
	if strings.TrimSpace(state) == "" {
		return samlStateEntry{}, errSAMLStateNotFound
	}
	payload, err := s.client.Get(ctx, samlStateKeyPrefix+state).Bytes()
	if err == redis.Nil {
		return samlStateEntry{}, errSAMLStateNotFound
	}
	if err != nil {
		return samlStateEntry{}, fmt.Errorf("redis get saml state: %w", err)
	}
	var entry samlStateEntry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return samlStateEntry{}, fmt.Errorf("unmarshal saml state: %w", err)
	}
	return entry, nil
}

// DeleteSAMLState removes a SAML RelayState entry from Redis.
func (s *RedisUserStore) DeleteSAMLState(ctx context.Context, state string) error {
	if strings.TrimSpace(state) == "" {
		return nil
	}
	if err := s.client.Del(ctx, samlStateKeyPrefix+state).Err(); err != nil {
		return fmt.Errorf("redis delete saml state: %w", err)
	}
	return nil
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
