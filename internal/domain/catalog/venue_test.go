package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
)

func openMondayNineToTen() catalog.Venue {
	return catalog.Venue{
		AcceptingOrders: true,
		Schedule: []catalog.ScheduleEntry{
			{Weekday: time.Monday, OpensAt: 9 * time.Hour, ClosesAt: 10 * time.Hour},
		},
	}
}

func TestVenue_EnsureCanOrder_OpenAndAccepting(t *testing.T) {
	v := openMondayNineToTen()
	assert.NoError(t, v.EnsureCanOrder(at(time.Monday, 9, 30)))
}

func TestVenue_EnsureCanOrder_NotAccepting(t *testing.T) {
	v := openMondayNineToTen()
	v.AcceptingOrders = false

	err := v.EnsureCanOrder(at(time.Monday, 9, 30))

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeVenueNotAcceptingOrders, code)
}

func TestVenue_EnsureCanOrder_NotAcceptingTakesPriorityOverClosed(t *testing.T) {
	// Both problems apply at once: the caller only needs one code to show
	// the customer, and "not accepting orders" is checked first.
	v := openMondayNineToTen()
	v.AcceptingOrders = false

	err := v.EnsureCanOrder(at(time.Tuesday, 9, 30))

	code, _ := errs.CodeOf(err)
	assert.Equal(t, errs.CodeVenueNotAcceptingOrders, code)
}

func TestVenue_EnsureCanOrder_Closed(t *testing.T) {
	v := openMondayNineToTen()

	err := v.EnsureCanOrder(at(time.Tuesday, 9, 30))

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeVenueClosed, code)
}

func TestVenue_EnsureMinOrderReached(t *testing.T) {
	v := catalog.Venue{MinOrderAmountMinor: 70000}

	assert.NoError(t, v.EnsureMinOrderReached(70000), "meeting the minimum exactly must pass")
	assert.NoError(t, v.EnsureMinOrderReached(80000))

	err := v.EnsureMinOrderReached(40000)
	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeMinOrderAmountNotReached, code)
}
