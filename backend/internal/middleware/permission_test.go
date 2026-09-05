package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type fakePermissionResolver struct {
	permissions []string
	err         error
}

func (f fakePermissionResolver) PermissionsForAdmin(ctx context.Context, adminID uid.ID) ([]string, error) {
	return f.permissions, f.err
}

func adminClaims(id uid.ID) *JWTClaims {
	return &JWTClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "admin:" + id.String()}}
}

// callPermission runs AdminPermission directly on a gin context, so a nil
// claims set exercises the 401 path and a claims set exercises authorization.
func callPermission(t *testing.T, resolver PermissionResolver, required string, claims *JWTClaims) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if claims != nil {
		ctx.Set(ClaimsKey, claims)
	}
	AdminPermission(resolver, required)(ctx)
	return rec
}

func TestAdminPermissionAllowsRequiredPermission(t *testing.T) {
	resolver := fakePermissionResolver{permissions: []string{"product:read", "product:write"}}
	if rec := callPermission(t, resolver, "product:write", adminClaims(uid.ID{})); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAdminPermissionRejectsMissingPermissionWith403(t *testing.T) {
	resolver := fakePermissionResolver{permissions: []string{"product:read"}}
	if rec := callPermission(t, resolver, "product:write", adminClaims(uid.ID{})); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAdminPermissionRejectsUnknownPermissionWith422(t *testing.T) {
	resolver := fakePermissionResolver{err: ErrUnknownPermission}
	if rec := callPermission(t, resolver, "product:write", adminClaims(uid.ID{})); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAdminPermissionRejectsResolverFailureWith500(t *testing.T) {
	resolver := fakePermissionResolver{err: errors.New("database down")}
	if rec := callPermission(t, resolver, "product:write", adminClaims(uid.ID{})); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestAdminPermissionRejectsMissingClaimsWith401(t *testing.T) {
	if rec := callPermission(t, fakePermissionResolver{}, "product:read", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAdminPermissionRejectsNonAdminSubjectWith401(t *testing.T) {
	claims := &JWTClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "buyer:" + uid.New().String()}}
	if rec := callPermission(t, fakePermissionResolver{}, "product:read", claims); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
