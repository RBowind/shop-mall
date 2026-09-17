package e2e

// Acceptance tests for specs/coupon/spec.md (sprint contract
// specs/coupon/contract.md). Each test name carries the contract B id it
// anchors. Scenarios already verified elsewhere are mapped in
// B* ↔ test coverage below and NOT duplicated here:
//
//   B1 TestSpecCouponB1_CreateTemplateLandsActiveZeroReceivedAndAudits (this file)
//   B2 TestSpecCouponB2_RejectInvalidRulesWithoutCreatingTemplate (this file)
//   B3 TestSpecCouponB3_CreateWithoutWritePermissionForbiddenAndFailureAudited (this file)
//   B4 TestSpecCouponB4_RuleFieldsFrozenAfterCreation (this file)
//   B5-B24 续写于本文件，按 B 编号分节。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"
)

// contract: B1
//
// 持 coupon:write 的管理员提交完整规则创建券模板：创建成功，初始状态为发放中
// （active）、已领取数为 0，并留下一条成功审计记录本次创建。
//
// 四层断言覆盖说明：
//  1. 端到端响应：POST /api/admin/v1/coupon-templates 走真实管理域路由（Cookie
//     JWT + 权限码 + CSRF），断言 201/200 与 envelope code=0；techspec 07 §5 未
//     定义该端点的响应字段名（券路径不进 docs/api/openapi.yaml，见 contract
//     FU-0a8e7f3b），故只断言响应 data 引用了刚创建的模板 id，不绑定字段名。
//  2. 参数 edge case：B1 的入参取值域是合法规则集；非法规则（非正整数、抵扣不
//     小于门槛、valid_until 不晚于 valid_from）与缺 coupon:write 权限分别是 B2、
//     B3 的 Scenario，由本文件那两条用例逐条覆盖，此处不重复。
//  3. 外部服务调用参数：创建模板链路不触达任何第三方 HTTP 服务（无图片上传、
//     无微信调用、无支付网关），无可 mock 的外部调用，本层缺省。
//  4. 数据库字段变动：绕过响应体直查 coupon_templates，断言按名称恰有一行、
//     status='active'、received_count=0，且门槛/抵扣/总量/每人限领/有效期起止
//     等于提交值；再直查 audit_logs，断言存在恰一条 target_id 为该模板的
//     result='success' 审计行且记录了操作人——「一条成功审计记录本次创建」。
func TestSpecCouponB1_CreateTemplateLandsActiveZeroReceivedAndAudits(t *testing.T) {
	h := NewHarness(t, testDB)

	// 模板名带唯一后缀：同一 package 内的用例如共享测试库，避免后续券用例
	// 的同名模板污染「按名称恰有一行」这条断言。
	name := fmt.Sprintf("E2E券模板B1-%d", time.Now().UnixNano())
	// 有效期窗口覆盖当前时刻（valid_from 已过、valid_until 未到），使模板处于
	// 可发放状态；窗口本身取自提交值，后面逐字段回查。
	validFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	validUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	submitted := map[string]any{
		"name":             name,
		"threshold_points": 500,
		"discount_points":  100,
		"total_count":      20,
		"per_user_limit":   2,
		"valid_from":       validFrom.Format(time.RFC3339),
		"valid_until":      validUntil.Format(time.RFC3339),
	}

	// WHEN 持 coupon:write 的管理员（harness 的 super_admin，0006 迁移已把
	// coupon:write 授予 super_admin 与 operator）提交名称、门槛积分、抵扣积分、
	// 总量、每人限领与 valid_from / valid_until。
	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", submitted, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("POST /api/admin/v1/coupon-templates status = %d body=%s, want 201 或 200（券模板创建接口未实现时这里是 404）",
			resp.StatusCode, raw)
	}
	env, _ := h.Decode(resp)
	h.AssertTraceID(resp, env)
	if env.Code != 0 {
		t.Fatalf("创建券模板 envelope code = %d body=%s, want 0", env.Code, h.dump(env))
	}

	// THEN 模板创建成功：库里按名称恰有一条模板行（新库只可能有本次创建的行）。
	var rows []struct {
		ID              uid.ID
		Name            string
		ThresholdPoints int64
		DiscountPoints  int64
		TotalCount      int32
		PerUserLimit    int32
		ReceivedCount   int32
		ValidFrom       time.Time
		ValidUntil      time.Time
		Status          string
	}
	if err := h.DB.Raw(
		`SELECT id, name, threshold_points, discount_points, total_count, per_user_limit,
		        received_count, valid_from, valid_until, status
		 FROM coupon_templates WHERE name = ?`, name,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("query coupon_templates by name: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("coupon_templates rows named %q = %d, want exactly 1 (模板未创建)", name, len(rows))
	}
	created := rows[0]

	// THEN 初始状态为发放中（active），已领取数为 0。
	if created.Status != "active" {
		t.Fatalf("新建模板 status = %q, want active（发放中）", created.Status)
	}
	if created.ReceivedCount != 0 {
		t.Fatalf("新建模板 received_count = %d, want 0", created.ReceivedCount)
	}

	// THEN 提交的规则落入该模板——「创建成功」的可观测含义。
	if created.Name != name {
		t.Fatalf("模板 name = %q, want %q", created.Name, name)
	}
	if created.ThresholdPoints != int64(500) {
		t.Fatalf("模板 threshold_points = %d, want 500", created.ThresholdPoints)
	}
	if created.DiscountPoints != int64(100) {
		t.Fatalf("模板 discount_points = %d, want 100", created.DiscountPoints)
	}
	if created.TotalCount != int32(20) {
		t.Fatalf("模板 total_count = %d, want 20", created.TotalCount)
	}
	if created.PerUserLimit != int32(2) {
		t.Fatalf("模板 per_user_limit = %d, want 2", created.PerUserLimit)
	}
	if !created.ValidFrom.UTC().Equal(validFrom) {
		t.Fatalf("模板 valid_from = %s, want %s", created.ValidFrom.UTC(), validFrom)
	}
	if !created.ValidUntil.UTC().Equal(validUntil) {
		t.Fatalf("模板 valid_until = %s, want %s", created.ValidUntil.UTC(), validUntil)
	}

	// 层 1 补充：成功响应指向刚创建的模板。字段名未冻结，只断言 id 出现在 data 中。
	if !strings.Contains(strings.ToLower(string(env.Data)), strings.ToLower(created.ID.String())) {
		t.Fatalf("创建响应 data 未引用刚创建的模板 %s: data=%s", created.ID, env.Data)
	}

	// THEN 一条成功审计记录本次创建：audit_logs 中 target_id 指向该模板的行恰有
	// 一条，result='success'，且记录了操作人。
	var audits []struct {
		Result   string
		Action   string
		ActorSet bool
	}
	if err := h.DB.Raw(
		`SELECT result, action, (actor_admin_id IS NOT NULL) AS actor_set
		 FROM audit_logs WHERE target_id = ?`, created.ID,
	).Scan(&audits).Error; err != nil {
		t.Fatalf("query audit_logs for template %s: %v", created.ID, err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit_logs rows for template %s = %d, want exactly 1 success record of this creation",
			created.ID, len(audits))
	}
	if audits[0].Result != "success" {
		t.Fatalf("创建审计 result = %q, want success", audits[0].Result)
	}
	if !audits[0].ActorSet {
		t.Fatalf("创建审计未记录操作人（actor_admin_id 为空）: action=%q", audits[0].Action)
	}
}

// b2TemplateRow is the coupon_templates row shape B2 reads back from the
// database directly, bypassing the create response.
type b2TemplateRow struct {
	ID              uid.ID
	Name            string
	ThresholdPoints int64
	DiscountPoints  int64
	TotalCount      int32
	PerUserLimit    int32
	ReceivedCount   int32
	ValidFrom       time.Time
	ValidUntil      time.Time
	Status          string
}

// b2TemplateBody is a fully legal create payload; every B2 sub-case mutates
// exactly one rule on top of it so a failure is attributable to that rule.
func b2TemplateBody(name string, validFrom, validUntil time.Time) map[string]any {
	return map[string]any{
		"name":             name,
		"threshold_points": 500,
		"discount_points":  100,
		"total_count":      20,
		"per_user_limit":   2,
		"valid_from":       validFrom.Format(time.RFC3339),
		"valid_until":      validUntil.Format(time.RFC3339),
	}
}

// b2ReadTemplate reads the at-most-one template row carrying this exact name.
func b2ReadTemplate(t *testing.T, h *Harness, name string) (b2TemplateRow, bool) {
	t.Helper()
	var rows []b2TemplateRow
	if err := h.DB.Raw(
		`SELECT id, name, threshold_points, discount_points, total_count, per_user_limit,
		        received_count, valid_from, valid_until, status
		 FROM coupon_templates WHERE name = ?`, name,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("query coupon_templates by name %q: %v", name, err)
	}
	if len(rows) > 1 {
		t.Fatalf("coupon_templates rows named %q = %d, want at most 1", name, len(rows))
	}
	if len(rows) == 0 {
		return b2TemplateRow{}, false
	}
	return rows[0], true
}

// b2CountTemplates returns the total row count of coupon_templates.
func b2CountTemplates(t *testing.T, h *Harness) int {
	t.Helper()
	var count int64
	if err := h.DB.Table("coupon_templates").Count(&count).Error; err != nil {
		t.Fatalf("count coupon_templates: %v", err)
	}
	return int(count)
}

// b2AssertTemplateUnchanged compares a template row field by field against the
// snapshot taken before the rejected request.
func b2AssertTemplateUnchanged(t *testing.T, desc string, before, after b2TemplateRow) {
	t.Helper()
	if after.ID != before.ID {
		t.Fatalf("%s：已创建模板 id 由 %s 变为 %s", desc, before.ID, after.ID)
	}
	if after.Name != before.Name {
		t.Fatalf("%s：已创建模板 name 由 %q 变为 %q", desc, before.Name, after.Name)
	}
	if after.ThresholdPoints != before.ThresholdPoints {
		t.Fatalf("%s：已创建模板 threshold_points 由 %d 变为 %d", desc, before.ThresholdPoints, after.ThresholdPoints)
	}
	if after.DiscountPoints != before.DiscountPoints {
		t.Fatalf("%s：已创建模板 discount_points 由 %d 变为 %d", desc, before.DiscountPoints, after.DiscountPoints)
	}
	if after.TotalCount != before.TotalCount {
		t.Fatalf("%s：已创建模板 total_count 由 %d 变为 %d", desc, before.TotalCount, after.TotalCount)
	}
	if after.PerUserLimit != before.PerUserLimit {
		t.Fatalf("%s：已创建模板 per_user_limit 由 %d 变为 %d", desc, before.PerUserLimit, after.PerUserLimit)
	}
	if after.ReceivedCount != before.ReceivedCount {
		t.Fatalf("%s：已创建模板 received_count 由 %d 变为 %d", desc, before.ReceivedCount, after.ReceivedCount)
	}
	if !after.ValidFrom.UTC().Equal(before.ValidFrom.UTC()) {
		t.Fatalf("%s：已创建模板 valid_from 由 %s 变为 %s", desc, before.ValidFrom.UTC(), after.ValidFrom.UTC())
	}
	if !after.ValidUntil.UTC().Equal(before.ValidUntil.UTC()) {
		t.Fatalf("%s：已创建模板 valid_until 由 %s 变为 %s", desc, before.ValidUntil.UTC(), after.ValidUntil.UTC())
	}
	if after.Status != before.Status {
		t.Fatalf("%s：已创建模板 status 由 %q 变为 %q", desc, before.Status, after.Status)
	}
}

// contract: B2
//
// 持 coupon:write 的管理员提交不满足任一规则约束的券模板：返回 1001 参数错误，
// 不创建模板，已创建的模板不受影响。
//
// spec Scenario「规则非法」的 WHEN 是一类非法输入，不是单个样本。本用例把该类里
// 的每一条约束各起一个子用例逐条断言，每条真踩边界（0 / 负数 / 相等）：
//
//	门槛不是正整数     → 门槛为 0、门槛为负
//	抵扣不是正整数     → 抵扣为 0、抵扣为负
//	抵扣不小于门槛     → 抵扣等于门槛、抵扣大于门槛
//	总量不是正整数     → 总量为 0、总量为负
//	每人限领不是正整数 → 每人限领为 0、每人限领为负
//	valid_until 不晚于 valid_from → 等于 valid_from、早于 valid_from
//
// 四层断言覆盖说明：
//  1. 端到端响应：每个子用例 POST /api/admin/v1/coupon-templates 走真实管理域
//     路由（Cookie JWT + coupon:write + CSRF），断言 envelope code=1001 且 HTTP
//     400——spec 的「1001 参数错误」。
//  2. 参数 edge case：本用例整体就是参数边界用例；每个子用例只改一处规则，其余
//     字段取合法值，使失败可归因到那一条约束。
//  3. 外部服务调用参数：创建模板链路不触达任何第三方 HTTP 服务（无图片上传、无
//     微信调用、无支付网关），无可 mock 的外部调用，本层缺省（同 B1）。
//  4. 数据库字段变动：绕过响应体直查 coupon_templates——按非法请求的模板名断言
//     零行、表内总行数不增；再直查先建好的合法模板行，逐字段断言其与非法请求前
//     完全一致——「不创建模板，已创建模板不受影响」。
func TestSpecCouponB2_RejectInvalidRulesWithoutCreatingTemplate(t *testing.T) {
	h := NewHarness(t, testDB)

	// 有效期窗口覆盖当前时刻，使合法模板处于可发放状态；两个边界值（下界、上界）
	// 也用于构造「上界等于/早于下界」两个子用例。
	validFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	validUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	// 先建一个完全合法的模板，作为「已创建模板不受影响」的对照对象：它先于所有
	// 非法请求存在，任何非法请求若顺手改了库，都会在这行上现形。
	baselineName := fmt.Sprintf("E2E券模板B2基线-%d", time.Now().UnixNano())
	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates",
		b2TemplateBody(baselineName, validFrom, validUntil), nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("前置合法模板创建失败 status=%d body=%s（B1 应已交付；这里失败说明前置不成立）", resp.StatusCode, raw)
	}
	if env, _ := h.Decode(resp); env.Code != 0 {
		t.Fatalf("前置合法模板创建失败 envelope code=%d, want 0", env.Code)
	}
	baseline, ok := b2ReadTemplate(t, h, baselineName)
	if !ok {
		t.Fatalf("前置合法模板未落库：coupon_templates 中无 name=%q 的行", baselineName)
	}
	baselineTotal := b2CountTemplates(t, h)
	if baselineTotal != 1 {
		t.Fatalf("前置合法模板落库后 coupon_templates 行数 = %d, want 1（新库只应有这一行）", baselineTotal)
	}

	// 每个子用例只在合法值基础上改一处规则。门槛非正整数与抵扣非正整数无法彼此
	// 完全隔离（门槛 ≤ 0 时不存在满足 discount < threshold 的正抵扣），门槛那两
	// 条子用例因此同时落在「门槛」与「抵扣」同一个非正整数约束族里，注释里标明。
	subcases := []struct {
		desc   string
		mutate func(m map[string]any)
	}{
		{"门槛为零", func(m map[string]any) { m["threshold_points"] = 0; m["discount_points"] = 0 }},
		{"门槛为负", func(m map[string]any) { m["threshold_points"] = -500; m["discount_points"] = -1000 }},
		{"抵扣为零", func(m map[string]any) { m["discount_points"] = 0 }},
		{"抵扣为负", func(m map[string]any) { m["discount_points"] = -50 }},
		{"抵扣等于门槛", func(m map[string]any) { m["discount_points"] = 500 }},
		{"抵扣大于门槛", func(m map[string]any) { m["discount_points"] = 800 }},
		{"总量为零", func(m map[string]any) { m["total_count"] = 0 }},
		{"总量为负", func(m map[string]any) { m["total_count"] = -1 }},
		{"每人限领为零", func(m map[string]any) { m["per_user_limit"] = 0 }},
		{"每人限领为负", func(m map[string]any) { m["per_user_limit"] = -2 }},
		{"有效期上界等于下界", func(m map[string]any) { m["valid_until"] = m["valid_from"] }},
		{"有效期上界早于下界", func(m map[string]any) {
			m["valid_until"] = validFrom.Add(-time.Hour).Format(time.RFC3339)
		}},
	}

	for _, tc := range subcases {
		tc := tc
		// contract: B2
		t.Run(tc.desc, func(t *testing.T) {
			name := fmt.Sprintf("E2E券模板B2非法-%s-%d", tc.desc, time.Now().UnixNano())
			body := b2TemplateBody(name, validFrom, validUntil)
			tc.mutate(body)

			// WHEN 持 coupon:write 的管理员提交违反上述某一类规则约束的模板。
			resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", body, nil)
			env, raw := h.Decode(resp)
			h.AssertTraceID(resp, env)

			// THEN 返回 1001 参数错误。非法规则是客户端入参问题，契约把它归为 1001
			// （platformhttp.CodeBadRequest），不是 5001 内部错误。
			if env.Code != 1001 || resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s：POST /api/admin/v1/coupon-templates status=%d code=%d body=%s, want HTTP 400 / code 1001 参数错误",
					tc.desc, resp.StatusCode, env.Code, raw)
			}

			// THEN 不创建模板：按本次非法请求的模板名直查，零行；表内总行数不增。
			if row, exists := b2ReadTemplate(t, h, name); exists {
				t.Fatalf("%s：非法规则被接受，coupon_templates 出现了 name=%q 的新行 id=%s", tc.desc, name, row.ID)
			}
			if got := b2CountTemplates(t, h); got != baselineTotal {
				t.Fatalf("%s：非法请求后 coupon_templates 行数 = %d, want %d（无新增行）", tc.desc, got, baselineTotal)
			}

			// THEN 已创建的模板不受影响：先建的合法模板逐字段原样还在。
			after, exists := b2ReadTemplate(t, h, baselineName)
			if !exists {
				t.Fatalf("%s：非法请求后，先建的合法模板 name=%q 消失了", tc.desc, baselineName)
			}
			b2AssertTemplateUnchanged(t, tc.desc, baseline, after)
		})
	}
}

// b3AuditRow is the audit_logs row shape B3 reads back to prove the failed
// attempt was recorded. Every column is selected through an explicit ::text
// cast so the test reads the stored value, not a driver-level JSON decoding.
type b3AuditRow struct {
	ActorAdminID string `gorm:"column:actor_admin_id"`
	ActorRole    string `gorm:"column:actor_role"`
	Action       string `gorm:"column:action"`
	TargetType   string `gorm:"column:target_type"`
	TargetID     string `gorm:"column:target_id"`
	BeforeText   string `gorm:"column:before_text"`
	AfterText    string `gorm:"column:after_text"`
}

// contract: B3
//
// 管理员角色不含 coupon:write 时提交券模板创建请求：响应 403，且一条失败审计记录
// 该次尝试。
//
// 「无 coupon:write 的管理员」的构造方式（照抄 tests/security
// TestAdminInsufficientPermission403 与 TestAdminUnknownPermission422 的既有路径，
// 不新造基建）：0006 迁移把 coupon:write 授予了 super_admin 与 operator，这两个种子
// 角色构造不出「缺 coupon:write」，因此新建一个只授 coupon:read 的角色
// coupon_reader，再经真实 BootstrapAdministrator 路径建一个管理员并真实登录。它是
// 一个合法的券域管理员，只是不含写权限——403 因此只能归因于权限码判定，不会与
// 「角色不存在」「权限集合为空」这类 fixture 事故混同。用例开头直接查 role_permissions
// 自证该角色的权限集合含 coupon:read 且不含 coupon:write，并先打一次只要求认证、
// 不要求权限的 GET /api/admin/v1/auth/me 证明会话有效。
//
// 四层断言覆盖说明：
//  1. 端到端响应：对该端点直接发 POST /api/admin/v1/coupon-templates（真闸门在
//     服务端 middleware.AdminPermission，不靠前端隐藏），断言 HTTP 403——spec 钉的
//     就是这个状态码，不用「非 200」代替，也不接受 401/404/422 任一替代语义。
//  2. 参数 edge case：本 Scenario 的 WHEN 是权限维度而非入参维度，请求体取 B2 的
//     合法规则集（b2TemplateBody），使 403 与请求内容无关；入参取值域由 B2 覆盖。
//  3. 外部服务调用参数：创建模板链路不触达任何第三方 HTTP 服务（无图片上传、无
//     微信调用、无支付网关），无可 mock 的外部调用，本层缺省（同 B1/B2）。
//  4. 数据库字段变动：绕过响应体直查两处——coupon_templates 断言本次尝试的模板名
//     零行、全表计数为 0（无副作用）；audit_logs 断言存在恰一条 result='failure'
//     的行，其 actor 是本次尝试的管理员且指向券模板创建动作——「一条失败审计记录
//     该尝试」。
func TestSpecCouponB3_CreateWithoutWritePermissionForbiddenAndFailureAudited(t *testing.T) {
	h := NewHarness(t, testDB)

	const (
		readerRoleName = "coupon_reader"
		readerUsername = "coupon-reader-e2e"
		readerPassword = "CouponReader#2026e2e"
	)

	// 只授 coupon:read 的角色，并授予该权限码（ON CONFLICT 让同包内重复运行幂等，
	// 与 tests/security 的 product_reader 构造完全同构）。
	if err := h.DB.Exec(
		`INSERT INTO roles (id, name, remark) VALUES (?, ?, ?) ON CONFLICT (name) DO NOTHING`,
		uid.New(), readerRoleName, "e2e coupon read-only role",
	).Error; err != nil {
		t.Fatalf("create role %s: %v", readerRoleName, err)
	}
	if err := h.DB.Exec(
		`INSERT INTO role_permissions (role_id, permission_id)
		 SELECT r.id, p.id FROM roles r, permissions p
		 WHERE r.name = ? AND p.code = 'coupon:read'
		 ON CONFLICT DO NOTHING`, readerRoleName,
	).Error; err != nil {
		t.Fatalf("grant coupon:read to %s: %v", readerRoleName, err)
	}

	// 前置自证：该角色的权限集合确实含 coupon:read、不含 coupon:write，否则后面
	// 的 403 无法归因到「缺 coupon:write」这条 Scenario 前提。
	var readerPermissions []string
	if err := h.DB.Raw(
		`SELECT p.code FROM role_permissions rp
		 JOIN permissions p ON p.id = rp.permission_id
		 JOIN roles r ON r.id = rp.role_id
		 WHERE r.name = ?`, readerRoleName,
	).Scan(&readerPermissions).Error; err != nil {
		t.Fatalf("read permissions of role %s: %v", readerRoleName, err)
	}
	hasRead, hasWrite := false, false
	for _, code := range readerPermissions {
		switch code {
		case "coupon:read":
			hasRead = true
		case "coupon:write":
			hasWrite = true
		}
	}
	if !hasRead {
		t.Fatalf("前置不成立：角色 %s 的权限集合 %v 不含 coupon:read", readerRoleName, readerPermissions)
	}
	if hasWrite {
		t.Fatalf("前置不成立：角色 %s 的权限集合 %v 含 coupon:write，构造不出「角色不含 coupon:write」", readerRoleName, readerPermissions)
	}

	// 经真实 bootstrap 路径创建该管理员并真实登录。
	reader := h.bootstrapAdmin(readerUsername, readerPassword, readerRoleName)
	var readerID string
	if err := h.DB.Raw(
		`SELECT id::text FROM admin_users WHERE username = ?`, readerUsername,
	).Scan(&readerID).Error; err != nil {
		t.Fatalf("read admin id of %s: %v", readerUsername, err)
	}
	if readerID == "" {
		t.Fatalf("管理员 %s 未落库", readerUsername)
	}

	// 前置自证：该会话已通过认证。GET /api/admin/v1/auth/me 只要求认证、不要求任何
	// 权限码，返回 200 说明后面的 403 来自权限闸门而不是会话失效。
	meResp := h.AdminDo(reader, http.MethodGet, "/api/admin/v1/auth/me", nil, nil)
	_, _ = h.Decode(meResp) // drains and closes the body; the envelope is not asserted here
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("前置不成立：无 coupon:write 的管理员 GET /api/admin/v1/auth/me status = %d, want 200（会话应已认证）", meResp.StatusCode)
	}

	// 请求体取 B2 的合法规则集：本次被拒的唯一原因是权限，与规则内容无关。
	name := fmt.Sprintf("E2E券模板B3-%d", time.Now().UnixNano())
	validFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	validUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	// WHEN 管理员角色不含 coupon:write 的管理员 POST /api/admin/v1/coupon-templates。
	resp := h.AdminDo(reader, http.MethodPost, "/api/admin/v1/coupon-templates",
		b2TemplateBody(name, validFrom, validUntil), nil)
	env, raw := h.Decode(resp)
	h.AssertTraceID(resp, env)

	// THEN 响应 403。权限不足的既有判定（middleware.AdminPermission → abortForbidden）
	// 就是 403；这里钉住 403 本身，401（未认证）、404（防探测）、422（权限码漂移）
	// 都不算通过。
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 coupon:write 的管理员 POST /api/admin/v1/coupon-templates status = %d code = %d body=%s, want 403（权限不足一律 403）",
			resp.StatusCode, env.Code, raw)
	}
	// 取证：闸门实测返回的状态与业务码（spec 只钉 403，业务码未冻结）。
	t.Logf("无 coupon:write 的创建尝试：HTTP %d code=%d", resp.StatusCode, env.Code)

	// THEN 无副作用：本次尝试的模板名在 coupon_templates 中零行，全表计数为 0
	// （harness 每个用例重置库，本次请求前表为空）。
	if row, exists := b2ReadTemplate(t, h, name); exists {
		t.Fatalf("无权限的创建尝试落库了：coupon_templates 出现 name=%q 的新行 id=%s", name, row.ID)
	}
	if got := b2CountTemplates(t, h); got != 0 {
		t.Fatalf("无权限的创建尝试后 coupon_templates 行数 = %d, want 0（不得有任何副作用）", got)
	}

	// THEN 一条失败审计记录该尝试：audit_logs 中 result='failure' 的行恰有一条
	// （本用例内只有本次尝试失败），它记录了操作人，且指向券模板创建。
	var failures []b3AuditRow
	if err := h.DB.Raw(
		`SELECT COALESCE(actor_admin_id::text, '') AS actor_admin_id,
		        actor_role,
		        action,
		        target_type,
		        COALESCE(target_id::text, '') AS target_id,
		        COALESCE(before_data::text, '') AS before_text,
		        COALESCE(after_data::text, '') AS after_text
		 FROM audit_logs
		 WHERE result = 'failure'
		 ORDER BY created_at, id`,
	).Scan(&failures).Error; err != nil {
		t.Fatalf("query audit_logs failure rows: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("无 coupon:write 的创建尝试后 audit_logs 中 result='failure' 的行数 = %d, want 恰好 1 条记录本次尝试；rows=%+v",
			len(failures), failures)
	}
	failed := failures[0]

	// 审计记录必须能归到本次尝试的操作人：本仓已认证管理员的写操作一律记录
	// actor_admin_id（product.create、coupon_template.create、refund.*、
	// points.adjust、image.upload 的失败分支均如此）。
	if failed.ActorAdminID != readerID {
		t.Fatalf("失败审计 actor_admin_id = %q, want 尝试创建模板的管理员 %s（action=%q target_type=%q actor_role=%q）",
			failed.ActorAdminID, readerID, failed.Action, failed.TargetType, failed.ActorRole)
	}

	// 审计记录必须对应到券模板创建这次尝试：动作或操作对象落在券模板域，或者审计
	// 载荷带着本次提交的模板名。spec 与 contract 未冻结失败审计的动作字符串
	// （techspec 07 §3 只写了「券模板创建」这个动作族），因此这里不钉死字面量。
	haystack := strings.ToLower(failed.Action + " " + failed.TargetType)
	if !strings.Contains(haystack, "coupon") &&
		!strings.Contains(failed.BeforeText+failed.AfterText, name) {
		t.Fatalf("失败审计未能对应到券模板创建：action=%q target_type=%q target_id=%q before=%s after=%s",
			failed.Action, failed.TargetType, failed.TargetID, failed.BeforeText, failed.AfterText)
	}
}

// b4ReadTemplateByID reads the template row by primary key. The rule-frozen
// assertion reads by id rather than by name so that a rename or an extra row
// cannot hide behind a name lookup, and so that the compared row is provably the
// one created by this test.
func b4ReadTemplateByID(t *testing.T, h *Harness, id uid.ID) (b2TemplateRow, bool) {
	t.Helper()
	var rows []b2TemplateRow
	if err := h.DB.Raw(
		`SELECT id, name, threshold_points, discount_points, total_count, per_user_limit,
		        received_count, valid_from, valid_until, status
		 FROM coupon_templates WHERE id = ?`, id,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("query coupon_templates by id %s: %v", id, err)
	}
	if len(rows) > 1 {
		t.Fatalf("coupon_templates rows with id %s = %d, want at most 1", id, len(rows))
	}
	if len(rows) == 0 {
		return b2TemplateRow{}, false
	}
	return rows[0], true
}

// b4AssertRuleFieldsFrozen compares the seven rule columns the create payload
// sets — name, threshold_points, discount_points, total_count, per_user_limit,
// valid_from, valid_until — plus the service-maintained received_count against
// the snapshot taken before. Status is deliberately left out of this helper: a
// request may legitimately flip it (spec Scenario「规则创建后不可修改」的
// 「仅发放状态变更生效」), and the status transition itself is B5/B6/B7's
// behavior. Every column is a value comparison against the snapshot, never a
// "no error happened" substitute.
func b4AssertRuleFieldsFrozen(t *testing.T, desc string, want, got b2TemplateRow) {
	t.Helper()
	if got.ID != want.ID {
		t.Fatalf("%s：模板 id 由 %s 变为 %s（行被替换了）", desc, want.ID, got.ID)
	}
	if got.Name != want.Name {
		t.Fatalf("%s：规则列 name 由 %q 变为 %q", desc, want.Name, got.Name)
	}
	if got.ThresholdPoints != want.ThresholdPoints {
		t.Fatalf("%s：规则列 threshold_points 由 %d 变为 %d —— 修改请求的规则字段被采信了", desc, want.ThresholdPoints, got.ThresholdPoints)
	}
	if got.DiscountPoints != want.DiscountPoints {
		t.Fatalf("%s：规则列 discount_points 由 %d 变为 %d —— 修改请求的规则字段被采信了", desc, want.DiscountPoints, got.DiscountPoints)
	}
	if got.TotalCount != want.TotalCount {
		t.Fatalf("%s：规则列 total_count 由 %d 变为 %d —— 修改请求的规则字段被采信了", desc, want.TotalCount, got.TotalCount)
	}
	if got.PerUserLimit != want.PerUserLimit {
		t.Fatalf("%s：规则列 per_user_limit 由 %d 变为 %d —— 修改请求的规则字段被采信了", desc, want.PerUserLimit, got.PerUserLimit)
	}
	if !got.ValidFrom.UTC().Equal(want.ValidFrom.UTC()) {
		t.Fatalf("%s：规则列 valid_from 由 %s 变为 %s —— 修改请求的规则字段被采信了", desc, want.ValidFrom.UTC(), got.ValidFrom.UTC())
	}
	if !got.ValidUntil.UTC().Equal(want.ValidUntil.UTC()) {
		t.Fatalf("%s：规则列 valid_until 由 %s 变为 %s —— 修改请求的规则字段被采信了", desc, want.ValidUntil.UTC(), got.ValidUntil.UTC())
	}
	if got.ReceivedCount != want.ReceivedCount {
		t.Fatalf("%s：received_count 由 %d 变为 %d（服务维护的发放计数被修改请求动了）", desc, want.ReceivedCount, got.ReceivedCount)
	}
}

// contract: B4
//
// 对已有模板提交规则字段（门槛、抵扣、总量、限领、有效期）的修改请求：请求中仅发放
// 状态变更生效，规则字段一概不被采信。
//
// spec Scenario「规则创建后不可修改」的承重断言是"规则字段一概不被采信"与"不产生
// 第二行"，本用例用两轮请求覆盖它：
//
//	轮 1（带发放状态字段）：一次请求同时改动全部六个规则字段并携带 status 变更。
//	  这是 Scenario 的 WHEN 原样——"仅发放状态变更生效"以状态字段在场为前提，规则
//	  字段随之一同提交才构成"只生效状态、不生效规则"的可观测对照。
//	轮 2（仅规则字段、不带状态字段）：只提交规则字段，规则字段同样不被采信。该请求
//	  是否报错、报什么，spec 只写"不被采信"，techspec 07 §5 也只写该端点"仅切换
//	  status"，两处都没有钉状态码，因此轮 2 把实测状态码写进日志、不当通过条件；
//	  承重断言仍是"规则字段不变 + 无新增行"。
//
// 四层断言覆盖说明：
//  1. 端到端响应：两轮都走真实管理域 PATCH /api/admin/v1/coupon-templates/{id}
//     （Cookie JWT + coupon:write + CSRF）。轮 1 钉住请求被当作一次状态变更请求受理
//     （2xx + envelope code=0）：若 PATCH 未实现，这里是 404，规则字段不变只是"请求
//     根本没被受理"的假绿，所以轮 1 必须断言受理结果本身。
//  2. 参数 edge case：本 Scenario 的输入维度是"改了哪些字段"，不是单个字段的取值范围
//     这里提交的六个规则新值全部自身合法（正整数、抵扣仍小于门槛、有效期仍为正区间），
//     否则实现可以辩解"改后的规则自身非法所以没被采信"；取值域的非法样本由 B2 逐条
//     覆盖，此处不重复。轮 2 覆盖"缺 status 字段"这一入参形态。
//  3. 外部服务调用参数：模板链路不触达任何第三方 HTTP 服务（无图片上传、无微信调用、
//     无支付网关），无可 mock 的外部调用，本层缺省（同 B1/B2/B3）。
//  4. 数据库字段变动：绕过响应体直查 coupon_templates——按模板 id 取回整行，逐个断言
//     name、threshold_points、discount_points、total_count、per_user_limit、
//     valid_from、valid_until 七个规则列与 received_count 等于创建时的快照（并与本轮
//     请求前的快照再比一次）；再按创建时的名称与全表计数断言没有产生第二行。
func TestSpecCouponB4_RuleFieldsFrozenAfterCreation(t *testing.T) {
	h := NewHarness(t, testDB)

	// 创建时的规则快照：有效期窗口覆盖当前时刻，模板处于可发放状态。这套值全程作为
	// 「创建即冻结」的参照。
	createFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	createUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	name := fmt.Sprintf("E2E券模板B4-%d", time.Now().UnixNano())

	resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates",
		b2TemplateBody(name, createFrom, createUntil), nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("前置合法模板创建失败 status=%d body=%s（B1 应已交付；这里失败说明前置不成立）", resp.StatusCode, raw)
	}
	if env, _ := h.Decode(resp); env.Code != 0 {
		t.Fatalf("前置合法模板创建失败 envelope code=%d, want 0", env.Code)
	}
	created, ok := b2ReadTemplate(t, h, name)
	if !ok {
		t.Fatalf("前置合法模板未落库：coupon_templates 中无 name=%q 的行", name)
	}
	baselineTotal := b2CountTemplates(t, h)
	if baselineTotal != 1 {
		t.Fatalf("前置合法模板落库后 coupon_templates 行数 = %d, want 1（新库只应有这一行）", baselineTotal)
	}

	// 修改请求要改动的全部规则字段：每个都换成与创建值不同、但自身合法的值。
	changedFrom := createFrom.Add(-time.Hour)
	changedUntil := createUntil.Add(48 * time.Hour)
	patchBody := func(withStatus bool) map[string]any {
		body := map[string]any{
			"threshold_points": 900,
			"discount_points":  300,
			"total_count":      50,
			"per_user_limit":   5,
			"valid_from":       changedFrom.Format(time.RFC3339),
			"valid_until":      changedUntil.Format(time.RFC3339),
		}
		if withStatus {
			// 状态字段在场：请求是一次"合法"的发放状态变更，规则字段夹带在其中。
			body["status"] = "halted"
		}
		return body
	}

	rounds := []struct {
		desc string
		// withStatus 决定本轮是否携带发放状态字段。
		withStatus bool
		// assertAccepted 决定本轮是否断言请求被受理。只有轮 1 断言：轮 1 若被拒，
		// 规则字段不变就没有证明力。
		assertAccepted bool
	}{
		{"带发放状态字段", true, true},
		{"仅规则字段", false, false},
	}

	for _, round := range rounds {
		round := round
		t.Run(round.desc, func(t *testing.T) {
			// 本轮请求前的快照：行的存在性与状态留痕的对比基准。
			before, exists := b4ReadTemplateByID(t, h, created.ID)
			if !exists {
				t.Fatalf("%s：修改请求前，模板 id=%s 行不存在", round.desc, created.ID)
			}

			// WHEN 对已有模板提交规则字段的修改请求。
			resp := h.AdminDo(h.SuperAdmin, http.MethodPatch,
				"/api/admin/v1/coupon-templates/"+created.ID.String(), patchBody(round.withStatus), nil)
			raw, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				t.Fatalf("%s：读 PATCH 响应失败：%v", round.desc, readErr)
			}

			if round.assertAccepted {
				// THEN 该请求被当作一次发放状态变更请求受理。规则字段"不被采信"的前提
				// 是请求真的到了这个端点并被处理：端点缺失（404）时规则字段当然没变，
				// 那不是本 Scenario 的行为。
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					t.Fatalf("%s：PATCH /api/admin/v1/coupon-templates/%s status=%d body=%s, want 2xx（PATCH 端点未实现时这里是 404；规则字段不变只有在该请求被受理的前提下才算「不被采信」）",
						round.desc, created.ID, resp.StatusCode, raw)
				}
				if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
					var env envelope
					if err := json.Unmarshal(raw, &env); err != nil {
						t.Fatalf("%s：PATCH 响应不是业务 envelope（body=%s），want code=0", round.desc, trimmed)
					}
					h.AssertTraceID(resp, env)
					if env.Code != 0 {
						t.Fatalf("%s：PATCH envelope code = %d body=%s, want 0（受理发放状态变更）", round.desc, env.Code, trimmed)
					}
				}
			}
			// 轮 2 的实测留痕：状态码不作通过条件（spec 只说"不被采信"，techspec 07 §5
			// 只把该端点定义为 status 切换）。
			t.Logf("%s：PATCH status=%d body=%s", round.desc, resp.StatusCode, raw)

			// THEN 规则字段一概不被采信：按 id 取回该模板整行，逐列比对。
			after, exists := b4ReadTemplateByID(t, h, created.ID)
			if !exists {
				t.Fatalf("%s：修改请求后，模板 id=%s 行消失（被改名或删行）", round.desc, created.ID)
			}
			b4AssertRuleFieldsFrozen(t, round.desc+"（相对本次请求前）", before, after)
			b4AssertRuleFieldsFrozen(t, round.desc+"（相对创建时快照，创建即冻结）", created, after)
			t.Logf("%s：模板 %s 请求前 status=%q，请求后 status=%q（状态是否切换属 B5/B6/B7，本用例不断言）",
				round.desc, created.ID, before.Status, after.Status)

			// THEN 没有产生第二行：按创建时的名称仍恰一行，全表计数不增。
			var namedRows int64
			if err := h.DB.Raw(
				`SELECT count(*) FROM coupon_templates WHERE name = ?`, name,
			).Scan(&namedRows).Error; err != nil {
				t.Fatalf("%s：按名称计数 coupon_templates 失败：%v", round.desc, err)
			}
			if namedRows != 1 {
				t.Fatalf("%s：name=%q 的模板行数 = %d, want 1（修改请求不得改名、不得新增行）", round.desc, name, namedRows)
			}
			if got := b2CountTemplates(t, h); got != baselineTotal {
				t.Fatalf("%s：修改请求后 coupon_templates 行数 = %d, want %d（无新增行）", round.desc, got, baselineTotal)
			}
		})
	}
}
