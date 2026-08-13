package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/JonasBorgesLM/task-api/task"
)

// readinessPingTimeout bounds how long GET /health/ready waits on the
// repository's Ping before reporting not-ready. It is intentionally short:
// a readiness probe is expected to return quickly, and a database that
// can't answer a bare ping well within this window is not one traffic
// should be routed to anyway.
const readinessPingTimeout = 5 * time.Second

// healthResponse is the body returned by GET /health and GET /health/ready.
type healthResponse struct {
	Status string `json:"status"`
}

// registerHealthRoute registers GET /health on the given mux. Unlike GET
// /health/ready (see registerReadinessRoute), this always responds 200
// while the process is alive — a liveness check, not a check of any
// dependency.
func registerHealthRoute(mux *http.ServeMux, logger *slog.Logger) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(healthResponse{Status: "ok"}); err != nil {
			logger.Error("failed to encode health response", "error", err)
		}
	})
}

// registerReadinessRoute registers GET /health/ready on the given mux. It
// reports whether repo's backing store is actually reachable, so an
// orchestrator (a Kubernetes readiness probe, a load balancer health
// check) can stop routing traffic to a replica whose database connection
// is broken, instead of only detecting a hard process crash the way GET
// /health does.
//
// repo is checked through the optional Pinger interface (see
// task.Pinger): memoryRepository doesn't implement it and has nothing to
// verify, so a repo that isn't a Pinger is always reported ready.
func registerReadinessRoute(mux *http.ServeMux, repo task.Repository, logger *slog.Logger) {
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		body := healthResponse{Status: "ok"}

		if pinger, ok := repo.(task.Pinger); ok {
			ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
			defer cancel()

			if err := pinger.Ping(ctx); err != nil {
				status = http.StatusServiceUnavailable
				body = healthResponse{Status: "unavailable"}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			logger.Error("failed to encode readiness response", "error", err)
		}
	})
}
