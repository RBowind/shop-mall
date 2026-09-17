package coupon

import (
	"fmt"
	"time"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

// AdminRouteDeps is the dependency bundle for RegisterAdminRoutes.
type AdminRouteDeps struct {
	Handler            *Handler
	Signer             *tokens.Signer
	Cookie             config.AdminCookieConfig
	Now                func() time.Time
	PermissionResolver middleware.PermissionResolver
	AdminStateResolver middleware.AdminStateResolver
	// DeniedAuditor records the failure audit row when the permission gate
	// rejects coupon template creation. Nil keeps the plain gate.
	DeniedAuditor middleware.DeniedAuditor
}

// couponTemplateAuditDescriptor is the domain's audit identity for a coupon
// template creation attempt. The denied attempt is audited under the same
// action and target type as the successful creation (Service.CreateTemplate),
// so both outcomes of one action are queryable under one key.
var couponTemplateAuditDescriptor = middleware.PermissionAuditDescriptor{
	Action:     "coupon_template.create",
	TargetType: "coupon_template",
}

// couponTemplateStatusAuditDescriptor is the domain's audit identity for an
// issuance-status switch attempt (docs/architecture/07-coupon-pay-lifecycle.md
// §5 券模板发放状态切换). A denied switch is audited under its own action so it
// does not read as a creation attempt.
var couponTemplateStatusAuditDescriptor = middleware.PermissionAuditDescriptor{
	Action:     "coupon_template.status_change",
	TargetType: "coupon_template",
}

// RegisterAdminRoutes wires the administrator coupon template routes under
// /api/admin/v1 and applies the same AdminAuth (signature, token_version
// freshness, enabled flag) and the contract's x-required-permission checks as
// the other admin domains. The caller adds the AdminOrigin and CSRF middleware
// to the group.
func RegisterAdminRoutes(group *gin.RouterGroup, deps AdminRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("coupon handler: invalid JWT configuration: %w", err))
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	protected := group.Group("")
	protected.Use(middleware.AdminAuthWithClock(jwtService, deps.Cookie.AccessTokenName, now, deps.AdminStateResolver))
	// A denied creation attempt is audited (specs/coupon/spec.md 无权限创建): the
	// gate short-circuits before the handler, so only the gate can record it.
	// This is the one admin route using the audited gate; every other domain
	// keeps the plain AdminPermission and stays unaudited on denial.
	protected.POST("/coupon-templates",
		middleware.AuditedAdminPermission(deps.PermissionResolver, "coupon:write", deps.DeniedAuditor, couponTemplateAuditDescriptor),
		deps.Handler.AdminCreateTemplate)
	// The issuance-status switch is the only write path into an existing
	// template (its rule set is frozen at creation), so it carries the same
	// coupon:write gate and the same denied-attempt audit as creation, under
	// the switch's own action.
	protected.PATCH("/coupon-templates/:templateId",
		middleware.AuditedAdminPermission(deps.PermissionResolver, "coupon:write", deps.DeniedAuditor, couponTemplateStatusAuditDescriptor),
		deps.Handler.AdminUpdateTemplateStatus)
}
