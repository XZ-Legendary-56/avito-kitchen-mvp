// Package http assembles the top-level chi router: shared middleware plus
// whatever route groups exist so far. The public and partner API handlers
// (adapter/http/public, adapter/http/partner) are mounted here once later
// stages add them — for now there is only operational plumbing.
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"avito-kitchen/internal/adapter/http/middleware"
)

// NewRouter builds the full HTTP handler for the API service.
func NewRouter(logger *slog.Logger, pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(logger))
	r.Use(middleware.Recover(logger))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(pool))

	return r
}
