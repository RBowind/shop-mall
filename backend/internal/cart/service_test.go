package cart_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type cartServiceFixture struct {
	db      *gorm.DB
	service *cart.CartService
}

func newCartServiceFixture(t *testing.T) *cartServiceFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	repo, err := product.NewRepository(db)
	if err != nil {
		t.Fatalf("new product repository: %v", err)
	}
	productService, err := product.NewService(product.ServiceDeps{
		DB:            db,
		Repository:    repo,
		Logger:        logger,
		PublicBaseURL: "https://api.shop-mall.invalid",
	})
	if err != nil {
		t.Fatalf("new product service: %v", err)
	}
	service, err := cart.NewCartService(cart.CartServiceDeps{
		DB:       db,
		Products: productService,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("new cart service: %v", err)
	}
	return &cartServiceFixture{db: db, service: service}
}

func (f *cartServiceFixture) createUser(t *testing.T, openid string) uid.ID {
	t.Helper()
	if err := f.db.Exec(`INSERT INTO users (id, openid) VALUES (?, ?)`, uid.New(), openid).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *cartServiceFixture) createProduct(t *testing.T, name, status string) uid.ID {
	t.Helper()
	statusValue := "on_sale"
	if status != "" {
		statusValue = status
	}
	if err := f.db.Exec(`INSERT INTO products (id, name, price_points, stock, status) VALUES (?, ?, 100, 10, ?)`, uid.New(), name, statusValue).Error; err != nil {
		t.Fatalf("insert product: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM products WHERE name = ?`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product id: %v", err)
	}
	return id
}

func TestCartServiceAddAccumulatesQuantityByProduct(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-accumulate")
	productID := fix.createProduct(t, "Accumulate Product", "")
	ctx := context.Background()

	first, err := fix.service.Add(ctx, userID, productID, 2)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if first.Quantity != 2 {
		t.Fatalf("first quantity = %d, want 2", first.Quantity)
	}
	second, err := fix.service.Add(ctx, userID, productID, 3)
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if second.Quantity != 5 {
		t.Fatalf("accumulated quantity = %d, want 5", second.Quantity)
	}
	if second.ID != first.ID {
		t.Fatalf("item id changed across adds: %d vs %d", first.ID, second.ID)
	}
	var rowCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ? AND product_id = ?`, userID, productID).Scan(&rowCount).Error; err != nil {
		t.Fatalf("count cart rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("cart row count = %d, want 1", rowCount)
	}
}

func TestCartServiceConcurrentAddsAccumulateWithoutLoss(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-concurrent")
	productID := fix.createProduct(t, "Concurrent Product", "")
	ctx := context.Background()

	const goroutines = 12
	const perAdd = 2
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = fix.service.Add(ctx, userID, productID, perAdd)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d add error: %v", i, err)
		}
	}
	var quantity int32
	if err := fix.db.Raw(`SELECT quantity FROM cart_items WHERE user_id = ? AND product_id = ?`, userID, productID).Scan(&quantity).Error; err != nil {
		t.Fatalf("read accumulated quantity: %v", err)
	}
	if quantity != int32(goroutines*perAdd) {
		t.Fatalf("accumulated quantity = %d, want %d", quantity, goroutines*perAdd)
	}
}

func TestCartServiceListReturnsOnlyCurrentUsersItems(t *testing.T) {
	fix := newCartServiceFixture(t)
	userA := fix.createUser(t, "openid-cart-list-a")
	userB := fix.createUser(t, "openid-cart-list-b")
	productA := fix.createProduct(t, "List Product A", "")
	productB := fix.createProduct(t, "List Product B", "")
	ctx := context.Background()
	if _, err := fix.service.Add(ctx, userA, productA, 1); err != nil {
		t.Fatalf("A add A-product: %v", err)
	}
	if _, err := fix.service.Add(ctx, userA, productB, 2); err != nil {
		t.Fatalf("A add B-product: %v", err)
	}
	if _, err := fix.service.Add(ctx, userB, productB, 5); err != nil {
		t.Fatalf("B add B-product: %v", err)
	}
	listA, err := fix.service.List(ctx, userA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("A list length = %d, want 2", len(listA))
	}
	listB, err := fix.service.List(ctx, userB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 1 {
		t.Fatalf("B list length = %d, want 1", len(listB))
	}
	if listA[0].Product.Name != "List Product A" || listA[1].Product.Name != "List Product B" {
		t.Fatalf("A product order unexpected: %s, %s", listA[0].Product.Name, listA[1].Product.Name)
	}
}

func TestCartServiceKeepsOffSaleProductButReportsNonPurchasable(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-off-sale")
	productID := fix.createProduct(t, "Soon Off Sale", "")
	ctx := context.Background()
	item, err := fix.service.Add(ctx, userID, productID, 1)
	if err != nil {
		t.Fatalf("add on-sale product: %v", err)
	}
	if item.Product.Status != database.ProductStatusOnSale {
		t.Fatalf("product status = %q, want on_sale", item.Product.Status)
	}
	// Take the product off sale; it must stay in the cart but report off_sale.
	if err := fix.db.Exec(`UPDATE products SET status = 'off_sale' WHERE id = ?`, productID).Error; err != nil {
		t.Fatalf("set off sale: %v", err)
	}
	list, err := fix.service.List(ctx, userID)
	if err != nil {
		t.Fatalf("list after off-sale: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("off-sale product dropped from cart: length = %d, want 1", len(list))
	}
	if list[0].Product.Status != database.ProductStatusOffSale {
		t.Fatalf("product status = %q, want off_sale", list[0].Product.Status)
	}
}

func TestCartServiceAddMissingProductRejected(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-missing-product")
	ctx := context.Background()
	if _, err := fix.service.Add(ctx, userID, uid.New(), 1); !errors.Is(err, cart.ErrCartProductNotFound) {
		t.Fatalf("add missing product error = %v, want ErrCartProductNotFound", err)
	}
}

func TestCartServiceUpdateOwnedItem(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-update")
	productID := fix.createProduct(t, "Update Product", "")
	ctx := context.Background()
	item, err := fix.service.Add(ctx, userID, productID, 1)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	updated, err := fix.service.Update(ctx, userID, item.ID, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Quantity != 7 {
		t.Fatalf("updated quantity = %d, want 7", updated.Quantity)
	}
	if updated.ID != item.ID {
		t.Fatalf("updated id = %d, want %d", updated.ID, item.ID)
	}
}

func TestCartServiceOwnershipMasksOtherUsersItem(t *testing.T) {
	fix := newCartServiceFixture(t)
	owner := fix.createUser(t, "openid-cart-owner")
	other := fix.createUser(t, "openid-cart-other")
	productID := fix.createProduct(t, "Owned Product", "")
	ctx := context.Background()
	item, err := fix.service.Add(ctx, owner, productID, 3)
	if err != nil {
		t.Fatalf("owner add: %v", err)
	}
	if _, err := fix.service.Update(ctx, other, item.ID, 1); !errors.Is(err, cart.ErrCartItemNotFound) {
		t.Fatalf("other update error = %v, want ErrCartItemNotFound", err)
	}
	if err := fix.service.Delete(ctx, other, item.ID); !errors.Is(err, cart.ErrCartItemNotFound) {
		t.Fatalf("other delete error = %v, want ErrCartItemNotFound", err)
	}
	// The owner's item is untouched.
	list, err := fix.service.List(ctx, owner)
	if err != nil {
		t.Fatalf("owner list: %v", err)
	}
	if len(list) != 1 || list[0].Quantity != 3 {
		t.Fatalf("owner item unexpected after masked attempts: %+v", list)
	}
}

func TestCartServiceDeleteOwnedItem(t *testing.T) {
	fix := newCartServiceFixture(t)
	userID := fix.createUser(t, "openid-cart-delete")
	productID := fix.createProduct(t, "Delete Product", "")
	ctx := context.Background()
	item, err := fix.service.Add(ctx, userID, productID, 1)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := fix.service.Delete(ctx, userID, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := fix.service.Delete(ctx, userID, item.ID); !errors.Is(err, cart.ErrCartItemNotFound) {
		t.Fatalf("second delete error = %v, want ErrCartItemNotFound", err)
	}
	list, err := fix.service.List(ctx, userID)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list length after delete = %d, want 0", len(list))
	}
}
