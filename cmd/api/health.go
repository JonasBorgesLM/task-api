package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// healthResponse is the body returned by GET /health.
type healthResponse struct {
	Status string `json:"status"`
}

// registerHealthRoute registers GET /health on the given mux.
func registerHealthRoute(mux *http.ServeMux, logger *slog.Logger) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(healthResponse{Status: "ok"}); err != nil {
			logger.Error("failed to encode health response", "error", err)
		}
	})
}
