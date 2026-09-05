package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

type secretBytes []byte

type nestedByteStruct struct {
	Raw   []byte      `json:"raw"`
	Named secretBytes `json:"named"`
}

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("request", "jwt", "jwt-secret", "password", "password-secret", "phone", "13800000000", "address", "full address")
	logger.Error("database failure", "error", "postgres://user:db-password@host/db")

	text := output.String()
	for _, secret := range []string{"jwt-secret", "password-secret", "13800000000", "full address", "db-password"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log contains sensitive value %q: %s", secret, text)
		}
	}
	if strings.Count(text, redactedValue) < 5 {
		t.Fatalf("log did not redact all sensitive attributes: %s", text)
	}
}

func TestLoggerCaptureDoesNotContainAuthenticationOrPersonalSecrets(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("sensitive request",
		"session_key", "wx-session-key-fixture",
		"jwt", "eyJhbGciOiJIUzI1NiJ9.secret.signature",
		"password", "plain-password-fixture",
		"phone", "13800138000",
		"address", "北京市朝阳区完整收货地址",
	)

	text := output.String()
	for _, secret := range []string{
		"wx-session-key-fixture",
		"eyJhbGciOiJIUzI1NiJ9.secret.signature",
		"plain-password-fixture",
		"13800138000",
		"北京市朝阳区完整收货地址",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("captured log contains sensitive value %q: %s", secret, text)
		}
	}
}

func TestLoggerRedactsSensitiveValuesInMessage(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("request completed password=password-secret phone=13800000000 jwt=jwt-secret session_key=session-secret token=token-secret")

	text := output.String()
	for _, secret := range []string{"password-secret", "13800000000", "jwt-secret", "session-secret", "token-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log message contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "request completed") {
		t.Fatalf("log message removed non-sensitive content: %s", text)
	}
}

func TestLoggerRedactsJSONColonAndVariantMessageFields(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info(`payload {"password":"json-secret","access_token":"access-secret","phone_number":"13900000000","sessionKey":"session-secret"} password: colon-secret`)

	text := output.String()
	for _, secret := range []string{"json-secret", "access-secret", "13900000000", "session-secret", "colon-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log message contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "payload") {
		t.Fatalf("log message removed non-sensitive content: %s", text)
	}
}

func TestLoggerRedactsNestedStructuredValues(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("body", "body", map[string]any{
		"password": "nested-password",
		"safe":     "keep-this-value",
		"nested": map[string]any{
			"access_token": "nested-access-token",
			"sessionKey":   "nested-session-key",
			"items": []any{
				map[string]any{"phone_number": "13700000000"},
			},
		},
	})

	text := output.String()
	for _, secret := range []string{"nested-password", "nested-access-token", "nested-session-key", "13700000000"} {
		if strings.Contains(text, secret) {
			t.Fatalf("structured log contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "keep-this-value") {
		t.Fatalf("structured log removed non-sensitive value: %s", text)
	}
}

func TestLoggerRedactsDSNCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("connecting to postgres://db-user:dsn-password@db.example.test/shop", "body", map[string]any{
		"dsn": "postgresql://body-user:body-password@db.example.test/shop",
	})

	text := output.String()
	for _, secret := range []string{"dsn-password", "body-password"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log contains DSN credential %q: %s", secret, text)
		}
	}
}

func TestLoggerRedactsSensitiveGroupValues(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.WithGroup("password").Info("grouped request", "value", "group-password")

	if text := output.String(); strings.Contains(text, "group-password") {
		t.Fatalf("log contains sensitive group value: %s", text)
	}
}

func TestLoggerRedactsJSONArraysWithoutRegexTruncation(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info(`{"password":[0,"json-array-secret"],"safe":"keep-json"}`)

	text := output.String()
	if strings.Contains(text, "json-array-secret") {
		t.Fatalf("JSON array message contains sensitive value: %s", text)
	}
	if !strings.Contains(text, "keep-json") {
		t.Fatalf("JSON array message removed non-sensitive value: %s", text)
	}
}

func TestLoggerRedactsDSNWithEmptyUsername(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("connecting to postgres://:empty-user-dsn-password@db.example.test/shop")

	if text := output.String(); strings.Contains(text, "empty-user-dsn-password") {
		t.Fatalf("DSN with empty username contains password: %s", text)
	}
}

func TestLoggerRedactsRawBytesAsAWholeStructuredValue(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("body", "body", []byte("postgres://:byte-password@db.example.test/shop"))

	text := output.String()
	if strings.Contains(text, "byte-password") || !strings.Contains(text, `"body":"[REDACTED]"`) {
		t.Fatalf("raw bytes were not replaced as a whole: %s", text)
	}
}

func TestLoggerRedactsSensitiveAssignmentVariants(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("cookie=cookie-secret error:error-secret err=err-secret csrfToken=csrf-secret")

	text := output.String()
	for _, secret := range []string{"cookie-secret", "error-secret", "err-secret", "csrf-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("assignment variant contains sensitive value %q: %s", secret, text)
		}
	}
}

func TestLoggerRedactsNamedByteSliceAsAWholeStructuredValue(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info("body", "body", secretBytes("postgres://:named-byte-password@db.example.test/shop"))

	text := output.String()
	if strings.Contains(text, "named-byte-password") || !strings.Contains(text, `"body":"[REDACTED]"`) {
		t.Fatalf("named byte slice was not replaced as a whole: %s", text)
	}
}

func TestLoggerRedactsNestedByteSlicesBeforeJSONEncoding(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	value := map[string]any{
		"items": []any{
			&nestedByteStruct{
				Raw:   []byte("nested-byte-password"),
				Named: secretBytes("nested-named-byte-password"),
			},
		},
	}
	logger.Info("body", "body", value)

	text := output.String()
	if !strings.Contains(text, `"body":"[REDACTED]"`) {
		t.Fatalf("nested byte slices were not replaced as a whole: %s", text)
	}
}

func TestRedactStructuredValueRedactsCyclicValues(t *testing.T) {
	value := map[string]any{}
	value["self"] = value

	redacted, ok := redactStructuredValue(value)
	if !ok || redacted != redactedValue {
		t.Fatalf("cyclic value was not replaced as a whole: %#v, %v", redacted, ok)
	}
}

func TestRedactStructuredValueRedactsCyclicValuesSkippedByJSON(t *testing.T) {
	type cyclicLogValue struct {
		Self *cyclicLogValue `json:"-"`
		Safe string          `json:"safe"`
	}
	value := &cyclicLogValue{Safe: "keep-this-value"}
	value.Self = value

	if _, err := json.Marshal(value); err != nil {
		t.Fatalf("test value should be encodable because the cycle is skipped: %v", err)
	}

	redacted, ok := redactStructuredValue(value)
	if !ok || redacted != redactedValue {
		t.Fatalf("cyclic value skipped by JSON was not replaced as a whole: %#v, %v", redacted, ok)
	}
}

func TestLoggerRedactsQuotedAssignmentWithEscapedQuote(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Info(`cookie="cookie-secret\"-suffix" keep=this`)

	text := output.String()
	for _, secret := range []string{"cookie-secret", "-suffix"} {
		if strings.Contains(text, secret) {
			t.Fatalf("quoted assignment contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "keep=this") {
		t.Fatalf("quoted assignment removed non-sensitive text: %s", text)
	}
}
