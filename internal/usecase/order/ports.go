// Package order holds the cart use-cases (PROMPT.md 6.3: Cart lives in the
// "order" domain module, alongside Order itself) and the ports they declare
// for themselves.
package order

import (
	"context"
	"time"

	"github.com/google/uuid"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	domainorder "avito-kitchen/internal/domain/order"
)

// MenuItemLookup is the cart use-case's own view of the catalog: just
// enough to price-check and availability-check a cart line. It returns
// domain/catalog's own MenuItem type (allowed — usecase may depend on any
// domain package) rather than importing usecase/catalog, so this package
// never depends on another module's use-case layer, only on domain
// (PROMPT.md 6.2). adapter/postgres satisfies this with the same
// repository that backs catalog's own MenuRepository.
type MenuItemLookup interface {
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domaincatalog.MenuItem, error)
	// GetMany batches a lookup for several ids in one query (PROMPT.md
	// 6.6: no per-line query when assembling a cart's live availability).
	// Ids that do not exist are simply absent from the result map.
	GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error)

	// LockForCheckout is checkout's hard availability/price check
	// (PROMPT.md 5.2): it locks the given menu items FOR UPDATE and
	// returns their state as of that lock, always acquiring the locks in
	// ascending id order regardless of the order ids is given in
	// (PROMPT.md 9 — this is what keeps two concurrent checkouts that
	// share items from deadlocking on each other). Must only be called
	// inside a CheckoutService.txManager.WithinTx, or the lock is released
	// the moment the call returns and protects nothing. Ids that do not
	// exist are simply absent from the result.
	LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error)

	// DecrementStock reduces a finite-stock item's stock by quantity.
	// changed is false for an unlimited-stock item (StockQty == nil, never
	// decremented) or an id that no longer exists. Must run against a row
	// already locked by LockForCheckout in the same transaction, or the
	// read-then-write it performs is not protected from a concurrent
	// checkout doing the same thing.
	DecrementStock(ctx context.Context, id uuid.UUID, quantity int) (changed bool, err error)
}

// VenueLookup is checkout's own narrow view of a venue: enough to enforce
// its business rules and to keep the venue's ETag fresh when a checkout
// changes its stock.
type VenueLookup interface {
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error)
	// BumpMenuVersion increases venues.menu_version by one. Call it
	// whenever a checkout actually changes stock (PROMPT.md 6.6: the
	// version must move in the same transaction as anything that changes
	// what the menu shows, or a client's cached ETag stays valid for a
	// menu that just changed).
	BumpMenuVersion(ctx context.Context, id uuid.UUID) error
}

// OrderRepository persists and retrieves orders.
type OrderRepository interface {
	// Create inserts the order, its items, and its History entries.
	Create(ctx context.Context, o *domainorder.Order) error
	// Get returns nil, nil if id does not exist. Used both to answer
	// GetOrder and to replay an order on an idempotent checkout retry.
	Get(ctx context.Context, id uuid.UUID) (*domainorder.Order, error)
}

// IdempotencyClaim is the result of trying to claim an idempotency key for
// a checkout.
type IdempotencyClaim struct {
	// Claimed is true if this call now owns (clientID, key): no one has
	// used it before. The caller should proceed with a normal checkout
	// using the orderID it just passed to Claim.
	Claimed bool
	// HashMatches is only meaningful when !Claimed: does the stored
	// request match the one being retried right now?
	HashMatches bool
	// OrderID is the order already linked to this key, when !Claimed &&
	// HashMatches. Zero otherwise.
	OrderID uuid.UUID
}

// IdempotencyRepository backs the Idempotency-Key mechanism (PROMPT.md
// 5.2): a repeat of the same request must return the same order instead of
// creating a second one, and the same key with a different request body is
// a conflict.
type IdempotencyRepository interface {
	// Claim tries to atomically own (clientID, key), storing requestHash so
	// a future retry can tell whether it is the same request. Must run
	// inside the same transaction as the rest of checkout: if that
	// transaction rolls back (the checkout failed for any reason), the
	// claim rolls back with it, and the key is free to retry — a failed
	// attempt must never permanently burn a key.
	//
	// order_id starts NULL and is filled in by LinkOrder once an order
	// actually exists — idempotency_keys.order_id has a (non-deferrable)
	// foreign key to orders, so it cannot be set to an order's id before
	// that order's own INSERT has run, even inside the same transaction.
	//
	// expiresAt is recorded for a future cleanup job (PROMPT.md does not
	// ask for one yet, so none exists) — nothing reads it back today, so
	// an "expired" key still counts as used.
	Claim(ctx context.Context, clientID uuid.UUID, key, requestHash string, expiresAt time.Time) (IdempotencyClaim, error)

	// LinkOrder records that (clientID, key) resulted in orderID. Call it
	// right before committing the transaction Claim ran in — a key must
	// never be observable by another transaction as claimed but linked to
	// nothing, since that is indistinguishable from "still being
	// processed" and this project has no polling/waiting story for that.
	LinkOrder(ctx context.Context, clientID uuid.UUID, key string, orderID uuid.UUID) error
}

// CartRepository persists a client's cart as a whole. Save replaces the
// entire item set atomically rather than patching individual rows — see
// adapter/postgres's implementation for why that is the simpler and
// therefore preferred choice for a cart's size.
type CartRepository interface {
	// Get returns nil, nil if clientID has no cart yet.
	Get(ctx context.Context, clientID uuid.UUID) (*domainorder.Cart, error)
	Save(ctx context.Context, cart *domainorder.Cart) error
	// Clear deletes the cart (and its items) entirely. A no-op if there is
	// no cart for clientID.
	Clear(ctx context.Context, clientID uuid.UUID) error
}
