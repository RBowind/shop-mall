package order_test

import (
	"context"
	"errors"
	"fmt"
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

type orderRepoFixture struct {
	db      *gorm.DB
	usecase *apporder.CreateOrderUsecase
	repo    *order.Repository
	address *user.AddressService
}

func newOrderRepoFixture(t *testing.T) *orderRepoFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
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
	repo, err := order.NewRepository(db)
	if err != nil {
		t.Fatalf("new order repository: %v", err)
	}
	return &orderRepoFixture{db: db, usecase: usecase, repo: repo, address: addressUsecase}
}

func (f *orderRepoFixture) createUser(t *testing.T, openid string, points int64) uid.ID {
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

func (f *orderRepoFixture) createProduct(t *testing.T, name string, price int64) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO products (id, name, price_points, stock, status) VALUES (?, ?, ?, 100, 'on_sale')`,
		uid.New(), name, price,
	).Error; err != nil {
		t.Fatalf("insert product: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM products WHERE name = ?`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product id: %v", err)
	}
	return id
}

func (f *orderRepoFixture) createAddress(t *testing.T, userID uid.ID) uid.ID {
	t.Helper()
	view, err := f.address.Create(context.Background(), userID, user.AddressInput{
		Receiver: "收件人", Phone: "13800138000",
		Region: "北京市朝阳区", Detail: "建国路 88 号",
		IsDefault: false,
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	return view.ID
}

func (f *orderRepoFixture) addCart(t *testing.T, userID, productID uid.ID) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, ?, 1)`,
		uid.New(), userID, productID,
	).Error; err != nil {
		t.Fatalf("insert cart item: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM cart_items WHERE user_id = ? AND product_id = ?`, userID, productID).Row().Scan(&id); err != nil {
		t.Fatalf("read cart item id: %v", err)
	}
	return id
}

func (f *orderRepoFixture) createOrder(t *testing.T, userID, addressID, cartItemID uid.ID, token string) uid.ID {
	t.Helper()
	orderView, err := f.usecase.Execute(context.Background(), userID, token, apporder.OrderRequest{
		CartItemIDs: []uid.ID{cartItemID}, AddressID: addressID,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return orderView.ID
}

func TestOrderRepositoryListAndGetMaskOwnership(t *testing.T) {
	fix := newOrderRepoFixture(t)
	ctx := context.Background()
	userA := fix.createUser(t, "openid-repo-a", 1000)
	userB := fix.createUser(t, "openid-repo-b", 1000)
	productID := fix.createProduct(t, "repo product", 100)
	addrA := fix.createAddress(t, userA)
	addrB := fix.createAddress(t, userB)
	cartA := fix.addCart(t, userA, productID)
	cartB := fix.addCart(t, userB, productID)
	orderA := fix.createOrder(t, userA, addrA, cartA, "repo-token-a")
	orderB := fix.createOrder(t, userB, addrB, cartB, "repo-token-b")

	// Buyer A sees only their own order, with items preloaded.
	rows, total, err := fix.repo.ListByUser(ctx, userA, 1, 20, nil)
	if err != nil {
		t.Fatalf("list by user A: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("A list total=%d len=%d, want 1/1", total, len(rows))
	}
	if rows[0].ID != orderA || len(rows[0].Items) != 1 {
		t.Fatalf("A order id=%d items=%d, want %d/1", rows[0].ID, len(rows[0].Items), orderA)
	}

	// Ownership masking: B's order is indistinguishable from a missing one.
	got, err := fix.repo.GetByUser(ctx, userA, orderB)
	if !errors.Is(err, order.ErrOrderNotFound) || got != nil {
		t.Fatalf("GetByUser(other's order) = %v, %v; want ErrOrderNotFound", got, err)
	}
	own, err := fix.repo.GetByUser(ctx, userA, orderA)
	if err != nil {
		t.Fatalf("GetByUser(own order): %v", err)
	}
	if own.ID != orderA || len(own.Items) != 1 {
		t.Fatalf("own order id=%d items=%d, want %d/1", own.ID, len(own.Items), orderA)
	}

	// Admin surface sees both orders and any order by id.
	all, totalAll, err := fix.repo.ListAll(ctx, 1, 20, nil)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if totalAll != 2 || len(all) != 2 {
		t.Fatalf("admin list total=%d len=%d, want 2/2", totalAll, len(all))
	}
	byID, err := fix.repo.GetByID(ctx, orderB)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if byID.ID != orderB {
		t.Fatalf("get by id = %d, want %d", byID.ID, orderB)
	}
	if _, err := fix.repo.GetByID(ctx, uid.ID{}); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("get missing = %v, want ErrOrderNotFound", err)
	}
}

func TestOrderRepositoryListFiltersByStatus(t *testing.T) {
	fix := newOrderRepoFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-repo-status", 1000)
	productID := fix.createProduct(t, "status product", 100)
	addrID := fix.createAddress(t, userID)
	for i := 0; i < 3; i++ {
		cartID := fix.addCart(t, userID, productID)
		fix.createOrder(t, userID, addrID, cartID, fmt.Sprintf("repo-status-%d", i))
	}
	rows, total, err := fix.repo.ListByUser(ctx, userID, 1, 20, ptrStatus(database.OrderStatusPaid))
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("paid list total=%d len=%d, want 3/3", total, len(rows))
	}
	empty, emptyTotal, err := fix.repo.ListByUser(ctx, userID, 1, 20, ptrStatus(database.OrderStatusRefunded))
	if err != nil {
		t.Fatalf("list by refunded: %v", err)
	}
	if emptyTotal != 0 || len(empty) != 0 {
		t.Fatalf("refunded list total=%d len=%d, want 0/0", emptyTotal, len(empty))
	}
}

func ptrStatus(value database.OrderStatus) *database.OrderStatus {
	return &value
}
