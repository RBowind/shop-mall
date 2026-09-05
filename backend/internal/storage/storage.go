package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"time"

	"golang.org/x/image/webp"
)

// Sentinel errors returned by the storage layer. Handlers map these to the
// HTTP status codes prescribed by the OpenAPI contract.
var (
	// ErrUnsupportedFormat is returned when the uploaded bytes do not match
	// the JPEG, PNG, or WebP magic numbers.
	ErrUnsupportedFormat = errors.New("unsupported image format")
	// ErrInvalidImage is returned when the bytes are not a decodable image.
	ErrInvalidImage = errors.New("invalid image data")
	// ErrTooManyPixels is returned when the decoded dimensions exceed the
	// configured pixel limit.
	ErrTooManyPixels = errors.New("image exceeds pixel limit")
	// ErrImageTooLarge is returned when the upload exceeds the configured
	// byte limit.
	ErrImageTooLarge = errors.New("image exceeds size limit")
	// ErrCapacityExceeded is returned when writing the object would exceed
	// the configured storage capacity.
	ErrCapacityExceeded = errors.New("storage capacity exceeded")
	// ErrInvalidKey is returned when an object key does not match the
	// server-generated key format.
	ErrInvalidKey = errors.New("invalid object key")
	// ErrObjectNotFound is returned when a stat or delete target does not
	// exist in the volume.
	ErrObjectNotFound = errors.New("object not found")
)

// Storage stores and deletes image objects by server-generated key. Keys are
// never supplied by the client; the writing side always generates them.
type Storage interface {
	// Put writes content under key. The caller declares the exact size; a
	// mismatch between the declared size and the number of bytes read is an
	// error.
	Put(ctx context.Context, key string, content io.Reader, size int64) error
	// Delete removes the object identified by key. Deleting a missing object
	// is not an error so cleanup can be run idempotently.
	Delete(ctx context.Context, key string) error
}

// ObjectInfo is a snapshot of one stored object.
type ObjectInfo struct {
	Key     string
	ModTime time.Time
	Size    int64
}

// ObjectLister enumerates the objects currently stored. Cleanup uses it to
// reconcile the volume against live references.
type ObjectLister interface {
	ListObjects(ctx context.Context) ([]ObjectInfo, error)
}

// ObjectStater reports the live metadata of a single object. Cleanup re-stats
// the target immediately before deletion so a concurrent Touch between the
// enumeration snapshot and the delete decision cannot be bypassed.
type ObjectStater interface {
	Stat(ctx context.Context, key string) (ObjectInfo, error)
}

// CapacityReader reports the number of bytes currently used.
type CapacityReader interface {
	UsedBytes(ctx context.Context) (int64, error)
}

// CapacityProvider reports the hard capacity configured for the volume.
type CapacityProvider interface {
	CapacityBytes() int64
}

// Toucher extends the mtime of an existing object to now. The product service
// uses it to start the cleanup grace period when a product image is replaced,
// so the replaced file is kept for the configured grace period even if the
// file itself is old.
type Toucher interface {
	Touch(ctx context.Context, key string) error
}

// ImageFormat is the decoded image container format.
type ImageFormat string

const (
	FormatJPEG ImageFormat = "jpg"
	FormatPNG  ImageFormat = "png"
	FormatWebP ImageFormat = "webp"
)

// Extension returns the file extension for the format, including the dot.
func (f ImageFormat) Extension() string {
	return "." + string(f)
}

// ImageInfo is the validated outcome of an upload.
type ImageInfo struct {
	Format ImageFormat
	Width  int
	Height int
	Size   int64
}

// objectKeyPattern accepts only server-generated keys: a UUIDv4 plus one of
// the three known image extensions. The pattern deliberately excludes path
// separators, dots, and any control characters, so a client-supplied string
// can never traverse directories or escape the volume.
var objectKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|png|webp)$`)

// ValidObjectKey reports whether key is a server-generated object key.
func ValidObjectKey(key string) bool {
	return objectKeyPattern.MatchString(key)
}

// ValidateImage checks the upload against the byte limit, the magic numbers,
// full decodability, and the pixel limit, and returns the decoded metadata.
// The dimension check runs on the image header before a full decode so a
// decompression bomb that fits under the byte limit is rejected before its
// bitmap is allocated.
func ValidateImage(content []byte, maxBytes, maxPixels int64) (ImageInfo, error) {
	if maxPixels <= 0 {
		return ImageInfo{}, errors.New("validate image: pixel limit must be positive")
	}
	if int64(len(content)) > maxBytes {
		return ImageInfo{}, ErrImageTooLarge
	}
	if len(content) == 0 {
		return ImageInfo{}, ErrInvalidImage
	}
	format, err := detectFormat(content)
	if err != nil {
		return ImageInfo{}, err
	}
	cfg, err := decodeConfig(content, format)
	if err != nil {
		return ImageInfo{}, ErrInvalidImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return ImageInfo{}, ErrInvalidImage
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return ImageInfo{}, ErrTooManyPixels
	}
	decoded, err := decodeImage(content, format)
	if err != nil {
		return ImageInfo{}, ErrInvalidImage
	}
	bounds := decoded.Bounds()
	return ImageInfo{
		Format: format,
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
		Size:   int64(len(content)),
	}, nil
}

// detectFormat identifies the container from its magic bytes only. Content
// type headers and file extensions are never consulted, so a renamed or
// spoofed file that claims to be an image is rejected here.
func detectFormat(content []byte) (ImageFormat, error) {
	switch {
	case len(content) >= 3 && content[0] == 0xFF && content[1] == 0xD8 && content[2] == 0xFF:
		return FormatJPEG, nil
	case len(content) >= 8 && bytes.HasPrefix(content, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return FormatPNG, nil
	case len(content) >= 12 && bytes.Equal(content[:4], []byte("RIFF")) && bytes.Equal(content[8:12], []byte("WEBP")):
		return FormatWebP, nil
	default:
		return "", ErrUnsupportedFormat
	}
}

func decodeConfig(content []byte, format ImageFormat) (image.Config, error) {
	switch format {
	case FormatJPEG:
		return jpeg.DecodeConfig(bytes.NewReader(content))
	case FormatPNG:
		return png.DecodeConfig(bytes.NewReader(content))
	case FormatWebP:
		return webp.DecodeConfig(bytes.NewReader(content))
	default:
		return image.Config{}, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

func decodeImage(content []byte, format ImageFormat) (image.Image, error) {
	switch format {
	case FormatJPEG:
		return jpeg.Decode(bytes.NewReader(content))
	case FormatPNG:
		return png.Decode(bytes.NewReader(content))
	case FormatWebP:
		return webp.Decode(bytes.NewReader(content))
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}
