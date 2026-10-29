package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

func TestCleanText(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"  Lake house  ", "Lake house", false},
		{"", "", true},
		{"   ", "", true},
		{strings.Repeat("a", 10), strings.Repeat("a", 10), false},
		{strings.Repeat("a", 11), "", true},
		{strings.Repeat("é", 10), strings.Repeat("é", 10), false},
		{"line\nbreak", "", true},
		{"tab\there", "", true},
	}

	for _, tt := range tests {
		got, err := cleanText("field", tt.in, 10)
		if (err != nil) != tt.wantErr {
			t.Errorf("cleanText(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("cleanText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWithSessionRejectsMissingToken(t *testing.T) {
	h := withSession(func(w http.ResponseWriter, r *http.Request, s db.Session) {
		t.Error("handler called without a token")
	})

	for _, header := range []string{"", "Bearer ", "Basic abc", "bearer abc"} {
		req := httptest.NewRequest(http.MethodGet, "/rooms/abc", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status = %d, want %d", header, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestSetVoteValidation(t *testing.T) {
	const itemID = "6f1c2c1e-4b9a-4c55-9a39-2f1b5f0c8d11"
	tests := []struct {
		name   string
		id     string
		body   string
		status int
	}{
		{"zero", itemID, `{"value":0}`, http.StatusBadRequest},
		{"two", itemID, `{"value":2}`, http.StatusBadRequest},
		{"missing", itemID, `{}`, http.StatusBadRequest},
		{"not json", itemID, `nope`, http.StatusBadRequest},
		{"bad item id", "not-a-uuid", `{"value":1}`, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/rooms/abc/items/x/vote", strings.NewReader(tt.body))
			req.SetPathValue("id", tt.id)
			rec := httptest.NewRecorder()
			handleSetVote(rec, req, db.Session{})

			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
		})
	}
}
