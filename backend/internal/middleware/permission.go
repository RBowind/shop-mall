package middleware

import (
	"context"
	"errors"
	"net/http"

	"shop-mall/backend/internal/platform/uid"

	"github.com/gin-gonic/gin"
)

// ErrUnknownPermission is returned by a PermissionResolver when the resolved
// role references a permission code that is not in the seed list. The admin
// service wraps the same sentinel so handlers can distinguish a configuration
// drift problem (422) from an authentication problem.
var ErrUnknownPermission = errors.New("unknown permission")

// PermissionResolver resolves the live permission codes for an administrator.
// The admin service implements it; permissions are never embedded in the JWT
// and must be resolved on every authorization decision.
type PermissionResolver interface {
	PermissionsForAdmin(ctx context.Context, adminID uid.ID) ([]string, error)
}

// AdminPermission enforces the x-required-permission contract for admin
// routes. An authenticated administrator without the required permission
// receives 403; a role that references an unknown permission code receives
// 422 (the contract's unknown-permission response).
func AdminPermission(resolver PermissionResolver, required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromGin(c)
		if !ok || claims == nil {
			abortUnauthorized(c)
			return
		}
		adminID, err := AdminIDFromSubject(claims.Subject)
		if err != nil {
			abortUnauthorized(c)
			return
		}
		permissions, err := resolver.PermissionsForAdmin(c.Request.Context(), adminID)
		if err != nil {
			if errors.Is(err, ErrUnknownPermission) {
				abortBusiness(c, http.StatusUnprocessableEntity, "unknown permission")
			} else {
				abortInternal(c)
			}
			return
		}
		for _, permission := range permissions {
			if permission == required {
				c.Next()
				return
			}
		}
		abortForbidden(c, "insufficient permission")
	}
}

func abortBusiness(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"code":     2002,
		"data":     nil,
		"message":  message,
		"trace_id": TraceIDFromGin(c),
	})
}

func abortInternal(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"code":     5001,
		"data":     nil,
		"message":  "internal server error",
		"trace_id": TraceIDFromGin(c),
	})
}
