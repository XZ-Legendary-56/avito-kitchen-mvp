package order

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	domaincatalog "avito-kitchen/internal/domain/catalog"
	"avito-kitchen/internal/domain/errs"
	domainorder "avito-kitchen/internal/domain/order"
	"avito-kitchen/internal/usecase"
)

// CartService implements the cart use-cases (PROMPT.md 5.1 item 3).
type CartService struct {
	carts     CartRepository
	menuItems MenuItemLookup
	txManager usecase.TxManager
}

// NewCartService builds a CartService. Mutations run through txManager
// (PROMPT.md 6.4) because each one is a read-modify-write of the whole
// cart: two requests for the same client racing between the read and the
// write would otherwise silently lose one of them.
func NewCartService(carts CartRepository, menuItems MenuItemLookup, txManager usecase.TxManager) *CartService {
	return &CartService{carts: carts, menuItems: menuItems, txManager: txManager}
}

// CartView is what every cart use-case returns: the cart itself (nil if the
// client has none yet) plus a fresh snapshot of each line's menu item —
// PROMPT.md's public API shows this as CartItem.isAvailable ("may have
// changed since the item was added") and needs the item's current name, so
// it is always re-fetched on read, never stored on the cart itself.
type CartView struct {
	ClientID  uuid.UUID
	Cart      *domainorder.Cart
	MenuItems map[uuid.UUID]domaincatalog.MenuItem // keyed by CartItem.MenuItemID
}

// GetCart returns the client's cart, or an empty view if they have none yet.
func (s *CartService) GetCart(ctx context.Context, clientID uuid.UUID) (CartView, error) {
	cart, err := s.carts.Get(ctx, clientID)
	if err != nil {
		return CartView{}, fmt.Errorf("get cart: %w", err)
	}
	view := CartView{ClientID: clientID, Cart: cart}
	if cart == nil {
		return view, nil
	}
	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	view.MenuItems = menuItems
	return view, nil
}

// menuItemSnapshot re-fetches every line's menu item in one batch call
// (PROMPT.md 6.6), never one query per line.
func (s *CartService) menuItemSnapshot(ctx context.Context, cart *domainorder.Cart) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	ids := make([]uuid.UUID, len(cart.Items))
	for i, item := range cart.Items {
		ids[i] = item.MenuItemID
	}
	items, err := s.menuItems.GetMany(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("look up cart item menu items: %w", err)
	}
	return items, nil
}

// AddItem adds quantity units of menuItemID to clientID's cart, starting a
// new cart pinned to the item's venue if they do not have one yet. This is
// the "soft" availability check from PROMPT.md 5.2 — checked here so the
// customer learns about a problem immediately, and checked again,
// authoritatively, inside the checkout transaction (PROMPT.md 5.2, stage 7)
// because stock can change in between.
func (s *CartService) AddItem(ctx context.Context, clientID, menuItemID uuid.UUID, quantity int) (CartView, error) {
	item, err := s.menuItems.Get(ctx, menuItemID)
	if err != nil {
		return CartView{}, fmt.Errorf("look up menu item: %w", err)
	}
	if item == nil {
		return CartView{}, errs.New(errs.CodeNotFound, "menu item not found")
	}
	if err := item.EnsureAvailable(quantity); err != nil {
		return CartView{}, err
	}

	var cart *domainorder.Cart
	err = s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			cart = domainorder.NewCart(clientID, item.VenueID)
		}
		if txErr := cart.AddItem(uuid.New(), item.VenueID, item.ID, quantity, item.PriceMinor); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// UpdateItemQuantity changes the quantity of the cart line identified by
// itemID (cart_items.id, not the menu item id).
func (s *CartService) UpdateItemQuantity(ctx context.Context, clientID, itemID uuid.UUID, quantity int) (CartView, error) {
	var cart *domainorder.Cart
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			return errs.New(errs.CodeNotFound, "cart not found")
		}
		if txErr := cart.UpdateQuantity(itemID, quantity); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// RemoveItem drops the cart line identified by itemID.
func (s *CartService) RemoveItem(ctx context.Context, clientID, itemID uuid.UUID) (CartView, error) {
	var cart *domainorder.Cart
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			return errs.New(errs.CodeNotFound, "cart not found")
		}
		if txErr := cart.RemoveItem(itemID); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// ClearCart empties the client's cart. A single delete needs no
// transaction of its own.
func (s *CartService) ClearCart(ctx context.Context, clientID uuid.UUID) error {
	if err := s.carts.Clear(ctx, clientID); err != nil {
		return fmt.Errorf("clear cart: %w", err)
	}
	return nil
}
