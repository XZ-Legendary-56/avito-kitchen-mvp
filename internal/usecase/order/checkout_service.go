package order

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	domainorder "avito-kitchen/internal/domain/order"
	"avito-kitchen/internal/usecase"
)

// CheckoutService places an order from a client's cart (PROMPT.md 5.1 item
// 4). It is a separate service from CartService on purpose: cart mutation
// and checkout have very different transactional needs — checkout is the
// only place in this package that ever decrements menu_items.stock_qty,
// under row locks, and everything it touches must agree inside one
// transaction or not happen at all.
type CheckoutService struct {
	carts     CartRepository
	venues    VenueLookup
	menuItems MenuItemLookup
	orders    OrderRepository
	txManager usecase.TxManager
}

func NewCheckoutService(carts CartRepository, venues VenueLookup, menuItems MenuItemLookup, orders OrderRepository, txManager usecase.TxManager) *CheckoutService {
	return &CheckoutService{carts: carts, venues: venues, menuItems: menuItems, orders: orders, txManager: txManager}
}

// PlaceOrder turns clientID's cart into an order, clearing the cart on
// success. Every check that can be invalidated by a concurrent request —
// venue availability, price, stock — runs again inside the transaction
// (PROMPT.md 5.2), even though the cart/menu endpoints already ran the
// same checks softly while the customer was building their cart: time has
// passed since then, and either one can have changed.
func (s *CheckoutService) PlaceOrder(ctx context.Context, clientID uuid.UUID, deliveryAddress, customerPhone, comment string) (*domainorder.Order, error) {
	cart, err := s.carts.Get(ctx, clientID)
	if err != nil {
		return nil, fmt.Errorf("get cart: %w", err)
	}
	if cart == nil {
		return nil, errs.New(errs.CodeCartEmpty, "cart is empty")
	}
	if err := cart.EnsureNotEmpty(); err != nil {
		return nil, err
	}

	var placed *domainorder.Order
	err = s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		venue, err := s.venues.Get(ctx, cart.VenueID)
		if err != nil {
			return fmt.Errorf("get venue: %w", err)
		}
		if venue == nil {
			return errs.New(errs.CodeNotFound, "venue not found")
		}
		now := time.Now()
		if err := venue.EnsureCanOrder(now); err != nil {
			return err
		}

		ids := make([]uuid.UUID, len(cart.Items))
		for i, item := range cart.Items {
			ids[i] = item.MenuItemID
		}
		// The line that makes concurrent checkouts safe: see
		// MenuItemLookup.LockForCheckout's doc comment and
		// adapter/postgres's implementation for why locking in id order
		// prevents a deadlock here.
		locked, err := s.menuItems.LockForCheckout(ctx, ids)
		if err != nil {
			return fmt.Errorf("lock menu items: %w", err)
		}

		lines := make([]domainorder.CheckoutLine, len(cart.Items))
		for i, item := range cart.Items {
			lines[i] = s.buildCheckoutLine(item, locked)
		}

		orderID := uuid.New()
		o, err := domainorder.Checkout(orderID, clientID, cart.VenueID, lines, deliveryAddress, customerPhone, comment, now)
		if err != nil {
			return err
		}
		if err := venue.EnsureMinOrderReached(o.TotalMinor()); err != nil {
			return err
		}

		stockChanged := false
		for _, item := range cart.Items {
			changed, err := s.menuItems.DecrementStock(ctx, item.MenuItemID, item.Quantity)
			if err != nil {
				return fmt.Errorf("decrement stock for %s: %w", item.MenuItemID, err)
			}
			stockChanged = stockChanged || changed
		}
		if stockChanged {
			if err := s.venues.BumpMenuVersion(ctx, cart.VenueID); err != nil {
				return fmt.Errorf("bump menu version: %w", err)
			}
		}

		if err := s.orders.Create(ctx, o); err != nil {
			return fmt.Errorf("create order: %w", err)
		}
		if err := s.carts.Clear(ctx, clientID); err != nil {
			return fmt.Errorf("clear cart: %w", err)
		}

		placed = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return placed, nil
}

// buildCheckoutLine turns one cart line into a domainorder.CheckoutLine,
// running the same per-item checks catalog.MenuItem already exposes
// (EnsurePriceUnchanged, then EnsureAvailable) against the just-locked
// state — this is the "hard check" from PROMPT.md 5.2. domainorder.Checkout
// only ever sees the resulting error, never the MenuItem itself, keeping
// this package's rule (it declares its own ports, never imports
// usecase/catalog) intact all the way down into the domain layer too.
func (s *CheckoutService) buildCheckoutLine(item domainorder.CartItem, locked map[uuid.UUID]domaincatalog.MenuItem) domainorder.CheckoutLine {
	line := domainorder.CheckoutLine{
		MenuItemID:     item.MenuItemID,
		Quantity:       item.Quantity,
		UnitPriceMinor: item.PriceMinorSnapshot,
	}

	mi, ok := locked[item.MenuItemID]
	if !ok {
		line.Err = errs.New(errs.CodeNotFound, "menu item no longer exists")
		return line
	}

	line.Name = mi.Name
	if err := mi.EnsurePriceUnchanged(item.PriceMinorSnapshot); err != nil {
		line.Err = err
		return line
	}
	line.Err = mi.EnsureAvailable(item.Quantity)
	return line
}
