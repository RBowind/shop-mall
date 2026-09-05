package product_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

const testPublicBaseURL = "https://api.example.test"

type productFixture struct {
	db            *gorm.DB
	service       *product.Service
	adminService  *admin.Service
	volume        *storage.LocalVolume
	touchRecorder *recordingToucher
	now           time.Time
	actor         product.Actor
}

// recordingToucher wraps the volume's Toucher and records every touched key,
// so tests assert WHICH keys entered the cleanup grace period instead of
// comparing wall-clock mtimes (the fixture's now is a frozen past instant).
type recordingToucher struct {
	mu      sync.Mutex
	touched []string
	inner   storage.Toucher
}

func (r *recordingToucher) Touch(ctx context.Context, key string) error {
	r.mu.Lock()
	r.touched = append(r.touched, key)
	r.mu.Unlock()
	return r.inner.Touch(ctx, key)
}

func (r *recordingToucher) touchedKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.touched...)
}

func newProductFixture(t *testing.T) *productFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	adminService, err := admin.NewService(admin.ServiceDeps{DB: db, Logger: logger, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
	volume, err := storage.NewLocalVolume(t.TempDir(), 10<<20)
	if err != nil {
		t.Fatalf("new local volume: %v", err)
	}
	repo, err := product.NewRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	recorder := &recordingToucher{inner: volume}
	service, err := product.NewService(product.ServiceDeps{
		DB:            db,
		Repository:    repo,
		Logger:        logger,
		PublicBaseURL: testPublicBaseURL,
		Now:           func() time.Time { return now },
		Toucher:       recorder,
	})
	if err != nil {
		t.Fatalf("new product service: %v", err)
	}
	// The audit rows reference the acting administrator, so the fixture
	// bootstraps a real admin and uses its id as the actor.
	if _, err := adminService.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "fixture-super",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "test",
	}); err != nil {
		t.Fatalf("bootstrap fixture admin: %v", err)
	}
	var actorID uid.ID
	if err := db.Raw(`SELECT id FROM admin_users WHERE username = 'fixture-super'`).Row().Scan(&actorID); err != nil {
		t.Fatalf("read fixture admin id: %v", err)
	}
	return &productFixture{
		db:            db,
		service:       service,
		adminService:  adminService,
		volume:        volume,
		touchRecorder: recorder,
		now:           now,
		actor:         product.Actor{AdminID: actorID, RoleName: "super_admin"},
	}
}

func (f *productFixture) create(t *testing.T, name string, price string, stock int32) product.ProductView {
	t.Helper()
	view, err := f.service.Create(context.Background(), f.actor, product.ProductInput{
		Name:        name,
		Description: "fixture",
		PricePoints: price,
		Stock:       stock,
	})
	if err != nil {
		t.Fatalf("create product %q: %v", name, err)
	}
	return view
}

func (f *productFixture) storeImage(t *testing.T, key string, content []byte) {
	t.Helper()
	if err := f.volume.Put(context.Background(), key, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("store image %s: %v", key, err)
	}
}

func TestPublicListIsOrderedByIDDescAndOnlyOnSale(t *testing.T) {
	fix := newProductFixture(t)
	first := fix.create(t, "first", "100", 5)
	second := fix.create(t, "second", "200", 6)
	fix.create(t, "third", "300", 7)
	// Take the third product off sale.
	if _, err := fix.service.Update(context.Background(), fix.actor, mustFirstID(t, fix, "third"), product.ProductUpdate{
		Status: &[]database.ProductStatus{database.ProductStatusOffSale}[0],
	}); err != nil {
		t.Fatalf("take third off sale: %v", err)
	}

	views, total, err := fix.service.ListPublic(context.Background(), 1, 20, "", "")
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(views) != 2 {
		t.Fatalf("list = %d items, want 2", len(views))
	}
	if views[0].ID != second.ID && views[0].ID != first.ID {
		t.Fatalf("unexpected first item %d", views[0].ID)
	}
	if bytes.Compare(second.ID[:], first.ID[:]) < 0 {
		t.Fatalf("fixture ordering assumption broken: second=%d first=%d", second.ID, first.ID)
	}
	if views[0].ID != second.ID || views[1].ID != first.ID {
		t.Fatalf("list not ordered by id DESC: got [%d %d], want [%d %d]",
			views[0].ID, views[1].ID, second.ID, first.ID)
	}
	for _, view := range views {
		if view.Status != database.ProductStatusOnSale {
			t.Fatalf("public list leaked off-sale product %d", view.ID)
		}
	}
}

func mustFirstID(t *testing.T, fix *productFixture, name string) uid.ID {
	t.Helper()
	var id uid.ID
	if err := fix.db.Raw(`SELECT id FROM products WHERE name = ? ORDER BY id LIMIT 1`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product %s: %v", name, err)
	}
	return id
}

func TestPublicDetailReturnsOffSaleAsNotFound(t *testing.T) {
	fix := newProductFixture(t)
	onSale := fix.create(t, "on-sale", "100", 5)
	offSale := fix.create(t, "off-sale", "150", 6)
	if _, err := fix.service.Update(context.Background(), fix.actor, offSale.ID, product.ProductUpdate{
		Status: &[]database.ProductStatus{database.ProductStatusOffSale}[0],
	}); err != nil {
		t.Fatalf("take off sale: %v", err)
	}

	got, err := fix.service.GetPublic(context.Background(), onSale.ID)
	if err != nil {
		t.Fatalf("get public on-sale: %v", err)
	}
	if got.ID != onSale.ID {
		t.Fatalf("got %d, want %d", got.ID, onSale.ID)
	}
	if _, err := fix.service.GetPublic(context.Background(), offSale.ID); !errors.Is(err, product.ErrProductNotFound) {
		t.Fatalf("off-sale detail error = %v, want ErrProductNotFound", err)
	}
	if _, err := fix.service.GetPublic(context.Background(), uid.ID{}); !errors.Is(err, product.ErrProductNotFound) {
		t.Fatalf("missing detail error = %v, want ErrProductNotFound", err)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	fix := newProductFixture(t)
	cases := []product.ProductInput{
		{Name: "", PricePoints: "100", Stock: 1},
		{Name: "x", PricePoints: "0", Stock: 1},
		{Name: "x", PricePoints: "abc", Stock: 1},
		{Name: "x", PricePoints: "100", Stock: -1},
		{Name: "x", PricePoints: "100", Stock: 1, Images: []string{"../evil.jpg"}},
		{Name: "x", PricePoints: "100", Stock: 1, Images: []string{validKeyN(t, 1), validKeyN(t, 2), validKeyN(t, 3), validKeyN(t, 4), validKeyN(t, 5), validKeyN(t, 6), validKeyN(t, 7), validKeyN(t, 8), validKeyN(t, 9), validKeyN(t, 10)}},
		{Name: "x", PricePoints: "100", Stock: 1, Status: database.ProductStatus("deleted")},
	}
	for i, input := range cases {
		if _, err := fix.service.Create(context.Background(), fix.actor, input); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestCreateStoresObjectKeyAndBuildsPublicURL(t *testing.T) {
	fix := newProductFixture(t)
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	extra := "98765432-10fe-dcba-9876-543210fedcba.png"
	fix.storeImage(t, key, []byte("image-bytes"))
	fix.storeImage(t, extra, []byte("image-bytes-2"))
	view, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "with image",
		Description: "desc",
		Images:      []string{key, extra},
		PricePoints: "250",
		Stock:       3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.PricePoints != 250 || view.Stock != 3 {
		t.Fatalf("view mismatch: %+v", view)
	}
	wantURL := testPublicBaseURL + "/static/images/" + key
	if view.MainImage != wantURL {
		t.Fatalf("main image = %q, want %q", view.MainImage, wantURL)
	}
	wantImages := []string{wantURL, testPublicBaseURL + "/static/images/" + extra}
	if len(view.Images) != 2 || view.Images[0] != wantImages[0] || view.Images[1] != wantImages[1] {
		t.Fatalf("gallery = %v, want %v", view.Images, wantImages)
	}
	var stored database.Product
	if err := fix.db.Where("id = ?", view.ID).First(&stored).Error; err != nil {
		t.Fatalf("read stored product: %v", err)
	}
	if keys := stored.ImageKeys(); len(keys) != 2 || keys[0] != key || keys[1] != extra {
		t.Fatalf("stored images should keep the object keys in order, got %v", keys)
	}
	if stored.MainImageKey() != key {
		t.Fatalf("main image key = %q, want %q", stored.MainImageKey(), key)
	}
}

// validKeyN builds a syntactically valid object key for gallery-length tests.
func validKeyN(t *testing.T, n int) string {
	t.Helper()
	return fmt.Sprintf("%08d-0000-0000-0000-000000000000.png", n)
}

func TestUpdateReplacesGalleryAndTouchesDroppedFiles(t *testing.T) {
	fix := newProductFixture(t)
	oldKey := "11111111-1111-1111-1111-111111111111.png"
	keptKey := "77777777-7777-7777-7777-777777777777.png"
	newKey := "22222222-2222-2222-2222-222222222222.png"
	fix.storeImage(t, oldKey, []byte("old"))
	fix.storeImage(t, keptKey, []byte("kept"))
	fix.storeImage(t, newKey, []byte("new"))
	view, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "replace me",
		Description: "fixture",
		Images:      []string{oldKey, keptKey},
		PricePoints: "100",
		Stock:       5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := fix.service.Update(context.Background(), fix.actor, view.ID, product.ProductUpdate{
		Images: &[]string{newKey, keptKey},
	}); err != nil {
		t.Fatalf("update gallery: %v", err)
	}
	// Exactly the dropped key enters the grace period; the kept and newly
	// added keys must NOT be touched.
	touched := fix.touchRecorder.touchedKeys()
	if !slices.Contains(touched, oldKey) {
		t.Fatalf("dropped image was not touched: touched=%v", touched)
	}
	if slices.Contains(touched, keptKey) || slices.Contains(touched, newKey) {
		t.Fatalf("kept/new images must not be touched: touched=%v", touched)
	}
	// The product now references the new gallery in order.
	got, err := fix.service.GetAdmin(context.Background(), view.ID)
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if got.MainImage != testPublicBaseURL+"/static/images/"+newKey {
		t.Fatalf("main image = %q, want new key URL", got.MainImage)
	}
	if len(got.Images) != 2 || got.Images[1] != testPublicBaseURL+"/static/images/"+keptKey {
		t.Fatalf("gallery after update = %v", got.Images)
	}
}

func TestUpdateRejectsBadStatusAndEmptyPatch(t *testing.T) {
	fix := newProductFixture(t)
	view := fix.create(t, "patch me", "100", 5)
	badStatus := database.ProductStatus("deleted")
	if _, err := fix.service.Update(context.Background(), fix.actor, view.ID, product.ProductUpdate{
		Status: &badStatus,
	}); !errors.Is(err, product.ErrInvalidProduct) {
		t.Fatalf("bad status error = %v, want ErrInvalidProduct", err)
	}
	if _, err := fix.service.Update(context.Background(), fix.actor, view.ID, product.ProductUpdate{}); !errors.Is(err, product.ErrEmptyUpdate) {
		t.Fatalf("empty update error = %v, want ErrEmptyUpdate", err)
	}
	if _, err := fix.service.Update(context.Background(), fix.actor, uid.ID{}, product.ProductUpdate{
		Name: &[]string{"renamed"}[0],
	}); !errors.Is(err, product.ErrProductNotFound) {
		t.Fatalf("missing update error = %v, want ErrProductNotFound", err)
	}
}

func TestUpdateWritesAuditWhenImageReplaced(t *testing.T) {
	fix := newProductFixture(t)
	oldKey := "33333333-3333-3333-3333-333333333333.png"
	newKey := "44444444-4444-4444-4444-444444444444.png"
	fix.storeImage(t, oldKey, []byte("old"))
	fix.storeImage(t, newKey, []byte("new"))
	view := fix.create(t, "audit me", "100", 5)
	if _, err := fix.service.Update(context.Background(), fix.actor, view.ID, product.ProductUpdate{Images: &[]string{newKey}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	var count int64
	if err := fix.db.Raw(`SELECT count(*) FROM audit_logs WHERE target_type = 'product' AND action = 'product.update' AND result = 'success'`).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count == 0 {
		t.Fatal("no product.update audit row written")
	}
}

func TestIsImageKeyReferenced(t *testing.T) {
	fix := newProductFixture(t)
	key := "55555555-5555-5555-5555-555555555555.png"
	fix.storeImage(t, key, []byte("image"))
	view := fix.create(t, "referenced", "100", 5)
	if _, err := fix.service.Update(context.Background(), fix.actor, view.ID, product.ProductUpdate{Images: &[]string{key}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	referenced, err := fix.service.IsImageKeyReferenced(context.Background(), key)
	if err != nil {
		t.Fatalf("is referenced: %v", err)
	}
	if !referenced {
		t.Fatal("key should be referenced")
	}
	orphan := "66666666-6666-6666-6666-666666666666.png"
	referenced, err = fix.service.IsImageKeyReferenced(context.Background(), orphan)
	if err != nil {
		t.Fatalf("is referenced: %v", err)
	}
	if referenced {
		t.Fatal("orphan key must not be referenced")
	}
}

func TestAdminListFiltersByStatus(t *testing.T) {
	fix := newProductFixture(t)
	onSale := fix.create(t, "on", "100", 5)
	offSale := fix.create(t, "off", "200", 6)
	if _, err := fix.service.Update(context.Background(), fix.actor, offSale.ID, product.ProductUpdate{
		Status: &[]database.ProductStatus{database.ProductStatusOffSale}[0],
	}); err != nil {
		t.Fatalf("off sale: %v", err)
	}
	all, total, err := fix.service.ListAdmin(context.Background(), 1, 20, nil)
	if err != nil {
		t.Fatalf("list admin: %v", err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("all = %d/%d, want 2/2", len(all), total)
	}
	offStatus := database.ProductStatusOffSale
	off, total, err := fix.service.ListAdmin(context.Background(), 1, 20, &offStatus)
	if err != nil {
		t.Fatalf("list off sale: %v", err)
	}
	if total != 1 || len(off) != 1 || off[0].ID != offSale.ID {
		t.Fatalf("off-sale filter = %+v, want exactly %d", off, offSale.ID)
	}
	_ = onSale
}

func TestPublicListCategoryAndKeywordFilters(t *testing.T) {
	fix := newProductFixture(t)
	// Create with a category through the service so the DB column round-trips.
	createCategorized := func(name, category string, price int64) product.ProductView {
		t.Helper()
		view, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
			Name:        name,
			Description: "categorized",
			Category:    category,
			PricePoints: fmt.Sprintf("%d", price),
			Stock:       3,
		})
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		return view
	}
	createCategorized("无线耳机", "digital", 100)
	createCategorized("机械键盘", "digital", 200)
	home := createCategorized("台灯", "home", 300)
	// Take the home product off sale so it never appears in category counts
	// or filtered listings.
	if _, err := fix.service.Update(context.Background(), fix.actor, home.ID, product.ProductUpdate{
		Status: &[]database.ProductStatus{database.ProductStatusOffSale}[0],
	}); err != nil {
		t.Fatalf("take home off sale: %v", err)
	}
	made := createCategorized("Made Digital", "digital", 400)
	createCategorized("Made Home", "home", 500)

	// Category filter narrows to the key and keeps the id-desc order.
	views, total, err := fix.service.ListPublic(context.Background(), 1, 20, "digital", "")
	if err != nil {
		t.Fatalf("list digital: %v", err)
	}
	if total != 3 || len(views) != 3 {
		t.Fatalf("digital = %d/%d, want 3/3", len(views), total)
	}
	if views[0].ID != made.ID {
		t.Fatalf("first digital = %d, want %d (id desc)", views[0].ID, made.ID)
	}
	for _, view := range views {
		if view.Category != "digital" {
			t.Fatalf("category filter leaked %q", view.Category)
		}
	}

	// Unknown category is rejected as an invalid product filter.
	if _, _, err := fix.service.ListPublic(context.Background(), 1, 20, "bogus", ""); !errors.Is(err, product.ErrInvalidProduct) {
		t.Fatalf("bogus category error = %v, want ErrInvalidProduct", err)
	}

	// Keyword matches product names case-insensitively across categories.
	views, total, err = fix.service.ListPublic(context.Background(), 1, 20, "", "made")
	if err != nil {
		t.Fatalf("list keyword: %v", err)
	}
	if total != 2 || len(views) != 2 {
		t.Fatalf("keyword = %d/%d, want 2/2", len(views), total)
	}
	for _, view := range views {
		if view.Name != "Made Digital" && view.Name != "Made Home" {
			t.Fatalf("unexpected keyword hit %+v", view)
		}
	}

	// Category + keyword compose.
	views, total, err = fix.service.ListPublic(context.Background(), 1, 20, "home", "made")
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	if total != 1 || len(views) != 1 || views[0].Name != "Made Home" {
		t.Fatalf("combined = %+v, want exactly Made Home", views)
	}
}

func TestListCategoriesPairsCatalogWithLiveCounts(t *testing.T) {
	fix := newProductFixture(t)
	fix.create(t, "未分类商品", "100", 5)
	if _, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "数码商品A",
		Description: "categorized",
		Category:    "digital",
		PricePoints: "200",
		Stock:       3,
	}); err != nil {
		t.Fatalf("create digital: %v", err)
	}
	if _, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "数码商品B",
		Description: "categorized",
		Category:    "digital",
		PricePoints: "300",
		Stock:       3,
	}); err != nil {
		t.Fatalf("create digital 2: %v", err)
	}
	if _, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "食品商品",
		Description: "categorized",
		Category:    "food",
		PricePoints: "400",
		Stock:       3,
	}); err != nil {
		t.Fatalf("create food: %v", err)
	}
	// An off-sale product must not inflate the count.
	if _, err := fix.service.Create(context.Background(), fix.actor, product.ProductInput{
		Name:        "家居下架",
		Description: "categorized",
		Category:    "home",
		PricePoints: "500",
		Stock:       3,
		Status:      database.ProductStatusOffSale,
	}); err != nil {
		t.Fatalf("create off-sale home: %v", err)
	}

	catalog, err := fix.service.ListCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(catalog) != 5 {
		t.Fatalf("catalog = %d entries, want the full static catalog of 5", len(catalog))
	}
	counts := map[string]int64{}
	for _, info := range catalog {
		counts[info.ID] = info.ProductCount
	}
	if counts["digital"] != 2 {
		t.Fatalf("digital count = %d, want 2", counts["digital"])
	}
	if counts["food"] != 1 {
		t.Fatalf("food count = %d, want 1", counts["food"])
	}
	if counts["home"] != 0 {
		t.Fatalf("off-sale home leaked into count: %d", counts["home"])
	}
	if counts["beauty"] != 0 || counts["apparel"] != 0 {
		t.Fatalf("empty categories should stay zero: %+v", counts)
	}
}
