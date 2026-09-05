package order

import (
	"fmt"
	"time"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

// BuyerRouteDeps is the dependency bundle for RegisterBuyerRoutes.
type BuyerRouteDeps struct {
	Handler            *OrderHandler
	Signer             *tokens.Signer
	BuyerStateResolver middleware.BuyerStateResolver
}

// RegisterBuyerRoutes wires the authenticated buyer order and points-ledger
// endpoints under /api/v1. The buyer signer supplies the keyring shared with
// login.
func RegisterBuyerRoutes(group *gin.RouterGroup, deps BuyerRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("order handler: invalid buyer JWT configuration: %w", err))
	}
	protected := group.Group("")
	protected.Use(middleware.BuyerAuth(jwtService))
	protected.Use(middleware.BuyerAuthWithState(jwtService, deps.BuyerStateResolver))
	protected.POST("/orders", deps.Handler.CreateOrder)
	protected.GET("/orders", deps.Handler.ListOrders)
	protected.GET("/orders/:orderId", deps.Handler.GetOrder)
	protected.POST("/orders/:orderId/refund", deps.Handler.RequestRefund)
	protected.POST("/orders/:orderId/confirm", deps.Handler.ConfirmOrder)
	protected.GET("/points/ledger", deps.Handler.ListPointsLedger)
}

// AdminRouteDeps is the dependency bundle for RegisterAdminRoutes.
type AdminRouteDeps struct {
	Handler            *OrderHandler
	Signer             *tokens.Signer
	Cookie             config.AdminCookieConfig
	Now                func() time.Time
	PermissionResolver middleware.PermissionResolver
	AdminStateResolver middleware.AdminStateResolver
}

// RegisterAdminRoutes wires the administrator order endpoints and the points
// adjustment endpoint, applying the same AdminAuth (signature, token_version
// freshness, enabled flag) and the contract's x-required-permission checks.
// The caller adds the AdminOrigin and CSRF middleware to the group.
func RegisterAdminRoutes(group *gin.RouterGroup, deps AdminRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("order handler: invalid JWT configuration: %w", err))
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	protected := group.Group("")
	protected.Use(middleware.AdminAuthWithClock(jwtService, deps.Cookie.AccessTokenName, now, deps.AdminStateResolver))
	protected.GET("/orders",
		middleware.AdminPermission(deps.PermissionResolver, "order:read"),
		deps.Handler.AdminListOrders)
	protected.GET("/orders/:orderId",
		middleware.AdminPermission(deps.PermissionResolver, "order:read"),
		deps.Handler.AdminGetOrder)
	protected.POST("/orders/:orderId/ship",
		middleware.AdminPermission(deps.PermissionResolver, "order:ship"),
		deps.Handler.AdminShipOrder)
	protected.GET("/refunds",
		middleware.AdminPermission(deps.PermissionResolver, "refund:read"),
		deps.Handler.AdminListRefunds)
	protected.POST("/refunds/:orderId/approve",
		middleware.AdminPermission(deps.PermissionResolver, "refund:approve"),
		deps.Handler.AdminApproveRefund)
	protected.POST("/refunds/:orderId/reject",
		middleware.AdminPermission(deps.PermissionResolver, "refund:approve"),
		deps.Handler.AdminRejectRefund)
	protected.POST("/points/adjust",
		middleware.AdminPermission(deps.PermissionResolver, "points:adjust"),
		deps.Handler.AdjustPoints)
	protected.GET("/points/ledger",
		middleware.AdminPermission(deps.PermissionResolver, "points:adjust"),
		deps.Handler.AdminListLedger)
}
