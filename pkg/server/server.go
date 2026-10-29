package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

// New returns the API's root handler: routes wrapped in the security checks.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return secure(cfg, mux)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// Generous timeout: a suspended Neon compute takes a moment to wake.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		log.Printf("health: db ping failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "db": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
