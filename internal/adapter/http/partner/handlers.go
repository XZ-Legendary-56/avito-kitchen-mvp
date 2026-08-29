package partner

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"avito-kitchen/internal/adapter/http/middleware"
	"avito-kitchen/internal/domain/errs"
	domainorder "avito-kitchen/internal/domain/order"
	"avito-kitchen/internal/generated/partnerapi"
	partnerusecase "avito-kitchen/internal/usecase/partner"
)

// Handlers implements partnerapi.StrictServerInterface. Every method reads
// the venue APIKeyAuth already resolved from context — the generated
// request objects carry no venue/auth field of their own, since
// "security: ApiKeyAuth" is enforced by that middleware, not by the spec's
// request shape.
type Handlers struct {
	Venues *partnerusecase.VenueService
	Menus  *partnerusecase.MenuService
	Orders *partnerusecase.OrderService
}

var _ partnerapi.StrictServerInterface = (*Handlers)(nil)

func venueIDFromContext(ctx context.Context) (uuid.UUID, error) {
	id, ok := middleware.VenueIDFromContext(ctx)
	if !ok {
		// Unreachable in production: APIKeyAuth always sets this before a
		// handler runs. Reported as a plain error, never a panic
		// (PROMPT.md: no panics in working code), so a future refactor
		// that drops the middleware fails loudly as a 500, not silently.
		return uuid.Nil, errors.New("venue id missing from context: APIKeyAuth middleware not applied")
	}
	return id, nil
}

func (h *Handlers) GetPartnerVenue(ctx context.Context, _ partnerapi.GetPartnerVenueRequestObject) (partnerapi.GetPartnerVenueResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.Venues.GetVenue(ctx, venueID)
	if err != nil {
		return nil, err
	}
	return partnerapi.GetPartnerVenue200JSONResponse(toPartnerVenue(v)), nil
}

func (h *Handlers) UpdatePartnerVenue(ctx context.Context, request partnerapi.UpdatePartnerVenueRequestObject) (partnerapi.UpdatePartnerVenueResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	req := partnerusecase.UpdateVenueRequest{
		Description:         request.Body.Description,
		AcceptingOrders:     request.Body.AcceptingOrders,
		MinOrderAmountMinor: request.Body.MinOrderAmountMinor,
		WebhookURL:          request.Body.WebhookUrl,
	}
	if request.Body.Schedule != nil {
		schedule := fromScheduleEntries(*request.Body.Schedule)
		req.Schedule = &schedule
	}

	v, newSecret, err := h.Venues.UpdateVenue(ctx, venueID, req)
	if err != nil {
		return nil, err
	}
	return partnerapi.UpdatePartnerVenue200JSONResponse(toPartnerVenueWithSecret(v, newSecret)), nil
}

func (h *Handlers) SyncMenu(ctx context.Context, request partnerapi.SyncMenuRequestObject) (partnerapi.SyncMenuResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	categories := make([]partnerusecase.MenuSyncCategory, len(request.Body.Categories))
	for i, c := range request.Body.Categories {
		items := make([]partnerusecase.MenuSyncItem, len(c.Items))
		for j, item := range c.Items {
			if item.PriceMinor < 1 {
				return nil, errs.Newf(errs.CodeValidationError, "priceMinor must be positive for item %q", item.ExternalId)
			}
			cookingTime := 15
			if item.CookingTimeMinutes != nil {
				cookingTime = *item.CookingTimeMinutes
			}
			isAvailable := true
			if item.IsAvailable != nil {
				isAvailable = *item.IsAvailable
			}
			description := ""
			if item.Description != nil {
				description = *item.Description
			}
			items[j] = partnerusecase.MenuSyncItem{
				ExternalID:         item.ExternalId,
				Name:               item.Name,
				Description:        description,
				PriceMinor:         item.PriceMinor,
				CookingTimeMinutes: cookingTime,
				IsAvailable:        isAvailable,
				StockQty:           item.StockQty,
			}
		}
		position := 0
		if c.Position != nil {
			position = *c.Position
		}
		categories[i] = partnerusecase.MenuSyncCategory{Name: c.Name, Position: position, Items: items}
	}

	menu, err := h.Menus.SyncMenu(ctx, venueID, categories)
	if err != nil {
		return nil, err
	}
	return partnerapi.SyncMenu200JSONResponse(toPartnerMenu(menu)), nil
}

func (h *Handlers) CreateMenuItem(ctx context.Context, request partnerapi.CreateMenuItemRequestObject) (partnerapi.CreateMenuItemResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.PriceMinor < 1 {
		return nil, errs.New(errs.CodeValidationError, "priceMinor must be positive")
	}

	cookingTime := 15
	if request.Body.CookingTimeMinutes != nil {
		cookingTime = *request.Body.CookingTimeMinutes
	}
	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	mi, err := h.Menus.CreateItem(ctx, venueID, partnerusecase.NewMenuItem{
		CategoryID:         request.Body.CategoryId,
		Name:               request.Body.Name,
		Description:        description,
		PriceMinor:         request.Body.PriceMinor,
		CookingTimeMinutes: cookingTime,
		StockQty:           request.Body.StockQty,
		ExternalID:         request.Body.ExternalId,
	})
	if err != nil {
		return nil, err
	}
	return partnerapi.CreateMenuItem201JSONResponse(toPartnerMenuItem(*mi)), nil
}

func (h *Handlers) UpdateMenuItem(ctx context.Context, request partnerapi.UpdateMenuItemRequestObject) (partnerapi.UpdateMenuItemResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.PriceMinor != nil && *request.Body.PriceMinor < 1 {
		return nil, errs.New(errs.CodeValidationError, "priceMinor must be positive")
	}

	mi, err := h.Menus.UpdateItem(ctx, venueID, request.ItemId, partnerusecase.MenuItemPatch{
		CategoryID:         request.Body.CategoryId,
		Name:               request.Body.Name,
		Description:        request.Body.Description,
		PriceMinor:         request.Body.PriceMinor,
		CookingTimeMinutes: request.Body.CookingTimeMinutes,
	})
	if err != nil {
		return nil, err
	}
	return partnerapi.UpdateMenuItem200JSONResponse(toPartnerMenuItem(*mi)), nil
}

func (h *Handlers) UpdateAvailability(ctx context.Context, request partnerapi.UpdateAvailabilityRequestObject) (partnerapi.UpdateAvailabilityResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil || len(request.Body.Items) == 0 {
		return nil, errs.New(errs.CodeValidationError, "items must not be empty")
	}

	updates := make([]partnerusecase.AvailabilityUpdate, len(request.Body.Items))
	for i, item := range request.Body.Items {
		updates[i] = partnerusecase.AvailabilityUpdate{
			MenuItemID:  item.MenuItemId,
			IsAvailable: item.IsAvailable,
			StockQtySet: true,
			StockQty:    item.StockQty,
		}
	}

	items, err := h.Menus.UpdateAvailability(ctx, venueID, updates)
	if err != nil {
		return nil, err
	}

	resp := partnerapi.UpdateAvailabilityResponse{Items: make([]partnerapi.AvailabilityUpdate, len(items))}
	for i, mi := range items {
		isAvailable := mi.IsAvailable
		resp.Items[i] = partnerapi.AvailabilityUpdate{
			MenuItemId:  mi.ID,
			IsAvailable: &isAvailable,
			StockQty:    mi.StockQty,
		}
	}
	return partnerapi.UpdateAvailability200JSONResponse(resp), nil
}

func (h *Handlers) ListPartnerOrders(ctx context.Context, request partnerapi.ListPartnerOrdersRequestObject) (partnerapi.ListPartnerOrdersResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var status *domainorder.Status
	if request.Params.Status != nil {
		s := domainorder.Status(*request.Params.Status)
		status = &s
	}
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}

	orders, err := h.Orders.ListOrders(ctx, venueID, status, request.Params.Since, limit)
	if err != nil {
		return nil, err
	}
	resp := make(partnerapi.ListPartnerOrders200JSONResponse, len(orders))
	for i, o := range orders {
		resp[i] = toPartnerOrder(&o)
	}
	return resp, nil
}

func (h *Handlers) AcceptOrder(ctx context.Context, request partnerapi.AcceptOrderRequestObject) (partnerapi.AcceptOrderResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.EtaMinutes < 1 {
		return nil, errs.New(errs.CodeValidationError, "etaMinutes must be positive")
	}

	o, err := h.Orders.AcceptOrder(ctx, venueID, request.OrderId, request.Body.EtaMinutes, request.Body.ExternalOrderId)
	if err != nil {
		return nil, err
	}
	return partnerapi.AcceptOrder200JSONResponse(toPartnerOrder(o)), nil
}

func (h *Handlers) RejectOrder(ctx context.Context, request partnerapi.RejectOrderRequestObject) (partnerapi.RejectOrderResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil || request.Body.Reason == "" {
		return nil, errs.New(errs.CodeValidationError, "reason must not be empty")
	}

	o, err := h.Orders.RejectOrder(ctx, venueID, request.OrderId, request.Body.Reason)
	if err != nil {
		return nil, err
	}
	return partnerapi.RejectOrder200JSONResponse(toPartnerOrder(o)), nil
}

func (h *Handlers) AdvanceOrderStatus(ctx context.Context, request partnerapi.AdvanceOrderStatusRequestObject) (partnerapi.AdvanceOrderStatusResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	o, err := h.Orders.AdvanceStatus(ctx, venueID, request.OrderId, domainorder.Status(request.Body.Status))
	if err != nil {
		return nil, err
	}
	return partnerapi.AdvanceOrderStatus200JSONResponse(toPartnerOrder(o)), nil
}

// errNotImplementedYet is returned by the three rescue-offer operations
// this stage does not build. PROMPT.md's own stage plan (section 13)
// defers rescue offers until after stage 10 — the strict server interface
// still requires a method for every path in the spec, so these exist to
// say "not yet" honestly instead of faking a response.
var errNotImplementedYet = errors.New("not implemented until a later stage of this project")

func (h *Handlers) ListPartnerRescueOffers(context.Context, partnerapi.ListPartnerRescueOffersRequestObject) (partnerapi.ListPartnerRescueOffersResponseObject, error) {
	return nil, errNotImplementedYet
}

func (h *Handlers) CreateRescueOffer(context.Context, partnerapi.CreateRescueOfferRequestObject) (partnerapi.CreateRescueOfferResponseObject, error) {
	return nil, errNotImplementedYet
}

func (h *Handlers) CancelRescueOffer(context.Context, partnerapi.CancelRescueOfferRequestObject) (partnerapi.CancelRescueOfferResponseObject, error) {
	return nil, errNotImplementedYet
}
