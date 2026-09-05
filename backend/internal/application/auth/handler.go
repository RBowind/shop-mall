package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	platformhttp "shop-mall/backend/internal/platform/http"

	"github.com/gin-gonic/gin"
)

// wxLoginCodeMaxRunes is the contract's WxLoginRequest.code maxLength. The
// one-time WeChat code is discarded server-side after the session lookup.
const wxLoginCodeMaxRunes = 512

// LoginHandler hosts POST /api/v1/auth/wx-login. It is the single public entry
// point that exchanges a one-time WeChat login code for a buyer session. The
// session_key is discarded by the usecase and never reaches this layer.
type LoginHandler struct {
	usecase *LoginUsecase
	limiter *LoginRateLimiter
	logger  *slog.Logger
}

// LoginHandlerDeps is the dependency bundle for NewLoginHandler. The limiter
// is optional; production wiring always supplies one so the contract's 429 is
// reachable under spray.
type LoginHandlerDeps struct {
	Usecase *LoginUsecase
	Logger  *slog.Logger
	Limiter *LoginRateLimiter
}

// NewLoginHandler validates the dependency bundle and returns the handler.
func NewLoginHandler(deps LoginHandlerDeps) (*LoginHandler, error) {
	if deps.Usecase == nil {
		return nil, errors.New("login handler: usecase is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &LoginHandler{usecase: deps.Usecase, limiter: deps.Limiter, logger: logger}, nil
}

// wxLoginRequest is the contract's WxLoginRequest payload: the single-use
// WeChat code, up to 512 characters.
type wxLoginRequest struct {
	Code string `json:"code"`
}

// buyerLoginData is the contract's BuyerLoginResponse data object: the fresh
// JWT plus the buyer projection with public int64 fields as decimal strings.
type buyerLoginData struct {
	AccessToken string   `json:"access_token"`
	User        userJSON `json:"user"`
}

// userJSON is the contract's User projection shared by wx-login and /me. The
// contract's User schema carries id, nickname, avatar_url and points_balance;
// server-only fields such as openid never appear.
type userJSON struct {
	ID            string `json:"id"`
	Nickname      string `json:"nickname"`
	AvatarURL     string `json:"avatar_url"`
	PointsBalance string `json:"points_balance"`
}

// Login handles POST /api/v1/auth/wx-login.
func (h *LoginHandler) Login(c *gin.Context) {
	var req wxLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid login payload")
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "WeChat login code is required")
		return
	}
	if utf8.RuneCountInString(code) > wxLoginCodeMaxRunes {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "WeChat login code is too long")
		return
	}
	ip := loginClientIP(c)
	if h.limiter != nil && !h.limiter.Allow(ip) {
		platformhttp.Error(c, http.StatusTooManyRequests, platformhttp.CodeRateLimited, "too many login attempts")
		return
	}
	result, err := h.usecase.Execute(c.Request.Context(), code)
	if err != nil {
		status, businessCode := mapLoginError(err)
		platformhttp.Error(c, status, businessCode, err.Error())
		return
	}
	if h.limiter != nil {
		h.limiter.Reset(ip)
	}
	platformhttp.Success(c, http.StatusOK, buyerLoginData{
		AccessToken: result.Token,
		User:        toUserJSON(result.User),
	})
}

// mapLoginError maps the usecase sentinels to the contract's wx-login status
// codes: a blank code is a 400, a failed or empty WeChat lookup is a 422
// business error, and anything else is an internal 500.
func mapLoginError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrLoginCodeRequired):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrWeChatLookup), errors.Is(err, ErrEmptyOpenID):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

// toUserJSON projects the application user onto the contract's User shape.
// Public int64 fields serialize as decimal strings.
func toUserJSON(u User) userJSON {
	return userJSON{
		ID:            u.ID.String(),
		Nickname:      u.Nickname,
		AvatarURL:     u.AvatarURL,
		PointsBalance: strconv.FormatInt(u.PointsBalance, 10),
	}
}

// BuyerAuthRouteDeps is the dependency bundle for RegisterBuyerAuthRoutes.
type BuyerAuthRouteDeps struct {
	Handler *LoginHandler
}

// RegisterBuyerAuthRoutes wires the public buyer authentication endpoints under
// /api/v1. wx-login is intentionally unauthenticated so a buyer can obtain the
// first JWT.
func RegisterBuyerAuthRoutes(group *gin.RouterGroup, deps BuyerAuthRouteDeps) {
	if group == nil || deps.Handler == nil {
		return
	}
	group.POST("/auth/wx-login", deps.Handler.Login)
}

// loginClientIP mirrors the admin handler's client IP resolution: the direct
// peer address, falling back to the X-Forwarded-For header behind a proxy.
func loginClientIP(c *gin.Context) string {
	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		ip = strings.TrimSpace(c.GetHeader("X-Forwarded-For"))
	}
	return ip
}
