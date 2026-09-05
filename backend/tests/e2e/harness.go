// Package e2e composes the FULL production backend (same packages and
// handlers wired by cmd/server/main.go) and serves it over a real TLS
// httptest server, so the acceptance tests drive genuine HTTP with real
// JWT/cookie/CSRF flows against a real PostgreSQL schema.
//
// Deviations from the production composition root, all test-only:
//
//   - The WeChat integration is the deterministic codeMappedClient adapter:
//     it maps frozen login codes to frozen openids so each test can create
//     several distinct buyers. The production root (cmd/server/main.go) uses
//     the real WeChat API when WECHAT_APP_ID/SECRET/ENDPOINT are configured
//     and the fixed-openid FakeClient otherwise. wx-login and /me are the
//     same production handlers the composition root registers.
//   - The admin login rate limiter uses very high budgets so the acceptance
//     suite cannot trip the per-IP budget when many admins log in from the
//     same test host. Rate limiting itself is covered by admin unit tests.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/application/access"

	applogin "shop-mall/backend/internal/application/auth"
	apporder "shop-mall/backend/internal/application/order"
	apppoints "shop-mall/backend/internal/application/points"
	apprefund "shop-mall/backend/internal/application/refund"
	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/logging"
	"shop-mall/backend/internal/platform/metrics"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/internal/user"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Deterministic E2E constants. No real AppID or secret is used anywhere; the
// fake WeChat client (T6) maps deterministic codes to deterministic openids.
const (
	adminOrigin   = "https://admin.example.test"
	publicBaseURL = "https://api.example.test"

	signupBonus = int64(100)

	buyerIssuer   = "buyer-e2e-issuer"
	buyerAudience = "buyer-e2e-audience"
	buyerKID      = "buyer-e2e-kid"
	buyerKey      = "buyer-e2e-key-material-32bytes-plus"
	adminIssuer   = "admin-e2e-issuer"
	adminAudience = "admin-e2e-audience"
	adminKID      = "admin-e2e-kid"
	adminKey      = "admin-e2e-key-material-32bytes-plus"

	superUsername    = "super-admin-e2e"
	superPassword    = "SuperAdmin#2026e2e"
	operatorUsername = "operator-e2e"
	operatorPassword = "Operator#2026e2e"

	defaultUploadLimit    = int64(2 * 1024 * 1024)
	defaultImageMaxPixels = int64(25_000_000)
)

// AdminSession is one authenticated administrator HTTP session: a cookie jar
// holding the Secure HttpOnly access-token cookie plus the readable CSRF
// cookie value the client must echo in X-CSRF-Token.
type AdminSession struct {
	Username string
	Password string
	RoleName string
	Client   *http.Client
	Origin   string
	CSRF     string
}

// envelope is the business envelope every /api/v1 and /api/admin/v1 response
// uses. Data is kept raw so each test decodes what it needs.
type envelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	TraceID string          `json:"trace_id"`
}

// Harness is one isolated running system: a fresh PostgreSQL schema, the full
// production router composition, real TLS, and pre-seeded super_admin and
// operator sessions.
type Harness struct {
	t         *testing.T
	DB        *gorm.DB
	srv       *httptest.Server
	baseURL   string
	transport *http.Transport
	buyerHTTP *http.Client

	adminCookieName string
	csrfCookieName  string
	adminOrigin     string

	adminService *admin.Service
	wechatClient *codeMappedClient

	SuperAdmin *AdminSession
	Operator   *AdminSession
}

// StartTestDB starts an isolated PostgreSQL container (or reuses
// TEST_DATABASE_URL when set), runs the forward migrations, and returns the
// handle plus a cleanup. It is used from each package's TestMain so the whole
// package shares one database.
func StartTestDB(migrationDir string) (*gorm.DB, func(), error) {
	var (
		dsn     string
		cleanup func()
	)
	if raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); raw != "" {
		dsn = raw
		cleanup = func() {}
	} else {
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, nil, fmt.Errorf("PostgreSQL integration tests skipped: Docker unavailable: %w", err)
		}
		var err error
		dsn, cleanup, err = startPostgresContainer()
		if err != nil {
			return nil, cleanup, err
		}
	}

	db, err := openPostgres(dsn, 30*time.Second)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, nil, err
	}
	if err := database.RunMigrations(context.Background(), db, migrationDir); err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}
	return db, func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		if cleanup != nil {
			cleanup()
		}
	}, nil
}

func startPostgresContainer() (string, func(), error) {
	container := fmt.Sprintf("shop-mall-e2e-test-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--rm", "--name", container,
		"-e", "POSTGRES_PASSWORD=test", "-e", "POSTGRES_DB=shop_mall_test",
		"-e", "POSTGRES_USER=test", "-p", "127.0.0.1::5432", "-d", "postgres:16-alpine")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("start PostgreSQL test container: %v: %s", err, strings.TrimSpace(string(output)))
	}
	// -v: an explicit docker rm bypasses the --rm cleanup that would otherwise
	// drop the container's anonymous volume, leaking one per test run.
	cleanup := func() { _ = exec.Command("docker", "rm", "-fv", container).Run() }

	output, err := exec.Command("docker", "port", container, "5432/tcp").CombinedOutput()
	if err != nil {
		cleanup()
		return "", cleanup, fmt.Errorf("get PostgreSQL test container port: %v: %s", err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	portIndex := strings.LastIndex(line, ":")
	if portIndex < 0 || portIndex == len(line)-1 {
		cleanup()
		return "", cleanup, fmt.Errorf("unexpected PostgreSQL test container port output %q", line)
	}
	port := line[portIndex+1:]
	if _, err := strconv.Atoi(port); err != nil {
		cleanup()
		return "", cleanup, fmt.Errorf("invalid PostgreSQL test container port %q", port)
	}
	return fmt.Sprintf("host=127.0.0.1 port=%s user=test password=test dbname=shop_mall_test sslmode=disable", port), cleanup, nil
}

func openPostgres(dsn string, timeout time.Duration) (*gorm.DB, error) {
	var db *gorm.DB
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		var err error
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil && db.Exec("SELECT 1").Error == nil {
				return db, nil
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("PostgreSQL did not become ready within %s: %w", timeout, lastErr)
}

// ResetDB empties every mutable table while keeping the seeded roles and
// permissions intact. The append-only audit trigger is temporarily disabled so
// the delete succeeds; it is re-enabled immediately after.
func ResetDB(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("reset db: database is required")
	}
	statements := []string{
		`ALTER TABLE audit_logs DISABLE TRIGGER audit_logs_append_only`,
		`ALTER TABLE audit_logs DISABLE TRIGGER audit_logs_append_only_truncate`,
		`DELETE FROM order_items`,
		`DELETE FROM points_ledger`,
		`DELETE FROM orders`,
		`DELETE FROM cart_items`,
		`DELETE FROM user_addresses`,
		`DELETE FROM products`,
		`DELETE FROM users`,
		`DELETE FROM audit_logs`,
		`DELETE FROM admin_users`,
		`ALTER TABLE audit_logs ENABLE TRIGGER audit_logs_append_only_truncate`,
		`ALTER TABLE audit_logs ENABLE TRIGGER audit_logs_append_only`,
	}
	for _, statement := range statements {
		if err := db.WithContext(ctx).Exec(statement).Error; err != nil {
			return fmt.Errorf("reset db: %s: %w", statement, err)
		}
	}
	return nil
}

// NewHarness builds the full production composition against db and serves it
// over TLS. Each test gets a freshly reset schema and its own admin sessions.
func NewHarness(t *testing.T, db *gorm.DB) *Harness {
	t.Helper()
	if db == nil {
		t.Skip("e2e harness: no test database (set TEST_DATABASE_URL or start Docker)")
	}
	if err := ResetDB(context.Background(), db); err != nil {
		t.Fatalf("reset database: %v", err)
	}

	logger := logging.New(io.Discard, slog.LevelError)
	appMetrics := metrics.New()

	buyerCfg := config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte(buyerKey)},
	}
	adminCfg := config.JWTConfig{
		Issuer: adminIssuer, Audience: adminAudience, TTL: 30 * time.Minute,
		ActiveKID: adminKID, Keys: map[string][]byte{adminKID: []byte(adminKey)},
	}
	adminSigner, err := tokens.NewSigner(adminCfg)
	if err != nil {
		t.Fatalf("admin signer: %v", err)
	}
	buyerSigner, err := tokens.NewSigner(buyerCfg)
	if err != nil {
		t.Fatalf("buyer signer: %v", err)
	}

	nowUTC := func() time.Time { return time.Now().UTC() }

	adminService, err := admin.NewService(admin.ServiceDeps{
		DB: db, Logger: logger, Now: nowUTC,
		RateLimiter: admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
			AccountLimit: 100000, AccountWindow: time.Minute,
			IPLimit: 100000, IPWindow: time.Minute,
		}),
		Metrics: appMetrics,
	})
	if err != nil {
		t.Fatalf("admin service: %v", err)
	}
	accessUsecase, err := access.NewAccessUsecase(access.AccessUsecaseDeps{DB: db, Service: adminService, Logger: logger, Now: nowUTC})
	if err != nil {
		t.Fatalf("access usecase: %v", err)
	}
	adminHandler, err := admin.NewHandler(admin.HandlerDeps{
		Service:         adminService,
		Signer:          adminSigner,
		RateLimiter:     admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{AccountLimit: 100000, AccountWindow: time.Minute, IPLimit: 100000, IPWindow: time.Minute}),
		PasswordChanger: passwordChangerAdapter{usecase: accessUsecase},
		Logger:          logger,
		Cookie:          config.DefaultAdminCookieConfig(),
		Now:             nowUTC,
	})
	if err != nil {
		t.Fatalf("admin handler: %v", err)
	}

	imageStorage, err := storage.NewLocalVolume(filepath.Join(t.TempDir(), "images"), 1<<30)
	if err != nil {
		t.Fatalf("image storage: %v", err)
	}
	productRepo, err := product.NewRepository(db)
	if err != nil {
		t.Fatalf("product repository: %v", err)
	}
	productService, err := product.NewService(product.ServiceDeps{
		DB: db, Repository: productRepo, Logger: logger,
		PublicBaseURL: publicBaseURL, Now: nowUTC, Toucher: imageStorage,
	})
	if err != nil {
		t.Fatalf("product service: %v", err)
	}
	productHandler, err := product.NewHandler(product.HandlerDeps{Service: productService, AdminViewer: adminService, Logger: logger})
	if err != nil {
		t.Fatalf("product handler: %v", err)
	}

	addressService, err := user.NewAddressService(user.AddressServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("address service: %v", err)
	}
	addressHandler, err := user.NewAddressHandler(user.AddressHandlerDeps{Service: addressService, Logger: logger})
	if err != nil {
		t.Fatalf("address handler: %v", err)
	}

	cartService, err := cart.NewCartService(cart.CartServiceDeps{DB: db, Products: productService, Logger: logger})
	if err != nil {
		t.Fatalf("cart service: %v", err)
	}
	cartHandler, err := cart.NewCartHandler(cart.CartHandlerDeps{Service: cartService, Logger: logger})
	if err != nil {
		t.Fatalf("cart handler: %v", err)
	}
	cartRepo, err := cart.NewRepository(db)
	if err != nil {
		t.Fatalf("cart repository: %v", err)
	}
	paymentRepo, err := payment.NewRepository(db)
	if err != nil {
		t.Fatalf("payment repository: %v", err)
	}
	orderRepo, err := order.NewRepository(db)
	if err != nil {
		t.Fatalf("order repository: %v", err)
	}
	orderUsecase, err := apporder.NewCreateOrderUsecase(apporder.CreateOrderUsecaseDeps{
		DB: db, Orders: orderRepo, Address: addressService, Cart: cartRepo, Products: productRepo, Ledger: paymentRepo, Now: nowUTC,
	})
	if err != nil {
		t.Fatalf("order usecase: %v", err)
	}
	adjustPointsUsecase, err := apppoints.NewAdjustPointsUsecase(apppoints.AdjustPointsUsecaseDeps{
		DB: db, Ledger: paymentRepo, Roles: adminService, Logger: logger, Now: nowUTC,
	})
	if err != nil {
		t.Fatalf("adjust points usecase: %v", err)
	}
	refundUsecase, err := apprefund.NewRefundUsecase(apprefund.RefundUsecaseDeps{
		DB: db, Orders: orderRepo, Products: productRepo, Ledger: paymentRepo, Roles: adminService, Logger: logger, Now: nowUTC,
	})
	if err != nil {
		t.Fatalf("refund usecase: %v", err)
	}
	fulfillmentUsecase, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB: db, Orders: orderRepo, Roles: adminService, Logger: logger, Now: nowUTC,
	})
	if err != nil {
		t.Fatalf("fulfillment usecase: %v", err)
	}
	orderService, err := order.NewOrderService(order.OrderServiceDeps{
		Repo: orderRepo, Ledger: paymentRepo,
	})
	if err != nil {
		t.Fatalf("order service: %v", err)
	}
	orderHandler, err := order.NewOrderHandler(order.OrderHandlerDeps{
		Service: orderService, Create: orderUsecase, Refund: refundUsecase,
		Fulfillment: fulfillmentUsecase, Points: adjustPointsUsecase,
		PublicBaseURL: publicBaseURL, Logger: logger,
	})
	if err != nil {
		t.Fatalf("order handler: %v", err)
	}
	imageHandler, err := admin.NewImageHandler(admin.ImageHandlerDeps{
		Store: imageStorage, Capacity: imageStorage, CapacityLimit: 1 << 30,
		UploadLimit: defaultUploadLimit, MaxPixels: defaultImageMaxPixels,
		PublicBaseURL: publicBaseURL, DB: db, Service: adminService, Logger: logger, Now: nowUTC, Metrics: appMetrics,
	})
	if err != nil {
		t.Fatalf("image handler: %v", err)
	}

	// The buyer login and profile handlers are the SAME production handlers
	// cmd/server/main.go wires; only the WeChat adapter is swapped for a
	// deterministic code→openid map so tests can create several distinct buyers.
	wechatClient := &codeMappedClient{codes: map[string]string{
		"code-buyer-a": "buyer-a-openid",
		"code-buyer-b": "buyer-b-openid",
		"code-buyer-c": "buyer-c-openid",
	}}
	loginUsecase := applogin.NewLoginUsecase(applogin.LoginUsecaseDeps{
		DB:          db,
		WeChat:      wechatClient,
		Signer:      buyerSigner,
		Logger:      logger,
		Now:         nowUTC,
		SignupBonus: signupBonus,
	})
	// The production root wires a real per-IP budget; the acceptance suite uses
	// a very high budget so many test logins from one host cannot trip a 429.
	loginHandler, err := applogin.NewLoginHandler(applogin.LoginHandlerDeps{
		Usecase: loginUsecase,
		Logger:  logger,
		Limiter: applogin.NewLoginRateLimiter(100000, time.Minute),
	})
	if err != nil {
		t.Fatalf("login handler: %v", err)
	}
	userService, err := user.NewService(user.ServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("user service: %v", err)
	}
	profileHandler, err := user.NewProfileHandler(user.ProfileHandlerDeps{Service: userService, Logger: logger})
	if err != nil {
		t.Fatalf("profile handler: %v", err)
	}

	cfg := config.Config{
		PublicBaseURL:  publicBaseURL,
		AllowedOrigins: []string{adminOrigin},
		AdminCookie:    config.DefaultAdminCookieConfig(),
		UploadLimit:    defaultUploadLimit,
		ImageMaxPixels: defaultImageMaxPixels,
	}

	engine := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger: logger, Metrics: appMetrics,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			applogin.RegisterBuyerAuthRoutes(group, applogin.BuyerAuthRouteDeps{Handler: loginHandler})
			product.RegisterPublicRoutes(group, productHandler)
			user.RegisterProfileRoutes(group, user.ProfileRouteDeps{Handler: profileHandler, Signer: buyerSigner})
			user.RegisterAddressRoutes(group, user.AddressRouteDeps{Handler: addressHandler, Signer: buyerSigner})
			cart.RegisterRoutes(group, cart.RouteDeps{Handler: cartHandler, Signer: buyerSigner})
			order.RegisterBuyerRoutes(group, order.BuyerRouteDeps{Handler: orderHandler, Signer: buyerSigner})
		},
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			admin.RegisterRoutes(group, adminHandler)
			product.RegisterAdminRoutes(group, product.AdminRouteDeps{
				Handler: productHandler, Signer: adminSigner, Cookie: cfg.AdminCookie,
				Now: nowUTC, PermissionResolver: adminService, AdminStateResolver: adminService,
			})
			admin.RegisterImageRoutes(group, admin.ImageRouteDeps{
				Handler: imageHandler, Signer: adminSigner, Cookie: cfg.AdminCookie,
				Now: nowUTC, PermissionResolver: adminService, AdminStateResolver: adminService,
			})
			order.RegisterAdminRoutes(group, order.AdminRouteDeps{
				Handler: orderHandler, Signer: adminSigner, Cookie: cfg.AdminCookie,
				Now: nowUTC, PermissionResolver: adminService, AdminStateResolver: adminService,
			})
		},
	})

	srv := httptest.NewTLSServer(engine)
	t.Cleanup(srv.Close)

	// httptest.Server.Client() returns a SHARED client in this Go version;
	// mutating its Jar would corrupt every session. Capture the TLS transport
	// once and clone it per session/jar so each authenticated session owns an
	// independent cookie jar.
	sharedTransport, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected httptest transport type %T", srv.Client().Transport)
	}

	h := &Harness{
		t:               t,
		DB:              db,
		srv:             srv,
		baseURL:         srv.URL,
		transport:       sharedTransport,
		buyerHTTP:       &http.Client{Transport: sharedTransport.Clone()},
		adminCookieName: cfg.AdminCookie.AccessTokenName,
		csrfCookieName:  cfg.AdminCookie.CSRFName,
		adminOrigin:     adminOrigin,
		adminService:    adminService,
		wechatClient:    wechatClient,
	}
	h.SuperAdmin = h.bootstrapAdmin(superUsername, superPassword, "super_admin")
	h.Operator = h.bootstrapAdmin(operatorUsername, operatorPassword, "operator")
	return h
}

// RegisterBuyer adds a deterministic wx-login code → openid mapping for a
// test, so the test can log in additional buyers.
func (h *Harness) RegisterBuyer(code, openid string) {
	if h == nil || h.wechatClient == nil || h.wechatClient.codes == nil {
		h.t.Fatalf("register buyer: harness not ready")
	}
	h.wechatClient.codes[code] = openid
}

// BootstrapAdmin seeds an administrator through the real
// BootstrapAdministrator path (Argon2id hash + audit row) without logging in.
// It returns the wrapped error so a test can assert on bootstrap failures.
func (h *Harness) BootstrapAdmin(username, password, role string) error {
	h.t.Helper()
	_, err := h.adminService.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: username, Password: password, RoleName: role, ActorName: "e2e-harness",
	})
	return err
}

// CookieName returns the admin access-token cookie name.
func (h *Harness) CookieName() string {
	return h.adminCookieName
}

// bootstrapAdmin seeds an administrator through the real
// BootstrapAdministrator path (Argon2id hash + audit row) and logs in,
// returning a live session.
func (h *Harness) bootstrapAdmin(username, password, role string) *AdminSession {
	h.t.Helper()
	_, err := h.adminService.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: username, Password: password, RoleName: role, ActorName: "e2e-harness",
	})
	if err != nil && !errors.Is(err, admin.ErrAdministratorExists) {
		h.t.Fatalf("bootstrap %s: %v", role, err)
	}
	return h.AdminLogin(username, password)
}

// AdminLogin performs a real POST /api/admin/v1/auth/login and returns a
// session carrying the cookie jar and the CSRF token.
func (h *Harness) AdminLogin(username, password string) *AdminSession {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Transport: h.transport.Clone(), Jar: jar}

	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		h.t.Fatalf("marshal login: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/api/admin/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", adminOrigin)

	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("admin login %s: %v", username, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("admin login %s status=%d body=%s", username, resp.StatusCode, raw)
	}

	// The admin cookies are path-scoped to /api/admin/v1, so they must be
	// matched against a URL under that path, not the bare base URL.
	csrf := ""
	for _, cookie := range jar.Cookies(mustParseURL(h.baseURL + "/api/admin/v1/auth/me")) {
		if cookie.Name == h.csrfCookieName {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		h.t.Fatalf("admin login %s did not set csrf cookie", username)
	}
	return &AdminSession{Username: username, Password: password, Client: client, Origin: adminOrigin, CSRF: csrf}
}

// AdminLoginCheck performs a raw admin login and returns the status and body
// without failing the test, so negative login tests can assert on the result.
func (h *Harness) AdminLoginCheck(username, password string) (int, []byte) {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Transport: h.transport.Clone(), Jar: jar}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		h.t.Fatalf("marshal login: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/api/admin/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.adminOrigin)
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("admin login check %s: %v", username, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read login response: %v", err)
	}
	return resp.StatusCode, raw
}

// AccessTokenCookie returns the current Secure access-token cookie value from
// the session's jar, so a test can replay a stale token after a password
// change or logout.
func (h *Harness) AccessTokenCookie(session *AdminSession) string {
	h.t.Helper()
	if session == nil || session.Client == nil || session.Client.Jar == nil {
		return ""
	}
	for _, cookie := range session.Client.Jar.Cookies(mustParseURL(h.baseURL + "/api/admin/v1/auth/me")) {
		if cookie.Name == h.adminCookieName {
			return cookie.Value
		}
	}
	return ""
}

// AdminDo sends an admin-write request with the session's Origin and CSRF
// headers by default. headers can override or add headers; setting a header to
// "" removes it.
func (h *Harness) AdminDo(session *AdminSession, method, path string, body any, headers map[string]string) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Origin", h.adminOrigin)
	if session != nil && session.CSRF != "" {
		req.Header.Set("X-CSRF-Token", session.CSRF)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if value == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
	if session == nil || session.Client == nil {
		h.t.Fatalf("admin session client is nil for %s %s", method, path)
	}
	resp, err := session.Client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// AdminUpload sends a multipart image upload to /api/admin/v1/images using the
// supplied field filename and Content-Type.
func (h *Harness) AdminUpload(session *AdminSession, filename, contentType string, content []byte, headers map[string]string) *http.Response {
	h.t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		h.t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		h.t.Fatalf("write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		h.t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/api/admin/v1/images", &buf)
	if err != nil {
		h.t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", h.adminOrigin)
	if session != nil && session.CSRF != "" {
		req.Header.Set("X-CSRF-Token", session.CSRF)
	}
	// contentType is informational only: the image handler validates the
	// upload by magic bytes, never by a declared Content-Type.
	if contentType != "" {
		req.Header.Set("X-Declared-Content-Type", contentType)
	}
	for key, value := range headers {
		if value == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
	resp, err := session.Client.Do(req)
	if err != nil {
		h.t.Fatalf("upload: %v", err)
	}
	return resp
}

// BuyerLogin performs a real POST /api/v1/auth/wx-login and returns the JWT
// plus the decoded data payload.
func (h *Harness) BuyerLogin(code string) (token string, data map[string]any) {
	h.t.Helper()
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		h.t.Fatalf("marshal wx-login: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/api/v1/auth/wx-login", bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("wx-login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.buyerHTTP.Do(req)
	if err != nil {
		h.t.Fatalf("wx-login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("wx-login status=%d body=%s", resp.StatusCode, raw)
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		h.t.Fatalf("decode wx-login response: %v", err)
	}
	if env.TraceID == "" {
		h.t.Fatalf("wx-login response missing trace_id")
	}
	var payload struct {
		AccessToken string         `json:"access_token"`
		User        map[string]any `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		h.t.Fatalf("decode wx-login data: %v", err)
	}
	return payload.AccessToken, payload.User
}

// BuyerDo sends an authenticated buyer request using the given bearer token.
func (h *Harness) BuyerDo(token, method, path string, body any, headers map[string]string) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal buyer body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		h.t.Fatalf("build buyer %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if value == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
	resp, err := h.buyerHTTP.Do(req)
	if err != nil {
		h.t.Fatalf("buyer %s %s: %v", method, path, err)
	}
	return resp
}

// RawReq builds a request against the harness base URL with the given
// headers, so security tests can construct adversarial requests the typed
// helpers would not allow.
func (h *Harness) RawReq(method, path string, body []byte, headers map[string]string) *http.Request {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		h.t.Fatalf("build raw %s %s: %v", method, path, err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req
}

// Do executes a request with the given client.
func (h *Harness) Do(client *http.Client, req *http.Request) *http.Response {
	h.t.Helper()
	if client == nil {
		client = h.buyerHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	return resp
}

// Decode decodes a response body into the envelope.
func (h *Harness) Decode(resp *http.Response) (envelope, []byte) {
	h.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read response body: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		h.t.Fatalf("decode envelope from %s: %v", raw, err)
	}
	return env, raw
}

// SeedProduct inserts a product row directly and returns its id.
func (h *Harness) SeedProduct(name string, pricePoints int64, stock int32, status string) string {
	h.t.Helper()
	var id uid.ID
	var idText string
	if err := h.DB.WithContext(context.Background()).Raw(
		`INSERT INTO products (id, name, description, images, price_points, stock, status) VALUES (?, ?, ?, '[]'::jsonb, ?, ?, ?) RETURNING id::text`,
		uid.New(), name, "deterministic e2e product", pricePoints, stock, status,
	).Scan(&idText).Error; err != nil {
		h.t.Fatalf("seed product: %v", err)
	}
	id, err := uid.ParseCanonical(idText)
	if err != nil {
		h.t.Fatalf("seed product: %v", err)
	}
	return id.String()
}

// SeedUser inserts a user row directly and returns its id.
func (h *Harness) SeedUser(openid string, points int64) string {
	h.t.Helper()
	var id uid.ID
	var idText string
	if err := h.DB.WithContext(context.Background()).Raw(
		`INSERT INTO users (id, openid, nickname, points_balance) VALUES (?, ?, ?, ?) RETURNING id::text`,
		uid.New(), openid, "seeded-buyer", points,
	).Scan(&idText).Error; err != nil {
		h.t.Fatalf("seed user: %v", err)
	}
	id, err := uid.ParseCanonical(idText)
	if err != nil {
		h.t.Fatalf("seed user: %v", err)
	}
	return id.String()
}

// PointsBalance returns the current points balance of the buyer with the
// given numeric id.
func (h *Harness) PointsBalance(userID string) int64 {
	h.t.Helper()
	var balance int64
	id, err := uid.ParseCanonical(userID)
	if err != nil {
		h.t.Fatalf("parse user id: %v", err)
	}
	if err := h.DB.WithContext(context.Background()).Raw(
		`SELECT points_balance FROM users WHERE id = ?`, id,
	).Scan(&balance).Error; err != nil {
		h.t.Fatalf("read points balance: %v", err)
	}
	return balance
}

// ProductStock returns the current stock of the product with the given
// numeric id.
func (h *Harness) ProductStock(productID string) int32 {
	h.t.Helper()
	var stock int32
	id, err := uid.ParseCanonical(productID)
	if err != nil {
		h.t.Fatalf("parse product id: %v", err)
	}
	if err := h.DB.WithContext(context.Background()).Raw(
		`SELECT stock FROM products WHERE id = ?`, id,
	).Scan(&stock).Error; err != nil {
		h.t.Fatalf("read product stock: %v", err)
	}
	return stock
}

// AssertTraceID asserts the envelope carries a trace_id and the response
// header X-Trace-Id matches it.
func (h *Harness) AssertTraceID(resp *http.Response, env envelope) {
	h.t.Helper()
	if env.TraceID == "" {
		h.t.Errorf("%s: envelope trace_id is empty", resp.Request.URL.Path)
	}
	if got := resp.Header.Get(middlewareTraceHeader); got == "" {
		h.t.Errorf("%s: X-Trace-Id header missing", resp.Request.URL.Path)
	} else if env.TraceID != "" && got != env.TraceID {
		h.t.Errorf("%s: X-Trace-Id header %q != envelope trace_id %q", resp.Request.URL.Path, got, env.TraceID)
	}
}

// middlewareTraceHeader mirrors middleware.TraceHeader.
const middlewareTraceHeader = "X-Trace-Id"

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// passwordChangerAdapter mirrors the accessPasswordChanger adapter in
// cmd/server/main.go: the access usecase owns the password-change transaction.
type passwordChangerAdapter struct {
	usecase *access.AccessUsecase
}

func (a passwordChangerAdapter) ChangePassword(ctx context.Context, input admin.ChangePasswordInput) (admin.ChangePasswordResult, error) {
	out, err := a.usecase.ChangePassword(ctx, access.ChangePasswordInput{
		ActorAdminID: input.ActorAdminID,
		ActorRole:    input.ActorRole,
		TargetID:     input.ActorAdminID,
		OldPassword:  input.OldPassword,
		NewPassword:  input.NewPassword,
	})
	if err != nil {
		return admin.ChangePasswordResult{}, err
	}
	return admin.ChangePasswordResult{
		AdminID: out.AdminID, TokenVersion: out.TokenVersion,
		Username: out.Username, RoleName: out.RoleName,
	}, nil
}

var _ admin.PasswordChanger = passwordChangerAdapter{}

// codeMappedClient is the test-only WeChat adapter: it maps a deterministic
// login code to a deterministic openid so each test can create several distinct
// buyers through the REAL production LoginHandler. It is the only buyer-side
// deviation from the production composition root, which uses the fixed-openid
// FakeClient locally and the real WeChat API when configured.
type codeMappedClient struct {
	codes map[string]string
}

func (c *codeMappedClient) Code2Session(ctx context.Context, code string) (wechat.Session, error) {
	if c == nil || c.codes == nil {
		return wechat.Session{}, errors.New("wechat code map is not configured")
	}
	openid := c.codes[strings.TrimSpace(code)]
	if strings.TrimSpace(openid) == "" {
		return wechat.Session{}, errors.New("unknown WeChat login code")
	}
	return wechat.Session{OpenID: openid, UnionID: "union-" + openid, SessionKey: "e2e-session-key"}, nil
}
