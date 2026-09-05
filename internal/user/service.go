package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JonasBorgesLM/moat/validate"
	"golang.org/x/crypto/bcrypt"
)

// Password length bounds. minPasswordLen is a baseline strength floor, not
// a full policy (no character-class rules — see Future Improvements in
// docs/ARCHITECTURE.md). maxPasswordLen exists because bcrypt itself
// rejects input over 72 bytes (golang.org/x/crypto/bcrypt.GenerateFromPassword
// returns an error rather than silently truncating) — bounding it here
// gives a clear ErrInvalidInput instead of a confusing failure surfacing
// from inside the hashing call.
const (
	minPasswordLen = 8
	maxPasswordLen = 72
	maxEmailLen    = 320 // RFC 5321's upper bound for a full email address
)

// sessionTokenBytes is the amount of crypto/rand entropy behind every
// issued session token (32 bytes = 256 bits, hex-encoded to 64
// characters) — see Service.CreateSession.
const sessionTokenBytes = 32

// dummyPasswordHash is a bcrypt hash of an arbitrary fixed string, used
// only to burn roughly the same CPU time as a real password comparison
// when Authenticate is called with an email that isn't registered — see
// Authenticate's doc comment for why. Computed once at package init
// rather than per-call, since bcrypt.GenerateFromPassword is itself the
// expensive operation this exists to avoid repeating.
var dummyPasswordHash = mustHashDummyPassword()

func mustHashDummyPassword() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("timing-attack-mitigation-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		// GenerateFromPassword only fails for a cost outside bcrypt's
		// valid range or a >72-byte input — bcrypt.DefaultCost and the
		// fixed string above are both compile-time constants, so this is
		// unreachable in practice; a package that can't even start up
		// correctly is a louder, clearer failure than a silent timing
		// side channel.
		panic(fmt.Sprintf("user: failed to precompute dummy bcrypt hash: %v", err))
	}
	return hash
}

// Service holds the business logic for user accounts and session-based
// authentication.
type Service struct {
	repo               Repository
	sessionTTL         time.Duration
	maxSessionsPerUser int
}

// NewService returns a new Service with the given Repository. sessionTTL
// bounds how long a token issued by CreateSession remains valid — see
// config.Config.AuthSessionTTL, the only place that sets it.
// maxSessionsPerUser bounds how many of a user's sessions CreateSession
// keeps alive at once — see config.Config.AuthMaxSessionsPerUser and
// Repository.CreateSession's doc comment for what happens past it.
func NewService(repo Repository, sessionTTL time.Duration, maxSessionsPerUser int) *Service {
	return &Service{repo: repo, sessionTTL: sessionTTL, maxSessionsPerUser: maxSessionsPerUser}
}

// newID generates a random UUID v4. Duplicated from task.Service's
// identical helper rather than shared: the two domain packages
// deliberately don't import each other (see CLAUDE.md), and this is a
// handful of lines, not worth a shared package for.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Register validates email/password, hashes the password with bcrypt, and
// creates a new User. Returns ErrAlreadyExists if email is already
// registered, ErrInvalidInput if either field fails validation.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	email, err := validateEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("register: hash password: %w", err)
	}

	id, err := newID()
	if err != nil {
		return User{}, fmt.Errorf("register: %w", err)
	}

	now := time.Now()
	u := User{
		ID:           id,
		Email:        email,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateUser(ctx, u); err != nil {
		return User{}, fmt.Errorf("register: %w", err)
	}

	return u, nil
}

// Authenticate verifies email/password against the stored user and hash.
// Returns ErrInvalidCredentials for both an unknown email and a wrong
// password — see that error's doc comment for why the distinction is
// deliberately not exposed.
//
// The unknown-email branch still runs a bcrypt comparison, against a
// fixed dummy hash, before returning — bcrypt.CompareHashAndPassword is
// deliberately slow (that's the point of bcrypt), so skipping it entirely
// for an unknown email would make that response measurably faster than a
// known-email-wrong-password response. A caller measuring response time
// could use that gap to enumerate registered emails despite the identical
// error message and status code. Running the same comparison either way
// closes that timing side channel.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	u, err := s.repo.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("authenticate: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}

	return u, nil
}

// ChangePassword verifies currentPassword against userID's stored hash,
// replaces it with a hash of newPassword, and revokes every other
// session belonging to userID — every one except currentSessionToken,
// the one that authenticated this call.
//
// Returns ErrInvalidCredentials if currentPassword is wrong (including
// the essentially unreachable case of userID no longer existing — same
// error either way, the same non-distinguishing discipline
// Authenticate follows). Returns ErrInvalidInput if newPassword fails
// validatePassword.
//
// Deliberately not a distinct new error/status for "current password
// wrong" versus "new password invalid": both are input validation
// problems from the caller's point of view, and handleServiceError
// already maps ErrInvalidCredentials to 401 and ErrInvalidInput to 400,
// matching what issue #196 asks for without inventing a third sentinel.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword, currentSessionToken string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	u, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("change password: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("change password: hash password: %w", err)
	}

	if err := s.repo.UpdateUserPassword(ctx, userID, string(hash)); err != nil {
		return fmt.Errorf("change password: %w", err)
	}

	// Runs after the password is already changed, not before: a failure
	// here would leave the new password in place with old sessions still
	// alive, which is exactly the state issue #196 exists to close —
	// better to report it (as a 500, via the wrap below) than to silently
	// treat the password change as if it hadn't happened.
	if err := s.repo.DeleteSessionsForUserExcept(ctx, userID, hashToken(currentSessionToken)); err != nil {
		return fmt.Errorf("change password: revoke other sessions: %w", err)
	}

	return nil
}

// CreateSession issues a new bearer token for userID, valid for
// Service's configured sessionTTL. The raw token is returned to the
// caller exactly once — only its SHA-256 hash is persisted (see Session's
// doc comment) — so it cannot be recovered from the database afterward;
// losing it means the session is only reachable via Logout(that token) or
// its own expiry.
func (s *Service) CreateSession(ctx context.Context, userID string) (token string, expiresAt time.Time, err error) {
	var b [sessionTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	token = hex.EncodeToString(b[:])

	now := time.Now()
	expiresAt = now.Add(s.sessionTTL)

	session := Session{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}
	if err := s.repo.CreateSession(ctx, session, s.maxSessionsPerUser); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}

	return token, expiresAt, nil
}

// LogoutAll deletes every session belonging to userID — including the
// one that authenticated the request calling this, which the caller
// should expect: this is the "sign out everywhere" operation, for a
// user who suspects a token of theirs has leaked and wants every live
// session gone at once rather than needing to know which one to target.
// Deleting a user with no sessions is not an error, the same
// idempotency Logout already has for one token.
func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	if err := s.repo.DeleteSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("logout all: %w", err)
	}
	return nil
}

// ValidateToken looks up the session for token and returns the owning
// user's ID. Returns ErrNotFound if the token is unknown or has expired —
// an expired session is lazily deleted here rather than by a scheduled
// job (see 0003_create_sessions_table.up.sql's doc comment).
func (s *Service) ValidateToken(ctx context.Context, token string) (userID string, err error) {
	session, err := s.repo.FindSessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		return "", fmt.Errorf("validate token: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		_ = s.repo.DeleteSession(ctx, session.TokenHash)
		return "", ErrNotFound
	}

	return session.UserID, nil
}

// Logout deletes the session for token, if any. Deleting an unknown token
// is not an error — logging out is idempotent, same as
// task.Service.CompleteTask's idempotency for an already-done task.
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.repo.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// PruneExpiredSessions deletes every session whose ExpiresAt has already
// passed. ValidateToken already rejects an expired session lazily, the
// moment someone tries to use it — this exists only to bound storage
// growth from sessions that are simply abandoned and never reused again
// (a closed browser tab, a token nobody ever sends back). Intended to be
// called periodically, not from any request path — see
// cmd/api/main.go's runPeriodicCleanup.
func (s *Service) PruneExpiredSessions(ctx context.Context) error {
	if err := s.repo.DeleteExpiredSessions(ctx, time.Now()); err != nil {
		return fmt.Errorf("prune expired sessions: %w", err)
	}
	return nil
}

// GetUser retrieves a User by ID, for GET /auth/me.
func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	u, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// hashToken returns the hex-encoded SHA-256 hash of a raw session token —
// what's actually persisted and looked up, never the token itself.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// normalizeEmail trims whitespace and lowercases email, so
// "User@Example.com" and "user@example.com" are treated as the same
// address everywhere an email is looked up or compared — both here and
// in Authenticate. Applied before the UNIQUE constraint on users.email
// ever sees the value (that constraint is case-sensitive at the schema
// level; see internal/user/postgres_repository_test.go's
// TestPostgres_Schema_UniqueConstraintIsCaseSensitive), so normalization
// happening once, here, is what actually prevents two accounts differing
// only in email case.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validateEmail normalizes email (see normalizeEmail) and validates it is
// non-empty, contains "@", and is at most maxEmailLen characters. This is
// a deliberately shallow check — full RFC 5322 validation is out of
// scope; confirming deliverability would require actually sending mail,
// which this project doesn't do.
func validateEmail(email string) (string, error) {
	email = normalizeEmail(email)

	// The rules run against the normalized value, so the address that is
	// validated is the one that will be stored and later looked up.
	//
	// This replaced a hand-rolled `strings.Contains(email, "@")`, which
	// accepted "@", "a@" and "@b" — enough to register an account whose
	// address can never receive anything.
	//
	// Required() is named explicitly rather than relied upon: validate's
	// rules do not skip empty values, so Email() would reject "" on its
	// own, but stating the constraint keeps the error message accurate
	// about which one failed.
	//
	// MaxLen counts runes, which is what maxEmailLen and the VARCHAR
	// column both mean. validatePassword deliberately does *not* use it —
	// see that function.
	if err := validate.Validate("email", email,
		validate.Required(),
		validate.Email(),
		validate.MaxLen(maxEmailLen),
	); err != nil {
		// validate's messages describe the constraint and never echo the
		// offending value back — the same rule the rest of this package
		// follows, since an error that reflects attacker-controlled input
		// ends up in logs and response bodies.
		return "", fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}

	return email, nil
}

// validatePassword checks password length is within
// [minPasswordLen, maxPasswordLen]. Measured in bytes, not runes: bcrypt
// itself operates on the byte representation and rejects input over 72
// bytes, so byte length is the constraint that actually matters here
// (unlike task.Service's title/description limits, which are a display
// concern and correctly measured in runes).
func validatePassword(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidInput, maxPasswordLen)
	}
	return nil
}
