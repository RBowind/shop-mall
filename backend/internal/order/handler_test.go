package order_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

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

type orderHandlerFixture struct {
	db      *gorm.DB
	signer  *tokens.Signer
	router  http.Handler
	service *order.OrderService
}

func orderTestBuyerSigner(t *testing.T) *tokens.Signer {
	t.Helper()
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "order-buyer-test-issuer",
		Audience:  "order-buyer-test-audience",
		TTL:       24 * time.Hour,
		ActiveKID: "order-buyer-test-kid",
		Keys: map[string][]byte{
			"order-buyer-test-kid": []byte("order-buyer-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new buyer signer: %v", err)
	}
	return signer
}

func orderIssueBuyerToken(t *testing.T, signer *tokens.Signer, userID uid.ID) string {
	t.Helper()
	token, err := signer.Issue(fmt.Sprintf("buyer:%s", userID), 1, time.Now())
	if err != nil {
		t.Fatalf("issue buyer token: %v", err)
	}
	return token
}

func newOrderHandlerFixture(t *testing.T) *orderHandlerFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	signer := orderTestBuyerSigner(t)
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
	adjuster, err := apppoints.NewAdjustPointsUsecase(apppoints.AdjustPointsUsecaseDeps{
		DB: db, Ledger: ledger, Logger: logger, Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new points usecase: %v", err)
	}
	refundUsecase, err := apprefund.NewRefundUsecase(apprefund.RefundUsecaseDeps{
		DB: db, Orders: orderRepo, Products: productRepo, Ledger: ledger, Logger: logger,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new refund usecase: %v", err)
	}
	fulfillmentUsecase, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB: db, Orders: orderRepo, Logger: logger, Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new fulfillment usecase: %v", err)
	}
	service, err := order.NewOrderService(order.OrderServiceDeps{
		Repo: orderRepo, Ledger: ledger,
	})
	if err != nil {
		t.Fatalf("new order service: %v", err)
	}
	handler, err := order.NewOrderHandler(order.OrderHandlerDeps{
		Service: service, Create: usecase, Refund: refundUsecase,
		Fulfillment: fulfillmentUsecase, Points: adjuster,
		PublicBaseURL: "https://api.shop-mall.invalid", Logger: logger,
	})
	if err != nil {
		t.Fatalf("new order handler: %v", err)
	}
	router := platformhttp.NewRouter(config.Config{}, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			order.RegisterBuyerRoutes(group, order.BuyerRouteDeps{Handler: handler, Signer: signer})
		},
	})
	return &orderHandlerFixture{db: db, signer: signer, router: router, service: service}
}

func (f *orderHandlerFixture) createUser(t *testing.T, openid string) uid.ID {
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

func (f *orderHandlerFixture) createProduct(t *testing.T, name string, price int64, stock int32) uid.ID {
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

func (f *orderHandlerFixture) createAddress(t *testing.T, userID uid.ID) uid.ID {
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

func (f *orderHandlerFixture) addCart(t *testing.T, userID, productID uid.ID) uid.ID {
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

func (f *orderHandlerFixture) perform(t *testing.T, method, path, token string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, req)
	return recorder
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, recorder.Body.String())
	}
	return envelope
}

func TestOrderHTTPCreateReplayReturnsSameOrderAndConflictOnDifferentHash(t *testing.T) {
	fix := newOrderHandlerFixture(t)
	userID := fix.createUser(t, "openid-http-replay")
	token := orderIssueBuyerToken(t, fix.signer, userID)
	productID := fix.createProduct(t, "http replay product", 100, 10)
	addrID := fix.createAddress(t, userID)
	cartID := fix.addCart(t, userID, productID)

	body := fmt.Sprintf(`{"cart_item_ids":["%s"],"address_id":"%s"}`, cartID, addrID)
	first := fix.perform(t, http.MethodPost, "/api/v1/orders", token, body, map[string]string{"Idempotency-Key": "http-token-a"})
	if first.Code != http.StatusOK {
		t.Fatalf("first create status = %d, want 200 (body=%s)", first.Code, first.Body.String())
	}
	firstData := decodeEnvelope(t, first)
	firstID := firstData["data"].(map[string]any)["id"].(string)

	// Replay the identical request: the cart rows were consumed but the order
	// must be served from the stored order.
	replayed := fix.perform(t, http.MethodPost, "/api/v1/orders", token, body, map[string]string{"Idempotency-Key": "http-token-a"})
	if replayed.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body=%s)", replayed.Code, replayed.Body.String())
	}
	replayData := decodeEnvelope(t, replayed)
	if id := replayData["data"].(map[string]any)["id"].(string); id != firstID {
		t.Fatalf("replayed id = %s, want %s", id, firstID)
	}

	// Same token, different cart item selection: conflict.
	otherCart := fix.addCart(t, userID, fix.createProduct(t, "http replay second", 50, 10))
	conflictBody := fmt.Sprintf(`{"cart_item_ids":["%s"],"address_id":"%s"}`, otherCart, addrID)
	conflict := fix.perform(t, http.MethodPost, "/api/v1/orders", token, conflictBody, map[string]string{"Idempotency-Key": "http-token-a"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409 (body=%s)", conflict.Code, conflict.Body.String())
	}
}

func TestOrderHTTPGetMasksOtherBuyersOrder(t *testing.T) {
	fix := newOrderHandlerFixture(t)
	userA := fix.createUser(t, "openid-http-owner")
	userB := fix.createUser(t, "openid-http-viewer")
	tokenA := orderIssueBuyerToken(t, fix.signer, userA)
	tokenB := orderIssueBuyerToken(t, fix.signer, userB)
	productID := fix.createProduct(t, "http mask product", 100, 10)
	addrA := fix.createAddress(t, userA)
	cartA := fix.addCart(t, userA, productID)
	body := fmt.Sprintf(`{"cart_item_ids":["%s"],"address_id":"%s"}`, cartA, addrA)
	created := fix.perform(t, http.MethodPost, "/api/v1/orders", tokenA, body, map[string]string{"Idempotency-Key": "http-mask-token"})
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body=%s)", created.Code, created.Body.String())
	}
	orderID := decodeEnvelope(t, created)["data"].(map[string]any)["id"].(string)

	own := fix.perform(t, http.MethodGet, "/api/v1/orders/"+orderID, tokenA, "", nil)
	if own.Code != http.StatusOK {
		t.Fatalf("owner get status = %d, want 200", own.Code)
	}
	other := fix.perform(t, http.MethodGet, "/api/v1/orders/"+orderID, tokenB, "", nil)
	if other.Code != http.StatusNotFound {
		t.Fatalf("other-buyer get status = %d, want 404 (body=%s)", other.Code, other.Body.String())
	}
}

func TestOrderHTTPCreateBusinessErrors(t *testing.T) {
	fix := newOrderHandlerFixture(t)
	userID := fix.createUser(t, "openid-http-errors")
	token := orderIssueBuyerToken(t, fix.signer, userID)
	addrID := fix.createAddress(t, userID)

	// Insufficient stock is a 422 business error.
	lowStock := fix.createProduct(t, "http low stock", 100, 1)
	lowCart := fix.addCart(t, userID, lowStock)
	if err := fix.db.Exec(`UPDATE cart_items SET quantity = 5 WHERE id = ?`, lowCart).Error; err != nil {
		t.Fatalf("bump quantity: %v", err)
	}
	body := fmt.Sprintf(`{"cart_item_ids":["%s"],"address_id":"%s"}`, lowCart, addrID)
	insufficient := fix.perform(t, http.MethodPost, "/api/v1/orders", token, body, map[string]string{"Idempotency-Key": "http-stock-token"})
	if insufficient.Code != http.StatusUnprocessableEntity {
		t.Fatalf("insufficient stock status = %d, want 422 (body=%s)", insufficient.Code, insufficient.Body.String())
	}

	// A missing Idempotency-Key is a 400 request-format violation.
	missingKey := fix.perform(t, http.MethodPost, "/api/v1/orders", token, body, nil)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400 (body=%s)", missingKey.Code, missingKey.Body.String())
	}

	// An unauthenticated request is a 401.
	noAuth := fix.perform(t, http.MethodPost, "/api/v1/orders", "", body, map[string]string{"Idempotency-Key": "http-noauth-token"})
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", noAuth.Code)
	}
}
