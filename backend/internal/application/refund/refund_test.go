package refund_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	apporder "shop-mall/backend/internal/application/order"
	apprefund "shop-mall/backend/internal/application/refund"
	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type refundFixture struct {
	db          *gorm.DB
	create      *apporder.CreateOrderUsecase
	refund      *apprefund.RefundUsecase
	fulfillment *apporder.FulfillmentUsecase
	address     *user.AddressService
	adminID     uid.ID
}

func newRefundFixture(t *testing.T) *refundFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO admin_users (id, username, password_hash, role_id) VALUES (?, 'refund-admin', 'x', (SELECT id FROM roles WHERE name = 'super_admin'))`,
		uid.New()).Error; err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	var adminID uid.ID
	if err := db.Raw(`SELECT id FROM admin_users WHERE username = 'refund-admin'`).Row().Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
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
	create, err := apporder.NewCreateOrderUsecase(apporder.CreateOrderUsecaseDeps{
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
	refund, err := apprefund.NewRefundUsecase(apprefund.RefundUsecaseDeps{
		DB:       db,
		Orders:   orderRepo,
		Products: productRepo,
		Ledger:   ledger,
		Now:      func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new refund usecase: %v", err)
	}
	fulfillment, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB: db, Orders: orderRepo, Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new fulfillment usecase: %v", err)
	}
	return &refundFixture{db: db, create: create, refund: refund, fulfillment: fulfillment, address: addressUsecase, adminID: adminID}
}

func (f *refundFixture) createUser(t *testing.T, openid string, points int64) uid.ID {
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

func (f *refundFixture) createProduct(t *testing.T, name string, price int64, stock int32, status string) uid.ID {
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

func (f *refundFixture) createAddress(t *testing.T, userID uid.ID) user.AddressView {
	t.Helper()
	view, err := f.address.Create(context.Background(), userID, user.AddressInput{
		Receiver: "收件人", Phone: "13800138000",
		Region: "北京市朝阳区", Detail: "建国路 88 号",
		IsDefault: false,
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	return view
}

func (f *refundFixture) addCart(t *testing.T, userID, productID uid.ID, quantity int32) uid.ID {
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

// placeOrder creates a paid order for userID buying one unit of productID.
func (f *refundFixture) placeOrder(t *testing.T, userID, productID uid.ID, token string) apporder.Order {
	t.Helper()
	cartID := f.addCart(t, userID, productID, 1)
	addr := f.createAddress(t, userID)
	order, err := f.create.Execute(context.Background(), userID, token, apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return order
}

// shipOrder moves a paid order to shipped through the fulfillment usecase.
func (f *refundFixture) shipOrder(t *testing.T, orderID uid.ID) {
	t.Helper()
	if _, err := f.fulfillment.Ship(context.Background(), f.adminID, orderID); err != nil {
		t.Fatalf("ship order: %v", err)
	}
}

func (f *refundFixture) balance(t *testing.T, userID uid.ID) int64 {
	t.Helper()
	var value int64
	if err := f.db.Raw(`SELECT points_balance FROM users WHERE id = ?`, userID).Scan(&value).Error; err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return value
}

func (f *refundFixture) stock(t *testing.T, productID uid.ID) int32 {
	t.Helper()
	var value int32
	if err := f.db.Raw(`SELECT stock FROM products WHERE id = ?`, productID).Scan(&value).Error; err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return value
}

func (f *refundFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query: %v", err)
	}
	return count
}

func (f *refundFixture) orderStatus(t *testing.T, orderID uid.ID) string {
	t.Helper()
	var status string
	if err := f.db.Raw(`SELECT status FROM orders WHERE id = ?`, orderID).Scan(&status).Error; err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}

func TestRefundRequestMovesPaidOrderToRefundRequested(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-request", 1000)
	productID := fix.createProduct(t, "refund request product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-request")

	result, err := fix.refund.Request(ctx, userID, order.ID, "不想要了")
	if err != nil {
		t.Fatalf("request refund: %v", err)
	}
	if result.Status != database.OrderStatusRefundRequested {
		t.Fatalf("status = %s, want refund_requested", result.Status)
	}
	if result.RefundReason != "不想要了" {
		t.Fatalf("refund_reason = %q, want %q", result.RefundReason, "不想要了")
	}
	if got := fix.orderStatus(t, order.ID); got != "refund_requested" {
		t.Fatalf("stored status = %s, want refund_requested", got)
	}
	// No side effects beyond the state change.
	if got := fix.balance(t, userID); got != 900 {
		t.Fatalf("balance = %d, want 900", got)
	}
	if got := fix.stock(t, productID); got != 9 {
		t.Fatalf("stock = %d, want 9", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE order_id = ? AND type = 'order_refund'`, order.ID); got != 0 {
		t.Fatalf("order_refund ledger rows = %d, want 0", got)
	}
}

func TestRefundRequestFromShippedOrderIsAllowed(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-shipped", 1000)
	productID := fix.createProduct(t, "refund shipped product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-shipped")
	fix.shipOrder(t, order.ID)

	result, err := fix.refund.Request(ctx, userID, order.ID, "发货后退款")
	if err != nil {
		t.Fatalf("request refund from shipped: %v", err)
	}
	if result.Status != database.OrderStatusRefundRequested {
		t.Fatalf("status = %s, want refund_requested", result.Status)
	}
	if result.ShippedAt == nil {
		t.Fatal("shipped_at must be preserved on a shipped-back refund request")
	}
}

func TestRefundRequestFromCompletedOrderIsStateConflict(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-completed", 1000)
	productID := fix.createProduct(t, "refund completed product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-completed")
	fix.shipOrder(t, order.ID)
	if _, err := fix.fulfillment.Confirm(ctx, userID, order.ID); err != nil {
		t.Fatalf("confirm order: %v", err)
	}

	_, err := fix.refund.Request(ctx, userID, order.ID, "完成后还要退")
	if !errors.Is(err, apporder.ErrOrderStateConflict) {
		t.Fatalf("request error = %v, want ErrOrderStateConflict", err)
	}
}

func TestRefundRequestForAnotherBuyersOrderIsNotFound(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	owner := fix.createUser(t, "openid-refund-owner", 1000)
	other := fix.createUser(t, "openid-refund-other", 1000)
	productID := fix.createProduct(t, "refund mask product", 100, 10, "on_sale")
	order := fix.placeOrder(t, owner, productID, "token-refund-mask")

	_, err := fix.refund.Request(ctx, other, order.ID, "别人的")
	if !errors.Is(err, apporder.ErrOrderNotFound) {
		t.Fatalf("request error = %v, want ErrOrderNotFound", err)
	}
	if got := fix.orderStatus(t, order.ID); got != "paid" {
		t.Fatalf("owner order status = %s, want paid untouched", got)
	}
}

func TestRefundRequestInvalidReason(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-invalid", 1000)
	productID := fix.createProduct(t, "refund invalid product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-invalid")

	if _, err := fix.refund.Request(ctx, userID, order.ID, "   "); !errors.Is(err, apprefund.ErrInvalidRefundReason) {
		t.Fatalf("blank reason error = %v, want ErrInvalidRefundReason", err)
	}
	long := make([]rune, 256)
	for i := range long {
		long[i] = '退'
	}
	if _, err := fix.refund.Request(ctx, userID, order.ID, string(long)); !errors.Is(err, apprefund.ErrInvalidRefundReason) {
		t.Fatalf("long reason error = %v, want ErrInvalidRefundReason", err)
	}
	if got := fix.orderStatus(t, order.ID); got != "paid" {
		t.Fatalf("order status = %s, want paid untouched", got)
	}
}

func TestRefundApproveRestoresPointsStockAndLedger(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-approve", 1000)
	productID := fix.createProduct(t, "refund approve product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-approve") // balance 900, stock 9
	if _, err := fix.refund.Request(ctx, userID, order.ID, "质量问题"); err != nil {
		t.Fatalf("request refund: %v", err)
	}

	result, err := fix.refund.Approve(ctx, fix.adminID, order.ID)
	if err != nil {
		t.Fatalf("approve refund: %v", err)
	}
	if result.Status != database.OrderStatusRefunded {
		t.Fatalf("status = %s, want refunded", result.Status)
	}
	if result.RefundedAt == nil {
		t.Fatal("refunded_at must be set on refunded orders")
	}
	if result.RefundReviewedByID == nil || *result.RefundReviewedByID != fix.adminID {
		t.Fatalf("refund_reviewed_by = %v, want %d", result.RefundReviewedByID, fix.adminID)
	}
	if got := fix.balance(t, userID); got != 1000 {
		t.Fatalf("balance = %d, want 1000 (restored)", got)
	}
	if got := fix.stock(t, productID); got != 10 {
		t.Fatalf("stock = %d, want 10 (restored)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE order_id = ? AND type = 'order_refund'`, order.ID); got != 1 {
		t.Fatalf("order_refund ledger rows = %d, want 1", got)
	}
	var delta int64
	if err := fix.db.Raw(`SELECT delta FROM points_ledger WHERE order_id = ? AND type = 'order_refund'`, order.ID).Scan(&delta).Error; err != nil {
		t.Fatalf("read ledger delta: %v", err)
	}
	if delta != 100 {
		t.Fatalf("ledger delta = %d, want +100", delta)
	}
	if got := fix.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'refund.approve' AND result = 'success'`); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	// The original order_pay entry is untouched (append-only ledger).
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE order_id = ? AND type = 'order_pay'`, order.ID); got != 1 {
		t.Fatalf("order_pay ledger rows = %d, want 1 preserved", got)
	}
}

func TestRefundApproveWrongStateIsConflict(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-approve-wrong", 1000)
	productID := fix.createProduct(t, "refund approve wrong product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-approve-wrong")

	// The order is still paid; no refund was requested.
	_, err := fix.refund.Approve(ctx, fix.adminID, order.ID)
	if !errors.Is(err, apporder.ErrOrderStateConflict) {
		t.Fatalf("approve error = %v, want ErrOrderStateConflict", err)
	}
	if got := fix.balance(t, userID); got != 900 {
		t.Fatalf("balance = %d, want 900 untouched", got)
	}
	if got := fix.stock(t, productID); got != 9 {
		t.Fatalf("stock = %d, want 9 untouched", got)
	}
	if got := fix.orderStatus(t, order.ID); got != "paid" {
		t.Fatalf("order status = %s, want paid untouched", got)
	}
}

func TestRefundApproveMissingOrderIsNotFound(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	_, err := fix.refund.Approve(ctx, fix.adminID, uid.ID{})
	if !errors.Is(err, apporder.ErrOrderNotFound) {
		t.Fatalf("approve error = %v, want ErrOrderNotFound", err)
	}
}

func TestRefundRejectRevertsToPaidWithoutFinancialSideEffects(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-reject", 1000)
	productID := fix.createProduct(t, "refund reject product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-reject") // balance 900, stock 9
	if _, err := fix.refund.Request(ctx, userID, order.ID, "不想要了"); err != nil {
		t.Fatalf("request refund: %v", err)
	}

	result, err := fix.refund.Reject(ctx, fix.adminID, order.ID, "不符合退货条件")
	if err != nil {
		t.Fatalf("reject refund: %v", err)
	}
	if result.Status != database.OrderStatusPaid {
		t.Fatalf("status = %s, want paid", result.Status)
	}
	if result.RefundRejectReason != "不符合退货条件" {
		t.Fatalf("refund_reject_reason = %q, want %q", result.RefundRejectReason, "不符合退货条件")
	}
	if got := fix.balance(t, userID); got != 900 {
		t.Fatalf("balance = %d, want 900 (no refund)", got)
	}
	if got := fix.stock(t, productID); got != 9 {
		t.Fatalf("stock = %d, want 9 (no restoration)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE order_id = ? AND type = 'order_refund'`, order.ID); got != 0 {
		t.Fatalf("order_refund ledger rows = %d, want 0", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'refund.reject' AND result = 'success'`); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
}

func TestRefundRejectFromShippedRequestRevertsToShipped(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-reject-shipped", 1000)
	productID := fix.createProduct(t, "refund reject shipped product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-reject-shipped")
	fix.shipOrder(t, order.ID)
	if _, err := fix.refund.Request(ctx, userID, order.ID, "发货后退款"); err != nil {
		t.Fatalf("request refund: %v", err)
	}

	result, err := fix.refund.Reject(ctx, fix.adminID, order.ID, "已发货，拒绝退款")
	if err != nil {
		t.Fatalf("reject refund: %v", err)
	}
	if result.Status != database.OrderStatusShipped {
		t.Fatalf("status = %s, want shipped", result.Status)
	}
	if result.ShippedAt == nil {
		t.Fatal("shipped_at must be preserved on a shipped-back rejection")
	}
}

func TestRefundRejectWrongStateIsConflict(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-reject-wrong", 1000)
	productID := fix.createProduct(t, "refund reject wrong product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-reject-wrong")

	_, err := fix.refund.Reject(ctx, fix.adminID, order.ID, "没申请也能拒")
	if !errors.Is(err, apporder.ErrOrderStateConflict) {
		t.Fatalf("reject error = %v, want ErrOrderStateConflict", err)
	}
}

func TestRefundRejectInvalidReason(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-reject-invalid", 1000)
	productID := fix.createProduct(t, "refund reject invalid product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-reject-invalid")
	if _, err := fix.refund.Request(ctx, userID, order.ID, "不想要了"); err != nil {
		t.Fatalf("request refund: %v", err)
	}
	if _, err := fix.refund.Reject(ctx, fix.adminID, order.ID, ""); !errors.Is(err, apprefund.ErrInvalidRefundReason) {
		t.Fatalf("blank reason error = %v, want ErrInvalidRefundReason", err)
	}
	if got := fix.orderStatus(t, order.ID); got != "refund_requested" {
		t.Fatalf("order status = %s, want refund_requested untouched", got)
	}
}

// TestRefundConcurrentApprovalPerformsSideEffectsExactlyOnce proves the
// concurrent-approval safety: N goroutines approve the same refund and exactly
// one performs the side effects. The order-row lock serializes the approvals;
// the losers re-read the committed refunded status and surface as
// ErrOrderStateConflict. Points and stock are restored exactly once, exactly
// one order_refund ledger entry survives, and the status flips exactly once.
func TestRefundConcurrentApprovalPerformsSideEffectsExactlyOnce(t *testing.T) {
	fix := newRefundFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-refund-race", 1000)
	productID := fix.createProduct(t, "refund race product", 100, 10, "on_sale")
	order := fix.placeOrder(t, userID, productID, "token-refund-race") // balance 900, stock 9
	if _, err := fix.refund.Request(ctx, userID, order.ID, "不想要了"); err != nil {
		t.Fatalf("request refund: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = fix.refund.Approve(ctx, fix.adminID, order.ID)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, apporder.ErrOrderStateConflict) {
			t.Fatalf("goroutine %d error = %v, want ErrOrderStateConflict", i, err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful approvals = %d, want exactly 1", successes)
	}
	if got := fix.balance(t, userID); got != 1000 {
		t.Fatalf("balance = %d, want 1000 (restored exactly once)", got)
	}
	if got := fix.stock(t, productID); got != 10 {
		t.Fatalf("stock = %d, want 10 (restored exactly once)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE order_id = ? AND type = 'order_refund'`, order.ID); got != 1 {
		t.Fatalf("order_refund ledger rows = %d, want exactly 1", got)
	}
	if got := fix.orderStatus(t, order.ID); got != "refunded" {
		t.Fatalf("order status = %s, want refunded (flipped exactly once)", got)
	}
}
