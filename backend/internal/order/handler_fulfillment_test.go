package order_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	apporder "shop-mall/backend/internal/application/order"
	apppoints "shop-mall/backend/internal/application/points"
	apprefund "shop-mall/backend/internal/application/refund"
	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// adminOrderFixture wires the full buyer and admin routers together with the
// order handler so the refund, confirm, ship and refund-review routes are
// exercised end-to-end including the permission middleware.
type adminOrderFixture struct {
	db          *gorm.DB
	buyerSigner *tokens.Signer
	adminSigner *tokens.Signer
	adminSvc    *admin.Service
	rateLimiter *admin.LoginRateLimiter
	cookieCfg   config.AdminCookieConfig
	now         time.Time
	logger      *slog.Logger
	router      http.Handler
}

const adminOrderOrigin = "https://admin.example.test"

func newAdminOrderFixture(t *testing.T) *adminOrderFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	adminSvc, err := admin.NewService(admin.ServiceDeps{DB: db, Logger: logger, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
	adminSigner, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "admin-order-test-issuer",
		Audience:  "admin-order-test-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-order-test-kid",
		Keys: map[string][]byte{
			"admin-order-test-kid": []byte("admin-order-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new admin signer: %v", err)
	}
	rateLimiter := admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
		AccountLimit:  10,
		AccountWindow: time.Minute,
		IPLimit:       5,
		IPWindow:      time.Minute,
	})
	rateLimiter.SetNow(func() time.Time { return now })
	adminSvc.SetRateLimiter(rateLimiter)
	cookieCfg := config.DefaultAdminCookieConfig()
	cfg := config.Config{
		AllowedOrigins: []string{adminOrderOrigin},
		AdminCookie:    cookieCfg,
		AdminJWT: config.JWTConfig{
			Issuer:    "admin-order-test-issuer",
			Audience:  "admin-order-test-audience",
			TTL:       30 * time.Minute,
			ActiveKID: "admin-order-test-kid",
			Keys: map[string][]byte{
				"admin-order-test-kid": []byte("admin-order-test-key-material-with-at-least-32-bytes"),
			},
		},
	}

	buyerSigner := orderTestBuyerSigner(t)
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
	paymentRepo, err := payment.NewRepository(db)
	if err != nil {
		t.Fatalf("new payment repository: %v", err)
	}
	orderRepo, err := order.NewRepository(db)
	if err != nil {
		t.Fatalf("new order repository: %v", err)
	}
	createUsecase, err := apporder.NewCreateOrderUsecase(apporder.CreateOrderUsecaseDeps{
		DB: db, Orders: orderRepo, Address: addressUsecase, Cart: cartRepo, Products: productRepo, Ledger: paymentRepo,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new order usecase: %v", err)
	}
	adjuster, err := apppoints.NewAdjustPointsUsecase(apppoints.AdjustPointsUsecaseDeps{
		DB: db, Ledger: paymentRepo, Roles: adminSvc, Logger: logger,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new points usecase: %v", err)
	}
	refundUsecase, err := apprefund.NewRefundUsecase(apprefund.RefundUsecaseDeps{
		DB: db, Orders: orderRepo, Products: productRepo, Ledger: paymentRepo, Roles: adminSvc, Logger: logger,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new refund usecase: %v", err)
	}
	fulfillmentUsecase, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB: db, Orders: orderRepo, Roles: adminSvc, Logger: logger, Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new fulfillment usecase: %v", err)
	}
	service, err := order.NewOrderService(order.OrderServiceDeps{
		Repo: orderRepo, Ledger: paymentRepo,
	})
	if err != nil {
		t.Fatalf("new order service: %v", err)
	}
	handler, err := order.NewOrderHandler(order.OrderHandlerDeps{
		Service: service, Create: createUsecase, Refund: refundUsecase,
		Fulfillment: fulfillmentUsecase, Points: adjuster,
		PublicBaseURL: "https://api.shop-mall.invalid", Logger: logger,
	})
	if err != nil {
		t.Fatalf("new order handler: %v", err)
	}
	router := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			order.RegisterBuyerRoutes(group, order.BuyerRouteDeps{Handler: handler, Signer: buyerSigner})
		},
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			adminHandler, err := admin.NewHandler(admin.HandlerDeps{
				Service: adminSvc, Signer: adminSigner, RateLimiter: rateLimiter, Logger: logger,
				Cookie: cookieCfg, Now: func() time.Time { return now },
			})
			if err != nil {
				panic(err)
			}
			admin.RegisterRoutes(group, adminHandler)
			order.RegisterAdminRoutes(group, order.AdminRouteDeps{
				Handler: handler, Signer: adminSigner, Cookie: cookieCfg,
				Now:                func() time.Time { return now },
				PermissionResolver: adminSvc, AdminStateResolver: adminSvc,
			})
		},
	})
	return &adminOrderFixture{
		db: db, buyerSigner: buyerSigner, adminSigner: adminSigner, adminSvc: adminSvc,
		rateLimiter: rateLimiter, cookieCfg: cookieCfg, now: now, logger: logger, router: router,
	}
}

func (f *adminOrderFixture) createUser(t *testing.T, openid string) uid.ID {
	t.Helper()
	if err := f.db.Exec(`INSERT INTO users (id, openid, points_balance) VALUES (?, ?, 10000)`, uid.New(), openid).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *adminOrderFixture) createProduct(t *testing.T, name string, price int64, stock int32) uid.ID {
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

func (f *adminOrderFixture) createAddress(t *testing.T, userID uid.ID) uid.ID {
	t.Helper()
	addrUsecase, err := user.NewAddressService(user.AddressServiceDeps{DB: f.db})
	if err != nil {
		t.Fatalf("new address service: %v", err)
	}
	view, err := addrUsecase.Create(context.Background(), userID, user.AddressInput{
		Receiver: "收件人", Phone: "13800138000",
		Region: "北京市朝阳区", Detail: "建国路 88 号",
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	return view.ID
}

func (f *adminOrderFixture) addCart(t *testing.T, userID, productID uid.ID) uid.ID {
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

// placeOrder creates a paid order through the HTTP buyer route and returns its
// id as a decimal string.
func (f *adminOrderFixture) placeOrder(t *testing.T, userID, productID uid.ID) string {
	t.Helper()
	token := orderIssueBuyerToken(t, f.buyerSigner, userID)
	addrID := f.createAddress(t, userID)
	cartID := f.addCart(t, userID, productID)
	body := fmt.Sprintf(`{"cart_item_ids":["%s"],"address_id":"%s"}`, cartID, addrID)
	rec := f.buyerPost(t, "/api/v1/orders", token, body, map[string]string{"Idempotency-Key": fmt.Sprintf("handler-token-%s", userID)})
	if rec.Code != http.StatusOK {
		t.Fatalf("create order status = %d, body=%s", rec.Code, rec.Body.String())
	}
	return decodeEnvelope(t, rec)["data"].(map[string]any)["id"].(string)
}

func (f *adminOrderFixture) buyerPost(t *testing.T, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *adminOrderFixture) bootstrap(t *testing.T, username, role string) {
	t.Helper()
	if role == "limited" {
		mustExecAdminOrder(t, f.db, `INSERT INTO roles (id, name, remark) VALUES (?, 'limited', '')`, uid.New())
		mustExecAdminOrder(t, f.db, `INSERT INTO role_permissions (role_id, permission_id)
			SELECT r.id, p.id FROM roles r JOIN permissions p ON p.code IN ('product:read','admin:self') WHERE r.name = 'limited'`)
	}
	if _, err := f.adminSvc.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: username, Password: "Sup3rSecret!Pass", RoleName: role, ActorName: "test",
	}); err != nil {
		t.Fatalf("bootstrap %s: %v", username, err)
	}
}

func (f *adminOrderFixture) login(t *testing.T, username string) (accessToken, csrfToken string) {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"Sup3rSecret!Pass"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", adminOrderOrigin)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, raw := range rec.Header().Values("Set-Cookie") {
		parsed := adminOrderParseCookie(raw)
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

func (f *adminOrderFixture) adminPost(t *testing.T, path, body, accessToken, csrfToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", adminOrderOrigin)
	req.Header.Set("X-CSRF-Token", csrfToken)
	if accessToken != "" {
		req.AddCookie(&http.Cookie{Name: f.cookieCfg.AccessTokenName, Value: accessToken})
		req.AddCookie(&http.Cookie{Name: f.cookieCfg.CSRFName, Value: csrfToken})
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func adminOrderParseCookie(raw string) *adminOrderCookie {
	parts := strings.SplitN(raw, ";", 2)
	kv := strings.SplitN(parts[0], "=", 2)
	if len(kv) != 2 {
		return &adminOrderCookie{}
	}
	return &adminOrderCookie{Name: strings.TrimSpace(kv[0]), Value: kv[1]}
}

type adminOrderCookie struct {
	Name  string
	Value string
}

func mustExecAdminOrder(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestOrderHTTPRefundRequestAndConfirmEndToEnd(t *testing.T) {
	fix := newAdminOrderFixture(t)
	userID := fix.createUser(t, "openid-http-refund-flow")
	token := orderIssueBuyerToken(t, fix.buyerSigner, userID)
	productID := fix.createProduct(t, "http refund flow product", 100, 10)
	orderID := fix.placeOrder(t, userID, productID)

	// Request a refund on the paid order.
	requested := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", token, `{"reason":"不想要了"}`, nil)
	if requested.Code != http.StatusOK {
		t.Fatalf("refund request status = %d, body=%s", requested.Code, requested.Body.String())
	}
	if status := decodeEnvelope(t, requested)["data"].(map[string]any)["status"].(string); status != "refund_requested" {
		t.Fatalf("refund request status field = %s, want refund_requested", status)
	}

	// The same request again is a 409 state conflict.
	again := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", token, `{"reason":"不想要了"}`, nil)
	if again.Code != http.StatusConflict {
		t.Fatalf("second refund request status = %d, want 409 (body=%s)", again.Code, again.Body.String())
	}

	// A blank reason is a 400 request-format violation.
	blank := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", token, `{"reason":"   "}`, nil)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank reason status = %d, want 400 (body=%s)", blank.Code, blank.Body.String())
	}

	// Another buyer's order is a 404.
	other := fix.createUser(t, "openid-http-refund-other")
	otherToken := orderIssueBuyerToken(t, fix.buyerSigner, other)
	masked := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", otherToken, `{"reason":"别人的"}`, nil)
	if masked.Code != http.StatusNotFound {
		t.Fatalf("other-buyer refund request status = %d, want 404 (body=%s)", masked.Code, masked.Body.String())
	}
}

func TestOrderHTTPAdminShipApproveRoundTrip(t *testing.T) {
	fix := newAdminOrderFixture(t)
	userID := fix.createUser(t, "openid-http-admin-roundtrip")
	token := orderIssueBuyerToken(t, fix.buyerSigner, userID)
	productID := fix.createProduct(t, "http admin roundtrip product", 100, 10)
	orderID := fix.placeOrder(t, userID, productID)

	fix.bootstrap(t, "order-admin", "super_admin")
	accessToken, csrfToken := fix.login(t, "order-admin")

	// Ship the paid order.
	shipped := fix.adminPost(t, "/api/admin/v1/orders/"+orderID+"/ship", "", accessToken, csrfToken)
	if shipped.Code != http.StatusOK {
		t.Fatalf("ship status = %d, body=%s", shipped.Code, shipped.Body.String())
	}
	if status := decodeEnvelope(t, shipped)["data"].(map[string]any)["status"].(string); status != "shipped" {
		t.Fatalf("ship status field = %s, want shipped", status)
	}

	// The buyer confirms receipt.
	confirmed := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/confirm", token, "", nil)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body=%s", confirmed.Code, confirmed.Body.String())
	}
	if status := decodeEnvelope(t, confirmed)["data"].(map[string]any)["status"].(string); status != "completed" {
		t.Fatalf("confirm status field = %s, want completed", status)
	}

	// A confirming a completed order again is a 409.
	again := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/confirm", token, "", nil)
	if again.Code != http.StatusConflict {
		t.Fatalf("second confirm status = %d, want 409 (body=%s)", again.Code, again.Body.String())
	}
}

func TestOrderHTTPAdminRefundApproveAndReject(t *testing.T) {
	fix := newAdminOrderFixture(t)
	userID := fix.createUser(t, "openid-http-refund-admin")
	token := orderIssueBuyerToken(t, fix.buyerSigner, userID)
	productID := fix.createProduct(t, "http refund admin product", 100, 10)
	orderID := fix.placeOrder(t, userID, productID)

	fix.bootstrap(t, "refund-admin", "super_admin")
	accessToken, csrfToken := fix.login(t, "refund-admin")

	// Approve a refund that was never requested is a 409.
	premature := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/approve", "", accessToken, csrfToken)
	if premature.Code != http.StatusConflict {
		t.Fatalf("premature approve status = %d, want 409 (body=%s)", premature.Code, premature.Body.String())
	}

	// Request, then reject.
	if rec := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", token, `{"reason":"不想要了"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("refund request status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rejected := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/reject", `{"reason":"不符合退货条件"}`, accessToken, csrfToken)
	if rejected.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body=%s", rejected.Code, rejected.Body.String())
	}
	if status := decodeEnvelope(t, rejected)["data"].(map[string]any)["status"].(string); status != "paid" {
		t.Fatalf("reject status field = %s, want paid", status)
	}

	// Request again, then approve.
	if rec := fix.buyerPost(t, "/api/v1/orders/"+orderID+"/refund", token, `{"reason":"质量问题"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("second refund request status = %d, body=%s", rec.Code, rec.Body.String())
	}
	approved := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/approve", "", accessToken, csrfToken)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body=%s", approved.Code, approved.Body.String())
	}
	if status := decodeEnvelope(t, approved)["data"].(map[string]any)["status"].(string); status != "refunded" {
		t.Fatalf("approve status field = %s, want refunded", status)
	}
	// Approving the already-refunded order is a 409.
	again := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/approve", "", accessToken, csrfToken)
	if again.Code != http.StatusConflict {
		t.Fatalf("second approve status = %d, want 409 (body=%s)", again.Code, again.Body.String())
	}
}

func TestOrderHTTPAdminWithoutPermissionGets403(t *testing.T) {
	fix := newAdminOrderFixture(t)
	userID := fix.createUser(t, "openid-http-403")
	productID := fix.createProduct(t, "http 403 product", 100, 10)
	orderID := fix.placeOrder(t, userID, productID)

	// The limited role has product:read and admin:self but no order:ship or
	// refund:approve, so every admin order write must be a 403.
	fix.bootstrap(t, "limited-admin", "limited")
	accessToken, csrfToken := fix.login(t, "limited-admin")

	ship := fix.adminPost(t, "/api/admin/v1/orders/"+orderID+"/ship", "", accessToken, csrfToken)
	if ship.Code != http.StatusForbidden {
		t.Fatalf("limited ship status = %d, want 403 (body=%s)", ship.Code, ship.Body.String())
	}
	approve := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/approve", "", accessToken, csrfToken)
	if approve.Code != http.StatusForbidden {
		t.Fatalf("limited approve status = %d, want 403 (body=%s)", approve.Code, approve.Body.String())
	}
	reject := fix.adminPost(t, "/api/admin/v1/refunds/"+orderID+"/reject", `{"reason":"x"}`, accessToken, csrfToken)
	if reject.Code != http.StatusForbidden {
		t.Fatalf("limited reject status = %d, want 403 (body=%s)", reject.Code, reject.Body.String())
	}
}
