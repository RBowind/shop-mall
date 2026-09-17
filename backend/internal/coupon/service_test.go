package coupon_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/coupon"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/uid"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// couponTestAdminOrigin is the Origin header the admin write routes accept; it
// mirrors the deployed admin console origin and is the only value
// middleware.AdminOrigin lets through.
const couponTestAdminOrigin = "https://admin.example.test"

// couponFixture is the shared fixture of the coupon package tests: a real
// PostgreSQL schema (the same migrations production runs), the real admin
// service as permission/state resolver, the coupon service and handler, and the
// coupon admin routes mounted on the production router. Service-level tests use
// service/db, HTTP-level tests use router/bareRouter.
//
// Two administrators are bootstrapped: superAdmin holds coupon:read and
// coupon:write (0006 grants both to super_admin), readlessAdmin holds a fixture
// role with no permission at all, so the permission gate's denial path is
// driven by the real resolver instead of a fake.
type couponFixture struct {
	db            *gorm.DB
	service       *coupon.Service
	adminService  *admin.Service
	handler       *coupon.Handler
	logger        *slog.Logger
	signer        *tokens.Signer
	cookie        config.AdminCookieConfig
	router        http.Handler
	bareRouter    http.Handler
	auditor       *coupon.PermissionDeniedAuditor
	superAdmin    couponAdmin
	readlessAdmin couponAdmin
	now           time.Time
	actor         coupon.Actor
}

// couponAdmin is a bootstrapped administrator plus the session material the
// admin cookie middleware verifies (token_version freshness, enabled flag).
type couponAdmin struct {
	id           uid.ID
	roleName     string
	tokenVersion int64
	token        string
}

func newCouponFixture(t *testing.T) *couponFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	// The service-level assertions read table-wide aggregates (ListTemplates
	// reports the total over coupon_templates) and the fixture bootstraps
	// administrators by role name, so the coupon tables are taken back to a
	// known empty state first. tests/e2e/harness.go ResetDB does the same for the
	// whole schema; this one stops at the two tables the coupon package owns.
	// audit_logs is deliberately left untouched: 0001 makes it append-only and
	// every audit assertion below is scoped to this fixture's own actor or
	// target, so rows left by earlier test cases (or other suites) cannot satisfy
	// them.
	if err := db.Exec(`DELETE FROM user_coupons`).Error; err != nil {
		t.Fatalf("clear user_coupons: %v", err)
	}
	if err := db.Exec(`DELETE FROM coupon_templates`).Error; err != nil {
		t.Fatalf("clear coupon_templates: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	now := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	adminService, err := admin.NewService(admin.ServiceDeps{
		DB: db, Logger: logger, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
	// A role outside the seed list carries no coupon permission, which is what
	// the "no permission to create / view" cases need; it still has to exist as
	// a row because BootstrapAdministrator resolves the role by name. Roles are
	// part of the migration seed and are never cleared, so the insert is
	// idempotent (the same shape tests/e2e and tests/security use).
	if err := db.Exec(
		`INSERT INTO roles (id, name, remark) VALUES (?, 'coupon_fixture_readless', 'fixture: role without coupon permissions')
		 ON CONFLICT (name) DO NOTHING`,
		uid.New(),
	).Error; err != nil {
		t.Fatalf("insert readless role: %v", err)
	}
	superAdmin := bootstrapCouponAdmin(t, adminService, couponFixtureAdminName("coupon-fix-super-"), "super_admin")
	readlessAdmin := bootstrapCouponAdmin(t, adminService, couponFixtureAdminName("coupon-fix-readless-"), "coupon_fixture_readless")

	service, err := coupon.NewService(coupon.ServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("new coupon service: %v", err)
	}
	handler, err := coupon.NewHandler(coupon.HandlerDeps{
		Service: service, AdminViewer: adminService, Logger: logger,
	})
	if err != nil {
		t.Fatalf("new coupon handler: %v", err)
	}
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "coupon-test-issuer",
		Audience:  "coupon-test-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "coupon-test-kid",
		Keys: map[string][]byte{
			"coupon-test-kid": []byte("coupon-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	cookie := config.DefaultAdminCookieConfig()
	for _, adm := range []*couponAdmin{&superAdmin, &readlessAdmin} {
		token, err := signer.Issue("admin:"+adm.id.String(), adm.tokenVersion, now)
		if err != nil {
			t.Fatalf("issue admin token: %v", err)
		}
		adm.token = token
	}
	auditor := coupon.NewPermissionDeniedAuditor(db, adminService, logger)
	cfg := config.Config{
		PublicBaseURL:  "https://api.shop-mall.invalid",
		AllowedOrigins: []string{couponTestAdminOrigin},
		AdminCookie:    cookie,
	}
	router := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			coupon.RegisterAdminRoutes(group, coupon.AdminRouteDeps{
				Handler:            handler,
				Signer:             signer,
				Cookie:             cookie,
				Now:                func() time.Time { return now },
				PermissionResolver: adminService,
				AdminStateResolver: adminService,
				DeniedAuditor:      auditor,
			})
		},
	})
	// bareRouter mounts the same handler methods with no authentication or
	// permission middleware, so the handler's own missing-identity mapping is
	// reachable.
	bare := gin.New()
	bare.POST("/api/admin/v1/coupon-templates", handler.AdminCreateTemplate)
	bare.PATCH("/api/admin/v1/coupon-templates/:templateId", handler.AdminUpdateTemplateStatus)
	bare.GET("/api/admin/v1/coupon-templates", handler.AdminListTemplates)
	return &couponFixture{
		db:            db,
		service:       service,
		adminService:  adminService,
		handler:       handler,
		logger:        logger,
		signer:        signer,
		cookie:        cookie,
		router:        router,
		bareRouter:    bare,
		auditor:       auditor,
		superAdmin:    superAdmin,
		readlessAdmin: readlessAdmin,
		now:           now,
		actor:         coupon.Actor{AdminID: superAdmin.id, RoleName: superAdmin.roleName},
	}
}

// couponFixtureAdminName returns a per-fixture administrator username. The
// fixture never removes administrators: bootstrap writes an audit row and
// audit_logs.actor_admin_id references admin_users ON DELETE RESTRICT, so a
// fixed username would answer ErrAdministratorExists as soon as a second test
// case shares the same database (TEST_DATABASE_URL set). The suffix is the
// random tail of a UUIDv7, which keeps the name inside the 32 character limit
// ValidateUsername enforces.
func couponFixtureAdminName(prefix string) string {
	return prefix + strings.ReplaceAll(uid.New().String(), "-", "")[20:]
}

func bootstrapCouponAdmin(t *testing.T, adminService *admin.Service, username, roleName string) couponAdmin {
	t.Helper()
	result, err := adminService.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  username,
		Password:  "Sup3rSecret!Pass",
		RoleName:  roleName,
		ActorName: "coupon-fixture",
	})
	if err != nil {
		t.Fatalf("bootstrap %s: %v", username, err)
	}
	return couponAdmin{id: result.AdminID, roleName: result.RoleName, tokenVersion: result.TokenVersion}
}

// perform drives a request through the supplied router with the administrator's
// session cookie. Write methods carry the Origin and CSRF material the admin
// group's middleware requires; a nil administrator sends an unauthenticated
// request.
func (f *couponFixture) perform(t *testing.T, router http.Handler, adm *couponAdmin, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	write := method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete || method == http.MethodPut
	if adm != nil {
		req.AddCookie(&http.Cookie{Name: f.cookie.AccessTokenName, Value: adm.token})
	}
	if write {
		req.Header.Set("Origin", couponTestAdminOrigin)
		const csrf = "coupon-test-csrf-token"
		req.AddCookie(&http.Cookie{Name: f.cookie.CSRFName, Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// createTemplate is the fixture's direct service-level template creation.
func (f *couponFixture) createTemplate(t *testing.T, name string, threshold, discount int64, total, perUser int32) coupon.TemplateView {
	t.Helper()
	view, err := f.service.CreateTemplate(context.Background(), f.actor, coupon.TemplateInput{
		Name:            name,
		ThresholdPoints: threshold,
		DiscountPoints:  discount,
		TotalCount:      total,
		PerUserLimit:    perUser,
		ValidFrom:       f.now.Add(-time.Hour),
		ValidUntil:      f.now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create template %q: %v", name, err)
	}
	return view
}

// validInput is the create payload the success cases submit.
func (f *couponFixture) validInput(name string) coupon.TemplateInput {
	return coupon.TemplateInput{
		Name:            name,
		ThresholdPoints: 500,
		DiscountPoints:  100,
		TotalCount:      20,
		PerUserLimit:    2,
		ValidFrom:       f.now.Add(-time.Hour),
		ValidUntil:      f.now.Add(24 * time.Hour),
	}
}

func (f *couponFixture) closePool() {
	if sqlDB, err := f.db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func TestNewServiceRequiresDatabase(t *testing.T) {
	if _, err := coupon.NewService(coupon.ServiceDeps{}); err == nil {
		t.Fatal("NewService without a database returned no error")
	}
}

func TestCreateTemplatePersistsActiveTemplateWithZeroReceivedAndAudit(t *testing.T) {
	fix := newCouponFixture(t)
	input := fix.validInput("创建成功-券模板")
	input.Name = "  " + input.Name + "  "

	view, err := fix.service.CreateTemplate(context.Background(), fix.actor, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if uid.IsZero(view.ID) {
		t.Fatal("created template has no id")
	}
	if view.Status != coupon.StatusActive {
		t.Fatalf("status = %q, want %q", view.Status, coupon.StatusActive)
	}
	if view.ReceivedCount != 0 {
		t.Fatalf("received_count = %d, want 0", view.ReceivedCount)
	}

	var stored database.CouponTemplate
	if err := fix.db.Where("id = ?", view.ID).First(&stored).Error; err != nil {
		t.Fatalf("read stored template: %v", err)
	}
	if stored.Name != "创建成功-券模板" {
		t.Fatalf("stored name = %q, want the trimmed input", stored.Name)
	}
	if stored.Status != database.CouponTemplateStatusActive {
		t.Fatalf("stored status = %q, want active", stored.Status)
	}
	if stored.ReceivedCount != 0 || stored.TotalCount != 20 || stored.PerUserLimit != 2 {
		t.Fatalf("stored counters = received %d total %d per_user %d, want 0/20/2",
			stored.ReceivedCount, stored.TotalCount, stored.PerUserLimit)
	}
	if stored.ThresholdPoints != 500 || stored.DiscountPoints != 100 {
		t.Fatalf("stored rules = %d/%d, want 500/100", stored.ThresholdPoints, stored.DiscountPoints)
	}

	var auditRow struct {
		Action     string
		Result     string
		ActorRole  string
		ActorAdmin uid.ID
		AfterData  string
	}
	if err := fix.db.Raw(
		`SELECT action, result, actor_role, actor_admin_id AS actor_admin, after_data::text AS after_data
		 FROM audit_logs WHERE target_type = 'coupon_template' AND target_id = ?`, view.ID,
	).Row().Scan(&auditRow.Action, &auditRow.Result, &auditRow.ActorRole, &auditRow.ActorAdmin, &auditRow.AfterData); err != nil {
		t.Fatalf("read create audit row: %v", err)
	}
	if auditRow.Action != "coupon_template.create" || auditRow.Result != "success" {
		t.Fatalf("audit = %q/%q, want coupon_template.create/success", auditRow.Action, auditRow.Result)
	}
	if auditRow.ActorAdmin != fix.superAdmin.id || auditRow.ActorRole != "super_admin" {
		t.Fatalf("audit actor = %s/%q, want %s/super_admin", auditRow.ActorAdmin, auditRow.ActorRole, fix.superAdmin.id)
	}
	if auditRow.AfterData == "" {
		t.Fatal("create audit row carries no after image")
	}
}

func TestCreateTemplateRejectsInvalidRulesWithoutWriting(t *testing.T) {
	fix := newCouponFixture(t)
	base := fix.validInput("规则非法")
	cases := []struct {
		name   string
		mutate func(*coupon.TemplateInput)
	}{
		{"threshold is zero", func(in *coupon.TemplateInput) { in.ThresholdPoints = 0 }},
		{"threshold is negative", func(in *coupon.TemplateInput) { in.ThresholdPoints = -500 }},
		{"discount is zero", func(in *coupon.TemplateInput) { in.DiscountPoints = 0 }},
		{"discount is negative", func(in *coupon.TemplateInput) { in.DiscountPoints = -100 }},
		{"discount equals threshold", func(in *coupon.TemplateInput) { in.DiscountPoints = in.ThresholdPoints }},
		{"discount exceeds threshold", func(in *coupon.TemplateInput) { in.DiscountPoints = in.ThresholdPoints + 1 }},
		{"total count is zero", func(in *coupon.TemplateInput) { in.TotalCount = 0 }},
		{"total count is negative", func(in *coupon.TemplateInput) { in.TotalCount = -1 }},
		{"per user limit is zero", func(in *coupon.TemplateInput) { in.PerUserLimit = 0 }},
		{"per user limit is negative", func(in *coupon.TemplateInput) { in.PerUserLimit = -3 }},
		{"valid until equals valid from", func(in *coupon.TemplateInput) { in.ValidUntil = in.ValidFrom }},
		{"valid until precedes valid from", func(in *coupon.TemplateInput) { in.ValidUntil = in.ValidFrom.Add(-time.Minute) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.mutate(&input)
			if _, err := fix.service.CreateTemplate(context.Background(), fix.actor, input); !errors.Is(err, coupon.ErrInvalidTemplate) {
				t.Fatalf("error = %v, want ErrInvalidTemplate", err)
			}
		})
	}

	// A rejected submission leaves neither a template row nor an audit row
	// behind (specs/coupon/spec.md 规则非法).
	var templates, audits int64
	if err := fix.db.Model(&database.CouponTemplate{}).Count(&templates).Error; err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'coupon_template.create' AND actor_admin_id = ?`, fix.superAdmin.id,
	).Scan(&audits).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if templates != 0 || audits != 0 {
		t.Fatalf("rejected submissions left %d templates and %d audit rows, want 0/0", templates, audits)
	}
}

// TestCreateTemplateAcceptsDiscountJustBelowThreshold pins the boundary the
// discount_below_threshold rule allows: discount == threshold - 1 is legal, so
// the rule is strict ("not less than"), not "must be at most half".
func TestCreateTemplateAcceptsDiscountJustBelowThreshold(t *testing.T) {
	fix := newCouponFixture(t)
	input := fix.validInput("边界-抵扣差一")
	input.ThresholdPoints = 100
	input.DiscountPoints = 99
	view, err := fix.service.CreateTemplate(context.Background(), fix.actor, input)
	if err != nil {
		t.Fatalf("create with discount = threshold-1: %v", err)
	}
	if view.DiscountPoints != 99 || view.ThresholdPoints != 100 {
		t.Fatalf("rules = %d/%d, want 99/100", view.DiscountPoints, view.ThresholdPoints)
	}
}

func TestListTemplatesPagesNewestFirstAndCountsUsedCoupons(t *testing.T) {
	fix := newCouponFixture(t)
	first := fix.createTemplate(t, "列表-最早", 500, 100, 10, 1)
	second := fix.createTemplate(t, "列表-居中", 500, 100, 10, 1)
	third := fix.createTemplate(t, "列表-最新", 500, 100, 10, 1)
	userID := fix.seedUser(t, "coupon-list-user", 1000000)
	// first: two redeemed coupons; second: one redeemed; third: none.
	used := []uid.ID{first.ID, first.ID, second.ID}
	for i, templateID := range used {
		fix.seedUserCoupon(t, userID, templateID, "used", fmt.Sprintf("req-%d", i))
	}
	// An available coupon must not inflate the redeemed count.
	fix.seedUserCoupon(t, userID, first.ID, "available", "req-available")

	// The default page: all three templates, newest (id DESC) first, each row
	// carrying its redeemed count and the template it belongs to.
	items, total, err := fix.service.ListTemplates(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("list = %d items / total %d, want 3/3", len(items), total)
	}
	if items[0].ID != third.ID || items[1].ID != second.ID || items[2].ID != first.ID {
		t.Fatalf("order = [%s %s %s], want newest first [%s %s %s]",
			items[0].ID, items[1].ID, items[2].ID, third.ID, second.ID, first.ID)
	}
	if items[0].UsedCount != 0 {
		t.Fatalf("newest used_count = %d, want 0", items[0].UsedCount)
	}
	if items[1].UsedCount != 1 {
		t.Fatalf("middle used_count = %d, want 1", items[1].UsedCount)
	}
	if items[2].UsedCount != 2 {
		t.Fatalf("oldest used_count = %d, want 2 (only status='used' counts)", items[2].UsedCount)
	}
	if items[2].Name != "列表-最早" || items[2].Status != coupon.StatusActive {
		t.Fatalf("row projection = %q/%q, want 列表-最早/active", items[2].Name, items[2].Status)
	}

	// A halted template stays in the list: nothing filters on the issuance
	// status.
	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, second.ID, coupon.StatusHalted); err != nil {
		t.Fatalf("halt template: %v", err)
	}
	all, total, err := fix.service.ListTemplates(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("list templates after halt: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("halted template dropped from the list: %d/%d, want 3/3", len(all), total)
	}

	// Paging: page 2 with page_size 2 holds exactly the oldest template.
	page2, total, err := fix.service.ListTemplates(context.Background(), 2, 2)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total != 3 || len(page2) != 1 || page2[0].ID != first.ID {
		t.Fatalf("page 2 = %d rows / total %d, want the single oldest %s", len(page2), total, first.ID)
	}

	// A page beyond the data returns an empty page with the same total, which
	// is the empty-input path of the redeemed-count aggregate.
	past, total, err := fix.service.ListTemplates(context.Background(), 99, 20)
	if err != nil {
		t.Fatalf("list far page: %v", err)
	}
	if total != 3 || len(past) != 0 {
		t.Fatalf("far page = %d rows / total %d, want 0/3", len(past), total)
	}
}

// TestListTemplatesNormalizesOutOfRangePaging covers the page/page_size
// normalization: page below 1 becomes 1, page_size outside 1..100 becomes the
// default 20, and the boundary values 1 and 100 are honoured as sent.
func TestListTemplatesNormalizesOutOfRangePaging(t *testing.T) {
	fix := newCouponFixture(t)
	fix.createTemplate(t, "分页-1", 100, 10, 5, 1)
	fix.createTemplate(t, "分页-2", 100, 10, 5, 1)
	third := fix.createTemplate(t, "分页-3", 100, 10, 5, 1)

	cases := []struct {
		name     string
		page     int
		pageSize int
		wantLen  int
		wantHead uid.ID
	}{
		{"page zero falls back to page 1", 0, 2, 2, third.ID},
		{"negative page falls back to page 1", -5, 2, 2, third.ID},
		{"page size zero falls back to the default 20", 1, 0, 3, third.ID},
		{"page size above the maximum falls back to the default 20", 1, 101, 3, third.ID},
		{"negative page size falls back to the default 20", 1, -1, 3, third.ID},
		{"page size 1 is honoured", 1, 1, 1, third.ID},
		{"page size 100 is honoured", 1, 100, 3, third.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := fix.service.ListTemplates(context.Background(), tc.page, tc.pageSize)
			if err != nil {
				t.Fatalf("list templates: %v", err)
			}
			if total != 3 {
				t.Fatalf("total = %d, want 3", total)
			}
			if len(items) != tc.wantLen {
				t.Fatalf("rows = %d, want %d", len(items), tc.wantLen)
			}
			if items[0].ID != tc.wantHead {
				t.Fatalf("head = %s, want the newest %s", items[0].ID, tc.wantHead)
			}
		})
	}
}

func TestSetTemplateStatusSwitchesAndAuditsBothDirections(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "切换-券模板", 500, 100, 10, 2)

	halted, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, coupon.StatusHalted)
	if err != nil {
		t.Fatalf("halt template: %v", err)
	}
	if halted.Status != coupon.StatusHalted {
		t.Fatalf("returned status = %q, want halted", halted.Status)
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusHalted)

	active, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, coupon.StatusActive)
	if err != nil {
		t.Fatalf("resume template: %v", err)
	}
	if active.Status != coupon.StatusActive {
		t.Fatalf("returned status = %q, want active", active.Status)
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusActive)

	// Both switches leave exactly one success audit row naming the actor and
	// carrying the before/after status snapshots.
	var rows []struct {
		ActorRole  string
		ActorAdmin uid.ID
		Before     string
		After      string
	}
	if err := fix.db.Raw(
		`SELECT actor_role, actor_admin_id AS actor_admin, before_data::text AS before, after_data::text AS after
		 FROM audit_logs WHERE action = 'coupon_template.status_change' AND target_id = ? ORDER BY created_at, id`, view.ID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("read status-change audits: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("status-change audit rows = %d, want 2", len(rows))
	}
	if rows[0].ActorAdmin != fix.superAdmin.id || rows[0].ActorRole != "super_admin" {
		t.Fatalf("audit actor = %s/%q, want %s/super_admin", rows[0].ActorAdmin, rows[0].ActorRole, fix.superAdmin.id)
	}
	if !strings.Contains(rows[0].Before, `"status": "active"`) || !strings.Contains(rows[0].After, `"status": "halted"`) {
		t.Fatalf("halt audit before/after = %s / %s, want active → halted", rows[0].Before, rows[0].After)
	}
	if !strings.Contains(rows[1].Before, `"status": "halted"`) || !strings.Contains(rows[1].After, `"status": "active"`) {
		t.Fatalf("resume audit before/after = %s / %s, want halted → active", rows[1].Before, rows[1].After)
	}
}

func TestSetTemplateStatusRejectsUnknownTemplateAndUnknownStatus(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "切换-拒因", 500, 100, 10, 2)

	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, uid.ID{}, coupon.StatusHalted); !errors.Is(err, coupon.ErrTemplateNotFound) {
		t.Fatalf("zero id error = %v, want ErrTemplateNotFound", err)
	}
	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, uid.New(), coupon.StatusHalted); !errors.Is(err, coupon.ErrTemplateNotFound) {
		t.Fatalf("missing template error = %v, want ErrTemplateNotFound", err)
	}
	for _, status := range []coupon.Status{"paused", "ACTIVE", "", " active "} {
		status := status
		t.Run(fmt.Sprintf("status %q", string(status)), func(t *testing.T) {
			if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, status); !errors.Is(err, coupon.ErrInvalidTemplate) {
				t.Fatalf("error = %v, want ErrInvalidTemplate", err)
			}
		})
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusActive)

	// Rejected switches leave no audit row behind.
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

// TestServiceSurfacesDatabaseFailures closes the connection pool, so the write
// path's transaction failure and the read path's query failure surface as
// errors instead of a panic or a silently empty result.
func TestServiceSurfacesDatabaseFailures(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "故障-券模板", 500, 100, 10, 2)
	fix.closePool()

	if _, err := fix.service.CreateTemplate(context.Background(), fix.actor, fix.validInput("故障-创建")); err == nil {
		t.Fatal("create on a closed pool returned no error")
	}
	if _, _, err := fix.service.ListTemplates(context.Background(), 1, 20); err == nil {
		t.Fatal("list on a closed pool returned no error")
	}
	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, coupon.StatusHalted); err == nil {
		t.Fatal("status switch on a closed pool returned no error")
	}
}

// TestCreateTemplateRollsBackWhenStorageRejectsTheWrite drives the insert into
// a storage-level rejection (the name column is VARCHAR(64)): the failed write
// must surface as an error and the audit row must roll back with it, so a
// recorded creation always has a template behind it.
func TestCreateTemplateRollsBackWhenStorageRejectsTheWrite(t *testing.T) {
	fix := newCouponFixture(t)
	input := fix.validInput(strings.Repeat("a", 65))

	if _, err := fix.service.CreateTemplate(context.Background(), fix.actor, input); err == nil {
		t.Fatal("create with an over-long name returned no error")
	}
	var templates, audits int64
	if err := fix.db.Model(&database.CouponTemplate{}).Count(&templates).Error; err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'coupon_template.create' AND actor_admin_id = ?`, fix.superAdmin.id,
	).Scan(&audits).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if templates != 0 || audits != 0 {
		t.Fatalf("failed write left %d templates and %d audit rows, want 0/0", templates, audits)
	}
}

// TestListTemplatesSurfacesRedeemedCountQueryFailure covers the folded query
// path: with the template page readable but user_coupons gone, the list must
// return the failure instead of rows whose redeemed count silently reads zero.
func TestListTemplatesSurfacesRedeemedCountQueryFailure(t *testing.T) {
	fix := newCouponFixture(t)
	fix.createTemplate(t, "故障-核销数", 500, 100, 10, 1)
	fix.renameTable(t, "user_coupons", "user_coupons_hidden")

	if _, _, err := fix.service.ListTemplates(context.Background(), 1, 20); err == nil {
		t.Fatal("list returned no error while the redeemed-count query was failing")
	}
}

// TestSetTemplateStatusSurfacesPreImageReadFailure covers the read-back failure
// before the switch: a template the service cannot read must surface as an
// error, not as a successful switch on an empty row.
func TestSetTemplateStatusSurfacesPreImageReadFailure(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "故障-前值", 500, 100, 10, 1)
	fix.renameTable(t, "coupon_templates", "coupon_templates_hidden")

	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, coupon.StatusHalted); err == nil {
		t.Fatal("status switch returned no error while the template table was unreadable")
	}
}

// TestListTemplatesSurfacesPageQueryFailure covers the paged read: with the
// table still count-able but its newest-first page query failing, the list must
// return that failure instead of an empty page behind a non-zero total.
//
// The count query (SELECT count(*) FROM coupon_templates) names no column, so
// renaming id leaves it working while `ORDER BY id DESC` fails — the state that
// separates the Count return from the Find return in service.go.
func TestListTemplatesSurfacesPageQueryFailure(t *testing.T) {
	fix := newCouponFixture(t)
	fix.createTemplate(t, "故障-分页", 500, 100, 10, 1)
	fix.renameColumn(t, "coupon_templates", "id", "id_hidden")

	var countable int64
	if err := fix.db.Raw(`SELECT count(*) FROM coupon_templates`).Scan(&countable).Error; err != nil {
		t.Fatalf("precondition: the template count must stay readable, got %v", err)
	}
	if countable != 1 {
		t.Fatalf("precondition: countable templates = %d, want the fixture's 1", countable)
	}

	_, _, err := fix.service.ListTemplates(context.Background(), 1, 20)
	if err == nil {
		t.Fatal("list returned no error while the page query was failing")
	}
	// SQLSTATE 42703 (undefined column) is only reachable through the page
	// query: the count query above cannot produce it, so this pins which of the
	// two statements surfaced the failure.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42703" {
		t.Fatalf("error = %v, want the page query's undefined-column failure (SQLSTATE 42703)", err)
	}
}

// TestSetTemplateStatusSurfacesUpdateFailureAndKeepsTheStoredStatus makes the
// UPDATE itself fail through a storage constraint: the switch must surface the
// error, leave the stored status untouched and write no success audit row.
//
// The two guards that run after that UPDATE in service.go are defensive: the
// read-back of the switched row and the audit insert fail only on a synthetic
// storage fault that production does not produce (a BEFORE UPDATE trigger that
// removes the row, a storage rejection of the audit insert). The reachable
// failures are the ones the tests around this one drive.
func TestSetTemplateStatusSurfacesUpdateFailureAndKeepsTheStoredStatus(t *testing.T) {
	fix := newCouponFixture(t)
	view := fix.createTemplate(t, "故障-更新", 500, 100, 10, 1)
	if err := fix.db.Exec(
		`ALTER TABLE coupon_templates ADD CONSTRAINT coupon_templates_test_halted_forbidden CHECK (status <> 'halted')`,
	).Error; err != nil {
		t.Fatalf("add fixture constraint: %v", err)
	}
	// The constraint stays out of the shared schema once the test ends: a
	// database that forbids 'halted' would fail every later status switch.
	t.Cleanup(func() {
		if err := fix.db.Exec(`ALTER TABLE coupon_templates DROP CONSTRAINT IF EXISTS coupon_templates_test_halted_forbidden`).Error; err != nil {
			t.Errorf("drop fixture constraint: %v", err)
		}
	})

	if _, err := fix.service.SetTemplateStatus(context.Background(), fix.actor, view.ID, coupon.StatusHalted); err == nil {
		t.Fatal("status switch returned no error while the UPDATE was rejected")
	}
	assertStoredStatus(t, fix, view.ID, database.CouponTemplateStatusActive)
	var audits int64
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'coupon_template.status_change' AND target_id = ?`, view.ID,
	).Scan(&audits).Error; err != nil {
		t.Fatalf("count status-change audits: %v", err)
	}
	if audits != 0 {
		t.Fatalf("rejected switch wrote %d audit rows, want 0", audits)
	}
}

// renameTable renames a table inside the fixture's own database, so a test can
// drive a query failure without stopping the connection pool. The original name
// is restored when the test ends: with TEST_DATABASE_URL set the schema is
// shared, and a table left under its temporary name would fail every later test
// in the same database.
func (f *couponFixture) renameTable(t *testing.T, from, to string) {
	t.Helper()
	if err := f.db.Exec("ALTER TABLE " + from + " RENAME TO " + to).Error; err != nil {
		t.Fatalf("rename %s: %v", from, err)
	}
	t.Cleanup(func() {
		if err := f.db.Exec("ALTER TABLE " + to + " RENAME TO " + from).Error; err != nil {
			t.Errorf("restore table name %s: %v", from, err)
		}
	})
}

// renameColumn renames a column for the duration of the test, the same
// fault-injection idiom renameTable uses, restored the same way. It drives a
// query failure in a statement that still resolves the table and its row count.
func (f *couponFixture) renameColumn(t *testing.T, table, from, to string) {
	t.Helper()
	if err := f.db.Exec("ALTER TABLE " + table + " RENAME COLUMN " + from + " TO " + to).Error; err != nil {
		t.Fatalf("rename column %s.%s: %v", table, from, err)
	}
	t.Cleanup(func() {
		if err := f.db.Exec("ALTER TABLE " + table + " RENAME COLUMN " + to + " TO " + from).Error; err != nil {
			t.Errorf("restore column name %s.%s: %v", table, from, err)
		}
	})
}

// withTableHidden runs fn with one table renamed away and restores the name
// before returning, so the test can keep querying the rest of the schema (and
// the connection pool) while the writes that touch that table fail.
func (f *couponFixture) withTableHidden(t *testing.T, table string, fn func()) {
	t.Helper()
	hidden := table + "__hidden"
	if err := f.db.Exec("ALTER TABLE " + table + " RENAME TO " + hidden).Error; err != nil {
		t.Fatalf("hide table %s: %v", table, err)
	}
	defer func() {
		if err := f.db.Exec("ALTER TABLE " + hidden + " RENAME TO " + table).Error; err != nil {
			t.Errorf("restore table name %s: %v", table, err)
		}
	}()
	fn()
}

func assertStoredStatus(t *testing.T, fix *couponFixture, id uid.ID, want database.CouponTemplateStatus) {
	t.Helper()
	var stored database.CouponTemplate
	if err := fix.db.Where("id = ?", id).First(&stored).Error; err != nil {
		t.Fatalf("read stored template: %v", err)
	}
	if stored.Status != want {
		t.Fatalf("stored status = %q, want %q", stored.Status, want)
	}
}

// seedUser inserts a buyer row, which user_coupons requires through its
// user_id foreign key. users.openid is unique and the row outlives the fixture,
// so a second run against the same database reuses the existing buyer.
func (f *couponFixture) seedUser(t *testing.T, openid string, points int64) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO users (id, openid, points_balance) VALUES (?, ?, ?) ON CONFLICT (openid) DO NOTHING`,
		uid.New(), openid, points,
	).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *couponFixture) seedUserCoupon(t *testing.T, userID, templateID uid.ID, status, requestID string) uid.ID {
	t.Helper()
	if err := f.db.Exec(
		`INSERT INTO user_coupons (id, user_id, template_id, status, request_id) VALUES (?, ?, ?, ?, ?)`,
		uid.New(), userID, templateID, status, requestID,
	).Error; err != nil {
		t.Fatalf("insert user coupon: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM user_coupons WHERE request_id = ?`, requestID).Row().Scan(&id); err != nil {
		t.Fatalf("read user coupon id: %v", err)
	}
	return id
}
