package config

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func validConfigForTest() Config {
	return Config{
		Environment:    "test",
		HTTPAddr:       ":8080",
		PublicBaseURL:  "https://api.example.test",
		AllowedOrigins: []string{"https://admin.example.test"},
		Database: DatabaseConfig{
			URL:             "postgres://shop:test@localhost/shop?sslmode=disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
			ConnMaxIdleTime: 10 * time.Minute,
		},
		BuyerJWT: JWTConfig{
			Issuer:    "shop-mall-buyer",
			Audience:  "shop-mall-buyer-api",
			TTL:       time.Hour,
			ActiveKID: "buyer-2026-01",
			Keys:      map[string][]byte{"buyer-2026-01": []byte("buyer-test-key-with-at-least-32-bytes")},
		},
		AdminJWT: JWTConfig{
			Issuer:    "shop-mall-admin",
			Audience:  "shop-mall-admin-api",
			TTL:       30 * time.Minute,
			ActiveKID: "admin-2026-01",
			Keys:      map[string][]byte{"admin-2026-01": []byte("admin-test-key-with-at-least-32-bytes")},
		},
		AdminCookie:          DefaultAdminCookieConfig(),
		UploadLimit:          2 * 1024 * 1024,
		SignupBonus:          100,
		ImageMaxPixels:       25 * 1000 * 1000,
		ImageStorageCapacity: 10 * 1024 * 1024 * 1024,
		ImageGracePeriod:     24 * time.Hour,
		ImageCleanupInterval: time.Hour,
		ImageVolumeDir:       "/var/lib/shop-mall/images",
	}
}

func TestValidateRejectsNonHTTPSPublicBaseURL(t *testing.T) {
	cfg := validConfigForTest()
	cfg.PublicBaseURL = "http://api.example.test"

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a non-HTTPS PUBLIC_BASE_URL")
	}
}

func TestValidateRejectsMissingBuyerKeyring(t *testing.T) {
	cfg := validConfigForTest()
	cfg.BuyerJWT.Keys = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a missing buyer JWT keyring")
	}
}

func TestValidateRejectsMissingAdminKeyring(t *testing.T) {
	cfg := validConfigForTest()
	cfg.AdminJWT.Keys = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a missing admin JWT keyring")
	}
}

func TestValidateRejectsInvalidUploadLimit(t *testing.T) {
	cfg := validConfigForTest()
	cfg.UploadLimit = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an invalid upload limit")
	}
}

func TestValidateRejectsNegativeSignupBonus(t *testing.T) {
	cfg := validConfigForTest()
	cfg.SignupBonus = -1

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a negative signup bonus")
	}
}

func TestValidateRejectsInvalidImageSettings(t *testing.T) {
	cfg := validConfigForTest()
	cfg.ImageMaxPixels = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a zero image max pixels")
	}
	cfg = validConfigForTest()
	cfg.ImageStorageCapacity = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a zero image storage capacity")
	}
	cfg = validConfigForTest()
	cfg.ImageGracePeriod = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a negative image grace period")
	}
	cfg = validConfigForTest()
	cfg.ImageCleanupInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a zero image cleanup interval")
	}
	cfg = validConfigForTest()
	cfg.ImageVolumeDir = "  "
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a blank image volume directory")
	}
}

func TestValidateAllowsImageSettings(t *testing.T) {
	if err := validConfigForTest().Validate(); err != nil {
		t.Fatalf("Validate rejected the baseline image settings: %v", err)
	}
}

func TestValidateRejectsSharedJWTAudience(t *testing.T) {
	cfg := validConfigForTest()
	cfg.AdminJWT.Audience = cfg.BuyerJWT.Audience

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a shared buyer/admin JWT audience")
	}
}

func TestValidateRejectsSharedJWTActiveKID(t *testing.T) {
	cfg := validConfigForTest()
	cfg.AdminJWT.ActiveKID = cfg.BuyerJWT.ActiveKID
	cfg.AdminJWT.Keys[cfg.AdminJWT.ActiveKID] = []byte("admin-test-key-with-at-least-32-bytes")

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a shared buyer/admin JWT active kid")
	}
}

func TestValidateRejectsSharedJWTTTL(t *testing.T) {
	cfg := validConfigForTest()
	cfg.BuyerJWT.TTL = cfg.AdminJWT.TTL

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a shared buyer/admin JWT TTL")
	}
}

func TestValidateRejectsSharedJWTKeyMaterial(t *testing.T) {
	cfg := validConfigForTest()
	cfg.AdminJWT.Keys[cfg.AdminJWT.ActiveKID] = append([]byte(nil), cfg.BuyerJWT.Keys[cfg.BuyerJWT.ActiveKID]...)

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted reused buyer/admin JWT key material")
	}
}

func TestValidateAllowsDistinctJWTConfiguration(t *testing.T) {
	if err := validConfigForTest().Validate(); err != nil {
		t.Fatalf("Validate rejected distinct buyer/admin JWT configuration: %v", err)
	}
}

func TestValidateWeChatPartialConfigurationIsRejected(t *testing.T) {
	cfg := validConfigForTest()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("baseline config must validate: %v", err)
	}

	partial := cfg
	partial.WeChat = WeChatConfig{AppID: "app-id", Secret: "", Endpoint: ""}
	if err := partial.Validate(); err == nil {
		t.Fatal("Validate accepted a partial WeChat configuration")
	}

	full := cfg
	full.WeChat = WeChatConfig{AppID: "app-id", Secret: "secret", Endpoint: "https://api.weixin.qq.com/sns/jscode2session"}
	if err := full.Validate(); err != nil {
		t.Fatalf("Validate rejected a fully configured WeChat integration: %v", err)
	}
	if !full.WeChat.Configured() {
		t.Fatal("Configured() must be true for a fully configured WeChat integration")
	}
	if cfg.WeChat.Configured() {
		t.Fatal("Configured() must be false when no WeChat credentials are set")
	}
}

func TestLoadRejectsInvalidIntegerEnvironmentValue(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DB_MAX_IDLE_CONNS", "invalid")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid integer environment value")
	}
}

func TestLoadRejectsInvalidDurationEnvironmentValue(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DB_CONN_MAX_LIFETIME", "invalid")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid duration environment value")
	}
}

func TestLoadRejectsMissingSecretsInsteadOfUsingDefaults(t *testing.T) {
	for _, name := range []string{"PUBLIC_BASE_URL", "DATABASE_URL", "BUYER_JWT_KEYRING", "ADMIN_JWT_KEYRING", "UPLOAD_MAX_BYTES", "SIGNUP_BONUS_POINTS"} {
		t.Setenv(name, "")
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted missing secret/configuration values")
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("PUBLIC_BASE_URL", "https://api.example.test")
	t.Setenv("ADMIN_ALLOWED_ORIGINS", "https://admin.example.test")
	t.Setenv("DATABASE_URL", "postgres://shop:test@localhost/shop?sslmode=disable")
	t.Setenv("DB_MAX_OPEN_CONNS", "20")
	t.Setenv("DB_MAX_IDLE_CONNS", "10")
	t.Setenv("DB_CONN_MAX_LIFETIME", "30m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "5m")
	t.Setenv("BUYER_JWT_ISSUER", "shop-mall-buyer")
	t.Setenv("BUYER_JWT_AUDIENCE", "shop-mall-buyer-api")
	t.Setenv("BUYER_JWT_TTL", "24h")
	t.Setenv("BUYER_JWT_ACTIVE_KID", "buyer-2026-01")
	t.Setenv("BUYER_JWT_KEYRING", fmt.Sprintf(`{"buyer-2026-01":%q}`, base64.StdEncoding.EncodeToString([]byte("buyer-test-key-with-at-least-32-bytes"))))
	t.Setenv("ADMIN_JWT_ISSUER", "shop-mall-admin")
	t.Setenv("ADMIN_JWT_AUDIENCE", "shop-mall-admin-api")
	t.Setenv("ADMIN_JWT_TTL", "30m")
	t.Setenv("ADMIN_JWT_ACTIVE_KID", "admin-2026-01")
	t.Setenv("ADMIN_JWT_KEYRING", fmt.Sprintf(`{"admin-2026-01":%q}`, base64.StdEncoding.EncodeToString([]byte("admin-test-key-with-at-least-32-bytes"))))
	t.Setenv("UPLOAD_MAX_BYTES", "2097152")
	t.Setenv("SIGNUP_BONUS_POINTS", "100")
	t.Setenv("IMAGE_MAX_PIXELS", "25000000")
	t.Setenv("IMAGE_STORAGE_CAPACITY_BYTES", "10737418240")
	t.Setenv("IMAGE_GRACE_PERIOD", "24h")
	t.Setenv("IMAGE_CLEANUP_INTERVAL", "1h")
	t.Setenv("IMAGE_VOLUME_DIR", "/var/lib/shop-mall/images")
}
