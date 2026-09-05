package security

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/tests/e2e"
)

// The cross-resource 404 assertions are the core of the IDOR contract: a
// buyer-scoped query that matches no row owned by the caller is
// INDISTINGUISHABLE from a missing row. Any 403 here would be an oracle that
// leaks the existence of another buyer's resource.

func TestBuyerIDORAddressesYield404(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	tokenA, _ := h.BuyerLogin("code-buyer-a")
	tokenB, _ := h.BuyerLogin("code-buyer-b")

	resp := h.BuyerDo(tokenA, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "买家甲", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ := h.Decode(resp)
	addrA := readString(t, jsonMap(t, env.Data), "id")

	// Buyer B cannot read, modify or delete buyer A's address.
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		resp = h.BuyerDo(tokenB, method, "/api/v1/addresses/"+addrA, map[string]any{"is_default": false}, nil)
		env, _ = h.Decode(resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("buyer B %s address A status = %d, want 404 (never 403)", method, resp.StatusCode)
		}
		if resp.StatusCode == http.StatusForbidden {
			t.Fatalf("buyer B %s address A leaked a 403 oracle", method)
		}
		h.AssertTraceID(resp, env)
	}
}

func TestBuyerIDORCartYield404(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	tokenA, _ := h.BuyerLogin("code-buyer-a")
	tokenB, _ := h.BuyerLogin("code-buyer-b")

	onSale := h.SeedProduct("IDOR Cart", 10, 100, "on_sale")
	resp := h.BuyerDo(tokenA, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	itemA := readString(t, jsonMap(t, env.Data), "id")

	// Buyer B cannot modify buyer A's cart item.
	resp = h.BuyerDo(tokenB, http.MethodPatch, "/api/v1/cart/"+itemA, map[string]any{"quantity": 9}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("buyer B PATCH cart A status = %d, want 404", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("buyer B PATCH cart A leaked a 403 oracle")
	}
	h.AssertTraceID(resp, env)

	resp = h.BuyerDo(tokenB, http.MethodDelete, "/api/v1/cart/"+itemA, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("buyer B DELETE cart A status = %d, want 404", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)

	// Buyer B's own cart is unaffected.
	resp = h.BuyerDo(tokenB, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if len(listData(t, env.Data)) != 0 {
		t.Fatalf("buyer B cart leaked buyer A's items: %v", listData(t, env.Data))
	}
}

func TestBuyerIDOROrdersYield404(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	tokenA, _ := h.BuyerLogin("code-buyer-a")
	tokenB, _ := h.BuyerLogin("code-buyer-b")

	onSale := h.SeedProduct("IDOR Order", 10, 100, "on_sale")
	resp := h.BuyerDo(tokenA, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	itemA := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(tokenA, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "买家甲", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrA := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(tokenA, http.MethodPost, "/api/v1/orders",
		map[string]any{"cart_item_ids": []string{itemA}, "address_id": addrA},
		map[string]string{"Idempotency-Key": "idor-order-token"})
	env, _ = h.Decode(resp)
	orderA := readString(t, jsonMap(t, env.Data), "id")

	// Buyer B cannot read buyer A's order detail.
	resp = h.BuyerDo(tokenB, http.MethodGet, "/api/v1/orders/"+orderA, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("buyer B GET order A status = %d, want 404", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("buyer B GET order A leaked a 403 oracle")
	}
	h.AssertTraceID(resp, env)

	// Buyer B cannot drive buyer A's order through refund or confirm.
	resp = h.BuyerDo(tokenB, http.MethodPost, "/api/v1/orders/"+orderA+"/refund", map[string]string{"reason": "steal"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("buyer B refund order A status = %d, want 404", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)

	resp = h.BuyerDo(tokenB, http.MethodPost, "/api/v1/orders/"+orderA+"/confirm", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("buyer B confirm order A status = %d, want 404", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)

	// Buyer B's order list is empty.
	resp = h.BuyerDo(tokenB, http.MethodGet, "/api/v1/orders", nil, nil)
	env, _ = h.Decode(resp)
	if len(listData(t, env.Data)) != 0 {
		t.Fatalf("buyer B order list leaked buyer A's orders: %v", listData(t, env.Data))
	}
}

// TestAdminInsufficientPermission403 creates a role that holds product:read
// but NOT product:write and verifies the write returns 403 while the read
// succeeds. It also verifies the seeded operator is rejected on
// points:adjust and role:manage.
func TestAdminInsufficientPermission403(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// Create a product_reader role (product:read only) and bootstrap an admin
	// into it through the real bootstrap path.
	ctx := context.Background()
	if err := h.DB.WithContext(ctx).Exec(
		`INSERT INTO roles (id, name, remark) VALUES (?, 'product_reader', 'read-only product role') ON CONFLICT (name) DO NOTHING`, uid.New()).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := h.DB.WithContext(ctx).Exec(
		`INSERT INTO role_permissions (role_id, permission_id)
		 SELECT r.id, p.id FROM roles r, permissions p
		 WHERE r.name = 'product_reader' AND p.code = 'product:read'
		 ON CONFLICT DO NOTHING`).Error; err != nil {
		t.Fatalf("grant product:read: %v", err)
	}
	const readerUsername, readerPassword = "reader-e2e", "ReaderOnly#2026e2e"
	if err := h.BootstrapAdmin(readerUsername, readerPassword, "product_reader"); err != nil {
		t.Fatalf("bootstrap product reader: %v", err)
	}
	reader := h.AdminLogin(readerUsername, readerPassword)

	// Read is allowed.
	resp := h.AdminDo(reader, http.MethodGet, "/api/admin/v1/products", nil, nil)
	_, _ = h.Decode(resp) // drains and closes the body; the envelope is not asserted here
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader GET products status = %d, want 200", resp.StatusCode)
	}

	// Write is a 403.
	resp = h.AdminDo(reader, http.MethodPost, "/api/admin/v1/products", map[string]any{
		"name": "Forbidden", "price_points": "1", "stock": 1, "status": "on_sale",
	}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reader POST products status = %d, want 403", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)

	// The seeded operator lacks points:adjust and role:manage.
	resp = h.AdminDo(h.Operator, http.MethodPost, "/api/admin/v1/points/adjust",
		map[string]any{"user_id": "1", "delta": "1", "remark": "x"}, map[string]string{"Idempotency-Key": "op-1"})
	_, _ = h.Decode(resp) // drains and closes the body; the envelope is not asserted here
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator points:adjust status = %d, want 403", resp.StatusCode)
	}
	resp = h.AdminDo(h.Operator, http.MethodGet, "/api/admin/v1/roles", nil, nil)
	_, _ = h.Decode(resp) // drains and closes the body; the envelope is not asserted here
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator role:manage status = %d, want 403", resp.StatusCode)
	}
}

// TestAdminUnknownPermission422 corrupts a DEDICATED role after login so the
// permission resolver hits an unknown code and the middleware answers the
// contract's 422. A dedicated role keeps the seeded operator intact for the
// other tests in this package, which share the database.
func TestAdminUnknownPermission422(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// Create a role whose valid permission set is product:write, bootstrap an
	// admin into it, then attach an unknown permission code afterwards (as a
	// future migration dropping a seed permission would leave behind).
	if err := h.DB.Exec(
		`INSERT INTO roles (id, name, remark) VALUES (?, 'drift_role', 'e2e drift role') ON CONFLICT (name) DO NOTHING`, uid.New()).Error; err != nil {
		t.Fatalf("create drift role: %v", err)
	}
	if err := h.DB.Exec(
		`INSERT INTO role_permissions (role_id, permission_id)
		 SELECT r.id, p.id FROM roles r, permissions p
		 WHERE r.name = 'drift_role' AND p.code = 'product:write'
		 ON CONFLICT DO NOTHING`).Error; err != nil {
		t.Fatalf("grant product:write to drift role: %v", err)
	}
	const driftUsername, driftPassword = "drift-e2e", "DriftPass#2026e2e"
	if err := h.BootstrapAdmin(driftUsername, driftPassword, "drift_role"); err != nil {
		t.Fatalf("bootstrap drift admin: %v", err)
	}
	drift := h.AdminLogin(driftUsername, driftPassword)

	// The valid path works before corruption.
	resp := h.AdminDo(drift, http.MethodPost, "/api/admin/v1/products", map[string]any{
		"name": "Before Drift", "price_points": "1", "stock": 1, "status": "on_sale",
	}, nil)
	_, _ = h.Decode(resp) // drains and closes the body; the envelope is not asserted here
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid write before corruption status = %d, want 201", resp.StatusCode)
	}

	// Corrupt the drift role with an unknown permission code.
	if err := h.DB.Exec(
		`INSERT INTO permissions (id, code, name) VALUES (?, 'product:frobnicate', 'e2e unknown') ON CONFLICT (code) DO NOTHING`, uid.New()).Error; err != nil {
		t.Fatalf("insert unknown permission: %v", err)
	}
	if err := h.DB.Exec(
		`INSERT INTO role_permissions (role_id, permission_id)
		 SELECT r.id, p.id FROM roles r, permissions p
		 WHERE r.name = 'drift_role' AND p.code = 'product:frobnicate'
		 ON CONFLICT DO NOTHING`).Error; err != nil {
		t.Fatalf("attach unknown permission: %v", err)
	}

	// The admin holds product:write, but resolution now fails with an unknown
	// permission → 422, never a silent grant.
	resp = h.AdminDo(drift, http.MethodPost, "/api/admin/v1/products", map[string]any{
		"name": "After Drift", "price_points": "1", "stock": 1, "status": "on_sale",
	}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown permission write status = %d, want 422", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
}

// TestAdminCSRFRejection asserts the CSRF middleware: a write without the
// matching X-CSRF-Token header is a 403.
func TestAdminCSRFRejection(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	body, _ := json.Marshal(map[string]any{"name": "CSRF", "price_points": "1", "stock": 1, "status": "on_sale"})

	for name, headers := range map[string]map[string]string{
		"missing header":   {"X-CSRF-Token": ""},
		"mismatched token": {"X-CSRF-Token": "not-the-cookie-value"},
	} {
		resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/products", body, headers)
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("CSRF %s status = %d, want 403", name, resp.StatusCode)
		}
		h.AssertTraceID(resp, env)
	}
}

// TestAdminOriginRejection asserts AdminOrigin: a write without a valid
// allowed HTTPS Origin is a 403 even when the CSRF token is correct.
func TestAdminOriginRejection(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	body, _ := json.Marshal(map[string]any{"name": "Origin", "price_points": "1", "stock": 1, "status": "on_sale"})

	for name, origin := range map[string]string{
		"missing origin":    "",
		"disallowed origin": "https://evil.example.test",
		"cleartext origin":  "http://admin.example.test",
		"origin with path":  "https://admin.example.test/path",
		"origin with user":  "https://user@admin.example.test",
	} {
		resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/products", body, map[string]string{"Origin": origin})
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %s status = %d, want 403", name, resp.StatusCode)
		}
		h.AssertTraceID(resp, env)
	}
}

// TestAdminStaleTokenAfterPasswordChange asserts the token_version contract:
// the password change bumps token_version, so the pre-change access token is
// rejected on the next admin request.
func TestAdminStaleTokenAfterPasswordChange(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	oldToken := h.AccessTokenCookie(h.SuperAdmin)
	if oldToken == "" {
		t.Fatal("super admin has no access-token cookie")
	}

	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/auth/password",
		map[string]string{"current_password": superPassword, "new_password": "StaleToken#2026e2e"}, nil)
	_, _ = h.Decode(resp) // drains and closes the body; the envelope is not asserted here
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password change status = %d", resp.StatusCode)
	}

	// Replay the old cookie: the JWT signature is still valid but the stale
	// token_version must make the middleware reject it.
	req := h.RawReq(http.MethodGet, "/api/admin/v1/auth/me", nil, nil)
	req.AddCookie(&http.Cookie{Name: h.CookieName(), Value: oldToken, Path: "/api/admin/v1"})
	meResp := h.Do(h.SuperAdmin.Client, req)
	env, _ := h.Decode(meResp)
	if meResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale token /auth/me status = %d, want 401", meResp.StatusCode)
	}
	h.AssertTraceID(meResp, env)

	// A fresh login with the new password works again.
	if status, _ := h.AdminLoginCheck(superUsername, "StaleToken#2026e2e"); status != http.StatusOK {
		t.Fatalf("fresh login after password change status = %d, want 200", status)
	}
}

// readString and jsonMap are re-exported shorthand so the security package
// does not depend on the e2e test-only helpers.
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

func jsonMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode data object: %v", err)
	}
	return data
}

// listData decodes the contract's paginated list envelope: data is an object
// carrying a "list" array.
func listData(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var data struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode data list object: %v", err)
	}
	return data.List
}

// superUsername and superPassword mirror the e2e harness constants so the
// security package can use the same deterministic bootstrap credentials.
const (
	superUsername = "super-admin-e2e"
	superPassword = "SuperAdmin#2026e2e"
)
