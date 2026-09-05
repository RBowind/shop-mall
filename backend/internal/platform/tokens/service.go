package tokens

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"shop-mall/backend/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

// JWTClaims is the JWT claim set issued and verified by Signer. The
// middleware package aliases the same struct via middleware.JWTClaims so
// the application layer and the HTTP middleware parse the same tokens
// without an explicit conversion.
type JWTClaims struct {
	jwt.RegisteredClaims
	TokenVersion int64 `json:"token_version,omitempty"`
}

// Claims is a shorthand alias for JWTClaims that the application layer uses
// to keep call sites compact.
type Claims = JWTClaims

// Signer issues and parses JWTs for the buyer and administrator flows. The
// Signer only holds key material; it does not touch the database, the
// network or the logger. The LoginUsecase and the admin handler must call
// Issue AFTER the database transaction commits so a signing failure cannot
// roll back the user or administrator write.
type Signer struct {
	cfg config.JWTConfig
}

// NewSigner validates the JWT configuration and returns a Signer ready for
// the buyer and administrator flows. It mirrors middleware.NewJWTService but
// lives in the application layer so the usecase can sign after commit.
func NewSigner(cfg config.JWTConfig) (*Signer, error) {
	if err := cfg.Validate("auth", 0); err != nil {
		return nil, err
	}
	return &Signer{cfg: cfg}, nil
}

// Issue signs a JWT for the given subject and token version at the supplied
// clock time. The TTL of the issued token equals cfg.TTL.
func (s *Signer) Issue(subject string, tokenVersion int64, now time.Time) (string, error) {
	return s.IssueAt(subject, tokenVersion, now, s.cfg.TTL)
}

// IssueAt signs a JWT with the supplied TTL. The TTL must not exceed the
// configured TTL; the call returns an error otherwise.
func (s *Signer) IssueAt(subject string, tokenVersion int64, now time.Time, ttl time.Duration) (string, error) {
	if s == nil {
		return "", errors.New("signer is not configured")
	}
	if strings.TrimSpace(subject) == "" {
		return "", errors.New("subject is required")
	}
	if ttl <= 0 {
		return "", errors.New("TTL must be positive")
	}
	if ttl > s.cfg.TTL {
		return "", errors.New("TTL exceeds configured TTL")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{s.cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        newJTI(),
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

// Parse validates the JWT against the configured key material and clock time
// and returns the embedded Claims. It mirrors middleware.JWTService.Parse so
// that tokens issued by the application layer can be consumed by HTTP
// middleware.
func (s *Signer) Parse(raw string, now time.Time) (*Claims, error) {
	if s == nil {
		return nil, errors.New("signer is not configured")
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("token is required")
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
	claims := &Claims{}
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

// Issuer returns the configured issuer claim.
func (s *Signer) Issuer() string {
	if s == nil {
		return ""
	}
	return s.cfg.Issuer
}

// Audience returns the configured audience claim.
func (s *Signer) Audience() string {
	if s == nil {
		return ""
	}
	return s.cfg.Audience
}

// TTL returns the configured JWT lifetime.
func (s *Signer) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.cfg.TTL
}

// ActiveKID returns the configured active key id.
func (s *Signer) ActiveKID() string {
	if s == nil {
		return ""
	}
	return s.cfg.ActiveKID
}

// Keys returns a copy of the configured keyring so callers cannot mutate the
// signer's internal state.
func (s *Signer) Keys() map[string][]byte {
	if s == nil {
		return nil
	}
	out := make(map[string][]byte, len(s.cfg.Keys))
	for kid, key := range s.cfg.Keys {
		copied := make([]byte, len(key))
		copy(copied, key)
		out[kid] = copied
	}
	return out
}

func newJTI() string {
	return fmt.Sprintf("jti-%d", time.Now().UnixNano())
}
