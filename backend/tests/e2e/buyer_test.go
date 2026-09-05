package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

// request bodies follow the OpenAPI contract exactly: public int64 ids are
// decimal strings, quantities and stock are numbers.

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

func readStrings(t *testing.T, data map[string]any, key string) []string {
	t.Helper()
	value, ok := data[key]
	if !ok {
		t.Fatalf("response data missing %q: %v", key, data)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("response data %q is %T, want array", key, value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("response data %q item is %T, want string", key, item)
		}
		out = append(out, text)
	}
	return out
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
// carrying a "list" array plus pagination fields.
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

func TestBuyerLoginSignupBonusIsExactlyOnce(t *testing.T) {
	h := NewHarness(t, testDB)

	token, user := h.BuyerLogin("code-buyer-a")
	if token == "" {
		t.Fatal("wx-login returned an empty access token")
	}
	if readString(t, user, "id") == "" {
		t.Fatal("wx-login user.id is empty")
	}
	if got := readString(t, user, "points_balance"); got != "100" {
		t.Fatalf("first login points_balance = %s, want exactly-once signup bonus 100", got)
	}
	// The contract's User schema does not carry openid; the response must not
	// leak the server-only WeChat identifier.
	if _, ok := user["openid"]; ok {
		t.Fatalf("wx-login response leaked openid: %v", user)
	}

	// Second login via the SAME deterministic frozen code + openid must not
	// grant a second signup bonus.
	_, userAgain := h.BuyerLogin("code-buyer-a")
	if got := readString(t, userAgain, "points_balance"); got != "100" {
		t.Fatalf("second login points_balance = %s, want 100 (no second bonus)", got)
	}
	if readString(t, userAgain, "id") != readString(t, user, "id") {
		t.Fatalf("second login created a different user row")
	}

	// An unknown WeChat code is rejected, not silently mapped.
	resp := h.BuyerDo("", http.MethodPost, "/api/v1/auth/wx-login", map[string]string{"code": "unknown-code"}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown code status = %d, want 422", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
}

// TestBuyerProfileGetAndUpdate drives the authenticated /me endpoints through
// the real production profile handler: GET returns the login-time profile and
// PATCH applies the contract's updatable fields.
func TestBuyerProfileGetAndUpdate(t *testing.T) {
	h := NewHarness(t, testDB)
	token, loginUser := h.BuyerLogin("code-buyer-a")
	userID := readString(t, loginUser, "id")

	// GET /me matches the login projection: same id, same points, empty profile.
	resp := h.BuyerDo(token, http.MethodGet, "/api/v1/me", nil, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	h.AssertTraceID(resp, env)
	me := jsonMap(t, env.Data)
	if readString(t, me, "id") != userID {
		t.Fatalf("GET /me id = %s, want %s", readString(t, me, "id"), userID)
	}
	if got := readString(t, me, "points_balance"); got != "100" {
		t.Fatalf("GET /me points_balance = %s, want 100", got)
	}
	if got := readString(t, me, "nickname"); got != "" {
		t.Fatalf("fresh profile nickname = %q, want empty", got)
	}

	// PATCH /me updates the nickname and avatar_url; GET /me reflects both.
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/me",
		map[string]string{"nickname": "刀客甲", "avatar_url": "https://cdn.example.test/a.png"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /me status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	updated := jsonMap(t, env.Data)
	if readString(t, updated, "nickname") != "刀客甲" {
		t.Fatalf("patched nickname = %q, want 刀客甲", readString(t, updated, "nickname"))
	}
	if readString(t, updated, "avatar_url") != "https://cdn.example.test/a.png" {
		t.Fatalf("patched avatar_url = %q", readString(t, updated, "avatar_url"))
	}

	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/me", nil, nil)
	env, _ = h.Decode(resp)
	me = jsonMap(t, env.Data)
	if readString(t, me, "nickname") != "刀客甲" {
		t.Fatalf("GET /me after patch nickname = %q", readString(t, me, "nickname"))
	}

	// Partial update must not wipe the absent field: a nickname-only PATCH
	// leaves avatar_url untouched, and vice versa.
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/me", map[string]string{"nickname": "刀客乙"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nickname-only PATCH status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	partial := jsonMap(t, env.Data)
	if readString(t, partial, "nickname") != "刀客乙" {
		t.Fatalf("nickname-only PATCH nickname = %q", readString(t, partial, "nickname"))
	}
	if readString(t, partial, "avatar_url") != "https://cdn.example.test/a.png" {
		t.Fatalf("nickname-only PATCH wiped avatar_url: %q", readString(t, partial, "avatar_url"))
	}
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/me", map[string]string{"avatar_url": "https://cdn.example.test/b.png"}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("avatar-only PATCH status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	partial = jsonMap(t, env.Data)
	if readString(t, partial, "avatar_url") != "https://cdn.example.test/b.png" {
		t.Fatalf("avatar-only PATCH avatar_url = %q", readString(t, partial, "avatar_url"))
	}
	if readString(t, partial, "nickname") != "刀客乙" {
		t.Fatalf("avatar-only PATCH wiped nickname: %q", readString(t, partial, "nickname"))
	}

	// A nickname beyond the contract's 64-character limit is a 422 business
	// error, and an unauthenticated GET /me is a 401.
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/me", map[string]string{"nickname": string(make([]byte, 65))}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized nickname status = %d, want 422", resp.StatusCode)
	}
	resp = h.BuyerDo("", http.MethodGet, "/api/v1/me", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /me status = %d, want 401", resp.StatusCode)
	}
}

func TestBuyerProductBrowseOnSaleAndDetail(t *testing.T) {
	h := NewHarness(t, testDB)

	onSale := h.SeedProduct("On Sale Widget", 10, 100, "on_sale")
	offSale := h.SeedProduct("Off Sale Widget", 20, 5, "off_sale")

	// On-sale listing must include only on_sale products.
	resp := h.BuyerDo("", http.MethodGet, "/api/v1/products?page=1&page_size=20", nil, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list products status = %d", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
	list := listData(t, env.Data)
	foundOnSale, foundOffSale := false, false
	for _, item := range list {
		id := readString(t, item, "id")
		switch readString(t, item, "status") {
		case "on_sale":
			if id == onSale {
				foundOnSale = true
			}
		case "off_sale":
			if id == offSale {
				foundOffSale = true
			}
		}
	}
	if !foundOnSale {
		t.Fatalf("on-sale product %s missing from public list %v", onSale, list)
	}
	if foundOffSale {
		t.Fatalf("off-sale product %s leaked into public list %v", offSale, list)
	}

	// Public detail of an on-sale product returns the int64 id as a string.
	resp = h.BuyerDo("", http.MethodGet, "/api/v1/products/"+onSale, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("on-sale detail status = %d", resp.StatusCode)
	}
	detail := jsonMap(t, env.Data)
	if readString(t, detail, "id") != onSale {
		t.Fatalf("detail id = %s, want %s", readString(t, detail, "id"), onSale)
	}
	if readString(t, detail, "price_points") == "" {
		t.Fatal("detail price_points is empty")
	}

	// Off-sale detail is indistinguishable from a missing product: 404.
	resp = h.BuyerDo("", http.MethodGet, "/api/v1/products/"+offSale, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("off-sale detail status = %d, want 404", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
}

func TestBuyerCartAddAccumulateOffSaleAndDelete(t *testing.T) {
	h := NewHarness(t, testDB)
	token, _ := h.BuyerLogin("code-buyer-a")

	onSale := h.SeedProduct("Cart Widget", 10, 100, "on_sale")
	offSale := h.SeedProduct("Cart Off", 10, 100, "off_sale")

	// Add accumulates onto the same row.
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add cart status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	item := jsonMap(t, env.Data)
	itemID := readString(t, item, "id")
	if readString(t, item, "product_id") != onSale {
		t.Fatalf("cart item product_id = %s, want %s", readString(t, item, "product_id"), onSale)
	}
	if q := item["quantity"].(float64); q != 1 {
		t.Fatalf("cart item quantity = %v, want 1", q)
	}

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 2}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accumulate cart status = %d", resp.StatusCode)
	}
	item = jsonMap(t, env.Data)
	if readString(t, item, "id") != itemID {
		t.Fatalf("accumulate created a new row: got id %s want %s", readString(t, item, "id"), itemID)
	}
	if q := item["quantity"].(float64); q != 3 {
		t.Fatalf("accumulated quantity = %v, want 3", q)
	}

	// Off-sale products remain addable but are reported non-purchasable.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": offSale, "quantity": 1}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add off-sale to cart status = %d (should stay addable)", resp.StatusCode)
	}
	offItem := jsonMap(t, env.Data)
	product := offItem["product"].(map[string]any)
	if readString(t, product, "status") != "off_sale" {
		t.Fatalf("off-sale cart item product.status = %s", readString(t, product, "status"))
	}

	// List shows both items.
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	cart := listData(t, env.Data)
	if len(cart) != 2 {
		t.Fatalf("cart length = %d, want 2", len(cart))
	}

	// Delete removes only the targeted item.
	resp = h.BuyerDo(token, http.MethodDelete, "/api/v1/cart/"+itemID, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete cart status = %d", resp.StatusCode)
	}
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	cart = listData(t, env.Data)
	if len(cart) != 1 {
		t.Fatalf("cart length after delete = %d, want 1", len(cart))
	}
}

func TestBuyerAddressesCRUDDefaultAndVersion(t *testing.T) {
	h := NewHarness(t, testDB)
	token, _ := h.BuyerLogin("code-buyer-a")

	base := map[string]any{
		"receiver": "收件人甲", "phone": "13800138000",
		"region": "北京市朝阳区", "detail": "建国路 88 号", "is_default": true,
	}
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", base, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create address status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	addr := jsonMap(t, env.Data)
	addrID := readString(t, addr, "id")
	if readString(t, addr, "version") != "1" {
		t.Fatalf("new address version = %s, want 1", readString(t, addr, "version"))
	}

	// Creating a second default promotes it and clears the first.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人乙", "phone": "13800138001",
		"region": "上海市浦东新区", "detail": "世纪大道 100 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create second address status = %d", resp.StatusCode)
	}
	addr2 := jsonMap(t, env.Data)
	addr2ID := readString(t, addr2, "id")

	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/addresses", nil, nil)
	env, _ = h.Decode(resp)
	list := listData(t, env.Data)
	if len(list) != 2 {
		t.Fatalf("address list length = %d, want 2", len(list))
	}
	for _, a := range list {
		if readString(t, a, "id") == addr2ID && a["is_default"] != true {
			t.Fatalf("second address should be default: %v", a)
		}
		if readString(t, a, "id") == addrID && a["is_default"] == true {
			t.Fatalf("first address should no longer be default: %v", a)
		}
	}

	// Update flips the default flag and bumps the version.
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/addresses/"+addrID, map[string]any{"is_default": true}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update address status = %d", resp.StatusCode)
	}
	updated := jsonMap(t, env.Data)
	if readString(t, updated, "version") != "2" {
		t.Fatalf("updated address version = %s, want 2", readString(t, updated, "version"))
	}

	// Delete leaves one address.
	resp = h.BuyerDo(token, http.MethodDelete, "/api/v1/addresses/"+addrID, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete address status = %d", resp.StatusCode)
	}
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/addresses", nil, nil)
	env, _ = h.Decode(resp)
	if len(listData(t, env.Data)) != 1 {
		t.Fatalf("address list length after delete = %d, want 1", len(listData(t, env.Data)))
	}
}

// TestBuyerOrderCreationCheckout exercises the real checkout: cart items →
// order with the client-supplied Idempotency-Key, then verifies stock and
// points decremented and the cart emptied.
func TestBuyerOrderCreationCheckout(t *testing.T) {
	h := NewHarness(t, testDB)
	token, user := h.BuyerLogin("code-buyer-a")
	userID := readString(t, user, "id")
	onSale := h.SeedProduct("Checkout Widget", 10, 100, "on_sale")

	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 2}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add cart status = %d", resp.StatusCode)
	}
	itemID := readString(t, jsonMap(t, env.Data), "id")

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人甲", "phone": "13800138000", "region": "北京市朝阳区", "detail": "建国路 88 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrID := readString(t, jsonMap(t, env.Data), "id")

	beforePoints := h.PointsBalance(userID)
	beforeStock := h.ProductStock(onSale)

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders",
		map[string]any{"cart_item_ids": []string{itemID}, "address_id": addrID},
		map[string]string{"Idempotency-Key": "e2e-checkout-token-1"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create order status = %d body=%s", resp.StatusCode, h.dump(env))
	}
	h.AssertTraceID(resp, env)
	order := jsonMap(t, env.Data)
	orderID := readString(t, order, "id")
	if readString(t, order, "status") != "paid" {
		t.Fatalf("order status = %s, want paid", readString(t, order, "status"))
	}
	if readString(t, order, "total_points") != "20" {
		t.Fatalf("order total_points = %s, want 20", readString(t, order, "total_points"))
	}

	// Points paid 20, stock decremented 2, cart emptied.
	if got := h.PointsBalance(userID); got != beforePoints-20 {
		t.Fatalf("points balance = %d, want %d (paid 20)", got, beforePoints-20)
	}
	if got := h.ProductStock(onSale); got != beforeStock-2 {
		t.Fatalf("stock = %d, want %d (dec 2)", got, beforeStock-2)
	}
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if len(listData(t, env.Data)) != 0 {
		t.Fatalf("cart not emptied after checkout: %v", listData(t, env.Data))
	}

	// Ownership: detail and list are buyer-scoped.
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/orders/"+orderID, nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("order detail status = %d", resp.StatusCode)
	}
	if readString(t, jsonMap(t, env.Data), "id") != orderID {
		t.Fatalf("order detail id mismatch")
	}
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/orders", nil, nil)
	env, _ = h.Decode(resp)
	list := listData(t, env.Data)
	if len(list) != 1 || readString(t, list[0], "id") != orderID {
		t.Fatalf("order list = %v, want exactly own order %s", list, orderID)
	}
}

// TestBuyerOrderIdempotencyReplay re-drives the HTTP-level same-token replay:
// the second request returns the exact same order and produces no new
// side effects.
func TestBuyerOrderIdempotencyReplay(t *testing.T) {
	h := NewHarness(t, testDB)
	token, user := h.BuyerLogin("code-buyer-a")
	userID := readString(t, user, "id")
	onSale := h.SeedProduct("Replay Widget", 10, 100, "on_sale")

	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	itemID := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrID := readString(t, jsonMap(t, env.Data), "id")

	payload := map[string]any{"cart_item_ids": []string{itemID}, "address_id": addrID}
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders", payload, map[string]string{"Idempotency-Key": "replay-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first create status = %d", resp.StatusCode)
	}
	first := jsonMap(t, env.Data)
	pointsAfterFirst := h.PointsBalance(userID)
	stockAfterFirst := h.ProductStock(onSale)

	// Same token, same payload: the SAME order row, no new side effects.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders", payload, map[string]string{"Idempotency-Key": "replay-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", resp.StatusCode)
	}
	second := jsonMap(t, env.Data)
	if readString(t, second, "id") != readString(t, first, "id") {
		t.Fatalf("replay order id = %s, want %s", readString(t, second, "id"), readString(t, first, "id"))
	}
	if readString(t, second, "order_no") != readString(t, first, "order_no") {
		t.Fatalf("replay order_no differs")
	}
	if got := h.PointsBalance(userID); got != pointsAfterFirst {
		t.Fatalf("replay changed points: %d != %d", got, pointsAfterFirst)
	}
	if got := h.ProductStock(onSale); got != stockAfterFirst {
		t.Fatalf("replay changed stock: %d != %d", got, stockAfterFirst)
	}
}

// TestBuyerOrderIdempotencyConflictSameTokenDifferentPayload asserts the 409
// contract when the same Idempotency-Key is replayed with a different request.
func TestBuyerOrderIdempotencyConflict(t *testing.T) {
	h := NewHarness(t, testDB)
	token, _ := h.BuyerLogin("code-buyer-a")
	productA := h.SeedProduct("Conflict A", 10, 100, "on_sale")
	productB := h.SeedProduct("Conflict B", 10, 100, "on_sale")

	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": productA, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	itemA := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": productB, "quantity": 1}, nil)
	env, _ = h.Decode(resp)
	itemB := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrID := readString(t, jsonMap(t, env.Data), "id")

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders",
		map[string]any{"cart_item_ids": []string{itemA}, "address_id": addrID},
		map[string]string{"Idempotency-Key": "conflict-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first create status = %d", resp.StatusCode)
	}

	// Same token with a different cart selection is a 409 conflict.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders",
		map[string]any{"cart_item_ids": []string{itemA, itemB}, "address_id": addrID},
		map[string]string{"Idempotency-Key": "conflict-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflict replay status = %d, want 409", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
}

// TestBuyerOrderFailedRetryThenSucceeds verifies that a failed checkout
// (insufficient points) leaves the token unconsumed: after an admin top-up the
// SAME token retry succeeds.
func TestBuyerOrderFailedRetryThenSucceeds(t *testing.T) {
	h := NewHarness(t, testDB)
	token, user := h.BuyerLogin("code-buyer-a")
	userID := readString(t, user, "id")
	onSale := h.SeedProduct("Retry Widget", 500, 100, "on_sale")

	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", map[string]any{"product_id": onSale, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	itemID := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/addresses", map[string]any{
		"receiver": "收件人", "phone": "13800138000", "region": "北京市", "detail": "甲 1 号", "is_default": true,
	}, nil)
	env, _ = h.Decode(resp)
	addrID := readString(t, jsonMap(t, env.Data), "id")
	payload := map[string]any{"cart_item_ids": []string{itemID}, "address_id": addrID}

	// 100 points cannot cover a 500-point product.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders", payload, map[string]string{"Idempotency-Key": "retry-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("underfunded order status = %d, want 422", resp.StatusCode)
	}
	h.AssertTraceID(resp, env)
	if got := h.PointsBalance(userID); got != 100 {
		t.Fatalf("failed order must not spend points: balance = %d", got)
	}

	// Admin top-up via the real points:adjust endpoint.
	resp = h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/points/adjust",
		map[string]any{"user_id": userID, "delta": "500", "remark": "e2e top-up"},
		map[string]string{"Idempotency-Key": "topup-1"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("top-up status = %d body=%s", resp.StatusCode, h.dump(env))
	}

	// Same token retry now succeeds.
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/orders", payload, map[string]string{"Idempotency-Key": "retry-token"})
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200: %s", resp.StatusCode, h.dump(env))
	}
	if got := h.PointsBalance(userID); got != 100 { // 100 + 500 - 500
		t.Fatalf("final points = %d, want 100", got)
	}
}

func (h *Harness) dump(env envelope) []byte {
	raw, _ := json.Marshal(env)
	return raw
}
