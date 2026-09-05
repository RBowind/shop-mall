package order_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	apporder "shop-mall/backend/internal/application/order"
	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type fixture struct {
	db      *gorm.DB
	usecase *apporder.CreateOrderUsecase
	address *user.AddressService
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	addressUsecase, err := user.NewAddressService(user.AddressServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("new address service: %v", err)
	}
	cartRepo, err := cart.NewRepository(db)
	if err != nil {
		t.Fatalf("new cart repository: %v", err)
	}
	productRepo, err := product.NewRepository(db)
	if err != nil {
		t.Fatalf("new product repository: %v", err)
	}
	ledger, err := payment.NewRepository(db)
	if err != nil {
		t.Fatalf("new payment repository: %v", err)
	}
	orderRepo, err := order.NewRepository(db)
	if err != nil {
		t.Fatalf("new order repository: %v", err)
	}
	usecase, err := apporder.NewCreateOrderUsecase(apporder.CreateOrderUsecaseDeps{
		DB:       db,
		Orders:   orderRepo,
		Address:  addressUsecase,
		Cart:     cartRepo,
		Products: productRepo,
		Ledger:   ledger,
		Now:      func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new order usecase: %v", err)
	}
	return &fixture{db: db, usecase: usecase, address: addressUsecase}
}

func (f *fixture) createUser(t *testing.T, openid string, points int64) uid.ID {
	t.Helper()
	if err := f.db.Exec(`INSERT INTO users (id, openid, points_balance) VALUES (?, ?, ?)`, uid.New(), openid, points).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *fixture) createProduct(t *testing.T, name string, price int64, stock int32, status string) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO products (id, name, price_points, stock, status) VALUES (?, ?, ?, ?, ?)`,
		uid.New(), name, price, stock, status,
	).Error; err != nil {
		t.Fatalf("insert product: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM products WHERE name = ?`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product id: %v", err)
	}
	return id
}

func (f *fixture) createAddress(t *testing.T, userID uid.ID) user.AddressView {
	t.Helper()
	view, err := f.address.Create(context.Background(), userID, user.AddressInput{
		Receiver:  "收件人",
		Phone:     "13800138000",
		Region:    "北京市朝阳区",
		Detail:    fmt.Sprintf("建国路 %d 号", userID),
		IsDefault: false,
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	return view
}

func (f *fixture) addCart(t *testing.T, userID, productID uid.ID, quantity int32) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, ?, ?)`,
		uid.New(), userID, productID, quantity,
	).Error; err != nil {
		t.Fatalf("insert cart item: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM cart_items WHERE user_id = ? AND product_id = ?`, userID, productID).Row().Scan(&id); err != nil {
		t.Fatalf("read cart item id: %v", err)
	}
	return id
}

func (f *fixture) balance(t *testing.T, userID uid.ID) int64 {
	t.Helper()
	var value int64
	if err := f.db.Raw(`SELECT points_balance FROM users WHERE id = ?`, userID).Scan(&value).Error; err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return value
}

func (f *fixture) stock(t *testing.T, productID uid.ID) int32 {
	t.Helper()
	var value int32
	if err := f.db.Raw(`SELECT stock FROM products WHERE id = ?`, productID).Scan(&value).Error; err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return value
}

func (f *fixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query: %v", err)
	}
	return count
}

func (f *fixture) orderHash(t *testing.T, userID uid.ID, token string) (string, bool) {
	t.Helper()
	var hash string
	err := f.db.Raw(`SELECT request_hash FROM orders WHERE user_id = ? AND client_token = ?`, userID, token).Scan(&hash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read order hash: %v", err)
	}
	return hash, true
}

func TestOrderCreatePersistsAllSideEffectsAtomically(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-atomic", 1000)
	productID := fix.createProduct(t, "atomic product", 100, 10, "on_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 2)

	order, err := fix.usecase.Execute(ctx, userID, "token-atomic", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID},
		AddressID:   addr.ID,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if uid.IsZero(order.ID) || order.OrderNo == "" {
		t.Fatal("order has no id or order number")
	}
	if order.Status != database.OrderStatusPaid {
		t.Fatalf("order status = %s, want paid", order.Status)
	}
	if order.TotalPoints != 200 {
		t.Fatalf("total points = %d, want 200", order.TotalPoints)
	}
	if order.Receiver != "收件人" || order.Phone != "13800138000" {
		t.Fatalf("order address snapshot mismatch: receiver=%q phone=%q", order.Receiver, order.Phone)
	}
	if !strings.Contains(order.Address, "建国路") {
		t.Fatalf("order address = %q, want the snapshot region+detail", order.Address)
	}
	if len(order.Items) != 1 {
		t.Fatalf("order items = %d, want 1", len(order.Items))
	}
	if order.Items[0].ProductID != productID || order.Items[0].PriceSnapshot != 100 || order.Items[0].Quantity != 2 {
		t.Fatalf("order item snapshot mismatch: %+v", order.Items[0])
	}
	// Stock decremented, points paid, cart item deleted, ledger entry written.
	if got := fix.stock(t, productID); got != 8 {
		t.Fatalf("stock = %d, want 8", got)
	}
	if got := fix.balance(t, userID); got != 800 {
		t.Fatalf("balance = %d, want 800", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM cart_items WHERE id = ?`, cartID); got != 0 {
		t.Fatalf("cart item still present after order")
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'order_pay'`, userID); got != 1 {
		t.Fatalf("order_pay ledger rows = %d, want 1", got)
	}
	var delta int64
	if err := fix.db.Raw(`SELECT delta FROM points_ledger WHERE user_id = ? AND type = 'order_pay'`, userID).Scan(&delta).Error; err != nil {
		t.Fatalf("read ledger delta: %v", err)
	}
	if delta != -200 {
		t.Fatalf("ledger delta = %d, want -200", delta)
	}
	if hash, ok := fix.orderHash(t, userID, "token-atomic"); !ok || len(hash) != 64 {
		t.Fatalf("order hash missing or malformed: %q ok=%v", hash, ok)
	}
}

func TestOrderCreateReplayReturnsOriginalOrderWithoutSideEffects(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-replay", 1000)
	productID := fix.createProduct(t, "replay product", 50, 10, "on_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)

	req := apporder.OrderRequest{CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID}
	first, err := fix.usecase.Execute(ctx, userID, "token-replay", req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Replay the identical wire request. The cart rows were already consumed,
	// so the replay must be served from the stored order.
	replayed, err := fix.usecase.Execute(ctx, userID, "token-replay", req)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replayed order id = %d, want %d", replayed.ID, first.ID)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("orders = %d, want 1 after replay", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'order_pay'`, userID); got != 1 {
		t.Fatalf("ledger rows = %d, want 1 after replay", got)
	}
	if got := fix.balance(t, userID); got != 950 {
		t.Fatalf("balance = %d, want 950 (replay must not recharge)", got)
	}
}

func TestOrderCreateSameTokenDifferentHashIsConflict(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-conflict", 1000)
	productA := fix.createProduct(t, "conflict A", 50, 10, "on_sale")
	productB := fix.createProduct(t, "conflict B", 60, 10, "on_sale")
	addr := fix.createAddress(t, userID)
	cartA := fix.addCart(t, userID, productA, 1)
	cartB := fix.addCart(t, userID, productB, 1)

	if _, err := fix.usecase.Execute(ctx, userID, "token-conflict", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartA}, AddressID: addr.ID,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Same token, different cart item selection: a different logical request.
	_, err := fix.usecase.Execute(ctx, userID, "token-conflict", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartB}, AddressID: addr.ID,
	})
	if !errors.Is(err, apporder.ErrIdempotencyConflict) {
		t.Fatalf("second create error = %v, want ErrIdempotencyConflict", err)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("orders = %d, want 1 after conflict", got)
	}
}

func TestOrderCreateInsufficientStockLeavesStateUntouched(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-stock", 1000)
	productID := fix.createProduct(t, "stock product", 100, 5, "on_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 10)

	_, err := fix.usecase.Execute(ctx, userID, "token-stock", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if !errors.Is(err, apporder.ErrInsufficientStock) {
		t.Fatalf("create error = %v, want ErrInsufficientStock", err)
	}
	if got := fix.stock(t, productID); got != 5 {
		t.Fatalf("stock = %d, want 5 untouched", got)
	}
	if got := fix.balance(t, userID); got != 1000 {
		t.Fatalf("balance = %d, want 1000 untouched", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM cart_items WHERE id = ?`, cartID); got != 1 {
		t.Fatalf("cart item = %d, want 1 kept", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 0 {
		t.Fatalf("orders = %d, want 0", got)
	}
}

func TestOrderCreateInsufficientPointsLeavesStateUntouched(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points", 50)
	productID := fix.createProduct(t, "points product", 100, 10, "on_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)

	_, err := fix.usecase.Execute(ctx, userID, "token-points", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if !errors.Is(err, apporder.ErrInsufficientPoints) {
		t.Fatalf("create error = %v, want ErrInsufficientPoints", err)
	}
	if got := fix.stock(t, productID); got != 10 {
		t.Fatalf("stock = %d, want 10 untouched (payment failure must not decrement)", got)
	}
	if got := fix.balance(t, userID); got != 50 {
		t.Fatalf("balance = %d, want 50 untouched", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM cart_items WHERE id = ?`, cartID); got != 1 {
		t.Fatalf("cart item = %d, want 1 kept", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 0 {
		t.Fatalf("orders = %d, want 0", got)
	}
}

func TestOrderCreateRejectsCartItemOfAnotherBuyer(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	buyer := fix.createUser(t, "openid-buyer", 1000)
	other := fix.createUser(t, "openid-other-cart", 1000)
	productID := fix.createProduct(t, "shared product", 100, 10, "on_sale")
	addr := fix.createAddress(t, buyer)
	foreignCart := fix.addCart(t, other, productID, 1)

	_, err := fix.usecase.Execute(ctx, buyer, "token-foreign-cart", apporder.OrderRequest{
		CartItemIDs: []uid.ID{foreignCart}, AddressID: addr.ID,
	})
	if !errors.Is(err, apporder.ErrCartItemNotFound) {
		t.Fatalf("create error = %v, want ErrCartItemNotFound", err)
	}
	if got := fix.stock(t, productID); got != 10 {
		t.Fatalf("stock = %d, want 10 untouched", got)
	}
}

func TestOrderCreateRejectsAddressOfAnotherBuyer(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	buyer := fix.createUser(t, "openid-buyer-addr", 1000)
	other := fix.createUser(t, "openid-other-addr", 1000)
	productID := fix.createProduct(t, "addr product", 100, 10, "on_sale")
	cartID := fix.addCart(t, buyer, productID, 1)
	foreignAddr := fix.createAddress(t, other)

	_, err := fix.usecase.Execute(ctx, buyer, "token-foreign-addr", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: foreignAddr.ID,
	})
	if !errors.Is(err, apporder.ErrAddressNotFound) {
		t.Fatalf("create error = %v, want ErrAddressNotFound", err)
	}
}

func TestOrderCreateRejectsOffSaleProduct(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-off-sale", 1000)
	productID := fix.createProduct(t, "off sale product", 100, 10, "off_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)

	_, err := fix.usecase.Execute(ctx, userID, "token-off-sale", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if !errors.Is(err, apporder.ErrProductOffSale) {
		t.Fatalf("create error = %v, want ErrProductOffSale", err)
	}
	if got := fix.stock(t, productID); got != 10 {
		t.Fatalf("stock = %d, want 10 untouched", got)
	}
}

func TestOrderCreateStoredHashMatchesCanonicalRecomputation(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-hash", 1000)
	productA := fix.createProduct(t, "hash A", 100, 10, "on_sale")
	productB := fix.createProduct(t, "hash B", 200, 10, "on_sale")
	addr := fix.createAddress(t, userID)
	cartA := fix.addCart(t, userID, productA, 2)
	cartB := fix.addCart(t, userID, productB, 1)

	req := apporder.OrderRequest{CartItemIDs: []uid.ID{cartB, cartA}, AddressID: addr.ID}
	order, err := fix.usecase.Execute(ctx, userID, "token-hash", req)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	addrNow, err := fix.address.Get(ctx, userID, addr.ID)
	if err != nil {
		t.Fatalf("get address: %v", err)
	}
	want := apporder.ComputeOrderRequestHash(req.CartItemIDs, []apporder.RequestHashItem{
		{ProductID: productA, Quantity: 2},
		{ProductID: productB, Quantity: 1},
	}, addrNow)
	if order.RequestHash != want {
		t.Fatalf("stored hash does not match canonical recomputation")
	}
}

func TestOrderCreateConcurrentDuplicateTokenCreatesSingleOrder(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-dup-token", 10000)
	productID := fix.createProduct(t, "dup token product", 100, 100, "on_sale")
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)
	req := apporder.OrderRequest{CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID}

	const goroutines = 8
	orders := make([]uid.ID, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			order, err := fix.usecase.Execute(ctx, userID, "token-dup", req)
			if err == nil {
				orders[idx] = order.ID
			}
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()
	first := uid.ID{}
	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d error: %v", i, errs[i])
		}
		if uid.IsZero(first) {
			first = orders[i]
		}
		if orders[i] != first {
			t.Fatalf("goroutine %d order id = %s, want all to share %s", i, orders[i], first)
		}
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("orders = %d, want exactly 1", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'order_pay'`, userID); got != 1 {
		t.Fatalf("ledger rows = %d, want exactly 1", got)
	}
	if got := fix.stock(t, productID); got != 99 {
		t.Fatalf("stock = %d, want 99 (decremented exactly once)", got)
	}
	if got := fix.balance(t, userID); got != 9900 {
		t.Fatalf("balance = %d, want 9900 (paid exactly once)", got)
	}
}

func TestOrderConcurrentStockRaceNeverExceedsStock(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	productID := fix.createProduct(t, "race stock product", 10, 5, "on_sale")
	const buyers = 10
	userIDs := make([]uid.ID, buyers)
	addrID := make([]uid.ID, buyers)
	cartID := make([]uid.ID, buyers)
	for i := 0; i < buyers; i++ {
		userID := fix.createUser(t, fmt.Sprintf("openid-stock-racer-%d", i), 10000)
		userIDs[i] = userID
		addrID[i] = fix.createAddress(t, userID).ID
		cartID[i] = fix.addCart(t, userID, productID, 1)
	}
	errs := make([]error, buyers)
	var wg sync.WaitGroup
	wg.Add(buyers)
	for i := 0; i < buyers; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = fix.usecase.Execute(ctx, userIDs[idx], fmt.Sprintf("token-stock-%d", idx), apporder.OrderRequest{
				CartItemIDs: []uid.ID{cartID[idx]}, AddressID: addrID[idx],
			})
		}(i)
	}
	wg.Wait()
	successes := 0
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, apporder.ErrInsufficientStock) {
			t.Fatalf("buyer %d unexpected error: %v", i, err)
		}
	}
	if successes != 5 {
		t.Fatalf("successful orders = %d, want 5", successes)
	}
	if got := fix.stock(t, productID); got != 0 {
		t.Fatalf("stock = %d, want 0 (never negative)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders`); int(got) != successes {
		t.Fatalf("orders = %d, want %d", got, successes)
	}
}

func TestOrderConcurrentBalanceRaceNeverGoesNegative(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-balance-racer", 300)
	const items = 10
	productID := make([]uid.ID, items)
	cartID := make([]uid.ID, items)
	for i := 0; i < items; i++ {
		productID[i] = fix.createProduct(t, fmt.Sprintf("race balance product %d", i), 100, 5, "on_sale")
		cartID[i] = fix.addCart(t, userID, productID[i], 1)
	}
	addr := fix.createAddress(t, userID)
	results := make([]error, items)
	var wg sync.WaitGroup
	wg.Add(items)
	for i := 0; i < items; i++ {
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = fix.usecase.Execute(ctx, userID, fmt.Sprintf("token-balance-%d", idx), apporder.OrderRequest{
				CartItemIDs: []uid.ID{cartID[idx]}, AddressID: addr.ID,
			})
		}(i)
	}
	wg.Wait()
	successes := 0
	for i, err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, apporder.ErrInsufficientPoints) {
			t.Fatalf("order %d unexpected error: %v", i, err)
		}
	}
	if successes != 3 {
		t.Fatalf("successful orders = %d, want 3 (300 / 100)", successes)
	}
	if got := fix.balance(t, userID); got != 0 {
		t.Fatalf("balance = %d, want 0 (never negative)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); int(got) != successes {
		t.Fatalf("orders = %d, want %d", got, successes)
	}
}

func TestOrderConcurrentDifferentProductOrderDoesNotDeadlock(t *testing.T) {
	fix := newFixture(t)
	ctx := context.Background()
	userA := fix.createUser(t, "openid-lock-a", 10000)
	userB := fix.createUser(t, "openid-lock-b", 10000)
	productLow := fix.createProduct(t, "lock low product", 100, 100, "on_sale")
	productHigh := fix.createProduct(t, "lock high product", 100, 100, "on_sale")
	addrA := fix.createAddress(t, userA)
	addrB := fix.createAddress(t, userB)
	// Each buyer has both products in a cart; the orders order the products in
	// the opposite request order to exercise the ascending-lock discipline.
	cartAHigh := fix.addCart(t, userA, productHigh, 1)
	cartALow := fix.addCart(t, userA, productLow, 1)
	cartBHigh := fix.addCart(t, userB, productHigh, 1)
	cartBLow := fix.addCart(t, userB, productLow, 1)
	reqA := apporder.OrderRequest{CartItemIDs: []uid.ID{cartAHigh, cartALow}, AddressID: addrA.ID}
	reqB := apporder.OrderRequest{CartItemIDs: []uid.ID{cartBLow, cartBHigh}, AddressID: addrB.ID}

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	go func() {
		defer wg.Done()
		_, errs[0] = fix.usecase.Execute(ctx, userA, "token-lock-a", reqA)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = fix.usecase.Execute(ctx, userB, "token-lock-b", reqB)
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("order %d deadlocked or failed: %v", i, err)
		}
	}
	if got := fix.count(t, `SELECT count(*) FROM orders`); got != 2 {
		t.Fatalf("orders = %d, want 2", got)
	}
	// Each of the two orders buys one unit of each product.
	if got := fix.stock(t, productLow); got != 98 {
		t.Fatalf("low product stock = %d, want 98", got)
	}
	if got := fix.stock(t, productHigh); got != 98 {
		t.Fatalf("high product stock = %d, want 98", got)
	}
}
