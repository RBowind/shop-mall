package cart

import (
	"fmt"

	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

// RouteDeps is the dependency bundle for RegisterRoutes.
type RouteDeps struct {
	Handler            *CartHandler
	Signer             *tokens.Signer
	BuyerStateResolver middleware.BuyerStateResolver
}

// RegisterRoutes wires the authenticated buyer cart endpoints under
// /api/v1/cart. The caller supplies the buyer signer so the middleware shares
// the keyring used to issue buyer tokens at login.
func RegisterRoutes(group *gin.RouterGroup, deps RouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("cart handler: invalid buyer JWT configuration: %w", err))
	}
	protected := group.Group("")
	protected.Use(middleware.BuyerAuth(jwtService))
	protected.Use(middleware.BuyerAuthWithState(jwtService, deps.BuyerStateResolver))
	protected.GET("/cart", deps.Handler.GetCart)
	protected.POST("/cart", deps.Handler.AddCartItem)
	protected.PATCH("/cart/:itemId", deps.Handler.UpdateCartItem)
	protected.DELETE("/cart/:itemId", deps.Handler.DeleteCartItem)
}
