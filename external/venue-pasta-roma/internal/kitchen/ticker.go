package kitchen

import (
	"context"
	"log/slog"
	"time"

	"venue-pasta-roma/internal/generated/partnerclient"
)

// statusSequence is this emulator's own timer-driven half of the order
// state machine (PROMPT.md 8.2 item 3: "по таймеру двигает статусы cooking
// → ready → delivering → delivered"). It only needs to know the platform's
// allowed next step, not reimplement the platform's full state machine —
// the platform's own table (domain/order/status.go) is still what actually
// enforces each transition.
var statusSequence = map[string]string{
	"confirmed":  "cooking",
	"cooking":    "ready",
	"ready":      "delivering",
	"delivering": "delivered",
}

// RunStatusAdvancer polls State for orders due to move to their next status
// and advances each through the platform's own partner API, until ctx is
// cancelled.
func RunStatusAdvancer(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			advanceDueOrders(ctx, client, state, interval, logger)
		}
	}
}

func advanceDueOrders(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, cookStep time.Duration, logger *slog.Logger) {
	for _, o := range state.DueOrders(time.Now()) {
		next, ok := statusSequence[o.Status]
		if !ok {
			continue
		}

		resp, err := client.AdvanceOrderStatusWithResponse(ctx, o.ID, partnerclient.AdvanceOrderStatusJSONRequestBody{
			Status: partnerclient.AdvanceOrderStatusRequestStatus(next),
		})
		if err != nil || resp.JSON200 == nil {
			logger.Error("advance order status failed", "order_id", o.ID, "to", next, "error", err)
			continue
		}

		nextAdvanceAt := time.Now().Add(cookStep)
		if next == "delivered" {
			nextAdvanceAt = time.Time{}
		}
		state.AdvanceOrder(o.ID, next, nextAdvanceAt)
		logger.Info("order advanced", "order_id", o.ID, "status", next)
	}
}

// RunStockSync periodically pushes this venue's own stock numbers to the
// platform (PROMPT.md 8.2 item 4: "раз в 30 секунд синхронизирует остатки
// в платформу") until ctx is cancelled.
func RunStockSync(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncStock(ctx, client, state, logger)
		}
	}
}

func syncStock(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, logger *slog.Logger) {
	items := state.StockSnapshot()
	if len(items) == 0 {
		return
	}

	updates := make([]partnerclient.AvailabilityUpdate, len(items))
	for i, mi := range items {
		isAvailable := mi.StockQty > 0
		stockQty := mi.StockQty
		updates[i] = partnerclient.AvailabilityUpdate{
			MenuItemId:  mi.PlatformID,
			IsAvailable: &isAvailable,
			StockQty:    &stockQty,
		}
	}

	resp, err := client.UpdateAvailabilityWithResponse(ctx, partnerclient.UpdateAvailabilityJSONRequestBody{Items: updates})
	if err != nil || resp.JSON200 == nil {
		logger.Error("periodic stock sync failed", "error", err)
		return
	}
	logger.Info("stock synced to platform", "items", len(updates))
}
