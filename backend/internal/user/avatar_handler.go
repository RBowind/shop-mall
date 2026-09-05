package user

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// avatarResponse is the contract's ImageResponse projection: the stored
// object key, its public URL and the decoded dimensions.
type avatarResponse struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

// UploadAvatar handles POST /api/v1/me/avatar. The multipart field must be
// named "file"; the object key is generated server-side as a UUID plus the
// validated format extension, so a client-supplied name can never reach the
// filesystem. The stored key is immediately written back to the buyer's
// avatar_url as the public URL, keeping the profile PATCH the only other
// avatar write path.
func (h *ProfileHandler) UploadAvatar(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	// Fast-path size rejection before any bytes are read.
	if header.Size > h.uploadLimit {
		platformhttp.Error(c, http.StatusRequestEntityTooLarge, platformhttp.CodePayloadTooBig, "file exceeds upload limit")
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, h.uploadLimit+1))
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "failed to read upload")
		return
	}
	info, err := storage.ValidateImage(content, h.uploadLimit, h.maxPixels)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrImageTooLarge):
			platformhttp.Error(c, http.StatusRequestEntityTooLarge, platformhttp.CodePayloadTooBig, "file exceeds upload limit")
		case errors.Is(err, storage.ErrUnsupportedFormat):
			platformhttp.Error(c, http.StatusUnsupportedMediaType, platformhttp.CodeUnsupported, "unsupported image format")
		case errors.Is(err, storage.ErrTooManyPixels):
			platformhttp.Error(c, http.StatusUnprocessableEntity, platformhttp.CodeBusiness, "image exceeds pixel limit")
		case errors.Is(err, storage.ErrInvalidImage):
			platformhttp.Error(c, http.StatusUnprocessableEntity, platformhttp.CodeBusiness, "invalid image data")
		default:
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid image upload")
		}
		return
	}

	// Capacity is enforced before and inside the write, mirroring the admin
	// upload path.
	if h.capacity != nil {
		used, err := h.capacity.UsedBytes(c.Request.Context())
		if err != nil {
			h.logError(c, "avatar upload: capacity check", err)
			platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "storage check failed")
			return
		}
		if used+info.Size > h.capacityLimit {
			platformhttp.Error(c, http.StatusInsufficientStorage, platformhttp.CodeStorageFailure, "image storage is full")
			return
		}
	}
	key := uuid.NewString() + info.Format.Extension()
	if err := h.store.Put(c.Request.Context(), key, bytes.NewReader(content), info.Size); err != nil {
		if errors.Is(err, storage.ErrCapacityExceeded) {
			platformhttp.Error(c, http.StatusInsufficientStorage, platformhttp.CodeStorageFailure, "image storage is full")
			return
		}
		h.logError(c, "avatar upload: store image", err)
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "failed to store image")
		return
	}
	url := h.publicImageURL(key)
	if _, err := h.service.UpdateProfile(c.Request.Context(), userID, nil, &url); err != nil {
		h.logError(c, "avatar upload: update profile", err)
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "failed to update profile")
		return
	}
	platformhttp.Success(c, http.StatusCreated, avatarResponse{
		Key:    key,
		URL:    url,
		Width:  info.Width,
		Height: info.Height,
		Size:   info.Size,
	})
}

// publicImageURL resolves an object key to the public image URL served by the
// static mount, consistent with the admin upload projection.
func (h *ProfileHandler) publicImageURL(key string) string {
	return strings.TrimRight(h.publicBaseURL, "/") + "/static/images/" + key
}

func (h *ProfileHandler) logError(c *gin.Context, message string, err error) {
	h.logger.ErrorContext(c.Request.Context(), message, "error", err, "trace_id", middleware.TraceIDFromGin(c))
}
