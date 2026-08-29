package order

import (
	"time"

	"github.com/google/uuid"

	"avito-kitchen/internal/domain/errs"
)

// CheckoutLine is one line of a checkout attempt: a cart line plus the
// result of re-checking it against live menu data, right before the order
// is created (PROMPT.md 5.2's "hard" check — stock and price can drift
// between adding to cart and checking out, so the soft check made at
// add-to-cart time is not enough on its own).
//
// This is order's own plain data shape, not domain/catalog.MenuItem:
// domain/order must not import domain/catalog (PROMPT.md 6.2 keeps every
// domain package independent of the others, since catalog and order are
// two of the modules the architecture is explicitly written to be able to
// split into separate services later — see PROMPT.md 6.1). The use-case
// that assembles a []CheckoutLine already has both a cart line and a live
// catalog.MenuItem in hand; Err is exactly whatever
// catalog.MenuItem.EnsureAvailable / EnsurePriceUnchanged returned for that
// line, unchanged — this package inspects only its Code (a plain string,
// not a catalog type) to decide what to do, never redoing the check
// itself. That is also why UnitPriceMinor is passed in already resolved:
// by the time Checkout runs, "is the price still what the cart thinks"
// has already been answered by Err.
type CheckoutLine struct {
	MenuItemID     uuid.UUID
	Name           string
	Quantity       int
	UnitPriceMinor int64
	Err            error
}

// Checkout validates lines and, if every one of them is clean, builds a new
// Order in StatusCreated. It is all-or-nothing: any failure returns only an
// error, never a partially built Order.
//
// Note what Checkout deliberately does NOT check: the venue's minimum
// order amount. That check (catalog.Venue.EnsureMinOrderReached) needs the
// venue's data, which — same rule as above — this package cannot import.
// The checkout use-case runs it itself, on the total this function's
// result reports through Order.TotalMinor(), after Checkout succeeds.
//
// Failure priority when lines have different problems, worth stating
// explicitly because nothing about it is forced by the data itself: a
// price change is reported ahead of a stock shortage, because a stale
// price invalidates whatever total the customer already agreed to
// regardless of what else is wrong — there is no point also telling them
// about stock on an order they would reject anyway once they see the
// corrected total. Within stock problems, an item pulled from the menu
// entirely (ITEM_UNAVAILABLE) is reported ahead of one that simply has too
// little left (INSUFFICIENT_STOCK), mirroring the priority
// catalog.MenuItem.EnsureAvailable already uses for a single item. Every
// line sharing the winning problem is reported together, in Details — not
// just the first one found — so the client can fix its cart in one round
// trip instead of one item at a time.
func Checkout(id, clientID, venueID uuid.UUID, lines []CheckoutLine, deliveryAddress, customerPhone, comment string, now time.Time) (*Order, error) {
	if len(lines) == 0 {
		return nil, errs.New(errs.CodeCartEmpty, "cart is empty")
	}

	for _, code := range []errs.Code{errs.CodePriceChanged, errs.CodeItemUnavailable, errs.CodeInsufficientStock} {
		if err := combineLineErrors(lines, code); err != nil {
			return nil, err
		}
	}
	for _, l := range lines {
		if l.Err != nil {
			// Some other, unanticipated per-line error (e.g. the use-case
			// wrapped a lookup failure) — nothing to batch, fail on the
			// first one.
			return nil, l.Err
		}
	}

	items := make([]Item, len(lines))
	for i, l := range lines {
		items[i] = Item{
			ID:             uuid.New(),
			MenuItemID:     l.MenuItemID,
			NameSnapshot:   l.Name,
			UnitPriceMinor: l.UnitPriceMinor,
			Quantity:       l.Quantity,
		}
	}

	return New(id, clientID, venueID, items, deliveryAddress, customerPhone, comment, now)
}

// combineLineErrors returns nil if no line's Err has the given code, or one
// combined *errs.Error whose Details concatenates every matching line's own
// Details — each line's Err already carries the right Details shape for
// its Code, built where the check happened (catalog.MenuItem), so this
// only merges, it never invents new detail fields.
func combineLineErrors(lines []CheckoutLine, code errs.Code) error {
	var message string
	var details []map[string]any

	for _, l := range lines {
		domainErr, ok := errs.As(l.Err)
		if !ok || domainErr.Code != code {
			continue
		}
		if message == "" {
			message = domainErr.Message
		}
		details = append(details, domainErr.Details...)
	}

	if len(details) == 0 {
		return nil
	}
	return errs.NewWithDetails(code, message, details)
}
