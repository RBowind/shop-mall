package e2e

// Acceptance tests for specs/buyer-auth/spec.md (sprint contract
// specs/buyer-auth/contract.md). Each test name carries the contract B*/Q* id
// it anchors. Scenarios already verified elsewhere are mapped in
// B* ↔ test coverage below and NOT duplicated here:
//
//   B1  partial: TestBuyerLoginSignupBonusIsExactlyOnce (token+user shape) +
//      TestSpecBuyerAuthB1_SessionKeyNeverLeaksToResponse (this file)
//   B2  TestSpecBuyerAuthB2_WeChatFailureIs2xxxBusinessCodeAndCreatesNoUser
//   B3  TestSpecBuyerAuthB3_ExpiredAndForgedAndAdminTokensRejectedByBuyerAPI
//   B4  端内行为，归小程序 E2E（TS 套件），不在 Go 层
//   B5  TestBuyerLoginSignupBonusIsExactlyOnce + TestLoginUsecaseExecuteOnFirstLoginCreatesUserAndSignupBonus
//   B6  TestBuyerLoginSignupBonusIsExactlyOnce + TestLoginUsecaseReturnsExistingUserWithoutSecondSignupBonus
//   B7  TestSpecBuyerAuthB7_ZeroSignupBonusGrantsNothing
//   B8  需数据库故障注入，HTTP 层不可达；单元层由
//      TestLoginUsecasePropagatesWeChatError / DoesNotInsertUserWhenSessionLookupFails 覆盖前置失败
//   B9  TestSpecBuyerAuthB9_LoginRateLimitedAfterFailedAttempts
//   B10 TestBuyerProfileGetAndUpdate
//   B11 TestSpecBuyerAuthB11_AvatarUploadReturnsPublicURL
//   B12 TestBuyerProfileGetAndUpdate (GET /me assertions)
//   B13 TestBuyerProfileGetAndUpdate (oversized nickname 422)
//   Q4  TestLoginUsecaseConcurrentFirstLoginCreatesExactlyOneUser
//      + TestSpecBuyerAuthQ4_ConcurrentSameOpenidLoginsGrantBonusOnce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	applogin "shop-mall/backend/internal/application/auth"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/internal/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// countingWeChat wraps the deterministic code map and counts upstream calls,
// so rate-limit tests can assert the rejected attempts never reach WeChat.
type countingWeChat struct {
	inner *codeMappedClient
	calls atomic.Int64
}

func (c *countingWeChat) Code2Session(ctx context.Context, code string) (wechat.Session, error) {
	c.calls.Add(1)
	return c.inner.Code2Session(ctx, code)
}

// newBuyerAuthServer starts an isolated /api/v1 auth router with a tunable
// signup bonus and login rate budget against the shared test database.
func newBuyerAuthServer(t *testing.T, db *gorm.DB, signupBonus int64, loginLimit int) (*httptest.Server, *countingWeChat) {
	t.Helper()
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte(buyerKey)},
	})
	if err != nil {
		t.Fatalf("buyer signer: %v", err)
	}
	codes := map[string]string{"code-spec-new": "spec-new-openid"}
	counted := &countingWeChat{inner: &codeMappedClient{codes: codes}}
	var limiter *applogin.LoginRateLimiter
	if loginLimit > 0 {
		limiter = applogin.NewLoginRateLimiter(loginLimit, time.Minute)
	}
	usecase := applogin.NewLoginUsecase(applogin.LoginUsecaseDeps{
		DB: db, WeChat: counted, Signer: signer,
		Logger: discardLogger(), Now: func() time.Time { return time.Now().UTC() },
		SignupBonus: signupBonus,
	})
	handler, err := applogin.NewLoginHandler(applogin.LoginHandlerDeps{
		Usecase: usecase, Logger: discardLogger(), Limiter: limiter,
	})
	if err != nil {
		t.Fatalf("login handler: %v", err)
	}
	engine := gin.New()
	group := engine.Group("/api/v1")
	applogin.RegisterBuyerAuthRoutes(group, applogin.BuyerAuthRouteDeps{Handler: handler})
	srv := httptest.NewTLSServer(engine)
	t.Cleanup(srv.Close)
	return srv, counted
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func postJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

// B1: 登录成功后，响应任何位置都不出现微信会话密钥（session_key 三禁之"不下发"）。
func TestSpecBuyerAuthB1_SessionKeyNeverLeaksToResponse(t *testing.T) {
	h := NewHarness(t, testDB)

	req := h.RawReq(http.MethodPost, "/api/v1/auth/wx-login", []byte(`{"code":"code-buyer-b"}`), map[string]string{"Content-Type": "application/json"})
	resp := h.Do(nil, req)
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wx-login status = %d body=%s", resp.StatusCode, raw)
	}
	if bytes.Contains(raw, []byte("e2e-session-key")) || bytes.Contains(raw, []byte("session_key")) {
		t.Fatalf("login response leaked session key: %s", raw)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if parts := bytes.Count([]byte(data.AccessToken), []byte(".")); parts != 2 {
		t.Fatalf("access_token is not a JWT-shaped token: %q", data.AccessToken)
	}
}

// B2: code2Session 失败 → HTTP 422、业务码落 2xxx 认证段，且用户计数不变。
func TestSpecBuyerAuthB2_WeChatFailureIs2xxxBusinessCodeAndCreatesNoUser(t *testing.T) {
	h := NewHarness(t, testDB)

	before := countRows(t, h.DB, `SELECT count(*) FROM users`)
	resp := h.BuyerDo("", http.MethodPost, "/api/v1/auth/wx-login", map[string]string{"code": "unknown-code"}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if env.Code < 2000 || env.Code > 2999 {
		t.Fatalf("business code = %d, want 2xxx auth segment", env.Code)
	}
	if after := countRows(t, h.DB, `SELECT count(*) FROM users`); after != before {
		t.Fatalf("failed login created users: %d rows before, %d after", before, after)
	}
}

func countRows(t *testing.T, db *gorm.DB, query string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query).Scan(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// B3: 过期、伪造签名、管理域签发的三种买家侧请求全部 401。
func TestSpecBuyerAuthB3_ExpiredAndForgedAndAdminTokensRejectedByBuyerAPI(t *testing.T) {
	h := NewHarness(t, testDB)

	valid, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte(buyerKey)},
	})
	if err != nil {
		t.Fatalf("valid signer: %v", err)
	}
	forged, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte("forged-key-material-32bytes-xxxxxxx")},
	})
	if err != nil {
		t.Fatalf("forged signer: %v", err)
	}
	crossDomain, err := tokens.NewSigner(config.JWTConfig{
		Issuer: adminIssuer, Audience: adminAudience, TTL: 30 * time.Minute,
		ActiveKID: adminKID, Keys: map[string][]byte{adminKID: []byte(adminKey)},
	})
	if err != nil {
		t.Fatalf("cross-domain signer: %v", err)
	}

	now := time.Now().UTC()
	expiredToken, err := valid.IssueAt("1", 1, now.Add(-2*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	forgedToken, err := forged.Issue("1", 1, now)
	if err != nil {
		t.Fatalf("issue forged token: %v", err)
	}
	adminToken, err := crossDomain.Issue("1", 1, now)
	if err != nil {
		t.Fatalf("issue admin-signed token: %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"过期令牌", expiredToken},
		{"伪造签名令牌", forgedToken},
		{"管理域令牌打买家端点", adminToken},
	} {
		resp := h.BuyerDo(tc.token, http.MethodGet, "/api/v1/me", nil, nil)
		if got := resp.StatusCode; got != http.StatusUnauthorized {
			_ = resp.Body.Close()
			t.Fatalf("%s: status = %d, want 401", tc.name, got)
		}
		_ = resp.Body.Close()
	}
}

// B7: 赠送配置为 0 时创建成功，但余额为 0 且不留 signup_bonus 流水。
func TestSpecBuyerAuthB7_ZeroSignupBonusGrantsNothing(t *testing.T) {
	h := NewHarness(t, testDB)
	srv, _ := newBuyerAuthServer(t, h.DB, 0, 0)

	resp := postJSON(t, srv.Client(), srv.URL+"/api/v1/auth/wx-login", map[string]string{"code": "code-spec-new"})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d body=%s", resp.StatusCode, body)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var data struct {
		User struct {
			ID            string `json:"id"`
			PointsBalance string `json:"points_balance"`
		} `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.User.PointsBalance != "0" {
		t.Fatalf("points_balance = %s, want 0 when bonus configured as 0", data.User.PointsBalance)
	}
	var ledger int64
	if err := h.DB.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, data.User.ID).Scan(&ledger).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledger != 0 {
		t.Fatalf("signup_bonus rows = %d, want 0 when bonus configured as 0", ledger)
	}
}

// B9: 连续失败登录后超出预算的请求被 429 拒绝，且不再触达微信侧。
// 注意：实现里"登录成功会重置该 IP 的计数"，因此本例断言的是失败连击路径；
// 成功是否也应计入每分钟预算，见 spec 与实现分歧上报（PRD F-102 字面是请求数）。
func TestSpecBuyerAuthB9_LoginRateLimitedAfterFailedAttempts(t *testing.T) {
	h := NewHarness(t, testDB)
	srv, counted := newBuyerAuthServer(t, h.DB, signupBonus, 3)

	for i := range 3 {
		resp := postJSON(t, srv.Client(), srv.URL+"/api/v1/auth/wx-login", map[string]string{"code": "unknown-code-" + strconv.Itoa(i)})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want 422", i+1, resp.StatusCode)
		}
	}
	resp := postJSON(t, srv.Client(), srv.URL+"/api/v1/auth/wx-login", map[string]string{"code": "unknown-code-4"})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th attempt status = %d body=%s, want 429", resp.StatusCode, body)
	}
	if got := counted.calls.Load(); got != 3 {
		t.Fatalf("wechat calls = %d, want 3 (rejected attempt must not reach WeChat)", got)
	}
}

// B11: 头像上传返回服务端 object key 与可访问完整地址，且全局生效（GET /me 可见）。
func TestSpecBuyerAuthB11_AvatarUploadReturnsPublicURL(t *testing.T) {
	h := NewHarness(t, testDB)
	token, _ := h.BuyerLogin("code-buyer-a")

	store, err := storage.NewLocalVolume(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatalf("avatar storage: %v", err)
	}
	userService, err := user.NewService(user.ServiceDeps{DB: h.DB})
	if err != nil {
		t.Fatalf("user service: %v", err)
	}
	profileHandler, err := user.NewProfileHandler(user.ProfileHandlerDeps{
		Service: userService, Logger: discardLogger(),
		Store: store, Capacity: store, CapacityLimit: 1 << 30,
		UploadLimit: defaultUploadLimit, MaxPixels: defaultImageMaxPixels,
		PublicBaseURL: publicBaseURL,
	})
	if err != nil {
		t.Fatalf("profile handler: %v", err)
	}
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte(buyerKey)},
	})
	if err != nil {
		t.Fatalf("buyer signer: %v", err)
	}
	engine := gin.New()
	user.RegisterProfileRoutes(engine.Group("/api/v1"), user.ProfileRouteDeps{Handler: profileHandler, Signer: signer})
	srv := httptest.NewTLSServer(engine)
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "chosen-avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(realPNG(8, 8)); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/me/avatar", &buf)
	if err != nil {
		t.Fatalf("avatar request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("avatar upload: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("avatar upload status = %d body=%s, want 201", resp.StatusCode, body)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var upload struct {
		Key string `json:"key"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(env.Data, &upload); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if upload.Key == "" {
		t.Fatalf("avatar response missing object key: %s", body)
	}
	if want := publicBaseURL + "/static/images/" + upload.Key; upload.URL != want {
		t.Fatalf("avatar url = %s, want server-composed %s", upload.URL, want)
	}

	meReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("me request: %v", err)
	}
	meReq.Header.Set("Authorization", "Bearer "+token)
	meResp, err := srv.Client().Do(meReq)
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	meBody, _ := io.ReadAll(meResp.Body)
	_ = meResp.Body.Close()
	if !bytes.Contains(meBody, []byte(upload.URL)) {
		t.Fatalf("avatar URL not visible on GET /me: %s", meBody)
	}
}

// loginOnce is BuyerLogin without the t.Fatalf shortcut, so goroutines can
// report failures through the result channel instead of killing the runner.
func (h *Harness) loginOnce(code string) (userID, balance string, err error) {
	raw, _ := json.Marshal(map[string]string{"code": code})
	req, reqErr := http.NewRequest(http.MethodPost, h.baseURL+"/api/v1/auth/wx-login", bytes.NewReader(raw))
	if reqErr != nil {
		return "", "", reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	resp, doErr := h.buyerHTTP.Do(req)
	if doErr != nil {
		return "", "", doErr
	}
	defer func() { _ = resp.Body.Close() }()
	var env envelope
	if decErr := json.NewDecoder(resp.Body).Decode(&env); decErr != nil {
		return "", "", fmt.Errorf("status=%d: %w", resp.StatusCode, decErr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status=%d code=%d message=%s", resp.StatusCode, env.Code, env.Message)
	}
	var data struct {
		User struct {
			ID            string `json:"id"`
			PointsBalance string `json:"points_balance"`
		} `json:"user"`
	}
	if unmarshalErr := json.Unmarshal(env.Data, &data); unmarshalErr != nil {
		return "", "", unmarshalErr
	}
	return data.User.ID, data.User.PointsBalance, nil
}

// Q4/F-103 数据库唯一索引兜底：同一 openid 两个不同 code 并发首登，
// 只建一个用户、只赠一次分。
func TestSpecBuyerAuthQ4_ConcurrentSameOpenidLoginsGrantBonusOnce(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-dup-1", "buyer-dup-openid")
	h.RegisterBuyer("code-dup-2", "buyer-dup-openid")

	type loginResult struct {
		userID  string
		balance string
		err     error
	}
	results := make(chan loginResult, 2)
	start := make(chan struct{})
	for _, code := range []string{"code-dup-1", "code-dup-2"} {
		go func() {
			<-start
			id, balance, err := h.loginOnce(code)
			results <- loginResult{userID: id, balance: balance, err: err}
		}()
	}
	close(start)

	seen := map[string]bool{}
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent login failed: %v", got.err)
		}
		if got.balance != "100" {
			t.Fatalf("login points_balance = %s, want 100 (exactly one bonus)", got.balance)
		}
		seen[got.userID] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent same-openid logins created different users: %v", seen)
	}
}
