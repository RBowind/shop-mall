package cart_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func cartTestBuyerSigner(t *testing.T) *tokens.Signer {
	t.Helper()
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       24 * time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new buyer signer: %v", err)
	}
	return signer
}

func cartIssueBuyerToken(t *testing.T, signer *tokens.Signer, userID uid.ID, now time.Time) string {
	t.Helper()
	token, err := signer.Issue(fmt.Sprintf("buyer:%s", userID), 1, now)
	if err != nil {
		t.Fatalf("issue buyer token: %v", err)
	}
	return token
}

type cartHandlerFixture struct {
	db     *gorm.DB
	signer *tokens.Signer
	router http.Handler
}

func newCartHandlerFixture(t *testing.T) *cartHandlerFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	signer := cartTestBuyerSigner(t)
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
	cartService, err := cart.NewCartService(cart.CartServiceDeps{
		DB:       db,
		Products: productService,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("new cart service: %v", err)
	}
	handler, err := cart.NewCartHandler(cart.CartHandlerDeps{Service: cartService, Logger: logger})
	if err != nil {
		t.Fatalf("new cart handler: %v", err)
	}
	router := platformhttp.NewRouter(config.Config{}, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			cart.RegisterRoutes(group, cart.RouteDeps{Handler: handler, Signer: signer})
		},
	})
	return &cartHandlerFixture{db: db, signer: signer, router: router}
}

func (f *cartHandlerFixture) createUser(t *testing.T, openid string) uid.ID {
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

func (f *cartHandlerFixture) createProduct(t *testing.T, name, status string) (uid.ID, string) {
	t.Helper()
	statusValue := "on_sale"
	if status != "" {
		statusValue = status
	}
	if err := f.db.Exec(`INSERT INTO products (id, name, price_points, stock, status) VALUES (?, ?, 250, 5, ?)`, uid.New(), name, statusValue).Error; err != nil {
		t.Fatalf("insert product: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM products WHERE name = ?`, name).Row().Scan(&id); err != nil {
		t.Fatalf("read product id: %v", err)
	}
	return id, id.String()
}

func (f *cartHandlerFixture) request(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func cartEnvelopeData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope["data"].(map[string]any)
}

func TestCartEndpointsRequireAuthentication(t *testing.T) {
	fix := newCartHandlerFixture(t)
	if rec := fix.request(t, http.MethodGet, "/api/v1/cart", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("get cart without token status = %d, want 401", rec.Code)
	}
	if rec := fix.request(t, http.MethodPost, "/api/v1/cart", "", `{"product_id":"1","quantity":1}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("add cart without token status = %d, want 401", rec.Code)
	}
}

func TestCartAddGetUpdateDeleteLifecycle(t *testing.T) {
	fix := newCartHandlerFixture(t)
	userID := fix.createUser(t, "openid-handler-cart-lifecycle")
	token := cartIssueBuyerToken(t, fix.signer, userID, time.Now())
	_, productIDStr := fix.createProduct(t, "Lifecycle Product", "")

	// Add returns 200 with the product status.
	add := fix.request(t, http.MethodPost, "/api/v1/cart", token,
		`{"product_id":"`+productIDStr+`","quantity":2}`)
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d, body=%s", add.Code, add.Body.String())
	}
	added := cartEnvelopeData(t, add.Body.Bytes())
	if added["product_id"] != productIDStr || added["quantity"].(float64) != 2 {
		t.Fatalf("added item mismatch: %v", added)
	}
	productData := added["product"].(map[string]any)
	if productData["status"] != "on_sale" || productData["price_points"] != "250" {
		t.Fatalf("nested product mismatch: %v", productData)
	}
	itemID := added["id"].(string)

	// Duplicate add accumulates.
	addAgain := fix.request(t, http.MethodPost, "/api/v1/cart", token,
		`{"product_id":"`+productIDStr+`","quantity":3}`)
	if addAgain.Code != http.StatusOK {
		t.Fatalf("duplicate add status = %d", addAgain.Code)
	}
	again := cartEnvelopeData(t, addAgain.Body.Bytes())
	if again["quantity"].(float64) != 5 {
		t.Fatalf("accumulated quantity = %v, want 5", again["quantity"])
	}

	// Get returns one row.
	get := fix.request(t, http.MethodGet, "/api/v1/cart", token, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get cart status = %d", get.Code)
	}
	items := cartEnvelopeData(t, get.Body.Bytes())["list"].([]any)
	if len(items) != 1 {
		t.Fatalf("cart list length = %d, want 1", len(items))
	}

	// Update quantity.
	update := fix.request(t, http.MethodPatch, "/api/v1/cart/"+itemID, token, `{"quantity":9}`)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", update.Code, update.Body.String())
	}
	updated := cartEnvelopeData(t, update.Body.Bytes())
	if updated["quantity"].(float64) != 9 {
		t.Fatalf("updated quantity = %v, want 9", updated["quantity"])
	}

	// Delete.
	del := fix.request(t, http.MethodDelete, "/api/v1/cart/"+itemID, token, "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	delAgain := fix.request(t, http.MethodDelete, "/api/v1/cart/"+itemID, token, "")
	if delAgain.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", delAgain.Code)
	}
}

func TestCartItemOwnershipIsMasked(t *testing.T) {
	fix := newCartHandlerFixture(t)
	ownerID := fix.createUser(t, "openid-handler-cart-owner")
	otherID := fix.createUser(t, "openid-handler-cart-other")
	ownerToken := cartIssueBuyerToken(t, fix.signer, ownerID, time.Now())
	otherToken := cartIssueBuyerToken(t, fix.signer, otherID, time.Now())
	_, productIDStr := fix.createProduct(t, "Owned Cart Product", "")

	add := fix.request(t, http.MethodPost, "/api/v1/cart", ownerToken,
		`{"product_id":"`+productIDStr+`","quantity":1}`)
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d", add.Code)
	}
	itemID := cartEnvelopeData(t, add.Body.Bytes())["id"].(string)

	// The other buyer cannot update or delete the owner's item.
	if rec := fix.request(t, http.MethodPatch, "/api/v1/cart/"+itemID, otherToken, `{"quantity":5}`); rec.Code != http.StatusNotFound {
		t.Fatalf("other update status = %d, want 404", rec.Code)
	}
	if rec := fix.request(t, http.MethodDelete, "/api/v1/cart/"+itemID, otherToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("other delete status = %d, want 404", rec.Code)
	}
	// The owner still sees the item.
	get := fix.request(t, http.MethodGet, "/api/v1/cart", ownerToken, "")
	items := cartEnvelopeData(t, get.Body.Bytes())["list"].([]any)
	if len(items) != 1 {
		t.Fatalf("owner cart length = %d, want 1", len(items))
	}
}

func TestCartAddBusinessAndFormatErrors(t *testing.T) {
	fix := newCartHandlerFixture(t)
	userID := fix.createUser(t, "openid-handler-cart-errors")
	token := cartIssueBuyerToken(t, fix.signer, userID, time.Now())

	// Missing product -> 422.
	missingBody := `{"product_id":"` + uid.New().String() + `","quantity":1}`
	if rec := fix.request(t, http.MethodPost, "/api/v1/cart", token,
		missingBody); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing product status = %d, want 422", rec.Code)
	}
	// Zero quantity -> 400.
	zeroQtyBody := `{"product_id":"` + uid.New().String() + `","quantity":0}`
	if rec := fix.request(t, http.MethodPost, "/api/v1/cart", token,
		zeroQtyBody); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero quantity status = %d, want 400", rec.Code)
	}
	// Malformed product_id -> 400.
	if rec := fix.request(t, http.MethodPost, "/api/v1/cart", token,
		`{"product_id":"abc","quantity":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed product id status = %d, want 400", rec.Code)
	}
	// Malformed path id -> 400.
	if rec := fix.request(t, http.MethodPatch, "/api/v1/cart/abc", token, `{"quantity":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed item id status = %d, want 400", rec.Code)
	}
}
