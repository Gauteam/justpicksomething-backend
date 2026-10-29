package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
	"github.com/google/uuid"
)

const (
	maxColumns        = 20
	maxColumnNameLen  = 40
	maxTextValueLen   = 200
	maxTextareaLen    = 2000
	maxCommentBodyLen = 1000
)

var columnTypes = map[string]bool{"text": true, "boolean": true, "textarea": true}

func registerM2Routes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /rooms/{slug}", withSession(requireAdmin(handleUpdateSettings)))

	mux.HandleFunc("POST /rooms/{slug}/columns", withSession(requireAdmin(requireUnlocked(handleAddColumn))))
	mux.HandleFunc("PATCH /rooms/{slug}/columns/{id}", withSession(requireAdmin(requireUnlocked(handleRenameColumn))))
	mux.HandleFunc("DELETE /rooms/{slug}/columns/{id}", withSession(requireAdmin(requireUnlocked(handleDeleteColumn))))
	mux.HandleFunc("PUT /rooms/{slug}/columns/order", withSession(requireAdmin(requireUnlocked(handleReorderColumns))))

	mux.HandleFunc("PATCH /rooms/{slug}/items/{id}", withSession(requireUnlocked(handleUpdateItem)))
	mux.HandleFunc("PUT /rooms/{slug}/items/order", withSession(requireAdmin(requireUnlocked(handleReorderItems))))

	mux.HandleFunc("GET /rooms/{slug}/items/{id}/comments", withSession(handleListComments))
	mux.HandleFunc("POST /rooms/{slug}/items/{id}/comments", withSession(requireUnlocked(handleAddComment)))
	mux.HandleFunc("PUT /rooms/{slug}/comments/{id}", withSession(requireUnlocked(handleEditComment)))
	mux.HandleFunc("DELETE /rooms/{slug}/comments/{id}", withSession(requireUnlocked(handleDeleteComment)))
}

type sessionHandler = func(http.ResponseWriter, *http.Request, db.Session)

// requireUnlocked rejects changes while the room is locked in.
func requireUnlocked(next sessionHandler) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, s db.Session) {
		if s.Room.LockedAt != nil {
			writeError(w, http.StatusConflict, "this room is locked in")
			return
		}
		next(w, r, s)
	}
}

func requireAdmin(next sessionHandler) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, s db.Session) {
		if !s.Member.IsAdmin {
			writeError(w, http.StatusForbidden, "only admins can do that")
			return
		}
		next(w, r, s)
	}
}

func handleAddColumn(w http.ResponseWriter, r *http.Request, s db.Session) {
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name, err := cleanText("name", body.Name, maxColumnNameLen)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !columnTypes[body.Type] {
		writeError(w, http.StatusBadRequest, "type must be text, boolean or textarea")
		return
	}

	col, err := db.AddColumn(r.Context(), s.Room.ID, name, body.Type, maxColumns)
	switch {
	case errors.Is(err, db.ErrLimit):
		writeError(w, http.StatusConflict, fmt.Sprintf("a room can have at most %d columns", maxColumns))
	case errors.Is(err, db.ErrNameTaken):
		writeError(w, http.StatusConflict, "there's already a column with that name")
	case err != nil:
		internalError(w, "add column", err)
	default:
		writeJSON(w, http.StatusCreated, col)
	}
}

func handleRenameColumn(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "column")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name, err := cleanText("name", body.Name, maxColumnNameLen)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = db.RenameColumn(r.Context(), s.Room.ID, id, name)
	writeMutationResult(w, err, "rename column", "column", "there's already a column with that name")
}

func handleDeleteColumn(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "column")
	if !ok {
		return
	}
	err := db.DeleteColumn(r.Context(), s.Room.ID, id)
	writeMutationResult(w, err, "delete column", "column", "")
}

func handleReorderColumns(w http.ResponseWriter, r *http.Request, s db.Session) {
	ids, ok := decodeIDs(w, r)
	if !ok {
		return
	}
	err := db.ReorderColumns(r.Context(), s.Room.ID, ids)
	writeMutationResult(w, err, "reorder columns", "column", "")
}

func handleReorderItems(w http.ResponseWriter, r *http.Request, s db.Session) {
	ids, ok := decodeIDs(w, r)
	if !ok {
		return
	}
	err := db.ReorderItems(r.Context(), s.Room.ID, ids)
	writeMutationResult(w, err, "reorder items", "item", "")
}

// handleUpdateItem edits an item's title and/or column values. Any member can
// edit any item. In fields, null or "" clears a value.
func handleUpdateItem(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	var body struct {
		Title  *string                    `json:"title"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	var title *string
	if body.Title != nil {
		t, err := cleanText("title", *body.Title, maxItemTitleLen)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		title = &t
	}

	columns, err := db.Columns(r.Context(), s.Room.ID)
	if err != nil {
		internalError(w, "update item: columns", err)
		return
	}
	fields, err := cleanFields(columns, body.Fields)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = db.UpdateItem(r.Context(), s.Room.ID, id, title, fields)
	writeMutationResult(w, err, "update item", "item", "another option already has that name")
}

// cleanFields validates raw column values against the room's columns.
// Cleared values come back as nil so the database removes them.
func cleanFields(columns []db.Column, raw map[string]json.RawMessage) (map[string]any, error) {
	types := make(map[string]db.Column, len(columns))
	for _, c := range columns {
		types[c.ID] = c
	}

	fields := make(map[string]any, len(raw))
	for id, v := range raw {
		col, ok := types[id]
		if !ok {
			return nil, errors.New("unknown column")
		}
		if string(v) == "null" {
			fields[id] = nil
			continue
		}

		switch col.Type {
		case "boolean":
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				return nil, fmt.Errorf("%s must be true or false", col.Name)
			}
			fields[id] = b
		case "text", "textarea":
			var str string
			if err := json.Unmarshal(v, &str); err != nil {
				return nil, fmt.Errorf("%s must be text", col.Name)
			}
			max, multiline := maxTextValueLen, false
			if col.Type == "textarea" {
				max, multiline = maxTextareaLen, true
			}
			cleaned, err := cleanValue(col.Name, str, max, multiline)
			if err != nil {
				return nil, err
			}
			if cleaned == "" {
				fields[id] = nil
			} else {
				fields[id] = cleaned
			}
		}
	}
	return fields, nil
}

// cleanValue trims s and checks its length; multiline values may contain line
// breaks and tabs. Unlike cleanText, empty is allowed.
func cleanValue(field, s string, max int, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", fmt.Errorf("%s must be at most %d characters", field, max)
	}
	bad := func(r rune) bool {
		if multiline && (r == '\n' || r == '\r' || r == '\t') {
			return false
		}
		return unicode.IsControl(r)
	}
	if strings.IndexFunc(s, bad) >= 0 {
		return "", fmt.Errorf("%s contains invalid characters", field)
	}
	return s, nil
}

func handleListComments(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	comments, err := db.Comments(r.Context(), s.Room.ID, id)
	if err != nil {
		internalError(w, "list comments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": comments})
}

func handleAddComment(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "item")
	if !ok {
		return
	}
	body, ok := decodeCommentBody(w, r)
	if !ok {
		return
	}

	c, err := db.AddComment(r.Context(), s.Room.ID, id, s.Member.ID, body)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		internalError(w, "add comment", err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// handleEditComment lets authors edit their own comments.
func handleEditComment(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "comment")
	if !ok {
		return
	}
	body, ok := decodeCommentBody(w, r)
	if !ok {
		return
	}
	if !canChangeComment(w, r, s, id, false) {
		return
	}
	err := db.EditComment(r.Context(), s.Room.ID, id, body)
	writeMutationResult(w, err, "edit comment", "comment", "")
}

// handleDeleteComment lets authors delete their own comments and admins
// delete any.
func handleDeleteComment(w http.ResponseWriter, r *http.Request, s db.Session) {
	id, ok := idFromPath(w, r, "comment")
	if !ok {
		return
	}
	if !canChangeComment(w, r, s, id, true) {
		return
	}
	err := db.DeleteComment(r.Context(), s.Room.ID, id)
	writeMutationResult(w, err, "delete comment", "comment", "")
}

func canChangeComment(w http.ResponseWriter, r *http.Request, s db.Session, commentID string, adminAllowed bool) bool {
	author, err := db.CommentAuthor(r.Context(), s.Room.ID, commentID)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "comment not found")
		return false
	}
	if err != nil {
		internalError(w, "comment author", err)
		return false
	}
	if author != s.Member.ID && !(adminAllowed && s.Member.IsAdmin) {
		writeError(w, http.StatusForbidden, "that's not your comment")
		return false
	}
	return true
}

func decodeCommentBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &body) {
		return "", false
	}
	text, err := cleanValue("comment", body.Body, maxCommentBodyLen, true)
	if err == nil && text == "" {
		err = errors.New("comment is required")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return text, true
}

func decodeIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return nil, false
	}
	for i, id := range body.IDs {
		u, err := uuid.Parse(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "ids must be UUIDs")
			return nil, false
		}
		body.IDs[i] = u.String()
	}
	return body.IDs, true
}

// idFromPath reads the {id} path segment; anything that isn't a UUID can't
// exist, so it's a 404.
func idFromPath(w http.ResponseWriter, r *http.Request, what string) (string, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, what+" not found")
		return "", false
	}
	return id.String(), true
}

// writeMutationResult maps the common db errors of an update/delete to a
// response, or writes 204 on success.
func writeMutationResult(w http.ResponseWriter, err error, op, what, nameTakenMsg string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, what+" not found")
	case errors.Is(err, db.ErrNameTaken) && nameTakenMsg != "":
		writeError(w, http.StatusConflict, nameTakenMsg)
	case errors.Is(err, db.ErrStale):
		writeError(w, http.StatusConflict, "the list changed; refresh and try again")
	default:
		internalError(w, op, err)
	}
}
