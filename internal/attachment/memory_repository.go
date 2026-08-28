package attachment

import (
	"context"
	"sort"
	"sync"
)

// memoryRepository is an in-memory Repository, used by the unit suite so
// it needs no external service. It is not a placeholder for the
// PostgreSQL implementation: it has to keep answering exactly what
// postgresRepository answers, including the ownership rules, or the tests
// that rely on it stop meaning anything.
type memoryRepository struct {
	mu sync.RWMutex

	// Keyed by attachment ID. FindByStorageKey scans rather than
	// maintaining a second index: this store exists to back tests, where
	// the map holds a handful of entries, and a second index is a second
	// thing that can fall out of sync with the first.
	store map[string]Attachment

	ownsTask TaskOwnershipFunc
}

// NewMemoryRepository returns a Repository backed by an in-memory map.
//
// ownsTask supplies the ownership check this implementation cannot make
// on its own (see TaskOwnershipFunc). Passing nil is a programming error
// rather than a way to disable the check — a Repository that silently
// accepted every task would let the unit suite pass while the rule it is
// supposed to be testing is absent — so it panics immediately instead.
func NewMemoryRepository(ownsTask TaskOwnershipFunc) Repository {
	if ownsTask == nil {
		panic("attachment: NewMemoryRepository requires a TaskOwnershipFunc")
	}
	return &memoryRepository{
		store:    make(map[string]Attachment),
		ownsTask: ownsTask,
	}
}

func (r *memoryRepository) Create(ctx context.Context, attachment Attachment, userID string) error {
	// The ownership check runs before the lock: it calls out to another
	// Repository, and holding this one's write lock across a call that
	// can touch a database is how an unrelated slow query becomes this
	// store's contention.
	owns, err := r.ownsTask(ctx, attachment.TaskID, userID)
	if err != nil {
		return err
	}
	if !owns {
		return ErrTaskNotFound
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.store[attachment.ID]; exists {
		return ErrAlreadyExists
	}
	// The storage key is unique in the schema too, and for a stronger
	// reason than the ID: two rows pointing at one blob means deleting
	// either one strands or destroys the other's bytes.
	for _, existing := range r.store {
		if existing.StorageKey == attachment.StorageKey {
			return ErrAlreadyExists
		}
	}

	r.store[attachment.ID] = attachment
	return nil
}

func (r *memoryRepository) FindByStorageKey(ctx context.Context, storageKey, userID string) (Attachment, error) {
	if err := ctx.Err(); err != nil {
		return Attachment{}, err
	}

	r.mu.RLock()
	found, ok := Attachment{}, false
	for _, existing := range r.store {
		if existing.StorageKey == storageKey {
			found, ok = existing, true
			break
		}
	}
	r.mu.RUnlock()

	if !ok {
		return Attachment{}, ErrNotFound
	}

	// Resolving the key is not authorization. A key that leads to a task
	// the caller does not own is reported as if it led nowhere, so
	// holding a key tells its holder nothing about whether it is real.
	owns, err := r.ownsTask(ctx, found.TaskID, userID)
	if err != nil {
		return Attachment{}, err
	}
	if !owns {
		return Attachment{}, ErrNotFound
	}

	return found, nil
}

func (r *memoryRepository) FindByTask(ctx context.Context, taskID, userID string) ([]Attachment, error) {
	owns, err := r.ownsTask(ctx, taskID, userID)
	if err != nil {
		return nil, err
	}
	if !owns {
		return nil, ErrTaskNotFound
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	attachments := make([]Attachment, 0)
	for _, existing := range r.store {
		if existing.TaskID == taskID {
			attachments = append(attachments, existing)
		}
	}

	// Map iteration order is randomized, so the ordering the Repository
	// contract promises has to be imposed here rather than inherited.
	sort.Slice(attachments, func(i, j int) bool {
		if !attachments[i].CreatedAt.Equal(attachments[j].CreatedAt) {
			return attachments[i].CreatedAt.Before(attachments[j].CreatedAt)
		}
		return attachments[i].ID < attachments[j].ID
	})

	return attachments, nil
}

// TotalBytesForUser sums SizeBytes across every attachment userID owns.
//
// The candidate rows are snapshotted under the read lock and the
// ownership check runs after releasing it — the same reason Create's
// ownership check runs before taking the lock at all: ownsTask can call
// out to another Repository, and holding this one's lock across that
// call is how an unrelated slow query becomes this store's contention.
func (r *memoryRepository) TotalBytesForUser(ctx context.Context, userID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	r.mu.RLock()
	candidates := make([]Attachment, 0, len(r.store))
	for _, att := range r.store {
		candidates = append(candidates, att)
	}
	r.mu.RUnlock()

	var total int64
	for _, att := range candidates {
		owns, err := r.ownsTask(ctx, att.TaskID, userID)
		if err != nil {
			return 0, err
		}
		if owns {
			total += att.SizeBytes
		}
	}

	return total, nil
}

func (r *memoryRepository) UnreferencedKeys(ctx context.Context, keys []string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	referenced := make(map[string]struct{}, len(r.store))
	for _, existing := range r.store {
		referenced[existing.StorageKey] = struct{}{}
	}
	r.mu.RUnlock()

	unreferenced := make([]string, 0)
	for _, key := range keys {
		if _, ok := referenced[key]; !ok {
			unreferenced = append(unreferenced, key)
		}
	}

	return unreferenced, nil
}
