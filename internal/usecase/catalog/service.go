package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
)

// Service implements the catalog use-cases on top of the ports declared in
// ports.go.
type Service struct {
	venues VenueRepository
	menus  MenuRepository
}

// NewService builds a Service. now is not a dependency here: each method
// takes its own snapshot of time.Now() so a single request filters and
// displays against one consistent instant.
func NewService(venues VenueRepository, menus MenuRepository) *Service {
	return &Service{venues: venues, menus: menus}
}

// VenueView is a venue plus the display fields computed from it and "now"
// (domaincatalog.Venue.IsOpen/NextOpensAt) — kept out of the domain type
// itself since they are a function of time, not stored state.
type VenueView struct {
	domaincatalog.Venue
	IsOpen      bool
	NextOpensAt *time.Time
}

func newVenueView(v domaincatalog.Venue, now time.Time) VenueView {
	view := VenueView{Venue: v, IsOpen: v.IsOpen(now)}
	if !view.IsOpen {
		view.NextOpensAt = v.NextOpensAt(now)
	}
	return view
}

// VenueListResult is ListVenues' output.
type VenueListResult struct {
	Items      []VenueView
	NextCursor string
}

// ListVenues returns a page of venues matching filter, per PROMPT.md 5.1
// item 1 (cuisine, "open now", name search, pagination).
func (s *Service) ListVenues(ctx context.Context, filter ListVenuesFilter) (VenueListResult, error) {
	now := time.Now()
	page, err := s.venues.List(ctx, filter, now)
	if err != nil {
		return VenueListResult{}, fmt.Errorf("list venues: %w", err)
	}

	items := make([]VenueView, len(page.Items))
	for i, v := range page.Items {
		items[i] = newVenueView(v, now)
	}
	return VenueListResult{Items: items, NextCursor: page.NextCursor}, nil
}

// GetVenue returns the venue card (PROMPT.md 5.1 item 2): details plus its
// weekly schedule. Returns errs.CodeNotFound if id does not exist.
func (s *Service) GetVenue(ctx context.Context, id uuid.UUID) (VenueView, error) {
	v, err := s.venues.GetByID(ctx, id)
	if err != nil {
		return VenueView{}, fmt.Errorf("get venue: %w", err)
	}
	if v == nil {
		return VenueView{}, errs.New(errs.CodeNotFound, "venue not found")
	}
	return newVenueView(*v, time.Now()), nil
}

// MenuResult is GetMenu's output. When NotModified is true, Menu is the
// zero value and the caller must respond 304 with no body — the whole
// point of the fast path is never assembling Menu in that case.
type MenuResult struct {
	Menu        Menu
	ETag        string
	NotModified bool
}

// GetMenu implements the conditional-request flow from PROMPT.md 6.6: check
// the version with a single primary-key lookup, and only assemble the full
// menu if the caller's ifNoneMatch does not already match it. ifNoneMatch
// is the raw If-None-Match header value, or "" if absent.
func (s *Service) GetMenu(ctx context.Context, venueID uuid.UUID, ifNoneMatch string) (MenuResult, error) {
	version, err := s.menus.GetMenuVersion(ctx, venueID)
	if err != nil {
		return MenuResult{}, fmt.Errorf("get menu version: %w", err)
	}

	etag := BuildETag(venueID, version)
	if ifNoneMatch != "" && ifNoneMatch == etag {
		return MenuResult{ETag: etag, NotModified: true}, nil
	}

	menu, err := s.menus.GetMenu(ctx, venueID)
	if err != nil {
		return MenuResult{}, fmt.Errorf("get menu: %w", err)
	}
	return MenuResult{Menu: menu, ETag: etag}, nil
}

// BuildETag derives an ETag from venue_id and menu_version, per PROMPT.md
// 6.6 — a strong ETag, since the value is byte-for-byte determined by the
// menu's actual content, not merely close to it.
func BuildETag(venueID uuid.UUID, menuVersion int64) string {
	return fmt.Sprintf(`"%s-%d"`, venueID, menuVersion)
}
