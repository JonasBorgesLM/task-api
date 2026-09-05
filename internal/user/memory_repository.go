package user

import (
	"context"
	"sort"
	"sync"
	"time"
)

// memoryRepository is an in-memory implementation of Repository.
type memoryRepository struct {
	mu           sync.RWMutex
	usersByID    map[string]User
	usersByEmail map[string]string // email -> user ID
	sessions     map[string]Session
}

// NewMemoryRepository returns a new in-memory Repository.
func NewMemoryRepository() Repository {
	return &memoryRepository{
		usersByID:    make(map[string]User),
		usersByEmail: make(map[string]string),
		sessions:     make(map[string]Session),
	}
}

// CreateUser persists a new user. Returns ErrAlreadyExists if the email is
// already taken.
func (r *memoryRepository) CreateUser(ctx context.Context, u User) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.usersByEmail[u.Email]; exists {
		return ErrAlreadyExists
	}

	r.usersByID[u.ID] = u
	r.usersByEmail[u.Email] = u.ID
	return nil
}

// FindUserByEmail returns the user with the given email. Returns
// ErrNotFound if absent.
func (r *memoryRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.usersByEmail[email]
	if !ok {
		return User{}, ErrNotFound
	}
	return r.usersByID[id], nil
}

// FindUserByID returns the user with the given ID. Returns ErrNotFound if
// absent.
func (r *memoryRepository) FindUserByID(ctx context.Context, id string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	u, ok := r.usersByID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// UpdateUserPassword replaces id's stored password hash and bumps
// updated_at to now. Returns ErrNotFound if id doesn't exist.
func (r *memoryRepository) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.usersByID[id]
	if !ok {
		return ErrNotFound
	}
	u.PasswordHash = passwordHash
	u.UpdatedAt = time.Now()
	r.usersByID[id] = u
	return nil
}

// CreateSession persists a new session and evicts s.UserID's oldest
// sessions past maxSessions, all under one write lock — see Repository's
// doc comment on CreateSession for why that matters for two logins
// racing each other.
func (r *memoryRepository) CreateSession(ctx context.Context, s Session, maxSessions int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sessions[s.TokenHash] = s

	var mine []Session
	for _, sess := range r.sessions {
		if sess.UserID == s.UserID {
			mine = append(mine, sess)
		}
	}
	if len(mine) <= maxSessions {
		return nil
	}

	sort.Slice(mine, func(i, j int) bool { return mine[i].CreatedAt.Before(mine[j].CreatedAt) })
	for _, victim := range mine[:len(mine)-maxSessions] {
		delete(r.sessions, victim.TokenHash)
	}
	return nil
}

// DeleteSessionsForUser removes every session belonging to userID.
func (r *memoryRepository) DeleteSessionsForUser(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for hash, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, hash)
		}
	}
	return nil
}

// DeleteSessionsForUserExcept removes every session belonging to userID
// other than the one whose hash is keepTokenHash.
func (r *memoryRepository) DeleteSessionsForUserExcept(ctx context.Context, userID, keepTokenHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for hash, s := range r.sessions {
		if s.UserID == userID && hash != keepTokenHash {
			delete(r.sessions, hash)
		}
	}
	return nil
}

// FindSessionByTokenHash returns the session with the given token hash.
// Returns ErrNotFound if absent.
func (r *memoryRepository) FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.sessions[tokenHash]
	if !ok {
		return Session{}, ErrNotFound
	}
	return s, nil
}

// DeleteSession removes the session with the given token hash. Deleting an
// absent session is not an error — Service.Logout and expired-session
// cleanup both call this without caring whether it was already gone.
func (r *memoryRepository) DeleteSession(ctx context.Context, tokenHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.sessions, tokenHash)
	return nil
}

// DeleteExpiredSessions removes every session whose ExpiresAt is before
// now.
func (r *memoryRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for hash, s := range r.sessions {
		if s.ExpiresAt.Before(now) {
			delete(r.sessions, hash)
		}
	}
	return nil
}
