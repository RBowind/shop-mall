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

// recordingDeniedAuditor captures every denial the audited gate reports, so a
// test asserts WHICH administrator and descriptor reached the auditor instead
// of only that a response code came back.
type recordingDeniedAuditor struct {
	calls []deniedAuditCall
}

type deniedAuditCall struct {
	adminID    uid.ID
	descriptor PermissionAuditDescriptor
}

func (r *recordingDeniedAuditor) RecordPermissionDenied(_ context.Context, adminID uid.ID, descriptor PermissionAuditDescriptor) {
	r.calls = append(r.calls, deniedAuditCall{adminID: adminID, descriptor: descriptor})
}

func (r *recordingDeniedAuditor) callCount() int { return len(r.calls) }

// callGate runs a permission gate directly on a gin context with an optional
// claims set, mirroring callPermission from permission_test.go for the gates
// that are not AdminPermission itself.
func callGate(t *testing.T, gate gin.HandlerFunc, claims *JWTClaims) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if claims != nil {
		ctx.Set(ClaimsKey, claims)
	}
	gate(ctx)
	return rec
}

func auditedDescriptor() PermissionAuditDescriptor {
	return PermissionAuditDescriptor{Action: "coupon_template.create", TargetType: "coupon_template"}
}

func TestAuditedAdminPermissionRecordsDenialThroughAuditor(t *testing.T) {
	adminID := uid.New()
	auditor := &recordingDeniedAuditor{}
	gate := AuditedAdminPermission(fakePermissionResolver{permissions: []string{"product:read"}}, "coupon:write", auditor, auditedDescriptor())

	rec := callGate(t, gate, adminClaims(adminID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if auditor.callCount() != 1 {
		t.Fatalf("auditor calls = %d, want exactly 1", auditor.callCount())
	}
	call := auditor.calls[0]
	if call.adminID != adminID {
		t.Fatalf("audited admin = %s, want the authenticated %s", call.adminID, adminID)
	}
	if call.descriptor != auditedDescriptor() {
		t.Fatalf("audited descriptor = %+v, want %+v", call.descriptor, auditedDescriptor())
	}
}

func TestAuditedAdminPermissionSkipsAuditorWhenPermissionIsGranted(t *testing.T) {
	adminID := uid.New()
	auditor := &recordingDeniedAuditor{}
	gate := AuditedAdminPermission(fakePermissionResolver{permissions: []string{"coupon:write"}}, "coupon:write", auditor, auditedDescriptor())

	if rec := callGate(t, gate, adminClaims(adminID)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when the required permission is held", rec.Code)
	}
	if auditor.callCount() != 0 {
		t.Fatalf("auditor calls = %d, want 0 when the permission is granted", auditor.callCount())
	}
}

func TestAuditedAdminPermissionSkipsAuditorWithoutIdentity(t *testing.T) {
	auditor := &recordingDeniedAuditor{}
	gate := AuditedAdminPermission(fakePermissionResolver{}, "coupon:write", auditor, auditedDescriptor())

	if rec := callGate(t, gate, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing-claims status = %d, want 401", rec.Code)
	}
	nonAdmin := &JWTClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "buyer:" + uid.New().String()}}
	if rec := callGate(t, gate, nonAdmin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin-subject status = %d, want 401", rec.Code)
	}
	if auditor.callCount() != 0 {
		t.Fatalf("auditor calls = %d, want 0 for an unauthenticated request", auditor.callCount())
	}
}

func TestAuditedAdminPermissionSkipsAuditorOnResolverFailure(t *testing.T) {
	auditor := &recordingDeniedAuditor{}
	unknown := AuditedAdminPermission(fakePermissionResolver{err: ErrUnknownPermission}, "coupon:write", auditor, auditedDescriptor())
	if rec := callGate(t, unknown, adminClaims(uid.New())); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown-permission status = %d, want 422", rec.Code)
	}
	broken := AuditedAdminPermission(fakePermissionResolver{err: errors.New("database down")}, "coupon:write", auditor, auditedDescriptor())
	if rec := callGate(t, broken, adminClaims(uid.New())); rec.Code != http.StatusInternalServerError {
		t.Fatalf("resolver-failure status = %d, want 500", rec.Code)
	}
	if auditor.callCount() != 0 {
		t.Fatalf("auditor calls = %d, want 0 when the permission lookup itself failed", auditor.callCount())
	}
}

func TestAuditedAdminPermissionWithNilAuditorKeepsThePlainGate(t *testing.T) {
	gate := AuditedAdminPermission(fakePermissionResolver{permissions: []string{"product:read"}}, "coupon:write", nil, auditedDescriptor())
	if rec := callGate(t, gate, adminClaims(uid.New())); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want the plain gate's 403", rec.Code)
	}
	granted := AuditedAdminPermission(fakePermissionResolver{permissions: []string{"coupon:write"}}, "coupon:write", nil, auditedDescriptor())
	if rec := callGate(t, granted, adminClaims(uid.New())); rec.Code != http.StatusOK {
		t.Fatalf("granted status = %d, want 200", rec.Code)
	}
}
