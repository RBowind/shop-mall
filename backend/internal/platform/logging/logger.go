package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"shop-mall/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

const redactedValue = "[REDACTED]"

var (
	sensitiveMessageAssignment = regexp.MustCompile(`(?i)(["']?)(password|passwd|secret|session[_-]?key|jwt|access[_-]?token|csrf[_-]?token|token|authorization|cookie|error|err|phone(?:[_-]?number)?|address|receiver|detail)(["']?)\s*(=|:)\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^,\s;}\]]+)`)
	dsnCredentialPattern       = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]*:)[^@\s]+@`)
	jwtMessagePattern          = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\b`)
	phoneMessagePattern        = regexp.MustCompile(`\b1[3-9][0-9]{9}\b`)
)

func New(out io.Writer, level slog.Level) *slog.Logger {
	if out == nil {
		out = io.Discard
	}
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	}))
}

func WithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}
	traceID := middleware.TraceID(ctx)
	if traceID == "" {
		return logger
	}
	return logger.With("trace_id", traceID)
}

func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if logger == nil {
			return
		}
		WithTrace(c.Request.Context(), logger).InfoContext(c.Request.Context(), "http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
		)
	}
}

func redactAttr(groups []string, attr slog.Attr) slog.Attr {
	for _, group := range groups {
		if sensitiveKey(group) {
			return slog.String(attr.Key, redactedValue)
		}
	}
	if attr.Key == slog.MessageKey && attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, redactMessage(attr.Value.String()))
	}
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, redactMessage(attr.Value.String()))
	}
	if attr.Value.Kind() == slog.KindAny {
		if isByteSlice(attr.Value.Any()) {
			return slog.String(attr.Key, redactedValue)
		}
		value, ok := redactStructuredValue(attr.Value.Any())
		if !ok {
			return slog.String(attr.Key, redactedValue)
		}
		return slog.Any(attr.Key, value)
	}
	return attr
}

func redactMessage(message string) string {
	if redacted, ok := redactJSONMessage(message); ok {
		return redacted
	}
	message = sensitiveMessageAssignment.ReplaceAllStringFunc(message, redactAssignment)
	message = dsnCredentialPattern.ReplaceAllString(message, "$1"+redactedValue+"@")
	message = jwtMessagePattern.ReplaceAllString(message, redactedValue)
	return phoneMessagePattern.ReplaceAllString(message, redactedValue)
}

func redactJSONMessage(message string) (string, bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	if _, ok := decoded.(map[string]any); !ok {
		if _, ok := decoded.([]any); !ok {
			return "", false
		}
	}
	redacted, ok := redactJSONValue(decoded)
	if !ok {
		return "", false
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func redactAssignment(match string) string {
	indices := sensitiveMessageAssignment.FindStringSubmatchIndex(match)
	if len(indices) < 12 {
		return redactedValue
	}
	valueStart, valueEnd := indices[10], indices[11]
	value := match[valueStart:valueEnd]
	replacement := redactedValue
	if len(value) >= 1 && (value[0] == '"' || value[0] == '\'') {
		quotedEnd := quotedValueEnd(value)
		if quotedEnd < 0 {
			return match[:valueStart] + redactedValue
		}
		valueEnd = valueStart + quotedEnd
		replacement = string(value[0]) + redactedValue + string(value[quotedEnd-1])
	}
	return match[:valueStart] + replacement + match[valueEnd:]
}

func quotedValueEnd(value string) int {
	if len(value) < 2 || (value[0] != '"' && value[0] != '\'') {
		return -1
	}
	escaped := false
	for index := 1; index < len(value); index++ {
		if escaped {
			escaped = false
			continue
		}
		if value[index] == '\\' {
			escaped = true
			continue
		}
		if value[index] == value[0] {
			return index + 1
		}
	}
	return -1
}

func redactStructuredValue(value any) (any, bool) {
	containsBytes, safe := containsByteSlice(reflect.ValueOf(value))
	if containsBytes || !safe {
		return redactedValue, true
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return redactedValue, true
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return redactedValue, true
	}
	return redactJSONValue(decoded)
}

const maxByteSliceTraversalDepth = 1024

type byteSliceVisit struct {
	typ      reflect.Type
	kind     reflect.Kind
	pointer  uintptr
	length   int
	capacity int
}

func containsByteSlice(value reflect.Value) (found bool, safe bool) {
	defer func() {
		if recover() != nil {
			found = false
			safe = false
		}
	}()
	return walkByteSlice(value, make(map[byteSliceVisit]struct{}), 0)
}

func walkByteSlice(value reflect.Value, visited map[byteSliceVisit]struct{}, depth int) (bool, bool) {
	if depth > maxByteSliceTraversalDepth {
		return false, false
	}
	if !value.IsValid() {
		return false, true
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return false, true
		}
		return walkByteSlice(value.Elem(), visited, depth+1)
	case reflect.Pointer:
		if value.IsNil() {
			return false, true
		}
		if alreadyVisitedByteSliceValue(value, visited) {
			return false, false
		}
		return walkByteSlice(value.Elem(), visited, depth+1)
	case reflect.Map:
		if value.IsNil() {
			return false, true
		}
		if alreadyVisitedByteSliceValue(value, visited) {
			return false, false
		}
		iter := value.MapRange()
		for iter.Next() {
			if found, safe := walkByteSlice(iter.Key(), visited, depth+1); found || !safe {
				return found, safe
			}
			if found, safe := walkByteSlice(iter.Value(), visited, depth+1); found || !safe {
				return found, safe
			}
		}
		return false, true
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return true, true
		}
		if value.IsNil() {
			return false, true
		}
		if alreadyVisitedByteSliceValue(value, visited) {
			return false, false
		}
		for index := 0; index < value.Len(); index++ {
			if found, safe := walkByteSlice(value.Index(index), visited, depth+1); found || !safe {
				return found, safe
			}
		}
		return false, true
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if found, safe := walkByteSlice(value.Index(index), visited, depth+1); found || !safe {
				return found, safe
			}
		}
		return false, true
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if found, safe := walkByteSlice(value.Field(index), visited, depth+1); found || !safe {
				return found, safe
			}
		}
		return false, true
	default:
		return false, true
	}
}

func alreadyVisitedByteSliceValue(value reflect.Value, visited map[byteSliceVisit]struct{}) bool {
	visit := byteSliceVisit{
		typ:     value.Type(),
		kind:    value.Kind(),
		pointer: value.Pointer(),
	}
	if value.Kind() == reflect.Slice {
		visit.length = value.Len()
		visit.capacity = value.Cap()
	}
	if _, ok := visited[visit]; ok {
		return true
	}
	visited[visit] = struct{}{}
	return false
}

func isByteSlice(value any) bool {
	typ := reflect.TypeOf(value)
	return typ != nil && typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8
}

func redactJSONValue(value any) (any, bool) {
	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			if sensitiveKey(key) {
				value[key] = redactedValue
				continue
			}
			redacted, ok := redactJSONValue(nested)
			if !ok {
				return nil, false
			}
			value[key] = redacted
		}
		return value, true
	case []any:
		for index, nested := range value {
			redacted, ok := redactJSONValue(nested)
			if !ok {
				return nil, false
			}
			value[index] = redacted
		}
		return value, true
	case string:
		return redactMessage(value), true
	default:
		return value, true
	}
}

func sensitiveKey(key string) bool {
	key = normalizeKey(key)
	for _, part := range []string{
		"authorization", "password", "passwd", "secret", "session_key", "jwt", "token", "phone", "address", "receiver", "detail", "cookie", "error", "err",
	} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func normalizeKey(key string) string {
	var normalized strings.Builder
	var previous rune
	for _, current := range key {
		switch current {
		case '-', '.', ' ', ':':
			normalized.WriteByte('_')
			previous = '_'
			continue
		}
		if unicode.IsUpper(current) && normalized.Len() > 0 && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			normalized.WriteByte('_')
		}
		normalized.WriteRune(unicode.ToLower(current))
		previous = current
	}
	return normalized.String()
}
