package order_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"avito-kitchen/internal/domain/errs"
	"avito-kitchen/internal/domain/order"
)

func TestCart_AddItem_NewLine(t *testing.T) {
	venueID, itemID := uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)

	err := cart.AddItem(venueID, itemID, 2, 10000)

	require.NoError(t, err)
	require.Len(t, cart.Items, 1)
	assert.Equal(t, order.CartItem{MenuItemID: itemID, Quantity: 2, PriceMinorSnapshot: 10000}, cart.Items[0])
}

func TestCart_AddItem_SameItemAccumulatesQuantity(t *testing.T) {
	venueID, itemID := uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)

	require.NoError(t, cart.AddItem(venueID, itemID, 2, 10000))
	require.NoError(t, cart.AddItem(venueID, itemID, 3, 10000))

	require.Len(t, cart.Items, 1, "adding the same menu item twice must not create a second line")
	assert.Equal(t, 5, cart.Items[0].Quantity)
}

func TestCart_AddItem_DifferentVenueRejected(t *testing.T) {
	cart := order.NewCart(uuid.New(), uuid.New())

	err := cart.AddItem(uuid.New(), uuid.New(), 1, 10000)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartVenueMismatch, code)
	assert.Empty(t, cart.Items, "a rejected add must not partially modify the cart")
}

func TestCart_TotalMinor_SumsLines(t *testing.T) {
	venueID := uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	require.NoError(t, cart.AddItem(venueID, uuid.New(), 2, 10000)) // 20000
	require.NoError(t, cart.AddItem(venueID, uuid.New(), 1, 5000))  // 5000

	assert.Equal(t, int64(25000), cart.TotalMinor())
}

func TestCart_EnsureNotEmpty(t *testing.T) {
	empty := order.NewCart(uuid.New(), uuid.New())
	err := empty.EnsureNotEmpty()
	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)

	venueID := uuid.New()
	nonEmpty := order.NewCart(uuid.New(), venueID)
	require.NoError(t, nonEmpty.AddItem(venueID, uuid.New(), 1, 1000))
	assert.NoError(t, nonEmpty.EnsureNotEmpty())
}
