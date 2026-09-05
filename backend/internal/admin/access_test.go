package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop-mall/backend/internal/admin"
)

// roleData is the contract Role projection carried inside an envelope data
// object: id (int64 as string), name and the permission codes.
type roleData struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type loginData struct {
	AdminID  string   `json:"admin_id"`
	Username string   `json:"username"`
	Role     roleData `json:"role"`
}

type meData struct {
	AdminID      string   `json:"admin_id"`
	Username     string   `json:"username"`
	Role         roleData `json:"role"`
	TokenVersion int64    `json:"token_version"`
	Enabled      bool     `json:"enabled"`
}

type roleListData struct {
	List []roleData `json:"list"`
}

type adminUserData struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Enabled  bool     `json:"enabled"`
	Role     roleData `json:"role"`
}

type adminUserListData struct {
	List     []adminUserData `json:"list"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

func TestAdminLoginReturnsRoleObjectWithPermissions(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "role-object-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	loginRes := loginAdmin(t, router, "role-object-admin", "Sup3rSecret!Pass")
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", loginRes.Code, loginRes.Body.String())
	}
	var env struct {
		Data loginData `json:"data"`
	}
	if err := json.Unmarshal(loginRes.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode login body: %v", err)
	}
	if env.Data.AdminID == "" {
		t.Fatal("login data missing admin_id")
	}
	if env.Data.Username != "role-object-admin" {
		t.Fatalf("login username = %q", env.Data.Username)
	}
	if env.Data.Role.Name != "super_admin" {
		t.Fatalf("login role name = %q, want super_admin", env.Data.Role.Name)
	}
	if env.Data.Role.ID == "" {
		t.Fatal("login role missing id")
	}
	if env.Data.Role.Permissions == nil {
		t.Fatal("login role missing permissions array")
	}
	if !containsPermission(env.Data.Role.Permissions, "role:manage") {
		t.Fatalf("login super_admin permissions missing role:manage: %v", env.Data.Role.Permissions)
	}
	if !containsPermission(env.Data.Role.Permissions, "product:read") {
		t.Fatalf("login super_admin permissions missing product:read: %v", env.Data.Role.Permissions)
	}
}

func TestAdminMeReturnsRoleObjectWithPermissions(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "me-role-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "operator",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	loginRes := loginAdmin(t, router, "me-role-admin", "Sup3rSecret!Pass")
	accessToken := cookieValue(loginRes, fix.cookieCfg.AccessTokenName)
	if accessToken == "" {
		t.Fatal("login did not set access token cookie")
	}
	probe := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/me", nil)
	probe.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	meRes := httptest.NewRecorder()
	router.ServeHTTP(meRes, probe)
	if meRes.Code != http.StatusOK {
		t.Fatalf("me status = %d; body=%s", meRes.Code, meRes.Body.String())
	}
	var env struct {
		Data meData `json:"data"`
	}
	if err := json.Unmarshal(meRes.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode me body: %v", err)
	}
	if env.Data.Username != "me-role-admin" {
		t.Fatalf("me username = %q", env.Data.Username)
	}
	if env.Data.Role.Name != "operator" {
		t.Fatalf("me role name = %q, want operator", env.Data.Role.Name)
	}
	if env.Data.Role.ID == "" {
		t.Fatal("me role missing id")
	}
	if containsPermission(env.Data.Role.Permissions, "role:manage") {
		t.Fatalf("operator must not have role:manage: %v", env.Data.Role.Permissions)
	}
	if !containsPermission(env.Data.Role.Permissions, "product:read") {
		t.Fatalf("operator me permissions missing product:read: %v", env.Data.Role.Permissions)
	}
	if !env.Data.Enabled {
		t.Fatal("me enabled must be true")
	}
}

func TestAdminRolesReturnsSeededRolesWithPermissions(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "roles-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	accessToken := cookieValue(loginAdmin(t, router, "roles-admin", "Sup3rSecret!Pass"), fix.cookieCfg.AccessTokenName)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/roles", nil)
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("roles status = %d; body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data roleListData `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode roles body: %v", err)
	}
	if len(env.Data.List) < 2 {
		t.Fatalf("expected at least super_admin and operator, got %d roles", len(env.Data.List))
	}
	byName := map[string]roleData{}
	for _, role := range env.Data.List {
		byName[role.Name] = role
	}
	super, ok := byName["super_admin"]
	if !ok {
		t.Fatal("seeded super_admin role missing from /roles")
	}
	if super.ID == "" {
		t.Fatal("super_admin role missing id")
	}
	if !containsPermission(super.Permissions, "role:manage") {
		t.Fatalf("super_admin missing role:manage: %v", super.Permissions)
	}
	operator, ok := byName["operator"]
	if !ok {
		t.Fatal("seeded operator role missing from /roles")
	}
	if containsPermission(operator.Permissions, "role:manage") {
		t.Fatalf("operator must not have role:manage: %v", operator.Permissions)
	}
	if !containsPermission(operator.Permissions, "order:ship") {
		t.Fatalf("operator missing order:ship: %v", operator.Permissions)
	}
}

func TestAdminRolesRejectsOperatorWithoutRoleManage(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "no-role-manage",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "operator",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	accessToken := cookieValue(loginAdmin(t, router, "no-role-manage", "Sup3rSecret!Pass"), fix.cookieCfg.AccessTokenName)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/roles", nil)
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("operator /roles status = %d, want 403", res.Code)
	}
}

func TestAdminUsersReturnsPagedAccountsWithRole(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "users-super",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap super: %v", err)
	}
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "users-operator",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "operator",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	accessToken := cookieValue(loginAdmin(t, router, "users-super", "Sup3rSecret!Pass"), fix.cookieCfg.AccessTokenName)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/admin-users?page=1&page_size=10", nil)
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin-users status = %d; body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data adminUserListData `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode admin-users body: %v", err)
	}
	if env.Data.Total < 2 {
		t.Fatalf("admin-users total = %d, want at least 2", env.Data.Total)
	}
	if env.Data.Page != 1 || env.Data.PageSize != 10 {
		t.Fatalf("admin-users page=%d page_size=%d", env.Data.Page, env.Data.PageSize)
	}
	byName := map[string]adminUserData{}
	for _, u := range env.Data.List {
		byName[u.Username] = u
	}
	super, ok := byName["users-super"]
	if !ok {
		t.Fatal("users-super missing from admin-users list")
	}
	if !super.Enabled {
		t.Fatal("users-super must be enabled")
	}
	if super.Role.Name != "super_admin" {
		t.Fatalf("users-super role name = %q, want super_admin", super.Role.Name)
	}
	if super.Role.ID == "" || !containsPermission(super.Role.Permissions, "role:manage") {
		t.Fatalf("users-super role malformed: %+v", super.Role)
	}
	op, ok := byName["users-operator"]
	if !ok {
		t.Fatal("users-operator missing from admin-users list")
	}
	if op.Role.Name != "operator" {
		t.Fatalf("users-operator role name = %q, want operator", op.Role.Name)
	}
	if containsPermission(op.Role.Permissions, "role:manage") {
		t.Fatalf("operator must not have role:manage: %v", op.Role.Permissions)
	}
}

func TestAdminPasswordChangeAcceptsCurrentPasswordAndRejectsWrong(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "current-pw-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	loginRes := loginAdmin(t, router, "current-pw-admin", "Sup3rSecret!Pass")
	accessToken := cookieValue(loginRes, fix.cookieCfg.AccessTokenName)
	csrfToken := cookieValue(loginRes, fix.cookieCfg.CSRFName)
	if accessToken == "" || csrfToken == "" {
		t.Fatal("login did not set both cookies")
	}
	// A wrong current_password is rejected with 401 and bumps nothing. The
	// CSRF middleware validates the csrf_token cookie against the X-CSRF-Token
	// header, so the request must carry both cookies from login.
	wrongReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/password",
			strings.NewReader(`{"current_password":"WrongPass!1","new_password":"Sup3rSecret!NewPass"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-CSRF-Token", csrfToken)
		req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
		req.AddCookie(&http.Cookie{Name: fix.cookieCfg.CSRFName, Value: csrfToken})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	res := wrongReq()
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current_password status = %d, want 401; body=%s", res.Code, res.Body.String())
	}
	// The correct current_password succeeds.
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/password",
		strings.NewReader(`{"current_password":"Sup3rSecret!Pass","new_password":"Sup3rSecret!NewPass"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	req.AddCookie(&http.Cookie{Name: fix.cookieCfg.CSRFName, Value: csrfToken})
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("correct current_password status = %d, want 200; body=%s", res.Code, res.Body.String())
	}
	// The old JWT is invalid after the change.
	probe := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/me", nil)
	probe.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: accessToken})
	meRes := httptest.NewRecorder()
	router.ServeHTTP(meRes, probe)
	if meRes.Code != http.StatusUnauthorized {
		t.Fatalf("old token after password change = %d, want 401", meRes.Code)
	}
}

// loginAdmin performs a valid admin login against the fixture router and
// returns the recorder so the caller can read the Set-Cookie headers.
func loginAdmin(t *testing.T, router http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// cookieValue extracts the value of the named Set-Cookie header from a login
// response recorder.
func cookieValue(res *httptest.ResponseRecorder, name string) string {
	for _, raw := range res.Header().Values("Set-Cookie") {
		parsed, err := http.ParseSetCookie(raw)
		if err != nil {
			continue
		}
		if parsed.Name == name {
			return parsed.Value
		}
	}
	return ""
}
