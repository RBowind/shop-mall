package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const MaxUploadLimitBytes int64 = 2 * 1024 * 1024

type Config struct {
	Environment          string
	HTTPAddr             string
	PublicBaseURL        string
	AllowedOrigins       []string
	Database             DatabaseConfig
	BuyerJWT             JWTConfig
	AdminJWT             JWTConfig
	AdminCookie          AdminCookieConfig
	UploadLimit          int64
	SignupBonus          int64
	ImageMaxPixels       int64
	ImageStorageCapacity int64
	ImageGracePeriod     time.Duration
	ImageCleanupInterval time.Duration
	ImageVolumeDir       string
	WeChat               WeChatConfig
}

// WeChatConfig carries the optional real code2session credentials. When every
// field is empty the composition root uses the deterministic local fake client;
// when any field is set all three must be set so a half-configured deployment
// cannot silently fall back to the fake. The AppID and Secret are deploy-only
// secrets and never appear in code, logs or responses.
type WeChatConfig struct {
	AppID    string
	Secret   string
	Endpoint string
}

// Configured reports whether the real WeChat client should be used. It is true
// only when all three fields are set; Validate() rejects partial configuration.
func (c WeChatConfig) Configured() bool {
	return c.AppID != "" && c.Secret != "" && c.Endpoint != ""
}

type DatabaseConfig struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

type JWTConfig struct {
	Issuer    string
	Audience  string
	TTL       time.Duration
	ActiveKID string
	Keys      map[string][]byte
}

type AdminCookieConfig struct {
	AccessTokenName string
	CSRFName        string
	Path            string
	Secure          bool
	HTTPOnly        bool
	SameSite        http.SameSite
	MaxAge          time.Duration
}

func DefaultAdminCookieConfig() AdminCookieConfig {
	return AdminCookieConfig{
		AccessTokenName: "admin_access_token",
		CSRFName:        "csrf_token",
		Path:            "/api/admin/v1",
		Secure:          true,
		HTTPOnly:        true,
		SameSite:        http.SameSiteStrictMode,
		MaxAge:          30 * time.Minute,
	}
}

func Load() (Config, error) {
	maxOpenConns, err := intEnvOrDefault("DB_MAX_OPEN_CONNS", 20)
	if err != nil {
		return Config{}, err
	}
	maxIdleConns, err := intEnvOrDefault("DB_MAX_IDLE_CONNS", 10)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := durationEnvOrDefault("DB_CONN_MAX_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	connMaxIdleTime, err := durationEnvOrDefault("DB_CONN_MAX_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	buyerTTL, err := durationEnvOrDefault("BUYER_JWT_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	adminTTL, err := durationEnvOrDefault("ADMIN_JWT_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	imageGracePeriod, err := durationEnvOrDefault("IMAGE_GRACE_PERIOD", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	imageCleanupInterval, err := durationEnvOrDefault("IMAGE_CLEANUP_INTERVAL", time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Environment:    envOrDefault("APP_ENV", "development"),
		HTTPAddr:       envOrDefault("HTTP_ADDR", ":8080"),
		PublicBaseURL:  os.Getenv("PUBLIC_BASE_URL"),
		AllowedOrigins: splitCSV(os.Getenv("ADMIN_ALLOWED_ORIGINS")),
		AdminCookie:    DefaultAdminCookieConfig(),
		WeChat: WeChatConfig{
			AppID:    os.Getenv("WECHAT_APP_ID"),
			Secret:   os.Getenv("WECHAT_APP_SECRET"),
			Endpoint: os.Getenv("WECHAT_ENDPOINT"),
		},
		Database: DatabaseConfig{
			URL:             os.Getenv("DATABASE_URL"),
			MaxOpenConns:    maxOpenConns,
			MaxIdleConns:    maxIdleConns,
			ConnMaxLifetime: connMaxLifetime,
			ConnMaxIdleTime: connMaxIdleTime,
		},
		BuyerJWT: JWTConfig{
			Issuer:    os.Getenv("BUYER_JWT_ISSUER"),
			Audience:  os.Getenv("BUYER_JWT_AUDIENCE"),
			TTL:       buyerTTL,
			ActiveKID: os.Getenv("BUYER_JWT_ACTIVE_KID"),
		},
		AdminJWT: JWTConfig{
			Issuer:    os.Getenv("ADMIN_JWT_ISSUER"),
			Audience:  os.Getenv("ADMIN_JWT_AUDIENCE"),
			TTL:       adminTTL,
			ActiveKID: os.Getenv("ADMIN_JWT_ACTIVE_KID"),
		},
	}

	if cfg.BuyerJWT.Keys, err = parseKeyringEnv("BUYER_JWT_KEYRING"); err != nil {
		return Config{}, err
	}
	if cfg.AdminJWT.Keys, err = parseKeyringEnv("ADMIN_JWT_KEYRING"); err != nil {
		return Config{}, err
	}
	cfg.UploadLimit, err = requiredInt64Env("UPLOAD_MAX_BYTES")
	if err != nil {
		return Config{}, err
	}
	cfg.SignupBonus, err = requiredInt64Env("SIGNUP_BONUS_POINTS")
	if err != nil {
		return Config{}, err
	}
	cfg.ImageMaxPixels, err = requiredInt64Env("IMAGE_MAX_PIXELS")
	if err != nil {
		return Config{}, err
	}
	cfg.ImageStorageCapacity, err = requiredInt64Env("IMAGE_STORAGE_CAPACITY_BYTES")
	if err != nil {
		return Config{}, err
	}
	cfg.ImageGracePeriod = imageGracePeriod
	cfg.ImageCleanupInterval = imageCleanupInterval
	cfg.ImageVolumeDir = envOrDefault("IMAGE_VOLUME_DIR", "/var/lib/shop-mall/images")
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if err := validateHTTPSBaseURL(c.PublicBaseURL); err != nil {
		errs = append(errs, fmt.Errorf("public base URL: %w", err))
	}
	if len(c.AllowedOrigins) == 0 {
		errs = append(errs, errors.New("admin allowed origins must not be empty"))
	}
	for _, origin := range c.AllowedOrigins {
		if err := validateHTTPSOrigin(origin); err != nil {
			errs = append(errs, fmt.Errorf("admin origin %q: %w", origin, err))
		}
	}
	if err := c.Database.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("database: %w", err))
	}
	if err := c.BuyerJWT.Validate("buyer", 0); err != nil {
		errs = append(errs, err)
	}
	if err := c.AdminJWT.Validate("admin", 30*time.Minute); err != nil {
		errs = append(errs, err)
	}
	if c.BuyerJWT.Issuer != "" && c.BuyerJWT.Issuer == c.AdminJWT.Issuer {
		errs = append(errs, errors.New("buyer and admin JWT issuers must be different"))
	}
	if c.BuyerJWT.Audience != "" && c.BuyerJWT.Audience == c.AdminJWT.Audience {
		errs = append(errs, errors.New("buyer and admin JWT audiences must be different"))
	}
	if c.BuyerJWT.ActiveKID != "" && c.BuyerJWT.ActiveKID == c.AdminJWT.ActiveKID {
		errs = append(errs, errors.New("buyer and admin JWT active kids must be different"))
	}
	if c.BuyerJWT.TTL > 0 && c.BuyerJWT.TTL == c.AdminJWT.TTL {
		errs = append(errs, errors.New("buyer and admin JWT TTLs must be different"))
	}
	for buyerKID, buyerKey := range c.BuyerJWT.Keys {
		for adminKID, adminKey := range c.AdminJWT.Keys {
			if bytes.Equal(buyerKey, adminKey) {
				errs = append(errs, fmt.Errorf("buyer JWT key %q and admin JWT key %q reuse key material", buyerKID, adminKID))
			}
		}
	}
	if err := c.AdminCookie.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("admin cookie: %w", err))
	}
	if c.UploadLimit <= 0 || c.UploadLimit > MaxUploadLimitBytes {
		errs = append(errs, fmt.Errorf("upload limit must be between 1 and %d bytes", MaxUploadLimitBytes))
	}
	if c.SignupBonus < 0 {
		errs = append(errs, errors.New("signup bonus must be non-negative"))
	}
	if c.ImageMaxPixels <= 0 {
		errs = append(errs, errors.New("image max pixels must be positive"))
	}
	if c.ImageStorageCapacity <= 0 {
		errs = append(errs, errors.New("image storage capacity must be positive"))
	}
	if c.ImageGracePeriod < 0 {
		errs = append(errs, errors.New("image grace period must not be negative"))
	}
	if c.ImageCleanupInterval <= 0 {
		errs = append(errs, errors.New("image cleanup interval must be positive"))
	}
	if strings.TrimSpace(c.ImageVolumeDir) == "" {
		errs = append(errs, errors.New("image volume directory is required"))
	}
	if err := c.WeChat.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Validate rejects a partially configured WeChat integration: either all three
// fields are set (real client) or none are (local fake). AppID and Secret are
// deploy secrets and are never echoed back.
func (c WeChatConfig) Validate() error {
	set := 0
	for _, field := range []string{c.AppID, c.Secret, c.Endpoint} {
		if strings.TrimSpace(field) != "" {
			set++
		}
	}
	if set > 0 && set < 3 {
		return errors.New("wechat app id, secret and endpoint must be configured together")
	}
	return nil
}

func (c DatabaseConfig) Validate() error {
	var errs []error
	if strings.TrimSpace(c.URL) == "" {
		errs = append(errs, errors.New("URL is required"))
	}
	if c.MaxOpenConns <= 0 {
		errs = append(errs, errors.New("max open connections must be positive"))
	}
	if c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		errs = append(errs, errors.New("max idle connections must be between zero and max open connections"))
	}
	if c.ConnMaxLifetime < 0 || c.ConnMaxIdleTime < 0 {
		errs = append(errs, errors.New("connection lifetimes must not be negative"))
	}
	return errors.Join(errs...)
}

func (c JWTConfig) Validate(name string, requiredTTL time.Duration) error {
	var errs []error
	if strings.TrimSpace(c.Issuer) == "" {
		errs = append(errs, fmt.Errorf("%s JWT issuer is required", name))
	}
	if strings.TrimSpace(c.Audience) == "" {
		errs = append(errs, fmt.Errorf("%s JWT audience is required", name))
	}
	if c.TTL <= 0 {
		errs = append(errs, fmt.Errorf("%s JWT TTL must be positive", name))
	}
	if requiredTTL > 0 && c.TTL != requiredTTL {
		errs = append(errs, fmt.Errorf("%s JWT TTL must be %s", name, requiredTTL))
	}
	if strings.TrimSpace(c.ActiveKID) == "" {
		errs = append(errs, fmt.Errorf("%s JWT active kid is required", name))
	}
	if len(c.Keys) == 0 {
		errs = append(errs, fmt.Errorf("%s JWT keyring must not be empty", name))
	}
	for kid, key := range c.Keys {
		if !validKID(kid) {
			errs = append(errs, fmt.Errorf("%s JWT key id %q is invalid", name, kid))
		}
		if len(key) < 32 {
			errs = append(errs, fmt.Errorf("%s JWT key %q must be at least 32 bytes", name, kid))
		}
	}
	if c.ActiveKID != "" {
		if _, ok := c.Keys[c.ActiveKID]; !ok {
			errs = append(errs, fmt.Errorf("%s JWT active kid is not in keyring", name))
		}
	}
	return errors.Join(errs...)
}

func (c AdminCookieConfig) Validate() error {
	want := DefaultAdminCookieConfig()
	var errs []error
	if c.AccessTokenName != want.AccessTokenName {
		errs = append(errs, fmt.Errorf("access token cookie name must be %q", want.AccessTokenName))
	}
	if c.CSRFName != want.CSRFName {
		errs = append(errs, fmt.Errorf("CSRF cookie name must be %q", want.CSRFName))
	}
	if c.Path != want.Path {
		errs = append(errs, fmt.Errorf("cookie path must be %q", want.Path))
	}
	if !c.Secure || !c.HTTPOnly || c.SameSite != want.SameSite {
		errs = append(errs, errors.New("admin access cookie must be Secure, HttpOnly and SameSite=Strict"))
	}
	if c.MaxAge != want.MaxAge {
		errs = append(errs, errors.New("admin cookie max age must be 30 minutes"))
	}
	return errors.Join(errs...)
}

func validateHTTPSBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must be an HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func validateHTTPSOrigin(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("must be an HTTPS origin")
	}
	return nil
}

func validKID(kid string) bool {
	if len(kid) == 0 || len(kid) > 64 {
		return false
	}
	for _, ch := range kid {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '.' && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func parseKeyringEnv(name string) (map[string][]byte, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, fmt.Errorf("%s is required and must contain a base64 JSON keyring", name)
	}
	var encoded map[string]string
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	keys := make(map[string][]byte, len(encoded))
	for kid, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%s key %q is not valid base64: %w", name, kid, err)
		}
		keys[kid] = key
	}
	return keys, nil
}

func splitCSV(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func intEnvOrDefault(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func durationEnvOrDefault(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	return parsed, nil
}

func requiredInt64Env(name string) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}
