package kitchen

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"venue-pasta-roma/internal/generated/partnerclient"
)

// envelope mirrors PROMPT.md 6.5's fixed event format exactly — this
// service only has the spec and the README's documented contract to go on,
// the same as any real integrator would, so it defines its own copy rather
// than importing the platform's internal type for it.
type envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

type orderCreatedPayload struct {
	OrderID         uuid.UUID `json:"order_id"`
	DeliveryAddress string    `json:"delivery_address"`
	Items           []struct {
		MenuItemID uuid.UUID `json:"menu_item_id"`
		Name       string    `json:"name"`
		Quantity   int       `json:"quantity"`
	} `json:"items"`
}

type orderCancelledPayload struct {
	OrderID uuid.UUID `json:"order_id"`
}

// acceptETAMinutes is the fixed lead time this emulator quotes on every
// order it accepts. A real venue would compute this from what's actually
// on the line; this service's whole job is to be a stand-in for that
// system, not to reimplement it.
const acceptETAMinutes = 20

// verifySignature reports whether signatureHeader is the correct
// HMAC-SHA256 hex digest of body under secret — pulled out as its own pure
// function so it can be unit-tested without spinning up an HTTP server.
func verifySignature(secret string, body []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signatureHeader))
}

// Handler serves this venue's own tiny HTTP API (PROMPT.md 8.2).
type Handler struct {
	client        *partnerclient.ClientWithResponses
	state         *State
	webhookSecret string
	cookStep      time.Duration
	logger        *slog.Logger
}

func NewHandler(client *partnerclient.ClientWithResponses, state *State, webhookSecret string, cookStep time.Duration, logger *slog.Logger) *Handler {
	return &Handler{client: client, state: state, webhookSecret: webhookSecret, cookStep: cookStep, logger: logger}
}

// HandleWebhook is POST /webhooks/orders: verifies the HMAC signature,
// dedupes by X-Event-Id, and dispatches order.created/order.cancelled.
// PROMPT.md 7.4 puts the dedup obligation on the receiver, not the sender —
// this is that half of the contract.
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.webhookSecret, body, r.Header.Get("X-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "invalid event body", http.StatusBadRequest)
		return
	}

	if !h.state.MarkEventSeen(env.EventID) {
		h.logger.Info("duplicate webhook delivery ignored", "event_id", env.EventID)
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	switch env.EventType {
	case "order.created":
		h.handleOrderCreated(ctx, env.Payload)
	case "order.cancelled":
		h.handleOrderCancelled(env.Payload)
	default:
		h.logger.Info("ignoring unknown event type", "event_type", env.EventType)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleOrderCreated(ctx context.Context, raw json.RawMessage) {
	var p orderCreatedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		h.logger.Error("invalid order.created payload", "error", err)
		return
	}

	items := make([]OrderItem, len(p.Items))
	for i, line := range p.Items {
		items[i] = OrderItem{PlatformMenuItemID: line.MenuItemID, Name: line.Name, Quantity: line.Quantity}
	}

	if h.state.CheckAndReserve(items) {
		h.acceptOrder(ctx, p.OrderID, items, p.DeliveryAddress)
		return
	}
	h.rejectOrder(ctx, p.OrderID, "insufficient stock in the venue's own system")
}

func (h *Handler) acceptOrder(ctx context.Context, orderID uuid.UUID, items []OrderItem, deliveryAddress string) {
	externalOrderID := fmt.Sprintf("PR-%s", orderID.String()[:8])
	resp, err := h.client.AcceptOrderWithResponse(ctx, orderID, partnerclient.AcceptOrderJSONRequestBody{
		EtaMinutes:      acceptETAMinutes,
		ExternalOrderId: &externalOrderID,
	})
	if err != nil || resp.JSON200 == nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode()
		}
		h.logger.Error("accept order failed", "order_id", orderID, "error", err, "status", status)
		h.state.ReleaseStock(items)
		return
	}

	h.state.AddOrder(Order{
		ID:              orderID,
		Status:          "confirmed",
		Items:           items,
		DeliveryAddress: deliveryAddress,
		Accepted:        true,
		NextAdvanceAt:   time.Now().Add(h.cookStep),
	})
	h.logger.Info("order accepted", "order_id", orderID, "external_order_id", externalOrderID)
}

func (h *Handler) rejectOrder(ctx context.Context, orderID uuid.UUID, reason string) {
	resp, err := h.client.RejectOrderWithResponse(ctx, orderID, partnerclient.RejectOrderJSONRequestBody{Reason: reason})
	if err != nil || resp.JSON200 == nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode()
		}
		h.logger.Error("reject order failed", "order_id", orderID, "error", err, "status", status)
	}
	h.state.AddOrder(Order{ID: orderID, Status: "rejected", Accepted: false, RejectionReason: reason})
	h.logger.Info("order rejected", "order_id", orderID, "reason", reason)
}

func (h *Handler) handleOrderCancelled(raw json.RawMessage) {
	var p orderCancelledPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		h.logger.Error("invalid order.cancelled payload", "error", err)
		return
	}
	items, ok := h.state.CancelOrder(p.OrderID)
	if !ok {
		// A cancellation for an order this service never accepted (it was
		// rejected, or the webhook for order.created never arrived) — the
		// platform's own state is still the source of truth, nothing more
		// to reconcile here.
		return
	}
	h.state.ReleaseStock(items)
	h.logger.Info("order cancelled", "order_id", p.OrderID)
}
