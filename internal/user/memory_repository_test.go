package user

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestUser(id, email string) User {
	now := time.Now()
	return User{
		ID:           id,
		Email:        email,
		PasswordHash: "not-a-real-hash",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestMemory_CreateAndFindUserByEmail(t *testing.T) {
	repo := NewMemoryRepository()
	u := newTestUser("1", "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	got, err := repo.FindUserByEmail(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("FindUserByEmail() unexpected error: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("FindUserByEmail() ID = %q, want %q", got.ID, u.ID)
	}
}

func TestMemory_FindUserByEmail_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	_, err := repo.FindUserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindUserByEmail() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_CreateUser_DuplicateEmail(t *testing.T) {
	repo := NewMemoryRepository()
	u := newTestUser("1", "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	duplicate := newTestUser("2", "user@example.com")
	err := repo.CreateUser(context.Background(), duplicate)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("CreateUser() duplicate email error = %v, want ErrAlreadyExists", err)
	}
}

func TestMemory_FindUserByID(t *testing.T) {
	repo := NewMemoryRepository()
	u := newTestUser("1", "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	got, err := repo.FindUserByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindUserByID() unexpected error: %v", err)
	}
	if got.Email != u.Email {
		t.Errorf("FindUserByID() Email = %q, want %q", got.Email, u.Email)
	}
}

func TestMemory_FindUserByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	_, err := repo.FindUserByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindUserByID() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_CreateAndFindSessionByTokenHash(t *testing.T) {
	repo := NewMemoryRepository()
	s := Session{TokenHash: "hash-1", UserID: "1", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}

	if err := repo.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	got, err := repo.FindSessionByTokenHash(context.Background(), "hash-1")
	if err != nil {
		t.Fatalf("FindSessionByTokenHash() unexpected error: %v", err)
	}
	if got.UserID != "1" {
		t.Errorf("FindSessionByTokenHash() UserID = %q, want %q", got.UserID, "1")
	}
}

func TestMemory_FindSessionByTokenHash_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	_, err := repo.FindSessionByTokenHash(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_DeleteSession(t *testing.T) {
	repo := NewMemoryRepository()
	s := Session{TokenHash: "hash-1", UserID: "1", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}

	if err := repo.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}
	if err := repo.DeleteSession(context.Background(), "hash-1"); err != nil {
		t.Fatalf("DeleteSession() unexpected error: %v", err)
	}

	_, err := repo.FindSessionByTokenHash(context.Background(), "hash-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash() after DeleteSession() error = %v, want ErrNotFound", err)
	}
}

// TestMemory_DeleteSession_Unknown_IsNotAnError verifies Logout's
// idempotency contract at the Repository layer: deleting a session that
// was never created (or already deleted) is not an error.
func TestMemory_DeleteSession_Unknown_IsNotAnError(t *testing.T) {
	repo := NewMemoryRepository()

	if err := repo.DeleteSession(context.Background(), "nonexistent"); err != nil {
		t.Errorf("DeleteSession() on an unknown token error = %v, want nil", err)
	}
}

// TestMemory_DeleteExpiredSessions verifies that only sessions whose
// ExpiresAt is before the given time are removed — a still-valid session
// must survive the sweep.
func TestMemory_DeleteExpiredSessions(t *testing.T) {
	repo := NewMemoryRepository()
	now := time.Now()

	expired := Session{TokenHash: "expired", UserID: "1", ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour)}
	valid := Session{TokenHash: "valid", UserID: "1", ExpiresAt: now.Add(time.Hour), CreatedAt: now}

	for _, s := range []Session{expired, valid} {
		if err := repo.CreateSession(context.Background(), s); err != nil {
			t.Fatalf("CreateSession() unexpected error: %v", err)
		}
	}

	if err := repo.DeleteExpiredSessions(context.Background(), now); err != nil {
		t.Fatalf("DeleteExpiredSessions() unexpected error: %v", err)
	}

	if _, err := repo.FindSessionByTokenHash(context.Background(), "expired"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash(expired) after DeleteExpiredSessions() error = %v, want ErrNotFound", err)
	}
	if _, err := repo.FindSessionByTokenHash(context.Background(), "valid"); err != nil {
		t.Errorf("FindSessionByTokenHash(valid) after DeleteExpiredSessions() unexpected error: %v", err)
	}
}
