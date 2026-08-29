package catalog

import (
	"time"

	"github.com/google/uuid"

	"avito-kitchen/internal/domain/errs"
)

// Venue is a restaurant on the platform (PROMPT.md 9: venues).
type Venue struct {
	ID                  uuid.UUID
	PartnerID           uuid.UUID
	Name                string
	Cuisine             string
	MinOrderAmountMinor int64
	AcceptingOrders     bool
	Schedule            []ScheduleEntry
}

// EnsureCanOrder returns VENUE_NOT_ACCEPTING_ORDERS or VENUE_CLOSED if the
// venue cannot take an order at "now". PROMPT.md 5.2 requires this check to
// run twice — softly when a customer adds an item to the cart, and again
// inside the checkout transaction — since the venue's state can change in
// the time between the two.
func (v Venue) EnsureCanOrder(now time.Time) error {
	if !v.AcceptingOrders {
		return errs.New(errs.CodeVenueNotAcceptingOrders, "venue is not accepting orders right now")
	}
	if !IsOpenAt(v.Schedule, now) {
		return errs.New(errs.CodeVenueClosed, "venue is closed right now")
	}
	return nil
}

// EnsureMinOrderReached returns MIN_ORDER_AMOUNT_NOT_REACHED if totalMinor
// is below the venue's minimum order amount.
func (v Venue) EnsureMinOrderReached(totalMinor int64) error {
	if totalMinor < v.MinOrderAmountMinor {
		return errs.Newf(errs.CodeMinOrderAmountNotReached,
			"order total %d is below the venue's minimum of %d", totalMinor, v.MinOrderAmountMinor)
	}
	return nil
}
