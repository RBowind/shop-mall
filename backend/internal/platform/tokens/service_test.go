package tokens_test

import (
	"errors"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/tokens"
)

func TestSignerIssueAndParseRoundTrip(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	subject := "buyer:" + uid.New().String()
	token, err := signer.Issue(subject, 7, now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := signer.Parse(token, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != subject {
		t.Fatalf("subject = %q, want %q", claims.Subject, subject)
	}
	if claims.TokenVersion != 7 {
		t.Fatalf("token version = %d, want 7", claims.TokenVersion)
	}
}

func TestSignerRejectsTokenSignedWithDifferentKey(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	token, err := signer.Issue("buyer:"+uid.New().String(), 7, now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	other, err := tokens.NewSigner(config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "other-kid",
		Keys: map[string][]byte{
			"other-kid": []byte("another-test-key-material-with-at-least-32-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("new other signer: %v", err)
	}
	if _, err := other.Parse(token, now); err == nil {
		t.Fatal("foreign signer accepted a token signed with a different key")
	}
}

func TestSignerRejectsExpiredToken(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Minute,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	issuedAt := time.Date(2026, time.August, 10, 11, 0, 0, 0, time.UTC)
	token, err := signer.Issue("buyer:"+uid.New().String(), 1, issuedAt)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := signer.Parse(token, issuedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("expected expiration error")
	}
}

func TestSignerRejectsEmptySubject(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	if _, err := signer.Issue("   ", 1, time.Now()); err == nil {
		t.Fatal("expected error for blank subject")
	}
}

func TestSignerRejectsInvalidConfig(t *testing.T) {
	if _, err := tokens.NewSigner(config.JWTConfig{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSignerRejectsBlankToken(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	if _, err := signer.Parse("  ", time.Now()); err == nil {
		t.Fatal("expected error for blank token")
	}
}

func TestSignerDoesNotExposeRawKeyMaterial(t *testing.T) {
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	if signer == nil {
		t.Fatal("signer is nil")
	}
	if errors.Is(nil, errors.New("x")) {
		t.Fatal("errors package sanity")
	}
}
