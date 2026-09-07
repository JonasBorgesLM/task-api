package user

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// fakeRepository is a test double for Repository. Each field controls the
// behavior of the corresponding method — the same pattern
// internal/task's fakeRepository uses.
type fakeRepository struct {
	createUserErr error
	savedUser     User

	findUserByEmailUser       User
	findUserByEmailErr        error
	findUserByEmailCalledWith string

	findUserByIDUser User
	findUserByIDErr  error

	updateUserPasswordErr        error
	updateUserPasswordCalledWith struct {
		id           string
		passwordHash string
	}

	deleteUserErr        error
	deleteUserCalledWith string

	createSessionErr        error
	savedSession            Session
	createSessionCalledWith int // maxSessions passed to CreateSession

	findSessionByHashSession   Session
	findSessionByHashErr       error
	findSessionByHashCallCount int

	deleteSessionErr error
	deletedTokenHash string

	deleteSessionsForUserErr        error
	deleteSessionsForUserCalledWith string

	deleteSessionsForUserExceptErr        error
	deleteSessionsForUserExceptCalledWith struct {
		userID        string
		keepTokenHash string
	}

	deleteExpiredSessionsErr        error
	deleteExpiredSessionsCalledWith time.Time

	findSessionsForUserSessions   []Session
	findSessionsForUserErr        error
	findSessionsForUserCalledWith string
}

func (f *fakeRepository) CreateUser(_ context.Context, u User) error {
	f.savedUser = u
	return f.createUserErr
}

func (f *fakeRepository) FindUserByEmail(_ context.Context, email string) (User, error) {
	f.findUserByEmailCalledWith = email
	return f.findUserByEmailUser, f.findUserByEmailErr
}

func (f *fakeRepository) FindUserByID(_ context.Context, _ string) (User, error) {
	return f.findUserByIDUser, f.findUserByIDErr
}

func (f *fakeRepository) UpdateUserPassword(_ context.Context, id, passwordHash string) error {
	f.updateUserPasswordCalledWith.id = id
	f.updateUserPasswordCalledWith.passwordHash = passwordHash
	return f.updateUserPasswordErr
}

func (f *fakeRepository) DeleteUser(_ context.Context, id string) error {
	f.deleteUserCalledWith = id
	return f.deleteUserErr
}

func (f *fakeRepository) CreateSession(_ context.Context, s Session, maxSessions int) error {
	f.savedSession = s
	f.createSessionCalledWith = maxSessions
	return f.createSessionErr
}

func (f *fakeRepository) DeleteSessionsForUser(_ context.Context, userID string) error {
	f.deleteSessionsForUserCalledWith = userID
	return f.deleteSessionsForUserErr
}

func (f *fakeRepository) DeleteSessionsForUserExcept(_ context.Context, userID, keepTokenHash string) error {
	f.deleteSessionsForUserExceptCalledWith.userID = userID
	f.deleteSessionsForUserExceptCalledWith.keepTokenHash = keepTokenHash
	return f.deleteSessionsForUserExceptErr
}

func (f *fakeRepository) FindSessionByTokenHash(_ context.Context, _ string) (Session, error) {
	f.findSessionByHashCallCount++
	return f.findSessionByHashSession, f.findSessionByHashErr
}

func (f *fakeRepository) DeleteSession(_ context.Context, tokenHash string) error {
	f.deletedTokenHash = tokenHash
	return f.deleteSessionErr
}

func (f *fakeRepository) DeleteExpiredSessions(_ context.Context, now time.Time) error {
	f.deleteExpiredSessionsCalledWith = now
	return f.deleteExpiredSessionsErr
}

func (f *fakeRepository) FindSessionsForUser(_ context.Context, userID string) ([]Session, error) {
	f.findSessionsForUserCalledWith = userID
	return f.findSessionsForUserSessions, f.findSessionsForUserErr
}

const testSessionTTL = time.Hour

// mustHash returns the bcrypt hash of password, failing the test on error.
func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	return string(hash)
}

// freezeTokenCache pins svc's tokenCache clock to a single instant, so a
// test asserting a cache hit/miss depends only on the invalidation logic
// under test, never on how much real wall-clock time a slow step (bcrypt,
// especially under -race on a loaded CI runner) happened to consume
// relative to tokenCacheTTL. Without this, TestChangePassword_* — which
// calls bcrypt twice — was observed taking long enough in CI for the
// cache entry it asserts on to expire for real, failing for the wrong
// reason.
func freezeTokenCache(svc *Service) {
	frozen := time.Now()
	svc.tokenCache.now = func() time.Time { return frozen }
}

// --- Register ---

func TestRegister_Valid(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.Register(context.Background(), "user@example.com", "password123")
	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}

	if got.ID == "" {
		t.Error("Register() ID must not be empty")
	}
	if got.Email != "user@example.com" {
		t.Errorf("Register() Email = %q, want %q", got.Email, "user@example.com")
	}
	if got.PasswordHash == "" || got.PasswordHash == "password123" {
		t.Error("Register() PasswordHash must be a hash, not empty or the plaintext password")
	}
	if bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("password123")) != nil {
		t.Error("Register() PasswordHash does not verify against the original password")
	}
	if repo.savedUser.ID != got.ID {
		t.Errorf("Register() savedUser.ID = %q, want %q", repo.savedUser.ID, got.ID)
	}
}

func TestRegister_TrimsEmail(t *testing.T) {
	svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

	got, err := svc.Register(context.Background(), "  user@example.com  ", "password123")
	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if got.Email != "user@example.com" {
		t.Errorf("Register() Email = %q, want trimmed %q", got.Email, "user@example.com")
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"missing @", "not-an-email"},
		{"too long", strings.Repeat("a", maxEmailLen) + "@example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

			_, err := svc.Register(context.Background(), tc.email, "password123")
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Register(%q) error = %v, want ErrInvalidInput", tc.email, err)
			}
		})
	}
}

func TestRegister_InvalidPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"too short", "short1"},
		{"too long", strings.Repeat("a", maxPasswordLen+1)},
		{"common password", "welcome1"},
		{"common password, different case", "WELCOME1"},
		{"single repeated rune", "aaaaaaaa"},
		{"sequential ascending", "12345678"},
		{"sequential descending", "87654321"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

			_, err := svc.Register(context.Background(), "user@example.com", tc.password)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Register() with %s password error = %v, want ErrInvalidInput", tc.name, err)
			}
		})
	}
}

// TestRegister_NormalizesEmailCase verifies that Register lowercases the
// email before storing it — see normalizeEmail's doc comment for why:
// the UNIQUE constraint on users.email is case-sensitive at the schema
// level, so normalization has to happen here to actually prevent two
// accounts differing only in case.
func TestRegister_NormalizesEmailCase(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.Register(context.Background(), "User@Example.COM", "password123")
	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if got.Email != "user@example.com" {
		t.Errorf("Register() Email = %q, want normalized %q", got.Email, "user@example.com")
	}
	if repo.savedUser.Email != "user@example.com" {
		t.Errorf("Register() savedUser.Email = %q, want normalized %q", repo.savedUser.Email, "user@example.com")
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	repo := &fakeRepository{createUserErr: ErrAlreadyExists}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.Register(context.Background(), "user@example.com", "password123")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Register() duplicate error = %v, want ErrAlreadyExists", err)
	}
}

// --- Authenticate ---

func TestAuthenticate_Valid(t *testing.T) {
	stored := User{ID: "u1", Email: "user@example.com", PasswordHash: mustHash(t, "password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.Authenticate(context.Background(), "user@example.com", "password123")
	if err != nil {
		t.Fatalf("Authenticate() unexpected error: %v", err)
	}
	if got.ID != stored.ID {
		t.Errorf("Authenticate() ID = %q, want %q", got.ID, stored.ID)
	}
}

func TestAuthenticate_WrongPassword(t *testing.T) {
	stored := User{ID: "u1", Email: "user@example.com", PasswordHash: mustHash(t, "password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.Authenticate(context.Background(), "user@example.com", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate() wrong password error = %v, want ErrInvalidCredentials", err)
	}
}

// TestAuthenticate_UnknownEmail also exercises the timing-attack
// mitigation's code path (the dummy bcrypt comparison in the
// ErrNotFound branch) — see Authenticate's doc comment. This test can't
// assert anything about *timing* (that would be flaky by nature), only
// that the functional outcome is still the correct, identical
// ErrInvalidCredentials either way.
func TestAuthenticate_UnknownEmail(t *testing.T) {
	repo := &fakeRepository{findUserByEmailErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.Authenticate(context.Background(), "nobody@example.com", "password123")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate() unknown email error = %v, want ErrInvalidCredentials (never a distinct 'not found')", err)
	}
}

// TestAuthenticate_NormalizesEmailCase verifies that a login attempt with
// a different case than the email was registered with still finds the
// account — Authenticate must look up by the same normalized form
// Register stored.
func TestAuthenticate_NormalizesEmailCase(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	if _, err := svc.Authenticate(context.Background(), "  User@Example.COM  ", "password123"); err != nil && !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() unexpected error: %v", err)
	}
	if repo.findUserByEmailCalledWith != "user@example.com" {
		t.Errorf("Authenticate() looked up FindUserByEmail with %q, want normalized %q", repo.findUserByEmailCalledWith, "user@example.com")
	}
}

// --- Authenticate: login backoff (issue #220) ---

// captureSleep replaces svc's injectable sleep with one that records the
// delay it was asked for instead of actually waiting — the tests below
// assert on that recorded value, not on wall-clock time.
func captureSleep(svc *Service) *time.Duration {
	var captured time.Duration
	svc.sleep = func(_ context.Context, d time.Duration) { captured = d }
	return &captured
}

func TestAuthenticate_NoDelayBelowFailureThreshold(t *testing.T) {
	repo := &fakeRepository{findUserByEmailErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	for i := 0; i < loginBackoffThreshold-1; i++ {
		_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong")
	}

	if *captured != 0 {
		t.Errorf("sleep delay after %d failures (threshold %d) = %v, want 0", loginBackoffThreshold-1, loginBackoffThreshold, *captured)
	}
}

func TestAuthenticate_DelaysAfterRepeatedFailures_UnknownEmail(t *testing.T) {
	repo := &fakeRepository{findUserByEmailErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	// delay() reflects failures *before* the in-flight attempt (see
	// Authenticate's doc comment), so it takes one extra call beyond the
	// threshold for the delay to actually show up on the captured sleep.
	for i := 0; i < loginBackoffThreshold+1; i++ {
		_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong")
	}

	if *captured != loginBackoffBase {
		t.Errorf("sleep delay on attempt %d = %v, want %v", loginBackoffThreshold+1, *captured, loginBackoffBase)
	}
}

func TestAuthenticate_DelaysAfterRepeatedFailures_WrongPassword(t *testing.T) {
	stored := User{ID: "u1", Email: "victim@example.com", PasswordHash: mustHash(t, "correct-password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	for i := 0; i < loginBackoffThreshold+1; i++ {
		_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	}

	if *captured != loginBackoffBase {
		t.Errorf("sleep delay on attempt %d = %v, want %v", loginBackoffThreshold+1, *captured, loginBackoffBase)
	}
}

// TestAuthenticate_UnknownEmailAndWrongPassword_ShareOneCounter pins the
// indistinguishability the issue's first pitfall demands: an attacker
// alternating between "is this email registered" and "is this the
// password" against the same address must not get two separate, half-
// sized budgets — both outcomes count against the same per-email state.
func TestAuthenticate_UnknownEmailAndWrongPassword_ShareOneCounter(t *testing.T) {
	stored := User{ID: "u1", Email: "victim@example.com", PasswordHash: mustHash(t, "correct-password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	// Three failures split across both outcome kinds, then a fourth
	// attempt to observe the effect: delay() reflects failures *before*
	// the in-flight attempt (see Authenticate's doc comment).
	_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	repo.findUserByEmailErr = ErrNotFound // simulate the same email now reported as unknown
	_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	repo.findUserByEmailErr = nil
	_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")

	if *captured != loginBackoffBase {
		t.Errorf("sleep delay on the attempt after %d mixed-outcome failures = %v, want %v (one shared counter)", loginBackoffThreshold, *captured, loginBackoffBase)
	}
}

// TestAuthenticate_DelayAppliesEvenOnSuccess pins the same
// indistinguishability guarantee from the other direction: once an
// account is under backoff, a *correct* password must still pay the
// delay before succeeding — an outcome-dependent delay (skipped on
// success) would itself leak "this account is currently throttled".
func TestAuthenticate_DelayAppliesEvenOnSuccess(t *testing.T) {
	stored := User{ID: "u1", Email: "victim@example.com", PasswordHash: mustHash(t, "correct-password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	for i := 0; i < loginBackoffThreshold; i++ {
		_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	}

	u, err := svc.Authenticate(context.Background(), "victim@example.com", "correct-password123")
	if err != nil {
		t.Fatalf("Authenticate() with the correct password unexpected error: %v", err)
	}
	if u.ID != "u1" {
		t.Errorf("Authenticate() ID = %q, want %q", u.ID, "u1")
	}
	if *captured != loginBackoffBase {
		t.Errorf("sleep delay on the successful attempt = %v, want %v (same as a failure would have paid)", *captured, loginBackoffBase)
	}
}

// TestAuthenticate_SuccessClearsBackoff is the middle-ground half of the
// issue's second pitfall: once a correct password gets in, the account
// is no longer under suspicion — a hard lock never existed here, and a
// resolved account shouldn't keep paying for failures before it.
func TestAuthenticate_SuccessClearsBackoff(t *testing.T) {
	stored := User{ID: "u1", Email: "victim@example.com", PasswordHash: mustHash(t, "correct-password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	captured := captureSleep(svc)

	for i := 0; i < loginBackoffThreshold; i++ {
		_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")
	}
	if _, err := svc.Authenticate(context.Background(), "victim@example.com", "correct-password123"); err != nil {
		t.Fatalf("Authenticate() with the correct password unexpected error: %v", err)
	}

	*captured = -1 // sentinel: prove the next call actually overwrites this
	_, _ = svc.Authenticate(context.Background(), "victim@example.com", "wrong-password")

	if *captured != 0 {
		t.Errorf("sleep delay on the first failure after a successful login = %v, want 0 (backoff should have been cleared)", *captured)
	}
}

// --- ChangePassword ---

func TestChangePassword_Valid(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{findUserByIDUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "u1", "old-password123", "new-password456", "current-token")
	if err != nil {
		t.Fatalf("ChangePassword() unexpected error: %v", err)
	}

	if repo.updateUserPasswordCalledWith.id != "u1" {
		t.Errorf("ChangePassword() called UpdateUserPassword with id %q, want %q", repo.updateUserPasswordCalledWith.id, "u1")
	}
	if err := bcrypt.CompareHashAndPassword(
		[]byte(repo.updateUserPasswordCalledWith.passwordHash), []byte("new-password456"),
	); err != nil {
		t.Error("ChangePassword() persisted hash does not match the new password")
	}

	if repo.deleteSessionsForUserExceptCalledWith.userID != "u1" {
		t.Errorf("ChangePassword() called DeleteSessionsForUserExcept with userID %q, want %q",
			repo.deleteSessionsForUserExceptCalledWith.userID, "u1")
	}
	if got, want := repo.deleteSessionsForUserExceptCalledWith.keepTokenHash, hashToken("current-token"); got != want {
		t.Errorf("ChangePassword() called DeleteSessionsForUserExcept with keepTokenHash %q, want %q (hash of the calling session's own token)", got, want)
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{findUserByIDUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "u1", "wrong-password", "new-password456", "current-token")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("ChangePassword() wrong current password error = %v, want ErrInvalidCredentials", err)
	}
	if repo.updateUserPasswordCalledWith.id != "" {
		t.Error("ChangePassword() must not update the password when the current one is wrong")
	}
	if repo.deleteSessionsForUserExceptCalledWith.userID != "" {
		t.Error("ChangePassword() must not revoke any session when the current password is wrong")
	}
}

// TestChangePassword_UnknownUser exercises the branch documented as
// essentially unreachable in practice (an authenticated session whose
// user vanished out from under it) — it still must not be distinguished
// from a plain wrong password, the same non-enumeration discipline
// Authenticate follows for its own ErrNotFound branch.
func TestChangePassword_UnknownUser(t *testing.T) {
	repo := &fakeRepository{findUserByIDErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "gone", "whatever", "new-password456", "current-token")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("ChangePassword() unknown user error = %v, want ErrInvalidCredentials (never a distinct 'not found')", err)
	}
}

func TestChangePassword_InvalidNewPassword(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{findUserByIDUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "u1", "old-password123", "short", "current-token")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ChangePassword() short new password error = %v, want ErrInvalidInput", err)
	}
	if repo.updateUserPasswordCalledWith.id != "" {
		t.Error("ChangePassword() must validate the new password before touching the repository at all")
	}
}

func TestChangePassword_UpdateRepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{findUserByIDUser: stored, updateUserPasswordErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "u1", "old-password123", "new-password456", "current-token")
	if !errors.Is(err, repoErr) {
		t.Errorf("ChangePassword() UpdateUserPassword error = %v, want %v", err, repoErr)
	}
	if repo.deleteSessionsForUserExceptCalledWith.userID != "" {
		t.Error("ChangePassword() must not revoke sessions when persisting the new password failed")
	}
}

func TestChangePassword_RevokeRepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{findUserByIDUser: stored, deleteSessionsForUserExceptErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.ChangePassword(context.Background(), "u1", "old-password123", "new-password456", "current-token")
	if !errors.Is(err, repoErr) {
		t.Errorf("ChangePassword() DeleteSessionsForUserExcept error = %v, want %v", err, repoErr)
	}
	// The password was already changed by this point (see ChangePassword's
	// doc comment on ordering) — this asserts that half actually happened,
	// not just that an error came back.
	if repo.updateUserPasswordCalledWith.id != "u1" {
		t.Error("ChangePassword() should have already persisted the new password before the revoke step failed")
	}
}

// TestChangePassword_InvalidatesCacheForOtherSessions_ButKeepsCallers is
// ChangePassword's shaped version of the same guarantee: the cache
// entry for the calling session must survive (Repository's own
// DeleteSessionsForUserExcept leaves it alive for the identical reason
// — see ChangePassword's doc comment), while every other cached session
// for the user must be invalidated immediately.
func TestChangePassword_InvalidatesCacheForOtherSessions_ButKeepsCallers(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "old-password123")}
	repo := &fakeRepository{
		findUserByIDUser:         stored,
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "current-token"); err != nil {
		t.Fatalf("ValidateToken(current-token) unexpected error: %v", err)
	}
	if _, err := svc.ValidateToken(context.Background(), "other-token"); err != nil {
		t.Fatalf("ValidateToken(other-token) unexpected error: %v", err)
	}

	if err := svc.ChangePassword(context.Background(), "u1", "old-password123", "new-password456", "current-token"); err != nil {
		t.Fatalf("ChangePassword() unexpected error: %v", err)
	}

	// Simulate what DeleteSessionsForUserExcept would really have done:
	// every session except the caller's own is gone from the repository.
	repo.findSessionByHashErr = ErrNotFound

	if _, err := svc.ValidateToken(context.Background(), "current-token"); err != nil {
		t.Errorf("ValidateToken(current-token) after ChangePassword() = %v, want nil -- the calling session's cache entry must survive", err)
	}
	if _, err := svc.ValidateToken(context.Background(), "other-token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken(other-token) after ChangePassword() = %v, want ErrNotFound -- every other session's cache entry must be invalidated", err)
	}
}

// --- VerifyPassword ---

func TestVerifyPassword_Valid(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "password123")}
	repo := &fakeRepository{findUserByIDUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	if err := svc.VerifyPassword(context.Background(), "u1", "password123"); err != nil {
		t.Errorf("VerifyPassword() unexpected error: %v", err)
	}
}

func TestVerifyPassword_Wrong(t *testing.T) {
	stored := User{ID: "u1", PasswordHash: mustHash(t, "password123")}
	repo := &fakeRepository{findUserByIDUser: stored}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.VerifyPassword(context.Background(), "u1", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("VerifyPassword() wrong password error = %v, want ErrInvalidCredentials", err)
	}
}

func TestVerifyPassword_UnknownUser(t *testing.T) {
	repo := &fakeRepository{findUserByIDErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.VerifyPassword(context.Background(), "gone", "whatever")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("VerifyPassword() unknown user error = %v, want ErrInvalidCredentials (never a distinct 'not found')", err)
	}
}

// --- DeleteAccount ---

func TestDeleteAccount_DeletesSessionsThenUser(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	if err := svc.DeleteAccount(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteAccount() unexpected error: %v", err)
	}
	if repo.deleteSessionsForUserCalledWith != "u1" {
		t.Errorf("DeleteAccount() called DeleteSessionsForUser with %q, want %q", repo.deleteSessionsForUserCalledWith, "u1")
	}
	if repo.deleteUserCalledWith != "u1" {
		t.Errorf("DeleteAccount() called DeleteUser with %q, want %q", repo.deleteUserCalledWith, "u1")
	}
}

func TestDeleteAccount_SessionRepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{deleteSessionsForUserErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.DeleteAccount(context.Background(), "u1")
	if !errors.Is(err, repoErr) {
		t.Errorf("DeleteAccount() DeleteSessionsForUser error = %v, want %v", err, repoErr)
	}
	if repo.deleteUserCalledWith != "" {
		t.Error("DeleteAccount() must not delete the user row when revoking sessions already failed")
	}
}

func TestDeleteAccount_UserRepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{deleteUserErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.DeleteAccount(context.Background(), "u1")
	if !errors.Is(err, repoErr) {
		t.Errorf("DeleteAccount() DeleteUser error = %v, want %v", err, repoErr)
	}
}

// TestDeleteAccount_InvalidatesCacheForUser is DeleteAccount's half of
// the same same-process revocation guarantee TestLogout_InvalidatesCache_*
// pins.
func TestDeleteAccount_InvalidatesCacheForUser(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}

	if err := svc.DeleteAccount(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteAccount() unexpected error: %v", err)
	}

	repo.findSessionByHashErr = ErrNotFound

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() after DeleteAccount() = %v, want ErrNotFound -- tokenCache entry should have been invalidated", err)
	}
}

func TestCreateSession_ReturnsTokenAndPersistsOnlyItsHash(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	token, expiresAt, err := svc.CreateSession(context.Background(), "u1")
	if err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("CreateSession() token must not be empty")
	}
	if repo.savedSession.UserID != "u1" {
		t.Errorf("CreateSession() savedSession.UserID = %q, want %q", repo.savedSession.UserID, "u1")
	}
	if repo.savedSession.TokenHash == token {
		t.Error("CreateSession() must persist a hash of the token, not the raw token itself")
	}
	if repo.savedSession.TokenHash != hashToken(token) {
		t.Error("CreateSession() persisted hash does not match hashToken(token)")
	}
	wantExpiry := time.Now().Add(testSessionTTL)
	if expiresAt.Before(wantExpiry.Add(-time.Minute)) || expiresAt.After(wantExpiry.Add(time.Minute)) {
		t.Errorf("CreateSession() expiresAt = %v, want close to %v", expiresAt, wantExpiry)
	}
}

func TestCreateSession_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{createSessionErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, _, err := svc.CreateSession(context.Background(), "u1")
	if !errors.Is(err, repoErr) {
		t.Errorf("CreateSession() repository error = %v, want %v", err, repoErr)
	}
}

// --- ValidateToken ---

func TestValidateToken_Valid(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	userID, err := svc.ValidateToken(context.Background(), "sometoken")
	if err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}
	if userID != "u1" {
		t.Errorf("ValidateToken() userID = %q, want %q", userID, "u1")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{
			TokenHash: hashToken("sometoken"),
			UserID:    "u1",
			ExpiresAt: time.Now().Add(-time.Minute),
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.ValidateToken(context.Background(), "sometoken")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() expired error = %v, want ErrNotFound", err)
	}
	if repo.deletedTokenHash != hashToken("sometoken") {
		t.Error("ValidateToken() must delete an expired session")
	}
}

func TestValidateToken_Unknown(t *testing.T) {
	repo := &fakeRepository{findSessionByHashErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.ValidateToken(context.Background(), "sometoken")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() unknown token error = %v, want ErrNotFound", err)
	}
}

// TestValidateToken_CachesResult_SecondCallSkipsRepository is the whole
// point of tokenCache (see token_cache.go and docs/DECISIONS.md § "Cache
// de ValidateToken"): a second call for the same token must not touch
// Repository again.
func TestValidateToken_CachesResult_SecondCallSkipsRepository(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); err != nil {
		t.Fatalf("first ValidateToken() unexpected error: %v", err)
	}
	if repo.findSessionByHashCallCount != 1 {
		t.Fatalf("findSessionByHashCallCount after first call = %d, want 1", repo.findSessionByHashCallCount)
	}

	userID, err := svc.ValidateToken(context.Background(), "sometoken")
	if err != nil {
		t.Fatalf("second ValidateToken() unexpected error: %v", err)
	}
	if userID != "u1" {
		t.Errorf("second ValidateToken() userID = %q, want %q", userID, "u1")
	}
	if repo.findSessionByHashCallCount != 1 {
		t.Errorf("findSessionByHashCallCount after second call = %d, want still 1 (should have hit tokenCache instead of Repository)", repo.findSessionByHashCallCount)
	}
}

// --- Logout ---

func TestLogout_DeletesSessionByHash(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	if err := svc.Logout(context.Background(), "sometoken"); err != nil {
		t.Fatalf("Logout() unexpected error: %v", err)
	}
	if repo.deletedTokenHash != hashToken("sometoken") {
		t.Errorf("Logout() deleted hash = %q, want %q", repo.deletedTokenHash, hashToken("sometoken"))
	}
}

func TestLogout_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{deleteSessionErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.Logout(context.Background(), "sometoken")
	if !errors.Is(err, repoErr) {
		t.Errorf("Logout() repository error = %v, want %v", err, repoErr)
	}
}

// TestLogout_InvalidatesCache_SoARevokedTokenStopsValidatingImmediately
// pins the same-process half of tokenCache's revocation guarantee (see
// token_cache.go's doc comment): the *only* staleness this cache is
// meant to ever tolerate is cross-process, never "the process that just
// revoked a token still accepts it."
func TestLogout_InvalidatesCache_SoARevokedTokenStopsValidatingImmediately(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}

	if err := svc.Logout(context.Background(), "sometoken"); err != nil {
		t.Fatalf("Logout() unexpected error: %v", err)
	}

	// Simulate what Logout's own DeleteSession call would really have
	// caused: the repository no longer has this session. If ValidateToken
	// answered from a stale cache entry instead of consulting the
	// repository again, this would still succeed.
	repo.findSessionByHashErr = ErrNotFound

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() after Logout() = %v, want ErrNotFound -- tokenCache entry should have been invalidated", err)
	}
}

// --- LogoutAll ---

func TestLogoutAll_DeletesEverySessionForUser(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	if err := svc.LogoutAll(context.Background(), "u1"); err != nil {
		t.Fatalf("LogoutAll() unexpected error: %v", err)
	}
	if repo.deleteSessionsForUserCalledWith != "u1" {
		t.Errorf("LogoutAll() called DeleteSessionsForUser with %q, want %q", repo.deleteSessionsForUserCalledWith, "u1")
	}
}

func TestLogoutAll_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{deleteSessionsForUserErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.LogoutAll(context.Background(), "u1")
	if !errors.Is(err, repoErr) {
		t.Errorf("LogoutAll() repository error = %v, want %v", err, repoErr)
	}
}

// TestLogoutAll_InvalidatesCacheForUser is LogoutAll's half of the same
// same-process revocation guarantee TestLogout_InvalidatesCache_* pins.
func TestLogoutAll_InvalidatesCacheForUser(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}

	if err := svc.LogoutAll(context.Background(), "u1"); err != nil {
		t.Fatalf("LogoutAll() unexpected error: %v", err)
	}

	repo.findSessionByHashErr = ErrNotFound

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() after LogoutAll() = %v, want ErrNotFound -- tokenCache entry should have been invalidated", err)
	}
}

// --- deriveSessionID / ListSessions / RevokeSession (issue #224) ---

func TestDeriveSessionID_IsDeterministic(t *testing.T) {
	id1 := deriveSessionID("some-token-hash")
	id2 := deriveSessionID("some-token-hash")
	if id1 != id2 {
		t.Errorf("deriveSessionID() = %q and %q for the same input, want equal", id1, id2)
	}
}

func TestDeriveSessionID_DiffersByInput(t *testing.T) {
	id1 := deriveSessionID("hash-a")
	id2 := deriveSessionID("hash-b")
	if id1 == id2 {
		t.Errorf("deriveSessionID() produced the same ID for different inputs: %q", id1)
	}
}

func TestDeriveSessionID_NeverEqualsItsInput(t *testing.T) {
	hash := "some-token-hash"
	if got := deriveSessionID(hash); got == hash {
		t.Error("deriveSessionID() returned the input unchanged, want a derived value")
	}
}

func TestListSessions_ReturnsDerivedIDsNeverTokenHash(t *testing.T) {
	repo := &fakeRepository{
		findSessionsForUserSessions: []Session{
			{TokenHash: "hash-1", UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.ListSessions(context.Background(), "u1", "current-raw-token")
	if err != nil {
		t.Fatalf("ListSessions() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSessions() returned %d entries, want 1", len(got))
	}
	if got[0].ID != deriveSessionID("hash-1") {
		t.Errorf("ListSessions()[0].ID = %q, want %q", got[0].ID, deriveSessionID("hash-1"))
	}
	if got[0].ID == "hash-1" {
		t.Error("ListSessions()[0].ID must never be the raw TokenHash")
	}
}

func TestListSessions_MarksTheCallingSessionAsCurrent(t *testing.T) {
	repo := &fakeRepository{
		findSessionsForUserSessions: []Session{
			{TokenHash: hashToken("current-token"), UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
			{TokenHash: hashToken("other-token"), UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.ListSessions(context.Background(), "u1", "current-token")
	if err != nil {
		t.Fatalf("ListSessions() unexpected error: %v", err)
	}

	var currentCount int
	for _, s := range got {
		if s.IsCurrent {
			currentCount++
			if s.ID != deriveSessionID(hashToken("current-token")) {
				t.Errorf("the session marked current has ID %q, want the one derived from current-token", s.ID)
			}
		}
	}
	if currentCount != 1 {
		t.Errorf("sessions marked IsCurrent = %d, want exactly 1", currentCount)
	}
}

func TestListSessions_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{findSessionsForUserErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.ListSessions(context.Background(), "u1", "sometoken")
	if !errors.Is(err, repoErr) {
		t.Errorf("ListSessions() repository error = %v, want %v", err, repoErr)
	}
}

func TestRevokeSession_DeletesTheMatchingSession(t *testing.T) {
	repo := &fakeRepository{
		findSessionsForUserSessions: []Session{
			{TokenHash: "target-hash", UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
			{TokenHash: "other-hash", UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	targetID := deriveSessionID("target-hash")
	if err := svc.RevokeSession(context.Background(), "u1", targetID); err != nil {
		t.Fatalf("RevokeSession() unexpected error: %v", err)
	}

	if repo.deletedTokenHash != "target-hash" {
		t.Errorf("RevokeSession() deleted hash = %q, want %q", repo.deletedTokenHash, "target-hash")
	}
}

func TestRevokeSession_UnknownID_ReturnsErrNotFound(t *testing.T) {
	repo := &fakeRepository{
		findSessionsForUserSessions: []Session{
			{TokenHash: "some-hash", UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.RevokeSession(context.Background(), "u1", "not-a-real-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("RevokeSession() with an unknown id error = %v, want ErrNotFound", err)
	}
	if repo.deletedTokenHash != "" {
		t.Error("RevokeSession() must not delete anything when the id doesn't match")
	}
}

// TestRevokeSession_AnotherUsersSessionID_ReturnsErrNotFound pins the
// never-confirm-existence discipline: sessionID here really does address
// a session that exists, just not one belonging to userID — the fake
// mirrors real Repository scoping (see go-repository-parity.md) by
// simply never returning another user's session for this userID.
func TestRevokeSession_AnotherUsersSessionID_ReturnsErrNotFound(t *testing.T) {
	otherUsersID := deriveSessionID("belongs-to-someone-else")
	repo := &fakeRepository{findSessionsForUserSessions: nil}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.RevokeSession(context.Background(), "u1", otherUsersID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("RevokeSession() for another user's session id error = %v, want ErrNotFound", err)
	}
}

func TestRevokeSession_InvalidatesTokenCache(t *testing.T) {
	repo := &fakeRepository{
		findSessionByHashSession: Session{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
		findSessionsForUserSessions: []Session{
			{TokenHash: hashToken("sometoken"), UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)
	freezeTokenCache(svc)

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}

	targetID := deriveSessionID(hashToken("sometoken"))
	if err := svc.RevokeSession(context.Background(), "u1", targetID); err != nil {
		t.Fatalf("RevokeSession() unexpected error: %v", err)
	}

	repo.findSessionByHashErr = ErrNotFound

	if _, err := svc.ValidateToken(context.Background(), "sometoken"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() after RevokeSession() = %v, want ErrNotFound -- tokenCache entry should have been invalidated", err)
	}
}

func TestRevokeSession_RepositoryError_OnFind(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{findSessionsForUserErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.RevokeSession(context.Background(), "u1", "some-id")
	if !errors.Is(err, repoErr) {
		t.Errorf("RevokeSession() repository error = %v, want %v", err, repoErr)
	}
}

func TestRevokeSession_RepositoryError_OnDelete(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{
		findSessionsForUserSessions: []Session{
			{TokenHash: "target-hash", UserID: "u1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
		deleteSessionErr: repoErr,
	}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.RevokeSession(context.Background(), "u1", deriveSessionID("target-hash"))
	if !errors.Is(err, repoErr) {
		t.Errorf("RevokeSession() repository error = %v, want %v", err, repoErr)
	}
}

// --- GetUser ---

func TestGetUser_Delegates(t *testing.T) {
	repo := &fakeRepository{findUserByIDUser: User{ID: "u1", Email: "user@example.com"}}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	got, err := svc.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetUser() unexpected error: %v", err)
	}
	if got.ID != "u1" {
		t.Errorf("GetUser() ID = %q, want %q", got.ID, "u1")
	}
}

func TestGetUser_NotFound(t *testing.T) {
	repo := &fakeRepository{findUserByIDErr: ErrNotFound}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	_, err := svc.GetUser(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser() error = %v, want ErrNotFound", err)
	}
}

// --- PruneExpiredSessions ---

func TestPruneExpiredSessions_Delegates(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	before := time.Now()
	if err := svc.PruneExpiredSessions(context.Background()); err != nil {
		t.Fatalf("PruneExpiredSessions() unexpected error: %v", err)
	}
	after := time.Now()

	if repo.deleteExpiredSessionsCalledWith.Before(before) || repo.deleteExpiredSessionsCalledWith.After(after) {
		t.Errorf("PruneExpiredSessions() called Repository with now = %v, want between %v and %v",
			repo.deleteExpiredSessionsCalledWith, before, after)
	}
}

func TestPruneExpiredSessions_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	repo := &fakeRepository{deleteExpiredSessionsErr: repoErr}
	svc := NewService(repo, testSessionTTL, unlimitedSessions)

	err := svc.PruneExpiredSessions(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("PruneExpiredSessions() repository error = %v, want %v", err, repoErr)
	}
}

// TestRegister_RejectsAddressesTheOldCheckAccepted covers the gap left by
// the previous validation, which asked only whether the string contained
// an "@". Each of these passed that check and cannot receive mail.
func TestRegister_RejectsAddressesTheOldCheckAccepted(t *testing.T) {
	for _, email := range []string{"@", "a@", "@b", "@example.com", "user@", "user@@example.com"} {
		t.Run(email, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

			_, err := svc.Register(context.Background(), email, "password12345")
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Register(%q) error = %v, want ErrInvalidInput", email, err)
			}
		})
	}
}

// TestRegister_AcceptsOrdinaryAddresses is the other side of the same
// change: tightening validation is only correct if it still admits the
// addresses people actually have.
func TestRegister_AcceptsOrdinaryAddresses(t *testing.T) {
	for _, email := range []string{
		"user@example.com",
		"first.last@example.co.uk",
		"user+tag@example.com",
		"user_name@sub.example.org",
	} {
		t.Run(email, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

			if _, err := svc.Register(context.Background(), email, "password12345"); err != nil {
				t.Errorf("Register(%q) unexpected error: %v", email, err)
			}
		})
	}
}

// TestRegister_EmailErrorDoesNotEchoTheInput guards a property the
// validation library and this package agree on: an error that reflects
// attacker-controlled input back is a reflection primitive once it lands
// in a log or a response body.
func TestRegister_EmailErrorDoesNotEchoTheInput(t *testing.T) {
	svc := NewService(&fakeRepository{}, testSessionTTL, unlimitedSessions)

	const hostile = "<script>alert(1)</script>"

	_, err := svc.Register(context.Background(), hostile, "password12345")
	if err == nil {
		t.Fatal("Register() error = nil, want ErrInvalidInput")
	}
	if strings.Contains(err.Error(), hostile) {
		t.Errorf("error message echoes the submitted value: %q", err.Error())
	}
}

// TestValidatePassword_MeasuresBytesNotRunes pins the one length check in
// this codebase that is deliberately byte-based. bcrypt rejects input
// over 72 *bytes*, so a rune-based limit (validate.MaxLen, used for
// email) would accept a password that hashing then refuses.
func TestValidatePassword_MeasuresBytesNotRunes(t *testing.T) {
	// 25 three-byte runes: 25 characters, 75 bytes.
	password := strings.Repeat("日", 25)

	if utf8.RuneCountInString(password) > maxPasswordLen {
		t.Fatalf("test premise broken: %d runes is already over the %d limit", utf8.RuneCountInString(password), maxPasswordLen)
	}
	if len(password) <= maxPasswordLen {
		t.Fatalf("test premise broken: %d bytes is not over the %d limit", len(password), maxPasswordLen)
	}

	if err := validatePassword(password); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("validatePassword() error = %v, want ErrInvalidInput — the limit must be counted in bytes", err)
	}
}
