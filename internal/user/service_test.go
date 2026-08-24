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

	createSessionErr error
	savedSession     Session

	findSessionByHashSession Session
	findSessionByHashErr     error

	deleteSessionErr error
	deletedTokenHash string

	deleteExpiredSessionsErr        error
	deleteExpiredSessionsCalledWith time.Time
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

func (f *fakeRepository) CreateSession(_ context.Context, s Session) error {
	f.savedSession = s
	return f.createSessionErr
}

func (f *fakeRepository) FindSessionByTokenHash(_ context.Context, _ string) (Session, error) {
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

// --- Register ---

func TestRegister_Valid(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(&fakeRepository{}, testSessionTTL)

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
			svc := NewService(&fakeRepository{}, testSessionTTL)

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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

	_, err := svc.Register(context.Background(), "user@example.com", "password123")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Register() duplicate error = %v, want ErrAlreadyExists", err)
	}
}

// --- Authenticate ---

func TestAuthenticate_Valid(t *testing.T) {
	stored := User{ID: "u1", Email: "user@example.com", PasswordHash: mustHash(t, "password123")}
	repo := &fakeRepository{findUserByEmailUser: stored}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

	if _, err := svc.Authenticate(context.Background(), "  User@Example.COM  ", "password123"); err != nil && !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() unexpected error: %v", err)
	}
	if repo.findUserByEmailCalledWith != "user@example.com" {
		t.Errorf("Authenticate() looked up FindUserByEmail with %q, want normalized %q", repo.findUserByEmailCalledWith, "user@example.com")
	}
}

// --- CreateSession ---

func TestCreateSession_ReturnsTokenAndPersistsOnlyItsHash(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

	_, err := svc.ValidateToken(context.Background(), "sometoken")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ValidateToken() unknown token error = %v, want ErrNotFound", err)
	}
}

// --- Logout ---

func TestLogout_DeletesSessionByHash(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

	err := svc.Logout(context.Background(), "sometoken")
	if !errors.Is(err, repoErr) {
		t.Errorf("Logout() repository error = %v, want %v", err, repoErr)
	}
}

// --- GetUser ---

func TestGetUser_Delegates(t *testing.T) {
	repo := &fakeRepository{findUserByIDUser: User{ID: "u1", Email: "user@example.com"}}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

	_, err := svc.GetUser(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser() error = %v, want ErrNotFound", err)
	}
}

// --- PruneExpiredSessions ---

func TestPruneExpiredSessions_Delegates(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, testSessionTTL)

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
	svc := NewService(repo, testSessionTTL)

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
			svc := NewService(&fakeRepository{}, testSessionTTL)

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
			svc := NewService(&fakeRepository{}, testSessionTTL)

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
	svc := NewService(&fakeRepository{}, testSessionTTL)

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
