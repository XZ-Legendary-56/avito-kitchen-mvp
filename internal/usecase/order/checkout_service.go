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

// idempotencyKeyTTL is how long an Idempotency-Key is honored. PROMPT.md
// does not name a duration, and nothing in this project reaps expired keys
// yet (idempotency_keys.expires_at is recorded for that future job, not
// enforced today) — 24 hours is this project's own assumption, chosen as
// comfortably longer than any plausible client retry window.
const idempotencyKeyTTL = 24 * time.Hour

// CheckoutService places an order from a client's cart (PROMPT.md 5.1 item
// 4). It is a separate service from CartService on purpose: cart mutation
// and checkout have very different transactional needs — checkout is the
// only place in this package that ever decrements menu_items.stock_qty,
// under row locks, and everything it touches must agree inside one
// transaction or not happen at all.
type CheckoutService struct {
	carts       CartRepository
	venues      VenueLookup
	menuItems   MenuItemLookup
	orders      OrderRepository
	idempotency IdempotencyRepository
	outbox      OutboxRepository
	txManager   usecase.TxManager
}

func NewCheckoutService(carts CartRepository, venues VenueLookup, menuItems MenuItemLookup, orders OrderRepository, idempotency IdempotencyRepository, outbox OutboxRepository, txManager usecase.TxManager) *CheckoutService {
	return &CheckoutService{carts: carts, venues: venues, menuItems: menuItems, orders: orders, idempotency: idempotency, outbox: outbox, txManager: txManager}
}

// PlaceOrder turns clientID's cart into an order, clearing the cart on
// success. Every check that can be invalidated by a concurrent request —
// venue availability, price, stock — runs again inside the transaction
// (PROMPT.md 5.2), even though the cart/menu endpoints already ran the
// same checks softly while the customer was building their cart: time has
// passed since then, and either one can have changed.
//
// idempotencyKey scopes everything below it: claiming the key happens
// first, and the cart is deliberately NOT read before that. A retried
// request with the same key arrives with an EMPTY cart whenever the
// original attempt already succeeded and cleared it — reading the cart
// first would misreport that as CART_EMPTY instead of replaying the
// original order.
//
// replayed reports whether this call returned an order created by an
// earlier request (true) or created one just now (false) — the public API
// answers 200 vs 201 based on it (api/openapi/public.yaml createOrder).
func (s *CheckoutService) PlaceOrder(ctx context.Context, clientID uuid.UUID, idempotencyKey, deliveryAddress, customerPhone, comment string) (*domainorder.Order, bool, error) {
	requestHash := domainorder.HashCheckoutRequest(deliveryAddress, customerPhone, comment)

	var placed *domainorder.Order
	var wasReplayed bool
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		claim, err := s.idempotency.Claim(ctx, clientID, idempotencyKey, requestHash, time.Now().Add(idempotencyKeyTTL))
		if err != nil {
			return fmt.Errorf("claim idempotency key: %w", err)
		}
		if !claim.Claimed {
			if !claim.HashMatches {
				return errs.New(errs.CodeIdempotencyKeyConflict,
					"this idempotency key was already used with a different request")
			}
			existing, err := s.orders.Get(ctx, claim.OrderID)
			if err != nil {
				return fmt.Errorf("get order for replayed idempotency key: %w", err)
			}
			if existing == nil {
				return fmt.Errorf("idempotency key %s references missing order %s", idempotencyKey, claim.OrderID)
			}
			placed = existing
			wasReplayed = true
			return nil
		}

		cart, err := s.carts.Get(ctx, clientID)
		if err != nil {
			return fmt.Errorf("get cart: %w", err)
		}
		if cart == nil {
			return errs.New(errs.CodeCartEmpty, "cart is empty")
		}
		if err := cart.EnsureNotEmpty(); err != nil {
			return err
		}

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
		// Enqueued in the same transaction as the order it describes
		// (PROMPT.md 6.5): either both are committed together or neither is,
		// so a webhook can never fire for an order that doesn't exist.
		event, err := newOrderCreatedEvent(o)
		if err != nil {
			return err
		}
		if err := s.outbox.Enqueue(ctx, event); err != nil {
			return fmt.Errorf("enqueue order.created event: %w", err)
		}
		if err := s.idempotency.LinkOrder(ctx, clientID, idempotencyKey, o.ID); err != nil {
			return fmt.Errorf("link idempotency key: %w", err)
		}
		if err := s.carts.Clear(ctx, clientID); err != nil {
			return fmt.Errorf("clear cart: %w", err)
		}

		placed = o
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return placed, wasReplayed, nil
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
