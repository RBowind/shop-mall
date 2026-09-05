package admin

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/metrics"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImageHandler owns POST /api/admin/v1/images. It validates the upload
// (size, magic bytes, decoded format, pixel limit, capacity), generates the
// server-side object key, stores the object, and records an audit row.
type ImageHandler struct {
	store         storage.Storage
	capacity      storage.CapacityReader
	capacityLimit int64
	uploadLimit   int64
	maxPixels     int64
	publicBaseURL string
	db            *gorm.DB
	service       *Service
	logger        *slog.Logger
	now           func() time.Time
	metrics       *metrics.Metrics
}

// ImageHandlerDeps is the dependency bundle for NewImageHandler.
type ImageHandlerDeps struct {
	Store         storage.Storage
	Capacity      storage.CapacityReader
	CapacityLimit int64
	UploadLimit   int64
	MaxPixels     int64
	PublicBaseURL string
	DB            *gorm.DB
	Service       *Service
	Logger        *slog.Logger
	Now           func() time.Time
	Metrics       *metrics.Metrics
}

// NewImageHandler validates the dependency bundle and returns an ImageHandler.
func NewImageHandler(deps ImageHandlerDeps) (*ImageHandler, error) {
	if deps.Store == nil {
		return nil, errors.New("image handler: storage is required")
	}
	if deps.UploadLimit <= 0 {
		return nil, errors.New("image handler: upload limit must be positive")
	}
	if deps.MaxPixels <= 0 {
		return nil, errors.New("image handler: pixel limit must be positive")
	}
	if deps.CapacityLimit <= 0 {
		return nil, errors.New("image handler: capacity must be positive")
	}
	if strings.TrimSpace(deps.PublicBaseURL) == "" {
		return nil, errors.New("image handler: public base URL is required")
	}
	if deps.DB == nil {
		return nil, errors.New("image handler: database is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &ImageHandler{
		store:         deps.Store,
		capacity:      deps.Capacity,
		capacityLimit: deps.CapacityLimit,
		uploadLimit:   deps.UploadLimit,
		maxPixels:     deps.MaxPixels,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(deps.PublicBaseURL), "/"),
		db:            deps.DB,
		service:       deps.Service,
		logger:        logger,
		now:           now,
		metrics:       deps.Metrics,
	}, nil
}

// imageResponse is the contract's ImageResponse projection.
type imageResponse struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

// Upload handles POST /api/admin/v1/images. The multipart field must be named
// "file"; the object key is always generated server-side as a UUID plus the
// validated format extension, so a client-supplied file name or path can never
// reach the filesystem.
func (h *ImageHandler) Upload(c *gin.Context) {
	traceID := middleware.TraceIDFromGin(c)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.recordUploadFailure("missing_file")
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	// Fast-path size rejection before any bytes are read.
	if header.Size > h.uploadLimit {
		h.recordUploadFailure("too_large")
		platformhttp.Error(c, http.StatusRequestEntityTooLarge, platformhttp.CodePayloadTooBig, "file exceeds upload limit")
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, h.uploadLimit+1))
	if err != nil {
		h.recordUploadFailure("read_failed")
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "failed to read upload")
		return
	}
	info, err := storage.ValidateImage(content, h.uploadLimit, h.maxPixels)
	if err != nil {
		h.recordUploadFailure(reasonForUploadError(err))
		h.writeFailureAudit(c, traceID, "invalid_image", reasonForUploadError(err))
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

	// Storage capacity is enforced before and inside the write. The volume
	// usage is refreshed on every upload so the disk-pressure gauge tracks the
	// live state; the composition root feeds the same gauge from the cleanup
	// loop when files are deleted.
	var used int64
	if h.capacity != nil {
		var err error
		used, err = h.capacity.UsedBytes(c.Request.Context())
		if err != nil {
			h.recordUploadFailure("storage_check_failed")
			h.logError(c, traceID, "capacity check", err)
			platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "storage check failed")
			return
		}
		h.recordStorageUsage(used)
		if used+info.Size > h.capacityLimit {
			h.recordUploadFailure("capacity_exceeded")
			platformhttp.Error(c, http.StatusInsufficientStorage, platformhttp.CodeStorageFailure, "image storage is full")
			return
		}
	}
	key := uuid.NewString() + info.Format.Extension()
	if err := h.store.Put(c.Request.Context(), key, bytes.NewReader(content), info.Size); err != nil {
		if errors.Is(err, storage.ErrCapacityExceeded) {
			h.recordUploadFailure("capacity_exceeded")
			platformhttp.Error(c, http.StatusInsufficientStorage, platformhttp.CodeStorageFailure, "image storage is full")
			return
		}
		h.recordUploadFailure("store_failed")
		h.logError(c, traceID, "store image", err)
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "failed to store image")
		return
	}
	h.recordStorageUsage(used + info.Size)
	h.writeAudit(c, traceID, imageResponse{Key: key, URL: h.publicURL(key), Width: info.Width, Height: info.Height, Size: info.Size})
	platformhttp.Success(c, http.StatusCreated, imageResponse{
		Key:    key,
		URL:    h.publicURL(key),
		Width:  info.Width,
		Height: info.Height,
		Size:   info.Size,
	})
}

func (h *ImageHandler) publicURL(key string) string {
	return h.publicBaseURL + "/static/images/" + key
}

// actorRole resolves the current administrator's role for the audit row.
func (h *ImageHandler) actorRole(c *gin.Context) string {
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		return ""
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		return ""
	}
	if h.service == nil {
		return ""
	}
	view, err := h.service.FindByID(c.Request.Context(), actorID)
	if err != nil {
		return ""
	}
	return view.RoleName
}

func (h *ImageHandler) actorID(c *gin.Context) *uid.ID {
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		return nil
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		return nil
	}
	return &actorID
}

func (h *ImageHandler) writeAudit(c *gin.Context, traceID string, response imageResponse) {
	actorRole := h.actorRole(c)
	err := database.RunTransaction(c.Request.Context(), h.db, func(tx *gorm.DB) error {
		return writeAuditLog(tx, auditEntry{
			ActorAdminID: h.actorID(c),
			ActorRole:    actorRole,
			Action:       "image.upload",
			TargetType:   "image",
			Result:       "success",
			AfterData: map[string]any{
				"format": extensionToFormat(response.Key),
				"width":  response.Width,
				"height": response.Height,
				"size":   response.Size,
			},
		})
	})
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "image.upload audit write failed", "error", err, "trace_id", traceID)
	}
}

func (h *ImageHandler) writeFailureAudit(c *gin.Context, traceID string, cause, reason string) {
	err := database.RunTransaction(c.Request.Context(), h.db, func(tx *gorm.DB) error {
		return writeAuditLog(tx, auditEntry{
			ActorAdminID: h.actorID(c),
			ActorRole:    h.actorRole(c),
			Action:       "image.upload",
			TargetType:   "image",
			Result:       "failure",
			AfterData: map[string]any{
				"cause":  cause,
				"reason": reason,
			},
		})
	})
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "image.upload failure audit write failed", "error", err, "trace_id", traceID)
	}
}

func (h *ImageHandler) logError(c *gin.Context, traceID string, operation string, err error) {
	h.logger.ErrorContext(c.Request.Context(), "image handler", "operation", operation, "error", err, "trace_id", traceID)
}

// recordUploadFailure increments the upload-failure metric. The metrics bundle
// is optional; handlers constructed without it simply skip recording.
func (h *ImageHandler) recordUploadFailure(reason string) {
	if h == nil || h.metrics == nil {
		return
	}
	h.metrics.RecordUploadFailure(reason)
}

// recordStorageUsage refreshes the image-volume usage gauge. The metrics
// bundle is optional; handlers constructed without it simply skip recording.
func (h *ImageHandler) recordStorageUsage(used int64) {
	if h == nil || h.metrics == nil {
		return
	}
	h.metrics.SetImageStorageUsage(used, h.capacityLimit)
}

func extensionToFormat(key string) string {
	switch {
	case strings.HasSuffix(key, ".jpg"):
		return "jpg"
	case strings.HasSuffix(key, ".png"):
		return "png"
	case strings.HasSuffix(key, ".webp"):
		return "webp"
	default:
		return "unknown"
	}
}

func reasonForUploadError(err error) string {
	switch {
	case errors.Is(err, storage.ErrImageTooLarge):
		return "too_large"
	case errors.Is(err, storage.ErrUnsupportedFormat):
		return "unsupported_format"
	case errors.Is(err, storage.ErrTooManyPixels):
		return "too_many_pixels"
	case errors.Is(err, storage.ErrInvalidImage):
		return "invalid_image"
	default:
		return "invalid_upload"
	}
}

// ImageRouteDeps is the dependency bundle for RegisterImageRoutes.
type ImageRouteDeps struct {
	Handler            *ImageHandler
	Signer             *tokens.Signer
	Cookie             config.AdminCookieConfig
	Now                func() time.Time
	PermissionResolver middleware.PermissionResolver
	AdminStateResolver middleware.AdminStateResolver
}

// RegisterImageRoutes wires POST /api/admin/v1/images with AdminAuth and the
// contract's image:write permission.
func RegisterImageRoutes(group *gin.RouterGroup, deps ImageRouteDeps) {
	if group == nil || deps.Handler == nil || deps.Signer == nil {
		return
	}
	jwtService, err := middleware.NewJWTServiceFromSigner(deps.Signer)
	if err != nil {
		panic(err)
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	protected := group.Group("")
	protected.Use(middleware.AdminAuthWithClock(jwtService, deps.Cookie.AccessTokenName, now, deps.AdminStateResolver))
	protected.POST("/images",
		middleware.AdminPermission(deps.PermissionResolver, "image:write"),
		deps.Handler.Upload)
}
