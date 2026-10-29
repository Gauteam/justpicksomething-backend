// Command dev runs the API locally on :8080 (or $PORT) without Vercel.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/Gauteam/justpicksomething-backend/pkg/server"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cfg := server.ConfigFromEnv()
	log.Printf("listening on :%s (allowed origins: %v)", port, cfg.AllowedOrigins)
	log.Fatal(http.ListenAndServe(":"+port, server.New(cfg)))
}
