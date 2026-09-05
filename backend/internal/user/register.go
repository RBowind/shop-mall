package user

import (
	"fmt"

	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

// AddressRouteDeps is the dependency bundle for RegisterAddressRoutes.
type AddressRouteDeps struct {
	Handler            *AddressHandler
	Signer             *tokens.Signer
	BuyerStateResolver middleware.BuyerStateResolver
}

// ProfileRouteDeps is the dependency bundle for RegisterProfileRoutes.
type ProfileRouteDeps struct {
	Handler            *ProfileHandler
	Signer             *tokens.Signer
	BuyerStateResolver middleware.BuyerStateResolver
}

// RegisterProfileRoutes wires the authenticated buyer profile endpoints under
// /api/v1/me. The caller supplies the buyer signer so the middleware shares the
// keyring used to issue buyer tokens at login.
func RegisterProfileRoutes(group *gin.RouterGroup, deps ProfileRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("profile handler: invalid buyer JWT configuration: %w", err))
	}
	protected := group.Group("")
	protected.Use(middleware.BuyerAuthWithState(jwtService, deps.BuyerStateResolver))
	protected.GET("/me", deps.Handler.GetMe)
	protected.PATCH("/me", deps.Handler.UpdateMe)
	protected.POST("/me/avatar", deps.Handler.UploadAvatar)
}

// RegisterAddressRoutes wires the authenticated buyer address endpoints under
// /api/v1/addresses. The caller supplies the buyer signer so the middleware
// shares the keyring used to issue buyer tokens at login.
func RegisterAddressRoutes(group *gin.RouterGroup, deps AddressRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(fmt.Errorf("address handler: invalid buyer JWT configuration: %w", err))
	}
	protected := group.Group("")
	protected.Use(middleware.BuyerAuthWithState(jwtService, deps.BuyerStateResolver))
	protected.GET("/addresses", deps.Handler.ListAddresses)
	protected.POST("/addresses", deps.Handler.CreateAddress)
	protected.PATCH("/addresses/:addressId", deps.Handler.UpdateAddress)
	protected.DELETE("/addresses/:addressId", deps.Handler.DeleteAddress)
}
