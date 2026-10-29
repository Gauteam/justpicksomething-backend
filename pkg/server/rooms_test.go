package server

import (
	"encoding/json"
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

func TestCleanNewItems(t *testing.T) {
	raws := func(ts ...string) []rawItem {
		var out []rawItem
		for _, t := range ts {
			out = append(out, rawItem{Title: t})
		}
		return out
	}
	many := func(n int) []rawItem {
		var out []rawItem
		for i := range n {
			out = append(out, rawItem{Title: strings.Repeat("x", i+1)})
		}
		return out
	}

	tests := []struct {
		name    string
		in      []rawItem
		want    []string
		wantErr bool
	}{
		{"single", raws("Lake house"), []string{"Lake house"}, false},
		{"trims and drops blanks", raws("  Cabin ", "", "   ", "Tahoe"), []string{"Cabin", "Tahoe"}, false},
		{"dedupes ignoring case, keeps first", raws("Tahoe", "Cabin", "tahoe"), []string{"Tahoe", "Cabin"}, false},
		{"all blank", raws("", " "), nil, true},
		{"too long", raws(strings.Repeat("a", maxItemTitleLen+1)), nil, true},
		{"too many", many(maxItemsPerAdd + 1), nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanNewItems(tt.in, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			var gotTitles []string
			for _, it := range got {
				gotTitles = append(gotTitles, it.Title)
			}
			if strings.Join(gotTitles, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", gotTitles, tt.want)
			}
		})
	}
}

func TestCleanNewItemsFields(t *testing.T) {
	columns := []db.Column{{ID: "beds", Name: "Bedrooms", Type: "text"}, {ID: "tub", Name: "Hot tub", Type: "boolean"}}

	got, err := cleanNewItems([]rawItem{
		{Title: "Cabin", Fields: map[string]json.RawMessage{"beds": json.RawMessage(`"3"`), "tub": json.RawMessage(`true`)}},
		{Title: "Condo", Fields: map[string]json.RawMessage{"beds": json.RawMessage(`""`)}},
	}, columns)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Fields["beds"] != "3" || got[0].Fields["tub"] != true {
		t.Errorf("Cabin fields = %v", got[0].Fields)
	}
	if v, ok := got[1].Fields["beds"]; !ok || v != nil {
		t.Errorf("Condo empty beds = %#v, want nil (dropped on insert)", v)
	}

	_, err = cleanNewItems([]rawItem{{Title: "Cabin", Fields: map[string]json.RawMessage{"tub": json.RawMessage(`"maybe"`)}}}, columns)
	if err == nil {
		t.Error("invalid boolean accepted")
	}
}

func TestVoteValue(t *testing.T) {
	n := func(i int) *int { return &i }
	thumbs := db.Room{VoteMode: "thumbs"}
	budget := db.Room{VoteMode: "budget", VotesPerMember: 3}
	budgetDup := db.Room{VoteMode: "budget", VotesPerMember: 3, AllowDuplicateVotes: true}

	tests := []struct {
		name    string
		room    db.Room
		body    voteBody
		want    int
		wantErr bool
	}{
		{"thumbs up", thumbs, voteBody{Value: n(1)}, 1, false},
		{"thumbs down", thumbs, voteBody{Value: n(-1)}, -1, false},
		{"thumbs zero", thumbs, voteBody{Value: n(0)}, 0, true},
		{"thumbs with count", thumbs, voteBody{Count: n(1)}, 0, true},
		{"budget one", budget, voteBody{Count: n(1)}, 1, false},
		{"budget stacked without duplicates", budget, voteBody{Count: n(2)}, 0, true},
		{"budget with value", budget, voteBody{Value: n(1)}, 0, true},
		{"budget zero", budget, voteBody{Count: n(0)}, 0, true},
		{"budget stacked with duplicates", budgetDup, voteBody{Count: n(3)}, 3, false},
		{"budget over total", budgetDup, voteBody{Count: n(4)}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := voteValue(tt.room, tt.body)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("voteValue = %d, %v; want %d, err %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
