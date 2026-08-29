// Package partner implements api/openapi/partner.yaml's generated
// partnerapi.StrictServerInterface. Like adapter/http/public, handlers here
// only translate between the wire format and the usecase layer.
package partner

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"avito-kitchen/internal/adapter/http/httperr"
	"avito-kitchen/internal/adapter/http/middleware"
	"avito-kitchen/internal/domain/errs"
)

// authenticator is the one method Handlers.APIKeyAuth needs — satisfied by
// usecase/partner.AuthService. Declared locally (not imported as a
// concrete type) so this middleware only depends on the shape it uses.
type authenticator interface {
	Authenticate(ctx context.Context, rawKey string) (uuid.UUID, error)
}

// APIKeyAuth is PROMPT.md 5.3 item 1: every partner route requires
// X-Api-Key, resolved to the one venue it belongs to. It lives here, not
// in adapter/http/middleware, because it needs httperr.WritePartner to
// report a failure — and httperr already imports middleware (for
// RequestIDFromContext), so middleware importing httperr back would be a
// cycle.
func APIKeyAuth(auth authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-Api-Key")
			if key == "" {
				httperr.WritePartner(w, r, errs.New(errs.CodeUnauthorized, "X-Api-Key header is required"))
				return
			}
			venueID, err := auth.Authenticate(r.Context(), key)
			if err != nil {
				httperr.WritePartner(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(middleware.WithVenueID(r.Context(), venueID)))
		})
	}
}
