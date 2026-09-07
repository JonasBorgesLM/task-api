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

// Password length bounds. minPasswordLen is a baseline strength floor —
// see validatePassword's doc comment for the checks layered on top of it
// (common-password and predictable-pattern rejection, issue #218).
// maxPasswordLen exists because bcrypt itself rejects input over 72 bytes
// (golang.org/x/crypto/bcrypt.GenerateFromPassword returns an error
// rather than silently truncating) — bounding it here gives a clear
// ErrInvalidInput instead of a confusing failure surfacing from inside
// the hashing call.
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
	tokenCache         *tokenCache
	loginBackoff       *loginBackoff
	// sleep exists so tests can observe/skip the login-backoff delay
	// without a real wait — see login_backoff_test.go and
	// service_test.go's Authenticate backoff cases. Always
	// sleepUnlessDone in production; NewService sets it and nothing
	// outside this file ever overrides it again.
	sleep func(context.Context, time.Duration)
}

// NewService returns a new Service with the given Repository. sessionTTL
// bounds how long a token issued by CreateSession remains valid — see
// config.Config.AuthSessionTTL, the only place that sets it.
// maxSessionsPerUser bounds how many of a user's sessions CreateSession
// keeps alive at once — see config.Config.AuthMaxSessionsPerUser and
// Repository.CreateSession's doc comment for what happens past it.
func NewService(repo Repository, sessionTTL time.Duration, maxSessionsPerUser int) *Service {
	return &Service{
		repo:               repo,
		sessionTTL:         sessionTTL,
		maxSessionsPerUser: maxSessionsPerUser,
		tokenCache:         newTokenCache(tokenCacheTTL),
		loginBackoff:       newLoginBackoff(),
		sleep:              sleepUnlessDone,
	}
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
//
// A second, distinct timing concern this same function closes (issue
// #220): none of the three tiers of address/user-keyed rate limiting in
// cmd/api/main.go bound the number of attempts against one specific
// account, since a distributed attacker trying passwords against a
// known email presents as many addresses each making few attempts —
// exactly the profile every address-keyed limiter treats as normal.
// loginBackoff tracks consecutive failures per normalized email and
// Authenticate applies the resulting delay to *every* outcome below
// (unknown email, wrong password, or success) rather than only to
// failures — an outcome-dependent delay would itself be a timing oracle
// for "is this account currently being throttled", undermining the same
// indistinguishability ErrInvalidCredentials already protects. The delay
// only ever slows an account down, capped at loginBackoffCap, and a
// correct password still succeeds after paying it — a hard lock would be
// denial-of-service against the legitimate owner, the middle ground the
// issue explicitly asks for. See docs/DECISIONS.md § "Atraso progressivo
// por conta" for the curve's numbers and what this does not cover.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	normalized := normalizeEmail(email)
	delay := s.loginBackoff.delay(normalized)

	u, err := s.repo.FindUserByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
			s.loginBackoff.recordFailure(normalized)
			s.sleep(ctx, delay)
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("authenticate: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		s.loginBackoff.recordFailure(normalized)
		s.sleep(ctx, delay)
		return User{}, ErrInvalidCredentials
	}

	s.loginBackoff.recordSuccess(normalized)
	s.sleep(ctx, delay)
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

	if err := s.verifyPassword(ctx, userID, currentPassword); err != nil {
		return err
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
	s.tokenCache.deleteAllForUserExcept(userID, hashToken(currentSessionToken))

	return nil
}

// verifyPassword confirms password matches userID's stored hash, without
// changing anything. Returns ErrInvalidCredentials for either a wrong
// password or (essentially unreachable in practice) userID no longer
// existing — the same non-distinguishing discipline Authenticate follows
// for its own ErrNotFound branch. Shared by ChangePassword and
// VerifyPassword, the two callers that both need to confirm the caller
// still knows the account's password before something significant
// happens.
func (s *Service) verifyPassword(ctx context.Context, userID, password string) error {
	u, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("verify password: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// VerifyPassword confirms password matches userID's stored hash. Used by
// Handler.deleteAccount before it runs the cross-domain cascade (every
// task and attachment belonging to the account) that this package
// cannot reach itself (see CLAUDE.md's layering, and
// AccountCascadeFunc's doc comment) — proving the caller still knows the
// password has to happen before anything irreversible does, not folded
// into DeleteAccount itself, which runs only after that cascade already
// succeeded.
func (s *Service) VerifyPassword(ctx context.Context, userID, password string) error {
	return s.verifyPassword(ctx, userID, password)
}

// DeleteAccount deletes every session belonging to userID and the
// account itself. Callers must have already removed everything else the
// account owns (tasks, attachments — rows and bytes) and verified the
// caller's password (see VerifyPassword) — this method does neither: the
// former lives outside what this package can reach at all, and the
// latter has already been proven true by the time this runs.
//
// Sessions are deleted explicitly, not left to sessions.user_id's own
// ON DELETE CASCADE: memoryRepository has no such cascade to rely on
// (see its own doc comment), so the two backends would otherwise
// disagree about whether a session outlives its user for even an
// instant. The user row goes last — after every task, or PostgreSQL's
// FK on tasks.user_id refuses this same call (see Repository.DeleteUser).
func (s *Service) DeleteAccount(ctx context.Context, userID string) error {
	if err := s.repo.DeleteSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete account: revoke sessions: %w", err)
	}
	s.tokenCache.deleteAllForUser(userID)
	if err := s.repo.DeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("delete account: %w", err)
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
	s.tokenCache.deleteAllForUser(userID)
	return nil
}

// SessionInfo is the subset of Session data GET /v1/auth/sessions (issue
// #224) is allowed to expose: never TokenHash itself (see
// deriveSessionID's doc comment for why an opaque ID is derived from it
// instead). IsCurrent marks the session that authenticated the request
// asking for this list — the only way a caller can know which of its own
// sessions is "this one", since it has no other way to compare its own
// raw token against a list of hashes it never sees.
type SessionInfo struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
	IsCurrent bool
}

// ListSessions returns every session belonging to userID, newest first —
// backs GET /v1/auth/sessions. currentToken is the raw token that
// authenticated this very request (from middleware.SessionTokenFromContext),
// used only to compute which entry's IsCurrent is true.
func (s *Service) ListSessions(ctx context.Context, userID, currentToken string) ([]SessionInfo, error) {
	sessions, err := s.repo.FindSessionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	currentHash := hashToken(currentToken)
	infos := make([]SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		infos = append(infos, SessionInfo{
			ID:        deriveSessionID(sess.TokenHash),
			CreatedAt: sess.CreatedAt,
			ExpiresAt: sess.ExpiresAt,
			IsCurrent: sess.TokenHash == currentHash,
		})
	}
	return infos, nil
}

// RevokeSession deletes the one session belonging to userID whose
// derived ID (see deriveSessionID) is sessionID — backs
// DELETE /v1/auth/sessions/{id}. Returns ErrNotFound if no session of
// userID's matches, including when sessionID addresses a real session
// belonging to a *different* user: the same never-confirm-existence
// discipline every other single-row lookup in this codebase follows for
// a row it does not own (see CLAUDE.md's domain-errors rule).
//
// Revoking the session that authenticated this very call is allowed —
// nothing here special-cases it. That is exactly what POST /auth/logout
// already does under a different address; this endpoint exists to
// revoke any *other* session by its own identity, and refusing it for
// the caller's own session would be an arbitrary inconsistency, not a
// safety measure.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	sessions, err := s.repo.FindSessionsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	for _, sess := range sessions {
		if deriveSessionID(sess.TokenHash) != sessionID {
			continue
		}
		if err := s.repo.DeleteSession(ctx, sess.TokenHash); err != nil {
			return fmt.Errorf("revoke session: %w", err)
		}
		// Same-process immediacy tokenCache's own doc comment describes
		// for Logout/LogoutAll: a revoked session must stop validating
		// in *this* process right away, not linger for up to
		// tokenCacheTTL just because this revocation happened to arrive
		// through a different endpoint than usual.
		s.tokenCache.delete(sess.TokenHash)
		return nil
	}
	return ErrNotFound
}

// ValidateToken looks up the session for token and returns the owning
// user's ID. Returns ErrNotFound if the token is unknown or has expired —
// an expired session is lazily deleted here rather than by a scheduled
// job (see 0003_create_sessions_table.up.sql's doc comment).
//
// Checks tokenCache before Repository: on every request this is the
// hottest call in the codebase (RequireAuth runs it for every
// authenticated route), and a cache hit skips the database read
// entirely. Only a genuinely successful lookup is cached — an unknown
// or expired token is never memoized as valid, so this cannot turn a
// briefly-invalid token into a validated one. See tokenCache's own doc
// comment and docs/DECISIONS.md § "Cache de ValidateToken" for what
// this trades away (a bounded revocation-delay window) and why that
// bound is where it is.
func (s *Service) ValidateToken(ctx context.Context, token string) (userID string, err error) {
	hash := hashToken(token)

	if cachedUserID, ok := s.tokenCache.get(hash); ok {
		return cachedUserID, nil
	}

	session, err := s.repo.FindSessionByTokenHash(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("validate token: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		_ = s.repo.DeleteSession(ctx, session.TokenHash)
		return "", ErrNotFound
	}

	s.tokenCache.set(hash, session.UserID)
	return session.UserID, nil
}

// Logout deletes the session for token, if any. Deleting an unknown token
// is not an error — logging out is idempotent, same as
// task.Service.CompleteTask's idempotency for an already-done task.
func (s *Service) Logout(ctx context.Context, token string) error {
	hash := hashToken(token)
	if err := s.repo.DeleteSession(ctx, hash); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	// After the repository call succeeds, not before: this process's own
	// cache should only forget a token once it is actually gone from
	// storage — see tokenCache's doc comment for why this same-process
	// invalidation is what keeps a revoked token from validating again
	// here even within tokenCacheTTL.
	s.tokenCache.delete(hash)
	return nil
}

// PruneExpiredSessions deletes every session whose ExpiresAt has already
// passed. ValidateToken already rejects an expired session lazily, the
// moment someone tries to use it — this exists only to bound storage
// growth from sessions that are simply abandoned and never reused again
// (a closed browser tab, a token nobody ever sends back). Intended to be
// called periodically, not from any request path — see
// cmd/api/main.go's runPeriodicCleanup.
//
// Also sweeps tokenCache and loginBackoff, the same bound-abandoned-
// growth reasoning applied to both in-process maps rather than the
// sessions table: a token validated once and never again, or an account
// that failed a few logins and was never attempted again, would
// otherwise sit in those maps harmlessly forever. Bundled into this same
// call rather than separate exported methods — cmd/api's
// runPeriodicCleanup already treats this as "the session-housekeeping
// pass," and both caches are session/account housekeeping too.
func (s *Service) PruneExpiredSessions(ctx context.Context) error {
	s.tokenCache.sweep()
	s.loginBackoff.sweep()
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

// sessionIDPrefix domain-separates deriveSessionID's hash from
// hashToken's own use of SHA-256, so the two purposes (hashing a raw
// token vs. deriving a session's public ID from its already-hashed
// value) can never collide even though both eventually hash something
// derived from the same underlying session. Changing this changes every
// session's exposed ID — only do that with a reason recorded in
// docs/DECISIONS.md.
const sessionIDPrefix = "session-id-v1:"

// deriveSessionID computes GET/DELETE /v1/auth/sessions' opaque
// identifier (issue #224) for a session from its TokenHash, rather than
// storing a separate id column: see docs/DECISIONS.md § "Tela de
// sessões ativas" for why, and what it costs — ListSessions/
// RevokeSession recompute this for every row a
// Repository.FindSessionsForUser call returns (bounded by
// AuthMaxSessionsPerUser, small) instead of doing a single indexed
// lookup by a stored id.
//
// Not reversible back to TokenHash — a second, domain-separated SHA-256
// application — though that is defense in depth rather than the actual
// requirement: TokenHash is already a value nothing legitimate needs to
// reconstruct, since the one truly sensitive input (the raw token)
// never entered this derivation chain at all.
func deriveSessionID(tokenHash string) string {
	sum := sha256.Sum256([]byte(sessionIDPrefix + tokenHash))
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
// [minPasswordLen, maxPasswordLen] and rejects the most predictable
// passwords a length floor alone lets through (issue #218 — length was
// the only check here before this, and "12345678"/"password" both
// satisfied it). Length is measured in bytes, not runes: bcrypt itself
// operates on the byte representation and rejects input over 72 bytes, so
// byte length is the constraint that actually matters here (unlike
// task.Service's title/description limits, which are a display concern
// and correctly measured in runes).
//
// Deliberately no mandatory character-class composition rule (no "must
// contain a digit AND a symbol"). NIST SP 800-63B recommends against
// exactly that: it pushes users toward predictable decorations —
// "Password1!" is the standard example, and it is also the reason
// commonWeakPasswords includes it explicitly — without meaningfully
// raising real entropy. Checking against a known-weak set and against
// trivially generated patterns (isCommonWeakPassword, isSequentialRun,
// isSingleRepeatedRune) targets what actually makes a password guessable
// first, instead of what merely makes it *look* complex.
//
// What this does not cover, by design (see docs/DECISIONS.md § "Validação
// de senha forte" for the trade-off and the rejected alternative): a live
// breach-database check (e.g. Have I Been Pwned's k-anonymity API) would
// catch real leaked passwords this local, dependency-free list cannot
// enumerate in advance, at the cost of a network call — and a decision
// about what happens when that service is unreachable — on every
// registration and password change.
func validatePassword(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidInput, maxPasswordLen)
	}
	if isCommonWeakPassword(password) {
		return fmt.Errorf("%w: password is too common — choose one that isn't on a list of frequently used passwords", ErrInvalidInput)
	}
	if isSingleRepeatedRune(password) || isSequentialRun(password) {
		return fmt.Errorf("%w: password is too predictable (repeated or sequential characters)", ErrInvalidInput)
	}
	return nil
}
