package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const frontend = "https://pick.rajwalgautam.com"

func TestSecure(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := secure(Config{AllowedOrigins: []string{frontend}, RequireHTTPS: true}, ok)

	tests := []struct {
		name       string
		method     string
		origin     string
		proto      string
		wantStatus int
		wantACAO   string
	}{
		{"GET from frontend", http.MethodGet, frontend, "https", http.StatusOK, frontend},
		{"GET without origin", http.MethodGet, "", "https", http.StatusOK, ""},
		{"GET from other origin", http.MethodGet, "https://evil.example", "https", http.StatusForbidden, ""},
		{"POST from frontend", http.MethodPost, frontend, "https", http.StatusOK, frontend},
		{"POST from other origin", http.MethodPost, "https://evil.example", "https", http.StatusForbidden, ""},
		{"POST without origin", http.MethodPost, "", "https", http.StatusForbidden, ""},
		{"preflight from frontend", http.MethodOptions, frontend, "https", http.StatusNoContent, frontend},
		{"preflight from other origin", http.MethodOptions, "https://evil.example", "https", http.StatusForbidden, ""},
		{"plain http", http.MethodGet, frontend, "http", http.StatusForbidden, ""},
		{"http origin of frontend host", http.MethodGet, "http://pick.rajwalgautam.com", "https", http.StatusForbidden, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/health", nil)
			req.Header.Set("X-Forwarded-Proto", tt.proto)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantACAO)
			}
		})
	}
}

func TestSecureHSTS(t *testing.T) {
	h := secure(Config{RequireHTTPS: true}, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("missing Strict-Transport-Security header")
	}
}

func TestSecureLocalSkipsHTTPS(t *testing.T) {
	h := secure(Config{AllowedOrigins: []string{"http://localhost:3000"}}, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// Every method the router uses must be allowed by CORS, or browsers block it
// at the preflight even though non-browser clients work.
func TestSecureAllowsRouterMethods(t *testing.T) {
	h := secure(Config{AllowedOrigins: []string{frontend}}, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodOptions, "/rooms/abc", nil)
	req.Header.Set("Origin", frontend)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	allowed := rec.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		if !strings.Contains(allowed, m) {
			t.Errorf("Access-Control-Allow-Methods %q is missing %s", allowed, m)
		}
	}
}
