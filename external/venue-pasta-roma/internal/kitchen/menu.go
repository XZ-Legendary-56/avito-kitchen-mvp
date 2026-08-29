package kitchen

import (
	"context"
	"fmt"

	"venue-pasta-roma/internal/generated/partnerclient"
)

// ownMenu is this venue's own menu, as it exists in the venue's imagined
// internal system — external ids are picked here, before the platform has
// ever heard of any of these dishes, which is the whole point of
// PROMPT.md's "source/external_id" mechanism (docs/schema-decisions.md).
func ownMenu() []partnerclient.MenuSyncCategory {
	boolPtr := func(b bool) *bool { return &b }
	intPtr := func(i int) *int { return &i }

	return []partnerclient.MenuSyncCategory{
		{
			Name: "Pasta",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-carbonara", Name: "Carbonara", PriceMinor: 45000, StockQty: intPtr(10), IsAvailable: boolPtr(true)},
				{ExternalId: "pr-bolognese", Name: "Bolognese", PriceMinor: 42000, StockQty: intPtr(8), IsAvailable: boolPtr(true)},
			},
		},
		{
			Name: "Pizza",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-margherita", Name: "Margherita", PriceMinor: 39000, StockQty: intPtr(12), IsAvailable: boolPtr(true)},
			},
		},
		{
			Name: "Dessert",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-tiramisu", Name: "Tiramisu", PriceMinor: 25000, StockQty: intPtr(15), IsAvailable: boolPtr(true)},
			},
		},
	}
}

// LoadMenu pushes ownMenu to the platform via PUT /partner/menu (PROMPT.md
// 8.2 item 1) and records the platform ids it gets back into state, so
// later webhook payloads (which only ever carry the platform's id) can be
// resolved back to this service's own stock counters.
func LoadMenu(ctx context.Context, client *partnerclient.ClientWithResponses, state *State) error {
	resp, err := client.SyncMenuWithResponse(ctx, partnerclient.SyncMenuJSONRequestBody{
		Categories: ownMenu(),
	})
	if err != nil {
		return fmt.Errorf("call syncMenu: %w", err)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("syncMenu: unexpected response (status %d): %s", resp.HTTPResponse.StatusCode, string(resp.Body))
	}

	var items []MenuItem
	for _, category := range resp.JSON200.Categories {
		for _, item := range category.Items {
			if item.ExternalId == nil {
				continue
			}
			items = append(items, MenuItem{
				PlatformID: item.Id,
				ExternalID: *item.ExternalId,
				Name:       item.Name,
				StockQty:   stockQtyOf(item),
			})
		}
	}
	state.SetMenu(items)
	return nil
}

// stockQtyOf treats "unlimited" (nil StockQty) as a very large but finite
// number: this emulator's own CheckAndReserve needs a plain int to compare
// against an order line's quantity, and nothing in ownMenu ever sets an
// item to unlimited stock, so this path only matters if that changes later.
func stockQtyOf(item partnerclient.PartnerMenuItem) int {
	if item.StockQty == nil {
		return 1 << 30
	}
	return *item.StockQty
}
