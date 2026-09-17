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
//   B7 TestSpecCouponB7_StatusSwitchWritesSuccessAuditWithActorAndBothStatuses (this file)
//   B8 TestSpecCouponB8_ListReturnsAllTemplatesWithRulesReceivedAndUsedCounts (this file)
//   B9 TestSpecCouponB9_ListWithoutReadPermissionForbidden (this file)
//   B5/B6 停发与恢复发放：断言对象是领券中心与领取端点（尚未实现），已挪到后续切片，
//         不落在本文件。B7 只覆盖「切换成功后的审计留痕」，不管停发/恢复对领券中心
//         可见性的影响。
//   B8-B24 续写于本文件，按 B 编号分节。

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

// b7AuditRow is one audit_logs row read back for the status-switch assertion:
// identity, action, actor, result and the raw before/after payloads. The
// payloads are read through ::text so the test reads the stored JSON, not a
// driver-level decoding of it.
type b7AuditRow struct {
	ID           string `gorm:"column:id"`
	Action       string `gorm:"column:action"`
	Result       string `gorm:"column:result"`
	ActorAdminID string `gorm:"column:actor_admin_id"`
	BeforeText   string `gorm:"column:before_text"`
	AfterText    string `gorm:"column:after_text"`
}

// b7ReadTemplateSuccessAudits returns every result='success' audit row tied to
// the template, oldest first. A row counts as tied to the template when its
// target_id is that template (the link the creation audit already uses,
// Service.CreateTemplate) or when one of its payloads carries the template id:
// the contract does not freeze which field links a status-switch record to its
// template, so both shapes satisfy "this row is about this template".
func b7ReadTemplateSuccessAudits(t *testing.T, h *Harness, templateID uid.ID) []b7AuditRow {
	t.Helper()
	var rows []b7AuditRow
	if err := h.DB.Raw(
		`SELECT id::text AS id,
		        action,
		        result,
		        COALESCE(actor_admin_id::text, '') AS actor_admin_id,
		        COALESCE(before_data::text, '') AS before_text,
		        COALESCE(after_data::text, '') AS after_text
		 FROM audit_logs
		 WHERE result = 'success'
		   AND (target_id = ? OR before_data::text LIKE ? OR after_data::text LIKE ?)
		 ORDER BY created_at, id`,
		templateID, "%"+templateID.String()+"%", "%"+templateID.String()+"%",
	).Scan(&rows).Error; err != nil {
		t.Fatalf("query success audit rows of coupon template %s: %v", templateID, err)
	}
	return rows
}

// b7NewAudits returns the rows in after that are absent from before, keyed by
// the audit row id. This id delta is how the switch record is told apart from
// the creation record: the two share target_id (and the template id inside
// their payloads), so row identity — not the template link — is the
// discriminator.
func b7NewAudits(before, after []b7AuditRow) []b7AuditRow {
	seen := make(map[string]struct{}, len(before))
	for _, row := range before {
		seen[row.ID] = struct{}{}
	}
	var fresh []b7AuditRow
	for _, row := range after {
		if _, ok := seen[row.ID]; !ok {
			fresh = append(fresh, row)
		}
	}
	return fresh
}

// b7PayloadStrings flattens every string value inside a stored JSON payload, at
// any depth, so "which key holds the status" never enters the assertion: the
// contract only requires that both statuses can be recovered from the row.
func b7PayloadStrings(t *testing.T, raw string) []string {
	t.Helper()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		t.Fatalf("审计载荷不是 JSON：%q：%v", trimmed, err)
	}
	var values []string
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case string:
			values = append(values, typed)
		case map[string]any:
			for _, nested := range typed {
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(decoded)
	return values
}

// b7HasString reports whether the flattened payload carries want as a whole
// value. The status vocabulary is the lowercase active / halted the
// coupon_templates CHECK constraint freezes, so an exact comparison on the
// whole string is the right resolution.
func b7HasString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

// b7AdminID resolves the stored id of one seeded administrator.
func b7AdminID(t *testing.T, h *Harness, username string) string {
	t.Helper()
	var id string
	if err := h.DB.Raw(`SELECT id::text FROM admin_users WHERE username = ?`, username).Scan(&id).Error; err != nil {
		t.Fatalf("read admin id of %s: %v", username, err)
	}
	if id == "" {
		t.Fatalf("管理员 %s 未落库", username)
	}
	return id
}

// contract: B7
//
// 任一发放状态切换成功：一条成功审计记录操作人与切换前后状态。
//
// spec Scenario「切换留痕」的 WHEN 是"任一发放状态切换成功"，两个方向
// （active → halted、halted → active）各起一个子用例，各自新建模板，方向二先用
// 一次真实切换把新模板置到 halted 再切回。
//
// 切换审计与创建审计的区分（本用例最容易假绿的地方）：创建与切换都指向同一个模板，
// target_id / 载荷里的模板 id 完全相同，因此区分落在两处互相独立的证据上——
//  1. 行 identity 的增量：切换前先把该模板的全部成功审计行取成快照（B1 已证明创建
//     后恰有一条），切换后要求新增行恰有一条。创建审计行在快照里，无法冒充新增行。
//  2. 操作人：创建由 super_admin 执行、切换由 operator 执行，两个管理员 id 不同。
//     若实现把创建留痕当切换留痕回放，actor_admin_id 这一项就对不上。
//
// 另有天然区分：创建留痕没有 before 影像（audit.Write 的 BeforeData 为空时落 NULL），
// 因此「能恢复出切换前状态」这一条创建行本就无法满足。
//
// 四层断言覆盖说明：
//  1. 端到端响应：切换走真实管理域 PATCH /api/admin/v1/coupon-templates/{id}
//     （Cookie JWT + coupon:write + CSRF），断言 2xx 且 envelope code=0。切换必须
//     先被受理：「没有审计行」只有在请求真的到达并被处理时才是 B7 缺口，而不是
//     404 假绿（B4 轮 1 已用同一条承重断言证明该端点被受理）。
//  2. 参数 edge case：本 Scenario 的输入维度是"切换方向"，不是单个字段取值域。
//     请求体只带 status，取值为模板列允许的两个发放状态之一；非法状态值与非法模板
//     id 属于 B4/B5/B6 的入参范围，此处不重复。
//  3. 外部服务调用参数：切换链路不触达任何第三方 HTTP 服务（无图片上传、无微信
//     调用、无支付网关），无可 mock 的外部调用，本层缺省（同 B1/B2/B3/B4）。
//  4. 数据库字段变动：绕过响应体直查两处——coupon_templates 断言模板 status 确实
//     落到切换后的值（切换真的发生了）；audit_logs 断言该模板的成功审计行恰新增
//     一条，其 result='success'、actor_admin_id 是本次操作的管理员，且从
//     before_data / after_data 里能同时恢复出切换前与切换后状态。
func TestSpecCouponB7_StatusSwitchWritesSuccessAuditWithActorAndBothStatuses(t *testing.T) {
	h := NewHarness(t, testDB)

	// 创建者与切换者刻意用两个不同的管理员：两个 id 都非空且互不相同，操作人才能
	// 作为独立于「行数增量」的判别证据。
	creatorID := b7AdminID(t, h, superUsername)
	switcherID := b7AdminID(t, h, operatorUsername)
	if creatorID == switcherID {
		t.Fatalf("前置不成立：创建者与切换者是同一个管理员 %s，无法用操作人区分创建留痕与切换留痕", creatorID)
	}

	// 前置自证：切换者持有 coupon:write（0006 把 coupon:write 授予 super_admin 与
	// operator，PATCH 路由的闸门也是 coupon:write）。否则切换会被 403 拦下，
	// 「没有审计行」就归因不到 B7 缺口，而是 fixture 事故。
	var switcherPermissions []string
	if err := h.DB.Raw(
		`SELECT p.code FROM role_permissions rp
		 JOIN permissions p ON p.id = rp.permission_id
		 JOIN roles r ON r.id = rp.role_id
		 JOIN admin_users a ON a.role_id = r.id
		 WHERE a.username = ?`, operatorUsername,
	).Scan(&switcherPermissions).Error; err != nil {
		t.Fatalf("read permissions of %s: %v", operatorUsername, err)
	}
	if !b7HasString(switcherPermissions, "coupon:write") {
		t.Fatalf("前置不成立：切换者 %s 的权限集合 %v 不含 coupon:write", operatorUsername, switcherPermissions)
	}

	directions := []struct {
		desc string
		from string
		to   string
	}{
		{"发放中切停发", "active", "halted"},
		{"停发切回发放中", "halted", "active"},
	}

	for _, dir := range directions {
		dir := dir
		// contract: B7
		t.Run(dir.desc, func(t *testing.T) {
			validFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			validUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
			name := fmt.Sprintf("E2E券模板B7-%s-%d", dir.to, time.Now().UnixNano())

			// 每个方向各建一个模板：创建或切换都由真实接口完成，创建留痕因此一定
			// 在场，区分逻辑的难度与线上一致。
			resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates",
				b2TemplateBody(name, validFrom, validUntil), nil)
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				raw, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				t.Fatalf("%s：前置合法模板创建失败 status=%d body=%s（B1 应已交付；这里失败说明前置不成立）",
					dir.desc, resp.StatusCode, raw)
			}
			if env, _ := h.Decode(resp); env.Code != 0 {
				t.Fatalf("%s：前置合法模板创建失败 envelope code=%d, want 0", dir.desc, env.Code)
			}
			created, ok := b2ReadTemplate(t, h, name)
			if !ok {
				t.Fatalf("%s：前置合法模板未落库：coupon_templates 中无 name=%q 的行", dir.desc, name)
			}
			if created.Status != "active" {
				t.Fatalf("%s：前置不成立，新建模板 status = %q, want active", dir.desc, created.Status)
			}

			// 切换前的成功审计快照：本方向的新增行判定基准。B1 已断言「创建后恰一条
			// 成功审计」，这里再确认一次，并确认那条是创建者的留痕——两条前置都成立，
			// 后面的「恰一条新增」才真的在区分创建与切换。
			snapshot := b7ReadTemplateSuccessAudits(t, h, created.ID)
			if len(snapshot) != 1 {
				t.Fatalf("%s：前置不成立，创建后模板 %s 的成功审计行数 = %d, want 恰好 1（创建留痕）；rows=%+v",
					dir.desc, created.ID, len(snapshot), snapshot)
			}
			if snapshot[0].ActorAdminID != creatorID {
				t.Fatalf("%s：前置不成立，创建留痕的 actor_admin_id = %q, want 创建者 %s", dir.desc, snapshot[0].ActorAdminID, creatorID)
			}

			// 一次真实的状态切换（由持 coupon:write 的 operator 发起），返回状态码、
			// envelope 与原始 body 供断言。
			patchStatus := func(status string) (int, envelope, []byte) {
				r := h.AdminDo(h.Operator, http.MethodPatch,
					"/api/admin/v1/coupon-templates/"+created.ID.String(), map[string]any{"status": status}, nil)
				env, raw := h.Decode(r)
				h.AssertTraceID(r, env)
				return r.StatusCode, env, raw
			}

			// 到达本方向的起点状态：方向二从 halted 起步，先做一次真实切换把模板置为
			// halted。这次切换自身的留痕由「发放中切停发」子用例负责断言（同一契约
			// 条目覆盖两个方向），此处只把它并入快照，使本方向的新增行判定相对它成立。
			if dir.from == "halted" {
				code, env, raw := patchStatus("halted")
				if code < 200 || code >= 300 || env.Code != 0 {
					t.Fatalf("%s：前置切换 active→halted 未被受理 status=%d code=%d body=%s, want 2xx / code 0",
						dir.desc, code, env.Code, raw)
				}
				current, exists := b4ReadTemplateByID(t, h, created.ID)
				if !exists {
					t.Fatalf("%s：前置切换后模板 %s 行消失", dir.desc, created.ID)
				}
				if current.Status != "halted" {
					t.Fatalf("%s：前置切换后模板 status = %q, want halted（起点状态不成立）", dir.desc, current.Status)
				}
				snapshot = b7ReadTemplateSuccessAudits(t, h, created.ID)
			}

			// WHEN 管理员把模板从 dir.from 切到 dir.to。
			status, env, raw := patchStatus(dir.to)

			// THEN 请求被受理：2xx 且 envelope code=0。切换失败（404/403/400/500）
			// 时「没有审计行」不构成 B7 证据，这条前置断言把那种假绿挡在外面。
			if status < 200 || status >= 300 || env.Code != 0 {
				t.Fatalf("%s：PATCH /api/admin/v1/coupon-templates/%s status=%d code=%d body=%s, want 2xx / code 0",
					dir.desc, created.ID, status, env.Code, raw)
			}

			// THEN 切换成功：模板确实落到新状态。留痕的前提是这次切换真的发生了。
			switched, exists := b4ReadTemplateByID(t, h, created.ID)
			if !exists {
				t.Fatalf("%s：切换后模板 %s 行消失", dir.desc, created.ID)
			}
			if switched.Status != dir.to {
				t.Fatalf("%s：切换后模板 status = %q, want %q", dir.desc, switched.Status, dir.to)
			}

			// THEN 恰好一条成功审计记录本次切换。创建审计与切换审计指向同一个模板，
			// 用行 id 集合的增量区分二者：切换前快照里已有创建行（方向二还含上一次
			// 切换行），新增行恰有一条才叫「一条」。
			all := b7ReadTemplateSuccessAudits(t, h, created.ID)
			fresh := b7NewAudits(snapshot, all)
			if len(fresh) != 1 {
				t.Fatalf("%s：切换 %s→%s 后该模板新增成功审计行数 = %d, want 恰好 1（切换前 %d 行、切换后 %d 行；契约 B7 要求切换成功留下恰一条成功留痕，交付前此处为 0）rows=%+v",
					dir.desc, dir.from, dir.to, len(fresh), len(snapshot), len(all), all)
			}
			switchRow := fresh[0]
			if switchRow.Result != "success" {
				t.Fatalf("%s：切换审计 result = %q, want success（action=%q）", dir.desc, switchRow.Result, switchRow.Action)
			}

			// THEN 记录操作人：本次执行切换的管理员，而不是创建者，也不是空。
			if switchRow.ActorAdminID != switcherID {
				t.Fatalf("%s：切换审计 actor_admin_id = %q, want 本次切换的管理员 %s（创建者是 %s，action=%q）",
					dir.desc, switchRow.ActorAdminID, switcherID, creatorID, switchRow.Action)
			}

			// THEN 记录切换前后状态：这一行必须同时能恢复出切换前与切换后状态。字段名
			// 未冻结（techspec 07 §3 只写了"券模板发放状态切换"这个动作族），因此不看
			// 键名，只看这一行的载荷里有没有这两个状态值；方向性按本仓既有约定钉住：
			// 结果态落在 after_data（product.update 的 BeforeData/AfterData 双快照、
			// refund.approve / points.adjust 的 AfterData 结果态同此惯例）。
			beforeValues := b7PayloadStrings(t, switchRow.BeforeText)
			afterValues := b7PayloadStrings(t, switchRow.AfterText)
			if !b7HasString(beforeValues, dir.from) && !b7HasString(afterValues, dir.from) {
				t.Fatalf("%s：切换审计未能恢复切换前状态 %q：before_data=%s after_data=%s",
					dir.desc, dir.from, switchRow.BeforeText, switchRow.AfterText)
			}
			if !b7HasString(afterValues, dir.to) {
				t.Fatalf("%s：切换审计未把切换后状态 %q 记入 after_data（本仓变更类成功审计的结果态约定）：before_data=%s after_data=%s",
					dir.desc, dir.to, switchRow.BeforeText, switchRow.AfterText)
			}
			t.Logf("%s：切换 %s→%s 的审计行 action=%q actor=%s before_data=%s after_data=%s",
				dir.desc, dir.from, dir.to, switchRow.Action, switchRow.ActorAdminID, switchRow.BeforeText, switchRow.AfterText)
		})
	}
}

// b8PageSize is the page size this case requests. Three templates span two
// pages, so a full page followed by a short last page makes the paging behavior
// observable.
const b8PageSize = 2

// b8RuleFieldKeys is the set of row keys asserted field by field before the
// redeemed-count column is identified: identity, the seven rule fields, the
// issuance counter and the issuance status. Excluding them stops a rule value
// that happens to equal the used-coupon count from being mistaken for the
// redeemed count.
var b8RuleFieldKeys = map[string]struct{}{
	"id":               {},
	"name":             {},
	"threshold_points": {},
	"discount_points":  {},
	"total_count":      {},
	"per_user_limit":   {},
	"received_count":   {},
	"valid_from":       {},
	"valid_until":      {},
	"status":           {},
}

// b8TemplateSpec is one template fixture: the rule set, the validity window and
// the issuance status the template must end up in. The rule values differ from
// template to template and none of them equals this case's redeemed counts
// (2 and 0), so identifying that column by value stays unambiguous.
type b8TemplateSpec struct {
	name            string
	thresholdPoints int64
	discountPoints  int64
	totalCount      int32
	perUserLimit    int32
	validFrom       time.Time
	validUntil      time.Time
	status          string
}

// b8Number reads one numeric field of a decoded template row. A missing key or a
// non-numeric value is a failure, never a zero.
func b8Number(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	value, ok := row[key]
	if !ok {
		t.Fatalf("模板列表行缺字段 %q：%v", key, row)
	}
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("模板列表行字段 %q 是 %T(%v), want JSON 数字：%v", key, value, value, row)
	}
	return number
}

// b8Time reads one timestamp field of a decoded template row. time.Parse accepts
// a fractional second even though the layout omits it.
func b8Time(t *testing.T, row map[string]any, key string) time.Time {
	t.Helper()
	raw := readString(t, row, key)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("模板列表行字段 %q = %q, want RFC 3339 时间：%v", key, raw, err)
	}
	return parsed
}

// b8Identity locates one row of the list: the canonical id when the row carries
// one, the template name otherwise. The list endpoint's field names are not in
// docs/api/openapi.yaml (contract FU-0a8e7f3b), so neither key is the only way
// in; a row carrying neither cannot be located and fails.
func b8Identity(t *testing.T, row map[string]any) string {
	t.Helper()
	if id, ok := row["id"].(string); ok && id != "" {
		return id
	}
	if name, ok := row["name"].(string); ok && name != "" {
		return name
	}
	t.Fatalf("模板列表行既无 id 也无 name，无法定位该行：%v", row)
	return ""
}

// b8FindUsedCountKey identifies which column of the list rows carries the
// redeemed-coupon count (已核销数).
//
// The spec's THEN writes only "已核销数（该模板下状态为 used 的券的计数）" and
// techspec 07 §5 only "按 user_coupons.status = 'used' 聚合派生": neither freezes
// the response field name (coupon paths never enter docs/api/openapi.yaml, see
// contract FU-0a8e7f3b), and the repo has no precedent for this column. So the
// key is identified by an observable shape instead of a name: one and the same
// key must equal 2 on the issuing template (two used coupons out of four) and 0
// on the halted one (no coupons at all), and must not be one of the rule fields
// already asserted by name. That criterion rejects both implementation
// deviations this Scenario can take — no such column at all (nothing matches),
// and "count every coupon as redeemed" (the issuing template would report 4, not
// 2).
func b8FindUsedCountKey(t *testing.T, rowIssuing, rowHalted map[string]any, wantIssuing, wantHalted float64) string {
	t.Helper()
	var matches []string
	for key, rawIssuing := range rowIssuing {
		if _, isRuleField := b8RuleFieldKeys[key]; isRuleField {
			continue
		}
		issuing, ok := rawIssuing.(float64)
		if !ok || issuing != wantIssuing {
			continue
		}
		halted, ok := rowHalted[key].(float64)
		if !ok || halted != wantHalted {
			continue
		}
		matches = append(matches, key)
	}
	if len(matches) == 0 {
		t.Fatalf("模板列表里找不到「已核销数」这一列：需要一个非规则字段，在发放中模板上等于 %v（该模板下 used 券数）、在停发模板上等于 %v；"+
			"若实现按券总数计数，发放中模板会得到 4 而不是 %v。发放中模板行=%v，停发模板行=%v",
			wantIssuing, wantHalted, wantIssuing, rowIssuing, rowHalted)
	}
	t.Logf("已核销数落在字段 %v 上（字段名未冻结，按 发放中模板=%v / 停发模板=%v 的值口径识别）", matches, wantIssuing, wantHalted)
	return matches[0]
}

// contract: B8
//
// 持 coupon:read 的管理员 GET /api/admin/v1/coupon-templates：分页返回含 halted
// 在内的全部券模板，每条带规则字段、已领取数、已核销数（该模板下状态为 used 的
// 券的计数）。
//
// 数据构造：
//   - 3 条模板全部经真实创建路径落库（B1 已交付），其中一条再经真实 PATCH 切到
//     halted（B7 已交付）——「含 halted 在内」这条断言的对象因此是真实状态切换的
//     产物，不是直接改库的快照。
//   - 「已核销数」的种子直接写 user_coupons：领取端点是 B12 的账，本切片没有任何
//     代码写这张表，非零的 used 计数别无来源。这是 fixture 搭建，不是对领取行为的
//     验证。
//   - 发放中模板的 received_count 也直接改库抬到 5（该列由服务维护，条件扣减同样
//     在 B12），刻意不等于该模板的券行数 4、也不等于已核销数 2，使「列表读的是
//     received_count 这一列本身」成为可判别的断言。
//
// 四层断言覆盖说明：
//  1. 端到端响应：GET /api/admin/v1/coupon-templates?page=1&page_size=2 与
//     ?page=2&page_size=2 走真实管理域路由（Cookie JWT + coupon:read + CSRF），
//     断言 HTTP 200、envelope code=0 与 trace_id，并逐条断言响应行内容。分页口径
//     照抄本仓既有管理端列表端点：请求参数 page / page_size（定义见
//     internal/product/handler.go parsePaging），响应 data 为
//     { list, total, page, page_size }（同文件的 productListData）。
//  2. 参数 edge case：本 Scenario 的入参维度是分页参数与「哪些模板在列表里」。
//     分页覆盖跨页（整页 2 条 + 末页余数 1 条，total 不随页码变化、两页不重不漏）；
//     模板取值域覆盖两种发放状态（active 与 halted 各一条都在列表里）。非法
//     page / page_size 归全局分页契约（与 product/order 端点同一口径），且属
//     5001/1001 的错误面而非本 Scenario 的 THEN，此处不重复。
//  3. 外部服务调用参数：列表读取不触达任何第三方 HTTP 服务（无图片上传、无微信
//     调用、无支付网关），无可 mock 的外部调用，本层缺省（同 B1/B2/B3/B4/B7）。
//  4. 数据库字段变动：读路径不写库。数据库侧直查 coupon_templates 与 user_coupons
//     作为断言基准（fixture 落库自证：received_count=5、used=2 / available=1 /
//     held=1），再把列表每行的规则字段、发放状态、已领取数与已核销数逐项对它比对。
func TestSpecCouponB8_ListReturnsAllTemplatesWithRulesReceivedAndUsedCounts(t *testing.T) {
	h := NewHarness(t, testDB)

	validFrom := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	validUntil := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	suffix := time.Now().UnixNano()

	// 三条模板：一条发放中（承载券种子与已领取数 fixture）、一条停发（证明 halted
	// 也在列表里）、一条只为跨页存在。规则值与发放状态两两不同。
	specs := []b8TemplateSpec{
		{
			name:            fmt.Sprintf("E2E券模板B8发放中-%d", suffix),
			thresholdPoints: 500, discountPoints: 100, totalCount: 20, perUserLimit: 7,
			validFrom: validFrom, validUntil: validUntil, status: "active",
		},
		{
			name:            fmt.Sprintf("E2E券模板B8停发-%d", suffix),
			thresholdPoints: 800, discountPoints: 200, totalCount: 50, perUserLimit: 3,
			validFrom: validFrom, validUntil: validUntil, status: "halted",
		},
		{
			name:            fmt.Sprintf("E2E券模板B8分页-%d", suffix),
			thresholdPoints: 900, discountPoints: 300, totalCount: 30, perUserLimit: 9,
			validFrom: validFrom, validUntil: validUntil, status: "active",
		},
	}

	// create 经真实创建路径落库，并在 spec 要求停发时经真实 PATCH 切换（两个端点
	// B1 / B7 都已交付；这里失败说明前置不成立，而不是 B8 的缺口）。
	create := func(spec b8TemplateSpec) b2TemplateRow {
		t.Helper()
		resp := h.AdminDo(h.SuperAdmin, http.MethodPost, "/api/admin/v1/coupon-templates",
			map[string]any{
				"name":             spec.name,
				"threshold_points": spec.thresholdPoints,
				"discount_points":  spec.discountPoints,
				"total_count":      spec.totalCount,
				"per_user_limit":   spec.perUserLimit,
				"valid_from":       spec.validFrom.Format(time.RFC3339),
				"valid_until":      spec.validUntil.Format(time.RFC3339),
			}, nil)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("前置：创建模板 %q status=%d body=%s, want 201 或 200（B1 应已交付）", spec.name, resp.StatusCode, raw)
		}
		if env, _ := h.Decode(resp); env.Code != 0 {
			t.Fatalf("前置：创建模板 %q envelope code=%d, want 0", spec.name, env.Code)
		}
		row, ok := b2ReadTemplate(t, h, spec.name)
		if !ok {
			t.Fatalf("前置：模板 %q 未落库", spec.name)
		}
		if row.Status != "active" || row.ReceivedCount != 0 {
			t.Fatalf("前置不成立：新建模板 %q status=%q received_count=%d, want active / 0", spec.name, row.Status, row.ReceivedCount)
		}
		if spec.status == "halted" {
			presp := h.AdminDo(h.SuperAdmin, http.MethodPatch,
				"/api/admin/v1/coupon-templates/"+row.ID.String(), map[string]any{"status": "halted"}, nil)
			penv, praw := h.Decode(presp)
			h.AssertTraceID(presp, penv)
			if presp.StatusCode < 200 || presp.StatusCode >= 300 || penv.Code != 0 {
				t.Fatalf("前置：把模板 %q 切到 halted 未被受理 status=%d code=%d body=%s（B7 应已交付该端点）",
					spec.name, presp.StatusCode, penv.Code, praw)
			}
			row, ok = b2ReadTemplate(t, h, spec.name)
			if !ok || row.Status != "halted" {
				t.Fatalf("前置不成立：模板 %q 切换后 status=%q, want halted", spec.name, row.Status)
			}
		}
		return row
	}

	created := make([]b2TemplateRow, 0, len(specs))
	for _, spec := range specs {
		created = append(created, create(spec))
	}
	issuing, halted := created[0], created[1]

	// ---- fixture：非零的已核销数只能直接写 user_coupons（非行为验证） ----
	// 领取端点是 B12 的账，本切片无任何代码写这张表。
	userIDText := h.SeedUser(fmt.Sprintf("coupon-b8-openid-%d", suffix), 1000)
	userID, err := uid.ParseCanonical(userIDText)
	if err != nil {
		t.Fatalf("parse seeded user id %q: %v", userIDText, err)
	}
	seedCoupon := func(status string) {
		t.Helper()
		if err := h.DB.Exec(
			`INSERT INTO user_coupons (id, user_id, template_id, status) VALUES (?, ?, ?, ?)`,
			uid.New(), userID, issuing.ID, status,
		).Error; err != nil {
			t.Fatalf("fixture：插入 status=%q 的券行：%v", status, err)
		}
	}
	seedCoupon("used")
	seedCoupon("used")
	// 非 used 的两条券：available 与 held 都不计入已核销数。有它们在，
	// 「把所有券当核销数」的实现会给出 4 而不是 2。
	seedCoupon("available")
	seedCoupon("held")

	// ---- fixture：已领取数由直接改库抬升（非行为验证） ----
	// received_count 是服务维护列（techspec 07 §3 的条件扣减，实现留在 B12）。
	// 5 刻意不等于该模板的券行数 4，也不等于已核销数 2。
	const (
		b8IssuingReceived   = 5
		b8IssuingCouponRows = 4
		b8IssuingUsed       = 2
	)
	if err := h.DB.Exec(
		`UPDATE coupon_templates SET received_count = ? WHERE id = ?`, b8IssuingReceived, issuing.ID,
	).Error; err != nil {
		t.Fatalf("fixture：抬升模板 %s 的 received_count：%v", issuing.ID, err)
	}

	// fixture 落库自证：按 id 回读发放中模板，并按状态统计它的券行——后面的列表
	// 断言以这些库内值为基准。
	issuingRow, exists := b4ReadTemplateByID(t, h, issuing.ID)
	if !exists {
		t.Fatalf("fixture 不成立：模板 %s 行不存在", issuing.ID)
	}
	if issuingRow.ReceivedCount != b8IssuingReceived {
		t.Fatalf("fixture 不成立：模板 %s 的 received_count = %d, want %d", issuing.ID, issuingRow.ReceivedCount, b8IssuingReceived)
	}
	var couponCounts []struct {
		Status string
		Total  int64
	}
	if err := h.DB.Raw(
		`SELECT status, count(*) AS total FROM user_coupons WHERE template_id = ? GROUP BY status ORDER BY status`, issuing.ID,
	).Scan(&couponCounts).Error; err != nil {
		t.Fatalf("fixture：统计模板 %s 的券行：%v", issuing.ID, err)
	}
	byStatus := map[string]int64{}
	for _, count := range couponCounts {
		byStatus[count.Status] = count.Total
	}
	if byStatus["used"] != int64(b8IssuingUsed) || byStatus["available"] != 1 || byStatus["held"] != 1 {
		t.Fatalf("fixture 不成立：模板 %s 的券行按状态计数 = %v, want used=%d available=1 held=1（合计 %d）",
			issuing.ID, byStatus, b8IssuingUsed, b8IssuingCouponRows)
	}
	if got := byStatus["used"] + byStatus["available"] + byStatus["held"]; got != int64(b8IssuingCouponRows) {
		t.Fatalf("fixture 不成立：模板 %s 的券行合计 = %d, want %d", issuing.ID, got, b8IssuingCouponRows)
	}

	// fetchPage 请求第 page 页并断言分页元数据。分页参数名与响应包裹字段名照抄
	// 本仓既有管理端列表端点（internal/product/handler.go：parsePaging 读 page /
	// page_size，productListData 返回 list / total / page / page_size）。
	fetchPage := func(page int) []map[string]any {
		t.Helper()
		path := fmt.Sprintf("/api/admin/v1/coupon-templates?page=%d&page_size=%d", page, b8PageSize)
		resp := h.AdminDo(h.SuperAdmin, http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("GET %s status=%d body=%s, want 200（列表端点未实现时这里是 404）", path, resp.StatusCode, raw)
		}
		env, raw := h.Decode(resp)
		h.AssertTraceID(resp, env)
		if env.Code != 0 {
			t.Fatalf("GET %s envelope code=%d body=%s, want 0", path, env.Code, raw)
		}
		meta := jsonMap(t, env.Data)
		if got := b8Number(t, meta, "total"); got != float64(len(specs)) {
			t.Fatalf("GET %s 的 total=%v, want %d（含 halted 在内的全部模板数）", path, got, len(specs))
		}
		if got := b8Number(t, meta, "page"); got != float64(page) {
			t.Fatalf("GET %s 的 page=%v, want %d", path, got, page)
		}
		if got := b8Number(t, meta, "page_size"); got != float64(b8PageSize) {
			t.Fatalf("GET %s 的 page_size=%v, want %d", path, got, b8PageSize)
		}
		return listData(t, env.Data)
	}

	// WHEN 管理员分页拉取券模板列表（page_size=2，3 条模板恰好整页加末页余数）。
	page1 := fetchPage(1)
	page2 := fetchPage(2)

	// THEN 分页：整页 2 条、末页 1 条，两页合起来不重不漏地覆盖全部 3 条模板。
	if len(page1) != b8PageSize {
		t.Fatalf("第 1 页返回 %d 条, want %d（page_size）", len(page1), b8PageSize)
	}
	if want := len(specs) - b8PageSize; len(page2) != want {
		t.Fatalf("第 2 页返回 %d 条, want %d（末页余数）", len(page2), want)
	}
	allRows := append(append([]map[string]any{}, page1...), page2...)
	identities := map[string]int{}
	for _, row := range allRows {
		identities[b8Identity(t, row)]++
	}
	if len(identities) != len(specs) {
		t.Fatalf("两页合计 %d 行、去重后 %d 条模板身份, want %d（分页不得重复或漏条）：page1=%v page2=%v",
			len(allRows), len(identities), len(specs), page1, page2)
	}

	// index 同时按 id 与 name 建立索引，find 两种定位方式都认。
	index := map[string]map[string]any{}
	for _, row := range allRows {
		if id, ok := row["id"].(string); ok && id != "" {
			index[id] = row
		}
		if name, ok := row["name"].(string); ok && name != "" {
			index[name] = row
		}
	}
	find := func(id uid.ID, name string) map[string]any {
		t.Helper()
		if row, ok := index[id.String()]; ok {
			return row
		}
		if row, ok := index[name]; ok {
			return row
		}
		t.Fatalf("列表两页里都找不到模板 %s（%q）：page1=%v page2=%v", id, name, page1, page2)
		return nil
	}

	// THEN 每条带规则字段：逐条模板断言列表里的值等于库内值。字段名取自同域既有
	// 投影 internal/coupon/handler.go 的 templateJSON（创建与 PATCH 与列表是同一
	// 资源同一形状），received_count 另见 techspec 07 §5 的实名要求。
	for i, spec := range specs {
		dbRow, exists := b4ReadTemplateByID(t, h, created[i].ID)
		if !exists {
			t.Fatalf("模板 %q（%s）在库里消失了", spec.name, created[i].ID)
		}
		listRow := find(dbRow.ID, spec.name)

		if got := readString(t, listRow, "name"); got != dbRow.Name {
			t.Fatalf("模板 %s 的列表 name=%q, want %q", dbRow.ID, got, dbRow.Name)
		}
		if got := b8Number(t, listRow, "threshold_points"); got != float64(dbRow.ThresholdPoints) {
			t.Fatalf("模板 %s 的列表 threshold_points=%v, want %d", dbRow.ID, got, dbRow.ThresholdPoints)
		}
		if got := b8Number(t, listRow, "discount_points"); got != float64(dbRow.DiscountPoints) {
			t.Fatalf("模板 %s 的列表 discount_points=%v, want %d", dbRow.ID, got, dbRow.DiscountPoints)
		}
		if got := b8Number(t, listRow, "total_count"); got != float64(dbRow.TotalCount) {
			t.Fatalf("模板 %s 的列表 total_count=%v, want %d", dbRow.ID, got, dbRow.TotalCount)
		}
		if got := b8Number(t, listRow, "per_user_limit"); got != float64(dbRow.PerUserLimit) {
			t.Fatalf("模板 %s 的列表 per_user_limit=%v, want %d", dbRow.ID, got, dbRow.PerUserLimit)
		}
		if got := b8Time(t, listRow, "valid_from"); !got.UTC().Equal(dbRow.ValidFrom.UTC()) {
			t.Fatalf("模板 %s 的列表 valid_from=%s, want %s", dbRow.ID, got.UTC(), dbRow.ValidFrom.UTC())
		}
		if got := b8Time(t, listRow, "valid_until"); !got.UTC().Equal(dbRow.ValidUntil.UTC()) {
			t.Fatalf("模板 %s 的列表 valid_until=%s, want %s", dbRow.ID, got.UTC(), dbRow.ValidUntil.UTC())
		}
		// 含 halted 在内：每条模板的发放状态按库内状态出现在列表里。
		if got := readString(t, listRow, "status"); got != dbRow.Status {
			t.Fatalf("模板 %s 的列表 status=%q, want %q", dbRow.ID, got, dbRow.Status)
		}
		// 已领取数：读的是 template.received_count 这一列本身（发放中模板由 fixture
		// 抬到 5，停发与分页模板是真实创建路径留下的 0）。
		if got := b8Number(t, listRow, "received_count"); got != float64(dbRow.ReceivedCount) {
			t.Fatalf("模板 %s 的列表 received_count=%v, want %d（库内值）", dbRow.ID, got, dbRow.ReceivedCount)
		}
	}

	// 「含 halted 在内」的显式复核：两种发放状态都在列表里。
	statuses := map[string]int{}
	for _, row := range allRows {
		statuses[readString(t, row, "status")]++
	}
	if statuses["halted"] != 1 || statuses["active"] != len(specs)-1 {
		t.Fatalf("列表里的发放状态分布 = %v, want active=%d halted=1（含 halted 在内的全部模板）",
			statuses, len(specs)-1)
	}

	// THEN 已核销数：发放中模板下共 4 条券（used=2、available=1、held=1），列表给出
	// 的核销数必须是 used 的计数 2，而不是券总数 4。该列字段名未冻结，按值口径识别
	// （见 b8FindUsedCountKey）。
	issuingListRow := find(issuing.ID, specs[0].name)
	haltedListRow := find(halted.ID, specs[1].name)
	usedKey := b8FindUsedCountKey(t, issuingListRow, haltedListRow, float64(b8IssuingUsed), 0)
	t.Logf("发放中模板 %s：received_count=%v 券行总数=%d 核销数(%s)=%v；停发模板 %s：核销数(%s)=%v",
		issuing.ID, b8Number(t, issuingListRow, "received_count"), b8IssuingCouponRows, usedKey,
		b8Number(t, issuingListRow, usedKey), halted.ID, usedKey, b8Number(t, haltedListRow, usedKey))
}

// b9RolePermissions returns the permission codes attached to one role, read
// straight from role_permissions / permissions. B9 proves the fixture's role
// really lacks coupon:read through this query rather than trusting the insert
// that was supposed to create it.
func b9RolePermissions(t *testing.T, h *Harness, roleName string) []string {
	t.Helper()
	var codes []string
	if err := h.DB.Raw(
		`SELECT p.code FROM role_permissions rp
		 JOIN permissions p ON p.id = rp.permission_id
		 JOIN roles r ON r.id = rp.role_id
		 WHERE r.name = ?`, roleName,
	).Scan(&codes).Error; err != nil {
		t.Fatalf("read permissions of role %s: %v", roleName, err)
	}
	return codes
}

// b9HasCode reports whether a permission set carries one exact code.
func b9HasCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

// contract: B9
//
// 管理员角色不含 coupon:read 时查看券模板列表：响应 403。
//
// 「不含 coupon:read 的管理员」的构造方式（沿用 B3 与 tests/security
// TestAdminInsufficientPermission403 的既有路径，不新造基建）：0006 迁移把 coupon:read
// 授予了 super_admin 与 operator，这两个种子角色构造不出「缺 coupon:read」，因此新建
// 一个只授 coupon:write 的角色 coupon_writer，再经真实 BootstrapAdministrator 路径建
// 管理员并真实登录。选 coupon:write 而不是「什么券权限都不给」是刻意的：该角色是一个
// 真实的券域管理员，403 只能归因到闸门按 coupon:read 这一个码判定，而不会与「角色不
// 存在」「券权限集合为空」「管理员被禁用」这类 fixture 事故混同；反过来，若实现把读路径
// 的闸门放宽成 coupon:read 或 coupon:write，这条用例会红。
//
// 用例开头直接查 role_permissions 自证该角色的权限集合含 coupon:write 且不含
// coupon:read（否则 fixture 出问题时本用例会以「其实有权限却期待 403」的形式假红）。
//
// 排除伪成因的两道前置：
//   - 未认证导致的 403：本仓未认证是 401（middleware.abortUnauthorized）。先打一次只
//     要求认证、不要求任何权限码的 GET /api/admin/v1/auth/me，返回 200 说明会话有效。
//   - 端点和路由本身不可用（404 / 对所有人 403）：持 coupon:read 的 super_admin 用完全
//     相同的请求打到同一路径得 200，说明该端点存在、可用，下面的 403 是权限判定而不是
//     「用例打错了路径」或「所有会话都被拒」。
//
// 四层断言覆盖说明：
//  1. 端到端响应：GET /api/admin/v1/coupon-templates 走真实管理域路由（Cookie JWT +
//     coupon:read + CSRF），断言 HTTP 403——spec 钉的就是这个状态码，不用「非 200」
//     代替，也不接受 401/404/422 任一替代语义。
//  2. 参数 edge case：本 Scenario 的 WHEN 是权限维度而非入参维度，请求不带任何参数；
//     列表的入参取值域（分页）由 B8 覆盖，此处不重复。
//  3. 外部服务调用参数：列表读取不触达任何第三方 HTTP 服务（无图片上传、无微信调用、
//     无支付网关），无可 mock 的外部调用，本层缺省（同 B1/B2/B3/B4/B7/B8）。
//  4. 数据库字段变动：读路径不写库，本 Scenario 的 THEN 也只钉 403。spec 未要求读路径
//     的拒绝留痕，且本仓其余管理端读端点同样不留拒绝痕（coupon/register.go 的列表路由
//     用未审计的 AdminPermission），故此处不构造审计断言——那是 invent 要求。
func TestSpecCouponB9_ListWithoutReadPermissionForbidden(t *testing.T) {
	h := NewHarness(t, testDB)

	const (
		writerRoleName = "coupon_writer"
		writerUsername = "coupon-writer-e2e"
		writerPassword = "CouponWriter#2026e2e"
		// 两侧用完全相同的请求路径，两个会话唯一的差别是权限集合——403 与 200 的差异
		// 因此只能归因到 coupon:read。
		listPath = "/api/admin/v1/coupon-templates"
	)

	// 只授 coupon:write 的角色（ON CONFLICT 让同包内重复运行幂等，与 B3 的
	// coupon_reader 和 tests/security 的 product_reader 构造同构）。
	if err := h.DB.Exec(
		`INSERT INTO roles (id, name, remark) VALUES (?, ?, ?) ON CONFLICT (name) DO NOTHING`,
		uid.New(), writerRoleName, "e2e coupon write-only role",
	).Error; err != nil {
		t.Fatalf("create role %s: %v", writerRoleName, err)
	}
	if err := h.DB.Exec(
		`INSERT INTO role_permissions (role_id, permission_id)
		 SELECT r.id, p.id FROM roles r, permissions p
		 WHERE r.name = ? AND p.code = 'coupon:write'
		 ON CONFLICT DO NOTHING`, writerRoleName,
	).Error; err != nil {
		t.Fatalf("grant coupon:write to %s: %v", writerRoleName, err)
	}

	// 前置自证：该角色的权限集合确实含 coupon:write、不含 coupon:read。
	writerPermissions := b9RolePermissions(t, h, writerRoleName)
	if !b9HasCode(writerPermissions, "coupon:write") {
		t.Fatalf("前置不成立：角色 %s 的权限集合 %v 不含 coupon:write（该角色应是真实的券域管理员，403 才只能归因到缺 coupon:read）",
			writerRoleName, writerPermissions)
	}
	if b9HasCode(writerPermissions, "coupon:read") {
		t.Fatalf("前置不成立：角色 %s 的权限集合 %v 含 coupon:read，构造不出「角色不含 coupon:read」",
			writerRoleName, writerPermissions)
	}

	// 经真实 bootstrap 路径创建该管理员并真实登录。
	writer := h.bootstrapAdmin(writerUsername, writerPassword, writerRoleName)
	var writerID string
	if err := h.DB.Raw(
		`SELECT id::text FROM admin_users WHERE username = ?`, writerUsername,
	).Scan(&writerID).Error; err != nil {
		t.Fatalf("read admin id of %s: %v", writerUsername, err)
	}
	if writerID == "" {
		t.Fatalf("管理员 %s 未落库", writerUsername)
	}

	// 前置一：该会话已通过认证。GET /api/admin/v1/auth/me 只要求认证、不要求任何权限码，
	// 返回 200 说明后面的 403 来自权限闸门，而不是会话失效（未认证在本仓是 401）。
	meResp := h.AdminDo(writer, http.MethodGet, "/api/admin/v1/auth/me", nil, nil)
	_, _ = h.Decode(meResp) // drains and closes the body; the envelope is not asserted here
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("前置不成立：无 coupon:read 的管理员 GET /api/admin/v1/auth/me status = %d, want 200（会话应已认证）",
			meResp.StatusCode)
	}

	// 前置二（正向对照）：持 coupon:read 的 super_admin 打同一路径得 200。该端点若缺失
	// （404）或对所有会话一律拒绝，对照就会失败——那两种情况下的 403 都不是本 Scenario
	// 的行为。先自证 super_admin 的权限集合含 coupon:read，对照才有归因力。
	superPermissions := b9RolePermissions(t, h, "super_admin")
	if !b9HasCode(superPermissions, "coupon:read") {
		t.Fatalf("前置不成立：角色 super_admin 的权限集合 %v 不含 coupon:read，对照组无法证明该端点对持权管理员可用",
			superPermissions)
	}
	controlResp := h.AdminDo(h.SuperAdmin, http.MethodGet, listPath, nil, nil)
	controlEnv, controlRaw := h.Decode(controlResp)
	h.AssertTraceID(controlResp, controlEnv)
	if controlResp.StatusCode != http.StatusOK || controlEnv.Code != 0 {
		t.Fatalf("前置不成立：持 coupon:read 的 super_admin GET %s status=%d code=%d body=%s, want 200 / code 0（对照不成立时下面的 403 可能是端点缺失或对所有会话一律拒绝）",
			listPath, controlResp.StatusCode, controlEnv.Code, controlRaw)
	}

	// WHEN 管理员角色不含 coupon:read 的管理员查看券模板列表。
	resp := h.AdminDo(writer, http.MethodGet, listPath, nil, nil)
	env, raw := h.Decode(resp)
	h.AssertTraceID(resp, env)

	// THEN 响应 403。闸门（middleware.AdminPermission → permissionGate → abortForbidden）
	// 对权限不足一律 403；这里钉住 403 本身，401（未认证）、404（端点缺失或防探测）、
	// 422（权限码漂移）都不算通过。
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 coupon:read 的管理员 GET %s status = %d code = %d body=%s, want 403（权限不足一律 403；401 是未认证、404 是端点缺失）",
			listPath, resp.StatusCode, env.Code, raw)
	}
	// 取证：闸门实测返回的状态与业务码（spec 只钉 403，业务码未冻结）。
	t.Logf("无 coupon:read 的列表查看：HTTP %d code=%d message=%q（同路径持权对照 HTTP %d）；"+
		"角色 %s 的权限集合 %v，管理员 %s",
		resp.StatusCode, env.Code, env.Message, controlResp.StatusCode, writerRoleName, writerPermissions, writerID)
}
