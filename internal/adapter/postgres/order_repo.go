package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domainorder "avito-kitchen/internal/domain/order"
	orderusecase "avito-kitchen/internal/usecase/order"
)

// OrderRepository implements orderusecase.OrderRepository on orders,
// order_items and order_status_history.
type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

var _ orderusecase.OrderRepository = (*OrderRepository)(nil)

// Create inserts o, its items, and one order_status_history row for the
// initial "created" transition. All three inserts run through
// QuerierFromContext, so whatever transaction CheckoutService.PlaceOrder
// opened (stock already decremented in it) is what they land in — an order
// is never visible without the stock it consumed also being gone, or vice
// versa.
func (r *OrderRepository) Create(ctx context.Context, o *domainorder.Order) error {
	q := QuerierFromContext(ctx, r.pool)

	if _, err := q.Exec(ctx, `
		INSERT INTO orders (
			id, client_id, venue_id, status, total_minor,
			delivery_address, customer_phone, comment, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, o.ID, o.ClientID, o.VenueID, string(o.Status), o.TotalMinor(),
		o.DeliveryAddress, o.CustomerPhone, o.Comment, o.CreatedAt, o.UpdatedAt); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for _, item := range o.Items {
		if _, err := q.Exec(ctx, `
			INSERT INTO order_items (
				id, order_id, menu_item_id, rescue_offer_id, name_snapshot, unit_price_minor, quantity
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, item.ID, o.ID, item.MenuItemID, item.RescueOfferID, item.NameSnapshot, item.UnitPriceMinor, item.Quantity); err != nil {
			return fmt.Errorf("insert order item %s: %w", item.MenuItemID, err)
		}
	}

	if _, err := q.Exec(ctx, `
		INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, created_at)
		VALUES ($1, $2, NULL, $3, $4, $5)
	`, uuid.New(), o.ID, string(o.Status), string(domainorder.ActorCustomer), o.CreatedAt); err != nil {
		return fmt.Errorf("insert order status history: %w", err)
	}

	return nil
}
