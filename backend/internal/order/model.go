package order

import (
	"errors"
	"time"

	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/uid"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/user"
)

// Status is the order lifecycle status. The row-level enum lives with the
// table model; this domain alias is the only status vocabulary the handler
// and service layers use, so the HTTP surface never imports the database
// package.
type Status = database.OrderStatus

// The order state machine values: paid -> shipped -> completed, with the
// refund branch paid|shipped -> refund_requested -> refunded.
const (
	StatusPaid            = database.OrderStatusPaid
	StatusShipped         = database.OrderStatusShipped
	StatusCompleted       = database.OrderStatusCompleted
	StatusRefundRequested = database.OrderStatusRefundRequested
	StatusRefunded        = database.OrderStatusRefunded
)

// ErrOrderNotFound is returned when an order identifier matches no row or the
// row is not owned by the supplied buyer. The buyer handler maps it to 404 so
// another buyer's order is indistinguishable from a missing one. The
// repository and every order-lifecycle usecase share this single sentinel.
var ErrOrderNotFound = errors.New("order not found")

// Business sentinels the HTTP layer maps to status codes. Checkout and the
// refund/fulfillment state machine own their own values; the cross-domain
// aliases reuse the producing domain's sentinel so errors.Is matches on both
// sides of the boundary.
var (
	// ErrInvalidIdempotencyKey is a request-format violation (blank or longer
	// than 64 characters). The handler maps it to 400.
	ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")
	// ErrInvalidOrderRequest is a request-format violation (no cart items,
	// duplicate or non-positive ids). The handler maps it to 400.
	ErrInvalidOrderRequest = errors.New("invalid order request")
	// ErrAddressNotFound is returned when the address does not exist or is not
	// owned by the buyer. Order creation maps it to 422. It aliases the user
	// domain sentinel because the address service produces it.
	ErrAddressNotFound = user.ErrAddressNotFound
	// ErrCartItemNotFound is returned when a selected cart item does not exist
	// or is not owned by the buyer. Order creation maps it to 422. It aliases
	// the cart domain sentinel.
	ErrCartItemNotFound = cart.ErrCartItemNotFound
	// ErrProductNotFound is returned when a cart item references a missing
	// product. Order creation maps it to 422. It aliases the product domain
	// sentinel.
	ErrProductNotFound = product.ErrProductNotFound
	// ErrProductOffSale is returned when a cart item references a product that
	// is no longer purchasable. Order creation maps it to 422.
	ErrProductOffSale = errors.New("product is off sale")
	// ErrInsufficientStock is returned when a product has too little stock. It
	// aliases the product domain sentinel.
	ErrInsufficientStock = product.ErrInsufficientStock
	// ErrInsufficientPoints is returned when the buyer's points balance cannot
	// cover the order total.
	ErrInsufficientPoints = errors.New("insufficient points")
	// ErrIdempotencyConflict is returned when the same Idempotency-Key is
	// replayed with a different request hash. The handler maps it to 409.
	ErrIdempotencyConflict = errors.New("idempotency key replay conflict")
	// ErrUserNotFound is returned when the authenticated buyer row does not
	// exist. This path is unreachable in practice because the JWT is only
	// issued to existing buyers.
	ErrUserNotFound = errors.New("user not found")
	// ErrOrderStateConflict is returned when the order is not in the state the
	// requested transition requires. The handlers map it to 409.
	ErrOrderStateConflict = errors.New("order is not in the required state")
	// ErrInvalidRefundReason is a request-format violation (blank or longer
	// than 255 characters). The handler maps it to 400.
	ErrInvalidRefundReason = errors.New("invalid refund reason")
)

// OrderRequest is the validated create payload.
type OrderRequest struct {
	CartItemIDs []uid.ID
	AddressID   uid.ID
}

// OrderItem is the read projection of one order_items row.
type OrderItem struct {
	ProductID     uid.ID
	ProductName   string
	ProductImage  string
	PriceSnapshot int64
	Quantity      int32
}

// Order is the read projection of an orders row with its items.
type Order struct {
	ID                 uid.ID
	OrderNo            string
	UserID             uid.ID
	RequestHash        string
	Status             Status
	TotalPoints        int64
	Receiver           string
	Phone              string
	Address            string
	RefundReason       string
	RefundRejectReason string
	RefundReviewedByID *uid.ID
	RefundReviewedAt   *time.Time
	PaidAt             time.Time
	ShippedAt          *time.Time
	CompletedAt        *time.Time
	RefundedAt         *time.Time
	CreatedAt          time.Time
	Items              []OrderItem
}

// ToOrder projects a database order row (with its Items preloaded) into the
// domain projection.
func ToOrder(row *database.Order) Order {
	if row == nil {
		return Order{}
	}
	out := Order{
		ID:                 row.ID,
		OrderNo:            row.OrderNo,
		UserID:             row.UserID,
		RequestHash:        row.RequestHash,
		Status:             row.Status,
		TotalPoints:        row.TotalPoints,
		Receiver:           row.Receiver,
		Phone:              row.Phone,
		Address:            row.Address,
		RefundReason:       row.RefundReason,
		RefundRejectReason: row.RefundRejectReason,
		RefundReviewedByID: row.RefundReviewedByID,
		RefundReviewedAt:   row.RefundReviewedAt,
		PaidAt:             row.PaidAt,
		ShippedAt:          row.ShippedAt,
		CompletedAt:        row.CompletedAt,
		RefundedAt:         row.RefundedAt,
		CreatedAt:          row.CreatedAt,
		Items:              make([]OrderItem, 0, len(row.Items)),
	}
	for _, item := range row.Items {
		out.Items = append(out.Items, OrderItem{
			ProductID:     item.ProductID,
			ProductName:   item.ProductName,
			ProductImage:  item.ProductImage,
			PriceSnapshot: item.PriceSnapshot,
			Quantity:      item.Quantity,
		})
	}
	return out
}
