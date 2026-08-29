package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	"avito-kitchen/internal/usecase/catalog"
)

func TestListVenues_MapsPageAndComputesDisplayFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	page := catalog.VenuePage{
		Items: []domaincatalog.Venue{
			{ID: venueID, Name: "Pizza Mania", AcceptingOrders: false}, // no schedule -> never open
		},
		NextCursor: "next-page-token",
	}
	venues.EXPECT().
		List(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(page, nil)

	svc := catalog.NewService(venues, menus)

	result, err := svc.ListVenues(context.Background(), catalog.ListVenuesFilter{Cuisine: "Pizza"})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, venueID, result.Items[0].ID)
	assert.False(t, result.Items[0].IsOpen, "a venue with no schedule must never show as open")
	assert.Equal(t, "next-page-token", result.NextCursor)
}

func TestListVenues_PropagatesRepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	boom := errors.New("connection reset")
	venues.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(catalog.VenuePage{}, boom)

	svc := catalog.NewService(venues, menus)
	_, err := svc.ListVenues(context.Background(), catalog.ListVenuesFilter{})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestGetVenue_Found(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	v := &domaincatalog.Venue{ID: venueID, Name: "Pizza Mania"}
	venues.EXPECT().GetByID(gomock.Any(), venueID).Return(v, nil)

	svc := catalog.NewService(venues, menus)
	view, err := svc.GetVenue(context.Background(), venueID)

	require.NoError(t, err)
	assert.Equal(t, venueID, view.ID)
}

func TestGetVenue_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	venues.EXPECT().GetByID(gomock.Any(), venueID).Return(nil, nil)

	svc := catalog.NewService(venues, menus)
	_, err := svc.GetVenue(context.Background(), venueID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestGetMenu_CacheMissAssemblesFullMenu(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	menus.EXPECT().GetMenuVersion(gomock.Any(), venueID).Return(int64(3), nil)
	menu := catalog.Menu{VenueID: venueID, MenuVersion: 3}
	menus.EXPECT().GetMenu(gomock.Any(), venueID).Return(menu, nil)

	svc := catalog.NewService(venues, menus)
	result, err := svc.GetMenu(context.Background(), venueID, "")

	require.NoError(t, err)
	assert.False(t, result.NotModified)
	assert.Equal(t, catalog.BuildETag(venueID, 3), result.ETag)
	assert.Equal(t, menu, result.Menu)
}

func TestGetMenu_MatchingIfNoneMatchSkipsAssembly(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	menus.EXPECT().GetMenuVersion(gomock.Any(), venueID).Return(int64(3), nil)
	// GetMenu must NOT be called: that is the entire point of the ETag fast
	// path (PROMPT.md 6.6) — a matching If-None-Match must cost one
	// primary-key lookup, never the full assembly. gomock fails the test if
	// an unexpected call happens, so simply not setting an expectation for
	// GetMenu is the assertion.

	svc := catalog.NewService(venues, menus)
	etag := catalog.BuildETag(venueID, 3)
	result, err := svc.GetMenu(context.Background(), venueID, etag)

	require.NoError(t, err)
	assert.True(t, result.NotModified)
	assert.Equal(t, etag, result.ETag)
}

func TestGetMenu_StaleIfNoneMatchStillAssembles(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	menus.EXPECT().GetMenuVersion(gomock.Any(), venueID).Return(int64(4), nil)
	menus.EXPECT().GetMenu(gomock.Any(), venueID).Return(catalog.Menu{VenueID: venueID, MenuVersion: 4}, nil)

	svc := catalog.NewService(venues, menus)
	staleETag := catalog.BuildETag(venueID, 3) // client's cached copy is one version behind
	result, err := svc.GetMenu(context.Background(), venueID, staleETag)

	require.NoError(t, err)
	assert.False(t, result.NotModified)
}

func TestGetMenu_VenueNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	venueID := uuid.New()
	menus.EXPECT().GetMenuVersion(gomock.Any(), venueID).Return(int64(0), errs.New(errs.CodeNotFound, "venue not found"))

	svc := catalog.NewService(venues, menus)
	_, err := svc.GetMenu(context.Background(), venueID, "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestBuildETag_ChangesWithVersion(t *testing.T) {
	venueID := uuid.New()
	assert.NotEqual(t, catalog.BuildETag(venueID, 1), catalog.BuildETag(venueID, 2))
}

// TestListVenues_UsesOneConsistentNow guards against a regression where
// filtering and display would read time.Now() twice and disagree with each
// other under an unlucky scheduling gap.
func TestListVenues_UsesOneConsistentNow(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	menus := NewMockMenuRepository(ctrl)

	var capturedNow time.Time
	venues.EXPECT().
		List(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ catalog.ListVenuesFilter, now time.Time) (catalog.VenuePage, error) {
			capturedNow = now
			return catalog.VenuePage{}, nil
		})

	svc := catalog.NewService(venues, menus)
	before := time.Now()
	_, err := svc.ListVenues(context.Background(), catalog.ListVenuesFilter{})
	after := time.Now()

	require.NoError(t, err)
	assert.False(t, capturedNow.Before(before) || capturedNow.After(after),
		"the now passed to the repository must be within this call's own window")
}
