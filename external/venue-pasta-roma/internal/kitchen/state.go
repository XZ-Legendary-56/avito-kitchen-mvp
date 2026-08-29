// Package kitchen holds everything that makes this emulator behave like a
// real venue's own back-office system: its menu, its stock, the orders it
// has taken, and the HTTP surface ("provides its own API", PROMPT.md 8.2)
// a real kitchen would use to see and manage them.
package kitchen

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// MenuItem is this venue's own view of one dish. ExternalID is how this
// service names it (chosen before the platform ever heard of it);
// PlatformID is what the platform calls the same dish, learned from the
// PUT /menu sync response. order.created webhooks only ever carry
// PlatformID, so that is the key stock is actually indexed by.
type MenuItem struct {
	PlatformID uuid.UUID
	ExternalID string
	Name       string
	StockQty   int
}

// OrderItem is one line of a tracked order — enough to give stock back if
// the order is later cancelled.
type OrderItem struct {
	PlatformMenuItemID uuid.UUID
	Name               string
	Quantity           int
}

// Order is this emulator's own record of one order, driven by whatever the
// order.created webhook said plus this service's own accept/reject
// decision. Status mirrors the platform's OrderStatus values so GET
// /kitchen/orders reads naturally next to the platform's own order view.
type Order struct {
	ID              uuid.UUID
	Status          string
	Items           []OrderItem
	DeliveryAddress string
	Accepted        bool
	RejectionReason string
	// NextAdvanceAt is when the status ticker should move this order to its
	// next status. Zero once the order reaches a terminal status.
	NextAdvanceAt time.Time
}

// State is this service's entire memory — no database, per PROMPT.md 8.1:
// a real small restaurant's own till is not PostgreSQL either, and nothing
// here needs to survive a restart to make the point this service exists to
// make (see the package's own README section for that argument in full).
type State struct {
	mu sync.Mutex

	menuByPlatformID map[uuid.UUID]*MenuItem
	menuByExternalID map[string]uuid.UUID

	orders map[uuid.UUID]*Order

	// seenEvents dedupes inbound webhooks by X-Event-Id (PROMPT.md 7.4: the
	// receiver, not the sender, is responsible for this). It only ever
	// grows for the life of the process — there is no expiry — which is
	// fine for a demo service that lives for one docker-compose run, not a
	// design this project would keep for something long-lived.
	seenEvents map[uuid.UUID]struct{}
}

func NewState() *State {
	return &State{
		menuByPlatformID: make(map[uuid.UUID]*MenuItem),
		menuByExternalID: make(map[string]uuid.UUID),
		orders:           make(map[uuid.UUID]*Order),
		seenEvents:       make(map[uuid.UUID]struct{}),
	}
}

// SetMenu replaces the tracked menu wholesale — called once, right after
// the startup sync (see menu.go), so PlatformID is already known for every
// item.
func (s *State) SetMenu(items []MenuItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.menuByPlatformID = make(map[uuid.UUID]*MenuItem, len(items))
	s.menuByExternalID = make(map[string]uuid.UUID, len(items))
	for i := range items {
		item := items[i]
		s.menuByPlatformID[item.PlatformID] = &item
		s.menuByExternalID[item.ExternalID] = item.PlatformID
	}
}

// CheckAndReserve reports whether every line in items has enough stock, and
// if so decrements it right away — "reserve on accept" is what lets a
// second order arriving a moment later see the true remaining count rather
// than a stale one.
func (s *State) CheckAndReserve(items []OrderItem) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, line := range items {
		mi, exists := s.menuByPlatformID[line.PlatformMenuItemID]
		if !exists || mi.StockQty < line.Quantity {
			return false
		}
	}
	for _, line := range items {
		s.menuByPlatformID[line.PlatformMenuItemID].StockQty -= line.Quantity
	}
	return true
}

// ReleaseStock puts items' quantities back — called when an accepted order
// is later cancelled.
func (s *State) ReleaseStock(items []OrderItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, line := range items {
		if mi, ok := s.menuByPlatformID[line.PlatformMenuItemID]; ok {
			mi.StockQty += line.Quantity
		}
	}
}

// SetStockByExternalID is POST /kitchen/stock's write path — kitchen staff
// know dishes by this service's own id, not the platform's. ok is false if
// no menu item has that external id.
func (s *State) SetStockByExternalID(externalID string, qty int) (platformID uuid.UUID, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	platformID, ok = s.menuByExternalID[externalID]
	if !ok {
		return uuid.Nil, false
	}
	s.menuByPlatformID[platformID].StockQty = qty
	return platformID, true
}

// StockSnapshot returns every tracked item's current stock, for the
// periodic sync to the platform (PROMPT.md 8.2: "раз в 30 секунд").
func (s *State) StockSnapshot() []MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]MenuItem, 0, len(s.menuByPlatformID))
	for _, mi := range s.menuByPlatformID {
		items = append(items, *mi)
	}
	return items
}

// MarkEventSeen reports whether eventID is new (true) or a duplicate
// delivery this service has already processed (false) — PROMPT.md 7.4's
// at-least-once contract means the platform may resend the same event, and
// this is the dedup half of that deal.
func (s *State) MarkEventSeen(eventID uuid.UUID) (isNew bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.seenEvents[eventID]; seen {
		return false
	}
	s.seenEvents[eventID] = struct{}{}
	return true
}

// AddOrder records a newly accepted or rejected order.
func (s *State) AddOrder(o Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.ID] = &o
}

// GetOrder returns nil if orderID is not tracked.
func (s *State) GetOrder(orderID uuid.UUID) *Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil
	}
	cp := *o
	return &cp
}

// ListOrders returns every tracked order — GET /kitchen/orders's backing
// data, the internal "kitchen feed" PROMPT.md 8.2 asks for.
func (s *State) ListOrders() []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Order, 0, len(s.orders))
	for _, o := range s.orders {
		out = append(out, *o)
	}
	return out
}

// DueOrders returns every tracked order whose NextAdvanceAt has arrived —
// the status ticker's own "what's due" query, same shape as the platform's
// outbox Dispatcher.FetchDue for the same reason: check once, act on a
// batch, not one query per order.
func (s *State) DueOrders(now time.Time) []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []Order
	for _, o := range s.orders {
		if !o.NextAdvanceAt.IsZero() && !o.NextAdvanceAt.After(now) {
			due = append(due, *o)
		}
	}
	return due
}

// AdvanceOrder updates a tracked order's status and its next-advance time
// in one step (nextAdvanceAt zero means "no further automatic advance" —
// the order reached a terminal status).
func (s *State) AdvanceOrder(orderID uuid.UUID, status string, nextAdvanceAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.orders[orderID]; ok {
		o.Status = status
		o.NextAdvanceAt = nextAdvanceAt
	}
}

// CancelOrder marks a tracked order cancelled and stops any further
// automatic advance. Returns the order's items (for stock release) and
// whether it was tracked at all.
func (s *State) CancelOrder(orderID uuid.UUID) (items []OrderItem, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil, false
	}
	o.Status = "cancelled"
	o.NextAdvanceAt = time.Time{}
	return o.Items, true
}
