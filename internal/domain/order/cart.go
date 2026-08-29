package order

import (
	"github.com/google/uuid"

	"avito-kitchen/internal/domain/errs"
)

// CartItem is one line in a cart. PriceMinorSnapshot is the price shown to
// the customer when the item was added (PROMPT.md 9: cart_items), compared
// against the live menu price at checkout so a change can be reported as
// PRICE_CHANGED instead of silently charging a different amount.
type CartItem struct {
	MenuItemID         uuid.UUID
	Quantity           int
	PriceMinorSnapshot int64
}

// Total is this line's snapshot price times quantity.
func (i CartItem) Total() int64 {
	return i.PriceMinorSnapshot * int64(i.Quantity)
}

// Cart holds the items a client is assembling for a single venue. A cart is
// pinned to the venue it was created for (PROMPT.md 5.1): adding an item
// from a different venue is an error, not an automatic merge or a silent
// switch, because a silent switch would leave the customer ordering from a
// venue they no longer meant to.
type Cart struct {
	ClientID uuid.UUID
	VenueID  uuid.UUID
	Items    []CartItem
}

// NewCart starts an empty cart pinned to venueID.
func NewCart(clientID, venueID uuid.UUID) *Cart {
	return &Cart{ClientID: clientID, VenueID: venueID}
}

// AddItem adds quantity units of menuItemID at priceMinorSnapshot. If
// menuItemID is already in the cart, quantity is added to the existing
// line rather than creating a duplicate one. venueID must match the cart's
// pinned venue, or this returns CART_VENUE_MISMATCH.
func (c *Cart) AddItem(venueID, menuItemID uuid.UUID, quantity int, priceMinorSnapshot int64) error {
	if venueID != c.VenueID {
		return errs.New(errs.CodeCartVenueMismatch,
			"cart is pinned to a different venue; clear it first")
	}

	for i, existing := range c.Items {
		if existing.MenuItemID == menuItemID {
			c.Items[i].Quantity += quantity
			return nil
		}
	}
	c.Items = append(c.Items, CartItem{
		MenuItemID:         menuItemID,
		Quantity:           quantity,
		PriceMinorSnapshot: priceMinorSnapshot,
	})
	return nil
}

// TotalMinor sums every line's Total().
func (c *Cart) TotalMinor() int64 {
	var total int64
	for _, item := range c.Items {
		total += item.Total()
	}
	return total
}

// EnsureNotEmpty returns CART_EMPTY if the cart has no items. Checked before
// checkout: an empty cart cannot become an order.
func (c *Cart) EnsureNotEmpty() error {
	if len(c.Items) == 0 {
		return errs.New(errs.CodeCartEmpty, "cart is empty")
	}
	return nil
}
