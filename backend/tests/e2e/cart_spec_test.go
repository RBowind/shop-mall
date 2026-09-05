package e2e

// Acceptance tests for specs/cart/spec.md (sprint contract
// specs/cart/contract.md). Each test name carries the contract B id it
// anchors. Scenarios already verified elsewhere are mapped in
// B* ↔ test coverage below and NOT duplicated here:
//
//   B1 TestSpecCartB1_DuplicateAddMergesIntoOneRowWithAccumulatedQuantity (this file)
//   B2 TestSpecCartB2_AddWithoutValidBuyerJWTRejected401CartUnchanged (this file)
//   B3 TestSpecCartB3_UpdateQuantityTakesEffectAndPersists (this file)
//   B4 TestSpecCartB4_DeleteRowNoLongerAppearsInList (this file)
//   B5-B8 续写于本文件，按 B 编号分节。
//   B9  端内行为，归小程序 E2E（TS 套件），不在 Go 层。

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/tokens"

	"gorm.io/gorm"
)

// spec: B1
//
// 已登录买家两次加购同一商品：购物车只保留一行，数量为两次之和。
//
// 四层断言覆盖说明：
//  1. 端到端响应：两次 POST /api/v1/cart 走真实路由，断言状态码、业务码、
//     第二次的响应仍是同一行 id 且 quantity 为两次之和；GET /api/v1/cart
//     只返回该商品的一行。
//  2. 参数 edge case：B1 行为边界内的组合——第三次加购仍合并（累加规则对
//     2 次以上成立）、另一商品不被合并（"同商品才一行"的反向边界）。
//     quantity ≤ 0 / 非整数属 spec「数量非法」scenario（B6）；product_id
//     缺失/非法 spec 未定义其行为——未列入 spec Coverage Gaps（该节只列
//     "已下架或零库存商品"），但同属未定义项——无对应 B，本用例不覆盖。
//  3. 外部服务调用参数：加购链路不触达任何第三方 HTTP 服务（微信调用只属于
//     登录端点，商品读取走进程内 product service），无可 mock 的外部调用，
//     本层缺省。
//  4. 数据库字段变动：绕过 API 返回值，直查 cart_items，按 (user_id,
//     product_id) 断言行数恰为 1、quantity 恰为累加和。
func TestSpecCartB1_DuplicateAddMergesIntoOneRowWithAccumulatedQuantity(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b1", "cart-b1-openid")
	token, user := h.BuyerLogin("code-cart-b1")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}

	productA := h.SeedProduct("Cart B1 Widget", 10, 100, "on_sale")
	productB := h.SeedProduct("Cart B1 Other", 10, 100, "on_sale")
	productAID, err := uid.ParseCanonical(productA)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productBID, err := uid.ParseCanonical(productB)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// WHEN 第一次加购：quantity 最小合法值 1。
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 1}, nil)
	env, _ := h.Decode(resp)
	h.AssertTraceID(resp, env)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first add status = %d body=%s, want 200", resp.StatusCode, env.Message)
	}
	if env.Code != 0 {
		t.Fatalf("first add business code = %d, want 0", env.Code)
	}
	first := jsonMap(t, env.Data)
	if readString(t, first, "product_id") != productA {
		t.Fatalf("first add product_id = %s, want %s", readString(t, first, "product_id"), productA)
	}
	if q := first["quantity"].(float64); q != 1 {
		t.Fatalf("first add quantity = %v, want 1", q)
	}
	itemID := readString(t, first, "id")

	// THEN 第二次加购同一商品（quantity=2）：仍是同一行，数量为两次之和 3。
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 2}, nil)
	env, _ = h.Decode(resp)
	h.AssertTraceID(resp, env)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second add status = %d body=%s, want 200", resp.StatusCode, env.Message)
	}
	if env.Code != 0 {
		t.Fatalf("second add business code = %d, want 0", env.Code)
	}
	second := jsonMap(t, env.Data)
	if got := readString(t, second, "id"); got != itemID {
		t.Fatalf("duplicate add created a new row: id %s, want same row %s", got, itemID)
	}
	if q := second["quantity"].(float64); q != 3 {
		t.Fatalf("duplicate add quantity = %v, want 1+2=3", q)
	}

	// THEN 列表侧观测：该商品在购物车中只有一行，数量为两次之和。
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	assertSingleListRow(t, listData(t, env.Data), productA, 3)

	// THEN 数据库直查（层 4）：(user_id, product_id) 恰一行且 quantity=3，
	// 不只信 API 返回值。
	assertCartRow(t, h.DB, userID, productAID, 1, 3)

	// 层 2 edge：第三次加购（quantity=5）仍合并进同一行，累加到 8——
	// "重复加购累加数量"对 2 次以上成立。
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 5}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("third add status = %d, want 200", resp.StatusCode)
	}
	third := jsonMap(t, env.Data)
	if got := readString(t, third, "id"); got != itemID {
		t.Fatalf("third add created a new row: id %s, want same row %s", got, itemID)
	}
	if q := third["quantity"].(float64); q != 8 {
		t.Fatalf("third add quantity = %v, want 1+2+5=8", q)
	}
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	assertSingleListRow(t, listData(t, env.Data), productA, 8)
	assertCartRow(t, h.DB, userID, productAID, 1, 8)

	// 层 2 edge：另一商品加购不被合并——"同一商品只保留一行"的边界只在同
	// 商品上成立，不同商品各自成行。
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productB, "quantity": 2}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add product B status = %d, want 200", resp.StatusCode)
	}
	assertCartRow(t, h.DB, userID, productBID, 1, 2)
	assertCartRow(t, h.DB, userID, productAID, 1, 8)
	var totalRows int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ?`, userID).Scan(&totalRows).Error; err != nil {
		t.Fatalf("count buyer cart rows: %v", err)
	}
	if totalRows != 2 {
		t.Fatalf("buyer cart rows = %d, want 2 (one per product)", totalRows)
	}
}

// spec: B2
//
// 未登录加购：无有效买家 JWT 请求 POST /api/v1/cart 被 401 拒绝，购物车不变。
//
// 四层断言覆盖说明：
//  1. 端到端响应：先以有效 JWT 加购 200/code=0 建立正对照与基线（防止路由
//     配错一律 401 造成的假绿），再让每种「无有效买家 JWT」形态走真实路由
//     打加购，断言状态码 401 且 envelope 业务码非 0（项目 envelope 约定：
//     code=0 为成功，拒绝必须非 0）。
//  2. 参数 edge case：本 B 决定行为的入参是凭据，「无效」取值域逐条覆盖——
//     无 Authorization 头、Bearer 空 token、Bearer 非 JWT 串、过期、伪造
//     签名、管理域签发打买家端点。quantity/product_id 取合法最小集：
//     quantity ≤ 0 / 非整数属 spec「数量非法」scenario（B6）；product_id
//     缺失/非法 spec 未定义其行为——未列入 spec Coverage Gaps（该节只列
//     "已下架或零库存商品"），但同属未定义项——无对应 B，本用例不覆盖。
//  3. 外部服务调用参数：加购链路不触达任何第三方 HTTP 服务（微信调用只属于
//     登录端点，商品读取走进程内 product service），无可 mock 的外部调用，
//     本层缺省。
//  4. 数据库字段变动：每次拒绝后直查 cart_items，断言本次请求目标商品 0 行、
//     既有行 quantity 未变、全表行数与打请求前基线一致——「购物车不变」的
//     正向证据，不只信 API 返回值。
func TestSpecCartB2_AddWithoutValidBuyerJWTRejected401CartUnchanged(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b2", "cart-b2-openid")
	token, user := h.BuyerLogin("code-cart-b2")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}
	userIDStr := userID.String()

	productA := h.SeedProduct("Cart B2 Existing", 10, 100, "on_sale")
	productB := h.SeedProduct("Cart B2 Target", 10, 100, "on_sale")
	productAID, err := uid.ParseCanonical(productA)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productBID, err := uid.ParseCanonical(productB)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// 层 1 正对照 + 基线：有效 JWT 加购成功建一行（productA, quantity 2），
	// 「购物车不变」由此获得可对照的正向基线。
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 2}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("control add status = %d body=%s, want 200", resp.StatusCode, env.Message)
	}
	if env.Code != 0 {
		t.Fatalf("control add business code = %d, want 0", env.Code)
	}
	assertCartRow(t, h.DB, userID, productAID, 1, 2)

	var baselineRows int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&baselineRows).Error; err != nil {
		t.Fatalf("count cart_items baseline: %v", err)
	}
	if baselineRows != 1 {
		t.Fatalf("cart_items baseline rows = %d, want 1", baselineRows)
	}

	// 过期/伪造/跨域 token 的签发作法对照 buyer_auth B3，但本用例只观测
	// 加购端点对「未登录」的拒绝行为，不重测认证域语义。
	now := time.Now().UTC()
	buyerSigner, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte(buyerKey)},
	})
	if err != nil {
		t.Fatalf("buyer signer: %v", err)
	}
	expiredToken, err := buyerSigner.IssueAt(userIDStr, 1, now.Add(-2*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	forgedSigner, err := tokens.NewSigner(config.JWTConfig{
		Issuer: buyerIssuer, Audience: buyerAudience, TTL: 24 * time.Hour,
		ActiveKID: buyerKID, Keys: map[string][]byte{buyerKID: []byte("forged-key-material-32bytes-xxxxxxx")},
	})
	if err != nil {
		t.Fatalf("forged signer: %v", err)
	}
	forgedToken, err := forgedSigner.Issue(userIDStr, 1, now)
	if err != nil {
		t.Fatalf("issue forged token: %v", err)
	}
	adminSigner, err := tokens.NewSigner(config.JWTConfig{
		Issuer: adminIssuer, Audience: adminAudience, TTL: 30 * time.Minute,
		ActiveKID: adminKID, Keys: map[string][]byte{adminKID: []byte(adminKey)},
	})
	if err != nil {
		t.Fatalf("admin signer: %v", err)
	}
	adminToken, err := adminSigner.Issue(userIDStr, 1, now)
	if err != nil {
		t.Fatalf("issue admin-domain token: %v", err)
	}

	// 层 2：「无有效买家 JWT」的每种形态逐条打同一加购接口。请求体固定为
	// 合法最小集（productB, quantity 3），保证拒绝原因只能是凭据无效。
	body := map[string]any{"product_id": productB, "quantity": 3}
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal cart add body: %v", err)
	}
	attempts := []struct {
		name string
		run  func() *http.Response
	}{
		{"无 Authorization 头", func() *http.Response {
			return h.BuyerDo("", http.MethodPost, "/api/v1/cart", body, nil)
		}},
		{"Bearer 空 token", func() *http.Response {
			req := h.RawReq(http.MethodPost, "/api/v1/cart", rawBody, map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer ",
			})
			return h.Do(nil, req)
		}},
		{"Bearer 非 JWT 串", func() *http.Response {
			return h.BuyerDo("not-a-jwt", http.MethodPost, "/api/v1/cart", body, nil)
		}},
		{"过期买家令牌", func() *http.Response {
			return h.BuyerDo(expiredToken, http.MethodPost, "/api/v1/cart", body, nil)
		}},
		{"伪造签名令牌", func() *http.Response {
			return h.BuyerDo(forgedToken, http.MethodPost, "/api/v1/cart", body, nil)
		}},
		{"管理域令牌打买家端点", func() *http.Response {
			return h.BuyerDo(adminToken, http.MethodPost, "/api/v1/cart", body, nil)
		}},
	}
	for _, attempt := range attempts {
		// THEN 层 1：401，且 envelope 业务码非 0。
		resp := attempt.run()
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d body=%s, want 401", attempt.name, resp.StatusCode, env.Message)
		}
		if env.Code == 0 {
			t.Fatalf("%s: rejection envelope business code = 0 (success code), want non-zero", attempt.name)
		}

		// THEN 层 4：购物车不变的正向证据——目标商品 0 行、既有行未被动过、
		// 全表行数与基线一致（防以他人身份插行）。
		assertCartRow(t, h.DB, userID, productBID, 0, 0)
		assertCartRow(t, h.DB, userID, productAID, 1, 2)
		var totalRows int64
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&totalRows).Error; err != nil {
			t.Fatalf("%s: count cart_items: %v", attempt.name, err)
		}
		if totalRows != baselineRows {
			t.Fatalf("%s: cart_items total rows = %d, want baseline %d", attempt.name, totalRows, baselineRows)
		}
	}

	// 全部拒绝后以有效 JWT 拉列表做侧观测：仍只有既有行且数量未变，
	// 被拒的目标商品不出现在列表。
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	rows := listData(t, env.Data)
	assertSingleListRow(t, rows, productA, 2)
	for _, row := range rows {
		if readString(t, row, "product_id") == productB {
			t.Fatalf("rejected add created list-visible row for product %s", productB)
		}
	}
}

// spec: B3
//
// 改数量：买家对购物车行 PATCH 合法新数量后变更生效且持久——重新拉取列表返回
// 新数量，DB 中该行 quantity 亦为新值。
//
// 四层断言覆盖说明：
//  1. 端到端响应：先加购建 A、B 两行拿到 itemId，再对目标行 PATCH 合法新数量
//     走真实路由（PATCH /api/v1/cart/{itemId}），断言 200、业务码 0、trace_id、
//     响应 data.id 仍为同一行且 data.quantity 为新值；随后 GET /api/v1/cart
//     重新拉取列表，目标行返回新数量（spec THEN 明确要求「重新拉取列表返回新
//     数量」，列表侧与响应侧一致性都查）。
//  2. 参数 edge case：本 B 决定行为的入参是新数量，逐方向覆盖——增大、减小、
//     多次连续改数量（末次生效）、改回原值；再加多行隔离边界：同一买家同时有
//     A、B 两行，只 PATCH A 行，B 行 quantity 全程不动（防串改/错改行）。
//     边界声明：itemId 不存在或属他人属 spec「操作非本人的行」scenario（B5），
//     未登录 PATCH 属认证域守卫（与 B2 同类），新数量 ≤0 或非整数属 spec
//     「数量非法」scenario（B6），三者均不在本用例覆盖，避免越界重测。
//  3. 外部服务调用参数：改数量链路不触达任何第三方 HTTP 服务（商品读取走进程
//     内 product service，落库走 DB），无可 mock 的外部调用，本层缺省。
//  4. 数据库字段变动：每次 PATCH 后绕过 API 返回值，按 itemId 直查 cart_items
//     断言该行 quantity 为新值且仍恰有一行（改数量不新增、不删行）；末次再叠加
//     按 (user_id, product_id) 的行数与数量之和、全买家行总数断言，隔离场景断言
//     B 行 quantity 未被动。
func TestSpecCartB3_UpdateQuantityTakesEffectAndPersists(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b3", "cart-b3-openid")
	token, user := h.BuyerLogin("code-cart-b3")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}

	productA := h.SeedProduct("Cart B3 Widget", 10, 100, "on_sale")
	productB := h.SeedProduct("Cart B3 Other", 10, 100, "on_sale")
	productAID, err := uid.ParseCanonical(productA)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productBID, err := uid.ParseCanonical(productB)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// 前置：加购两行，A 行 quantity 2（目标行），B 行 quantity 4（隔离对照行）。
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 2}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add A status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemA := readString(t, jsonMap(t, env.Data), "id")

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productB, "quantity": 4}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add B status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemB := readString(t, jsonMap(t, env.Data), "id")

	// 基线 DB 直查：A=2、B=4，各恰一行。
	assertCartQuantityByItemID(t, h.DB, itemA, 2)
	assertCartQuantityByItemID(t, h.DB, itemB, 4)

	// patchAndAssert 对目标行 PATCH 新数量，串起层 1（响应）、层 4（按 itemId
	// 直查 DB）、以及 spec THEN 的「重新拉取列表返回新数量」一致性断言。
	// productID 传入 itemID 所属的商品，列表侧按它定位目标行，不固定到某一商品。
	patchAndAssert := func(itemID, productID string, want int) {
		t.Helper()
		pResp := h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemID,
			map[string]any{"quantity": want}, nil)
		pEnv, _ := h.Decode(pResp)
		h.AssertTraceID(pResp, pEnv)
		if pResp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %s quantity=%d status=%d body=%s, want 200", itemID, want, pResp.StatusCode, pEnv.Message)
		}
		if pEnv.Code != 0 {
			t.Fatalf("PATCH %s quantity=%d business code=%d, want 0", itemID, want, pEnv.Code)
		}
		updated := jsonMap(t, pEnv.Data)
		if got := readString(t, updated, "id"); got != itemID {
			t.Fatalf("PATCH %s response id=%s, want same row %s", itemID, got, itemID)
		}
		if q := updated["quantity"].(float64); q != float64(want) {
			t.Fatalf("PATCH %s response quantity=%v, want %d", itemID, q, want)
		}
		// 层 4：绕过返回值直查 DB——变更已持久，且仍恰有一行。
		assertCartQuantityByItemID(t, h.DB, itemID, int64(want))
		// spec THEN 核心：重新拉取列表，目标行返回新数量。
		lResp := h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
		lEnv, _ := h.Decode(lResp)
		if lResp.StatusCode != http.StatusOK {
			t.Fatalf("re-fetch list status=%d, want 200", lResp.StatusCode)
		}
		assertSingleListRow(t, listData(t, lEnv.Data), productID, float64(want))
	}

	// WHEN 增大方向：2 → 5。
	patchAndAssert(itemA, productA, 5)
	// THEN 隔离：B 行不受本次改动影响。
	assertCartQuantityByItemID(t, h.DB, itemB, 4)

	// WHEN 减小方向：5 → 3。
	patchAndAssert(itemA, productA, 3)
	// WHEN 多次连续改数量：3 → 7 → 1，末次生效。
	patchAndAssert(itemA, productA, 7)
	patchAndAssert(itemA, productA, 1)
	// WHEN 改回原值：1 → 2。
	patchAndAssert(itemA, productA, 2)

	// 最终一致性：A 行=2 且只一行，B 行全程未动=4 且只一行，全买家共 2 行。
	assertCartRow(t, h.DB, userID, productAID, 1, 2)
	assertCartRow(t, h.DB, userID, productBID, 1, 4)
	var totalRows int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ?`, userID).Scan(&totalRows).Error; err != nil {
		t.Fatalf("count buyer cart rows: %v", err)
	}
	if totalRows != 2 {
		t.Fatalf("buyer cart rows=%d, want 2 (one per product)", totalRows)
	}
}

// spec: B4
//
// 删除行：买家对购物车行 DELETE /api/v1/cart/{itemId} 后该行移除，重新拉取
// 列表不再出现，且删除持久在服务端。
//
// 四层断言覆盖说明：
//  1. 端到端响应：加购 A、B 两商品建立两行，对目标行 A 走真实路由
//     DELETE /api/v1/cart/{itemId}，断言 2xx（spec 未钉死删除成功状态码，口径
//     为 2xx）与 envelope 业务码 0、trace_id；随后 GET /api/v1/cart 重新拉取
//     列表——目标商品不再出现，另一行 B 仍在且数量 4 未动（spec THEN「该行
//     移除，不再出现在列表」的列表侧观测；删除成功的响应 data 形态 spec
//     未定义，不断言）。
//  2. 参数 edge case：本 B 决定行为的入参是 itemId，删除成功路径覆盖不同
//     位置与次数——两行中删首行、删到全部行清空（列表为空、0 行可观测）；
//     另覆盖「删除后再加购」边界：被删商品重新加购得到全新独立行（行 id 不
//     等于被删行 id、数量从新值 3 起，证明不是旧行复活、也未在其上累加）。
//     边界声明：itemId 不存在或属他人属 spec「操作非本人的行」scenario（B5），
//     未登录 DELETE 属认证域守卫（与 B2 同类），本用例不覆盖，避免越界重测。
//  3. 外部服务调用参数：删除行链路不触达任何第三方 HTTP 服务（纯 cart_items
//     落库操作），无可 mock 的外部调用，本层缺省。
//  4. 数据库字段变动：删除后绕过 API 返回值直查 cart_items——目标
//     (user_id, product_id) 0 行、被删行主键 id 不再存在、买家行总数与全表
//     行数各减一；再以同 openid 重新登录取新 token 拉列表仍无该行，证明
//     「状态持久在服务端」而非会话内假象。
func TestSpecCartB4_DeleteRowNoLongerAppearsInList(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b4", "cart-b4-openid")
	token, user := h.BuyerLogin("code-cart-b4")
	userIDStr := readString(t, user, "id")
	userID, err := uid.ParseCanonical(userIDStr)
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}

	productA := h.SeedProduct("Cart B4 Widget", 10, 100, "on_sale")
	productB := h.SeedProduct("Cart B4 Other", 10, 100, "on_sale")
	productAID, err := uid.ParseCanonical(productA)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productBID, err := uid.ParseCanonical(productB)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// listAbsentProduct 断言列表中没有任何一行的商品为 productID——spec THEN
	// 「不再出现在列表」的列表侧直接观测。
	listAbsentProduct := func(rows []map[string]any, productID string) {
		t.Helper()
		for _, row := range rows {
			if readString(t, row, "product_id") == productID {
				t.Fatalf("deleted product %s still appears in cart list: %v", productID, row)
			}
		}
	}
	// buyerRowCount / totalRowCount 直查 cart_items 行数，绕开 API 返回值。
	buyerRowCount := func() int64 {
		t.Helper()
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ?`, userID).Scan(&n).Error; err != nil {
			t.Fatalf("count buyer cart rows: %v", err)
		}
		return n
	}
	totalRowCount := func() int64 {
		t.Helper()
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&n).Error; err != nil {
			t.Fatalf("count cart_items total rows: %v", err)
		}
		return n
	}

	// 前置：加购两行，A 行 quantity 2（目标行），B 行 quantity 4（对照行）。
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 2}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add A status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemA := readString(t, jsonMap(t, env.Data), "id")

	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productB, "quantity": 4}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add B status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemB := readString(t, jsonMap(t, env.Data), "id")

	// 基线：列表两行，买家 2 行、全表 2 行（NewHarness 已重置库，表内只有本
	// 用例的行，「全表行数减一」由此获得可对照基线）。
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline list status=%d, want 200", resp.StatusCode)
	}
	rows := listData(t, env.Data)
	assertSingleListRow(t, rows, productA, 2)
	assertSingleListRow(t, rows, productB, 4)
	if n := buyerRowCount(); n != 2 {
		t.Fatalf("baseline buyer cart rows = %d, want 2", n)
	}
	baselineTotal := totalRowCount()
	if baselineTotal != 2 {
		t.Fatalf("baseline cart_items total rows = %d, want 2", baselineTotal)
	}

	// WHEN 删除目标行 A：DELETE /api/v1/cart/{itemId} 走真实路由。
	// THEN 层 1：2xx + 业务码 0 + trace_id。
	resp = h.BuyerDo(token, http.MethodDelete, "/api/v1/cart/"+itemA, nil, nil)
	env, _ = h.Decode(resp)
	h.AssertTraceID(resp, env)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("DELETE %s status = %d body=%s, want 2xx", itemA, resp.StatusCode, env.Message)
	}
	if env.Code != 0 {
		t.Fatalf("DELETE %s business code = %d, want 0", itemA, env.Code)
	}

	// THEN 层 1（列表侧）：目标商品不再出现，另一行仍在且数量未动。
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status=%d, want 200", resp.StatusCode)
	}
	rows = listData(t, env.Data)
	listAbsentProduct(rows, productA)
	assertSingleListRow(t, rows, productB, 4)

	// THEN 层 4：DB 直查——(user_id, product_id) 0 行、被删行 id 不再存在、
	// 另一行未动、买家行与全表行数各减一。
	assertCartRow(t, h.DB, userID, productAID, 0, 0)
	itemAID, err := uid.ParseCanonical(itemA)
	if err != nil {
		t.Fatalf("parse deleted cart item id: %v", err)
	}
	var goneRows int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE id = ?`, itemAID).Scan(&goneRows).Error; err != nil {
		t.Fatalf("count cart_items by deleted id: %v", err)
	}
	if goneRows != 0 {
		t.Fatalf("cart_items rows for deleted id %s = %d, want 0", itemA, goneRows)
	}
	assertCartRow(t, h.DB, userID, productBID, 1, 4)
	if n := buyerRowCount(); n != 1 {
		t.Fatalf("buyer cart rows after delete = %d, want 1", n)
	}
	if n := totalRowCount(); n != baselineTotal-1 {
		t.Fatalf("cart_items total rows after delete = %d, want %d (one less)", n, baselineTotal-1)
	}

	// THEN 持久面（最强证据）：同 openid 重新登录取全新 token，拉列表仍无
	// 被删行——删除持久在服务端，不是会话内假象。
	token2, user2 := h.BuyerLogin("code-cart-b4")
	if got := readString(t, user2, "id"); got != userIDStr {
		t.Fatalf("re-login user id = %s, want same buyer %s", got, userIDStr)
	}
	resp = h.BuyerDo(token2, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list after re-login status=%d, want 200", resp.StatusCode)
	}
	rows = listData(t, env.Data)
	listAbsentProduct(rows, productA)
	assertSingleListRow(t, rows, productB, 4)

	// 层 2 edge：删除后再加购商品 A（quantity=3）——得到全新独立行：行 id
	// 不等于被删的 itemA、数量从 3 起（旧行未复活、未在其上累加）。
	resp = h.BuyerDo(token2, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productA, "quantity": 3}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("re-add A status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	readded := jsonMap(t, env.Data)
	newItemA := readString(t, readded, "id")
	if newItemA == itemA {
		t.Fatalf("re-add after delete reused deleted row id %s, want a fresh independent row", itemA)
	}
	if q := readded["quantity"].(float64); q != 3 {
		t.Fatalf("re-add quantity = %v, want fresh 3 (old row must not resurrect or accumulate)", q)
	}
	assertCartRow(t, h.DB, userID, productAID, 1, 3)
	resp = h.BuyerDo(token2, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	rows = listData(t, env.Data)
	assertSingleListRow(t, rows, productA, 3)
	assertSingleListRow(t, rows, productB, 4)

	// 层 2 edge：删除全部行（B 行与重建的 A 行）——列表为空，买家行清零。
	for _, id := range []string{itemB, newItemA} {
		dResp := h.BuyerDo(token2, http.MethodDelete, "/api/v1/cart/"+id, nil, nil)
		dEnv, _ := h.Decode(dResp)
		if dResp.StatusCode < http.StatusOK || dResp.StatusCode >= http.StatusMultipleChoices {
			t.Fatalf("DELETE %s status = %d body=%s, want 2xx", id, dResp.StatusCode, dEnv.Message)
		}
		if dEnv.Code != 0 {
			t.Fatalf("DELETE %s business code = %d, want 0", id, dEnv.Code)
		}
	}
	resp = h.BuyerDo(token2, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list after deleting all status=%d, want 200", resp.StatusCode)
	}
	if emptyRows := listData(t, env.Data); len(emptyRows) != 0 {
		t.Fatalf("cart list after deleting all rows = %d, want 0 (empty list)", len(emptyRows))
	}
	if n := buyerRowCount(); n != 0 {
		t.Fatalf("buyer cart rows after deleting all = %d, want 0", n)
	}
	// 本用例共 2 删 1 增 2 删，净减 2：全表行数回到 0。
	if n := totalRowCount(); n != baselineTotal-2 {
		t.Fatalf("cart_items total rows after deleting all = %d, want %d", n, baselineTotal-2)
	}
}

// spec: B5
//
// 操作非本人的行：itemId 不属于当前买家时，PATCH（改数量）与 DELETE（删除）
// 一律响应 404（不暴露资源是否存在），他人购物车不变。
//
// 四层断言覆盖说明：
//  1. 端到端响应：买家 B 用 A 真实存在的 itemId（A 的全部两行，逐行）走真实
//     路由打 PATCH /api/v1/cart/{itemId}（改数量）与 DELETE /api/v1/cart/
//     {itemId}（删除行）——spec「操作」两类操作都覆盖——断言状态码 404 且
//     envelope 业务码非 0（项目 envelope 约定：code=0 为成功，拒绝必须非 0）。
//     正对照先行：B 成功 PATCH 自己的一行（200/0）——同一端点对本人可用，
//     越权尝试的 404 原因只能落在行归属上，防「端点整体坏掉一律 404」假绿。
//     「不暴露资源是否存在」：B 再打一个不存在（max(id)+1000、两人皆不属于）
//     的 itemId，断同一操作下状态码同为 404，且 envelope 的 code、message 与
//     data（原始 JSON 载荷）与「存在但属他人」的响应完全一致——data 也纳入
//     不可区分面，任一侧 data 若回显行信息同样是存在性泄露（2026-09-02 B5
//     review Warning 加固）；「无法区分存在性」是结构性断言，不钉死具体文案
//     （spec 未定义 message 措辞，不断定死文案）。实际观测到的码值/message/
//     data 以 t.Logf 如实记录。
//  2. 参数 edge case：本 B 决定行为的入参是 itemId，取值逐条覆盖——「存在但
//     属他人」（扩展到 A 的全部行，防个别行归属判断漏网）与「不存在」两类，
//     并叠加操作维度 PATCH/DELETE × 两类 itemId 共 4 组组合。边界声明：
//     未登录 401 属认证域守卫（B2 口径），不属本 B；quantity ≤0 / 非整数属
//     spec「数量非法」scenario（B6），越权请求体固定用合法值 99 以隔离归属
//     变量；itemId 非数字等非法格式 spec 无对应 scenario，本用例不覆盖。
//     变体声明：internal/cart/register.go 路由注册只有 GET /cart（列表）、
//     POST /cart、PATCH /cart/:itemId、DELETE /cart/:itemId，不存在
//     /api/v1/cart/{itemId} 的单行 GET 端点，该变体无对象可测，不覆盖。
//  3. 外部服务调用参数：行归属校验与拒绝路径不触达任何第三方 HTTP 服务
//     （判定在 cart service 进程内 + DB 查询完成），无可 mock 的外部调用，
//     本层缺省。
//  4. 数据库字段变动：每次拒绝后绕过 API 返回值直查 cart_items——A 的行仍
//     恰有一行（主键直查）且 quantity 等于基线、A 买家行数恒为 2、B 自身行
//     未被动、全表行数与基线一致（「他人购物车不变」的正向证据，兼防拒绝
//     伴随旁路写入）；收尾以 A、B 各自 token 拉列表侧观测：A 两行完整数量
//     如基线，B 列表仍只自己的那一行。
func TestSpecCartB5_OperateForeignCartItemRejected404ForeignCartUnchanged(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b5-owner", "cart-b5-owner-openid")
	h.RegisterBuyer("code-cart-b5-attacker", "cart-b5-attacker-openid")
	tokenA, userA := h.BuyerLogin("code-cart-b5-owner")
	tokenB, userB := h.BuyerLogin("code-cart-b5-attacker")
	ownerID, err := uid.ParseCanonical(readString(t, userA, "id"))
	if err != nil {
		t.Fatalf("parse owner id: %v", err)
	}
	attackerID, err := uid.ParseCanonical(readString(t, userB, "id"))
	if err != nil {
		t.Fatalf("parse attacker id: %v", err)
	}
	if ownerID == attackerID {
		t.Fatalf("owner and attacker resolved to same user %d, premise of cross-buyer test broken", ownerID)
	}

	productP := h.SeedProduct("Cart B5 Owner P", 10, 100, "on_sale")
	productQ := h.SeedProduct("Cart B5 Owner Q", 10, 100, "on_sale")
	productR := h.SeedProduct("Cart B5 Attacker R", 10, 100, "on_sale")

	// addRow 前置 helper：加购一行并返回行 id。
	addRow := func(token, productID string, quantity int) string {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
			map[string]any{"product_id": productID, "quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusOK || env.Code != 0 {
			t.Fatalf("seed add product %s status=%d code=%d body=%s, want 200/0",
				productID, resp.StatusCode, env.Code, env.Message)
		}
		return readString(t, jsonMap(t, env.Data), "id")
	}

	itemA1 := addRow(tokenA, productP, 2) // 受害者行 1，quantity 基线 2
	itemA2 := addRow(tokenA, productQ, 5) // 受害者行 2，quantity 基线 5
	itemB1 := addRow(tokenB, productR, 3) // 攻击者本人行，正对照对象

	// 层 1 正对照：B 成功 PATCH 自己的行（3→4）——同一端点对本人可用；
	// 同时按 A 的行直查，证明归属过滤精准（本人写不波及他人行）。
	resp := h.BuyerDo(tokenB, http.MethodPatch, "/api/v1/cart/"+itemB1,
		map[string]any{"quantity": 4}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("owner-self control PATCH status=%d code=%d body=%s, want 200/0",
			resp.StatusCode, env.Code, env.Message)
	}
	assertCartQuantityByItemID(t, h.DB, itemB1, 4)
	assertCartQuantityByItemID(t, h.DB, itemA1, 2)
	assertCartQuantityByItemID(t, h.DB, itemA2, 5)

	var baselineTotal int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&baselineTotal).Error; err != nil {
		t.Fatalf("count cart_items baseline: %v", err)
	}
	if baselineTotal != 3 {
		t.Fatalf("cart_items baseline rows = %d, want 3 (owner 2 + attacker 1)", baselineTotal)
	}

	// 层 4 恒等式：任何越权拒绝之后，受害者两行按主键直查仍各一行且 quantity
	// 如基线，受害者行数=2、攻击者行数=1（自身正对照行未被旁动）、全表行数
	// 如基线——「他人购物车不变」的正向证据，兼防拒绝伴随旁路插入/删除。
	allRowsIntact := func() {
		t.Helper()
		assertCartQuantityByItemID(t, h.DB, itemA1, 2)
		assertCartQuantityByItemID(t, h.DB, itemA2, 5)
		assertCartQuantityByItemID(t, h.DB, itemB1, 4)
		var ownerRows, attackerRows, total int64
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ?`, ownerID).Scan(&ownerRows).Error; err != nil {
			t.Fatalf("count owner cart rows: %v", err)
		}
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE user_id = ?`, attackerID).Scan(&attackerRows).Error; err != nil {
			t.Fatalf("count attacker cart rows: %v", err)
		}
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&total).Error; err != nil {
			t.Fatalf("count cart_items total: %v", err)
		}
		if ownerRows != 2 || attackerRows != 1 || total != baselineTotal {
			t.Fatalf("row counts after rejection: owner=%d(want 2) attacker=%d(want 1) total=%d(want %d)",
				ownerRows, attackerRows, total, baselineTotal)
		}
	}

	// ownerListIntact 受害者视角列表侧观测：两行完整、数量如基线、无多余行。
	ownerListIntact := func() {
		t.Helper()
		lResp := h.BuyerDo(tokenA, http.MethodGet, "/api/v1/cart", nil, nil)
		lEnv, _ := h.Decode(lResp)
		if lResp.StatusCode != http.StatusOK {
			t.Fatalf("owner list status = %d, want 200", lResp.StatusCode)
		}
		rows := listData(t, lEnv.Data)
		assertSingleListRow(t, rows, productP, 2)
		assertSingleListRow(t, rows, productQ, 5)
		if len(rows) != 2 {
			t.Fatalf("owner cart list rows = %d, want 2", len(rows))
		}
	}

	// 「不存在」的 itemId：随机 uuid，任何买家都不拥有——与「存在但属
	// 他人」对照，观测「不暴露资源是否存在」。
	ghostID := uid.New()
	var ghostRows int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE id = ?`, ghostID).Scan(&ghostRows).Error; err != nil {
		t.Fatalf("verify non-existent id %d: %v", ghostID, err)
	}
	if ghostRows != 0 {
		t.Fatalf("ghost id %d unexpectedly exists in cart_items", ghostID)
	}
	ghost := ghostID.String()

	// attempt：攻击者 B 以 method 打目标行，断层 1（404 + 业务码非 0），
	// 如实记录实际观测到的响应形态，返回 envelope 供同构性对照。
	// PATCH 请求体用合法数量 99——非法数量属 B6，此处隔离出归属这一个变量。
	attempt := func(method, target string) envelope {
		t.Helper()
		var body any
		if method == http.MethodPatch {
			body = map[string]any{"quantity": 99}
		}
		aResp := h.BuyerDo(tokenB, method, "/api/v1/cart/"+target, body, nil)
		aEnv, _ := h.Decode(aResp)
		if aResp.StatusCode != http.StatusNotFound {
			t.Fatalf("attacker %s /api/v1/cart/%s: status = %d body=%s, want 404",
				method, target, aResp.StatusCode, aEnv.Message)
		}
		if aEnv.Code == 0 {
			t.Fatalf("attacker %s /api/v1/cart/%s: rejection envelope business code = 0 (success code), want non-zero",
				method, target)
		}
		t.Logf("observed %s /api/v1/cart/%s -> status=404 code=%d message=%q data=%s",
			method, target, aEnv.Code, aEnv.Message, string(aEnv.Data))
		return aEnv
	}

	// WHEN/THEN 层 1+4+列表侧：攻击者对受害者的每一个真实行，PATCH 与 DELETE
	// 逐一被 404 拒绝，每次拒绝后他人购物车不变。
	foreignPatch := attempt(http.MethodPatch, itemA1)
	allRowsIntact()
	ownerListIntact()
	foreignDelete := attempt(http.MethodDelete, itemA1)
	allRowsIntact()
	attempt(http.MethodPatch, itemA2)
	allRowsIntact()
	attempt(http.MethodDelete, itemA2)
	allRowsIntact()
	ownerListIntact()

	// WHEN/THEN「不暴露资源是否存在」：同一个攻击者打不存在的 itemId，得到
	// 与打他人真实行同码同构的响应。
	ghostPatch := attempt(http.MethodPatch, ghost)
	ghostDelete := attempt(http.MethodDelete, ghost)
	allRowsIntact()

	// data 也在不可区分面内：两侧 envelope.Data 原始 JSON 必须一致。
	if ghostPatch.Code != foreignPatch.Code || ghostPatch.Message != foreignPatch.Message ||
		string(ghostPatch.Data) != string(foreignPatch.Data) {
		t.Fatalf("PATCH must not disclose resource existence: foreign-existing (code=%d message=%q data=%s) differs from non-existent (code=%d message=%q data=%s)",
			foreignPatch.Code, foreignPatch.Message, string(foreignPatch.Data),
			ghostPatch.Code, ghostPatch.Message, string(ghostPatch.Data))
	}
	if ghostDelete.Code != foreignDelete.Code || ghostDelete.Message != foreignDelete.Message ||
		string(ghostDelete.Data) != string(foreignDelete.Data) {
		t.Fatalf("DELETE must not disclose resource existence: foreign-existing (code=%d message=%q data=%s) differs from non-existent (code=%d message=%q data=%s)",
			foreignDelete.Code, foreignDelete.Message, string(foreignDelete.Data),
			ghostDelete.Code, ghostDelete.Message, string(ghostDelete.Data))
	}

	// 收尾列表侧观测：受害者列表两行完整如基线；攻击者列表仍只有自己那行
	// （正对照行 quantity 4），全程未被越权尝试污染。
	ownerListIntact()
	lResp := h.BuyerDo(tokenB, http.MethodGet, "/api/v1/cart", nil, nil)
	lEnv, _ := h.Decode(lResp)
	if lResp.StatusCode != http.StatusOK {
		t.Fatalf("attacker list status = %d, want 200", lResp.StatusCode)
	}
	bRows := listData(t, lEnv.Data)
	assertSingleListRow(t, bRows, productR, 4)
	if len(bRows) != 1 {
		t.Fatalf("attacker cart list rows = %d, want 1", len(bRows))
	}
}

// spec: B6
//
// 数量非法：已登录买家在加购（POST /api/v1/cart）与改数量
// （PATCH /api/v1/cart/{itemId}）两个入口提交数量 ≤0 或非整数时，返回参数
// 错误且行不变。
//
// 四层断言覆盖说明：
//  1. 端到端响应：两类入口的每种非法数量提交都走真实路由打到 API；spec 未
//     钉死「参数错误」的状态码与文案（B5 文案口径同此），故断状态码落在 4xx
//     客户端错误区间、envelope 业务码非 0（项目 envelope 约定：code=0 为成
//     功，拒绝必须非 0）；实际码值与 message 形态（观测为 400/code=1001，
//     溢出对照为 422/code=2002 等）以 t.Logf 逐条如实记录，供 contract 附证据。
//  2. 参数 edge case：本 B 决定行为的入参是 quantity，非法形态逐条覆盖——
//     0（合法最小值 1 的 -1 边界）、负数 -1、非数值字符串 "abc"、小数 1.5
//     （非整数）、显式 null、字段缺失，共 6 形态；POST 侧对「已有行的商品」
//     与「无行的商品」两个目标各打一遍全表，PATCH 侧打在「已存在的本人行」
//     上。POST/PATCH 非法循环后各置正对照（同一商品/同一行换合法数量 →
//     200/code=0 且生效），证明 4xx 是选择性拒绝、不是端点整体坏掉。边界声
//     明：itemId 属他人/不存在属 B5，未登录属 B2，不越界重测；quantity 超
//     int32 / 超 int64 / 累加越界属 spec 未定义上限——不 invent 上限规则、
//     不断言接受或拒绝，只观测记录并断恒成立的不变式（被拒 4xx 后行必不
//     动、绝不出现 ≤0 的行、绝不打 5xx）。
//  3. 外部服务调用参数：数量校验与拒绝路径不触达任何第三方 HTTP 服务（判定
//     在 handler 绑定校验 + cart service + DB 进程内完成），无可 mock 的外
//     部调用，本层缺省。
//  4. 数据库字段变动：每次拒绝后绕过 API 返回值直查 cart_items——PATCH 侧
//     断目标行 quantity 仍为基线值；POST 侧断目标商品行数与数量之和仍为原
//     状（无行商品不出现 ≤0/负数幽灵行、已有行不被污染）；非法循环内每次
//     拒绝后全表行数与基线一致；收尾全表扫描 quantity <= 0 计 0 行，并以列
//     表侧观测复核（幽灵商品不在列表、列表无任何非正数量行）。
func TestSpecCartB6_InvalidQuantityRejectedWith4xxAndCartRowUnchanged(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b6", "cart-b6-openid")
	token, user := h.BuyerLogin("code-cart-b6")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}

	// productP：PATCH 目标与 POST 既有行目标（全程 quantity 有确定基线）。
	// productG：幽灵检查目标——非法 POST 全程必须保持 0 行。
	// productC：POST 正对照商品。
	productP := h.SeedProduct("Cart B6 Patch Target", 10, 100, "on_sale")
	productG := h.SeedProduct("Cart B6 Ghost Target", 10, 100, "on_sale")
	productC := h.SeedProduct("Cart B6 Post Control", 10, 100, "on_sale")
	productO := h.SeedProduct("Cart B6 Overflow Probe", 10, 100, "on_sale")
	productY := h.SeedProduct("Cart B6 Big Patch Probe", 10, 100, "on_sale")
	productPID, err := uid.ParseCanonical(productP)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productGID, err := uid.ParseCanonical(productG)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productCID, err := uid.ParseCanonical(productC)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// int32Max 仅作超大值探针取值与断言算术用，不是 spec 定义的上限。
	const int32Max = 2147483647

	// omitQuantity 标记「缺 quantity 字段」形态，区别于显式 null。
	type omitQuantity struct{}
	omitQty := omitQuantity{}

	// 非法数量取值表：spec「数量小于等于 0 或非整数」逐形态展开。
	badQuantities := []struct {
		name  string
		value any
	}{
		{"数量 0（合法最小值 1 减一）", 0},
		{"数量 -1（负数）", -1},
		{"数量 \"abc\"（非数值字符串）", "abc"},
		{"数量 1.5（小数，非整数）", 1.5},
		{"数量 null", nil},
		{"缺 quantity 字段", omitQty},
	}
	addBody := func(productID string, q any) map[string]any {
		if _, isOmit := q.(omitQuantity); isOmit {
			return map[string]any{"product_id": productID}
		}
		return map[string]any{"product_id": productID, "quantity": q}
	}
	patchBody := func(q any) map[string]any {
		if _, isOmit := q.(omitQuantity); isOmit {
			return map[string]any{}
		}
		return map[string]any{"quantity": q}
	}

	// assertRejected 层 1：参数错误 = 4xx + 业务码非 0；实际码值/message/
	// data 先如实记录再断言，失败输出里也留得下观测形态。
	assertRejected := func(op string, resp *http.Response, env envelope) {
		t.Helper()
		t.Logf("observed %s -> status=%d code=%d message=%q data=%s",
			op, resp.StatusCode, env.Code, env.Message, string(env.Data))
		if resp.StatusCode < http.StatusBadRequest || resp.StatusCode >= http.StatusInternalServerError {
			t.Fatalf("%s: status = %d (code=%d message=%q), want 4xx parameter error",
				op, resp.StatusCode, env.Code, env.Message)
		}
		if env.Code == 0 {
			t.Fatalf("%s: rejection envelope business code = 0 (success code), want non-zero", op)
		}
	}
	totalRows := func() int64 {
		t.Helper()
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM cart_items`).Scan(&n).Error; err != nil {
			t.Fatalf("count cart_items total rows: %v", err)
		}
		return n
	}
	// addLegal / patchLegal：合法提交 helper，断 200/code=0，返回行 id。
	addLegal := func(productID string, quantity any) string {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
			map[string]any{"product_id": productID, "quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusOK || env.Code != 0 {
			t.Fatalf("legal add product %s quantity=%v status=%d code=%d body=%s, want 200/0",
				productID, quantity, resp.StatusCode, env.Code, env.Message)
		}
		return readString(t, jsonMap(t, env.Data), "id")
	}
	patchLegal := func(itemID string, quantity any) {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemID,
			map[string]any{"quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusOK || env.Code != 0 {
			t.Fatalf("legal PATCH %s quantity=%v status=%d code=%d body=%s, want 200/0",
				itemID, quantity, resp.StatusCode, env.Code, env.Message)
		}
	}

	// 前置基线：P 行合法加购 2 件（兼 POST 侧首个正对照），全表基线 1 行。
	itemP := addLegal(productP, 2)
	assertCartRow(t, h.DB, userID, productPID, 1, 2)
	wantTotal := totalRows()
	if wantTotal != 1 {
		t.Fatalf("baseline cart_items total rows = %d, want 1", wantTotal)
	}

	// ---- 层 1+2+4：POST 非法数量 × 6 形态 × 两目标（既有行商品、无行商品）----
	for _, bad := range badQuantities {
		// 目标 1：已有行的商品——拒绝且既有行分毫不动。
		resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart", addBody(productP, bad.value), nil)
		env, _ := h.Decode(resp)
		assertRejected("POST 已有行商品 "+bad.name, resp, env)
		assertCartRow(t, h.DB, userID, productPID, 1, 2)
		assertCartRow(t, h.DB, userID, productGID, 0, 0)
		if n := totalRows(); n != wantTotal {
			t.Fatalf("%s: cart_items total rows = %d, want baseline %d", bad.name, n, wantTotal)
		}
		// 目标 2：无行的商品——拒绝且不得长出幽灵行。
		resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart", addBody(productG, bad.value), nil)
		env, _ = h.Decode(resp)
		assertRejected("POST 无行商品 "+bad.name, resp, env)
		assertCartRow(t, h.DB, userID, productGID, 0, 0)
		assertCartRow(t, h.DB, userID, productPID, 1, 2)
		if n := totalRows(); n != wantTotal {
			t.Fatalf("%s: cart_items total rows = %d, want baseline %d", bad.name, n, wantTotal)
		}
	}

	// ---- 正对照（POST 侧）：刚被 6 形态全拒的商品 G，换合法数量 3 → 生效----
	// 证明上面的 4xx 是对非法数量的选择性拒绝，不是加购端点整体坏掉。
	addLegal(productG, 3)
	assertCartRow(t, h.DB, userID, productGID, 1, 3)
	wantTotal = totalRows()
	if wantTotal != 2 {
		t.Fatalf("after control add total rows = %d, want 2", wantTotal)
	}

	// ---- 层 1+2+4：PATCH 非法数量 × 6 形态，打在已存在的本人行上 ----
	// 先正对照建立 9 的基线：PATCH 端点对本人行可用，非法值的 4xx 才有对照。
	patchLegal(itemP, 9)
	assertCartQuantityByItemID(t, h.DB, itemP, 9)
	for _, bad := range badQuantities {
		resp := h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemP, patchBody(bad.value), nil)
		env, _ := h.Decode(resp)
		assertRejected("PATCH 本人行 "+bad.name, resp, env)
		// THEN 行不变：目标行 quantity 仍为基线 9，G/C 旁路不伤，全表行数如基线。
		assertCartQuantityByItemID(t, h.DB, itemP, 9)
		assertCartRow(t, h.DB, userID, productGID, 1, 3)
		if n := totalRows(); n != wantTotal {
			t.Fatalf("%s: cart_items total rows = %d, want baseline %d", bad.name, n, wantTotal)
		}
	}
	// ---- 正对照（PATCH 侧，置于非法循环之后）：同一行换合法最小值 1 → 生效----
	// 非法提交后行仍可正常改数量，拒绝不留污染。
	patchLegal(itemP, 1)
	assertCartQuantityByItemID(t, h.DB, itemP, 1)

	// C 行留作列表复核对照：合法加购数量 5。
	addLegal(productC, 5)
	assertCartRow(t, h.DB, userID, productCID, 1, 5)

	// ---- 层 2 边界（如实观测，不作接受/拒绝断言）：超大数量取值 ----
	// spec 未定义 quantity 上限，B6 的「参数错误」只钉 ≤0 与非整数两族。超大
	// 值（int32 内最大、超 int32、超 int64）逐条打真实路由，只记录形态并断
	// 恒成立不变式：绝不 5xx；被 4xx 拒绝后行必不动（spec THEN「行不变」对
	// 任何拒绝同样成立）；落库行 quantity 必为正（不 invent 上限、不放 ≤0 行）。
	observeOversizedAdd := func(label, productID string, quantity any) {
		t.Helper()
		pid, err := uid.ParseCanonical(productID)
		if err != nil {
			t.Fatalf("parse product id: %v", err)
		}
		resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
			map[string]any{"product_id": productID, "quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		t.Logf("oversized add probe %s -> status=%d code=%d message=%q (spec 未钉上限，如实观测，不断言接受或拒绝)",
			label, resp.StatusCode, env.Code, env.Message)
		if resp.StatusCode >= http.StatusInternalServerError {
			t.Fatalf("oversized add %s: status=%d — 超大值不得触发 5xx", label, resp.StatusCode)
		}
		var rowCount, sum int64
		if err := h.DB.Raw(`SELECT count(*), COALESCE(sum(quantity), 0) FROM cart_items WHERE user_id = ? AND product_id = ?`,
			userID, pid).Row().Scan(&rowCount, &sum); err != nil {
			t.Fatalf("query probe product %s rows: %v", productID, err)
		}
		if rowCount > 1 {
			t.Fatalf("oversized add %s created %d rows for one product, want ≤1", label, rowCount)
		}
		if rowCount > 0 && sum < 1 {
			t.Fatalf("oversized add %s wrote non-positive quantity %d", label, sum)
		}
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError && rowCount != 0 {
			t.Fatalf("oversized add %s rejected (status=%d) but still created a row — violates 行不变", label, resp.StatusCode)
		}
	}
	for i, probe := range []struct {
		name  string
		value any
	}{
		{"quantity=2147483647（int32 最大）", int32Max},
		{"quantity=3000000000（超 int32）", json.Number("3000000000")},
		{"quantity=99999999999999999999（超 int64）", json.Number("99999999999999999999")},
	} {
		probeProduct := h.SeedProduct("Cart B6 Big Add Probe "+strconv.Itoa(i+1), 10, 100, "on_sale")
		observeOversizedAdd(probe.name, probeProduct, probe.value)
	}

	observeOversizedPatch := func(label, itemID string, quantity any) {
		t.Helper()
		before := cartQuantityByItemID(t, h.DB, itemID)
		resp := h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemID,
			map[string]any{"quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		t.Logf("oversized PATCH probe %s -> status=%d code=%d message=%q (spec 未钉上限，如实观测，不断言接受或拒绝)",
			label, resp.StatusCode, env.Code, env.Message)
		if resp.StatusCode >= http.StatusInternalServerError {
			t.Fatalf("oversized PATCH %s: status=%d — 超大值不得触发 5xx", label, resp.StatusCode)
		}
		after := cartQuantityByItemID(t, h.DB, itemID)
		if after < 1 {
			t.Fatalf("oversized PATCH %s left non-positive quantity %d on row %s", label, after, itemID)
		}
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError && after != before {
			t.Fatalf("oversized PATCH %s rejected (status=%d) but moved row quantity %d -> %d — violates 行不变",
				label, resp.StatusCode, before, after)
		}
	}
	itemY := addLegal(productY, 2)
	for _, probe := range []struct {
		name  string
		value any
	}{
		{"quantity=2147483647（int32 最大）", int32Max},
		{"quantity=3000000000（超 int32）", json.Number("3000000000")},
		{"quantity=99999999999999999999（超 int64）", json.Number("99999999999999999999")},
	} {
		observeOversizedPatch(probe.name, itemY, probe.value)
	}
	// 超大值探针后同一行仍可正常 PATCH——拒绝选择性收尾。
	patchLegal(itemY, 7)
	assertCartQuantityByItemID(t, h.DB, itemY, 7)

	// 累加越界探针：O 行 quantity 1，再 POST int32 最大——合并将超 int32。
	// spec 未钉上限：接受则按 B1 合并规则落到确定和值，拒绝则行不动；两种
	// 结局都放行，只有 5xx、≤0 行、拒绝却动行是失败。
	itemO := addLegal(productO, 1)
	beforeO := cartQuantityByItemID(t, h.DB, itemO)
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productO, "quantity": int32Max}, nil)
	env, _ := h.Decode(resp)
	t.Logf("observed accumulation overflow probe (existing %d + %d) -> status=%d code=%d message=%q (spec 未钉上限，如实观测)",
		beforeO, int32Max, resp.StatusCode, env.Code, env.Message)
	afterO := cartQuantityByItemID(t, h.DB, itemO)
	switch {
	case resp.StatusCode >= http.StatusInternalServerError:
		t.Fatalf("accumulation probe: status=%d — 超大合并不得触发 5xx", resp.StatusCode)
	case resp.StatusCode >= http.StatusBadRequest && afterO != beforeO:
		t.Fatalf("accumulation probe rejected (status=%d) but row quantity moved %d -> %d — violates 行不变",
			resp.StatusCode, beforeO, afterO)
	case resp.StatusCode == http.StatusOK && afterO != beforeO+int32Max:
		t.Fatalf("accumulation probe accepted but row quantity %d != %d+%d (B1 合并规则)", afterO, beforeO, int32Max)
	}
	// 探针后该行仍可正常累加（合法值 2）。
	baseO := cartQuantityByItemID(t, h.DB, itemO)
	addLegal(productO, 2)
	assertCartQuantityByItemID(t, h.DB, itemO, baseO+2)

	// ---- 层 4 收尾全表扫描：全程没有任何 ≤0 的行落库 ----
	var nonPositive int64
	if err := h.DB.Raw(`SELECT count(*) FROM cart_items WHERE quantity <= 0`).Scan(&nonPositive).Error; err != nil {
		t.Fatalf("sweep non-positive quantity rows: %v", err)
	}
	if nonPositive != 0 {
		t.Fatalf("cart_items contains %d rows with quantity <= 0 after all invalid submissions, want 0", nonPositive)
	}

	// ---- 列表侧复核：确定基线行原样，幽灵对照商品仍缺席，列表无非正数量行 ----
	resp = h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	rows := listData(t, env.Data)
	// 幽灵对照商品 G：非法 POST 全程未让它长出幽灵行（循环内逐次 DB 直查
	// 0 行），列表里它只该有正对照那唯一一行、数量 3。
	assertSingleListRow(t, rows, productP, 1)
	assertSingleListRow(t, rows, productC, 5)
	assertSingleListRow(t, rows, productG, 3)
	for _, row := range rows {
		if q, ok := row["quantity"].(float64); !ok || q < 1 {
			t.Fatalf("cart list row carries non-positive or malformed quantity: %v", row)
		}
	}
}

// spec: B7
//
// 管理员下架后：购物车内某商品被管理员下架，买家再拉取列表，该行仍在列表中
// （行数不减、行 id 与 quantity 不变）且被标记为不可购；其他在售行不被标记。
// Requirement 另写明「商品下架不删除购物车记录」——层 4 DB 直查 cart_items
// 证该行仍在库。
//
// 「不可购」标记的实际字段形态（读实现确认，非发明）：列表行嵌套 product
// 对象的 status 字符串字段，取值 "on_sale"/"off_sale"
// （internal/cart/cart_handler.go cartItemJSON.Product →
// cartProductJSON.Status，行 203-219、234-244；
// internal/platform/database/models.go 行 66-69）。
// internal/cart/service.go 行 32-33 注释明确
// "Off-sale products stay in the cart; they are reported non-purchasable
// through their product status"——实现没有独立的 purchasable 布尔字段，
// 断言按 product.status 的实际形态来。
//
// 四层断言覆盖说明：
//  1. 端到端响应：下架走真实管理端路由 PATCH /api/admin/v1/products/
//     {productId}（权限码 product:write，internal/product/register.go 行
//     60-62，SuperAdmin 会话），断 200/code=0/trace_id 且响应体
//     status=off_sale；买家 GET /api/v1/cart 断 200、列表恰 2 行、目标行
//     id 与 quantity=3 不变、product.status=off_sale，对照行 product.status
//     仍 on_sale。
//  2. 参数 edge case：本 B 决定行为的变量是商品状态与拉取时机，逐一覆盖——
//     下架后连续再拉取 2 次标记稳定且不误删行；下架后对**其他在售行**正常
//     PATCH 数量（4→6）不受影响（隔离），且目标行不被旁改；对下架目标行
//     自身 PATCH 数量（同值 3 提交，成功/拒绝后行值都仍是 3）spec scenario
//     与 Coverage Gaps 均未定义其允许性——不 invent 接受或拒绝，只观测并断
//     恒成立的不变式（不得 5xx、行 quantity 恒为 3、标记侧仍 off_sale）。
//     重新上架 spec 未写 scenario：管理端 PATCH 成功只作为观测前提断言，
//     购物车侧标记变化只 t.Logf 观测、不断言。边界声明：直接加购已下架商品
//     属 FU-e59a3c18（spec Coverage Gaps），本用例不覆盖；库存不足标记属
//     B8；未登录/越权/非法数量属 B2/B5/B6，不越界重测。
//  3. 外部服务调用参数：下架与拉取列表链路不触达任何第三方 HTTP 服务（商品
//     状态由 cart 列表组装时进程内读 product service，下架写入走
//     DB+审计），无可 mock 的外部调用，本层缺省。
//  4. 数据库字段变动：下架后直查 products.status='off_sale'（下架真实落库、
//     商品行未被删除）；直查 cart_items 断目标 (user_id, product_id) 恰一行
//     且 quantity 和为 3、按主键 id 直查该行仍在（「不删除购物车记录」的
//     DB 侧证据，不只信 API 返回值），对照行一行 quantity 4；多拉取与每次
//     PATCH 观测后再按主键直查复核行存在与数量。
func TestSpecCartB7_AdminOffSaleKeepsCartRowMarkedNonPurchasable(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b7", "cart-b7-openid")
	token, user := h.BuyerLogin("code-cart-b7")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}

	// productX：下架目标行商品；productY：全程在售对照行商品（隔离面）。
	productX := h.SeedProduct("Cart B7 Off Target", 10, 100, "on_sale")
	productY := h.SeedProduct("Cart B7 On Control", 10, 100, "on_sale")
	productXID, err := uid.ParseCanonical(productX)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}
	productYID, err := uid.ParseCanonical(productY)
	if err != nil {
		t.Fatalf("parse product id: %v", err)
	}

	// listRows 拉买家列表：断 200/code=0/trace_id、行数不减（恒 2 行——
	// 「下架不删除购物车记录」的列表侧观测），返回行数组。
	listRows := func() []map[string]any {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
		env, _ := h.Decode(resp)
		h.AssertTraceID(resp, env)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/cart status = %d body=%s, want 200", resp.StatusCode, env.Message)
		}
		if env.Code != 0 {
			t.Fatalf("GET /api/v1/cart business code = %d, want 0", env.Code)
		}
		rows := listData(t, env.Data)
		if len(rows) != 2 {
			t.Fatalf("cart list rows = %d, want 2 (admin off-sale must not drop cart rows)", len(rows))
		}
		return rows
	}
	// rowOf 按 product_id 定位列表行：下架后该行必须恰有一行。
	rowOf := func(rows []map[string]any, productID string) map[string]any {
		t.Helper()
		var found map[string]any
		matched := 0
		for _, row := range rows {
			if readString(t, row, "product_id") == productID {
				found = row
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("cart rows for product %s = %d, want exactly 1 (row must survive admin off-sale)", productID, matched)
		}
		return found
	}
	// productStatusOf 读行的不可购标记实际形态：嵌套 product.status。
	productStatusOf := func(row map[string]any) string {
		t.Helper()
		nested, ok := row["product"].(map[string]any)
		if !ok {
			t.Fatalf("cart row carries no nested product object (non-purchasable marker source missing): %v", row)
		}
		return readString(t, nested, "status")
	}
	// assertRow 断一行的行 id（同一行、未被重建）、quantity（未被动）、
	// product.status（不可购标记）。
	assertRow := func(row map[string]any, wantItemID string, wantQuantity float64, wantStatus string) {
		t.Helper()
		if got := readString(t, row, "id"); got != wantItemID {
			t.Fatalf("cart row id = %s, want same row %s (off-sale must not recreate the row)", got, wantItemID)
		}
		if q, ok := row["quantity"].(float64); !ok || q != wantQuantity {
			t.Fatalf("cart row %s quantity = %v, want %v", wantItemID, row["quantity"], wantQuantity)
		}
		if got := productStatusOf(row); got != wantStatus {
			t.Fatalf("cart row %s product.status = %s, want %s", wantItemID, got, wantStatus)
		}
	}
	// patchProductStatus 走真实管理端路由改商品状态，断端点本身成功
	// （200/code=0、响应 id 与 status 一致）——这是本 B 的 WHEN 动作。
	patchProductStatus := func(productID, wantStatus string) {
		t.Helper()
		resp := h.AdminDo(h.SuperAdmin, http.MethodPatch, "/api/admin/v1/products/"+productID,
			map[string]any{"status": wantStatus}, nil)
		env, _ := h.Decode(resp)
		h.AssertTraceID(resp, env)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("admin PATCH product %s status=%q -> %d body=%s, want 200",
				productID, wantStatus, resp.StatusCode, env.Message)
		}
		if env.Code != 0 {
			t.Fatalf("admin PATCH product %s business code = %d, want 0", productID, env.Code)
		}
		updated := jsonMap(t, env.Data)
		if got := readString(t, updated, "id"); got != productID {
			t.Fatalf("admin PATCH response id = %s, want %s", got, productID)
		}
		if got := readString(t, updated, "status"); got != wantStatus {
			t.Fatalf("admin PATCH response status = %s, want %s", got, wantStatus)
		}
	}
	// productStatusInDB 直查 products 表读 status——下架真实落库、商品行仍在
	// （查得到行本身即「未删商品」的证据）。
	productStatusInDB := func(productID string) string {
		t.Helper()
		id, err := uid.ParseCanonical(productID)
		if err != nil {
			t.Fatalf("parse product id: %v", err)
		}
		var status string
		if err := h.DB.Raw(`SELECT status FROM products WHERE id = ?`, id).Scan(&status).Error; err != nil {
			t.Fatalf("query products.status for %s: %v", productID, err)
		}
		return status
	}

	// 前置：两行各 quantity 3 / 4；基线列表两行都标 on_sale（下架前的
	// 「在售」对照形态）。
	resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productX, "quantity": 3}, nil)
	env, _ := h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add X status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemX := readString(t, jsonMap(t, env.Data), "id")
	resp = h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
		map[string]any{"product_id": productY, "quantity": 4}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("seed add Y status=%d code=%d body=%s, want 200/0", resp.StatusCode, env.Code, env.Message)
	}
	itemY := readString(t, jsonMap(t, env.Data), "id")

	rows := listRows()
	assertRow(rowOf(rows, productX), itemX, 3, "on_sale")
	assertRow(rowOf(rows, productY), itemY, 4, "on_sale")

	// WHEN 管理员下架商品 X（真实管理端路由）。
	patchProductStatus(productX, "off_sale")
	// 层 4：下架落库（商品行未被删除，status 已为 off_sale）。
	if got := productStatusInDB(productX); got != "off_sale" {
		t.Fatalf("products.status for %s = %s in DB, want off_sale", productX, got)
	}

	// THEN 买家再拉取：行仍在（listRows 已断恒 2 行）、id/quantity 不变、
	// product.status=off_sale 即不可购标记；其他在售行不被标记。
	rows = listRows()
	assertRow(rowOf(rows, productX), itemX, 3, "off_sale")
	assertRow(rowOf(rows, productY), itemY, 4, "on_sale")
	// 层 4：「商品下架不删除购物车记录」的 DB 侧证据——目标 (user_id,
	// product_id) 恰一行且 quantity 和 3，且被标记的行仍是原主键那一行；
	// 对照行一行未动。
	assertCartRow(t, h.DB, userID, productXID, 1, 3)
	assertCartRow(t, h.DB, userID, productYID, 1, 4)
	assertCartQuantityByItemID(t, h.DB, itemX, 3)
	assertCartQuantityByItemID(t, h.DB, itemY, 4)

	// 层 2 edge：连续再拉取 2 次——标记稳定，行不漂移、不被延迟删除，
	// 每次拉取后按主键直查复核。
	for round := 1; round <= 2; round++ {
		rows = listRows()
		assertRow(rowOf(rows, productX), itemX, 3, "off_sale")
		assertRow(rowOf(rows, productY), itemY, 4, "on_sale")
		assertCartQuantityByItemID(t, h.DB, itemX, 3)
	}

	// 层 2 edge（隔离）：下架行仍在时，对其他在售行 Y 的正常 PATCH 不受影响
	// （4→6 成功），且下架目标行 X 不被旁改。
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemY,
		map[string]any{"quantity": 6}, nil)
	env, _ = h.Decode(resp)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("PATCH on-sale row %s quantity=6 status=%d code=%d body=%s, want 200/0",
			itemY, resp.StatusCode, env.Code, env.Message)
	}
	assertCartQuantityByItemID(t, h.DB, itemY, 6)
	assertCartQuantityByItemID(t, h.DB, itemX, 3)
	rows = listRows()
	assertRow(rowOf(rows, productX), itemX, 3, "off_sale")
	assertRow(rowOf(rows, productY), itemY, 6, "on_sale")

	// 层 2 观测（不 invent）：对下架行 X 自身 PATCH 数量——spec 未定义下架行
	// 能否改数量。用同值 3 提交，使「成功/拒绝」两种结局下行值都仍是 3，
	// 只断不变式：不得 5xx；2xx 必 code=0、拒绝必 code≠0；行 quantity 恒 3；
	// 列表标记侧仍 off_sale。实际码值以 t.Logf 如实记录。
	resp = h.BuyerDo(token, http.MethodPatch, "/api/v1/cart/"+itemX,
		map[string]any{"quantity": 3}, nil)
	env, _ = h.Decode(resp)
	t.Logf("observed PATCH on off-sale row (same-value quantity=3) -> status=%d code=%d message=%q (spec 未定义下架行改数量口径，只观测)",
		resp.StatusCode, env.Code, env.Message)
	if resp.StatusCode >= http.StatusInternalServerError {
		t.Fatalf("PATCH off-sale row returned %d — must not 5xx", resp.StatusCode)
	}
	if resp.StatusCode < http.StatusBadRequest && env.Code != 0 {
		t.Fatalf("PATCH off-sale row 2xx but business code = %d, want 0", env.Code)
	}
	if resp.StatusCode >= http.StatusBadRequest && env.Code == 0 {
		t.Fatalf("PATCH off-sale row rejected (%d) with success business code 0", resp.StatusCode)
	}
	assertCartQuantityByItemID(t, h.DB, itemX, 3)
	rows = listRows()
	assertRow(rowOf(rows, productX), itemX, 3, "off_sale")

	// 观测（不 invent）：重新上架后标记形态。spec 未写重新上架 scenario，
	// 购物车侧只记 t.Logf；管理端 PATCH 成功仅作为观测前提断言。行仍在由
	// rowOf 兜底（上下架都不该删行——Requirement「下架不删除记录」的下架
	// 侧已断，这里是同一不变式的复确认，不针对标记形态发明断言）。
	patchProductStatus(productX, "on_sale")
	rows = listRows()
	relisted := rowOf(rows, productX)
	t.Logf("observed after re-listing: row %s still present, quantity=%v, product.status=%q (spec 无重新上架 scenario，只观测不断言标记形态)",
		readString(t, relisted, "id"), relisted["quantity"], productStatusOf(relisted))
	// 收尾层 4：整场上下架操作序列后购物车记录仍在库且数量未动。
	assertCartRow(t, h.DB, userID, productXID, 1, 3)
}

// spec: B8
//
// 库存不足：买家 GET /api/v1/cart，某行对应商品的当前库存小于该行数量，
// 该行被标记为不可购（行仍在列表中，标记依据是行内数量与商品当前库存的比对）。
//
// 「标记为不可购」的实际可观测面（读实现确认，非发明字段）：列表行投影
// cartItemJSON/cartProductJSON（internal/cart/cart_handler.go 行 203-219）
// 没有独立的 purchasable/缺货布尔字段；不可购标记的唯一载体是行自身的
// quantity 与嵌套 product.stock 的实时取值——service.List
// （internal/cart/service.go 行 140-156）每次拉取都 Preload("Product") 实时
// 读商品，toView → product.Service.View 透传 DB stock 字段
// （internal/product/service.go 行 486 → cart_handler.go 行 217、241
// "stock"）。实现不为缺货行改写 product.status（service.go 行 31-33 注释：
// 仅下架商品经 status 报不可购）——故断言按实际形态：
//
//	不可购 = 行同时带 quantity Q 与 product.stock S 且 S < Q 成立、status
//	仍 on_sale（缺货不借用 B7 的下架标记面，两面独立）；
//	可购   = S ≥ Q 且 status=on_sale。
//
// 行仍在列表是 Requirement「在购物车列表中标出已下架或缺货的行」的载体面，
// 一并断言。
//
// 库存变动通道走真实管理端路由 PATCH /api/admin/v1/products/{id}
// {"stock": N}（productPatchRequest.Stock *int32，internal/product/
// handler.go 行 335；service.Update 写 updates["stock"]，internal/product/
// service.go 行 302-303；校验仅拒负数、0 合法，行 407-408）——沿用 B7 走
// 真路由的先例，不用后门直改。SeedProduct 只设初始前置库存（既有测试基建，
// 与 B1-B7 同形态）。
//
// 四层断言覆盖说明：
//  1. 端到端响应：三次库存变更 PATCH 与多轮 GET 全部走真实路由；PATCH 断
//     200/code=0/trace_id 及响应体 id+stock，GET 断 200/code=0/trace_id 与
//     列表每行的 id/quantity/product.stock/product.status。
//  2. 参数 edge case：本 B 决定行为的变量是（行数量 Q，商品当前库存 S）的
//     组合，边界逐一覆盖——S=Q 恰好相等（行 B 基线 Q=4、S=4：spec 措辞为
//     「小于」，相等不标）；S=Q-1（行 B 变更后 S=3，越界的第一刻即被标，
//     与基线合证「小于等于」不成立）；S<Q 一般形态（行 A S=2<Q=3）；
//     S=0 而 Q>0（行 D S=0<Q=2，零库存不足）；S>Q 对照（行 C S=10>Q=2，
//     不误标）；回补 S>Q（行 A 回到 S=5，列表立即反映——spec AND「当前库存」
//     的实时面）。改单一商品库存后其余行标记态不受影响（隔离）。
//     边界声明：直接加购零库存商品属 FU-e59a3c18（spec Coverage Gaps 未定
//     义），行 D 走「先加购后减库存」构造，避开未定义路径；缺货行改数量
//     spec 未定义，不操作；下架标记属 B7；结算排除属 B9（小程序端内）；
//     未登录与非法数量属 B2/B6，不越界重测。
//  3. 外部服务调用参数：拉取列表与库存变更链路不触达任何第三方 HTTP 服务
//     （stock 读写在进程内 + DB 完成），无可 mock 的外部调用，本层缺省。
//  4. 数据库字段变动：每次管理端 PATCH 后直查 products.stock 证变更落库；
//     标记生效后按行主键直查 cart_items，quantity 与加购基线一致（标记不动
//     行数据）；GET 是读操作——全部库存变更完成后连续三次拉列表，前后全库
//     快照（products.id+stock、cart_items.id+quantity）逐行比对，证「拉列表
//     不消耗、不锁定库存」，不只信 API 返回值。
func TestSpecCartB8_InsufficientStockMarksCartRowNonPurchasable(t *testing.T) {
	h := NewHarness(t, testDB)
	h.RegisterBuyer("code-cart-b8", "cart-b8-openid")
	token, user := h.BuyerLogin("code-cart-b8")
	userID, err := uid.ParseCanonical(readString(t, user, "id"))
	if err != nil {
		t.Fatalf("parse buyer id: %v", err)
	}
	parseID := func(s string) uid.ID {
		t.Helper()
		id, err := uid.ParseCanonical(s)
		if err != nil {
			t.Fatalf("parse id %q: %v", s, err)
		}
		return id
	}

	// 四商品（SeedProduct(name, pricePoints, stock, status) 只设初始前置库存；
	// 行数量全部合法加购取得，不踩 FU-e59a3c18 的零库存直接加购未定义区）：
	//   A 不足目标行：S=5 加 3（基线可购），后减到 S=2 → 2<3
	//   B 边界行：    S=4 加 4（Q=S 相等不标），后减到 S=3 → 3<4 越界即标
	//   C 充足对照行：S=10 加 2 → 2<10，全程保持不标
	//   D 零库存行：  S=6 加 2（合法加购），后减到 S=0 → 0<2
	productA := h.SeedProduct("Cart B8 Low Target", 10, 5, "on_sale")
	productB := h.SeedProduct("Cart B8 Boundary Target", 10, 4, "on_sale")
	productC := h.SeedProduct("Cart B8 Sufficient Control", 10, 10, "on_sale")
	productD := h.SeedProduct("Cart B8 Zero Stock Target", 10, 6, "on_sale")

	addRow := func(productID string, quantity int) string {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodPost, "/api/v1/cart",
			map[string]any{"product_id": productID, "quantity": quantity}, nil)
		env, _ := h.Decode(resp)
		if resp.StatusCode != http.StatusOK || env.Code != 0 {
			t.Fatalf("seed add product %s quantity=%d status=%d code=%d body=%s, want 200/0",
				productID, quantity, resp.StatusCode, env.Code, env.Message)
		}
		return readString(t, jsonMap(t, env.Data), "id")
	}
	itemA := addRow(productA, 3)
	itemB := addRow(productB, 4)
	itemC := addRow(productC, 2)
	itemD := addRow(productD, 2)

	// listRows 拉买家列表：断 200/code=0/trace_id 与行数恒 4——缺货行必须
	// 仍在列表里才有「被标记」的载体（Requirement 标出缺货行）。
	listRows := func() []map[string]any {
		t.Helper()
		resp := h.BuyerDo(token, http.MethodGet, "/api/v1/cart", nil, nil)
		env, _ := h.Decode(resp)
		h.AssertTraceID(resp, env)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/cart status = %d body=%s, want 200", resp.StatusCode, env.Message)
		}
		if env.Code != 0 {
			t.Fatalf("GET /api/v1/cart business code = %d, want 0", env.Code)
		}
		rows := listData(t, env.Data)
		if len(rows) != 4 {
			t.Fatalf("cart list rows = %d, want 4 (insufficient stock must not drop cart rows)", len(rows))
		}
		return rows
	}
	rowOf := func(rows []map[string]any, productID string) map[string]any {
		t.Helper()
		var found map[string]any
		matched := 0
		for _, row := range rows {
			if readString(t, row, "product_id") == productID {
				found = row
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("cart rows for product %s = %d, want exactly 1", productID, matched)
		}
		return found
	}
	// assertRowPurchasability 按实现实际可观测面断一行：行 id/quantity、嵌套
	// product.stock（当前库存实时值）、product.status（恒 on_sale——缺货不
	// 借用下架标记面）。wantNonPurchasable 与 (wantQty, wantStock) 组合自守
	// 卫：不可购要求 wantStock < wantQty、可购要求 wantStock >= wantQty，
	// 防把边界方向写反造成假断言。
	assertRowPurchasability := func(label string, row map[string]any, wantItemID string, wantQty, wantStock int, wantNonPurchasable bool) {
		t.Helper()
		if wantNonPurchasable && wantStock >= wantQty {
			t.Fatalf("%s: test premise broken — non-purchasable expects stock(%d) < quantity(%d)", label, wantStock, wantQty)
		}
		if !wantNonPurchasable && wantStock < wantQty {
			t.Fatalf("%s: test premise broken — purchasable expects stock(%d) >= quantity(%d)", label, wantStock, wantQty)
		}
		if got := readString(t, row, "id"); got != wantItemID {
			t.Fatalf("%s: cart row id = %s, want same row %s (out-of-stock mark must not recreate or drop the row)", label, got, wantItemID)
		}
		if q, ok := row["quantity"].(float64); !ok || q != float64(wantQty) {
			t.Fatalf("%s: cart row %s quantity = %v, want %d", label, wantItemID, row["quantity"], wantQty)
		}
		nested, ok := row["product"].(map[string]any)
		if !ok {
			t.Fatalf("%s: cart row carries no nested product object (row-level purchasability signal missing): %v", label, row)
		}
		s, ok := nested["stock"].(float64)
		if !ok {
			t.Fatalf("%s: cart row %s product carries no numeric stock (no current-stock signal to compare against quantity): %v", label, wantItemID, nested)
		}
		if int(s) != wantStock {
			t.Fatalf("%s: cart row %s product.stock = %v, want current stock %d", label, wantItemID, s, wantStock)
		}
		if got, ok := nested["status"].(string); !ok || got != "on_sale" {
			t.Fatalf("%s: cart row %s product.status = %v, want on_sale (insufficient stock must not be reported through the off_sale status — that is B7's off-sale marker)", label, wantItemID, nested["status"])
		}
		if wantNonPurchasable {
			t.Logf("observed %s: row %s quantity=%d, product.stock=%d, %d < %d — marked non-purchasable via in-row stock<quantity comparison", label, wantItemID, wantQty, wantStock, wantStock, wantQty)
		} else {
			t.Logf("observed %s: row %s quantity=%d, product.stock=%d, %d >= %d — stays purchasable", label, wantItemID, wantQty, wantStock, wantStock, wantQty)
		}
	}

	// patchProductStock 走真实管理端路由改库存：层 1（200/code=0/trace_id、
	// 响应体 id+stock）+ 层 4（直查 products.stock 证变更落库）。
	patchProductStock := func(productID string, wantStock int) {
		t.Helper()
		resp := h.AdminDo(h.SuperAdmin, http.MethodPatch, "/api/admin/v1/products/"+productID,
			map[string]any{"stock": wantStock}, nil)
		env, _ := h.Decode(resp)
		h.AssertTraceID(resp, env)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("admin PATCH product %s stock=%d -> %d body=%s, want 200", productID, wantStock, resp.StatusCode, env.Message)
		}
		if env.Code != 0 {
			t.Fatalf("admin PATCH product %s business code = %d, want 0", productID, env.Code)
		}
		updated := jsonMap(t, env.Data)
		if got := readString(t, updated, "id"); got != productID {
			t.Fatalf("admin PATCH response id = %s, want %s", got, productID)
		}
		if s, ok := updated["stock"].(float64); !ok || int(s) != wantStock {
			t.Fatalf("admin PATCH response stock = %v, want %d", updated["stock"], wantStock)
		}
		if got := h.ProductStock(productID); int(got) != wantStock {
			t.Fatalf("products.stock for %s = %d in DB, want %d", productID, got, wantStock)
		}
	}

	// 基线：变更前四行都不得带不可购标记——含关键边界 Q=S（行 B 4=4：spec
	// 措辞「小于」，相等不标）。
	rows := listRows()
	assertRowPurchasability("baseline A (S=5 > Q=3)", rowOf(rows, productA), itemA, 3, 5, false)
	assertRowPurchasability("baseline B (Q=S=4, equal is not less than)", rowOf(rows, productB), itemB, 4, 4, false)
	assertRowPurchasability("baseline C (S=10 > Q=2)", rowOf(rows, productC), itemC, 2, 10, false)
	assertRowPurchasability("baseline D (S=6 > Q=2)", rowOf(rows, productD), itemD, 2, 6, false)
	if got := h.ProductStock(productA); got != 5 {
		t.Fatalf("DB baseline products.stock A = %d, want 5", got)
	}
	if got := h.ProductStock(productB); got != 4 {
		t.Fatalf("DB baseline products.stock B = %d, want 4", got)
	}
	if got := h.ProductStock(productC); got != 10 {
		t.Fatalf("DB baseline products.stock C = %d, want 10", got)
	}
	if got := h.ProductStock(productD); got != 6 {
		t.Fatalf("DB baseline products.stock D = %d, want 6", got)
	}

	// WHEN 当前库存降到小于行数量：三次真实管理端 PATCH——
	// A: 5→2（S<Q 一般形态 2<3）；B: 4→3（越界 S=Q-1，3<4）；D: 6→0（零库存 0<2）。
	patchProductStock(productA, 2)
	patchProductStock(productB, 3)
	patchProductStock(productD, 0)

	// THEN 买家拉取列表：三条不足行被标记为不可购（行仍在、quantity 不变、
	// 行内 stock < quantity、status 不被改写），充足对照行 C 不误标。
	rows = listRows()
	assertRowPurchasability("A after S=2 < Q=3", rowOf(rows, productA), itemA, 3, 2, true)
	assertRowPurchasability("B after S=Q-1 (3 < 4)", rowOf(rows, productB), itemB, 4, 3, true)
	assertRowPurchasability("C control (S=10 > Q=2)", rowOf(rows, productC), itemC, 2, 10, false)
	assertRowPurchasability("D zero stock (0 < 2)", rowOf(rows, productD), itemD, 2, 0, true)

	// 层 4：标记不动行数据——cart_items 各行 quantity 与加购基线一致，
	// (user_id, product_id) 仍各恰一行。
	assertCartQuantityByItemID(t, h.DB, itemA, 3)
	assertCartQuantityByItemID(t, h.DB, itemB, 4)
	assertCartQuantityByItemID(t, h.DB, itemC, 2)
	assertCartQuantityByItemID(t, h.DB, itemD, 2)
	assertCartRow(t, h.DB, userID, parseID(productA), 1, 3)
	assertCartRow(t, h.DB, userID, parseID(productB), 1, 4)
	assertCartRow(t, h.DB, userID, parseID(productC), 1, 2)
	assertCartRow(t, h.DB, userID, parseID(productD), 1, 2)

	// 持久面（层 4）：GET 是读操作——全部库存变更完成后连续三次拉列表，
	// 前后全库快照（products.id+stock、cart_items.id+quantity）逐行比对，
	// 证「拉列表不消耗、不锁定库存」。
	type dbSnapshot struct {
		products []struct {
			ID    uid.ID
			Stock int64
		}
		items []struct {
			ID       uid.ID
			Quantity int64
		}
	}
	snapshot := func() dbSnapshot {
		t.Helper()
		var snap dbSnapshot
		if err := h.DB.Raw(`SELECT id, stock FROM products ORDER BY id`).Scan(&snap.products).Error; err != nil {
			t.Fatalf("snapshot products: %v", err)
		}
		if err := h.DB.Raw(`SELECT id, quantity FROM cart_items ORDER BY id`).Scan(&snap.items).Error; err != nil {
			t.Fatalf("snapshot cart_items: %v", err)
		}
		return snap
	}
	before := snapshot()
	if len(before.products) != 4 || len(before.items) != 4 {
		t.Fatalf("pre-GET snapshot products=%d items=%d, want 4/4", len(before.products), len(before.items))
	}
	for round := 1; round <= 3; round++ {
		listRows()
		after := snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("after GET round %d: DB snapshot changed (GET /api/v1/cart must not consume or lock stock):\nbefore products=%v items=%v\nafter  products=%v items=%v",
				round, before.products, before.items, after.products, after.items)
		}
	}

	// 层 2 edge（「当前库存」实时面）：回补行 A 库存到 S=5（≥Q=3）再拉列表，
	// 该行 product.stock 立即更新为 5——列表读的是拉取时刻的库存，不是加购
	// 时快照；B/C/D 不受影响（隔离：改单一商品只落自己一行）。
	patchProductStock(productA, 5)
	rows = listRows()
	assertRowPurchasability("A after restock S=5 > Q=3", rowOf(rows, productA), itemA, 3, 5, false)
	assertRowPurchasability("B stays out of stock (3 < 4)", rowOf(rows, productB), itemB, 4, 3, true)
	assertRowPurchasability("C stays purchasable (2 < 10)", rowOf(rows, productC), itemC, 2, 10, false)
	assertRowPurchasability("D stays zero stock (0 < 2)", rowOf(rows, productD), itemD, 2, 0, true)

	// 收尾层 4：最终 DB 状态——products.stock 为 5/3/10/0，cart 行数据全程
	// 未被任何读写路径改动。
	if got := h.ProductStock(productA); got != 5 {
		t.Fatalf("final products.stock A = %d, want 5", got)
	}
	if got := h.ProductStock(productB); got != 3 {
		t.Fatalf("final products.stock B = %d, want 3", got)
	}
	if got := h.ProductStock(productC); got != 10 {
		t.Fatalf("final products.stock C = %d, want 10", got)
	}
	if got := h.ProductStock(productD); got != 0 {
		t.Fatalf("final products.stock D = %d, want 0", got)
	}
	assertCartQuantityByItemID(t, h.DB, itemA, 3)
	assertCartQuantityByItemID(t, h.DB, itemB, 4)
	assertCartQuantityByItemID(t, h.DB, itemC, 2)
	assertCartQuantityByItemID(t, h.DB, itemD, 2)
}

// assertSingleListRow 断言 GET /api/v1/cart 返回的行中，指定 product 恰好
// 出现一行且 quantity 为期望值。
func assertSingleListRow(t *testing.T, rows []map[string]any, productID string, wantQuantity float64) {
	t.Helper()
	matched := 0
	for _, row := range rows {
		if readString(t, row, "product_id") != productID {
			continue
		}
		matched++
		if q := row["quantity"].(float64); q != wantQuantity {
			t.Fatalf("cart row for %s quantity = %v, want %v", productID, q, wantQuantity)
		}
	}
	if matched != 1 {
		t.Fatalf("cart rows for product %s = %d, want exactly 1", productID, matched)
	}
}

// assertCartRow 直查 cart_items，按 (user_id, product_id) 断言行数与
// quantity 之和。
func assertCartRow(t *testing.T, db *gorm.DB, userID, productID uid.ID, wantRowCount int64, wantQuantitySum int64) {
	t.Helper()
	var rowCount, quantitySum int64
	if err := db.Raw(`SELECT count(*), COALESCE(sum(quantity), 0) FROM cart_items WHERE user_id = ? AND product_id = ?`,
		userID, productID).Row().Scan(&rowCount, &quantitySum); err != nil {
		t.Fatalf("query cart_items for (user %d, product %d): %v", userID, productID, err)
	}
	if rowCount != wantRowCount {
		t.Fatalf("cart_items rows for (user %d, product %d) = %d, want %d",
			userID, productID, rowCount, wantRowCount)
	}
	if quantitySum != wantQuantitySum {
		t.Fatalf("cart_items quantity sum for (user %d, product %d) = %d, want %d",
			userID, productID, quantitySum, wantQuantitySum)
	}
}

// assertCartQuantityByItemID 直查 cart_items，按行主键 id 断言该行恰好存在一
// 条且 quantity 为期望值——绕开 API 返回值，验证「改数量」真正落到目标行。
func assertCartQuantityByItemID(t *testing.T, db *gorm.DB, itemID string, wantQuantity int64) {
	t.Helper()
	id, err := uid.ParseCanonical(itemID)
	if err != nil {
		t.Fatalf("parse cart item id %q: %v", itemID, err)
	}
	var rowCount, quantity int64
	if err := db.Raw(`SELECT count(*), COALESCE(max(quantity), 0) FROM cart_items WHERE id = ?`, id).
		Row().Scan(&rowCount, &quantity); err != nil {
		t.Fatalf("query cart_items by id %s: %v", itemID, err)
	}
	if rowCount != 1 {
		t.Fatalf("cart_items rows for id %s = %d, want exactly 1", itemID, rowCount)
	}
	if quantity != wantQuantity {
		t.Fatalf("cart_items quantity for id %s = %d, want %d", itemID, quantity, wantQuantity)
	}
}

// cartQuantityByItemID 直查 cart_items 按行主键返回该行 quantity（断言行恰
// 在，不预设数值），供需要比较前后取值的观测型断言使用。
func cartQuantityByItemID(t *testing.T, db *gorm.DB, itemID string) int64 {
	t.Helper()
	id, err := uid.ParseCanonical(itemID)
	if err != nil {
		t.Fatalf("parse cart item id %q: %v", itemID, err)
	}
	var rowCount, quantity int64
	if err := db.Raw(`SELECT count(*), COALESCE(max(quantity), 0) FROM cart_items WHERE id = ?`, id).
		Row().Scan(&rowCount, &quantity); err != nil {
		t.Fatalf("query cart_items by id %s: %v", itemID, err)
	}
	if rowCount != 1 {
		t.Fatalf("cart_items rows for id %s = %d, want exactly 1", itemID, rowCount)
	}
	return quantity
}
