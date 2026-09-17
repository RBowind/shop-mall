package coupon_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/coupon"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/uid"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// couponEnvelope is the business envelope every admin response uses.
type couponEnvelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

func decodeCouponEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) couponEnvelope {
	t.Helper()
	var envelope couponEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, recorder.Body.String())
	}
	return envelope
}

// createBody renders the create payload for the admin POST route.
func createBody(fix *couponFixture, name string) string {
	return fmt.Sprintf(`{
		"name": %q,
		"threshold_points": 500,
		"discount_points": 100,
		"total_count": 20,
		"per_user_limit": 2,
		"valid_from": %q,
		"valid_until": %q
	}`,
		name,
		fix.now.Add(-time.Hour).Format(time.RFC3339),
		fix.now.Add(24*time.Hour).Format(time.RFC3339),
	)
}

func TestNewHandlerRequiresServiceAndViewer(t *testing.T) {
	fix := newCouponFixture(t)
	if _, err := coupon.NewHandler(coupon.HandlerDeps{AdminViewer: fix.adminService}); err == nil {
		t.Fatal("NewHandler without a service returned no error")
	}
	if _, err := coupon.NewHandler(coupon.HandlerDeps{Service: fix.service}); err == nil {
		t.Fatal("NewHandler without an admin viewer returned no error")
	}
	// A nil logger is optional: the handler falls back to the default logger.
	if _, err := coupon.NewHandler(coupon.HandlerDeps{Service: fix.service, AdminViewer: fix.adminService}); err != nil {
		t.Fatalf("NewHandler with a nil logger: %v", err)
	}
}

func TestAdminCreateTemplateReturnsCreatedTemplate(t *testing.T) {
	fix := newCouponFixture(t)
	recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "HTTP创建-券模板"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeCouponEnvelope(t, recorder)
	if envelope.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 (message=%s)", envelope.Code, envelope.Message)
	}
	var data struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Status        string `json:"status"`
		ReceivedCount int32  `json:"received_count"`
		TotalCount    int32  `json:"total_count"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, envelope.Data)
	}
	if data.Status != string(coupon.StatusActive) || data.ReceivedCount != 0 {
		t.Fatalf("created template = %s/%d, want active/0", data.Status, data.ReceivedCount)
	}
	if data.Name != "HTTP创建-券模板" || data.TotalCount != 20 {
		t.Fatalf("projection = %q/%d, want the submitted name and total", data.Name, data.TotalCount)
	}
	id, err := uid.ParseCanonical(data.ID)
	if err != nil {
		t.Fatalf("response id %q is not canonical: %v", data.ID, err)
	}
	var stored database.CouponTemplate
	if err := fix.db.Where("id = ?", id).First(&stored).Error; err != nil {
		t.Fatalf("read stored template: %v", err)
	}
	if stored.Status != database.CouponTemplateStatusActive || stored.ThresholdPoints != 500 {
		t.Fatalf("stored template = %q/%d, want active/500", stored.Status, stored.ThresholdPoints)
	}
}

func TestAdminCreateTemplateRejectsMalformedRequests(t *testing.T) {
	fix := newCouponFixture(t)
	cases := []struct {
		name string
		body string
	}{
		{"body is not json", `{"name":`},
		{"valid_from has no offset", `{"name":"x","threshold_points":500,"discount_points":100,"total_count":20,"per_user_limit":2,"valid_from":"2026-09-17 12:00:00","valid_until":"2026-09-18T12:00:00Z"}`},
		{"valid_until is garbage", `{"name":"x","threshold_points":500,"discount_points":100,"total_count":20,"per_user_limit":2,"valid_from":"2026-09-17T12:00:00Z","valid_until":"tomorrow"}`},
		{"valid_until missing", `{"name":"x","threshold_points":500,"discount_points":100,"total_count":20,"per_user_limit":2,"valid_from":"2026-09-17T12:00:00Z"}`},
		{"discount equals threshold", `{"name":"x","threshold_points":500,"discount_points":500,"total_count":20,"per_user_limit":2,"valid_from":"2026-09-17T12:00:00Z","valid_until":"2026-09-18T12:00:00Z"}`},
		{"per user limit is zero", `{"name":"x","threshold_points":500,"discount_points":100,"total_count":20,"per_user_limit":0,"valid_from":"2026-09-17T12:00:00Z","valid_until":"2026-09-18T12:00:00Z"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", recorder.Code, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != 1001 {
				t.Fatalf("envelope code = %d, want 1001", envelope.Code)
			}
		})
	}

	// A rejected rule set must not leave a template behind.
	var templates int64
	if err := fix.db.Model(&database.CouponTemplate{}).Count(&templates).Error; err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if templates != 0 {
		t.Fatalf("rejected payloads created %d templates, want 0", templates)
	}
}

// TestAdminCreateTemplateRuleViolationMessageIsReported pins the mapping a
// rejected rule set gets: 400 with the rule text the service produced (the
// spec's 1001 参数错误), not the generic payload message.
func TestAdminCreateTemplateRuleViolationMessageIsReported(t *testing.T) {
	fix := newCouponFixture(t)
	body := `{"name":"x","threshold_points":500,"discount_points":600,"total_count":20,"per_user_limit":2,"valid_from":"2026-09-17T12:00:00Z","valid_until":"2026-09-18T12:00:00Z"}`
	recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeCouponEnvelope(t, recorder)
	if !strings.Contains(envelope.Message, "discount_points must be less than threshold_points") {
		t.Fatalf("message = %q, want the violated rule text", envelope.Message)
	}
}

func TestAdminUpdateTemplateStatusSwitchesStatusAndDropsRuleFields(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "HTTP切换-券模板", 500, 100, 10, 2)

	// The payload carries rule fields next to the status: the request struct
	// declares only status, so the rules are dropped rather than applied.
	body := `{"status":"halted","threshold_points":9999,"discount_points":1,"total_count":999,"per_user_limit":9,"valid_until":"2030-01-01T00:00:00Z"}`
	recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodPatch, "/api/admin/v1/coupon-templates/"+view.ID.String(), body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeCouponEnvelope(t, recorder)
	if envelope.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 (message=%s)", envelope.Code, envelope.Message)
	}
	var data struct {
		Status          string `json:"status"`
		ThresholdPoints int64  `json:"threshold_points"`
		DiscountPoints  int64  `json:"discount_points"`
		TotalCount      int32  `json:"total_count"`
		PerUserLimit    int32  `json:"per_user_limit"`
		ReceivedCount   int32  `json:"received_count"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, envelope.Data)
	}
	if data.Status != string(coupon.StatusHalted) {
		t.Fatalf("status = %q, want halted", data.Status)
	}
	if data.ThresholdPoints != 500 || data.DiscountPoints != 100 || data.TotalCount != 10 || data.PerUserLimit != 2 {
		t.Fatalf("rule fields changed: %+v, want the frozen 500/100/10/2", data)
	}
	var stored database.CouponTemplate
	if err := fix.db.Where("id = ?", view.ID).First(&stored).Error; err != nil {
		t.Fatalf("read stored template: %v", err)
	}
	if stored.Status != database.CouponTemplateStatusHalted {
		t.Fatalf("stored status = %q, want halted", stored.Status)
	}
	if stored.ThresholdPoints != 500 || stored.TotalCount != 10 || stored.ReceivedCount != 0 {
		t.Fatalf("stored rules = %d/%d/%d, want the creation values 500/10/0",
			stored.ThresholdPoints, stored.TotalCount, stored.ReceivedCount)
	}
}

func TestAdminUpdateTemplateStatusRejectsBadRequests(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "HTTP切换-拒因", 500, 100, 10, 2)
	cases := []struct {
		name       string
		templateID string
		body       string
		wantStatus int
		wantCode   int
	}{
		{"id is not a uuid", "not-a-uuid", `{"status":"halted"}`, http.StatusBadRequest, 1001},
		{"id is an uppercase uuid", strings.ToUpper(view.ID.String()), `{"status":"halted"}`, http.StatusBadRequest, 1001},
		{"id is a uuid with braces", "{" + view.ID.String() + "}", `{"status":"halted"}`, http.StatusBadRequest, 1001},
		{"body is not json", view.ID.String(), `{"status":`, http.StatusBadRequest, 1001},
		{"status is missing", view.ID.String(), `{}`, http.StatusBadRequest, 1001},
		{"status is unknown", view.ID.String(), `{"status":"paused"}`, http.StatusBadRequest, 1001},
		{"status is blank", view.ID.String(), `{"status":"  "}`, http.StatusBadRequest, 1001},
		{"status has different case", view.ID.String(), `{"status":"Halted"}`, http.StatusBadRequest, 1001},
		{"template does not exist", uid.New().String(), `{"status":"halted"}`, http.StatusNotFound, 1004},
		{"template id is the zero uuid", "00000000-0000-0000-0000-000000000000", `{"status":"halted"}`, http.StatusNotFound, 1004},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodPatch, "/api/admin/v1/coupon-templates/"+tc.templateID, tc.body)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != tc.wantCode {
				t.Fatalf("envelope code = %d, want %d", envelope.Code, tc.wantCode)
			}
		})
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusActive)

	// None of the rejected switches left an audit row behind.
	var audits int64
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'coupon_template.status_change' AND target_id = ?`, view.ID,
	).Scan(&audits).Error; err != nil {
		t.Fatalf("count status-change audits: %v", err)
	}
	if audits != 0 {
		t.Fatalf("rejected switches wrote %d audit rows, want 0", audits)
	}
}

func TestAdminListTemplatesReturnsRowsWithUsedCountAndPaging(t *testing.T) {
	fix := newCouponFixture(t)
	first := fix.createTemplate(t, "HTTP列表-最早", 500, 100, 10, 1)
	second := fix.createTemplate(t, "HTTP列表-居中", 500, 100, 10, 1)
	third := fix.createTemplate(t, "HTTP列表-最新", 500, 100, 10, 1)
	userID := fix.seedUser(t, "coupon-http-list-user", 100000)
	fix.seedUserCoupon(t, userID, first.ID, "used", "http-req-1")
	fix.seedUserCoupon(t, userID, first.ID, "used", "http-req-2")
	fix.seedUserCoupon(t, userID, second.ID, "available", "http-req-3")

	recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodGet, "/api/admin/v1/coupon-templates?page=1&page_size=2", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeCouponEnvelope(t, recorder)
	var data struct {
		List []struct {
			ID        string `json:"id"`
			UsedCount int64  `json:"used_count"`
			Status    string `json:"status"`
		} `json:"list"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, envelope.Data)
	}
	if data.Total != 3 || data.Page != 1 || data.PageSize != 2 {
		t.Fatalf("envelope = total %d page %d size %d, want 3/1/2", data.Total, data.Page, data.PageSize)
	}
	if len(data.List) != 2 {
		t.Fatalf("list = %d rows, want 2", len(data.List))
	}
	if data.List[0].ID != third.ID.String() || data.List[0].UsedCount != 0 {
		t.Fatalf("head row = %s/%d, want the newest with 0 redeemed", data.List[0].ID, data.List[0].UsedCount)
	}
	if data.List[1].ID != second.ID.String() || data.List[1].UsedCount != 0 {
		t.Fatalf("second row = %s/%d, want the middle template with 0 redeemed", data.List[1].ID, data.List[1].UsedCount)
	}

	// The oldest template is on page 2 and carries the redeemed count.
	page2 := fix.perform(t, fix.router, &fix.superAdmin, http.MethodGet, "/api/admin/v1/coupon-templates?page=2&page_size=2", "")
	if page2.Code != http.StatusOK {
		t.Fatalf("page 2 status = %d, want 200", page2.Code)
	}
	var page2Data struct {
		List []struct {
			ID        string `json:"id"`
			UsedCount int64  `json:"used_count"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(decodeCouponEnvelope(t, page2).Data, &page2Data); err != nil {
		t.Fatalf("decode page 2 data: %v", err)
	}
	if page2Data.Total != 3 || len(page2Data.List) != 1 {
		t.Fatalf("page 2 = %d rows / total %d, want the single oldest / 3", len(page2Data.List), page2Data.Total)
	}
	if page2Data.List[0].ID != first.ID.String() || page2Data.List[0].UsedCount != 2 {
		t.Fatalf("oldest row = %s/%d, want %s/2", page2Data.List[0].ID, page2Data.List[0].UsedCount, first.ID)
	}

	// Omitting the query parameters uses the documented defaults.
	defaulted := fix.perform(t, fix.router, &fix.superAdmin, http.MethodGet, "/api/admin/v1/coupon-templates", "")
	if defaulted.Code != http.StatusOK {
		t.Fatalf("default paging status = %d, want 200", defaulted.Code)
	}
	var defaultData struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	if err := json.Unmarshal(decodeCouponEnvelope(t, defaulted).Data, &defaultData); err != nil {
		t.Fatalf("decode default data: %v", err)
	}
	if defaultData.Page != 1 || defaultData.PageSize != 20 {
		t.Fatalf("defaults = page %d size %d, want 1/20", defaultData.Page, defaultData.PageSize)
	}
}

func TestAdminListTemplatesRejectsInvalidPaging(t *testing.T) {
	fix := newCouponFixture(t)
	for _, query := range []string{"page=0", "page=-3", "page=abc", "page_size=0", "page_size=101", "page_size=xyz", "page_size=-1"} {
		t.Run(query, func(t *testing.T) {
			recorder := fix.perform(t, fix.router, &fix.superAdmin, http.MethodGet, "/api/admin/v1/coupon-templates?"+query, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", recorder.Code, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != 1001 {
				t.Fatalf("envelope code = %d, want 1001", envelope.Code)
			}
		})
	}
}

// TestAdminRoutesDenyUnpermittedAdminAndAuditWriteDenials drives the coupon
// routes with an authenticated administrator whose role holds no permission at
// all: the write routes answer 403 and leave a failure audit row, the read
// route answers the same 403 through the plain gate with no audit row.
func TestAdminRoutesDenyUnpermittedAdminAndAuditWriteDenials(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "HTTP无权限-券模板", 500, 100, 10, 2)

	create := fix.perform(t, fix.router, &fix.readlessAdmin, http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "HTTP无权限-创建"))
	if create.Code != http.StatusForbidden {
		t.Fatalf("create status = %d, want 403 (body=%s)", create.Code, create.Body.String())
	}
	if envelope := decodeCouponEnvelope(t, create); envelope.Code != 1003 {
		t.Fatalf("create envelope code = %d, want 1003", envelope.Code)
	}
	// The gate short-circuits before the handler, so no template appeared.
	var templates int64
	if err := fix.db.Model(&database.CouponTemplate{}).Count(&templates).Error; err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if templates != 1 {
		t.Fatalf("templates = %d, want only the fixture's one", templates)
	}

	switchRecorder := fix.perform(t, fix.router, &fix.readlessAdmin, http.MethodPatch, "/api/admin/v1/coupon-templates/"+view.ID.String(), `{"status":"halted"}`)
	if switchRecorder.Code != http.StatusForbidden {
		t.Fatalf("status switch status = %d, want 403 (body=%s)", switchRecorder.Code, switchRecorder.Body.String())
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusActive)

	list := fix.perform(t, fix.router, &fix.readlessAdmin, http.MethodGet, "/api/admin/v1/coupon-templates", "")
	if list.Code != http.StatusForbidden {
		t.Fatalf("list status = %d, want 403 (body=%s)", list.Code, list.Body.String())
	}

	// Exactly two failure audit rows: one per denied write, each under the
	// write's own action and naming the denied administrator. The read denial
	// leaves no trail.
	var rows []struct {
		Action     string
		Result     string
		ActorAdmin uid.ID
	}
	if err := fix.db.Raw(
		`SELECT action, result, actor_admin_id AS actor_admin FROM audit_logs
		 WHERE result = 'failure' AND actor_admin_id = ? ORDER BY action`, fix.readlessAdmin.id,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("read denial audits: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("failure audit rows = %d, want 2 (one per denied write)", len(rows))
	}
	if rows[0].Action != "coupon_template.create" || rows[1].Action != "coupon_template.status_change" {
		t.Fatalf("denial actions = %q/%q, want coupon_template.create/coupon_template.status_change", rows[0].Action, rows[1].Action)
	}
	for _, row := range rows {
		if row.ActorAdmin != fix.readlessAdmin.id {
			t.Fatalf("denial audit actor = %s, want the denied administrator %s", row.ActorAdmin, fix.readlessAdmin.id)
		}
	}
}

func TestAdminRoutesRejectUnauthenticatedRequests(t *testing.T) {
	fix := newCouponFixture(t)
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list without a session", http.MethodGet, "/api/admin/v1/coupon-templates", ""},
		{"create without a session", http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "未认证")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, fix.router, nil, tc.method, tc.path, tc.body)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%s)", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// TestHandlersReportMissingIdentityWithoutMiddleware calls the handlers through
// a router that has no authentication middleware: an admin route reached
// without claims must answer 401, never 200 or 500.
func TestHandlersReportMissingIdentityWithoutMiddleware(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "无身份-券模板", 500, 100, 10, 2)
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "无身份-创建")},
		{"switch", http.MethodPatch, "/api/admin/v1/coupon-templates/" + view.ID.String(), `{"status":"halted"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, fix.bareRouter, &fix.superAdmin, tc.method, tc.path, tc.body)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%s)", recorder.Code, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != 1002 {
				t.Fatalf("envelope code = %d, want 1002", envelope.Code)
			}
		})
	}
}

// TestHandlersReportInternalErrorWhenDatabaseUnavailable points the handlers at
// a service whose connection pool is closed: a failure that is not a client
// error must surface as the 5001 internal error, not as a 400 or a panic.
func TestHandlersReportInternalErrorWhenDatabaseUnavailable(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "故障-券模板", 500, 100, 10, 2)
	claimed := claimsRouter(fix)
	fix.closePool()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "故障-创建")},
		{"switch", http.MethodPatch, "/api/admin/v1/coupon-templates/" + view.ID.String(), `{"status":"halted"}`},
		{"list", http.MethodGet, "/api/admin/v1/coupon-templates", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, claimed, nil, tc.method, tc.path, tc.body)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body=%s)", recorder.Code, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != 5001 {
				t.Fatalf("envelope code = %d, want 5001", envelope.Code)
			}
		})
	}
}

// subjectRouter mounts the handler methods behind a middleware that only
// installs a claims set, so the handler's own identity and service-error
// mappings are reachable without the admin authentication stack.
func subjectRouter(fix *couponFixture, subject string) http.Handler {
	engine := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ClaimsKey, &middleware.JWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
		})
		c.Next()
	}
	group := engine.Group("", inject)
	group.POST("/api/admin/v1/coupon-templates", fix.handler.AdminCreateTemplate)
	group.PATCH("/api/admin/v1/coupon-templates/:templateId", fix.handler.AdminUpdateTemplateStatus)
	group.GET("/api/admin/v1/coupon-templates", fix.handler.AdminListTemplates)
	return engine
}

func claimsRouter(fix *couponFixture) http.Handler {
	return subjectRouter(fix, "admin:"+fix.superAdmin.id.String())
}

// TestHandlersRejectAClaimsSetThatIsNotAnAdmin pins the handler's own identity
// check: a verified claims set whose subject is not an administrator must not
// be treated as an actor.
func TestHandlersRejectAClaimsSetThatIsNotAnAdmin(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "非管理员-券模板", 500, 100, 10, 2)
	router := subjectRouter(fix, "buyer:"+uid.New().String())
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/api/admin/v1/coupon-templates", createBody(fix, "非管理员-创建")},
		{"switch", http.MethodPatch, "/api/admin/v1/coupon-templates/" + view.ID.String(), `{"status":"halted"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := fix.perform(t, router, nil, tc.method, tc.path, tc.body)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%s)", recorder.Code, recorder.Body.String())
			}
			if envelope := decodeCouponEnvelope(t, recorder); envelope.Code != 1002 {
				t.Fatalf("envelope code = %d, want 1002", envelope.Code)
			}
		})
	}
}

func TestRegisterAdminRoutesToleratesNilInputsAndPanicsOnInvalidSigner(t *testing.T) {
	fix := newCouponFixture(t)
	// A nil group or a bundle without a handler registers nothing instead of
	// panicking on a nil dereference.
	group := gin.New().Group("/api/admin/v1")
	coupon.RegisterAdminRoutes(nil, coupon.AdminRouteDeps{Handler: fix.handler})
	coupon.RegisterAdminRoutes(group, coupon.AdminRouteDeps{Signer: fix.signer})
	// A valid bundle without a clock registers the routes with the wall clock;
	// the route group must be usable without every optional dependency set.
	coupon.RegisterAdminRoutes(group, coupon.AdminRouteDeps{Handler: fix.handler, Signer: fix.signer})

	// A nil signer is a composition error: the route group refuses to serve an
	// unverifiable admin surface and panics at wiring time.
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("RegisterAdminRoutes with a nil signer did not panic")
		}
		if !strings.Contains(fmt.Sprint(recovered), "invalid JWT configuration") {
			t.Fatalf("panic = %v, want the JWT configuration error", recovered)
		}
	}()
	coupon.RegisterAdminRoutes(gin.New().Group("/api/admin/v1"), coupon.AdminRouteDeps{Handler: fix.handler})
}
