package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"testing"

	"shop-mall/backend/tests/e2e"
)

// multipartFile builds a multipart body for the "file" field with an explicit
// part Content-Type, so the tests can declare a MIME type that does not match
// the magic bytes.
func multipartFile(t *testing.T, filename, partContentType string, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", partContentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), writer.FormDataContentType()
}

// solidImage renders a solid-color image without allocating a full pixel
// buffer, so a pixel-limit PNG can exceed the pixel cap while staying far
// under the byte limit.
type solidImage struct{ width, height int }

func (s solidImage) ColorModel() color.Model { return color.RGBAModel }
func (s solidImage) Bounds() image.Rectangle { return image.Rect(0, 0, s.width, s.height) }
func (s solidImage) At(x, y int) color.Color { return color.RGBA{R: 200, G: 30, B: 30, A: 255} }

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// serverKeyPattern mirrors storage.objectKeyPattern: the only key format the
// server ever generates.
var serverKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|png|webp)$`)

func upload(t *testing.T, h *e2e.Harness, filename, partContentType string, content []byte) (int, map[string]any, string) {
	t.Helper()
	body, boundary := multipartFile(t, filename, partContentType, content)
	req := h.RawReq(http.MethodPost, "/api/admin/v1/images", body, map[string]string{
		"Content-Type": boundary,
		"Origin":       "https://admin.example.test",
		"X-CSRF-Token": h.SuperAdmin.CSRF,
	})
	resp := h.Do(h.SuperAdmin.Client, req)
	env, raw := h.Decode(resp)
	h.AssertTraceID(resp, env)
	data := map[string]any{}
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("decode upload data: %v", err)
		}
	}
	return resp.StatusCode, data, string(raw)
}

func TestUploadMIMESpoofingRejected(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// Declared image/png with plain-text magic bytes: the server must reject
	// on magic bytes, never on the declared type.
	spoof := []byte("this is absolutely not an image, just text pretending to be a png")
	status, _, raw := upload(t, h, "spoofed.png", "image/png", spoof)
	if status != http.StatusUnsupportedMediaType {
		t.Fatalf("MIME spoof status = %d, want 415: %s", status, raw)
	}
}

func TestUploadInvalidImageBytesRejected(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// JPEG magic bytes followed by truncated garbage: format is detected but
	// the bytes are not decodable.
	invalid := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x02, 0x03}
	status, _, raw := upload(t, h, "broken.jpg", "image/jpeg", invalid)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid image status = %d, want 422: %s", status, raw)
	}
}

func TestUploadOversizedRejected(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// 3 MB of bytes exceeds the 2 MB upload limit.
	oversized := bytes.Repeat([]byte{0xAB}, 3*1024*1024)
	status, _, raw := upload(t, h, "huge.png", "image/png", oversized)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413: %s", status, raw)
	}
}

func TestUploadPixelLimitRejected(t *testing.T) {
	h := e2e.NewHarness(t, testDB)

	// A real, decodable PNG whose dimensions exceed the 25M pixel cap while
	// staying far under the 2 MB byte limit.
	content := encodePNG(t, solidImage{width: 8000, height: 3200}) // 25.6M pixels
	if len(content) > 2*1024*1024 {
		t.Fatalf("pixel-limit PNG is %d bytes, expected far under 2MB", len(content))
	}
	status, _, raw := upload(t, h, "pixels.png", "image/png", content)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("pixel-limit status = %d, want 422: %s", status, raw)
	}
}

// TestUploadPathTraversalClientKeyIgnored proves the client-controlled file
// name never reaches the filesystem: the server always returns its own
// generated UUID key regardless of a traversal or absolute-path file name.
func TestUploadPathTraversalClientKeyIgnored(t *testing.T) {
	h := e2e.NewHarness(t, testDB)
	content := encodePNG(t, solidImage{width: 8, height: 8})

	for _, name := range []string{
		"../../etc/passwd",
		"/etc/passwd",
		"..\\..\\windows\\system32\\config",
		"evil.png/../../../../tmp/owned",
	} {
		status, data, raw := upload(t, h, name, "image/png", content)
		if status != http.StatusCreated {
			t.Fatalf("filename %q status = %d, want 201: %s", name, status, raw)
		}
		key, _ := data["key"].(string)
		if !serverKeyPattern.MatchString(key) {
			t.Fatalf("filename %q produced non-server key %q", name, key)
		}
		if strings.Contains(key, "..") || strings.ContainsAny(key, `/\`) || strings.Contains(key, "passwd") {
			t.Fatalf("filename %q influenced the object key %q", name, key)
		}
	}
}
