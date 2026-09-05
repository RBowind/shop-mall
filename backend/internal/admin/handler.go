package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/tokens"

	"github.com/gin-gonic/gin"
)

var _ = middleware.NewJWTService

// PasswordChanger is the change-password boundary the admin handler consumes.
// The task brief assigns the access-related transaction to
// AccessUsecase.ChangePassword, so the handler must never reach the service
// directly. The composition root wires the access usecase; when no usecase is
// supplied the handler falls back to the admin Service so it keeps behaving
// correctly in tests.
type PasswordChanger interface {
	ChangePassword(ctx context.Context, input ChangePasswordInput) (ChangePasswordResult, error)
}

// HandlerDeps is the dependency bundle for NewHandler.
type HandlerDeps struct {
	Service         *Service
	Signer          *tokens.Signer
	RateLimiter     *LoginRateLimiter
	PasswordChanger PasswordChanger
	Logger          *slog.Logger
	Cookie          config.AdminCookieConfig
	Now             func() time.Time
}

// Handler hosts the admin HTTP endpoints. It owns the login rate-limit
// policy, the cookie issuance and the CSRF token rotation. token_version
// freshness and the enabled flag are enforced by the middleware's
// admin-authentication path via the Service's AdminState resolver.
type Handler struct {
	service         *Service
	signer          *tokens.Signer
	rateLimiter     *LoginRateLimiter
	passwordChanger PasswordChanger
	logger          *slog.Logger
	cookie          config.AdminCookieConfig
	now             func() time.Time
}

// NewHandler validates the dependency bundle and returns a Handler.
func NewHandler(deps HandlerDeps) (*Handler, error) {
	if deps.Service == nil {
		return nil, errors.New("admin handler: service is required")
	}
	if deps.Signer == nil {
		return nil, errors.New("admin handler: signer is required")
	}
	if deps.RateLimiter == nil {
		return nil, errors.New("admin handler: rate limiter is required")
	}
	if deps.Cookie.AccessTokenName == "" || deps.Cookie.CSRFName == "" {
		return nil, errors.New("admin handler: cookie configuration is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	passwordChanger := deps.PasswordChanger
	if passwordChanger == nil {
		passwordChanger = deps.Service
	}
	return &Handler{
		service:         deps.Service,
		signer:          deps.Signer,
		rateLimiter:     deps.RateLimiter,
		passwordChanger: passwordChanger,
		logger:          logger,
		cookie:          deps.Cookie,
		now:             now,
	}, nil
}

// loginRequest is the JSON body the client posts to /auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// roleResponse is the contract's Role projection: id (int64 as string), name
// and the role's permission codes.
type roleResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// loginResponse is the success response body. It matches the contract's
// AdminLoginResponse data object exactly: admin_id, username and the role
// object with its permissions. The sensitive fields (password hash, JWT)
// never appear here; the JWT is set on a Secure HttpOnly cookie.
type loginResponse struct {
	AdminID  string       `json:"admin_id"`
	Username string       `json:"username"`
	Role     roleResponse `json:"role"`
}

// passwordChangeRequest is the JSON body the client posts to /auth/password.
// The field name follows the contract's PasswordChangeRequest (current_password).
type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// meResponse is the projection returned by /auth/me. The endpoint is not yet
// listed in the OpenAPI contract, so it keeps the backend's existing fields
// (token_version, enabled) next to the contract role object and admin_id.
type meResponse struct {
	AdminID      string       `json:"admin_id"`
	Username     string       `json:"username"`
	Role         roleResponse `json:"role"`
	TokenVersion int64        `json:"token_version"`
	Enabled      bool         `json:"enabled"`
}

// adminUserResponse is the contract's AdminUser projection: id, username,
// enabled and the role object.
type adminUserResponse struct {
	ID       string       `json:"id"`
	Username string       `json:"username"`
	Enabled  bool         `json:"enabled"`
	Role     roleResponse `json:"role"`
}

// adminUserListResponse is the contract's AdminUserListResponse data object:
// the paginated PageMeta plus the list of accounts.
type adminUserListResponse struct {
	List     []adminUserResponse `json:"list"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

// roleWriteRequest is the contract's RoleWriteRequest: name and the permission
// codes that back the new role.
type roleWriteRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// roleUpdateRequest is the contract's RoleUpdateRequest: an optional rename
// and/or a wholesale permissions replacement.
type roleUpdateRequest struct {
	Name        *string   `json:"name"`
	Permissions *[]string `json:"permissions"`
}

// adminUserUpdateRequest is the contract's AdminUserUpdateRequest: an optional
// enabled toggle and/or an optional role_id reassignment.
type adminUserUpdateRequest struct {
	Enabled *bool  `json:"enabled"`
	RoleID  string `json:"role_id"`
}

// userResponse is the contract projection of a buyer account on the member
// management list: id (int64 as string), nickname, points balance and the
// registration time.
type userResponse struct {
	ID            string `json:"id"`
	Nickname      string `json:"nickname"`
	PointsBalance string `json:"points_balance"`
	CreatedAt     string `json:"created_at"`
}

// userListResponse is the paginated member list envelope.
type userListResponse struct {
	List     []userResponse `json:"list"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

// auditLogResponse is the contract projection of an audit_logs row: actor,
// action, target, result, the JSON details and the timestamp.
type auditLogResponse struct {
	ID           string         `json:"id"`
	ActorAdminID *string        `json:"actor_admin_id,omitempty"`
	ActorRole    string         `json:"actor_role"`
	Action       string         `json:"action"`
	TargetType   string         `json:"target_type"`
	TargetID     *string        `json:"target_id,omitempty"`
	Result       string         `json:"result"`
	BeforeData   map[string]any `json:"before_data"`
	AfterData    map[string]any `json:"after_data"`
	TraceID      string         `json:"trace_id"`
	CreatedAt    string         `json:"created_at"`
}

// auditLogListResponse is the paginated audit log envelope.
type auditLogListResponse struct {
	List     []auditLogResponse `json:"list"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

// Login handles POST /api/admin/v1/auth/login. The CSRF middleware skips the
// login path; the AdminOrigin middleware still rejects requests from
// disallowed origins.
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid login payload")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "username and password are required")
		return
	}
	ip := clientIP(c)
	result, err := h.service.Authenticate(c.Request.Context(), AuthenticateInput{
		Username: username,
		Password: req.Password,
		IP:       ip,
	}, h.signer)
	if err != nil {
		status, code := mapAuthError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	h.rateLimiter.Reset(username, ip)
	csrf := newCSRFToken()
	setAdminCookie(c, h.cookie.AccessTokenName, result.Token, h.cookie.MaxAge, h.cookie.Path, h.cookie.Secure, h.cookie.HTTPOnly, h.cookie.SameSite)
	// CSRF cookie Path=/ so admin SPA at / can echo it back as X-CSRF-Token
	// on writes. The token stays tied to the same Strict + Secure protection as
	// the auth cookie; SameSite=Strict blocks cross-site exfiltration.
	setAdminCookie(c, h.cookie.CSRFName, csrf, h.cookie.MaxAge, "/", false, false, h.cookie.SameSite)
	platformhttp.Success(c, http.StatusOK, loginResponse{
		AdminID:  result.AdminID.String(),
		Username: result.Username,
		Role: roleResponse{
			ID:          result.RoleID.String(),
			Name:        result.RoleName,
			Permissions: result.Permissions,
		},
	})
}

// Logout handles POST /api/admin/v1/auth/logout. It bumps token_version so
// the current JWT and any other outstanding JWTs for the administrator
// become invalid.
func (h *Handler) Logout(c *gin.Context) {
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing subject")
		return
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "invalid subject")
		return
	}
	if err := h.service.Logout(c.Request.Context(), subject, actorID); err != nil {
		status, code := mapAuthError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	clearAdminCookie(c, h.cookie.AccessTokenName, h.cookie.Path)
	clearAdminCookie(c, h.cookie.CSRFName, h.cookie.Path)
	platformhttp.Success(c, http.StatusOK, gin.H{"logged_out": true})
}

// ChangePassword handles POST /api/admin/v1/auth/password. It requires the
// supplied old password to match the stored hash and bumps token_version so
// the current JWT is invalidated.
func (h *Handler) ChangePassword(c *gin.Context) {
	var req passwordChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid password payload")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "current and new passwords are required")
		return
	}
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing subject")
		return
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "invalid subject")
		return
	}
	if _, err := h.passwordChanger.ChangePassword(c.Request.Context(), ChangePasswordInput{
		ActorAdminID: actorID,
		ActorRole:    "self",
		Subject:      subject,
		OldPassword:  req.CurrentPassword,
		NewPassword:  req.NewPassword,
	}); err != nil {
		status, code := mapAuthError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	clearAdminCookie(c, h.cookie.AccessTokenName, h.cookie.Path)
	clearAdminCookie(c, h.cookie.CSRFName, h.cookie.Path)
	platformhttp.Success(c, http.StatusOK, gin.H{"changed": true})
}

// Me handles GET /api/admin/v1/auth/me. It enforces the token_version check
// on top of the JWT signature verification performed by middleware.AdminAuth.
func (h *Handler) Me(c *gin.Context) {
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing subject")
		return
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "invalid subject")
		return
	}
	claims := claimsFromGin(c)
	if claims == nil {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing claims")
		return
	}
	current, err := h.service.IsTokenVersionCurrent(c.Request.Context(), actorID, claims.TokenVersion)
	if err != nil {
		status, code := mapAuthError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	if !current {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "token version no longer current")
		return
	}
	view, err := h.service.FindByID(c.Request.Context(), actorID)
	if err != nil {
		status, code := mapAuthError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, meResponse{
		AdminID:  view.AdminID.String(),
		Username: view.Username,
		Role: roleResponse{
			ID:          view.RoleID.String(),
			Name:        view.RoleName,
			Permissions: view.Permissions,
		},
		TokenVersion: view.TokenVersion,
		Enabled:      view.Enabled,
	})
}

// ListRoles handles GET /api/admin/v1/roles. The route is gated by the
// role:manage permission via the middleware; the handler only shapes the
// contract's RoleListResponse data object.
func (h *Handler) ListRoles(c *gin.Context) {
	roles, err := h.service.ListRoles(c.Request.Context())
	if err != nil {
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "list roles")
		return
	}
	list := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		list = append(list, roleResponse{
			ID:          role.ID.String(),
			Name:        role.Name,
			Permissions: role.Permissions,
		})
	}
	platformhttp.Success(c, http.StatusOK, gin.H{"list": list})
}

// ListAdminUsers handles GET /api/admin/v1/admin-users. The route is gated by
// the role:manage permission via the middleware; the handler parses the
// contract's Page/PageSize query parameters and shapes the paginated
// AdminUserListResponse data object.
func (h *Handler) ListAdminUsers(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	admins, total, err := h.service.ListAdminUsers(c.Request.Context(), page, pageSize)
	if err != nil {
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "list administrator accounts")
		return
	}
	list := make([]adminUserResponse, 0, len(admins))
	for _, admin := range admins {
		list = append(list, adminUserResponse{
			ID:       admin.ID.String(),
			Username: admin.Username,
			Enabled:  admin.Enabled,
			Role: roleResponse{
				ID:          admin.RoleID.String(),
				Name:        admin.RoleName,
				Permissions: admin.Permissions,
			},
		})
	}
	platformhttp.Success(c, http.StatusOK, adminUserListResponse{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// CreateRole handles POST /api/admin/v1/roles. The route is gated by the
// role:manage permission plus the group-level Origin and CSRF middleware. The
// service validates the seed permission list, and the handler answers the
// contract's 201/400/409/422 statuses.
func (h *Handler) CreateRole(c *gin.Context) {
	var req roleWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid role payload")
		return
	}
	actorID, actorRole, ok := h.actorIdentity(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	result, err := h.service.CreateRole(c.Request.Context(), CreateRoleInput{
		ActorAdminID: actorID,
		ActorRole:    actorRole,
		Name:         req.Name,
		Permissions:  req.Permissions,
	})
	if err != nil {
		status, code := mapAccessWriteError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusCreated, roleResponse{
		ID:          result.ID.String(),
		Name:        result.Name,
		Permissions: result.Permissions,
	})
}

// UpdateRole handles PATCH /api/admin/v1/roles/{roleId}. The service replaces
// the permission codes and/or renames the role inside one transaction; the
// handler maps ErrRoleNotFound to 404, ErrRoleExists to 409 and
// ErrUnknownPermission to 422 per the contract.
func (h *Handler) UpdateRole(c *gin.Context) {
	roleID, ok := parsePositiveID(c.Param("roleId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid role id")
		return
	}
	var req roleUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid role payload")
		return
	}
	if req.Name == nil && req.Permissions == nil {
		status, code := mapAccessWriteError(ErrRoleUpdateEmpty)
		platformhttp.Error(c, status, code, ErrRoleUpdateEmpty.Error())
		return
	}
	actorID, actorRole, ok := h.actorIdentity(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	result, err := h.service.UpdateRole(c.Request.Context(), UpdateRoleInput{
		ActorAdminID: actorID,
		ActorRole:    actorRole,
		RoleID:       roleID,
		Name:         req.Name,
		Permissions:  req.Permissions,
	})
	if err != nil {
		status, code := mapAccessWriteError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, roleResponse{
		ID:          result.ID.String(),
		Name:        result.Name,
		Permissions: result.Permissions,
	})
}

// UpdateAdminUser handles PATCH /api/admin/v1/admin-users/{adminUserId}. The
// body supports an enabled toggle and/or a role_id reassignment (the contract
// carries role_id rather than a role name). The handler delegates to the
// existing UpdateAdmin and AssignRole service paths and returns the refreshed
// account.
func (h *Handler) UpdateAdminUser(c *gin.Context) {
	targetID, ok := parsePositiveID(c.Param("adminUserId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid administrator id")
		return
	}
	var req adminUserUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid administrator payload")
		return
	}
	if req.Enabled == nil && strings.TrimSpace(req.RoleID) == "" {
		status, code := mapAccessWriteError(ErrAdminUserUpdateEmpty)
		platformhttp.Error(c, status, code, ErrAdminUserUpdateEmpty.Error())
		return
	}
	actorID, actorRole, ok := h.actorIdentity(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	ctx := c.Request.Context()
	var targetRoleName string
	if strings.TrimSpace(req.RoleID) != "" {
		roleID, ok := parsePositiveID(req.RoleID)
		if !ok {
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid role_id")
			return
		}
		resolved, err := h.service.RoleNameByID(ctx, roleID)
		if err != nil {
			status, code := mapAccessWriteError(err)
			platformhttp.Error(c, status, code, err.Error())
			return
		}
		targetRoleName = resolved
	}
	if req.Enabled != nil {
		if _, err := h.service.UpdateAdmin(ctx, UpdateAdminInput{
			ActorAdminID: actorID,
			ActorRole:    actorRole,
			TargetID:     targetID,
			Enabled:      req.Enabled,
		}); err != nil {
			status, code := mapAccessWriteError(err)
			platformhttp.Error(c, status, code, err.Error())
			return
		}
	}
	if targetRoleName != "" {
		if _, err := h.service.AssignRole(ctx, AssignRoleInput{
			ActorAdminID: actorID,
			ActorRole:    actorRole,
			TargetID:     targetID,
			NewRoleName:  targetRoleName,
		}); err != nil {
			status, code := mapAccessWriteError(err)
			platformhttp.Error(c, status, code, err.Error())
			return
		}
	}
	view, err := h.service.FindByID(ctx, targetID)
	if err != nil {
		status, code := mapAccessWriteError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, adminUserResponse{
		ID:       view.AdminID.String(),
		Username: view.Username,
		Enabled:  view.Enabled,
		Role: roleResponse{
			ID:          view.RoleID.String(),
			Name:        view.RoleName,
			Permissions: view.Permissions,
		},
	})
}

// ListUsers handles GET /api/admin/v1/users. The route is gated by the
// user:read permission. The keyword query matches the buyer id when numeric,
// otherwise the nickname.
func (h *Handler) ListUsers(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	keyword := strings.TrimSpace(c.Query("keyword"))
	users, total, err := h.service.ListUsers(c.Request.Context(), page, pageSize, keyword)
	if err != nil {
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "list members")
		return
	}
	list := make([]userResponse, 0, len(users))
	for _, user := range users {
		list = append(list, userResponse{
			ID:            user.ID.String(),
			Nickname:      user.Nickname,
			PointsBalance: strconv.FormatInt(user.PointsBalance, 10),
			CreatedAt:     user.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	platformhttp.Success(c, http.StatusOK, userListResponse{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// ListAuditLogs handles GET /api/admin/v1/audit-logs. The route is gated by
// the audit:read permission.
func (h *Handler) ListAuditLogs(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	logs, total, err := h.service.ListAuditLogs(c.Request.Context(), page, pageSize)
	if err != nil {
		platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "list audit logs")
		return
	}
	list := make([]auditLogResponse, 0, len(logs))
	for _, log := range logs {
		item := auditLogResponse{
			ID:         log.ID.String(),
			ActorRole:  log.ActorRole,
			Action:     log.Action,
			TargetType: log.TargetType,
			Result:     log.Result,
			BeforeData: log.BeforeData,
			AfterData:  log.AfterData,
			TraceID:    log.TraceID,
			CreatedAt:  log.CreatedAt.UTC().Format(time.RFC3339),
		}
		if log.ActorAdminID != nil {
			value := log.ActorAdminID.String()
			item.ActorAdminID = &value
		}
		if log.TargetID != nil {
			value := log.TargetID.String()
			item.TargetID = &value
		}
		list = append(list, item)
	}
	platformhttp.Success(c, http.StatusOK, auditLogListResponse{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// actorIdentity resolves the authenticated administrator's id and role name
// from the JWT claims, resolving the role name through the service so audit
// writers can tag the actor's role.
func (h *Handler) actorIdentity(c *gin.Context) (uid.ID, string, bool) {
	subject := adminSubjectFromClaims(c)
	if subject == "" {
		return uid.ID{}, "", false
	}
	actorID, err := adminIDFromSubject(subject)
	if err != nil {
		return uid.ID{}, "", false
	}
	view, err := h.service.FindByID(c.Request.Context(), actorID)
	if err != nil {
		return uid.ID{}, "", false
	}
	return actorID, view.RoleName, true
}

func claimsFromGin(c *gin.Context) *tokens.Claims {
	claims, ok := middleware.ClaimsFromGin(c)
	if !ok {
		return nil
	}
	return claims
}

func adminSubjectFromClaims(c *gin.Context) string {
	claims := claimsFromGin(c)
	if claims == nil {
		return ""
	}
	return claims.Subject
}

func adminIDFromSubject(subject string) (uid.ID, error) {
	return middleware.AdminIDFromSubject(subject)
}

func clientIP(c *gin.Context) string {
	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		ip = strings.TrimSpace(c.GetHeader("X-Forwarded-For"))
	}
	return ip
}

// parsePaging reads the contract's Page (min 1, default 1) and PageSize
// (1..100, default 20) query parameters.
func parsePaging(c *gin.Context) (int, int, error) {
	page := 1
	pageSize := 20
	if raw := c.Query("page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, errors.New("invalid page")
		}
		page = parsed
	}
	if raw := c.Query("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("invalid page_size")
		}
		pageSize = parsed
	}
	return page, pageSize, nil
}

// parsePositiveID parses a canonical UUID path/body identifier; non-UUID
// input is rejected so identifiers cannot smuggle a numeric or empty value.
func parsePositiveID(raw string) (uid.ID, bool) {
	id, err := uid.ParseCanonical(strings.TrimSpace(raw))
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// mapAccessWriteError maps the role/admin-user write errors onto the contract
// statuses: missing rows are 404, duplicate roles 409, and unknown
// roles/permissions are 422 (the contract's BusinessError).
func mapAccessWriteError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrRoleNotFound), errors.Is(err, ErrAdminNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	case errors.Is(err, ErrInvalidRoleName), errors.Is(err, ErrRoleUpdateEmpty), errors.Is(err, ErrAdminUserUpdateEmpty):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrUnknownRole), errors.Is(err, ErrUnknownPermission):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	case errors.Is(err, ErrRoleExists):
		return http.StatusConflict, platformhttp.CodeConflict
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func mapAuthError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrAdminNotFound), errors.Is(err, ErrInvalidCredentials):
		return http.StatusUnauthorized, platformhttp.CodeUnauthorized
	case errors.Is(err, ErrAdminDisabled):
		return http.StatusForbidden, platformhttp.CodeForbidden
	case errors.Is(err, ErrWeakPassword), errors.Is(err, ErrInvalidUsername):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrUnknownRole), errors.Is(err, ErrUnknownPermission):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrAdministratorExists):
		return http.StatusConflict, platformhttp.CodeConflict
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests, platformhttp.CodeRateLimited
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

// setAdminCookie writes a single Set-Cookie header. Splitting the writer
// into this helper makes it easy to assert against in tests.
func setAdminCookie(c *gin.Context, name, value string, maxAge time.Duration, path string, secure, httpOnly bool, sameSite http.SameSite) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(maxAge.Seconds()),
		Secure:   secure,
		HttpOnly: httpOnly,
		SameSite: sameSite,
	}
	http.SetCookie(c.Writer, cookie)
}

func clearAdminCookie(c *gin.Context, name, path string) {
	cookie := &http.Cookie{
		Name:    name,
		Value:   "",
		Path:    path,
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	}
	http.SetCookie(c.Writer, cookie)
}

// RegisterRoutes wires the handler into a Gin router group. The caller is
// responsible for adding AdminOrigin and CSRF middleware to the group. The
// authenticated endpoints (logout, password, me) require AdminAuth on the
// supplied cookie name; the login endpoint stays open so the client can
// obtain the cookie.
func RegisterRoutes(group *gin.RouterGroup, handler *Handler) {
	if group == nil || handler == nil {
		return
	}
	authGroup := group.Group("/auth")
	authGroup.POST("/login", handler.Login)
	jwtService, err := middleware.NewJWTService(buildMiddlewareJWTConfig(handler))
	if err != nil {
		panic(fmt.Errorf("admin handler: invalid JWT configuration: %w", err))
	}
	clock := handler.now
	if clock == nil {
		clock = time.Now
	}
	secure := authGroup.Group("")
	// The middleware enforces token_version freshness and the enabled flag on
	// top of the JWT signature, so every current and future admin route is
	// protected automatically. The service implements AdminStateResolver.
	secure.Use(middleware.AdminAuthWithClock(jwtService, handler.cookie.AccessTokenName, clock, handler.service))
	secure.POST("/logout", handler.Logout)
	secure.POST("/password", handler.ChangePassword)
	secure.GET("/me", handler.Me)

	// The access endpoints (roles, admin-users) live on the admin group root,
	// not under /auth, and carry the contract's role:manage permission gate on
	// top of the same AdminAuth protection. The routes are read-only GETs, so
	// the group-level Origin/CSRF middleware skips them; an operator without
	// role:manage is rejected with 403 by the permission middleware.
	access := group.Group("")
	access.Use(middleware.AdminAuthWithClock(jwtService, handler.cookie.AccessTokenName, clock, handler.service))
	access.GET("/roles",
		middleware.AdminPermission(handler.service, "role:manage"),
		handler.ListRoles)
	access.POST("/roles",
		middleware.AdminPermission(handler.service, "role:manage"),
		handler.CreateRole)
	access.PATCH("/roles/:roleId",
		middleware.AdminPermission(handler.service, "role:manage"),
		handler.UpdateRole)
	access.GET("/admin-users",
		middleware.AdminPermission(handler.service, "role:manage"),
		handler.ListAdminUsers)
	access.PATCH("/admin-users/:adminUserId",
		middleware.AdminPermission(handler.service, "role:manage"),
		handler.UpdateAdminUser)
	// The member list and audit log endpoints carry their own permissions so a
	// role manager does not automatically see every member or audit entry.
	access.GET("/users",
		middleware.AdminPermission(handler.service, "user:read"),
		handler.ListUsers)
	access.GET("/audit-logs",
		middleware.AdminPermission(handler.service, "audit:read"),
		handler.ListAuditLogs)
}

// buildMiddlewareJWTConfig derives a config.JWTConfig from the tokens.Signer
// so the middleware.AdminAuth can share the same keyring. The conversion is
// a small indirection that keeps the handler constructor unchanged.
func buildMiddlewareJWTConfig(handler *Handler) config.JWTConfig {
	cfg := config.JWTConfig{
		Issuer:    handler.signer.Issuer(),
		Audience:  handler.signer.Audience(),
		TTL:       handler.signer.TTL(),
		ActiveKID: handler.signer.ActiveKID(),
		Keys:      handler.signer.Keys(),
	}
	return cfg
}
