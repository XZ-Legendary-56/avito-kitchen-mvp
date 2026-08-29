package public

import (
	"context"
	"errors"

	"avito-kitchen/internal/domain/errs"
	"avito-kitchen/internal/generated/publicapi"
	catalogusecase "avito-kitchen/internal/usecase/catalog"
	orderusecase "avito-kitchen/internal/usecase/order"
)

// Handlers implements publicapi.StrictServerInterface. Every method either
// maps a request to a use-case call and its result to a wire type, or —
// for the four operations this stage does not build (orders, rescue) —
// reports that plainly rather than pretending to handle them.
type Handlers struct {
	Catalog *catalogusecase.Service
	Cart    *orderusecase.CartService
}

var _ publicapi.StrictServerInterface = (*Handlers)(nil)

func (h *Handlers) ListVenues(ctx context.Context, request publicapi.ListVenuesRequestObject) (publicapi.ListVenuesResponseObject, error) {
	filter := catalogusecase.ListVenuesFilter{}
	if request.Params.Cuisine != nil {
		filter.Cuisine = *request.Params.Cuisine
	}
	if request.Params.Q != nil {
		filter.NamePrefix = *request.Params.Q
	}
	if request.Params.IsOpen != nil {
		filter.OnlyOpen = *request.Params.IsOpen
	}
	if request.Params.Cursor != nil {
		filter.Cursor = *request.Params.Cursor
	}
	if request.Params.Limit != nil {
		filter.Limit = *request.Params.Limit
	}

	result, err := h.Catalog.ListVenues(ctx, filter)
	if err != nil {
		return nil, err
	}

	items := make([]publicapi.Venue, len(result.Items))
	for i, v := range result.Items {
		items[i] = toVenue(v)
	}
	page := publicapi.VenueListPage{Items: items}
	if result.NextCursor != "" {
		page.NextCursor = &result.NextCursor
	}
	return publicapi.ListVenues200JSONResponse(page), nil
}

func (h *Handlers) GetVenue(ctx context.Context, request publicapi.GetVenueRequestObject) (publicapi.GetVenueResponseObject, error) {
	view, err := h.Catalog.GetVenue(ctx, request.VenueId)
	if err != nil {
		return nil, err
	}
	return publicapi.GetVenue200JSONResponse(toVenueDetail(view)), nil
}

func (h *Handlers) GetVenueMenu(ctx context.Context, request publicapi.GetVenueMenuRequestObject) (publicapi.GetVenueMenuResponseObject, error) {
	ifNoneMatch := ""
	if request.Params.IfNoneMatch != nil {
		ifNoneMatch = *request.Params.IfNoneMatch
	}

	result, err := h.Catalog.GetMenu(ctx, request.VenueId, ifNoneMatch)
	if err != nil {
		return nil, err
	}
	if result.NotModified {
		return publicapi.GetVenueMenu304Response{}, nil
	}

	etag := result.ETag
	return publicapi.GetVenueMenu200JSONResponse{
		Body:    toMenu(result.Menu),
		Headers: publicapi.GetVenueMenu200ResponseHeaders{ETag: &etag},
	}, nil
}

func (h *Handlers) GetCart(ctx context.Context, request publicapi.GetCartRequestObject) (publicapi.GetCartResponseObject, error) {
	view, err := h.Cart.GetCart(ctx, request.Params.XClientId)
	if err != nil {
		return nil, err
	}
	return publicapi.GetCart200JSONResponse(toCart(view)), nil
}

func (h *Handlers) ClearCart(ctx context.Context, request publicapi.ClearCartRequestObject) (publicapi.ClearCartResponseObject, error) {
	if err := h.Cart.ClearCart(ctx, request.Params.XClientId); err != nil {
		return nil, err
	}
	return publicapi.ClearCart204Response{}, nil
}

func (h *Handlers) AddCartItem(ctx context.Context, request publicapi.AddCartItemRequestObject) (publicapi.AddCartItemResponseObject, error) {
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	view, err := h.Cart.AddItem(ctx, request.Params.XClientId, request.Body.MenuItemId, request.Body.Quantity)
	if err != nil {
		return nil, err
	}
	return publicapi.AddCartItem200JSONResponse(toCart(view)), nil
}

func (h *Handlers) UpdateCartItem(ctx context.Context, request publicapi.UpdateCartItemRequestObject) (publicapi.UpdateCartItemResponseObject, error) {
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	view, err := h.Cart.UpdateItemQuantity(ctx, request.Params.XClientId, request.ItemId, request.Body.Quantity)
	if err != nil {
		return nil, err
	}
	return publicapi.UpdateCartItem200JSONResponse(toCart(view)), nil
}

func (h *Handlers) RemoveCartItem(ctx context.Context, request publicapi.RemoveCartItemRequestObject) (publicapi.RemoveCartItemResponseObject, error) {
	view, err := h.Cart.RemoveItem(ctx, request.Params.XClientId, request.ItemId)
	if err != nil {
		return nil, err
	}
	return publicapi.RemoveCartItem200JSONResponse(toCart(view)), nil
}

// errNotImplementedYet is returned by every operation this stage does not
// build (checkout, order status/cancel, the rescue feed). PROMPT.md's own
// stage plan (section 13) puts order placement at stage 7 and rescue
// offers after stage 10 — the strict server interface still requires a
// method for every path in the spec, so these exist to say "not yet"
// honestly instead of faking a response. httperr.Write reports them as a
// plain 500 for now; there is nothing more specific to say about a route
// that legitimately has no implementation.
var errNotImplementedYet = errors.New("not implemented until a later stage of this project")

func (h *Handlers) CreateOrder(context.Context, publicapi.CreateOrderRequestObject) (publicapi.CreateOrderResponseObject, error) {
	return nil, errNotImplementedYet
}

func (h *Handlers) GetOrder(context.Context, publicapi.GetOrderRequestObject) (publicapi.GetOrderResponseObject, error) {
	return nil, errNotImplementedYet
}

func (h *Handlers) CancelOrder(context.Context, publicapi.CancelOrderRequestObject) (publicapi.CancelOrderResponseObject, error) {
	return nil, errNotImplementedYet
}

func (h *Handlers) ListRescueOffers(context.Context, publicapi.ListRescueOffersRequestObject) (publicapi.ListRescueOffersResponseObject, error) {
	return nil, errNotImplementedYet
}
