package e2e

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// realPNG returns a small, fully decodable PNG image rendered by the Go
// standard library. Width and height are requested; the encoded bytes are the
// real magic bytes a browser would send.
func realPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestAdminLoginAndMe(t *testing.T) {
	h := NewHarness(t, testDB)

	// The harness logged the super_admin in during setup; /auth/me must
	// resolve the role object with its permissions.
	resp := h.AdminDo(h.SuperAdmin, http.MethodGet, "/api/admin/v1/auth/me", nil, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	h.AssertTraceID(resp, env)
	me := jsonMap(t, env.Data)
	if readString(t, me, "username") != superUsername {
		t.Fatalf("me.username = %s", readString(t, me, "username"))
	}
	role := me["role"].(map[string]any)
	perms := role["permissions"].([]any)
	hasWrite := false
	for _, p := range perms {
		if p == "product:write" {
			hasWrite = true
		}
	}
	if !hasWrite {
		t.Fatalf("super_admin role.permissions missing product:write: %v", perms)
	}

	// Wrong password is rejected with 401.
	status, _ := h.AdminLoginCheck(superUsername, "WrongPassword#999")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong-password login status = %d, want 401", status)
	}
}

func TestAdminPasswordChangeCurrentPassword(t *testing.T) {
	h := NewHarness(t, testDB)

	// Wrong current_password is rejected.
	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/auth/password",
		map[string]string{"current_password": "NotThePassword#1", "new_password": "NewSuperPass#2026"},
		nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong current_password status = %d, want 401", resp.StatusCode)
	}

	// Correct current_password changes the password.
	newPassword := "NewSuperPass#2026"
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/auth/password",
		map[string]string{"current_password": superPassword, "new_password": newPassword}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password change status = %d body=%s", resp.StatusCode, h.dump(env))
	}

	// Old password no longer authenticates; the new one does.
	if status, _ := h.AdminLoginCheck(superUsername, superPassword); status != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d, want 401", status)
	}
	if status, _ := h.AdminLoginCheck(superUsername, newPassword); status != http.StatusOK {
		t.Fatalf("new password login status = %d, want 200", status)
	}
}

func TestAdminProductUploadCreateUpdate(t *testing.T) {
	h := NewHarness(t, testDB)

	// Upload a real image: server-generated key + URL + dimensions.
	content := realPNG(64, 64)
	resp := h.AdminUpload(h.SuperAdmin, "product.png", "image/png", content, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	h.AssertTraceID(resp, env)
	upload := jsonMap(t, env.Data)
	key := readString(t, upload, "key")
	if !strings.HasSuffix(key, ".png") {
		t.Fatalf("upload key %q does not end with .png", key)
	}
	if readString(t, upload, "url") == "" {
		t.Fatal("upload url is empty")
	}

	// Create a product referencing the uploaded image key.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/products",
		map[string]any{
			"name": "Admin Widget", "description": "created over HTTP",
			"images": []string{key}, "price_points": "100", "stock": 10, "status": "on_sale",
		}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create product status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	product := jsonMap(t, env.Data)
	productID := readString(t, product, "id")
	resolvedImage := readString(t, product, "main_image")
	if !strings.HasSuffix(resolvedImage, key) {
		t.Fatalf("product main_image = %s, want suffix %s", resolvedImage, key)
	}
	if gallery := readStrings(t, product, "images"); len(gallery) != 1 || !strings.HasSuffix(gallery[0], key) {
		t.Fatalf("product images = %v, want [%s]", gallery, key)
	}

	// Update the product with a partial patch.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPatch, "/api/admin/v1/products/"+productID,
		map[string]any{"stock": 5, "price_points": "120"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update product status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	updated := jsonMap(t, env.Data)
	if s := updated["stock"].(float64); s != 5 {
		t.Fatalf("updated stock = %v, want 5", s)
	}
	if readString(t, updated, "price_points") != "120" {
		t.Fatalf("updated price_points = %s, want 120", readString(t, updated, "price_points"))
	}
}

// TestAdminShippingAndRefundApproval drives an order through ship → refund
// request → approve and asserts points/stock are restored exactly once.
func TestAdminShippingAndRefundApproval(t *testing.T) {
	h := NewHarness(t, testDB)

	// Build a paid order as buyer-a.
	token, user := h.BuyerLogin("code-buyer-a")
	userID := readString(t, user, "id")
	onSale := h.SeedProduct("Refund Widget", 10, 100, "on_sale")
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 2}, nil)
	env, _ := h.Decode(resp)
	itemID := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrID := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders",
		map[string]any{"cart_item_ids": []string{itemID}, "address_id": addrID},
		map[string]string{"Idempotency-Key": "refund-order-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create order status = %d", resp.StatusCode)
	}
	orderID := readString(t, jsonMap(t, env.Data), "id")
	pointsPaid := h.PointsBalance(userID) // = 100 - 20
	stockAfter := h.ProductStock(onSale)  // = 100 - 2

	// Admin ships.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/orders/"+orderID+"/ship", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ship status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	if readString(t, jsonMap(t, env.Data), "status") != "shipped" {
		t.Fatalf("order status after ship != shipped")
	}

	// Buyer requests refund.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders/"+orderID+"/refund", map[string]string{"reason": "not satisfied"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refund request status = %d", resp.StatusCode)
	}
	if readString(t, jsonMap(t, env.Data), "status") != "refund_requested" {
		t.Fatalf("order status after request != refund_requested")
	}

	// Admin approves: points and stock restored.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/refunds/"+orderID+"/approve", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refund approve status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	if readString(t, jsonMap(t, env.Data), "status") != "refunded" {
		t.Fatalf("order status after approve != refunded")
	}
	if got := h.PointsBalance(userID); got != pointsPaid+20 {
		t.Fatalf("points after refund = %d, want %d (restored once)", got, pointsPaid+20)
	}
	if got := h.ProductStock(onSale); got != stockAfter+2 {
		t.Fatalf("stock after refund = %d, want %d (restored once)", got, stockAfter+2)
	}

	// Approving again is a state conflict and must NOT restore a second time.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/refunds/"+orderID+"/approve", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second approve status = %d, want 409", resp.StatusCode)
	}
	if got := h.PointsBalance(userID); got != pointsPaid+20 {
		t.Fatalf("points changed on second approve: %d", got)
	}
	if got := h.ProductStock(onSale); got != stockAfter+2 {
		t.Fatalf("stock changed on second approve: %d", got)
	}
}

func TestAdminPointsAdjustmentIdempotentAndMandatoryRemark(t *testing.T) {
	h := NewHarness(t, testDB)
	userID := h.SeedUser("topup-target-openid", 100)

	adjustBody := map[string]any{"user_id": userID, "delta": "50", "remark": "e2e bonus"}

	// First apply.
	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/points/adjust", adjustBody,
		map[string]string{"Idempotency-Key": "adj-1"})
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("points adjust status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	entry := jsonMap(t, env.Data)
	if readString(t, entry, "balance_after") != "150" {
		t.Fatalf("balance_after = %s, want 150", readString(t, entry, "balance_after"))
	}
	if got := h.PointsBalance(userID); got != 150 {
		t.Fatalf("points balance = %d, want 150", got)
	}

	// Same key replay returns the same entry, no double apply.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/points/adjust", adjustBody,
		map[string]string{"Idempotency-Key": "adj-1"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("points adjust replay status = %d", resp.StatusCode)
	}
	replayed := jsonMap(t, env.Data)
	if readString(t, replayed, "id") != readString(t, entry, "id") {
		t.Fatalf("replay returned a different ledger entry")
	}
	if got := h.PointsBalance(userID); got != 150 {
		t.Fatalf("replay double-applied: balance = %d", got)
	}

	// Same key, different delta → conflict.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/points/adjust",
		map[string]any{"user_id": userID, "delta": "999", "remark": "changed my mind"},
		map[string]string{"Idempotency-Key": "adj-1"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting replay status = %d, want 409", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)

	// Mandatory remark: missing remark is a 422.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/points/adjust",
		map[string]any{"user_id": userID, "delta": "10", "remark": ""},
		map[string]string{"Idempotency-Key": "adj-2"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing remark status = %d, want 400", resp.StatusCode)
	}
}
