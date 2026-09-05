package cart

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinels the HTTP layer maps to status codes.
var (
	// ErrCartItemNotFound is returned when the cart item does not exist or is
	// not owned by the supplied userID. The handler maps it to 404.
	ErrCartItemNotFound = errors.New("cart item not found")
	// ErrInvalidCartItem is a request-level violation (missing product, bad
	// quantity).
	ErrInvalidCartItem = errors.New("invalid cart item")
	// ErrCartProductNotFound is returned when the product being added does not
	// exist.
	ErrCartProductNotFound = errors.New("cart product not found")
	// ErrQuantityOverflow guards the int32 quantity column against runaway
	// accumulation.
	ErrQuantityOverflow = errors.New("cart quantity exceeds limit")
)

// CartService owns the buyer cart. Every method takes the authenticated
// userID and never accepts a client-supplied owner id. Off-sale products stay
// in the cart; they are reported non-purchasable through their product status.
type CartService struct {
	db       *gorm.DB
	products *product.Service
	logger   *slog.Logger
}

// CartServiceDeps is the dependency bundle for NewCartService.
type CartServiceDeps struct {
	DB       *gorm.DB
	Products *product.Service
	Logger   *slog.Logger
}

// NewCartService validates the dependency bundle and returns the service.
func NewCartService(deps CartServiceDeps) (*CartService, error) {
	if deps.DB == nil {
		return nil, errors.New("cart service: database is required")
	}
	if deps.Products == nil {
		return nil, errors.New("cart service: product service is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CartService{db: deps.DB, products: deps.Products, logger: logger}, nil
}

// CartItemView is the read projection of one cart row with its current
// product. The product status drives purchasability.
type CartItemView struct {
	ID        uid.ID
	ProductID uid.ID
	Quantity  int32
	Product   product.ProductView
}

// Add accumulates quantity by (user_id, product_id): adding the same product
// again sums the quantities onto the existing row. The accumulation is an
// atomic INSERT ... ON CONFLICT ... DO UPDATE so concurrent adds cannot lose
// quantity. An off-sale product is still addable and is reported
// non-purchasable through its status.
func (s *CartService) Add(ctx context.Context, userID, productID uid.ID, quantity int32) (CartItemView, error) {
	if s == nil || s.db == nil || s.products == nil {
		return CartItemView{}, errors.New("cart service is not configured")
	}
	if uid.IsZero(userID) || uid.IsZero(productID) {
		return CartItemView{}, ErrInvalidCartItem
	}
	if quantity < 1 {
		return CartItemView{}, ErrInvalidCartItem
	}
	var view CartItemView
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		var productRow database.Product
		if err := tx.WithContext(ctx).Select("id").Where("id = ?", productID).First(&productRow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCartProductNotFound
			}
			return err
		}
		// Pre-check the accumulated quantity against the int32 column so a
		// runaway sum fails with a business error. The upsert below is still
		// the atomic accumulation.
		var current database.CartItem
		err := tx.WithContext(ctx).
			Where("user_id = ? AND product_id = ?", userID, productID).
			First(&current).Error
		switch {
		case err == nil:
			if int64(current.Quantity)+int64(quantity) > math.MaxInt32 {
				return ErrQuantityOverflow
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			// First add; nothing to pre-check.
		default:
			return err
		}
		item := database.CartItem{UserID: userID, ProductID: productID, Quantity: quantity}
		if err := tx.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "user_id"}, {Name: "product_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"quantity": gorm.Expr("cart_items.quantity + EXCLUDED.quantity"),
				}),
			}).
			Create(&item).Error; err != nil {
			return err
		}
		var saved database.CartItem
		if err := tx.WithContext(ctx).Preload("Product").
			Where("user_id = ? AND product_id = ?", userID, productID).
			First(&saved).Error; err != nil {
			return err
		}
		view = s.toView(saved)
		return nil
	})
	if err != nil {
		return CartItemView{}, err
	}
	return view, nil
}

// List returns the current buyer's cart items ordered by id ascending. Only
// rows owned by userID are returned.
func (s *CartService) List(ctx context.Context, userID uid.ID) ([]CartItemView, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("cart service is not configured")
	}
	var items []database.CartItem
	if err := s.db.WithContext(ctx).Preload("Product").
		Where("user_id = ?", userID).
		Order("id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	views := make([]CartItemView, 0, len(items))
	for i := range items {
		views = append(views, s.toView(items[i]))
	}
	return views, nil
}

// Update sets the quantity of a buyer-owned cart item. A row owned by another
// buyer surfaces as ErrCartItemNotFound.
func (s *CartService) Update(ctx context.Context, userID, itemID uid.ID, quantity int32) (CartItemView, error) {
	if s == nil || s.db == nil || s.products == nil {
		return CartItemView{}, errors.New("cart service is not configured")
	}
	if uid.IsZero(userID) || uid.IsZero(itemID) {
		return CartItemView{}, ErrCartItemNotFound
	}
	if quantity < 1 {
		return CartItemView{}, ErrInvalidCartItem
	}
	var view CartItemView
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		var item database.CartItem
		if err := tx.WithContext(ctx).Where("id = ? AND user_id = ?", itemID, userID).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCartItemNotFound
			}
			return err
		}
		if err := tx.WithContext(ctx).
			Model(&database.CartItem{}).
			Where("id = ?", item.ID).
			Update("quantity", quantity).Error; err != nil {
			return err
		}
		var saved database.CartItem
		if err := tx.WithContext(ctx).Preload("Product").Where("id = ?", item.ID).First(&saved).Error; err != nil {
			return err
		}
		view = s.toView(saved)
		return nil
	})
	if err != nil {
		return CartItemView{}, err
	}
	return view, nil
}

// Delete removes a buyer-owned cart item. A row owned by another buyer
// surfaces as ErrCartItemNotFound.
func (s *CartService) Delete(ctx context.Context, userID, itemID uid.ID) error {
	if s == nil || s.db == nil {
		return errors.New("cart service is not configured")
	}
	if uid.IsZero(userID) || uid.IsZero(itemID) {
		return ErrCartItemNotFound
	}
	return database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).
			Where("id = ? AND user_id = ?", itemID, userID).
			Delete(&database.CartItem{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrCartItemNotFound
		}
		return nil
	})
}

func (s *CartService) toView(item database.CartItem) CartItemView {
	return CartItemView{
		ID:        item.ID,
		ProductID: item.ProductID,
		Quantity:  item.Quantity,
		Product:   s.products.View(item.Product),
	}
}
