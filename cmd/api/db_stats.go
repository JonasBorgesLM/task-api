package main

import (
	"database/sql"
	"expvar"
	"sync"
	"sync/atomic"
)

// currentDB is what the db_stats entry published by
// publishDBStatsExpvarOnce reads from — package-level atomic pointer for
// the same reason currentCrierInstance and the attachment breaker
// atomics are (see cmd/api/crier.go, cmd/api/attachment_breaker.go):
// expvar.Publish panics on a duplicate name, and newServer runs once per
// *testing.T across this package's own suite, not just once per process.
var (
	dbStatsExpvarOnce sync.Once
	currentDB         atomic.Pointer[sql.DB]
)

// dbStatsVars is the /debug/vars shape for sql.DBStats — the fields
// 16.C1 asked for, not the whole struct: MaxOpenConnections and
// OpenConnections describe capacity, InUse/Idle describe the moment,
// and WaitCount/WaitDuration are the ones this issue actually exists
// for — a query that had to queue for a pooled connection, and how long
// it waited. See docs/DECISIONS.md § "Medir esgotamento de pool antes
// de decidir sobre breaker no banco".
type dbStatsVars struct {
	MaxOpenConnections int    `json:"max_open_connections"`
	OpenConnections    int    `json:"open_connections"`
	InUse              int    `json:"in_use"`
	Idle               int    `json:"idle"`
	WaitCount          int64  `json:"wait_count"`
	WaitDuration       string `json:"wait_duration"`
}

// dbStatsSnapshot reads db.Stats() into the /debug/vars shape above. A
// nil db (the in-memory-store configuration, see openDatabase) reports
// every field at its zero value rather than an error — there is no pool
// to describe, and "zero" is the honest answer, not a special case a
// reader needs to know about.
func dbStatsSnapshot(db *sql.DB) any {
	if db == nil {
		return dbStatsVars{}
	}
	s := db.Stats()
	return dbStatsVars{
		MaxOpenConnections: s.MaxOpenConnections,
		OpenConnections:    s.OpenConnections,
		InUse:              s.InUse,
		Idle:               s.Idle,
		WaitCount:          s.WaitCount,
		WaitDuration:       s.WaitDuration.String(),
	}
}

// publishDBStatsExpvarOnce registers the db_stats entry the first time
// it is called, and does nothing after. Safe to call whether or not a
// database is actually configured: with nothing stored in currentDB,
// the published value just reports every field at zero.
func publishDBStatsExpvarOnce() {
	dbStatsExpvarOnce.Do(func() {
		expvar.Publish("db_stats", expvar.Func(func() any {
			return dbStatsSnapshot(currentDB.Load())
		}))
	})
}
