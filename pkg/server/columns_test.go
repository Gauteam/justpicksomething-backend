package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

func TestCleanFields(t *testing.T) {
	columns := []db.Column{
		{ID: "beds", Name: "Bedrooms", Type: "text"},
		{ID: "tub", Name: "Hot tub", Type: "boolean"},
		{ID: "notes", Name: "Notes", Type: "textarea"},
	}
	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	tests := []struct {
		name    string
		in      string
		want    map[string]any
		wantErr bool
	}{
		{"valid values", `{"beds":" 3 ","tub":true,"notes":"Near lake\nDock"}`,
			map[string]any{"beds": "3", "tub": true, "notes": "Near lake\nDock"}, false},
		{"null clears", `{"beds":null}`, map[string]any{"beds": nil}, false},
		{"empty string clears", `{"beds":"  "}`, map[string]any{"beds": nil}, false},
		{"unknown column", `{"nope":"x"}`, nil, true},
		{"boolean as string", `{"tub":"yes"}`, nil, true},
		{"text as number", `{"beds":3}`, nil, true},
		{"newline in single-line text", `{"beds":"3\n4"}`, nil, true},
		{"text too long", `{"beds":"` + strings.Repeat("a", maxTextValueLen+1) + `"}`, nil, true},
		{"textarea allows longer", `{"notes":"` + strings.Repeat("a", maxTextValueLen+1) + `"}`,
			map[string]any{"notes": strings.Repeat("a", maxTextValueLen+1)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanFields(columns, raw(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if gv, ok := got[k]; !ok || gv != v {
					t.Errorf("%s = %#v, want %#v", k, gv, v)
				}
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request, s db.Session) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", nil), db.Session{Member: db.Member{IsAdmin: false}})
	if rec.Code != http.StatusForbidden || called {
		t.Errorf("non-admin: status = %d, called = %v; want 403, false", rec.Code, called)
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", nil), db.Session{Member: db.Member{IsAdmin: true}})
	if rec.Code != http.StatusNoContent || !called {
		t.Errorf("admin: status = %d, called = %v; want 204, true", rec.Code, called)
	}
}

func TestDecodeCommentBody(t *testing.T) {
	tests := []struct {
		body   string
		ok     bool
		wanted string
	}{
		{`{"body":"  Looks great\nBook it  "}`, true, "Looks great\nBook it"},
		{`{"body":"   "}`, false, ""},
		{`{"body":"` + strings.Repeat("a", maxCommentBodyLen+1) + `"}`, false, ""},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		got, ok := decodeCommentBody(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body)))
		if ok != tt.ok || got != tt.wanted {
			t.Errorf("decodeCommentBody(%.30q) = %q, %v; want %q, %v", tt.body, got, ok, tt.wanted, tt.ok)
		}
	}
}
