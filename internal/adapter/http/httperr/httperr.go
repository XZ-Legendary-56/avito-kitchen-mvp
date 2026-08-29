// Package httperr is the single place a Go error becomes an HTTP response
// for the public API (PROMPT.md 10.2): handlers never choose a status code
// themselves, they just return the error and the generated strict server
// routes it here through the hooks wired in adapter/http/public.
package httperr

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"avito-kitchen/internal/adapter/http/middleware"
	"avito-kitchen/internal/domain/errs"
	"avito-kitchen/internal/generated/publicapi"
)

// statusFor maps a domain error Code to its HTTP status, per the table in
// PROMPT.md 5.2 plus the generic REST cases from 7.1.
func statusFor(code errs.Code) int {
	switch code {
	case errs.CodeVenueClosed,
		errs.CodeVenueNotAcceptingOrders,
		errs.CodeItemUnavailable,
		errs.CodeInsufficientStock,
		errs.CodePriceChanged,
		errs.CodeCartVenueMismatch,
		errs.CodeCartEmpty,
		errs.CodeOrderInvalidStateTransition,
		errs.CodeIdempotencyKeyConflict:
		return http.StatusConflict
	case errs.CodeMinOrderAmountNotReached:
		return http.StatusUnprocessableEntity
	case errs.CodeValidationError:
		return http.StatusBadRequest
	case errs.CodeNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// Write encodes err as the Error schema shared by both OpenAPI specs
// (api/openapi/*.yaml components.schemas.Error): a stable code, a message,
// and the request id so a bug report can be matched to server logs. A
// domain error (errs.Error) drives status and code directly. Anything else
// is unexpected: it is logged in full here (this is the only place that
// sees it) and reported to the client as a generic 500 with no details,
// so an internal error message or stack-adjacent text never leaks out.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())

	var (
		code    errs.Code
		status  int
		message string
	)
	if domainErr, ok := errs.As(err); ok {
		code = domainErr.Code
		status = statusFor(code)
		message = domainErr.Message
	} else {
		slog.Error("unhandled error reached the http layer",
			"error", err, "request_id", requestID, "path", r.URL.Path)
		code = errs.CodeInternal
		status = http.StatusInternalServerError
		message = "internal server error"
	}

	writeJSON(w, status, publicapi.Error{
		Code:      publicapi.ErrorCode(code),
		Message:   message,
		RequestId: requestID,
	})
}

// WriteRequestError handles a malformed request the generated layer could
// not even bind (bad JSON body, wrong-format path/query/header parameter) —
// always the client's fault, so it is always VALIDATION_ERROR/400 rather
// than going through statusFor.
func WriteRequestError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())
	writeJSON(w, http.StatusBadRequest, publicapi.Error{
		Code:      publicapi.ErrorCode(errs.CodeValidationError),
		Message:   err.Error(),
		RequestId: requestID,
	})
}

func writeJSON(w http.ResponseWriter, status int, body publicapi.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
