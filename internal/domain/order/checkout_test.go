package order_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	"avito-kitchen/internal/domain/order"
)

// okLine builds a CheckoutLine that passes every check, using catalog's own
// EnsureAvailable/EnsurePriceUnchanged so the test only fabricates a line
// that would genuinely be considered fine, not just Err: nil by fiat.
func okLine(name string, quantity int, priceMinor int64) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, PriceMinor: priceMinor, IsAvailable: true}
	err := mi.EnsureAvailable(quantity)
	if err == nil {
		err = mi.EnsurePriceUnchanged(priceMinor)
	}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, UnitPriceMinor: priceMinor, Err: err}
}

func priceChangedLine(name string, quantity int, oldPrice, newPrice int64) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, PriceMinor: newPrice, IsAvailable: true}
	return order.CheckoutLine{
		MenuItemID: mi.ID, Name: name, Quantity: quantity, UnitPriceMinor: newPrice,
		Err: mi.EnsurePriceUnchanged(oldPrice),
	}
}

func unavailableLine(name string, quantity int) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, IsAvailable: false}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, Err: mi.EnsureAvailable(quantity)}
}

func insufficientStockLine(name string, quantity, available int) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, IsAvailable: true, StockQty: &available}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, Err: mi.EnsureAvailable(quantity)}
}

func TestCheckout_EmptyCart(t *testing.T) {
	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), nil, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)
}

func TestCheckout_AllLinesClean_BuildsOrder(t *testing.T) {
	lines := []order.CheckoutLine{
		okLine("Margherita", 2, 45900),
		okLine("Cola", 1, 15900),
	}
	now := time.Now()
	orderID, clientID, venueID := uuid.New(), uuid.New(), uuid.New()

	o, err := order.Checkout(orderID, clientID, venueID, lines, "addr", "+70000000000", "no onions", now)

	require.NoError(t, err)
	assert.Equal(t, orderID, o.ID)
	assert.Equal(t, order.StatusCreated, o.Status)
	require.Len(t, o.Items, 2)
	assert.Equal(t, int64(45900*2+15900), o.TotalMinor())
	for i, l := range lines {
		assert.Equal(t, l.MenuItemID, o.Items[i].MenuItemID)
		assert.Equal(t, l.Name, o.Items[i].NameSnapshot)
		assert.Equal(t, l.UnitPriceMinor, o.Items[i].UnitPriceMinor)
		assert.NotEqual(t, uuid.Nil, o.Items[i].ID, "each order line must get its own fresh id")
	}
}

func TestCheckout_PriceChanged_ReportsEveryMismatchedLine(t *testing.T) {
	lines := []order.CheckoutLine{
		priceChangedLine("Margherita", 1, 45900, 49900),
		okLine("Cola", 1, 15900),
		priceChangedLine("Pepperoni", 1, 55900, 59900),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)

	domainErr, ok := errs.As(err)
	require.True(t, ok)
	assert.Len(t, domainErr.Details, 2, "both mismatched lines must be reported, not just the first")
}

func TestCheckout_PriceChangeOutranksStockShortage(t *testing.T) {
	// Both problems exist at once; the customer needs to fix the price
	// issue regardless of stock, so only PRICE_CHANGED should come back.
	lines := []order.CheckoutLine{
		priceChangedLine("Margherita", 1, 45900, 49900),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)
}

func TestCheckout_UnavailableOutranksInsufficientStock(t *testing.T) {
	lines := []order.CheckoutLine{
		unavailableLine("Margherita", 1),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeItemUnavailable, code)
}

func TestCheckout_InsufficientStockAlone(t *testing.T) {
	lines := []order.CheckoutLine{
		okLine("Margherita", 1, 45900),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)

	domainErr, ok := errs.As(err)
	require.True(t, ok)
	require.Len(t, domainErr.Details, 1)
	assert.Equal(t, 5, domainErr.Details[0]["requested"])
	assert.Equal(t, 1, domainErr.Details[0]["available"])
}

func TestCheckout_UnexpectedLineErrorFailsFast(t *testing.T) {
	boom := errors.New("database is on fire")
	lines := []order.CheckoutLine{
		okLine("Margherita", 1, 45900),
		{MenuItemID: uuid.New(), Name: "Cola", Quantity: 1, Err: boom},
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}
