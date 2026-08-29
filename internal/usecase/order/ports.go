// Package order holds the cart use-cases (PROMPT.md 6.3: Cart lives in the
// "order" domain module, alongside Order itself) and the ports they declare
// for themselves.
package order

import (
	"context"

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

// OrderRepository persists a freshly placed order.
type OrderRepository interface {
	// Create inserts the order, its items, and one order_status_history
	// row recording the initial "created" transition (actor: customer).
	Create(ctx context.Context, o *domainorder.Order) error
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
