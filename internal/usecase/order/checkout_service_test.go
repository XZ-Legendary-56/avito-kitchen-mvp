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

func TestPlaceOrder_EmptyCart(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	tx := NewMockTxManager(ctrl)

	clientID := uuid.New()
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)
	// No WithinTx expectation: an empty cart must fail before any
	// transaction is opened.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000))

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(nil, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000))

	venue := alwaysOpenVenue(venueID, 0)
	venue.AcceptingOrders = false
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	// LockForCheckout must not be called: the venue check fails first.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 2, 10000))

	venue := alwaysOpenVenue(venueID, 15000)
	stock := 5
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

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
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	o, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "no onions")

	require.NoError(t, err)
	assert.Equal(t, int64(20000), o.TotalMinor())
}

func TestPlaceOrder_PriceChanged_NoStockOrOrderSideEffects(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	orders := NewMockOrderRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 12000, IsAvailable: true}

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	// No DecrementStock, BumpMenuVersion, Create or Clear: a failed
	// checkout must have zero side effects.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 3, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	stock := 1
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 50000) // minimum far above this cart's total
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true}

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	// DecrementStock must not be called: the order is rejected before
	// anything is written.

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

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
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Soup of the day", PriceMinor: 10000, IsAvailable: true, StockQty: nil}

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 1).Return(false, nil)
	// BumpMenuVersion must not be called: nothing about the menu actually
	// changed for an unlimited-stock item.
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, orders, tx)
	_, err := svc.PlaceOrder(context.Background(), clientID, "addr", "+70000000000", "")

	require.NoError(t, err)
}
