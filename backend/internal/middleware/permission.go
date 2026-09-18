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

// PermissionAuditDescriptor names the failure audit row a denied request is
// recorded under. The gate itself does not know the business domain, so the
// route that enables auditing supplies the descriptor.
type PermissionAuditDescriptor struct {
	Action     string
	TargetType string
}

// DeniedAuditor records the failure audit row for a request the permission gate
// rejected. The gate short-circuits before the handler, so no code in the
// domain ever sees the denied request; the owning domain supplies this hook.
// An implementation runs best-effort: it writes in its own transaction and logs
// a write failure instead of surfacing it, so the response stays the gate's.
type DeniedAuditor interface {
	RecordPermissionDenied(ctx context.Context, adminID uid.ID, descriptor PermissionAuditDescriptor)
}

// AdminPermission enforces the x-required-permission contract for admin
// routes. An authenticated administrator without the required permission
// receives 403; a role that references an unknown permission code receives
// 422 (the contract's unknown-permission response).
func AdminPermission(resolver PermissionResolver, required string) gin.HandlerFunc {
	return permissionGate(resolver, required, nil)
}

// AuditedAdminPermission is AdminPermission plus a failure audit on denial: the
// caller receives the same response the plain gate produces (401, 422 or the
// 403 for an authenticated administrator without the permission), and the
// denied attempt is additionally recorded through auditor under the route's
// descriptor. Only routes that must leave a denial trail pass an auditor; a nil
// auditor degrades to the plain gate, so no other admin domain changes behavior.
func AuditedAdminPermission(resolver PermissionResolver, required string, auditor DeniedAuditor, descriptor PermissionAuditDescriptor) gin.HandlerFunc {
	if auditor == nil {
		return permissionGate(resolver, required, nil)
	}
	return permissionGate(resolver, required, func(c *gin.Context, adminID uid.ID) {
		auditor.RecordPermissionDenied(c.Request.Context(), adminID, descriptor)
	})
}

// permissionGate is the authorization decision shared by AdminPermission and
// AuditedAdminPermission, so the two never drift. onDenied runs only for an
// authenticated administrator that lacks the required permission, after the
// permission lookup succeeded and before the 403 is written.
func permissionGate(resolver PermissionResolver, required string, onDenied func(*gin.Context, uid.ID)) gin.HandlerFunc {
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
		if onDenied != nil {
			onDenied(c, adminID)
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
