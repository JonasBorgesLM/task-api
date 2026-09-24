package task

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/JonasBorgesLM/cistern"
)

// CachedRepository decorates a Repository with a cache-aside layer over
// FindAll (internal/user/token_cache.go's tokenCache is the in-process
// precedent for "cache in front of a hot read"; docs/DECISIONS.md § "Cache-
// aside para GET /v1/tasks" is why this one needs more than that — L1+L2+Bus,
// not L1 alone). Every other method is a pure passthrough to next: FindByID,
// CountAll and CountByStatusAndPriority are never cached, and Create/Update/
// Delete write through and then invalidate (see this file's invalidation
// methods).
//
// Same decorator shape as breakerRepository (postgres_breaker.go): implements
// Repository whole, wraps another Repository, lets cmd/api decide whether it
// is in the stack at all.
type CachedRepository struct {
	next  Repository
	cache *cistern.Cache[cacheKey, []Task]

	// OnInvalidationError receives an invalidation that failed after a write
	// already succeeded (Create/Update/Delete, below). The write is never
	// undone and never reported as failed for this — cistern's own
	// InvalidateTag/Bus are best-effort by design (a Redis blip must not
	// start rejecting task writes, docs/DECISIONS.md § "Cache-aside para GET
	// /v1/tasks"'s correction paragraph) — but the owner's cached lists may
	// be stale until their own TTL, so it is worth logging. Nil ignores it;
	// cmd/api is the only place that sets it, keeping Service/Handler out of
	// it entirely. Same shape and same reasoning as cistern's own reference
	// decorator, github.com/JonasBorgesLM/cistern/examples/taskapi.
	OnInvalidationError func(error)
}

// Close releases the cache's own resources (an L2/Bus's subscription
// goroutine, in particular — cistern's own WithBus doc comment: "The cache
// subscribes in New; call Close to stop."). It never closes next, nor the
// *redis.Client cmd/api built to pass into WithL2/WithBus — those are
// cmd/api's own to close, since it is the one that opened them.
func (r *CachedRepository) Close() error {
	r.cache.Close()
	return nil
}

// NewCachedRepository wraps next with a cistern cache-aside layer over
// FindAll. opts configures the cache's levels and TTLs (cmd/api supplies
// WithL1/WithL2/WithBus/WithTTL/WithL1TTL from config.Config); the namespace,
// key, tags and codec are this decorator's own and are not configurable, the
// same division cistern's own reference decorator
// (github.com/JonasBorgesLM/cistern/examples/taskapi) uses. The codec is
// gobCodec (cache_codec.go), not cistern's default codec.JSON: Task.Version
// and Task.UserID are both `json:"-"`, which codec.JSON would silently drop
// on every response once caching is in the stack — see gobCodec's own doc
// comment. Both cistern.WithCodec and cistern.WithTags are appended after
// opts, so neither can be shadowed by an option a caller passes in.
func NewCachedRepository(next Repository, opts ...cistern.Option) (*CachedRepository, error) {
	cache, err := cistern.New[cacheKey, []Task]("tasks", cacheKeyString,
		append(opts, cistern.WithTags(cacheKeyTag), cistern.WithCodec(gobCodec{}))...)
	if err != nil {
		return nil, err
	}
	return &CachedRepository{next: next, cache: cache}, nil
}

var _ Repository = (*CachedRepository)(nil)

func (r *CachedRepository) Create(ctx context.Context, task Task) error {
	if err := r.next.Create(ctx, task); err != nil {
		return err
	}
	r.invalidateOwner(ctx, task.UserID)
	return nil
}

func (r *CachedRepository) FindByID(ctx context.Context, id, userID string) (Task, error) {
	return r.next.FindByID(ctx, id, userID)
}

// cacheKey is FindAll's parameters, normalized into a comparable value (a
// generic cistern.Cache's key type must satisfy Go's `comparable`, which
// []Status/[]Priority slices do not — statuses and priorities are folded
// into sorted, comma-joined strings here, once, rather than carried as
// slices and re-sorted on every cache operation). docs/DECISIONS.md:1638 §
// "Índices para status/priority em tasks" is why the key has to include
// every FindAll parameter and not just userID: there is no index behind
// those filters, so a cache that ignored them would answer one filter's
// request with another filter's page.
type cacheKey struct {
	userID     string
	statuses   string
	priorities string
	limit      int
	offset     int
}

// newCacheKey builds a cacheKey from FindAll's actual parameters. Sorting
// statuses/priorities here — not just deduplicating, which Service.ListTasks
// already did — means two requests for the same filter set that arrived in a
// different query-parameter order still land on the same cache entry;
// skipping this would not be wrong, only a worse hit rate.
func newCacheKey(userID string, limit, offset int, statuses []Status, priorities []Priority) cacheKey {
	return cacheKey{
		userID:     userID,
		statuses:   sortedJoin(statuses),
		priorities: sortedJoin(priorities),
		limit:      limit,
		offset:     offset,
	}
}

// sortedJoin renders values as a sorted, comma-joined string, without
// mutating the caller's slice — FindAll's statuses/priorities are also
// handed to next unchanged, so this never sorts in place. An empty or nil
// values yields "", matching FindAll's own "no filter" meaning for both.
func sortedJoin[T ~string](values []T) string {
	if len(values) == 0 {
		return ""
	}
	strs := make([]string, len(values))
	for i, v := range values {
		strs[i] = string(v)
	}
	sort.Strings(strs)
	return strings.Join(strs, ",")
}

// cacheKeyString is the key function cistern.New requires: it must put the
// owner in the physical key (RS-02, ADR-0008), which it does via userID —
// see cacheKey's own doc comment for why the rest of FindAll's parameters
// belong in the key too.
func cacheKeyString(k cacheKey) string {
	return fmt.Sprintf("user:%s:lists:status=%s:priority=%s:limit=%d:offset=%d",
		k.userID, k.statuses, k.priorities, k.limit, k.offset)
}

// cacheKeyTag is deliberately narrower than cacheKeyString: every cacheKey
// for one userID — no matter its filters or pagination — shares this one
// tag, so a single InvalidateTag call (invalidateOwner, below) retires every
// cached FindAll variant for that user at once, without enumerating them.
func cacheKeyTag(k cacheKey) []string {
	return []string{userTag(k.userID)}
}

// userTag is cacheKeyTag's building block, exposed separately so
// invalidateOwner can name a user's tag without constructing a cacheKey it
// would otherwise throw away.
func userTag(userID string) string {
	return "user:" + userID + ":lists"
}

// invalidateOwner retires every cached FindAll page belonging to userID —
// called after a write that changed userID's tasks (Create/Update/Delete,
// below). A publish failure is reported through OnInvalidationError, never
// through this write's own return value: see OnInvalidationError's doc
// comment for why.
func (r *CachedRepository) invalidateOwner(ctx context.Context, userID string) {
	if err := r.cache.InvalidateTag(ctx, userTag(userID)); err != nil && r.OnInvalidationError != nil {
		r.OnInvalidationError(err)
	}
}

func (r *CachedRepository) FindAll(ctx context.Context, userID string, limit, offset int, statuses []Status, priorities []Priority) ([]Task, error) {
	return r.cache.GetOrLoad(ctx, newCacheKey(userID, limit, offset, statuses, priorities), func(ctx context.Context) ([]Task, error) {
		return r.next.FindAll(ctx, userID, limit, offset, statuses, priorities)
	})
}

func (r *CachedRepository) Update(ctx context.Context, task Task) error {
	if err := r.next.Update(ctx, task); err != nil {
		return err
	}
	// task.UserID, not a re-checked ownership: the caller already resolved
	// it via a prior FindByID (Repository's own doc comment), and Update
	// itself checks it against the stored row. This decorator only needs to
	// know whose cache to invalidate.
	r.invalidateOwner(ctx, task.UserID)
	return nil
}

func (r *CachedRepository) Delete(ctx context.Context, id, userID string) error {
	if err := r.next.Delete(ctx, id, userID); err != nil {
		return err
	}
	r.invalidateOwner(ctx, userID)
	return nil
}

// CountAll is never cached — GET /v1/tasks's X-Total-Count must always
// reflect a real count, computed fresh on every request (docs/DECISIONS.md §
// "Total real na listagem"; CachedRepository's own doc comment above).
func (r *CachedRepository) CountAll(ctx context.Context, userID string, statuses []Status, priorities []Priority) (int, error) {
	return r.next.CountAll(ctx, userID, statuses, priorities)
}

// CountByStatusAndPriority is never cached, for the same reason CountAll
// isn't: nothing in the decision this decorator implements asked for it, and
// GET /v1/tasks/stats has no staleness budget of its own to spend.
func (r *CachedRepository) CountByStatusAndPriority(ctx context.Context, userID string, statuses []Status, priorities []Priority) (byStatus map[Status]int, byPriority map[Priority]int, err error) {
	return r.next.CountByStatusAndPriority(ctx, userID, statuses, priorities)
}
