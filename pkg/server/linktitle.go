package server

import (
	"log"
	"net/http"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
	"github.com/Gauteam/justpicksomething-backend/pkg/linktitle"
)

var titles = linktitle.New()

// handleLinkTitle returns the page title for a pasted link, so the UI can
// name an option after it. Any member can ask. Failures (blocked sites like
// VRBO, timeouts, refused addresses) come back as {"title": null} so the UI
// falls back to a name made from the link.
func handleLinkTitle(w http.ResponseWriter, r *http.Request, s db.Session) {
	var body struct {
		URL string `json:"url"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.URL) > 2000 {
		writeError(w, http.StatusBadRequest, "url is too long")
		return
	}

	title, err := titles.Title(r.Context(), body.URL)
	if err != nil {
		log.Printf("link title: %v", err)
	}
	if err != nil || title == "" {
		writeJSON(w, http.StatusOK, map[string]any{"title": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"title": title})
}
