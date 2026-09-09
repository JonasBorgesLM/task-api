package link

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cairn"
	"github.com/JonasBorgesLM/cairn/memstore"
	"github.com/JonasBorgesLM/cairn/policy"
)

const (
	testOwnerA = "user-a"
	testOwnerB = "user-b"
)

func newTestShortener(t *testing.T) (*cairn.Shortener, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	shortener, err := cairn.New(store, cairn.WithPolicy(policy.Default(nil)))
	if err != nil {
		t.Fatalf("cairn.New() unexpected error: %v", err)
	}
	return shortener, store
}

func newTestService(t *testing.T) (*Service, *cairn.Shortener) {
	t.Helper()
	shortener, store := newTestShortener(t)
	svc, err := NewService(shortener, store)
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}
	return svc, shortener
}

// fakeStoreWithoutOwnerLister implements only cairn.Store's three
// required methods — no ListByOwner — to prove NewService fails
// construction against a Store that cannot back List, rather than
// discovering the gap on the first call to it.
type fakeStoreWithoutOwnerLister struct{}

func (fakeStoreWithoutOwnerLister) Save(context.Context, *cairn.Link) error { return nil }
func (fakeStoreWithoutOwnerLister) Load(context.Context, cairn.Code) (*cairn.Link, error) {
	return nil, cairn.ErrCodeNotFound
}
func (fakeStoreWithoutOwnerLister) Revoke(context.Context, cairn.Code, time.Time, bool) error {
	return nil
}

func TestNewService_RequiresOwnerLister(t *testing.T) {
	shortener, err := cairn.New(fakeStoreWithoutOwnerLister{}, cairn.WithPolicy(policy.Default(nil)))
	if err != nil {
		t.Fatalf("cairn.New() unexpected error: %v", err)
	}

	_, err = NewService(shortener, fakeStoreWithoutOwnerLister{})
	if err == nil {
		t.Fatal("NewService() with a store that has no ListByOwner: want an error, got nil")
	}
}

func TestService_Create_OwnsTheCreatedLink(t *testing.T) {
	svc, _ := newTestService(t)

	link, err := svc.Create(context.Background(), testOwnerA, CreateRequest{RawURL: "https://example.com/a"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if link.OwnerID != testOwnerA {
		t.Errorf("Create() OwnerID = %q, want %q", link.OwnerID, testOwnerA)
	}
}

func TestService_Create_VanityCode(t *testing.T) {
	svc, _ := newTestService(t)

	link, err := svc.Create(context.Background(), testOwnerA, CreateRequest{
		RawURL:     "https://example.com/a",
		VanityCode: "mylink",
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if link.Code != "mylink" {
		t.Errorf("Create() Code = %q, want %q", link.Code, "mylink")
	}
	if !link.Vanity {
		t.Error("Create() Vanity = false, want true")
	}
}

func TestService_Create_RejectedDestinationPropagates(t *testing.T) {
	svc, _ := newTestService(t)

	// A loopback destination — rejected by policy.Default's
	// BlockPrivateNetworks, the same check every deployment gets for
	// free (cairn SR-07).
	_, err := svc.Create(context.Background(), testOwnerA, CreateRequest{RawURL: "https://127.0.0.1/"})
	if !errors.Is(err, cairn.ErrDestinationRejected) {
		t.Fatalf("Create() error = %v, want errors.Is(_, cairn.ErrDestinationRejected)", err)
	}
}

func TestService_List_ReturnsOnlyThatOwnersLinks(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, testOwnerA, CreateRequest{RawURL: "https://example.com/a1"}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := svc.Create(ctx, testOwnerA, CreateRequest{RawURL: "https://example.com/a2"}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := svc.Create(ctx, testOwnerB, CreateRequest{RawURL: "https://example.com/b1"}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	links, _, err := svc.List(ctx, testOwnerA, "", 10)
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("List(testOwnerA) returned %d links, want 2", len(links))
	}
	for _, l := range links {
		if l.OwnerID != testOwnerA {
			t.Errorf("List(testOwnerA) returned a link owned by %q", l.OwnerID)
		}
	}
}

// TestService_Revoke_RejectsWrongOwner is the dedicated negative test
// docs/INTEGRATION.md §4.1 and docs/integrations/task-api.md (in the
// cairn module) both call for — user A cannot revoke user B's link.
// cairn itself performs no such check (ADR-0010); this proves Service
// does.
//
// Negative control: run once against a build of Service.Revoke with the
// `if link.OwnerID != ownerID` check removed. It failed — user B's
// revocation attempt succeeded, and user A's own subsequent Resolve came
// back cairn.ErrLinkRevoked. Restored immediately after; see this
// package's PR description for the transcript.
func TestService_Revoke_RejectsWrongOwner(t *testing.T) {
	svc, shortener := newTestService(t)
	ctx := context.Background()

	link, err := svc.Create(ctx, testOwnerA, CreateRequest{RawURL: "https://example.com/a"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := svc.Revoke(ctx, testOwnerB, link.Code); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Revoke(user-b) error = %v, want ErrNotOwner", err)
	}

	// The rejected attempt must not have revoked it regardless.
	if _, err := shortener.Resolve(ctx, link.Code); err != nil {
		t.Fatalf("Resolve after rejected revoke attempt error = %v, want nil (still active)", err)
	}

	if err := svc.Revoke(ctx, testOwnerA, link.Code); err != nil {
		t.Fatalf("Revoke(user-a) error = %v, want nil", err)
	}
	if _, err := shortener.Resolve(ctx, link.Code); !errors.Is(err, cairn.ErrLinkRevoked) {
		t.Fatalf("Resolve after correct-owner revoke error = %v, want errors.Is(_, ErrLinkRevoked)", err)
	}
}

func TestService_Revoke_UnknownCodeIsNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.Revoke(context.Background(), testOwnerA, "doesnotexist")
	if !errors.Is(err, cairn.ErrCodeNotFound) {
		t.Errorf("Revoke() error = %v, want errors.Is(_, cairn.ErrCodeNotFound)", err)
	}
}
