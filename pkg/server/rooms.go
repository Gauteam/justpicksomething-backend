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
	mux.HandleFunc("POST /rooms/{slug}/items", withSession(handleAddItem))
	mux.HandleFunc("PUT /rooms/{slug}/items/{id}/vote", withSession(handleSetVote))
	mux.HandleFunc("DELETE /rooms/{slug}/items/{id}/vote", withSession(handleClearVote))
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

// handleAddItem adds one item ({title}) or several at once ({titles}).
// Blank entries and titles already in the room (ignoring case) are skipped.
func handleAddItem(w http.ResponseWriter, r *http.Request, s db.Session) {
	var body struct {
		Title  string   `json:"title"`
		Titles []string `json:"titles"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	titles, err := cleanTitles(append(body.Titles, body.Title))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	items, err := db.AddItems(r.Context(), s.Room.ID, s.Member.ID, titles)
	if err != nil {
		internalError(w, "add items", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": items})
}

// cleanTitles trims and validates titles, dropping blanks and case-insensitive
// duplicates while keeping the original order.
func cleanTitles(raw []string) ([]string, error) {
	seen := map[string]bool{}
	var titles []string
	for _, t := range raw {
		if strings.TrimSpace(t) == "" {
			continue
		}
		title, err := cleanText("title", t, maxItemTitleLen)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", t, err)
		}
		if k := strings.ToLower(title); !seen[k] {
			seen[k] = true
			titles = append(titles, title)
		}
	}
	if len(titles) == 0 {
		return nil, errors.New("title is required")
	}
	if len(titles) > maxItemsPerAdd {
		return nil, fmt.Errorf("at most %d items at a time", maxItemsPerAdd)
	}
	return titles, nil
}

func handleSetVote(w http.ResponseWriter, r *http.Request, s db.Session) {
	itemID, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	var body struct {
		Value int `json:"value"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Value != 1 && body.Value != -1 {
		writeError(w, http.StatusBadRequest, "value must be 1 or -1")
		return
	}

	err := db.SetVote(r.Context(), s.Room.ID, itemID, s.Member.ID, body.Value)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		internalError(w, "set vote", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
