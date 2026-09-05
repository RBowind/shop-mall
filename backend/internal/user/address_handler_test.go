package user_test

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

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testBuyerSigner(t *testing.T) *tokens.Signer {
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

func issueBuyerToken(t *testing.T, signer *tokens.Signer, userID uid.ID, now time.Time) string {
	t.Helper()
	token, err := signer.Issue(fmt.Sprintf("buyer:%s", userID), 1, now)
	if err != nil {
		t.Fatalf("issue buyer token: %v", err)
	}
	return token
}

type addressHandlerFixture struct {
	db      *gorm.DB
	signer  *tokens.Signer
	router  http.Handler
	service *user.AddressService
}

func newAddressHandlerFixture(t *testing.T) *addressHandlerFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	signer := testBuyerSigner(t)
	service, err := user.NewAddressService(user.AddressServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("new address service: %v", err)
	}
	handler, err := user.NewAddressHandler(user.AddressHandlerDeps{Service: service, Logger: logger})
	if err != nil {
		t.Fatalf("new address handler: %v", err)
	}
	router := platformhttp.NewRouter(config.Config{}, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			user.RegisterAddressRoutes(group, user.AddressRouteDeps{Handler: handler, Signer: signer})
		},
	})
	return &addressHandlerFixture{db: db, signer: signer, router: router, service: service}
}

func (f *addressHandlerFixture) createUser(t *testing.T, openid string) uid.ID {
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

func (f *addressHandlerFixture) request(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
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

func envelopeData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope["data"].(map[string]any)
}

func TestAddressEndpointsRequireAuthentication(t *testing.T) {
	fix := newAddressHandlerFixture(t)
	rec := fix.request(t, http.MethodGet, "/api/v1/addresses", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("list without token status = %d, want 401", rec.Code)
	}
	rec = fix.request(t, http.MethodPost, "/api/v1/addresses", "",
		`{"receiver":"R","phone":"13800138000","region":"北京市","detail":"某地址"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("create without token status = %d, want 401", rec.Code)
	}
}

func TestAddressCRUDLifecycle(t *testing.T) {
	fix := newAddressHandlerFixture(t)
	userID := fix.createUser(t, "openid-handler-lifecycle")
	token := issueBuyerToken(t, fix.signer, userID, time.Now())

	create := fix.request(t, http.MethodPost, "/api/v1/addresses", token,
		`{"receiver":"张三","phone":"13800138000","region":"北京市朝阳区","detail":"建国路 88 号","is_default":true}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", create.Code, create.Body.String())
	}
	created := envelopeData(t, create.Body.Bytes())
	if created["receiver"] != "张三" || created["is_default"] != true {
		t.Fatalf("created address mismatch: %v", created)
	}
	if created["version"] != "1" {
		t.Fatalf("created version = %v, want string \"1\"", created["version"])
	}
	id := created["id"].(string)

	list := fix.request(t, http.MethodGet, "/api/v1/addresses", token, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	items := envelopeData(t, list.Body.Bytes())["list"].([]any)
	if len(items) != 1 {
		t.Fatalf("list length = %d, want 1", len(items))
	}

	update := fix.request(t, http.MethodPatch, "/api/v1/addresses/"+id, token,
		`{"receiver":"李四","region":"上海市浦东新区"}`)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", update.Code, update.Body.String())
	}
	updated := envelopeData(t, update.Body.Bytes())
	if updated["receiver"] != "李四" || updated["version"] != "2" {
		t.Fatalf("updated address mismatch: %v", updated)
	}

	del := fix.request(t, http.MethodDelete, "/api/v1/addresses/"+id, token, "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", del.Code, del.Body.String())
	}
	delAgain := fix.request(t, http.MethodDelete, "/api/v1/addresses/"+id, token, "")
	if delAgain.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", delAgain.Code)
	}
}

func TestAddressOwnershipMasksOtherBuyer(t *testing.T) {
	fix := newAddressHandlerFixture(t)
	ownerID := fix.createUser(t, "openid-handler-owner")
	otherID := fix.createUser(t, "openid-handler-other")
	ownerToken := issueBuyerToken(t, fix.signer, ownerID, time.Now())
	otherToken := issueBuyerToken(t, fix.signer, otherID, time.Now())

	created := fix.request(t, http.MethodPost, "/api/v1/addresses", ownerToken,
		`{"receiver":"Owner","phone":"13800138000","region":"北京市","detail":"某地址"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d", created.Code)
	}
	id := envelopeData(t, created.Body.Bytes())["id"].(string)

	// Other buyer cannot read, update or delete the owner's address.
	if rec := fix.request(t, http.MethodPatch, "/api/v1/addresses/"+id, otherToken,
		`{"receiver":"Hijack"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("other-buyer update status = %d, want 404", rec.Code)
	}
	if rec := fix.request(t, http.MethodDelete, "/api/v1/addresses/"+id, otherToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("other-buyer delete status = %d, want 404", rec.Code)
	}
	// The owner still sees it.
	list := fix.request(t, http.MethodGet, "/api/v1/addresses", ownerToken, "")
	items := envelopeData(t, list.Body.Bytes())["list"].([]any)
	if len(items) != 1 {
		t.Fatalf("owner list length = %d, want 1", len(items))
	}
}

func TestAddressCreateBusinessErrors(t *testing.T) {
	fix := newAddressHandlerFixture(t)
	userID := fix.createUser(t, "openid-handler-business")
	token := issueBuyerToken(t, fix.signer, userID, time.Now())

	// Malformed payload -> 400.
	if rec := fix.request(t, http.MethodPost, "/api/v1/addresses", token, `{not-json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed payload status = %d, want 400", rec.Code)
	}
	// Field rule violation -> 422.
	if rec := fix.request(t, http.MethodPost, "/api/v1/addresses", token,
		`{"receiver":"","phone":"123","region":"","detail":""}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid fields status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Invalid path id -> 400.
	if rec := fix.request(t, http.MethodPatch, "/api/v1/addresses/abc", token, `{"receiver":"X"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid path id status = %d, want 400", rec.Code)
	}
	// Empty update -> 400 (patch endpoint has no 422 in the contract).
	created := fix.request(t, http.MethodPost, "/api/v1/addresses", token,
		`{"receiver":"R","phone":"13800138000","region":"北京市","detail":"某地址"}`)
	id := envelopeData(t, created.Body.Bytes())["id"].(string)
	if rec := fix.request(t, http.MethodPatch, "/api/v1/addresses/"+id, token, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty update status = %d, want 400", rec.Code)
	}
}

func TestAddressMissingBuyerIDIsRejected(t *testing.T) {
	fix := newAddressHandlerFixture(t)
	// A token with a non-buyer subject must not clear the buyer middleware.
	token, err := fix.signer.Issue("admin:7", 1, time.Now())
	if err != nil {
		t.Fatalf("issue admin-shaped token: %v", err)
	}
	rec := fix.request(t, http.MethodGet, "/api/v1/addresses", token, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin-shaped token status = %d, want 401", rec.Code)
	}
}
