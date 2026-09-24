package task

import (
	"bytes"
	"encoding/gob"
)

// gobCodec is CachedRepository's codec.Codec, in place of cistern's default
// codec.JSON. Task.Version and Task.UserID are both `json:"-"` — deliberately
// kept off the public API response (see task.go's comments on each) — but
// encoding/json.Marshal honours that same tag for *any* caller, including
// cistern's own cache. Every FindAll response cistern serves once caching is
// in the stack, not only a cache hit (cistern.Cache.GetOrLoad round-trips its
// own value through the codec even on the call that first populates the
// cache), would come back with Version and UserID silently zeroed.
//
// Version zeroed is the sharper failure: the handler's pageETag hashes
// exactly (id, Version) of the rows FindAll returns (docs/DECISIONS.md §
// "ETag de GET /v1/tasks"), so two responses for the same task IDs would
// share one ETag even after a write really changed one of those rows'
// Version — a client polling with If-None-Match would get an incorrect 304
// for content that changed. See TestCachedRepository_FindAll_PreservesVersion.
//
// encoding/gob ignores json struct tags and encodes every exported field, so
// both survive. This is safe under cistern's own RS-06 warning against gob
// ("decoding into a destination named by the data itself... rules out gob
// with registered interface types" — codec.go's package doc): Unmarshal here
// only ever decodes into *[]Task, a concrete, non-interface, statically-known
// type, and nothing in this file calls gob.Register. There is no interface
// value anywhere in the wire format for a malformed L2 entry to name an
// unexpected type through.
type gobCodec struct{}

func (gobCodec) ID() string { return "task-api-gob-v1" }

func (gobCodec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (gobCodec) Unmarshal(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
