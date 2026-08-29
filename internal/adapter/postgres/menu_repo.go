package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	catalogusecase "avito-kitchen/internal/usecase/catalog"
	orderusecase "avito-kitchen/internal/usecase/order"
)

// MenuRepository implements catalogusecase.MenuRepository (menu structure
// and versioning) and orderusecase.MenuItemLookup (single-item lookups for
// the cart). Both ports read the same menu_items/menu_categories tables —
// one repository backing two usecase-declared interfaces is exactly the
// point of PROMPT.md 6.2's rule: each module owns the shape of the port it
// needs, and an adapter is free to satisfy several of them at once.
type MenuRepository struct {
	pool *pgxpool.Pool
}

func NewMenuRepository(pool *pgxpool.Pool) *MenuRepository {
	return &MenuRepository{pool: pool}
}

var (
	_ catalogusecase.MenuRepository = (*MenuRepository)(nil)
	_ orderusecase.MenuItemLookup   = (*MenuRepository)(nil)
)

// GetMenuVersion is the cheap half of PROMPT.md 6.6's ETag fast path: one
// primary-key lookup, no join, so a 304 costs almost nothing.
func (r *MenuRepository) GetMenuVersion(ctx context.Context, venueID uuid.UUID) (int64, error) {
	q := QuerierFromContext(ctx, r.pool)

	var version int64
	err := q.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errs.New(errs.CodeNotFound, "venue not found")
	}
	if err != nil {
		return 0, fmt.Errorf("query menu version: %w", err)
	}
	return version, nil
}

// GetMenu assembles the full menu in three fixed queries — venue, then all
// its categories, then all its items — never one query per category or
// item (PROMPT.md 6.6).
func (r *MenuRepository) GetMenu(ctx context.Context, venueID uuid.UUID) (catalogusecase.Menu, error) {
	q := QuerierFromContext(ctx, r.pool)

	var version int64
	err := q.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogusecase.Menu{}, errs.New(errs.CodeNotFound, "venue not found")
	}
	if err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("query venue: %w", err)
	}

	catRows, err := q.Query(ctx, `
		SELECT id, name, position
		FROM menu_categories
		WHERE venue_id = $1
		ORDER BY position, name
	`, venueID)
	if err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("query menu categories: %w", err)
	}

	categories := make(map[uuid.UUID]*catalogusecase.MenuCategory)
	var order []uuid.UUID
	for catRows.Next() {
		var c catalogusecase.MenuCategory
		if err := catRows.Scan(&c.ID, &c.Name, &c.Position); err != nil {
			catRows.Close()
			return catalogusecase.Menu{}, fmt.Errorf("scan menu category: %w", err)
		}
		categories[c.ID] = &c
		order = append(order, c.ID)
	}
	catRows.Close()
	if err := catRows.Err(); err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("iterate menu categories: %w", err)
	}

	itemRows, err := q.Query(ctx, `
		SELECT id, category_id, name, description, price_minor, is_available, stock_qty
		FROM menu_items
		WHERE venue_id = $1
		ORDER BY name
	`, venueID)
	if err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("query menu items: %w", err)
	}

	for itemRows.Next() {
		var mi domaincatalog.MenuItem
		var categoryID uuid.UUID
		if err := itemRows.Scan(&mi.ID, &categoryID, &mi.Name, &mi.Description, &mi.PriceMinor, &mi.IsAvailable, &mi.StockQty); err != nil {
			itemRows.Close()
			return catalogusecase.Menu{}, fmt.Errorf("scan menu item: %w", err)
		}
		mi.VenueID = venueID
		if cat, ok := categories[categoryID]; ok {
			cat.Items = append(cat.Items, mi)
		}
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("iterate menu items: %w", err)
	}

	menu := catalogusecase.Menu{VenueID: venueID, MenuVersion: version}
	for _, id := range order {
		menu.Categories = append(menu.Categories, *categories[id])
	}
	return menu, nil
}

// Get returns nil, nil if id does not exist.
func (r *MenuRepository) Get(ctx context.Context, id uuid.UUID) (*domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)

	var mi domaincatalog.MenuItem
	err := q.QueryRow(ctx, `
		SELECT id, venue_id, name, description, price_minor, is_available, stock_qty
		FROM menu_items WHERE id = $1
	`, id).Scan(&mi.ID, &mi.VenueID, &mi.Name, &mi.Description, &mi.PriceMinor, &mi.IsAvailable, &mi.StockQty)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query menu item: %w", err)
	}
	return &mi, nil
}

// GetMany batches a lookup for several ids in one query.
func (r *MenuRepository) GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domaincatalog.MenuItem{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		SELECT id, venue_id, name, description, price_minor, is_available, stock_qty
		FROM menu_items WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.MenuItem, len(ids))
	for rows.Next() {
		var mi domaincatalog.MenuItem
		if err := rows.Scan(&mi.ID, &mi.VenueID, &mi.Name, &mi.Description, &mi.PriceMinor, &mi.IsAvailable, &mi.StockQty); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		result[mi.ID] = mi
	}
	return result, rows.Err()
}

// LockForCheckout locks menu_items rows FOR UPDATE, scanning them in
// ascending id order via ORDER BY. Postgres acquires FOR UPDATE locks in
// the order rows are produced by the query, so two concurrent checkouts
// that share items — however their carts happened to list them — always
// request their locks in the same relative order as each other. One
// blocks and waits for the other; neither can end up waiting on a lock the
// other is itself waiting on, which is what a deadlock would require
// (PROMPT.md 9). Rows for ids that do not exist are simply absent from the
// result, same as Get/GetMany.
func (r *MenuRepository) LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domaincatalog.MenuItem{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		SELECT id, venue_id, name, description, price_minor, is_available, stock_qty
		FROM menu_items
		WHERE id = ANY($1)
		ORDER BY id
		FOR UPDATE
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("lock menu items: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.MenuItem, len(ids))
	for rows.Next() {
		var mi domaincatalog.MenuItem
		if err := rows.Scan(&mi.ID, &mi.VenueID, &mi.Name, &mi.Description, &mi.PriceMinor, &mi.IsAvailable, &mi.StockQty); err != nil {
			return nil, fmt.Errorf("scan locked menu item: %w", err)
		}
		result[mi.ID] = mi
	}
	return result, rows.Err()
}

// DecrementStock reduces a finite-stock item's stock_qty by quantity.
// "AND stock_qty IS NOT NULL" makes an unlimited-stock item a no-op by
// construction — there is no separate Go branch that has to remember to
// skip it. This must run against a row already locked by LockForCheckout
// in the same transaction: on its own, "read stock, check it, then write
// stock" is a classic race, and the lock is what turns it into a single
// atomic step from every other transaction's point of view.
func (r *MenuRepository) DecrementStock(ctx context.Context, id uuid.UUID, quantity int) (bool, error) {
	q := QuerierFromContext(ctx, r.pool)
	tag, err := q.Exec(ctx, `
		UPDATE menu_items
		SET stock_qty = stock_qty - $2, updated_at = now()
		WHERE id = $1 AND stock_qty IS NOT NULL
	`, id, quantity)
	if err != nil {
		return false, fmt.Errorf("decrement stock: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
