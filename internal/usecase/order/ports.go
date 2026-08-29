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
