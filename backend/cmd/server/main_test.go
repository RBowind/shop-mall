package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/logging"
	"shop-mall/backend/internal/platform/metrics"
	"shop-mall/backend/tests/integration"
)

// envelope is the business envelope every /api/v1 response uses. Data is kept
// raw so each test decodes what it needs.
type envelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	TraceID string          `json:"trace_id"`
}

// TestComposedServerServesBuyerLoginAndProfile proves that the production
// composition root (buildRouter, the same code path main() runs) serves the
// buyer routes the OpenAPI contract and the miniapp require: wx-login for the
// first JWT, then the authenticated GET/PATCH /me profile endpoints. The test
// uses a real PostgreSQL schema and the deterministic fake WeChat client, so a
// release binary built from this composition no longer 404s buyer login.
func TestComposedServerServesBuyerLoginAndProfile(t *testing.T) {
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := logging.New(io.Discard, slog.LevelError)
	appMetrics := metrics.New()

	cfg := config.Config{
		Environment:    "test",
		PublicBaseURL:  "https://api.example.test",
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    config.DefaultAdminCookieConfig(),
		BuyerJWT: config.JWTConfig{
			Issuer: "buyer-compose-test-issuer", Audience: "buyer-compose-test-audience",
			TTL: 24 * time.Hour, ActiveKID: "buyer-compose-test-kid",
			Keys: map[string][]byte{"buyer-compose-test-kid": []byte("buyer-compose-test-key-material-32b")},
		},
		AdminJWT: config.JWTConfig{
			Issuer: "admin-compose-test-issuer", Audience: "admin-compose-test-audience",
			TTL: 30 * time.Minute, ActiveKID: "admin-compose-test-kid",
			Keys: map[string][]byte{"admin-compose-test-kid": []byte("admin-compose-test-key-material-32b")},
		},
		UploadLimit:          2 * 1024 * 1024,
		SignupBonus:          100,
		ImageMaxPixels:       25_000_000,
		ImageStorageCapacity: 1 << 30,
		ImageGracePeriod:     24 * time.Hour,
		ImageCleanupInterval: time.Hour,
		ImageVolumeDir:       t.TempDir(),
	}

	engine, _, err := buildRouter(cfg, db, logger, appMetrics)
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	srv := httptest.NewServer(engine)
	defer srv.Close()
	client := srv.Client()

	// 1. wx-login: the fake WeChat client accepts any non-empty code and maps
	// it to a deterministic openid, awarding the exactly-once signup bonus.
	body, err := json.Marshal(map[string]string{"code": "compose-login-code"})
	if err != nil {
		t.Fatalf("marshal wx-login: %v", err)
	}
	resp, err := client.Post(srv.URL+"/api/v1/auth/wx-login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("wx-login: %v", err)
	}
	env := decodeEnvelope(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wx-login status = %d, want 200: message=%s", resp.StatusCode, env.Message)
	}
	var loginData struct {
		AccessToken string         `json:"access_token"`
		User        map[string]any `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &loginData); err != nil {
		t.Fatalf("decode wx-login data: %v", err)
	}
	if loginData.AccessToken == "" {
		t.Fatal("wx-login access_token is empty")
	}
	if readString(t, loginData.User, "id") == "" {
		t.Fatal("wx-login user.id is empty")
	}
	if got := readString(t, loginData.User, "points_balance"); got != "100" {
		t.Fatalf("wx-login points_balance = %s, want signup bonus 100", got)
	}
	// The contract's User schema does not carry openid; the server-only WeChat
	// identifier must not leak into the response.
	if _, ok := loginData.User["openid"]; ok {
		t.Fatalf("wx-login response leaked openid: %v", loginData.User)
	}

	// 2. GET /me with the fresh bearer token returns the profile.
	meResp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/me", loginData.AccessToken, nil)
	meEnv := decodeEnvelope(t, meResp)
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d, want 200: message=%s", meResp.StatusCode, meEnv.Message)
	}
	var meUser map[string]any
	if err := json.Unmarshal(meEnv.Data, &meUser); err != nil {
		t.Fatalf("decode /me data: %v", err)
	}
	if got := readString(t, meUser, "nickname"); got != "" {
		t.Fatalf("fresh profile nickname = %q, want empty", got)
	}
	if got := readString(t, meUser, "points_balance"); got != "100" {
		t.Fatalf("GET /me points_balance = %s, want 100", got)
	}
	if _, ok := meUser["openid"]; ok {
		t.Fatalf("GET /me leaked openid: %v", meUser)
	}

	// 2b. A partial PATCH must not wipe the absent field: set avatar_url, then
	// PATCH only the nickname and confirm avatar_url survives.
	avatarBody, err := json.Marshal(map[string]string{"avatar_url": "https://cdn.example.test/a.png"})
	if err != nil {
		t.Fatalf("marshal avatar patch: %v", err)
	}
	avatarResp := doRequest(t, client, http.MethodPatch, srv.URL+"/api/v1/me", loginData.AccessToken, avatarBody)
	if avatarResp.StatusCode != http.StatusOK {
		t.Fatalf("avatar-only PATCH status = %d, want 200", avatarResp.StatusCode)
	}
	_ = decodeEnvelope(t, avatarResp)

	// 3. PATCH /me updates the nickname, and GET /me reflects it.
	patchBody, err := json.Marshal(map[string]string{"nickname": "刀客甲"})
	if err != nil {
		t.Fatalf("marshal patch /me: %v", err)
	}
	patchResp := doRequest(t, client, http.MethodPatch, srv.URL+"/api/v1/me", loginData.AccessToken, patchBody)
	patchEnv := decodeEnvelope(t, patchResp)
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /me status = %d, want 200: message=%s", patchResp.StatusCode, patchEnv.Message)
	}
	var patched map[string]any
	if err := json.Unmarshal(patchEnv.Data, &patched); err != nil {
		t.Fatalf("decode patch /me data: %v", err)
	}
	if got := readString(t, patched, "nickname"); got != "刀客甲" {
		t.Fatalf("patched nickname = %q, want 刀客甲", got)
	}
	if got := readString(t, patched, "avatar_url"); got != "https://cdn.example.test/a.png" {
		t.Fatalf("nickname-only PATCH wiped avatar_url: %q", got)
	}

	// 4. The buyer surface is protected: an unauthenticated /me is a 401, a
	// blank login code is a 400 and an over-long code is a 400, matching the
	// contract's wx-login responses.
	unauthResp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/me", "", nil)
	unauthEnv := decodeEnvelope(t, unauthResp)
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /me status = %d, want 401", unauthResp.StatusCode)
	}
	if unauthEnv.Code != 1002 {
		t.Fatalf("unauthenticated GET /me code = %d, want 1002", unauthEnv.Code)
	}
	blankBody, err := json.Marshal(map[string]string{"code": "  "})
	if err != nil {
		t.Fatalf("marshal blank wx-login: %v", err)
	}
	blankResp, err := client.Post(srv.URL+"/api/v1/auth/wx-login", "application/json", bytes.NewReader(blankBody))
	if err != nil {
		t.Fatalf("blank wx-login: %v", err)
	}
	_ = decodeEnvelope(t, blankResp)
	if blankResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("blank code status = %d, want 400", blankResp.StatusCode)
	}
	longBody, err := json.Marshal(map[string]string{"code": strings.Repeat("c", 513)})
	if err != nil {
		t.Fatalf("marshal long wx-login: %v", err)
	}
	longResp, err := client.Post(srv.URL+"/api/v1/auth/wx-login", "application/json", bytes.NewReader(longBody))
	if err != nil {
		t.Fatalf("long wx-login: %v", err)
	}
	_ = decodeEnvelope(t, longResp)
	if longResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("513-char code status = %d, want 400", longResp.StatusCode)
	}
}

func decodeEnvelope(t *testing.T, resp *http.Response) envelope {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope from %s: %v", raw, err)
	}
	return env
}

func doRequest(t *testing.T, client *http.Client, method, url, token string, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func readString(t *testing.T, data map[string]any, key string) string {
	t.Helper()
	value, ok := data[key]
	if !ok {
		t.Fatalf("response data missing %q: %v", key, data)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("response data %q is %T, want string", key, value)
	}
	return text
}
