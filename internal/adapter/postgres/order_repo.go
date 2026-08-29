package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// Create inserts o, its items, and every entry already in o.History (at
// minimum the one Order.New itself adds for the initial "created" state).
// All three inserts run through QuerierFromContext, so whatever transaction
// CheckoutService.PlaceOrder opened (stock already decremented in it) is
// what they land in — an order is never visible without the stock it
// consumed also being gone, or vice versa.
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

	for _, change := range o.History {
		var fromStatus *string
		if change.From != nil {
			s := string(*change.From)
			fromStatus = &s
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, uuid.New(), o.ID, fromStatus, string(change.To), string(change.Actor), change.CreatedAt); err != nil {
			return fmt.Errorf("insert order status history: %w", err)
		}
	}

	return nil
}

// Get returns nil, nil if id does not exist.
func (r *OrderRepository) Get(ctx context.Context, id uuid.UUID) (*domainorder.Order, error) {
	q := QuerierFromContext(ctx, r.pool)

	var o domainorder.Order
	var status string
	var rejectionReason, externalOrderID *string
	err := q.QueryRow(ctx, `
		SELECT id, client_id, venue_id, status, delivery_address, customer_phone, comment,
		       eta_minutes, rejection_reason, external_order_id, created_at, updated_at
		FROM orders WHERE id = $1
	`, id).Scan(&o.ID, &o.ClientID, &o.VenueID, &status, &o.DeliveryAddress, &o.CustomerPhone, &o.Comment,
		&o.ETAMinutes, &rejectionReason, &externalOrderID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query order: %w", err)
	}
	o.Status = domainorder.Status(status)
	if rejectionReason != nil {
		o.RejectionReason = *rejectionReason
	}
	if externalOrderID != nil {
		o.ExternalOrderID = *externalOrderID
	}

	itemRows, err := q.Query(ctx, `
		SELECT id, menu_item_id, rescue_offer_id, name_snapshot, unit_price_minor, quantity
		FROM order_items WHERE order_id = $1
		ORDER BY name_snapshot
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query order items: %w", err)
	}
	for itemRows.Next() {
		var item domainorder.Item
		if err := itemRows.Scan(&item.ID, &item.MenuItemID, &item.RescueOfferID, &item.NameSnapshot, &item.UnitPriceMinor, &item.Quantity); err != nil {
			itemRows.Close()
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		o.Items = append(o.Items, item)
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}

	historyRows, err := q.Query(ctx, `
		SELECT from_status, to_status, actor, created_at
		FROM order_status_history WHERE order_id = $1
		ORDER BY created_at
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query order status history: %w", err)
	}
	for historyRows.Next() {
		var change domainorder.StatusChange
		var fromStatus *string
		var toStatus, actor string
		if err := historyRows.Scan(&fromStatus, &toStatus, &actor, &change.CreatedAt); err != nil {
			historyRows.Close()
			return nil, fmt.Errorf("scan order status history: %w", err)
		}
		if fromStatus != nil {
			s := domainorder.Status(*fromStatus)
			change.From = &s
		}
		change.To = domainorder.Status(toStatus)
		change.Actor = domainorder.Actor(actor)
		o.History = append(o.History, change)
	}
	historyRows.Close()
	if err := historyRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order status history: %w", err)
	}

	return &o, nil
}
