package order_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	apporder "shop-mall/backend/internal/application/order"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/platform/database"
)

// fulfillmentFixture extends the order-creation fixture (create_test.go) with
// the fulfillment usecase so transitions can run against real orders.
type fulfillmentFixture struct {
	*fixture
	fulfillment *apporder.FulfillmentUsecase
	adminID     uid.ID
}

func newFulfillmentFixture(t *testing.T) *fulfillmentFixture {
	t.Helper()
	base := newFixture(t)
	if err := base.db.Exec(
		`INSERT INTO admin_users (id, username, password_hash, role_id) VALUES (?, 'ship-admin', 'x', (SELECT id FROM roles WHERE name = 'super_admin'))`,
		uid.New()).Error; err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	var adminID uid.ID
	if err := base.db.Raw(`SELECT id FROM admin_users WHERE username = 'ship-admin'`).Row().Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	fulfillmentRepo, err := order.NewRepository(base.db)
	if err != nil {
		t.Fatalf("new order repository: %v", err)
	}
	fulfillment, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB: base.db, Orders: fulfillmentRepo, Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new fulfillment usecase: %v", err)
	}
	return &fulfillmentFixture{fixture: base, fulfillment: fulfillment, adminID: adminID}
}

// placeOrder creates a paid order for userID buying one unit of productID.
func (f *fulfillmentFixture) placeOrder(t *testing.T, userID, productID uid.ID, token string) apporder.Order {
	t.Helper()
	cartID := f.addCart(t, userID, productID, 1)
	addr := f.createAddress(t, userID)
	order, err := f.usecase.Execute(context.Background(), userID, token, apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return order
}

func (f *fulfillmentFixture) orderStatus(t *testing.T, orderID uid.ID) string {
	t.Helper()
	var status string
	if err := f.db.Raw(`SELECT status FROM orders WHERE id = ?`, orderID).Scan(&status).Error; err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}

func TestFulfillmentShipFlipsPaidToShipped(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-ship", 1000)
	productID := fix.createProduct(t, "ship product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-ship")

	result, err := fix.fulfillment.Ship(ctx, fix.adminID, order.ID)
	if err != nil {
		t.Fatalf("ship order: %v", err)
	}
	if result.Status != database.OrderStatusShipped {
		t.Fatalf("status = %s, want shipped", result.Status)
	}
	if result.ShippedAt == nil {
		t.Fatal("shipped_at must be set")
	}
	var shippedBy uid.ID
	if err := fix.db.Raw(`SELECT shipped_by FROM orders WHERE id = ?`, order.ID).Row().Scan(&shippedBy); err != nil {
		t.Fatalf("read shipped_by: %v", err)
	}
	if shippedBy != fix.adminID {
		t.Fatalf("shipped_by = %d, want %d", shippedBy, fix.adminID)
	}
	if got := fix.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'order.ship' AND result = 'success'`); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
}

func TestFulfillmentShipWrongStateIsConflict(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-ship-wrong", 1000)
	productID := fix.createProduct(t, "ship wrong product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-ship-wrong")
	fix.shipOrder(t, order) // already shipped

	_, err := fix.fulfillment.Ship(ctx, fix.adminID, order.ID)
	if !errors.Is(err, apporder.ErrOrderStateConflict) {
		t.Fatalf("ship error = %v, want ErrOrderStateConflict", err)
	}
}

func (f *fulfillmentFixture) shipOrder(t *testing.T, order apporder.Order) {
	t.Helper()
	if _, err := f.fulfillment.Ship(context.Background(), f.adminID, order.ID); err != nil {
		t.Fatalf("ship order: %v", err)
	}
}

func TestFulfillmentShipMissingOrderIsNotFound(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	_, err := fix.fulfillment.Ship(ctx, fix.adminID, uid.ID{})
	if !errors.Is(err, apporder.ErrOrderNotFound) {
		t.Fatalf("ship error = %v, want ErrOrderNotFound", err)
	}
}

func TestFulfillmentConfirmFlipsShippedToCompleted(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-confirm", 1000)
	productID := fix.createProduct(t, "confirm product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-confirm")
	fix.shipOrder(t, order)

	result, err := fix.fulfillment.Confirm(ctx, userID, order.ID)
	if err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	if result.Status != database.OrderStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.CompletedAt == nil {
		t.Fatal("completed_at must be set")
	}
	if got := fix.orderStatus(t, order.ID); got != "completed" {
		t.Fatalf("stored status = %s, want completed", got)
	}
}

func TestFulfillmentConfirmWrongStateIsConflict(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-confirm-wrong", 1000)
	productID := fix.createProduct(t, "confirm wrong product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-confirm-wrong")

	// The order is still paid; confirming before ship is a state conflict.
	_, err := fix.fulfillment.Confirm(ctx, userID, order.ID)
	if !errors.Is(err, apporder.ErrOrderStateConflict) {
		t.Fatalf("confirm error = %v, want ErrOrderStateConflict", err)
	}
}

func TestFulfillmentConfirmAnotherBuyersOrderIsNotFound(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	owner := fix.createUser(t, "openid-confirm-owner", 1000)
	other := fix.createUser(t, "openid-confirm-other", 1000)
	productID := fix.createProduct(t, "confirm mask product", 100, 10, "on_sale")
	order := fix.placeOrder(t, owner, productID, "token-confirm-mask")
	fix.shipOrder(t, order)

	_, err := fix.fulfillment.Confirm(ctx, other, order.ID)
	if !errors.Is(err, apporder.ErrOrderNotFound) {
		t.Fatalf("confirm error = %v, want ErrOrderNotFound", err)
	}
	if got := fix.orderStatus(t, order.ID); got != "shipped" {
		t.Fatalf("owner order status = %s, want shipped untouched", got)
	}
}

func TestFulfillmentConfirmMissingOrderIsNotFound(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-confirm-missing", 1000)
	_, err := fix.fulfillment.Confirm(ctx, userID, uid.ID{})
	if !errors.Is(err, apporder.ErrOrderNotFound) {
		t.Fatalf("confirm error = %v, want ErrOrderNotFound", err)
	}
}

// TestFulfillmentConfirmConcurrentFlipsStatusExactlyOnce runs concurrent
// confirms on the same shipped order and asserts exactly one wins.
func TestFulfillmentConfirmConcurrentFlipsStatusExactlyOnce(t *testing.T) {
	fix := newFulfillmentFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-confirm-race", 1000)
	productID := fix.createProduct(t, "confirm race product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-confirm-race")
	fix.shipOrder(t, order)

	const goroutines = 6
	results := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, results[idx] = fix.fulfillment.Confirm(ctx, userID, order.ID)
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	for i, err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, apporder.ErrOrderStateConflict) {
			t.Fatalf("goroutine %d error = %v, want ErrOrderStateConflict", i, err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful confirms = %d, want exactly 1", successes)
	}
	if got := fix.orderStatus(t, order.ID); got != "completed" {
		t.Fatalf("order status = %s, want completed (flipped exactly once)", got)
	}
}
