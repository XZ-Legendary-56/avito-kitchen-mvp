package catalog

import (
	"github.com/google/uuid"

	"avito-kitchen/internal/domain/errs"
)

// MenuItem is one position on a venue's menu (PROMPT.md 9: menu_items).
// StockQty nil means unlimited stock, typical for items cooked to order;
// a non-nil value is a hard cap that orders decrement.
type MenuItem struct {
	ID          uuid.UUID
	VenueID     uuid.UUID
	Name        string
	PriceMinor  int64
	IsAvailable bool
	StockQty    *int
}

// EnsureAvailable returns ITEM_UNAVAILABLE if the item was switched off by
// hand (the stop list), or INSUFFICIENT_STOCK if fewer than quantity units
// remain. These stay two different codes on purpose (PROMPT.md 9): "we are
// not selling this today" and "one portion left instead of three" need
// different messages shown to the customer.
func (m MenuItem) EnsureAvailable(quantity int) error {
	if !m.IsAvailable {
		return errs.Newf(errs.CodeItemUnavailable, "menu item %q is unavailable", m.Name)
	}
	if m.StockQty != nil && *m.StockQty < quantity {
		return errs.Newf(errs.CodeInsufficientStock,
			"only %d of menu item %q left, requested %d", *m.StockQty, m.Name, quantity)
	}
	return nil
}

// EnsurePriceUnchanged returns PRICE_CHANGED if snapshotPriceMinor no
// longer matches the item's current price — the cart holds a price snapshot
// from when the item was added, and checkout must catch drift instead of
// silently charging the new price.
func (m MenuItem) EnsurePriceUnchanged(snapshotPriceMinor int64) error {
	if m.PriceMinor != snapshotPriceMinor {
		return errs.Newf(errs.CodePriceChanged,
			"price of %q changed from %d to %d", m.Name, snapshotPriceMinor, m.PriceMinor)
	}
	return nil
}
