package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gauteam/justpicksomething-backend/pkg/auth"
	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

const (
	maxRoomNameLen    = 100
	maxDisplayNameLen = 40
	maxItemTitleLen   = 200
	maxItemsPerAdd    = 50
	maxBodyBytes      = 16 << 10
)

func registerRoomRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /rooms", handleCreateRoom)
	mux.HandleFunc("POST /rooms/{slug}/join", handleJoinRoom)
	mux.HandleFunc("GET /rooms/{slug}", withSession(handleGetRoom))
	mux.HandleFunc("POST /rooms/{slug}/items", withSession(requireUnlocked(handleAddItem)))
	mux.HandleFunc("PUT /rooms/{slug}/items/{id}/vote", withSession(requireUnlocked(handleSetVote)))
	mux.HandleFunc("DELETE /rooms/{slug}/items/{id}/vote", withSession(requireUnlocked(handleClearVote)))
	registerM2Routes(mux)
}

func handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name, err := cleanText("name", body.Name, maxRoomNameLen)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	displayName, err := cleanText("displayName", body.DisplayName, maxDisplayNameLen)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	passcode := auth.NewPasscode()
	passcodeHash, err := auth.HashPasscode(passcode)
	if err != nil {
		internalError(w, "create room: hash passcode", err)
		return
	}
	token, tokenHash := auth.NewToken()

	var s db.Session
	for range 3 {
		s, err = db.CreateRoom(r.Context(), auth.NewSlug(), name, passcodeHash, displayName, tokenHash)
		if !errors.Is(err, db.ErrSlugTaken) {
			break
		}
	}
	if err != nil {
		internalError(w, "create room", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"slug":     s.Room.Slug,
		"passcode": passcode,
		"token":    token,
	})
}

func handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Passcode    string `json:"passcode"`
		DisplayName string `json:"displayName"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	displayName, err := cleanText("displayName", body.DisplayName, maxDisplayNameLen)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	roomID, passcodeHash, err := db.PasscodeHash(r.Context(), r.PathValue("slug"))
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return
	}
	if err != nil {
		internalError(w, "join room: lookup", err)
		return
	}
	if !auth.CheckPasscode(passcodeHash, body.Passcode) {
		writeError(w, http.StatusUnauthorized, "wrong passcode")
		return
	}

	token, tokenHash := auth.NewToken()
	if _, err := db.AddMember(r.Context(), roomID, displayName, tokenHash); err != nil {
		if errors.Is(err, db.ErrNameTaken) {
			writeError(w, http.StatusConflict, "that name is taken in this room")
			return
		}
		internalError(w, "join room: add member", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"token": token})
}

func handleGetRoom(w http.ResponseWriter, r *http.Request, s db.Session) {
	// Polling clients send the updatedAt they last saw; nothing changed means
	// a tiny response and no further queries. Not a 304: Vercel strips the
	// CORS headers from 304s, so browsers reject them.
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339Nano, since); err == nil && t.Equal(s.Room.UpdatedAt) {
			writeJSON(w, http.StatusOK, map[string]bool{"unchanged": true})
			return
		}
	}

	members, err := db.Members(r.Context(), s.Room.ID)
	if err != nil {
		internalError(w, "get room: members", err)
		return
	}
	columns, err := db.Columns(r.Context(), s.Room.ID)
	if err != nil {
		internalError(w, "get room: columns", err)
		return
	}
	items, err := db.Items(r.Context(), s.Room.ID, s.Member.ID)
	if err != nil {
		internalError(w, "get room: items", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"room":    s.Room,
		"me":      s.Member,
		"members": members,
		"columns": columns,
		"items":   items,
	})
}

// handleAddItem adds one item ({title}), several titles ({titles}), or
// several items with column values ({items: [{title, fields}]}). Blank titles
// and titles already in the room (ignoring case) are skipped.
func handleAddItem(w http.ResponseWriter, r *http.Request, s db.Session) {
	var body struct {
		Title  string    `json:"title"`
		Titles []string  `json:"titles"`
		Items  []rawItem `json:"items"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	raw := body.Items
	for _, t := range append(body.Titles, body.Title) {
		raw = append(raw, rawItem{Title: t})
	}

	var columns []db.Column
	for _, it := range raw {
		if len(it.Fields) > 0 {
			var err error
			if columns, err = db.Columns(r.Context(), s.Room.ID); err != nil {
				internalError(w, "add items: columns", err)
				return
			}
			break
		}
	}
	newItems, err := cleanNewItems(raw, columns)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	items, err := db.AddItems(r.Context(), s.Room.ID, s.Member.ID, newItems, s.Room.MaxItems)
	if errors.Is(err, db.ErrLimit) {
		writeError(w, http.StatusConflict, fmt.Sprintf("this room is limited to %d options", *s.Room.MaxItems))
		return
	}
	if err != nil {
		internalError(w, "add items", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": items})
}

type rawItem struct {
	Title  string                     `json:"title"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// cleanNewItems trims and validates items, dropping blank titles and
// case-insensitive duplicates while keeping the original order.
func cleanNewItems(raw []rawItem, columns []db.Column) ([]db.NewItem, error) {
	seen := map[string]bool{}
	var items []db.NewItem
	for _, it := range raw {
		if strings.TrimSpace(it.Title) == "" {
			continue
		}
		title, err := cleanText("title", it.Title, maxItemTitleLen)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", it.Title, err)
		}
		k := strings.ToLower(title)
		if seen[k] {
			continue
		}
		seen[k] = true

		fields, err := cleanFields(columns, it.Fields)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", title, err)
		}
		items = append(items, db.NewItem{Title: title, Fields: fields})
	}
	if len(items) == 0 {
		return nil, errors.New("title is required")
	}
	if len(items) > maxItemsPerAdd {
		return nil, fmt.Errorf("at most %d items at a time", maxItemsPerAdd)
	}
	return items, nil
}

func handleSetVote(w http.ResponseWriter, r *http.Request, s db.Session) {
	itemID, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	var body voteBody
	if !decodeBody(w, r, &body) {
		return
	}
	value, err := voteValue(s.Room, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var budget *int
	if s.Room.VoteMode == "budget" {
		budget = &s.Room.VotesPerMember
	}
	err = db.SetVote(r.Context(), s.Room.ID, itemID, s.Member.ID, value, budget)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "item not found")
	case errors.Is(err, db.ErrLimit):
		writeError(w, http.StatusConflict, fmt.Sprintf("you only have %d votes", s.Room.VotesPerMember))
	case err != nil:
		internalError(w, "set vote", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type voteBody struct {
	Value *int `json:"value"`
	Count *int `json:"count"`
}

// voteValue checks a vote against the room's mode: thumbs takes {value: ±1};
// budget takes {count: n}, with n > 1 only when duplicate votes are allowed.
// Clearing a vote is DELETE, not a zero.
func voteValue(room db.Room, b voteBody) (int, error) {
	if room.VoteMode == "budget" {
		switch {
		case b.Count == nil:
			return 0, errors.New("this room uses a vote budget: send count")
		case *b.Count < 1:
			return 0, errors.New("count must be at least 1")
		case *b.Count > 1 && !room.AllowDuplicateVotes:
			return 0, errors.New("this room allows one vote per option")
		case *b.Count > room.VotesPerMember:
			return 0, fmt.Errorf("you only have %d votes", room.VotesPerMember)
		}
		return *b.Count, nil
	}
	if b.Value == nil || (*b.Value != 1 && *b.Value != -1) {
		return 0, errors.New("value must be 1 or -1")
	}
	return *b.Value, nil
}

func handleClearVote(w http.ResponseWriter, r *http.Request, s db.Session) {
	itemID, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	if err := db.ClearVote(r.Context(), s.Room.ID, itemID, s.Member.ID); err != nil {
		internalError(w, "clear vote", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withSession authenticates the caller's bearer token against the room in the
// path. Any failure is a 401 so the client knows to show the join form.
func withSession(next func(http.ResponseWriter, *http.Request, db.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "missing token")
			return
		}

		s, err := db.SessionByToken(r.Context(), r.PathValue("slug"), auth.HashToken(token))
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if err != nil {
			internalError(w, "session lookup", err)
			return
		}
		next(w, r, s)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// cleanText trims s and checks it is non-empty, single-line and at most max
// characters.
func cleanText(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(s) > max {
		return "", fmt.Errorf("%s must be at most %d characters", field, max)
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s contains invalid characters", field)
	}
	return s, nil
}

func internalError(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
