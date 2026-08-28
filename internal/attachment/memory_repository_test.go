package attachment

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	ownerID     = "11111111-1111-4111-8111-111111111111"
	strangerID  = "22222222-2222-4222-8222-222222222222"
	ownedTaskID = "33333333-3333-4333-8333-333333333333"
	otherTaskID = "44444444-4444-4444-8444-444444444444"
)

// fixedOwnership is a TaskOwnershipFunc for the fixture above: ownedTaskID
// belongs to ownerID, otherTaskID belongs to strangerID, anything else
// exists for nobody.
func fixedOwnership(_ context.Context, taskID, userID string) (bool, error) {
	switch taskID {
	case ownedTaskID:
		return userID == ownerID, nil
	case otherTaskID:
		return userID == strangerID, nil
	default:
		return false, nil
	}
}

func newTestRepo(t *testing.T) Repository {
	t.Helper()
	return NewMemoryRepository(fixedOwnership)
}

func testAttachment(id, storageKey, taskID string) Attachment {
	return Attachment{
		ID:               id,
		TaskID:           taskID,
		OriginalFilename: "report.pdf",
		StorageKey:       storageKey,
		ContentType:      "application/pdf",
		SizeBytes:        1024,
		CreatedAt:        time.Now(),
	}
}

func TestMemory_CreateAndFindByStorageKey(t *testing.T) {
	repo := newTestRepo(t)
	att := testAttachment("a1", "key-1", ownedTaskID)

	if err := repo.Create(context.Background(), att, ownerID); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	found, err := repo.FindByStorageKey(context.Background(), "key-1", ownerID)
	if err != nil {
		t.Fatalf("FindByStorageKey() unexpected error: %v", err)
	}
	if found.ID != att.ID || found.OriginalFilename != att.OriginalFilename {
		t.Errorf("FindByStorageKey() = %+v, want %+v", found, att)
	}
}

// TestMemory_Create_OnSomeoneElsesTask is the rule the whole ownership
// chain exists for: an attachment must not be attachable to a task the
// caller does not own, and the refusal must not confirm that the task is
// real.
func TestMemory_Create_OnSomeoneElsesTask(t *testing.T) {
	repo := newTestRepo(t)

	err := repo.Create(context.Background(), testAttachment("a1", "key-1", otherTaskID), ownerID)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Create() error = %v, want ErrTaskNotFound", err)
	}

	// And nothing was stored: the real owner must not find it either.
	if _, err := repo.FindByStorageKey(context.Background(), "key-1", strangerID); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByStorageKey() after a refused Create = %v, want ErrNotFound", err)
	}
}

func TestMemory_Create_OnMissingTask(t *testing.T) {
	repo := newTestRepo(t)

	err := repo.Create(context.Background(), testAttachment("a1", "key-1", "no-such-task"), ownerID)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("Create() error = %v, want ErrTaskNotFound", err)
	}
}

func TestMemory_Create_DuplicateID(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Create(context.Background(), testAttachment("a1", "key-1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	err := repo.Create(context.Background(), testAttachment("a1", "key-2", ownedTaskID), ownerID)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() error = %v, want ErrAlreadyExists", err)
	}
}

// TestMemory_Create_DuplicateStorageKey matters more than the duplicate
// ID above: two rows pointing at one blob means deleting either one
// strands or destroys the other's bytes.
func TestMemory_Create_DuplicateStorageKey(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Create(context.Background(), testAttachment("a1", "key-1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	err := repo.Create(context.Background(), testAttachment("a2", "key-1", ownedTaskID), ownerID)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() error = %v, want ErrAlreadyExists", err)
	}
}

// TestMemory_FindByStorageKey_KeyIsNotACapability pins that holding a
// valid storage key is not by itself permission to read the file: the key
// resolves, and the lookup still refuses because the task behind it is
// not the caller's.
func TestMemory_FindByStorageKey_KeyIsNotACapability(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Create(context.Background(), testAttachment("a1", "key-1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	_, err := repo.FindByStorageKey(context.Background(), "key-1", strangerID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByStorageKey() with a valid key from a non-owner = %v, want ErrNotFound", err)
	}
}

func TestMemory_FindByStorageKey_Unknown(t *testing.T) {
	repo := newTestRepo(t)

	if _, err := repo.FindByStorageKey(context.Background(), "nope", ownerID); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByStorageKey() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_FindByTask_OrdersByCreatedAtThenID(t *testing.T) {
	repo := newTestRepo(t)
	base := time.Now()

	// Inserted out of order, and with two sharing a timestamp so the
	// tie-break by ID is actually exercised.
	for _, att := range []Attachment{
		{ID: "c", TaskID: ownedTaskID, StorageKey: "k3", CreatedAt: base.Add(time.Second)},
		{ID: "b", TaskID: ownedTaskID, StorageKey: "k2", CreatedAt: base},
		{ID: "a", TaskID: ownedTaskID, StorageKey: "k1", CreatedAt: base},
	} {
		if err := repo.Create(context.Background(), att, ownerID); err != nil {
			t.Fatalf("Create(%s) unexpected error: %v", att.ID, err)
		}
	}

	got, err := repo.FindByTask(context.Background(), ownedTaskID, ownerID)
	if err != nil {
		t.Fatalf("FindByTask() unexpected error: %v", err)
	}

	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("FindByTask() returned %d attachments, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("FindByTask()[%d].ID = %q, want %q", i, got[i].ID, want[i])
		}
	}
}

// TestMemory_FindByTask_EmptyIsNotTheSameAsForbidden covers the
// distinction FindByTask exists to preserve: an owner with no
// attachments gets an empty slice, a stranger gets ErrTaskNotFound.
func TestMemory_FindByTask_EmptyIsNotTheSameAsForbidden(t *testing.T) {
	repo := newTestRepo(t)

	got, err := repo.FindByTask(context.Background(), ownedTaskID, ownerID)
	if err != nil {
		t.Fatalf("FindByTask() for the owner unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindByTask() = %v, want empty", got)
	}
	if got == nil {
		t.Error("FindByTask() returned nil, want an empty non-nil slice so it serialises as [] and not null")
	}

	if _, err := repo.FindByTask(context.Background(), ownedTaskID, strangerID); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("FindByTask() for a stranger = %v, want ErrTaskNotFound", err)
	}
}

// TestMemory_FindByTask_ExcludesOtherTasks guards the filter itself: with
// two tasks in play, one task's listing must not leak the other's rows.
func TestMemory_FindByTask_ExcludesOtherTasks(t *testing.T) {
	repo := newTestRepo(t)

	if err := repo.Create(context.Background(), testAttachment("mine", "k1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := repo.Create(context.Background(), testAttachment("theirs", "k2", otherTaskID), strangerID); err != nil {
		t.Fatalf("Create() for the other task unexpected error: %v", err)
	}

	got, err := repo.FindByTask(context.Background(), ownedTaskID, ownerID)
	if err != nil {
		t.Fatalf("FindByTask() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Errorf("FindByTask() = %v, want exactly the one attachment on this task", got)
	}
}

func TestNewMemoryRepository_PanicsWithoutOwnershipCheck(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewMemoryRepository(nil) did not panic — a repository that accepts every task would make the ownership tests vacuous")
		}
	}()
	NewMemoryRepository(nil)
}

// --- TotalBytesForUser ---

func TestMemory_TotalBytesForUser_SumsOwnedAttachments(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Create(context.Background(), testAttachment("a1", "key-1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("Create(a1) unexpected error: %v", err)
	}
	att2 := testAttachment("a2", "key-2", ownedTaskID)
	att2.SizeBytes = 2048
	if err := repo.Create(context.Background(), att2, ownerID); err != nil {
		t.Fatalf("Create(a2) unexpected error: %v", err)
	}

	total, err := repo.TotalBytesForUser(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if want := int64(1024 + 2048); total != want {
		t.Errorf("TotalBytesForUser() = %d, want %d", total, want)
	}
}

func TestMemory_TotalBytesForUser_NoAttachments_IsZero(t *testing.T) {
	repo := newTestRepo(t)

	total, err := repo.TotalBytesForUser(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("TotalBytesForUser() = %d, want 0", total)
	}
}

// TestMemory_TotalBytesForUser_ExcludesOtherUsers guards the scoping
// itself: another user's stored bytes must never count toward this
// user's total.
func TestMemory_TotalBytesForUser_ExcludesOtherUsers(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Create(context.Background(), testAttachment("mine", "k1", ownedTaskID), ownerID); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	theirs := testAttachment("theirs", "k2", otherTaskID)
	theirs.SizeBytes = 999999
	if err := repo.Create(context.Background(), theirs, strangerID); err != nil {
		t.Fatalf("Create() for the other user unexpected error: %v", err)
	}

	total, err := repo.TotalBytesForUser(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if total != 1024 {
		t.Errorf("TotalBytesForUser() = %d, want 1024 (must exclude the stranger's 999999-byte file)", total)
	}
}
