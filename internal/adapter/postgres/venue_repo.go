package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	catalogusecase "avito-kitchen/internal/usecase/catalog"
)

const defaultVenuePageLimit = 20

// VenueRepository implements catalogusecase.VenueRepository on venues and
// venue_schedules.
type VenueRepository struct {
	pool *pgxpool.Pool
}

func NewVenueRepository(pool *pgxpool.Pool) *VenueRepository {
	return &VenueRepository{pool: pool}
}

var _ catalogusecase.VenueRepository = (*VenueRepository)(nil)

// List returns a page of venues ordered by (name, id) — a stable tiebreak
// so keyset pagination never skips or repeats a row when two venues share
// a name. Every filter (cuisine, name prefix, "open now", cursor) is
// pushed into the WHERE clause: PROMPT.md 6.6 requires cursor pagination
// to stay correct, which is only possible if filtering happens in the same
// query as the LIMIT, not afterwards in Go.
func (r *VenueRepository) List(ctx context.Context, filter catalogusecase.ListVenuesFilter, now time.Time) (catalogusecase.VenuePage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = defaultVenuePageLimit
	}

	var cur *venueCursor
	if filter.Cursor != "" {
		c, err := decodeVenueCursor(filter.Cursor)
		if err != nil {
			return catalogusecase.VenuePage{}, err
		}
		cur = &c
	}

	var conditions []string
	var args []any

	if filter.Cuisine != "" {
		args = append(args, filter.Cuisine)
		conditions = append(conditions, fmt.Sprintf("cuisine = $%d", len(args)))
	}
	if filter.NamePrefix != "" {
		args = append(args, filter.NamePrefix)
		conditions = append(conditions, fmt.Sprintf("lower(name) LIKE lower($%d) || '%%'", len(args)))
	}
	if filter.OnlyOpen {
		// Mirrors domaincatalog.Venue.EnsureCanOrder in SQL: this is a
		// filter predicate that must run in the database for pagination to
		// stay correct (see the function comment), so the same "open and
		// accepting orders" rule necessarily exists in two places. The
		// value actually shown to the client (Venue.isOpen) still comes
		// from the one Go implementation, via newVenueView — this SQL only
		// decides which rows qualify, never what is displayed.
		args = append(args, isoWeekday(now.Weekday()))
		weekdayArg := len(args)
		args = append(args, now.Format("15:04:05"))
		timeArg := len(args)
		conditions = append(conditions, fmt.Sprintf(`accepting_orders AND EXISTS (
			SELECT 1 FROM venue_schedules vs
			WHERE vs.venue_id = v.id AND vs.weekday = $%d AND $%d::time >= vs.opens_at AND $%d::time < vs.closes_at
		)`, weekdayArg, timeArg, timeArg))
	}
	if cur != nil {
		args = append(args, cur.Name)
		nameArg := len(args)
		args = append(args, cur.ID)
		idArg := len(args)
		conditions = append(conditions, fmt.Sprintf("(name, id) > ($%d, $%d)", nameArg, idArg))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Fetch one extra row so its presence tells us whether there is a next
	// page, without a separate COUNT query.
	args = append(args, limit+1)
	limitArg := len(args)

	query := fmt.Sprintf(`
		SELECT id, partner_id, name, description, cuisine, min_order_amount_minor, accepting_orders
		FROM venues v
		%s
		ORDER BY name, id
		LIMIT $%d
	`, where, limitArg)

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return catalogusecase.VenuePage{}, fmt.Errorf("query venues: %w", err)
	}
	defer rows.Close()

	var venues []domaincatalog.Venue
	for rows.Next() {
		var v domaincatalog.Venue
		if err := rows.Scan(&v.ID, &v.PartnerID, &v.Name, &v.Description, &v.Cuisine, &v.MinOrderAmountMinor, &v.AcceptingOrders); err != nil {
			return catalogusecase.VenuePage{}, fmt.Errorf("scan venue: %w", err)
		}
		venues = append(venues, v)
	}
	if err := rows.Err(); err != nil {
		return catalogusecase.VenuePage{}, fmt.Errorf("iterate venues: %w", err)
	}

	var nextCursor string
	if len(venues) > limit {
		last := venues[limit-1]
		nextCursor = encodeVenueCursor(venueCursor{Name: last.Name, ID: last.ID})
		venues = venues[:limit]
	}

	if err := r.attachSchedules(ctx, venues); err != nil {
		return catalogusecase.VenuePage{}, err
	}

	return catalogusecase.VenuePage{Items: venues, NextCursor: nextCursor}, nil
}

// GetByID returns nil, nil if id does not exist.
func (r *VenueRepository) GetByID(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error) {
	q := QuerierFromContext(ctx, r.pool)

	var v domaincatalog.Venue
	err := q.QueryRow(ctx, `
		SELECT id, partner_id, name, description, cuisine, min_order_amount_minor, accepting_orders
		FROM venues WHERE id = $1
	`, id).Scan(&v.ID, &v.PartnerID, &v.Name, &v.Description, &v.Cuisine, &v.MinOrderAmountMinor, &v.AcceptingOrders)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query venue: %w", err)
	}

	venues := []domaincatalog.Venue{v}
	if err := r.attachSchedules(ctx, venues); err != nil {
		return nil, err
	}
	return &venues[0], nil
}

// attachSchedules loads every schedule row for venues in a single query
// (WHERE venue_id = ANY($1)) and appends each to its venue. PROMPT.md 6.6
// forbids a listing from turning into one query per row; this is how List
// and GetByID both honor that.
func (r *VenueRepository) attachSchedules(ctx context.Context, venues []domaincatalog.Venue) error {
	if len(venues) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(venues))
	index := make(map[uuid.UUID]int, len(venues))
	for i, v := range venues {
		ids[i] = v.ID
		index[v.ID] = i
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		SELECT venue_id, weekday, opens_at, closes_at
		FROM venue_schedules
		WHERE venue_id = ANY($1)
	`, ids)
	if err != nil {
		return fmt.Errorf("query venue schedules: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var venueID uuid.UUID
		var weekday int
		var opens, closes pgtype.Time
		if err := rows.Scan(&venueID, &weekday, &opens, &closes); err != nil {
			return fmt.Errorf("scan venue schedule: %w", err)
		}
		i, ok := index[venueID]
		if !ok {
			continue
		}
		venues[i].Schedule = append(venues[i].Schedule, domaincatalog.ScheduleEntry{
			Weekday:  goWeekday(weekday),
			OpensAt:  time.Duration(opens.Microseconds) * time.Microsecond,
			ClosesAt: time.Duration(closes.Microseconds) * time.Microsecond,
		})
	}
	return rows.Err()
}
