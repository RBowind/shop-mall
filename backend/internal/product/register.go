package product

import (
	"fmt"
	"time"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

// RegisterPublicRoutes wires the buyer-facing product routes.
func RegisterPublicRoutes(group *gin.RouterGroup, handler *Handler) {
	if group == nil || handler == nil {
		return
	}
	group.GET("/products", handler.ListProducts)
	group.GET("/products/:productId", handler.GetProduct)
	group.GET("/categories", handler.ListCategories)
}

// AdminRouteDeps is the dependency bundle for RegisterAdminRoutes.
type AdminRouteDeps struct {
	Handler            *Handler
	Signer             *tokens.Signer
	Cookie             config.AdminCookieConfig
	Now                func() time.Time
	PermissionResolver middleware.PermissionResolver
	AdminStateResolver middleware.AdminStateResolver
}

// RegisterAdminRoutes wires the administrator product routes and applies the
// same AdminAuth (signature, token_version freshness, enabled flag) and the
// contract's x-required-permission checks. The caller adds the AdminOrigin
// and CSRF middleware to the group.
func RegisterAdminRoutes(group *gin.RouterGroup, deps AdminRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("product handler: invalid JWT configuration: %w", err))
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	protected := group.Group("")
	protected.Use(middleware.AdminAuthWithClock(jwtService, deps.Cookie.AccessTokenName, now, deps.AdminStateResolver))
	protected.GET("/products",
		middleware.AdminPermission(deps.PermissionResolver, "product:read"),
		deps.Handler.AdminListProducts)
	protected.POST("/products",
		middleware.AdminPermission(deps.PermissionResolver, "product:write"),
		deps.Handler.AdminCreateProduct)
	protected.GET("/products/:productId",
		middleware.AdminPermission(deps.PermissionResolver, "product:read"),
		deps.Handler.AdminGetProduct)
	protected.PATCH("/products/:productId",
		middleware.AdminPermission(deps.PermissionResolver, "product:write"),
		deps.Handler.AdminUpdateProduct)
}
