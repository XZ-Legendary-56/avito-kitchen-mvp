package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	domainorder "avito-kitchen/internal/domain/order"
	"avito-kitchen/internal/usecase/order"
)

const testIdempotencyKey = "test-idempotency-key"

// alwaysOpenVenue is open every hour of every day, so tests that are not
// about scheduling do not have to worry about what time they happen to run.
func alwaysOpenVenue(id uuid.UUID, minOrderAmountMinor int64) domaincatalog.Venue {
	var schedule []domaincatalog.ScheduleEntry
	for d := time.Sunday; d <= time.Saturday; d++ {
		schedule = append(schedule, domaincatalog.ScheduleEntry{Weekday: d, OpensAt: 0, ClosesAt: 24 * time.Hour})
	}
	return domaincatalog.Venue{
		ID:                  id,
		AcceptingOrders:     true,
		MinOrderAmountMinor: minOrderAmountMinor,
		Schedule:            schedule,
	}
}

// expectFreshClaim sets up idempotency.Claim to report a brand-new key —
// the common case for tests that are not about idempotency itself.
func expectFreshClaim(idem *MockIdempotencyRepository) {
	idem.EXPECT().
		Claim(gomock.Any(), gomock.Any(), testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: true}, nil)
}

func TestPlaceOrder_EmptyCart(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)
}

func TestPlaceOrder_VenueNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000))

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(nil, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestPlaceOrder_VenueNotAcceptingOrders(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000))

	venue := alwaysOpenVenue(venueID, 0)
	venue.AcceptingOrders = false
	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	// LockForCheckout must not be called: the venue check fails first.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeVenueNotAcceptingOrders, code)
}

func TestPlaceOrder_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 2, 10000))

	venue := alwaysOpenVenue(venueID, 15000)
	stock := 5
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 2).Return(true, nil)
	venues.EXPECT().BumpMenuVersion(gomock.Any(), venueID).Return(nil)
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, o *domainorder.Order) error {
		assert.Equal(t, int64(20000), o.TotalMinor())
		assert.Equal(t, domainorder.StatusCreated, o.Status)
		return nil
	})
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
	idem.EXPECT().LinkOrder(gomock.Any(), clientID, testIdempotencyKey, gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	o, replayed, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "no onions")

	require.NoError(t, err)
	assert.False(t, replayed, "a brand-new order must not be reported as a replay")
	assert.Equal(t, int64(20000), o.TotalMinor())
}

func TestPlaceOrder_PriceChanged_NoStockOrOrderSideEffects(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 12000, IsAvailable: true}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	// No DecrementStock, BumpMenuVersion, Create, LinkOrder or Clear: a
	// failed checkout must have zero side effects.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)
}

func TestPlaceOrder_InsufficientStock(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 3, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	stock := 1
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)
}

func TestPlaceOrder_MinOrderAmountNotReached_StockUntouched(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 50000) // minimum far above this cart's total
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	// DecrementStock must not be called: the order is rejected before
	// anything is written.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeMinOrderAmountNotReached, code)
}

func TestPlaceOrder_UnlimitedStockDoesNotBumpMenuVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Soup of the day", PriceMinor: 10000, IsAvailable: true, StockQty: nil}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 1).Return(false, nil)
	// BumpMenuVersion must not be called: nothing about the menu actually
	// changed for an unlimited-stock item.
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
	idem.EXPECT().LinkOrder(gomock.Any(), clientID, testIdempotencyKey, gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.NoError(t, err)
}

func TestPlaceOrder_IdempotentReplay_ReturnsSameOrderWithoutTouchingCartOrStock(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, existingOrderID := uuid.New(), uuid.New()
	existingOrder := &domainorder.Order{ID: existingOrderID, ClientID: clientID, Status: domainorder.StatusCreated}

	idem.EXPECT().
		Claim(gomock.Any(), clientID, testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: false, HashMatches: true, OrderID: existingOrderID}, nil)
	orders.EXPECT().Get(gomock.Any(), existingOrderID).Return(existingOrder, nil)
	// No CartRepository, VenueLookup, MenuItemLookup or LinkOrder calls at
	// all: a replay must not re-run any part of checkout, precisely
	// because the cart it would need is typically already gone (the
	// original, successful attempt cleared it).

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	o, replayed, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.NoError(t, err)
	assert.True(t, replayed, "a replayed order must be reported as such, for the HTTP layer to answer 200 instead of 201")
	assert.Same(t, existingOrder, o)
}

func TestPlaceOrder_SameKeyDifferentBody_Conflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	idem.EXPECT().
		Claim(gomock.Any(), clientID, testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: false, HashMatches: false}, nil)
	// No OrderRepository.Get either: a hash mismatch is a conflict, not a
	// lookup.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "a different address", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeIdempotencyKeyConflict, code)
}
