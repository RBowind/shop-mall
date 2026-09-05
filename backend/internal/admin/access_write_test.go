package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
)

// The fixture router uses config.DefaultAdminCookieConfig, so the cookie names
// are deterministic for the request helpers in this file.
const (
	adminTestCookieName = "admin_access_token"
	adminTestCSRFName   = "csrf_token"
)

// sessionCookies extracts the access-token and CSRF cookie values from a login
// response so a write request can replay both.
func sessionCookies(res *httptest.ResponseRecorder, fix *adminFixture) (access, csrf string) {
	return cookieValue(res, fix.cookieCfg.AccessTokenName), cookieValue(res, fix.cookieCfg.CSRFName)
}

// adminWriteRequest builds a CSRF-bearing write request against the fixture
// router. The group-level CSRF middleware requires Origin, X-CSRF-Token and
// both cookies from the login response.
func adminWriteRequest(method, target, body, accessToken, csrfToken string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: accessToken})
	req.AddCookie(&http.Cookie{Name: adminTestCSRFName, Value: csrfToken})
	return req
}

func TestCreateRoleHandlerCreatesAndLists(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "role-write-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "role-write-admin", "Sup3rSecret!Pass"), fix)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPost, "/api/admin/v1/roles",
		`{"name":"analyst","permissions":["product:read","audit:read"]}`, access, csrf))
	if res.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data roleData `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode create role: %v", err)
	}
	if env.Data.Name != "analyst" || env.Data.ID == "" {
		t.Fatalf("create role data = %+v", env.Data)
	}
	if !containsPermission(env.Data.Permissions, "audit:read") {
		t.Fatalf("created role permissions = %v", env.Data.Permissions)
	}

	// The new role appears in the list.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/roles", nil)
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
	listRes := httptest.NewRecorder()
	router.ServeHTTP(listRes, req)
	var listEnv struct {
		Data roleListData `json:"data"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listEnv); err != nil {
		t.Fatalf("decode roles list: %v", err)
	}
	found := false
	for _, role := range listEnv.Data.List {
		if role.Name == "analyst" {
			found = true
		}
	}
	if !found {
		t.Fatal("created role missing from /roles list")
	}
}

func TestCreateRoleHandlerRejectsOperatorWithoutRoleManage(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "role-op-admin", Password: "Sup3rSecret!Pass",
		RoleName: "operator", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "role-op-admin", "Sup3rSecret!Pass"), fix)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPost, "/api/admin/v1/roles",
		`{"name":"nope","permissions":["product:read"]}`, access, csrf))
	if res.Code != http.StatusForbidden {
		t.Fatalf("operator create role status = %d, want 403; body=%s", res.Code, res.Body.String())
	}
}

func TestCreateRoleHandlerRejectsUnknownPermission422(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "role-422-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "role-422-admin", "Sup3rSecret!Pass"), fix)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPost, "/api/admin/v1/roles",
		`{"name":"bad","permissions":["product:frobnicate"]}`, access, csrf))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown permission status = %d, want 422; body=%s", res.Code, res.Body.String())
	}
}

func TestCreateRoleHandlerRejectsMissingCSRF(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "role-csrf-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "role-csrf-admin", "Sup3rSecret!Pass"), fix)
	_ = csrf

	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/roles",
		strings.NewReader(`{"name":"nocsrf","permissions":["product:read"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
	// No X-CSRF-Token header and no csrf cookie.
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403; body=%s", res.Code, res.Body.String())
	}
}

func TestUpdateRoleHandlerChangesPermissions(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "update-role-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	created, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix), ActorRole: "super_admin",
		Name: "update_me", Permissions: []string{"product:read", "order:read"},
	})
	if err != nil {
		t.Fatalf("seed role: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "update-role-admin", "Sup3rSecret!Pass"), fix)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPatch, "/api/admin/v1/roles/"+created.ID.String(),
		`{"permissions":["refund:read"]}`, access, csrf))
	if res.Code != http.StatusOK {
		t.Fatalf("update role status = %d body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data roleData `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode update role: %v", err)
	}
	if len(env.Data.Permissions) != 1 || env.Data.Permissions[0] != "refund:read" {
		t.Fatalf("updated role permissions = %v", env.Data.Permissions)
	}

	// Unknown role id maps to 404.
	res = httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPatch, "/api/admin/v1/roles/"+uid.New().String(),
		`{"permissions":["product:read"]}`, access, csrf))
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing role status = %d, want 404", res.Code)
	}
}

func TestUpdateAdminUserHandlerDisablesAndReassigns(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "actor-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap actor: %v", err)
	}
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "target-admin", Password: "Sup3rSecret!Pass",
		RoleName: "operator", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap target: %v", err)
	}
	viewer, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix), ActorRole: "super_admin",
		Name: "viewer", Permissions: []string{"product:read"},
	})
	if err != nil {
		t.Fatalf("seed viewer role: %v", err)
	}
	targetID := readAdminID(t, fix.db, "target-admin")
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, csrf := sessionCookies(loginAdmin(t, router, "actor-admin", "Sup3rSecret!Pass"), fix)

	// Disable.
	res := httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPatch,
		"/api/admin/v1/admin-users/"+targetID.String(), `{"enabled":false}`, access, csrf))
	if res.Code != http.StatusOK {
		t.Fatalf("disable status = %d body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data adminUserData `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode disable: %v", err)
	}
	if env.Data.Enabled {
		t.Fatal("disabled admin still enabled in response")
	}

	// Reassign to the viewer role (contract carries role_id).
	res = httptest.NewRecorder()
	router.ServeHTTP(res, adminWriteRequest(http.MethodPatch,
		"/api/admin/v1/admin-users/"+targetID.String(),
		`{"enabled":true,"role_id":"`+viewer.ID.String()+`"}`, access, csrf))
	if res.Code != http.StatusOK {
		t.Fatalf("reassign status = %d body=%s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode reassign: %v", err)
	}
	if !env.Data.Enabled {
		t.Fatal("re-enabled admin still disabled in response")
	}
	if env.Data.Role.Name != "viewer" {
		t.Fatalf("reassigned role = %q, want viewer", env.Data.Role.Name)
	}
}

func TestAdminWriteHandlersRejectUnauthenticated401(t *testing.T) {
	fix := newAdminFixture(t)
	router := newAdminRouter(fix, []string{"https://admin.example.test"})

	// GET endpoints reject a cookie-less request at the AdminAuth middleware.
	for _, target := range []string{"/api/admin/v1/users", "/api/admin/v1/audit-logs"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401; body=%s", target, res.Code, res.Body.String())
		}
	}

	// Write endpoints need a valid Origin and a matching csrf cookie/header to
	// reach the AdminAuth middleware; with no access-token cookie the write is
	// answered 401 (never 500).
	csrf := "unauthenticated-csrf"
	for _, target := range []string{"/api/admin/v1/roles", "/api/admin/v1/roles/1", "/api/admin/v1/admin-users/1"} {
		method := http.MethodPost
		if target != "/api/admin/v1/roles" {
			method = http.MethodPatch
		}
		req := httptest.NewRequest(method, target, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: adminTestCSRFName, Value: csrf})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s (no access cookie) status = %d, want 401; body=%s", method, target, res.Code, res.Body.String())
		}
	}
}

func TestListUsersHandlerReturnsPagedMembers(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "users-read-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, name := range []string{"member-alpha", "member-beta"} {
		if err := fix.db.Exec(`INSERT INTO users (id, openid, nickname, points_balance) VALUES (?, ?, ?, ?)`,
			uid.New(), name+"-openid", name, 30).Error; err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, _ := sessionCookies(loginAdmin(t, router, "users-read-admin", "Sup3rSecret!Pass"), fix)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/users?page=1&page_size=10", nil)
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("users status = %d body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data struct {
			List  []map[string]any `json:"list"`
			Total int64            `json:"total"`
			Page  int              `json:"page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if env.Data.Total < 2 || len(env.Data.List) < 2 {
		t.Fatalf("users total=%d list=%d, want at least 2", env.Data.Total, len(env.Data.List))
	}
	if env.Data.List[0]["points_balance"] == nil || env.Data.List[0]["created_at"] == nil {
		t.Fatalf("member projection missing balance/created_at: %v", env.Data.List[0])
	}

	// Keyword search narrows the result.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/v1/users?keyword=beta", nil)
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode users search: %v", err)
	}
	if env.Data.Total != 1 {
		t.Fatalf("users keyword search total = %d, want 1", env.Data.Total)
	}
}

func TestListAuditLogsHandlerReturnsHistory(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "audit-read-admin", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix), ActorRole: "super_admin",
		Name: "audited_role", Permissions: []string{"product:read"},
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, _ := sessionCookies(loginAdmin(t, router, "audit-read-admin", "Sup3rSecret!Pass"), fix)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/audit-logs?page=1&page_size=10", nil)
	req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("audit-logs status = %d body=%s", res.Code, res.Body.String())
	}
	var env struct {
		Data struct {
			List []struct {
				ID        string `json:"id"`
				Action    string `json:"action"`
				ActorRole string `json:"actor_role"`
				CreatedAt string `json:"created_at"`
			} `json:"list"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode audit-logs: %v", err)
	}
	if env.Data.Total < 2 {
		t.Fatalf("audit total = %d, want at least 2", env.Data.Total)
	}
	found := false
	for _, entry := range env.Data.List {
		if entry.Action == "role.create" && entry.ActorRole == "super_admin" && entry.CreatedAt != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("audit-logs missing the seeded role.create entry")
	}
}

func TestListUsersAndAuditLogsRejectOperator403(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "read-op-admin", Password: "Sup3rSecret!Pass",
		RoleName: "operator", ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	access, _ := sessionCookies(loginAdmin(t, router, "read-op-admin", "Sup3rSecret!Pass"), fix)

	for _, target := range []string{"/api/admin/v1/users", "/api/admin/v1/audit-logs"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: adminTestCookieName, Value: access})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("operator %s status = %d, want 403; body=%s", target, res.Code, res.Body.String())
		}
	}
}
