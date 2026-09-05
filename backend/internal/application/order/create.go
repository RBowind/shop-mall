// Package order hosts the CreateOrderUsecase, the consistency gate of the
// order and points subsystem. Order creation, inventory decrement, points
// payment, the order_pay ledger entry and the selected cart-item deletion all
// commit in ONE database transaction. The transaction runner retries only
// complete transactions on PostgreSQL deadlock (40P01) and serialization
// failure (40001); a single SQL statement is never retried.
package order

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/cart"
	domorder "shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/user"

	"gorm.io/gorm"
)

// Order-lifecycle errors and projections live in the order domain package;
// this usecase re-exports them so the checkout vocabulary stays importable
// from the application layer. ErrAddressNotFound and ErrCartItemNotFound
// double as ownership masking: a buyer-scoped query that matches nothing is
// indistinguishable from a row owned by another buyer.
var (
	ErrInvalidIdempotencyKey = domorder.ErrInvalidIdempotencyKey
	ErrInvalidOrderRequest   = domorder.ErrInvalidOrderRequest
	ErrAddressNotFound       = domorder.ErrAddressNotFound
	ErrCartItemNotFound      = domorder.ErrCartItemNotFound
	ErrProductNotFound       = domorder.ErrProductNotFound
	ErrProductOffSale        = domorder.ErrProductOffSale
	ErrInsufficientStock     = domorder.ErrInsufficientStock
	ErrInsufficientPoints    = domorder.ErrInsufficientPoints
	ErrIdempotencyConflict   = domorder.ErrIdempotencyConflict
	ErrUserNotFound          = domorder.ErrUserNotFound
)

// Order projections re-exported from the order domain (see model.go there).
type (
	Order        = domorder.Order
	OrderItem    = domorder.OrderItem
	OrderRequest = domorder.OrderRequest
)

// ToOrder projects a database order row into the domain projection.
func ToOrder(row *database.Order) Order { return domorder.ToOrder(row) }

// RequestHashItem is one product/quantity pair that participates in the
// canonical request hash.
type RequestHashItem struct {
	ProductID uid.ID `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

// hashAddress is the server-side address snapshot embedded in the request
// hash. The client never supplies it; order creation reads it from the
// user_addresses row.
type hashAddress struct {
	AddressID uid.ID `json:"address_id"`
	Version   int64  `json:"version"`
	Receiver  string `json:"receiver"`
	Phone     string `json:"phone"`
	Region    string `json:"region"`
	Detail    string `json:"detail"`
}

// requestHashPayload is the canonical, key-ordered serialization the hash
// covers. Ordering every collection ascending makes the hash stable across
// request resends.
type requestHashPayload struct {
	CartItemIDs []uid.ID          `json:"cart_item_ids"`
	Items       []RequestHashItem `json:"items"`
	Address     hashAddress       `json:"address"`
}

// ComputeOrderRequestHash derives the canonical request hash from the selected
// cart item ids, the per-item product/quantity pairs and the server-side
// address snapshot. The contract mandates these inputs (canonical cart ids,
// quantities, address id, address version and the server-side address
// snapshot); the address version and snapshot are read from the database, not
// trusted from the client body.
func ComputeOrderRequestHash(cartItemIDs []uid.ID, items []RequestHashItem, addr user.AddressView) string {
	ids := append([]uid.ID(nil), cartItemIDs...)
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	ordered := append([]RequestHashItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].ProductID[:], ordered[j].ProductID[:]) < 0 })
	payload := requestHashPayload{
		CartItemIDs: ids,
		Items:       ordered,
		Address: hashAddress{
			AddressID: addr.ID,
			Version:   addr.Version,
			Receiver:  addr.Receiver,
			Phone:     addr.Phone,
			Region:    addr.Region,
			Detail:    addr.Detail,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal over plain int/string fields cannot fail.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ValidateIdempotencyKey enforces the Idempotency-Key header contract: 1-64
// characters and not blank.
func ValidateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 64 || strings.TrimSpace(key) == "" {
		return ErrInvalidIdempotencyKey
	}
	return nil
}

// CreateOrderUsecase executes order creation. It owns the single transaction
// that commits the order, the order items, the stock decrements, the points
// payment, the order_pay ledger entry and the selected cart-item deletion.
type CreateOrderUsecase struct {
	db       *gorm.DB
	orders   *domorder.Repository
	address  *user.AddressService
	cart     *cart.Repository
	products *product.Repository
	ledger   *payment.Repository
	now      func() time.Time
	orderNo  func() string
}

// CreateOrderUsecaseDeps is the dependency bundle for NewCreateOrderUsecase.
type CreateOrderUsecaseDeps struct {
	DB       *gorm.DB
	Orders   *domorder.Repository
	Address  *user.AddressService
	Cart     *cart.Repository
	Products *product.Repository
	Ledger   *payment.Repository
	Now      func() time.Time
	OrderNo  func() string
}

// NewCreateOrderUsecase validates the dependency bundle and returns the
// usecase.
func NewCreateOrderUsecase(deps CreateOrderUsecaseDeps) (*CreateOrderUsecase, error) {
	if deps.DB == nil {
		return nil, errors.New("order usecase: database is required")
	}
	if deps.Orders == nil {
		return nil, errors.New("order usecase: order repository is required")
	}
	if deps.Address == nil {
		return nil, errors.New("order usecase: address service is required")
	}
	if deps.Cart == nil {
		return nil, errors.New("order usecase: cart repository is required")
	}
	if deps.Products == nil {
		return nil, errors.New("order usecase: product repository is required")
	}
	if deps.Ledger == nil {
		return nil, errors.New("order usecase: ledger repository is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	orderNo := deps.OrderNo
	if orderNo == nil {
		orderNo = func() string { return generateOrderNo(now()) }
	}
	return &CreateOrderUsecase{
		db:       deps.DB,
		orders:   deps.Orders,
		address:  deps.Address,
		cart:     deps.Cart,
		products: deps.Products,
		ledger:   deps.Ledger,
		now:      now,
		orderNo:  orderNo,
	}, nil
}

// Execute creates an order idempotently. The locks are acquired in the
// documented order: order rows, then the user row, then the address and cart
// rows, then product rows ordered by product_id ascending. A same-token
// same-hash replay returns the existing order before any new side effect; a
// same-token different-hash replay returns ErrIdempotencyConflict.
func (u *CreateOrderUsecase) Execute(ctx context.Context, userID uid.ID, idempotencyKey string, req OrderRequest) (Order, error) {
	if u == nil || u.db == nil {
		return Order{}, errors.New("order usecase is not configured")
	}
	if err := ValidateIdempotencyKey(idempotencyKey); err != nil {
		return Order{}, err
	}
	if err := validateOrderRequest(req); err != nil {
		return Order{}, err
	}
	if uid.IsZero(userID) {
		return Order{}, ErrInvalidOrderRequest
	}

	var result Order
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		// 1. Lock order rows for this (user, token). The FOR UPDATE lock
		// serializes replays; a concurrent same-token create that reaches the
		// INSERT first is handled below via the unique-constraint conflict.
		existing, err := u.orders.LockForReplay(tx, ctx, userID, idempotencyKey)
		if err != nil {
			return err
		}
		if existing != nil {
			replay, err := u.resolveReplay(tx, ctx, userID, req, existing)
			if err != nil {
				return err
			}
			result = replay
			return nil
		}

		// 2. Lock the user row.
		if err := u.ledger.LockUser(tx, ctx, userID); err != nil {
			return translateBalanceError(err)
		}
		// 3. Lock the address row.
		addr, err := u.address.LockForOrder(ctx, tx, userID, req.AddressID)
		if err != nil {
			return err
		}
		// 4. Lock the selected cart rows (ascending id).
		cartItems, err := u.cart.LockForOrder(tx, ctx, userID, req.CartItemIDs)
		if err != nil {
			if errors.Is(err, cart.ErrCartItemNotFound) {
				// A concurrent same-token transaction can consume the cart rows
				// after this transaction's step-1 replay check. Re-check the
				// token before reporting the 422: if an order now exists, serve
				// the replay instead. The re-check is a plain read, NOT a
				// FOR UPDATE: this transaction already holds the user and
				// address rows, so locking the order row here would invert the
				// documented lock order (user → address → order instead of
				// order → user → address) and deadlock against a concurrent
				// replay that holds the order row at step 1 and wants the
				// address row in resolveReplay. The read needs no lock because
				// the user-row lock serializes this transaction behind a
				// same-token winner's commit: by the time this transaction
				// holds the user row, the winner has committed and the order
				// row is visible to a plain read.
				racer, rerr := u.orders.FindForReplay(tx, ctx, userID, idempotencyKey)
				if rerr != nil {
					return rerr
				}
				if racer != nil {
					replay, rerr := u.resolveReplay(tx, ctx, userID, req, racer)
					if rerr != nil {
						return rerr
					}
					result = replay
					return nil
				}
			}
			return err
		}
		// 5. Lock the product rows (ascending product_id).
		products, err := u.products.LockByIDs(tx, ctx, productIDsOf(cartItems))
		if err != nil {
			return err
		}
		productByID := indexProducts(products)

		created, err := u.buildAndCommit(ctx, tx, userID, idempotencyKey, req, addr, cartItems, productByID)
		if err != nil {
			return err
		}
		result = created
		return nil
	})
	if err != nil {
		if database.IsUniqueViolationError(err) && uid.IsZero(result.ID) {
			// A concurrent transaction created the same-token order first. The
			// loser's transaction rolled back all of its side effects; re-read
			// the winner and answer replay or conflict.
			replay, rerr := u.resolveAfterConflict(ctx, userID, idempotencyKey, req)
			if rerr != nil {
				return Order{}, err
			}
			return replay, nil
		}
		return Order{}, err
	}
	return result, nil
}

// resolveReplay decides same-token replay vs conflict for an existing order.
// The hash is recomputed from the request's static cart ids, the existing
// order's item quantities (the cart rows were already consumed) and the
// current server-side address snapshot.
func (u *CreateOrderUsecase) resolveReplay(tx *gorm.DB, ctx context.Context, userID uid.ID, req OrderRequest, existing *database.Order) (Order, error) {
	addr, err := u.address.LockForOrder(ctx, tx, userID, req.AddressID)
	if err != nil {
		return Order{}, err
	}
	items := make([]RequestHashItem, 0, len(existing.Items))
	for _, item := range existing.Items {
		items = append(items, RequestHashItem{ProductID: item.ProductID, Quantity: item.Quantity})
	}
	if ComputeOrderRequestHash(req.CartItemIDs, items, addr) != existing.RequestHash {
		return Order{}, ErrIdempotencyConflict
	}
	return ToOrder(existing), nil
}

// resolveAfterConflict re-reads the same-token order after a unique-violation
// rollback and answers replay or conflict. It runs outside the failed
// transaction because the loser's transaction already rolled back.
func (u *CreateOrderUsecase) resolveAfterConflict(ctx context.Context, userID uid.ID, clientToken string, req OrderRequest) (Order, error) {
	existing, err := u.orders.FindByToken(ctx, userID, clientToken)
	if err != nil {
		return Order{}, err
	}
	addr, err := u.address.Get(ctx, userID, req.AddressID)
	if err != nil {
		return Order{}, err
	}
	items := make([]RequestHashItem, 0, len(existing.Items))
	for _, item := range existing.Items {
		items = append(items, RequestHashItem{ProductID: item.ProductID, Quantity: item.Quantity})
	}
	if ComputeOrderRequestHash(req.CartItemIDs, items, addr) != existing.RequestHash {
		return Order{}, ErrIdempotencyConflict
	}
	return ToOrder(existing), nil
}

// buildAndCommit performs the actual side effects: conditional stock
// decrements, points payment with the returned balance, the order insert, the
// item inserts, the order_pay ledger entry and the cart-item deletion.
func (u *CreateOrderUsecase) buildAndCommit(ctx context.Context, tx *gorm.DB, userID uid.ID, idempotencyKey string, req OrderRequest, addr user.AddressView, cartItems []database.CartItem, productByID map[uid.ID]database.Product) (Order, error) {
	// Verify every product is on sale with enough stock and snapshot the
	// current price and product data into the order items.
	var total int64
	orderItems := make([]database.OrderItem, 0, len(cartItems))
	for _, cartItem := range cartItems {
		productRow, ok := productByID[cartItem.ProductID]
		if !ok {
			return Order{}, ErrProductNotFound
		}
		if productRow.Status != product.StatusOnSale {
			return Order{}, ErrProductOffSale
		}
		if productRow.Stock < cartItem.Quantity {
			return Order{}, ErrInsufficientStock
		}
		lineTotal := productRow.PricePoints * int64(cartItem.Quantity)
		if total > math.MaxInt64-lineTotal {
			return Order{}, ErrInvalidOrderRequest
		}
		total += lineTotal
		orderItems = append(orderItems, database.OrderItem{
			ProductID:     productRow.ID,
			ProductName:   productRow.Name,
			ProductImage:  productRow.MainImageKey(),
			PriceSnapshot: productRow.PricePoints,
			Quantity:      cartItem.Quantity,
		})
	}

	// Conditional stock decrements (products already locked by ascending id).
	for _, cartItem := range cartItems {
		if _, err := u.products.DecrementStock(tx, ctx, cartItem.ProductID, cartItem.Quantity); err != nil {
			return Order{}, err
		}
	}

	// Conditional points payment; the returned balance feeds balance_after.
	newBalance, err := u.ledger.AdjustBalance(tx, ctx, userID, -total)
	if err != nil {
		return Order{}, translateBalanceError(err)
	}

	now := u.now().UTC()
	orderRow := database.Order{
		OrderNo:     u.orderNo(),
		UserID:      userID,
		ClientToken: idempotencyKey,
		RequestHash: ComputeOrderRequestHash(req.CartItemIDs, requestHashItemsOf(cartItems), addr),
		Status:      domorder.StatusPaid,
		TotalPoints: total,
		Receiver:    addr.Receiver,
		Phone:       addr.Phone,
		Address:     buildAddress(addr),
		PaidAt:      now,
	}
	if err := tx.WithContext(ctx).Create(&orderRow).Error; err != nil {
		return Order{}, err
	}
	for i := range orderItems {
		orderItems[i].OrderID = orderRow.ID
	}
	if err := tx.WithContext(ctx).Create(&orderItems).Error; err != nil {
		return Order{}, err
	}
	orderRow.Items = orderItems

	// The unique order_pay ledger entry in the same transaction.
	ledgerRow := database.PointsLedger{
		UserID:       userID,
		OrderID:      &orderRow.ID,
		EventKey:     payment.OrderPayEventKey(orderRow.ID),
		Type:         database.LedgerTypeOrderPay,
		Delta:        -total,
		BalanceAfter: newBalance,
		Remark:       orderRow.OrderNo,
		CreatedAt:    now,
	}
	if err := u.ledger.Create(tx, ctx, &ledgerRow); err != nil {
		return Order{}, err
	}

	// Delete the consumed cart items in the same transaction.
	if err := u.cart.DeleteByIDs(tx, ctx, userID, req.CartItemIDs); err != nil {
		return Order{}, err
	}
	return ToOrder(&orderRow), nil
}

// translateBalanceError maps the payment domain's balance sentinels onto the
// order domain's so the handler keeps a single error vocabulary.
func translateBalanceError(err error) error {
	switch {
	case errors.Is(err, payment.ErrUserNotFound):
		return ErrUserNotFound
	case errors.Is(err, payment.ErrInsufficientBalance):
		return ErrInsufficientPoints
	default:
		return err
	}
}

func validateOrderRequest(req OrderRequest) error {
	if uid.IsZero(req.AddressID) {
		return ErrInvalidOrderRequest
	}
	if len(req.CartItemIDs) == 0 {
		return ErrInvalidOrderRequest
	}
	seen := make(map[uid.ID]struct{}, len(req.CartItemIDs))
	for _, id := range req.CartItemIDs {
		if uid.IsZero(id) {
			return ErrInvalidOrderRequest
		}
		if _, ok := seen[id]; ok {
			return ErrInvalidOrderRequest
		}
		seen[id] = struct{}{}
	}
	return nil
}

func productIDsOf(cartItems []database.CartItem) []uid.ID {
	ids := make([]uid.ID, 0, len(cartItems))
	for _, item := range cartItems {
		ids = append(ids, item.ProductID)
	}
	return ids
}

func indexProducts(products []database.Product) map[uid.ID]database.Product {
	out := make(map[uid.ID]database.Product, len(products))
	for _, productRow := range products {
		out[productRow.ID] = productRow
	}
	return out
}

func requestHashItemsOf(cartItems []database.CartItem) []RequestHashItem {
	items := make([]RequestHashItem, 0, len(cartItems))
	for _, item := range cartItems {
		items = append(items, RequestHashItem{ProductID: item.ProductID, Quantity: item.Quantity})
	}
	return items
}

func buildAddress(addr user.AddressView) string {
	return strings.TrimSpace(addr.Region + " " + addr.Detail)
}

// generateOrderNo returns a unique order number under the 32-character
// column limit. The orders.order_no UNIQUE constraint is the backstop against
// random collisions.
func generateOrderNo(now time.Time) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("ORD%d", now.UnixNano())
	}
	return fmt.Sprintf("ORD%d-%s", now.Unix(), hex.EncodeToString(raw[:]))
}
