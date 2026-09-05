package integration

import (
	"context"
	"errors"
	"fmt"
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

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// orderFixture builds the full order-creation dependency chain against the
// isolated test database. The DSN is retained so a test can open a second,
// independent connection to the same database.
type orderFixture struct {
	db      *gorm.DB
	dsn     string
	usecase *apporder.CreateOrderUsecase
	address *user.AddressService
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	db, dsn, _ := OpenTestDatabaseWithDSN(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
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
	return &orderFixture{db: db, dsn: dsn, usecase: usecase, address: addressUsecase}
}

func (f *orderFixture) createUser(t *testing.T, openid string, points int64) uid.ID {
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

func (f *orderFixture) createProduct(t *testing.T, name string, price int64, stock int32) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO products (id, name, price_points, stock, status) VALUES (?, ?, ?, ?, 'on_sale')`,
		uid.New(), name, price, stock,
	).Error; err != nil {
		t.Fatalf("insert product: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM products WHERE name = ?`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product id: %v", err)
	}
	return id
}

func (f *orderFixture) createAddress(t *testing.T, userID uid.ID) user.AddressView {
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

func (f *orderFixture) addCart(t *testing.T, userID, productID uid.ID, quantity int32) uid.ID {
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

func (f *orderFixture) balance(t *testing.T, userID uid.ID) int64 {
	t.Helper()
	var value int64
	if err := f.db.Raw(`SELECT points_balance FROM users WHERE id = ?`, userID).Scan(&value).Error; err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return value
}

func (f *orderFixture) stock(t *testing.T, productID uid.ID) int32 {
	t.Helper()
	var value int32
	if err := f.db.Raw(`SELECT stock FROM products WHERE id = ?`, productID).Scan(&value).Error; err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return value
}

func (f *orderFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query: %v", err)
	}
	return count
}

// TestOrderConcurrencyDeadlockRetryViaRunner proves that the exact transaction
// runner the order usecase calls retries a real PostgreSQL deadlock (40P01)
// and commits both transactions. Two runners lock the same two rows in the
// opposite order; the deadlock detector aborts one, the runner retries the
// complete transaction, and both succeed.
func TestOrderConcurrencyDeadlockRetryViaRunner(t *testing.T) {
	fix := newOrderFixture(t)
	first := fix.createUser(t, "openid-deadlock-a", 100)
	second := fix.createUser(t, "openid-deadlock-b", 100)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	run := func(lockA, lockB uid.ID, slot int) {
		defer wg.Done()
		errs[slot] = database.RunTransaction(ctx, fix.db, func(tx *gorm.DB) error {
			if err := tx.Exec(`SELECT id FROM users WHERE id = ? FOR UPDATE`, lockA).Error; err != nil {
				return err
			}
			if err := tx.Exec(`SELECT id FROM users WHERE id = ? FOR UPDATE`, lockB).Error; err != nil {
				return err
			}
			return nil
		})
	}
	go run(first, second, 0)
	go run(second, first, 1)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("runner %d failed after deadlock retry: %v", i, err)
		}
	}
}

// TestOrderConcurrencyOrderCreateSurvivesConflictingTransaction holds the
// selected cart row on a second connection while order creation runs. The
// order transaction blocks on the cart lock (it already holds the user row),
// the conflicting transaction releases, and the order commits. Depending on
// the interleaving the order transaction may be the deadlock victim and is
// then retried by the runner; either way the order must succeed with exactly
// one stock decrement and one points payment.
func TestOrderConcurrencyOrderCreateSurvivesConflictingTransaction(t *testing.T) {
	fix := newOrderFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-conflict-tx", 10000)
	productID := fix.createProduct(t, "conflict tx product", 100, 100)
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)

	blocker, err := gorm.Open(postgres.Open(fix.dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open blocker connection: %v", err)
	}
	blockerSQL, err := blocker.DB()
	if err != nil {
		t.Fatalf("blocker pool: %v", err)
	}
	defer func() { _ = blockerSQL.Close() }()

	// The blocker holds the cart row, then wants the user row the order
	// transaction will hold. Started first so it carries the older xid and the
	// order transaction is the likely deadlock victim.
	hasCart := make(chan struct{})
	blockerDone := make(chan error, 1)
	go func() {
		tx := blocker.Begin()
		if err := tx.Exec(`SELECT id FROM cart_items WHERE id = ? FOR UPDATE`, cartID).Error; err != nil {
			_ = tx.Rollback()
			blockerDone <- err
			return
		}
		close(hasCart)
		// Wait out the order transaction's lock acquisition, then want the user
		// row. If the order transaction already holds it, a deadlock forms.
		time.Sleep(150 * time.Millisecond)
		err := tx.Exec(`SELECT id FROM users WHERE id = ? FOR UPDATE`, userID).Error
		if err == nil {
			err = tx.Commit().Error
		} else {
			_ = tx.Rollback()
		}
		blockerDone <- err
	}()

	<-hasCart
	order, oerr := fix.usecase.Execute(ctx, userID, "token-conflict-tx", apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
	})
	if oerr != nil {
		t.Fatalf("order create under conflicting transaction: %v", oerr)
	}
	if uid.IsZero(order.ID) {
		t.Fatal("order has zero id")
	}
	if err := <-blockerDone; err != nil && !database.IsRetryableTransactionError(err) {
		t.Fatalf("blocker error: %v", err)
	}
	if got := fix.stock(t, productID); got != 99 {
		t.Fatalf("stock = %d, want 99 (exactly one decrement)", got)
	}
	if got := fix.balance(t, userID); got != 9900 {
		t.Fatalf("balance = %d, want 9900 (exactly one payment)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("orders = %d, want 1", got)
	}
}

// TestOrderConcurrencyManyOrdersPreserveStockAndBalance runs a burst of order
// creations that share stock and balance, and asserts the invariants hold
// exactly: no negative stock, no negative balance, and every successful order
// is fully reflected.
func TestOrderConcurrencyManyOrdersPreserveStockAndBalance(t *testing.T) {
	fix := newOrderFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-burst", 500)
	const products = 5
	const perStock int32 = 20
	productIDs := make([]uid.ID, products)
	cartIDs := make([]uid.ID, products)
	for i := 0; i < products; i++ {
		productIDs[i] = fix.createProduct(t, fmt.Sprintf("burst product %d", i), 100, perStock)
		cartIDs[i] = fix.addCart(t, userID, productIDs[i], 1)
	}
	addr := fix.createAddress(t, userID)

	results := make([]error, products)
	var wg sync.WaitGroup
	wg.Add(products)
	for i := 0; i < products; i++ {
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = fix.usecase.Execute(ctx, userID, fmt.Sprintf("token-burst-%d", idx), apporder.OrderRequest{
				CartItemIDs: []uid.ID{cartIDs[idx]}, AddressID: addr.ID,
			})
		}(i)
	}
	wg.Wait()
	successes := 0
	for i, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, apporder.ErrInsufficientPoints):
			// Balance capped the burst at 5 (500 / 100).
		default:
			t.Fatalf("order %d unexpected error: %v", i, err)
		}
	}
	if successes != 5 {
		t.Fatalf("successful orders = %d, want 5", successes)
	}
	if got := fix.balance(t, userID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
	for i := 0; i < products; i++ {
		if got := fix.stock(t, productIDs[i]); got != perStock-1 {
			t.Fatalf("product %d stock = %d, want %d", i, got, perStock-1)
		}
	}
}

// readDeadlocks returns the PostgreSQL deadlock-detector counter for the test
// database. A real deadlock increments it; the regression test below asserts it
// does not move while a duplicate-token loser and a concurrent replay overlap.
func readDeadlocks(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var value int64
	if err := db.Raw(`SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&value).Error; err != nil {
		t.Fatalf("read deadlock counter: %v", err)
	}
	return value
}

// waitForLockWaiters polls until at least n sessions in this database are
// blocked waiting for a lock. A session blocked on a row-level lock (SELECT ...
// FOR UPDATE or an uncommitted DELETE) reports wait_event_type='Lock' in
// pg_stat_activity. The relation-keyed pg_locks query cannot detect this state:
// PostgreSQL parks the waiting session on the holder's transactionid lock
// (relation NULL) while its own tuple lock is already granted, so the wait is
// invisible to a pg_locks join on the table oid.
func waitForLockWaiters(t *testing.T, db *gorm.DB, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Raw(
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&count).Error; err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if count >= int64(n) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("lock-waiting sessions stayed below %d within %v", n, timeout)
}

// TestOrderConcurrencyReplayDoesNotInvertLockOrderOnCartMiss reproduces the
// lock-order inversion fixed at create.go's cart-not-found re-check. A
// duplicate-token loser that ran its step-1 replay check before any order
// existed can reach step 4 with the cart already consumed by the winner, then
// re-check the token while already holding the user and address row locks. The
// pre-fix re-check re-locked the order row FOR UPDATE (user → address → order),
// inverting the documented order (order → user → address → cart → product) and
// deadlocking against a concurrent replay that holds the order row at step 1
// and wants the address row in resolveReplay. The fix makes the re-check a
// plain non-locking read: the loser is serialized behind the winner's commit by
// the user-row lock, so the order row it needs is already committed and visible.
//
// The test forces the exact lock state with three auxiliary connections (a
// controller parking the loser on the user row, a committed winner order
// inserted via SQL, and an uncommitted cart delete) and asserts the
// deadlock-detector counter does not move and both transactions return the
// winning order.
func TestOrderConcurrencyReplayDoesNotInvertLockOrderOnCartMiss(t *testing.T) {
	fix := newOrderFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-lock-inversion", 1000)
	productID := fix.createProduct(t, "lock inversion product", 100, 10)
	addr := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID, 1)
	const (
		token       = "token-lock-inversion"
		productName = "lock inversion product"
		totalPoints = int64(100)
	)
	deadlocksBefore := readDeadlocks(t, fix.db)

	// The winner order's request_hash must match what the loser's re-check
	// recomputes, so it is derived from the same inputs (cart id,
	// product/quantity and the current address snapshot).
	addrView, err := fix.address.Get(ctx, userID, addr.ID)
	if err != nil {
		t.Fatalf("get address view: %v", err)
	}
	requestHash := apporder.ComputeOrderRequestHash([]uid.ID{cartID}, []apporder.RequestHashItem{
		{ProductID: productID, Quantity: 1},
	}, addrView)
	orderNo := fmt.Sprintf("ORDINV%d", time.Now().UnixNano()%1_000_000_000)

	// Controller transaction: holds the user row (parking the loser at its
	// step-2 user-row lock) and inserts the winner order in the SAME
	// transaction. A separate orders INSERT would deadlock here: the loser's
	// SELECT ... FOR UPDATE holds an AccessExclusiveLock on the user row, and
	// the foreign key orders_user_id_fk makes the INSERT's KEY SHARE check
	// block on it. Inside the controller transaction the FK check is re-entrant
	// because the controller already holds the user row.
	ctl, err := gorm.Open(postgres.Open(fix.dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open controller connection: %v", err)
	}
	ctlSQL, err := ctl.DB()
	if err != nil {
		t.Fatalf("controller pool: %v", err)
	}
	defer func() { _ = ctlSQL.Close() }()
	ctlTx := ctl.Begin()
	if err := ctlTx.Exec(`SELECT id FROM users WHERE id = ? FOR UPDATE`, userID).Error; err != nil {
		t.Fatalf("controller lock user: %v", err)
	}
	var orderID uid.ID
	if err := ctlTx.Raw(
		`INSERT INTO orders (id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, paid_at)
		 VALUES (?, ?, ?, ?, ?, 'paid', ?, ?, ?, ?, now()) RETURNING id`,
		uid.New(), orderNo, userID, token, requestHash, totalPoints, addr.Receiver, addr.Phone, addr.Region+" "+addr.Detail,
	).Row().Scan(&orderID); err != nil {
		t.Fatalf("insert winner order: %v", err)
	}
	if err := ctlTx.Exec(
		`INSERT INTO order_items (id, order_id, product_id, product_name, product_image, price_snapshot, quantity)
		 VALUES (?, ?, ?, ?, '', ?, 1)`,
		uid.New(), orderID, productID, productName, totalPoints,
	).Error; err != nil {
		t.Fatalf("insert winner order item: %v", err)
	}
	if err := ctlTx.Exec(
		`INSERT INTO points_ledger (id, user_id, order_id, event_key, type, delta, balance_after, remark)
		 VALUES (?, ?, ?, ?, 'order_pay', ?, 900, ?)`,
		uid.New(), userID, orderID, payment.OrderPayEventKey(orderID), -totalPoints, orderNo,
	).Error; err != nil {
		t.Fatalf("insert winner order_pay ledger: %v", err)
	}

	// The loser: step 1 sees no order (the controller transaction is still
	// uncommitted), step 2 parks on the controller's user row.
	var loserOrder apporder.Order
	loserDone := make(chan error, 1)
	go func() {
		var err error
		loserOrder, err = fix.usecase.Execute(ctx, userID, token, apporder.OrderRequest{
			CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
		})
		loserDone <- err
	}()
	waitForLockWaiters(t, fix.db, 1, 5*time.Second)

	// The cart delete parked uncommitted so the loser blocks at step 4 (cart
	// lock) after it acquires the user and address rows in order.
	w2, err := gorm.Open(postgres.Open(fix.dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open cart-delete connection: %v", err)
	}
	w2SQL, err := w2.DB()
	if err != nil {
		t.Fatalf("cart-delete pool: %v", err)
	}
	defer func() { _ = w2SQL.Close() }()
	w2Tx := w2.Begin()
	if err := w2Tx.Exec(`DELETE FROM cart_items WHERE id = ? AND user_id = ?`, cartID, userID).Error; err != nil {
		t.Fatalf("park cart delete: %v", err)
	}

	// Commit the controller: the winner order becomes visible and the loser
	// acquires user + address, then parks on the cart delete above.
	if err := ctlTx.Commit().Error; err != nil {
		t.Fatalf("release user row: %v", err)
	}
	waitForLockWaiters(t, fix.db, 1, 5*time.Second)

	// The concurrent replay locks the order row at step 1 and then wants the
	// address row the loser holds. With the pre-fix code this is the deadlock
	// partner; with the fix the loser's re-check reads without locking. The
	// second lock-waiter only appears once the replay has acquired the order row
	// and is itself blocked on the loser's address lock.
	var replayOrder apporder.Order
	replayDone := make(chan error, 1)
	go func() {
		var err error
		replayOrder, err = fix.usecase.Execute(ctx, userID, token, apporder.OrderRequest{
			CartItemIDs: []uid.ID{cartID}, AddressID: addr.ID,
		})
		replayDone <- err
	}()
	waitForLockWaiters(t, fix.db, 2, 5*time.Second)

	// Commit the cart delete: the loser's step 4 now reports the cart gone and
	// the loser reaches the re-check while the replay holds the order row.
	if err := w2Tx.Commit().Error; err != nil {
		t.Fatalf("commit cart delete: %v", err)
	}

	if err := <-loserDone; err != nil {
		t.Fatalf("loser order failed: %v", err)
	}
	if err := <-replayDone; err != nil {
		t.Fatalf("replay order failed: %v", err)
	}
	deadlocksAfter := readDeadlocks(t, fix.db)

	if loserOrder.ID != orderID || replayOrder.ID != orderID {
		t.Fatalf("loser order id = %d, replay order id = %d, want both %d", loserOrder.ID, replayOrder.ID, orderID)
	}
	if deadlocksAfter != deadlocksBefore {
		t.Fatalf("deadlock counter moved from %d to %d: lock-order inversion present", deadlocksBefore, deadlocksAfter)
	}
	// Exactly one order and one order_pay ledger row survived.
	if got := fix.count(t, `SELECT count(*) FROM orders WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("orders = %d, want 1", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'order_pay'`, userID); got != 1 {
		t.Fatalf("order_pay ledger rows = %d, want 1", got)
	}
}
