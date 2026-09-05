package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const testImagePublicBaseURL = "https://api.example.test"

type imageFixture struct {
	db        *gorm.DB
	service   *admin.Service
	signer    *tokens.Signer
	cookieCfg config.AdminCookieConfig
	now       time.Time
	router    http.Handler
	volume    *storage.LocalVolume
}

func newImageFixture(t *testing.T, uploadLimit, maxPixels, capacity int64) *imageFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	service, err := admin.NewService(admin.ServiceDeps{DB: db, Logger: logger, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
	limiter := admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
		AccountLimit:  10,
		AccountWindow: time.Minute,
		IPLimit:       5,
		IPWindow:      time.Minute,
	})
	limiter.SetNow(func() time.Time { return now })
	service.SetRateLimiter(limiter)
	signer, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "admin-test-issuer",
		Audience:  "admin-test-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-test-kid",
		Keys: map[string][]byte{
			"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	volume, err := storage.NewLocalVolume(t.TempDir(), capacity)
	if err != nil {
		t.Fatalf("new volume: %v", err)
	}
	imageHandler, err := admin.NewImageHandler(admin.ImageHandlerDeps{
		Store:         volume,
		Capacity:      volume,
		CapacityLimit: capacity,
		UploadLimit:   uploadLimit,
		MaxPixels:     maxPixels,
		PublicBaseURL: testImagePublicBaseURL,
		DB:            db,
		Service:       service,
		Logger:        logger,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new image handler: %v", err)
	}
	cookieCfg := config.DefaultAdminCookieConfig()
	cfg := config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    cookieCfg,
		AdminJWT: config.JWTConfig{
			Issuer:    "admin-test-issuer",
			Audience:  "admin-test-audience",
			TTL:       30 * time.Minute,
			ActiveKID: "admin-test-kid",
			Keys: map[string][]byte{
				"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
			},
		},
	}
	router := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger: logger,
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			handler, err := admin.NewHandler(admin.HandlerDeps{
				Service:     service,
				Signer:      signer,
				RateLimiter: limiter,
				Logger:      logger,
				Cookie:      cookieCfg,
				Now:         func() time.Time { return now },
			})
			if err != nil {
				panic(err)
			}
			admin.RegisterRoutes(group, handler)
			admin.RegisterImageRoutes(group, admin.ImageRouteDeps{
				Handler:            imageHandler,
				Signer:             signer,
				Cookie:             cookieCfg,
				Now:                func() time.Time { return now },
				PermissionResolver: service,
				AdminStateResolver: service,
			})
		},
	})
	return &imageFixture{
		db:        db,
		service:   service,
		signer:    signer,
		cookieCfg: cookieCfg,
		now:       now,
		router:    router,
		volume:    volume,
	}
}

func (f *imageFixture) login(t *testing.T) (access, csrf string) {
	t.Helper()
	if _, err := f.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "image-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "test",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	body := strings.NewReader(`{"username":"image-admin","password":"Sup3rSecret!Pass"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, raw := range rec.Header().Values("Set-Cookie") {
		parsed, _ := http.ParseSetCookie(raw)
		switch parsed.Name {
		case f.cookieCfg.AccessTokenName:
			access = parsed.Value
		case f.cookieCfg.CSRFName:
			csrf = parsed.Value
		}
	}
	if access == "" || csrf == "" {
		t.Fatal("login did not set both cookies")
	}
	return access, csrf
}

func (f *imageFixture) upload(t *testing.T, filename, contentType string, content []byte, access, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	headers := map[string][]string{
		"Content-Disposition": []string{"form-data; name=\"file\"; filename=\"" + filename + "\""},
	}
	if contentType != "" {
		headers["Content-Type"] = []string{contentType}
	}
	part, err := writer.CreatePart(headers)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/images", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.AccessTokenName, Value: access})
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.CSRFName, Value: csrf})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *imageFixture) uploadWithoutAuth(t *testing.T, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "x.png")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/images", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://admin.example.test")
	// A matching CSRF pair is supplied so the request passes the CSRF
	// middleware; the missing access token is then rejected by AdminAuth.
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.CSRFName, Value: "csrf-for-test"})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *imageFixture) uploadWithToken(t *testing.T, token string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "x.png")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/images", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.AccessTokenName, Value: token})
	req.AddCookie(&http.Cookie{Name: f.cookieCfg.CSRFName, Value: "csrf-for-test"})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func generateTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestImageUploadAcceptsPNG(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 10<<20)
	access, csrf := fix.login(t)
	rec := fix.upload(t, "photo.png", "image/png", generateTestPNG(t), access, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	key := data["key"].(string)
	url := data["url"].(string)
	if !strings.HasSuffix(key, ".png") {
		t.Fatalf("key %q must end with .png", key)
	}
	if url != testImagePublicBaseURL+"/static/images/"+key {
		t.Fatalf("url = %q, want %q", url, testImagePublicBaseURL+"/static/images/"+key)
	}
	if data["width"].(float64) != 8 || data["height"].(float64) != 8 {
		t.Fatalf("dimensions mismatch: %v", data)
	}
	used, err := fix.volume.UsedBytes(context.Background())
	if err != nil {
		t.Fatalf("used bytes: %v", err)
	}
	if used == 0 {
		t.Fatal("image was not persisted")
	}
}

func TestImageUploadRejectsSpoofedNonImageWith415(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 10<<20)
	access, csrf := fix.login(t)
	html := []byte("<!DOCTYPE html><html><body>not an image</body></html>")
	rec := fix.upload(t, "evil.png", "image/png", html, access, csrf)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("spoofed html status = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
	gif := []byte("GIF89a" + strings.Repeat("\x00", 20))
	rec = fix.upload(t, "anim.gif", "image/gif", gif, access, csrf)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("gif status = %d, want 415", rec.Code)
	}
	// The declared Content-Type does not influence validation: a valid PNG
	// declared as text/plain is accepted because its magic bytes are a PNG.
	rec = fix.upload(t, "photo.png", "text/plain", generateTestPNG(t), access, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("png-with-lied-content-type status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageUploadRejectsInvalidBytesWith422(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 10<<20)
	access, csrf := fix.login(t)
	truncated := generateTestPNG(t)[:14]
	rec := fix.upload(t, "broken.png", "image/png", truncated, access, csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("truncated status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageUploadRejectsOversizedWith413(t *testing.T) {
	fix := newImageFixture(t, 64, 25_000_000, 10<<20)
	access, csrf := fix.login(t)
	rec := fix.upload(t, "big.png", "image/png", generateTestPNG(t), access, csrf)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageUploadRejectsPixelLimitWith422(t *testing.T) {
	// The 8x8 fixture is 64 pixels; a limit of 30 must reject it.
	fix := newImageFixture(t, 2<<20, 30, 10<<20)
	access, csrf := fix.login(t)
	rec := fix.upload(t, "huge.png", "image/png", generateTestPNG(t), access, csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("pixel-limited status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageUploadRejectsStorageFullWith507(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 1)
	access, csrf := fix.login(t)
	rec := fix.upload(t, "full.png", "image/png", generateTestPNG(t), access, csrf)
	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("storage-full status = %d, want 507; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageUploadRequiresAuthentication(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 10<<20)
	rec := fix.uploadWithoutAuth(t, generateTestPNG(t))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload status = %d, want 401", rec.Code)
	}
}

func TestImageUploadWithoutImageWritePermissionIs403(t *testing.T) {
	fix := newImageFixture(t, 2<<20, 25_000_000, 10<<20)
	mustExecAdmin(t, fix.db, `INSERT INTO roles (id, name, remark) VALUES (?, 'limited', '')`, uid.New())
	mustExecAdmin(t, fix.db, `INSERT INTO role_permissions (role_id, permission_id)
		SELECT r.id, p.id FROM roles r JOIN permissions p ON p.code = 'admin:self' WHERE r.name = 'limited'`)
	limited, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "limited-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "limited",
		ActorName: "test",
	})
	if err != nil {
		t.Fatalf("bootstrap limited admin: %v", err)
	}
	token, err := fix.signer.Issue("admin:"+limited.AdminID.String(), limited.TokenVersion, fix.now)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	rec := fix.uploadWithToken(t, token, generateTestPNG(t))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload without image:write status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func mustExecAdmin(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}
