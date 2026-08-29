package postgres

import (
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"

	"avito-kitchen/internal/domain/errs"
)

// venueCursor is the keyset position for venue listing: (name, id), the
// same tuple List orders by, so "give me everything after this row" is a
// single ordered comparison. Encoded opaquely (PROMPT.md 7.1: "opaque
// pagination cursor") so the API is free to change how it paginates later
// without that leaking into the wire format.
type venueCursor struct {
	Name string    `json:"n"`
	ID   uuid.UUID `json:"i"`
}

func encodeVenueCursor(c venueCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeVenueCursor(s string) (venueCursor, error) {
	var c venueCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, errs.New(errs.CodeValidationError, "invalid cursor")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, errs.New(errs.CodeValidationError, "invalid cursor")
	}
	return c, nil
}
