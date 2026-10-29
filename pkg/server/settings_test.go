package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

func TestSettingsPatch(t *testing.T) {
	parse := func(s string) settingsBody {
		var b settingsBody
		if err := json.Unmarshal([]byte(s), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	tests := []struct {
		name    string
		body    string
		check   func(db.SettingsPatch) bool
		wantErr bool
	}{
		{"max items", `{"maxItems":10}`, func(p db.SettingsPatch) bool { return p.SetMaxItems && *p.MaxItems == 10 }, false},
		{"remove max items", `{"maxItems":null}`, func(p db.SettingsPatch) bool { return p.SetMaxItems && p.MaxItems == nil }, false},
		{"max items untouched", `{"locked":true}`, func(p db.SettingsPatch) bool { return !p.SetMaxItems && *p.Locked }, false},
		{"zero max items", `{"maxItems":0}`, nil, true},
		{"bad mode", `{"voteMode":"ranked"}`, nil, true},
		{"budget mode", `{"voteMode":"budget","votesPerMember":5}`, func(p db.SettingsPatch) bool {
			return *p.VoteMode == "budget" && *p.VotesPerMember == 5
		}, false},
		{"zero votes", `{"votesPerMember":0}`, nil, true},
		{"too many votes", `{"votesPerMember":101}`, nil, true},
		{"blank name", `{"name":"  "}`, nil, true},
		{"name trimmed", `{"name":" Trip "}`, func(p db.SettingsPatch) bool { return *p.Name == "Trip" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := settingsPatch(db.Room{}, parse(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil && !tt.check(p) {
				t.Errorf("unexpected patch %+v", p)
			}
		})
	}
}

func TestChangesMoreThanLock(t *testing.T) {
	f, tr := false, true
	if changesMoreThanLock(db.SettingsPatch{Locked: &f}) {
		t.Error("unlock-only patch reported as a settings change")
	}
	if !changesMoreThanLock(db.SettingsPatch{Locked: &f, AllowDuplicateVotes: &tr}) {
		t.Error("settings change not detected")
	}
}

func TestRequireUnlocked(t *testing.T) {
	h := requireUnlocked(func(w http.ResponseWriter, r *http.Request, s db.Session) {
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", nil), db.Session{})
	if rec.Code != http.StatusNoContent {
		t.Errorf("unlocked: status = %d, want 204", rec.Code)
	}

	now := time.Now()
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", nil), db.Session{Room: db.Room{LockedAt: &now}})
	if rec.Code != http.StatusConflict {
		t.Errorf("locked: status = %d, want 409", rec.Code)
	}
}
