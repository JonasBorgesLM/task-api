package main

import (
	"context"
	"expvar"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// currentAttachmentRWBreaker and currentAttachmentListBreaker are what
// the /debug/vars entries published by publishAttachmentBreakerExpvarOnce
// read from. Package-level atomic pointers rather than a value closed
// over by buildBlobStore's own caller, for the same reason
// currentCrierInstance is one (see cmd/api/crier.go): expvar.Publish
// panics on a duplicate name, and newServer — hence buildBlobStore —
// runs once per *testing.T across this package's own test suite, not
// just once per process the way it does in main(). Publishing the
// expvar.Func closures exactly once and re-pointing what they read on
// every subsequent buildBlobStore call is what keeps both true at once.
var (
	attachmentBreakerExpvarOnce  sync.Once
	currentAttachmentRWBreaker   atomic.Pointer[bastion.Breaker]
	currentAttachmentListBreaker atomic.Pointer[bastion.Breaker]
)

// attachmentBreakerVars is the shape one breaker's Counts() takes on
// /debug/vars. A struct rather than bastion.Counts itself: Counts.State
// is bastion.State, a defined int type that encodes as a bare number
// through expvar's default JSON marshaling — exactly the trap
// hooks.go's own doc comment warns a type-safelisted pipeline falls
// into (issue #81) — so State is rendered through its own String()
// here, the same fix applied to the OnStateChange logging below.
type attachmentBreakerVars struct {
	State               string `json:"state"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	FailureThreshold    int    `json:"failure_threshold"`
	OpenedAt            string `json:"opened_at,omitempty"`
	Manual              bool   `json:"manual,omitempty"`
}

// attachmentBreakerSnapshot reads b's Counts() into the /debug/vars
// shape above. A nil b — attachments disabled, or the filesystem
// backend, neither of which builds a breaker — reports "disabled"
// rather than a zero-valued breaker that never existed.
func attachmentBreakerSnapshot(b *bastion.Breaker) any {
	if b == nil {
		return attachmentBreakerVars{State: "disabled"}
	}
	c := b.Counts()
	v := attachmentBreakerVars{
		State:               c.State.String(),
		ConsecutiveFailures: c.ConsecutiveFailures,
		FailureThreshold:    c.FailureThreshold,
		Manual:              c.Manual,
	}
	if !c.OpenedAt.IsZero() {
		v.OpenedAt = c.OpenedAt.Format(time.RFC3339)
	}
	return v
}

// publishAttachmentBreakerExpvarOnce registers the two /debug/vars
// entries the attachment S3 breakers report — attachment_s3_breaker for
// Put/Delete/Open, attachment_s3_list_breaker for the background orphan
// sweep (see s3_breaker.go) — the first time it is called, and does
// nothing after. Safe to call whether or not S3 storage ends up
// configured: with nothing ever stored in the two atomics, the published
// values just report a disabled breaker, which is the correct answer.
//
// Deliberately not wired into GET /health/ready: see
// docs/DECISIONS.md § "Counts() no /debug/vars" — an open circuit here
// is a handled failure on an opt-in dependency, not a reason to stop
// routing traffic to this instance.
func publishAttachmentBreakerExpvarOnce() {
	attachmentBreakerExpvarOnce.Do(func() {
		expvar.Publish("attachment_s3_breaker", expvar.Func(func() any {
			return attachmentBreakerSnapshot(currentAttachmentRWBreaker.Load())
		}))
		expvar.Publish("attachment_s3_list_breaker", expvar.Func(func() any {
			return attachmentBreakerSnapshot(currentAttachmentListBreaker.Load())
		}))
	})
}

// attachmentBreakerHooks builds the bastion.Hooks passed to
// attachment.NewBreakerBlobStore: OnStateChange logs every transition,
// and OnCall logs the error behind every call the breaker counted as a
// failure. The two together are what let an operator tell "S3 fell
// over" from "the credential is wrong" apart in the log around a
// transition to Open, per docs/DECISIONS.md § "Validar permissão de
// escrita no S3 na subida" — bastion.StateChangeEvent itself carries no
// error, only the bare transition, so the causing error is read off the
// OnCall event for the same call instead.
func attachmentBreakerHooks(logger *slog.Logger) bastion.Hooks {
	return bastion.Hooks{
		OnStateChange: func(ctx context.Context, ev bastion.StateChangeEvent) {
			level := slog.LevelInfo
			if ev.To == bastion.StateOpen {
				level = slog.LevelWarn
			}
			logger.Log(ctx, level, "attachment store circuit breaker state change",
				"breaker", ev.Name,
				// .String(), not the bastion.State value itself — see
				// attachmentBreakerVars' doc comment on why a defined
				// type reaches a type-safelisted pipeline (crier's own
				// crierAttrValue, mirrored from this same *slog.Logger)
				// as a placeholder otherwise.
				"from", ev.From.String(),
				"to", ev.To.String(),
				"manual", ev.Manual,
			)
		},
		OnCall: func(ctx context.Context, ev bastion.CallEvent) {
			if !ev.Counted || ev.Err == nil {
				return
			}
			logger.Warn("attachment store call failed",
				"breaker", ev.Name,
				"state", ev.State.String(),
				"error", ev.Err,
			)
		},
	}
}
