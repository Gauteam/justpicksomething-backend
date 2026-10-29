package server

import (
	"net/http"
	"os"
	"strings"
)

// Config controls the request security checks.
type Config struct {
	// AllowedOrigins are the exact origins (scheme://host[:port]) browsers may
	// call the API from.
	AllowedOrigins []string
	// RequireHTTPS rejects requests that did not arrive over HTTPS and sends
	// HSTS. Enabled on Vercel, disabled for local development.
	RequireHTTPS bool
}

// ConfigFromEnv reads ALLOWED_ORIGINS (comma-separated) and enables HTTPS
// enforcement when running on Vercel (VERCEL is set).
func ConfigFromEnv() Config {
	var origins []string
	for _, o := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return Config{
		AllowedOrigins: origins,
		RequireHTTPS:   os.Getenv("VERCEL") != "",
	}
}

// secure enforces HTTPS and restricts which origins may call the API.
//
// The Origin check only constrains browsers; it is not authentication.
func secure(cfg Config, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.RequireHTTPS {
			if r.Header.Get("X-Forwarded-Proto") != "https" {
				writeError(w, http.StatusForbidden, "https required")
				return
			}
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")

		switch {
		case origin != "" && !allowed[origin]:
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		case origin == "" && r.Method != http.MethodGet && r.Method != http.MethodHead:
			// Browsers always send Origin on non-GET requests; its absence on a
			// write means a non-browser client or a stripped header.
			writeError(w, http.StatusForbidden, "origin required")
			return
		}

		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			// Cache preflights to save function invocations.
			w.Header().Set("Access-Control-Max-Age", "600")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
