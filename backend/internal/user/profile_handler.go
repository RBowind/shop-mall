package user

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/storage"

	"github.com/gin-gonic/gin"
)

const (
	profileNicknameMaxRunes  = 64
	profileAvatarURLMaxRunes = 2048
)

// ProfileHandler hosts the authenticated buyer profile endpoints under
// /api/v1/me. The authenticated userID always comes from the bearer JWT
// middleware context; no request body carries an owner id.
type ProfileHandler struct {
	service       *Service
	logger        *slog.Logger
	store         storage.Storage
	capacity      storage.CapacityReader
	capacityLimit int64
	uploadLimit   int64
	maxPixels     int64
	publicBaseURL string
}

// ProfileHandlerDeps is the dependency bundle for NewProfileHandler.
// Store/capacity/limits/publicBaseURL are only needed for the avatar upload
// endpoint and may be left zero-valued on handlers that skip it.
type ProfileHandlerDeps struct {
	Service       *Service
	Logger        *slog.Logger
	Store         storage.Storage
	Capacity      storage.CapacityReader
	CapacityLimit int64
	UploadLimit   int64
	MaxPixels     int64
	PublicBaseURL string
}

// NewProfileHandler validates the dependency bundle and returns the handler.
func NewProfileHandler(deps ProfileHandlerDeps) (*ProfileHandler, error) {
	if deps.Service == nil {
		return nil, errors.New("profile handler: service is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ProfileHandler{
		service:       deps.Service,
		logger:        logger,
		store:         deps.Store,
		capacity:      deps.Capacity,
		capacityLimit: deps.CapacityLimit,
		uploadLimit:   deps.UploadLimit,
		maxPixels:     deps.MaxPixels,
		publicBaseURL: deps.PublicBaseURL,
	}, nil
}

// profileJSON is the contract's User projection: public int64 fields are
// serialized as decimal strings. The contract's User schema carries id,
// nickname, avatar_url and points_balance; server-only fields such as openid
// never appear.
type profileJSON struct {
	ID            string `json:"id"`
	Nickname      string `json:"nickname"`
	AvatarURL     string `json:"avatar_url"`
	PointsBalance string `json:"points_balance"`
}

// userUpdateRequest is the contract's UserUpdateRequest: both fields are
// optional and updated only when present.
type userUpdateRequest struct {
	Nickname  *string `json:"nickname"`
	AvatarURL *string `json:"avatar_url"`
}

// GetMe handles GET /api/v1/me.
func (h *ProfileHandler) GetMe(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	buyer, err := h.service.GetByID(c.Request.Context(), userID)
	if err != nil {
		h.respondProfileError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toProfileJSON(buyer))
}

// UpdateMe handles PATCH /api/v1/me.
func (h *ProfileHandler) UpdateMe(c *gin.Context) {
	var req userUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid profile payload")
		return
	}
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	if req.Nickname == nil && req.AvatarURL == nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "empty profile update")
		return
	}
	// Presence is threaded through to the service so a PATCH writes ONLY the
	// fields that appeared; an absent field keeps its current value.
	var nickname *string
	if req.Nickname != nil {
		trimmed := strings.TrimSpace(*req.Nickname)
		if utf8.RuneCountInString(trimmed) > profileNicknameMaxRunes {
			platformhttp.Error(c, http.StatusUnprocessableEntity, platformhttp.CodeBusiness, "nickname must be at most 64 characters")
			return
		}
		nickname = &trimmed
	}
	var avatarURL *string
	if req.AvatarURL != nil {
		value := *req.AvatarURL
		if utf8.RuneCountInString(value) > profileAvatarURLMaxRunes {
			platformhttp.Error(c, http.StatusUnprocessableEntity, platformhttp.CodeBusiness, "avatar_url must be at most 2048 characters")
			return
		}
		avatarURL = &value
	}
	buyer, err := h.service.UpdateProfile(c.Request.Context(), userID, nickname, avatarURL)
	if err != nil {
		h.respondProfileError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toProfileJSON(buyer))
}

// respondProfileError maps the service error to the contract's /me status
// codes and logs internal failures. A missing profile is a 404; everything else
// is an internal 500.
func (h *ProfileHandler) respondProfileError(c *gin.Context, err error) {
	status, code := mapProfileError(err)
	if status >= http.StatusInternalServerError {
		h.logger.ErrorContext(c.Request.Context(), "profile handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	}
	platformhttp.Error(c, status, code, err.Error())
}

// mapProfileError maps the service errors to the contract's /me status codes:
// a missing profile is a 404, everything else is an internal 500.
func mapProfileError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrUserNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func toProfileJSON(u User) profileJSON {
	return profileJSON{
		ID:            u.ID.String(),
		Nickname:      u.Nickname,
		AvatarURL:     u.AvatarURL,
		PointsBalance: strconv.FormatInt(u.PointsBalance, 10),
	}
}
