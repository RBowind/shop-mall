package user

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func generateTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func newAvatarFixture(t *testing.T) (*ProfileHandler, *gorm.DB, uid.ID) {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc, err := NewService(ServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}
	volume, err := storage.NewLocalVolume(t.TempDir(), 10<<20)
	if err != nil {
		t.Fatalf("new local volume: %v", err)
	}
	h, err := NewProfileHandler(ProfileHandlerDeps{
		Service:       svc,
		Logger:        logger,
		Store:         volume,
		Capacity:      volume,
		CapacityLimit: 10 << 20,
		UploadLimit:   2 << 20,
		MaxPixels:     25_000_000,
		PublicBaseURL: "https://api.example.test",
	})
	if err != nil {
		t.Fatalf("new profile handler: %v", err)
	}
	userRow, err := svc.UpsertByOpenID(context.Background(), "avatar-fixture-openid", "", 1000, nil, time.Now())
	if err != nil {
		t.Fatalf("upsert fixture buyer: %v", err)
	}
	return h, db, userRow.ID
}

func multipartBody(t *testing.T, field string, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func performAvatarRequest(h *ProfileHandler, subject string, body *bytes.Buffer, contentType string) (*httptest.ResponseRecorder, map[string]any) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if subject != "" {
			c.Set(middleware.BuyerSubjectKey, subject)
		}
		c.Next()
	})
	r.POST("/me/avatar", h.UploadAvatar)
	req := httptest.NewRequest(http.MethodPost, "/me/avatar", body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var payload map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
	}
	return w, payload
}

func TestUploadAvatarRequiresIdentityAndFile(t *testing.T) {
	h, _, userID := newAvatarFixture(t)

	// No buyer subject → 401.
	w, _ := performAvatarRequest(h, "", bytes.NewBufferString(""), "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("upload without identity status = %d, want 401", w.Code)
	}

	// Authenticated but no file field → 400.
	w, _ = performAvatarRequest(h, fmt.Sprintf("buyer:%s", userID), bytes.NewBufferString("not-multipart"), "application/json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("upload without file status = %d, want 400", w.Code)
	}
}

func TestUploadAvatarStoresImageAndUpdatesProfile(t *testing.T) {
	h, db, userID := newAvatarFixture(t)

	body, contentType := multipartBody(t, "file", "avatar.png", generateTestPNG(t))
	w, payload := performAvatarRequest(h, fmt.Sprintf("buyer:%s", userID), body, contentType)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body %s, want 201", w.Code, w.Body.String())
	}
	data, _ := payload["data"].(map[string]any)
	key, _ := data["key"].(string)
	url, _ := data["url"].(string)
	if key == "" || url == "" {
		t.Fatalf("upload response missing key/url: %v", data)
	}
	if url != "https://api.example.test/static/images/"+key {
		t.Fatalf("url = %q, want public URL built from key", url)
	}
	if data["width"].(float64) != 8 || data["height"].(float64) != 8 {
		t.Fatalf("dimensions = %v, want 8x8", data)
	}

	// The profile now points at the uploaded avatar.
	var stored database.User
	if err := db.First(&stored, userID).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if stored.AvatarURL != url {
		t.Fatalf("avatar_url = %q, want %q", stored.AvatarURL, url)
	}
}

func TestUploadAvatarRejectsOversizeAndWrongFormat(t *testing.T) {
	h, _, userID := newAvatarFixture(t)

	// Oversize (> 2MB) → 413.
	big := make([]byte, 2<<20+1)
	copy(big, generateTestPNG(t))
	body, contentType := multipartBody(t, "file", "big.png", big)
	w, _ := performAvatarRequest(h, fmt.Sprintf("buyer:%s", userID), body, contentType)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload status = %d, want 413", w.Code)
	}

	// GIF magic bytes → 415.
	body, contentType = multipartBody(t, "file", "fake.gif", []byte("GIF89a-fake"))
	w, _ = performAvatarRequest(h, fmt.Sprintf("buyer:%s", userID), body, contentType)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong format status = %d, want 415", w.Code)
	}
}
