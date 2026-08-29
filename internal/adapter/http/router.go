// Package http assembles the top-level chi router: shared middleware, the
// public API under /api/v1, the partner API under /api/v1/partner, and
// operational plumbing.
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"avito-kitchen/internal/adapter/http/httperr"
	"avito-kitchen/internal/adapter/http/middleware"
	"avito-kitchen/internal/adapter/http/partner"
	"avito-kitchen/internal/adapter/http/public"
	"avito-kitchen/internal/adapter/postgres"
	"avito-kitchen/internal/generated/partnerapi"
	"avito-kitchen/internal/generated/publicapi"
	catalogusecase "avito-kitchen/internal/usecase/catalog"
	orderusecase "avito-kitchen/internal/usecase/order"
	partnerusecase "avito-kitchen/internal/usecase/partner"
)

// NewRouter builds the full HTTP handler for the API service.
func NewRouter(logger *slog.Logger, pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(logger))
	r.Use(middleware.Recover(logger))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(pool))

	r.Mount("/api/v1", newPublicAPIRouter(pool))
	r.Mount("/api/v1/partner", newPartnerAPIRouter(pool))

	return r
}

// newPublicAPIRouter wires the public API's use-cases onto their Postgres
// repositories and mounts the generated strict server. adapter/postgres's
// MenuRepository is passed to both catalog's own MenuRepository port and
// order's MenuItemLookup port — one adapter type satisfying two
// usecase-declared interfaces, per PROMPT.md 6.2 (see that type's doc
// comment for why).
func newPublicAPIRouter(pool *pgxpool.Pool) http.Handler {
	venues := postgres.NewVenueRepository(pool)
	menus := postgres.NewMenuRepository(pool)
	carts := postgres.NewCartRepository(pool)
	orders := postgres.NewOrderRepository(pool)
	idempotency := postgres.NewIdempotencyRepository(pool)
	txManager := postgres.NewTxManager(pool)

	handlers := &public.Handlers{
		Catalog:  catalogusecase.NewService(venues, menus),
		Cart:     orderusecase.NewCartService(carts, menus, txManager),
		Checkout: orderusecase.NewCheckoutService(carts, venues, menus, orders, idempotency, txManager),
		Orders:   orderusecase.NewOrderService(orders, txManager),
	}

	strictHandler := publicapi.NewStrictHandlerWithOptions(handlers, nil, publicapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httperr.WriteRequestError,
		ResponseErrorHandlerFunc: httperr.Write,
	})

	apiRouter := chi.NewRouter()
	return publicapi.HandlerWithOptions(strictHandler, publicapi.ChiServerOptions{
		BaseRouter:       apiRouter,
		ErrorHandlerFunc: httperr.WriteRequestError,
	})
}

// newPartnerAPIRouter wires the partner API. Every route requires
// X-Api-Key (PROMPT.md 5.3 item 1), enforced by partner.APIKeyAuth before
// any generated handler runs — it resolves the key to a venue and stores
// it in the request context, which every Handlers method then reads
// instead of taking a venue id as a request parameter.
func newPartnerAPIRouter(pool *pgxpool.Pool) http.Handler {
	venues := postgres.NewVenueRepository(pool)
	menus := postgres.NewMenuRepository(pool)
	orders := postgres.NewOrderRepository(pool)
	auth := postgres.NewPartnerAuthRepository(pool)
	txManager := postgres.NewTxManager(pool)

	handlers := &partner.Handlers{
		Venues: partnerusecase.NewVenueService(venues, txManager),
		Menus:  partnerusecase.NewMenuService(menus),
		Orders: partnerusecase.NewOrderService(orders),
	}

	strictHandler := partnerapi.NewStrictHandlerWithOptions(handlers, nil, partnerapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httperr.WritePartnerRequestError,
		ResponseErrorHandlerFunc: httperr.WritePartner,
	})

	apiRouter := chi.NewRouter()
	apiRouter.Use(partner.APIKeyAuth(partnerusecase.NewAuthService(auth)))
	return partnerapi.HandlerWithOptions(strictHandler, partnerapi.ChiServerOptions{
		BaseRouter:       apiRouter,
		ErrorHandlerFunc: httperr.WritePartnerRequestError,
	})
}
