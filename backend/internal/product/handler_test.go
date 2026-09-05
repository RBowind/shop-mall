package product_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type handlerFixture struct {
	db           *gorm.DB
	adminService *admin.Service
	signer       *tokens.Signer
	rateLimiter  *admin.LoginRateLimiter
	cookieCfg    config.AdminCookieConfig
	now          time.Time
	logger       *slog.Logger
	router       http.Handler
}

func newHandlerFixture(t *testing.T) *handlerFixture {
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
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "admin-test-issuer",
		Audience:  "admin-test-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-test-kid",
		Keys: map[string][]byte{
			"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	rateLimiter := admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
		AccountLimit:  10,
		AccountWindow: time.Minute,
		IPLimit:       5,
		IPWindow:      time.Minute,
	})
	rateLimiter.SetNow(func() time.Time { return now })
	adminService.SetRateLimiter(rateLimiter)

	volume, err := storage.NewLocalVolume(t.TempDir(), 10<<20)
	if err != nil {
		t.Fatalf("new volume: %v", err)
	}
	repo, err := product.NewRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	productService, err := product.NewService(product.ServiceDeps{
		DB:            db,
		Repository:    repo,
		Logger:        logger,
		PublicBaseURL: testPublicBaseURL,
		Now:           func() time.Time { return now },
		Toucher:       volume,
	})
	if err != nil {
		t.Fatalf("new product service: %v", err)
	}
	productHandler, err := product.NewHandler(product.HandlerDeps{
		Service:     productService,
		AdminViewer: adminService,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("new product handler: %v", err)
	}
	cookieCfg := config.DefaultAdminCookieConfig()
	cfg := config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    cookieCfg,
		AdminJWT: config.JWTConfig{
			Issuer:    "admin-test-issuer",
			Audience:  "admin-test-audience",
			TTL:       30 * time.Minute,
			ActiveKID: "admin-test-kid",
			Keys: map[string][]byte{
				"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
			},
		},
	}
	router := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			product.RegisterPublicRoutes(group, productHandler)
		},
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			adminHandler, err := admin.NewHandler(admin.HandlerDeps{
				Service:     adminService,
				Signer:      signer,
				RateLimiter: rateLimiter,
				Logger:      logger,
				Cookie:      cookieCfg,
				Now:         func() time.Time { return now },
			})
			if err != nil {
				panic(err)
			}
			admin.RegisterRoutes(group, adminHandler)
			product.RegisterAdminRoutes(group, product.AdminRouteDeps{
				Handler:            productHandler,
				Signer:             signer,
				Cookie:             cookieCfg,
				Now:                func() time.Time { return now },
				PermissionResolver: adminService,
				AdminStateResolver: adminService,
			})
		},
	})
	return &handlerFixture{
		db:           db,
		adminService: adminService,
		signer:       signer,
		rateLimiter:  rateLimiter,
		cookieCfg:    cookieCfg,
		now:          now,
		logger:       logger,
		router:       router,
	}
}

func (f *handlerFixture) bootstrap(t *testing.T, username, role string) {
	t.Helper()
	if role == "limited" {
		mustExec(t, f.db, `INSERT INTO roles (id, name, remark) VALUES (?, 'limited', '')`, uid.New())
		mustExec(t, f.db, `INSERT INTO role_permissions (role_id, permission_id)
			SELECT r.id, p.id FROM roles r JOIN permissions p ON p.code IN ('product:read','admin:self') WHERE r.name = 'limited'`)
	}
	if _, err := f.adminService.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  username,
		Password:  "Sup3rSecret!Pass",
		RoleName:  role,
		ActorName: "test",
	}); err != nil {
		t.Fatalf("bootstrap %s: %v", username, err)
	}
}

func (f *handlerFixture) login(t *testing.T, username, password string) (accessToken, csrfToken string) {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, raw := range rec.Header().Values("Set-Cookie") {
		parsed := parseCookie(raw)
		switch parsed.Name {
		case f.cookieCfg.AccessTokenName:
			accessToken = parsed.Value
		case f.cookieCfg.CSRFName:
			csrfToken = parsed.Value
		}
	}
	if accessToken == "" || csrfToken == "" {
		t.Fatal("login did not set both cookies")
	}
	return accessToken, csrfToken
}

func (f *handlerFixture) adminWrite(t *testing.T, method, path, body string, accessToken, csrfToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.AccessTokenName, Value: accessToken})
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.CSRFName, Value: csrfToken})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *handlerFixture) adminRead(t *testing.T, method, path string, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.AccessTokenName, Value: accessToken})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func parseCookie(raw string) *http.Cookie {
	parsed, _ := http.ParseSetCookie(raw)
	if parsed == nil {
		return &http.Cookie{}
	}
	return parsed
}

func mustExec(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

func decodeEnvelope(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func decodeProduct(t *testing.T, body []byte) map[string]any {
	t.Helper()
	envelope := decodeEnvelope(t, body)
	return envelope["data"].(map[string]any)
}

func TestPublicProductRoutes(t *testing.T) {
	fix := newHandlerFixture(t)
	// Seed two on-sale products.
	mustExec(t, fix.db, `INSERT INTO products (id, name, price_points, stock) VALUES (?, 'Hero', 100, 5)`, uid.New())
	mustExec(t, fix.db, `INSERT INTO products (id, name, price_points, stock, status) VALUES (?, 'Hidden', 200, 6, 'off_sale')`, uid.New())
	var heroID uid.ID
	if err := fix.db.Raw(`SELECT id FROM products WHERE name = 'Hero'`).Row().Scan(&heroID); err != nil {
		t.Fatalf("read hero id: %v", err)
	}

	list := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	fix.router.ServeHTTP(list, req)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	envelope := decodeEnvelope(t, list.Body.Bytes())
	data := envelope["data"].(map[string]any)
	if data["total"].(float64) != 1 {
		t.Fatalf("total = %v, want 1 (off_sale hidden)", data["total"])
	}
	items := data["list"].([]any)
	first := items[0].(map[string]any)
	if first["name"] != "Hero" {
		t.Fatalf("first item = %v, want Hero", first)
	}
	if first["status"] != "on_sale" {
		t.Fatalf("status = %v, want on_sale", first["status"])
	}
	if first["price_points"] != "100" {
		t.Fatalf("price_points = %v, want string \"100\"", first["price_points"])
	}

	detail := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/products/"+heroID.String(), nil)
	fix.router.ServeHTTP(detail, req)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}

	hiddenDetail := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/products/"+uid.New().String(), nil)
	fix.router.ServeHTTP(hiddenDetail, req)
	if hiddenDetail.Code != http.StatusNotFound {
		t.Fatalf("missing detail status = %d, want 404", hiddenDetail.Code)
	}

	badDetail := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/products/abc", nil)
	fix.router.ServeHTTP(badDetail, req)
	if badDetail.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d, want 400", badDetail.Code)
	}

	badPage := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/products?page_size=101", nil)
	fix.router.ServeHTTP(badPage, req)
	if badPage.Code != http.StatusBadRequest {
		t.Fatalf("invalid page_size status = %d, want 400", badPage.Code)
	}
}

func TestAdminProductWriteRequiresPermission(t *testing.T) {
	fix := newHandlerFixture(t)
	fix.bootstrap(t, "limited-admin", "limited")
	access, csrf := fix.login(t, "limited-admin", "Sup3rSecret!Pass")

	// The limited role has product:read but not product:write.
	rec := fix.adminWrite(t, http.MethodPost, "/api/admin/v1/products",
		`{"name":"Blocked","price_points":"100","stock":1}`, access, csrf)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create without product:write = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	// product:read is allowed.
	read := fix.adminRead(t, http.MethodGet, "/api/admin/v1/products", access)
	if read.Code != http.StatusOK {
		t.Fatalf("list with product:read = %d, want 200", read.Code)
	}
}

func TestAdminProductCreateUpdateAndRead(t *testing.T) {
	fix := newHandlerFixture(t)
	fix.bootstrap(t, "super-admin", "super_admin")
	access, csrf := fix.login(t, "super-admin", "Sup3rSecret!Pass")

	create := fix.adminWrite(t, http.MethodPost, "/api/admin/v1/products",
		`{"name":"New Product","description":"desc","price_points":"250","stock":3}`, access, csrf)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", create.Code, create.Body.String())
	}
	created := decodeProduct(t, create.Body.Bytes())
	id := created["id"].(string)
	if created["price_points"] != "250" || created["stock"].(float64) != 3 {
		t.Fatalf("created product mismatch: %v", created)
	}

	update := fix.adminWrite(t, http.MethodPatch, "/api/admin/v1/products/"+id,
		`{"name":"Renamed","status":"off_sale"}`, access, csrf)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", update.Code, update.Body.String())
	}
	updated := decodeProduct(t, update.Body.Bytes())
	if updated["name"] != "Renamed" || updated["status"] != "off_sale" {
		t.Fatalf("updated product mismatch: %v", updated)
	}

	get := fix.adminRead(t, http.MethodGet, "/api/admin/v1/products/"+id, access)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.Code)
	}
	got := decodeProduct(t, get.Body.Bytes())
	if got["status"] != "off_sale" {
		t.Fatalf("off_sale not persisted: %v", got)
	}

	missing := fix.adminRead(t, http.MethodGet, "/api/admin/v1/products/"+uid.New().String(), access)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing get status = %d, want 404", missing.Code)
	}

	// Missing required stock -> 400.
	bad := fix.adminWrite(t, http.MethodPost, "/api/admin/v1/products",
		`{"name":"No Stock","price_points":"100"}`, access, csrf)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing stock status = %d, want 400", bad.Code)
	}
	// Business rule violation -> 422.
	business := fix.adminWrite(t, http.MethodPost, "/api/admin/v1/products",
		`{"name":"Bad Price","price_points":"0","stock":1}`, access, csrf)
	if business.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad price status = %d, want 422", business.Code)
	}
}

func TestAdminProductWriteRequiresCSRF(t *testing.T) {
	fix := newHandlerFixture(t)
	fix.bootstrap(t, "csrf-admin", "super_admin")
	access, csrf := fix.login(t, "csrf-admin", "Sup3rSecret!Pass")

	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/products",
		strings.NewReader(`{"name":"No CSRF","price_points":"100","stock":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: access})
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.CSRFName, Value: csrf})
	rec := httptest.NewRecorder()
	fix.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write without X-CSRF-Token = %d, want 403", rec.Code)
	}
}
