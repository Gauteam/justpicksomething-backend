// Package handler is the single Vercel function. vercel.json rewrites every
// path here and the server's router dispatches on the original path.
package handler

import (
	"net/http"
	"strings"

	"github.com/Gauteam/justpicksomething-backend/pkg/server"
)

var app = server.New(server.ConfigFromEnv())

func Handler(w http.ResponseWriter, r *http.Request) {
	// Guard against the rewritten path being passed through instead of the
	// original one.
	if p := strings.TrimPrefix(r.URL.Path, "/api/index"); p != r.URL.Path {
		if p == "" {
			p = "/"
		}
		r.URL.Path = p
	}
	app.ServeHTTP(w, r)
}
