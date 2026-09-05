package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	BuyerSubjectKey = "buyer_subject"
	AdminSubjectKey = "admin_subject"
	ClaimsKey       = "jwt_claims"
)

// JWTClaims is the type alias of tokens.JWTClaims so the middleware can parse
// tokens signed by the tokens.Signer. The application layer owns the
// concrete definition; middleware aliases it here to keep the package
// dependency graph acyclic.
type JWTClaims = tokens.JWTClaims

type JWTService struct {
	cfg config.JWTConfig
}

func NewJWTService(cfg config.JWTConfig) (*JWTService, error) {
	if err := cfg.Validate("JWT", 0); err != nil {
		return nil, err
	}
	return &JWTService{cfg: cfg}, nil
}

// SignerConfigSource is the subset of the tokens.Signer surface the admin
// middleware needs to build a peer JWTService from an existing signer. The
// tokens.Signer satisfies it structurally.
type SignerConfigSource interface {
	Issuer() string
	Audience() string
	TTL() time.Duration
	ActiveKID() string
	Keys() map[string][]byte
}

// NewJWTServiceFromSigner builds a JWTService that shares the key material of
// an existing signer, so route groups outside the admin package can enforce
// the same token verification without re-reading the configuration.
func NewJWTServiceFromSigner(source SignerConfigSource) (*JWTService, error) {
	if source == nil {
		return nil, errors.New("JWT signer is required")
	}
	return NewJWTService(config.JWTConfig{
		Issuer:    source.Issuer(),
		Audience:  source.Audience(),
		TTL:       source.TTL(),
		ActiveKID: source.ActiveKID(),
		Keys:      source.Keys(),
	})
}

func (s *JWTService) Issue(subject string, tokenVersion int64, now time.Time) (string, error) {
	return s.IssueAt(subject, tokenVersion, now, s.cfg.TTL)
}

func (s *JWTService) IssueAt(subject string, tokenVersion int64, now time.Time, ttl time.Duration) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", errors.New("JWT subject is required")
	}
	if ttl <= 0 {
		return "", errors.New("JWT TTL must be positive")
	}
	if ttl > s.cfg.TTL {
		return "", errors.New("JWT TTL exceeds configured TTL")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	claims := JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{s.cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        NewTraceID(),
		},
		TokenVersion: tokenVersion,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = s.cfg.ActiveKID
	key := s.cfg.Keys[s.cfg.ActiveKID]
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

func (s *JWTService) Parse(raw string, now time.Time) (*JWTClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("JWT is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	parser := jwt.NewParser(
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithAudience(s.cfg.Audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	claims := &JWTClaims{}
	token, err := parser.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected JWT signing method")
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("JWT kid is required")
		}
		key, ok := s.cfg.Keys[kid]
		if !ok {
			return nil, errors.New("unknown JWT kid")
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse JWT: %w", err)
	}
	if token == nil || !token.Valid {
		return nil, errors.New("invalid JWT")
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return nil, errors.New("JWT iat and exp are required")
	}
	if claims.IssuedAt.After(now.Add(time.Second)) {
		return nil, errors.New("JWT iat is in the future")
	}
	if claims.ExpiresAt.Before(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > s.cfg.TTL {
		return nil, errors.New("JWT lifetime exceeds configured TTL")
	}
	return claims, nil
}

func BuyerAuth(service *JWTService) gin.HandlerFunc {
	return BuyerAuthWithState(service, nil)
}

// BuyerStateResolver reports whether the buyer row behind a JWT subject still
// exists. The JWT signature alone cannot detect a dangling subject — after a
// database reset or a deleted user, an old-but-valid token would otherwise
// reach handlers with an id that no longer satisfies foreign keys and every
// write would explode as a 500. The middleware rejects those tokens as 401 so
// the client falls back to the login flow.
type BuyerStateResolver interface {
	BuyerExists(ctx context.Context, buyerID uid.ID) (bool, error)
}

// BuyerAuthWithState builds buyer authentication that, on top of the JWT
// signature, consults the resolver so a token whose subject no longer exists
// surfaces as 401. A nil resolver keeps the legacy signature-only behavior.
func BuyerAuthWithState(service *JWTService, resolver BuyerStateResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			abortUnauthorized(c)
			return
		}
		claims, err := service.Parse(strings.TrimSpace(strings.TrimPrefix(header, prefix)), time.Now().UTC())
		if err != nil {
			abortUnauthorized(c)
			return
		}
		if resolver != nil {
			buyerID, err := BuyerIDFromSubject(claims.Subject)
			if err != nil {
				abortUnauthorized(c)
				return
			}
			exists, err := resolver.BuyerExists(c.Request.Context(), buyerID)
			if err != nil || !exists {
				abortUnauthorized(c)
				return
			}
		}
		c.Set(ClaimsKey, claims)
		c.Set(BuyerSubjectKey, claims.Subject)
		c.Next()
	}
}

func AdminAuth(service *JWTService, cookieName string, resolver AdminStateResolver) gin.HandlerFunc {
	return AdminAuthWithClock(service, cookieName, time.Now, resolver)
}

// AdminState is the live token freshness and enabled state of an
// administrator row. The middleware compares these against the JWT claims on
// every admin-authenticated request.
type AdminState struct {
	Enabled      bool
	TokenVersion int64
}

// AdminStateResolver resolves the live state of an administrator by id. The
// JWT signature alone cannot enforce the "password change invalidates old
// JWT" contract, so the admin middleware consults the resolver on every
// request. A nil resolver disables the freshness check; production wiring
// always supplies one.
type AdminStateResolver interface {
	AdminState(ctx context.Context, adminID uid.ID) (AdminState, error)
}

// AdminAuthWithClock builds an admin authentication middleware that uses the
// supplied clock to evaluate token expiration and the supplied resolver to
// enforce token_version freshness and the enabled flag on top of the JWT
// signature. A password change, logout or disable bumps token_version, so a
// stale-but-unexpired JWT is rejected here before it reaches any admin route;
// a disabled administrator's session is rejected the same way. Tests that
// exercise the middleware with a fixed clock pass a deterministic clock;
// production code uses AdminAuth which forwards to time.Now.
func AdminAuthWithClock(service *JWTService, cookieName string, clock func() time.Time, resolver AdminStateResolver) gin.HandlerFunc {
	if cookieName == "" {
		cookieName = config.DefaultAdminCookieConfig().AccessTokenName
	}
	if clock == nil {
		clock = time.Now
	}
	return func(c *gin.Context) {
		raw, err := c.Cookie(cookieName)
		if err != nil || raw == "" {
			abortUnauthorized(c)
			return
		}
		claims, err := service.Parse(raw, clock().UTC())
		if err != nil {
			abortUnauthorized(c)
			return
		}
		if resolver != nil {
			adminID, idErr := AdminIDFromSubject(claims.Subject)
			if idErr != nil {
				abortUnauthorized(c)
				return
			}
			state, stateErr := resolver.AdminState(c.Request.Context(), adminID)
			if stateErr != nil {
				abortUnauthorized(c)
				return
			}
			if !state.Enabled || state.TokenVersion != claims.TokenVersion {
				abortUnauthorized(c)
				return
			}
		}
		c.Set(ClaimsKey, claims)
		c.Set(AdminSubjectKey, claims.Subject)
		c.Next()
	}
}

// BuyerIDFromSubject extracts the buyer id from the "buyer:<uuid>" JWT
// subject. It returns an error for any other subject format so an
// administrator-shaped subject cannot authenticate as a buyer. The buyer
// handlers use it to derive the authenticated userID from the middleware
// context; the value is never taken from the request body. The ID segment
// must be the canonical lowercase hyphenated UUID form.
func BuyerIDFromSubject(subject string) (uid.ID, error) {
	if !strings.HasPrefix(subject, "buyer:") {
		return uid.ID{}, errors.New("subject does not have buyer prefix")
	}
	return uid.ParseCanonical(subject[len("buyer:"):])
}

// AdminIDFromSubject extracts the administrator id from the "admin:<uuid>"
// JWT subject. It returns an error for any other subject format so an
// attacker cannot smuggle a non-admin subject into the admin path.
func AdminIDFromSubject(subject string) (uid.ID, error) {
	if !strings.HasPrefix(subject, "admin:") {
		return uid.ID{}, errors.New("subject does not have admin prefix")
	}
	return uid.ParseCanonical(subject[len("admin:"):])
}

func ClaimsFromGin(c *gin.Context) (*JWTClaims, bool) {
	value, ok := c.Get(ClaimsKey)
	if !ok {
		return nil, false
	}
	claims, ok := value.(*JWTClaims)
	return claims, ok && claims != nil
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"code":     1002,
		"data":     nil,
		"message":  "unauthorized",
		"trace_id": TraceIDFromGin(c),
	})
}
