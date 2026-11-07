package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Gauteam/justpicksomething-backend/pkg/auth"
	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

func registerMemberRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /rooms/{slug}/members/{id}", withSession(requireAdmin(handleSetAdmin)))
	// Removing someone deletes their votes, so not while locked.
	mux.HandleFunc("DELETE /rooms/{slug}/members/{id}", withSession(requireAdmin(requireUnlocked(handleRemoveMember))))
	mux.HandleFunc("POST /rooms/{slug}/passcode", withSession(requireAdmin(handleResetPasscode)))
}

func handleSetAdmin(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "member")
	if !ok {
		return
	}
	var body struct {
		IsAdmin *bool `json:"isAdmin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.IsAdmin == nil {
		writeError(w, http.StatusBadRequest, "isAdmin is required")
		return
	}
	err := db.SetAdmin(r.Context(), s.Room.ID, id, *body.IsAdmin)
	writeMemberResult(w, err, "set admin")
}

func handleRemoveMember(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "member")
	if !ok {
		return
	}
	err := db.RemoveMember(r.Context(), s.Room.ID, id)
	writeMemberResult(w, err, "remove member")
}

// handleResetPasscode sets a new random passcode and returns it once.
// Existing members stay joined.
func handleResetPasscode(w http.ResponseWriter, r *http.Request, s db.Session) {
	passcode := auth.NewPasscode()
	hash, err := auth.HashPasscode(passcode)
	if err != nil {
		internalError(w, "reset passcode: hash", err)
		return
	}
	changedAt, err := db.ResetPasscode(r.Context(), s.Room.ID, hash)
	if err != nil {
		internalError(w, "reset passcode", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"passcode":          passcode,
		"passcodeChangedAt": changedAt.UTC().Format(time.RFC3339Nano),
	})
}

func writeMemberResult(w http.ResponseWriter, err error, op string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "member not found")
	case errors.Is(err, db.ErrLastAdmin):
		writeError(w, http.StatusConflict, "a room needs at least one admin; make someone else an admin first")
	default:
		internalError(w, op, err)
	}
}

// cleanNewColumns validates starting columns: at most maxColumns, known
// types, names unique ignoring case.
func cleanNewColumns(cols []db.NewColumn) ([]db.NewColumn, error) {
	if len(cols) > maxColumns {
		return nil, fmt.Errorf("a room can have at most %d columns", maxColumns)
	}
	seen := map[string]bool{}
	out := make([]db.NewColumn, 0, len(cols))
	for _, c := range cols {
		name, err := cleanText("column name", c.Name, maxColumnNameLen)
		if err != nil {
			return nil, err
		}
		if !columnTypes[c.Type] {
			return nil, fmt.Errorf("column %q: type must be text, boolean or textarea", name)
		}
		k := strings.ToLower(name)
		if seen[k] {
			return nil, fmt.Errorf("column %q is listed twice", name)
		}
		seen[k] = true
		out = append(out, db.NewColumn{Name: name, Type: c.Type})
	}
	return out, nil
}
