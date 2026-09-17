package coupon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/uid"

	"github.com/gin-gonic/gin"
)

// publicIDPattern is the contract's canonical-UUID path parameter format.
var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// AdminViewer resolves the actor's current role for audit rows. The admin
// service satisfies it structurally.
type AdminViewer interface {
	FindByID(ctx context.Context, adminID uid.ID) (admin.AdminView, error)
}

// Handler hosts the administrator coupon template endpoints.
type Handler struct {
	service     *Service
	adminViewer AdminViewer
	logger      *slog.Logger
}

// HandlerDeps is the dependency bundle for NewHandler.
type HandlerDeps struct {
	Service     *Service
	AdminViewer AdminViewer
	Logger      *slog.Logger
}

// NewHandler validates the dependency bundle and returns a Handler.
func NewHandler(deps HandlerDeps) (*Handler, error) {
	if deps.Service == nil {
		return nil, errors.New("coupon handler: service is required")
	}
	if deps.AdminViewer == nil {
		return nil, errors.New("coupon handler: admin viewer is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: deps.Service, adminViewer: deps.AdminViewer, logger: logger}, nil
}

// AdminCreateTemplate handles POST /api/admin/v1/coupon-templates. Requires
// coupon:write. The created template is returned in its issuing state.
func (h *Handler) AdminCreateTemplate(c *gin.Context) {
	var req createTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid coupon template payload")
		return
	}
	input, err := req.toInput()
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid coupon template payload")
		return
	}
	actor, ok := h.resolveActor(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	template, err := h.service.CreateTemplate(c.Request.Context(), actor, input)
	if err != nil {
		// A rejected rule set is a client-input problem: the spec reports it as
		// 1001 参数错误, not as an internal failure (specs/coupon/spec.md 规则非法).
		if errors.Is(err, ErrInvalidTemplate) {
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusCreated, toTemplateJSON(template))
}

// AdminUpdateTemplateStatus handles
// PATCH /api/admin/v1/coupon-templates/{templateId}. Requires coupon:write. The
// payload carries the issuance status and nothing else: the rule set is frozen
// once the template exists (specs/coupon/spec.md 规则创建后不可修改), so rule
// fields submitted alongside the status are dropped by the decoder rather than
// rejected — a modification request is accepted for its status change, and the
// rules simply do not take effect.
func (h *Handler) AdminUpdateTemplateStatus(c *gin.Context) {
	id, ok := parseTemplateID(c.Param("templateId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid coupon template id")
		return
	}
	var req templateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid coupon template payload")
		return
	}
	status, ok := parseIssuanceStatus(req.Status)
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid coupon template status")
		return
	}
	template, err := h.service.SetTemplateStatus(c.Request.Context(), id, status)
	if err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "coupon template not found")
			return
		}
		// An unknown status is a client-input problem, reported the same way
		// the create path reports a rejected rule set (1001 参数错误).
		if errors.Is(err, ErrInvalidTemplate) {
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toTemplateJSON(template))
}

// templateStatusRequest is the status-switch payload. This struct is the whole
// whitelist: only status is declared, so any other field a client sends — the
// rule fields above all — is not decoded and never reaches the service
// (specs/coupon/spec.md 规则创建后不可修改).
type templateStatusRequest struct {
	Status string `json:"status"`
}

// parseIssuanceStatus accepts exactly the two issuance statuses the template
// column allows (0006_coupon_schema.up.sql coupon_templates_status_valid).
func parseIssuanceStatus(raw string) (Status, bool) {
	status := Status(strings.TrimSpace(raw))
	if !isIssuanceStatus(status) {
		return "", false
	}
	return status, true
}

// parseTemplateID reads the canonical-UUID path parameter. The format check
// mirrors the other admin PATCH routes (product, order): a non-canonical id is
// a client error, not a 404 probe surface.
func parseTemplateID(raw string) (uid.ID, bool) {
	if !publicIDPattern.MatchString(raw) {
		return uid.ID{}, false
	}
	id, err := uid.ParseCanonical(raw)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// resolveActor derives the actor from the verified admin JWT claims and
// resolves the current role for the audit row. A missing or unparseable
// subject is treated as unauthenticated.
func (h *Handler) resolveActor(c *gin.Context) (Actor, bool) {
	claims, ok := middleware.ClaimsFromGin(c)
	if !ok || claims == nil {
		return Actor{}, false
	}
	adminID, err := middleware.AdminIDFromSubject(claims.Subject)
	if err != nil {
		return Actor{}, false
	}
	roleName := ""
	if view, err := h.adminViewer.FindByID(c.Request.Context(), adminID); err == nil {
		roleName = view.RoleName
	}
	return Actor{AdminID: adminID, RoleName: roleName}, true
}

func (h *Handler) internalError(c *gin.Context, err error) {
	h.logger.ErrorContext(c.Request.Context(), "coupon handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "internal server error")
}

// createTemplateRequest is the create payload: the template rule set plus the
// validity window. Times are RFC 3339 timestamps.
type createTemplateRequest struct {
	Name            string `json:"name"`
	ThresholdPoints int64  `json:"threshold_points"`
	DiscountPoints  int64  `json:"discount_points"`
	TotalCount      int32  `json:"total_count"`
	PerUserLimit    int32  `json:"per_user_limit"`
	ValidFrom       string `json:"valid_from"`
	ValidUntil      string `json:"valid_until"`
}

func (r createTemplateRequest) toInput() (TemplateInput, error) {
	validFrom, err := parseTimestamp(r.ValidFrom)
	if err != nil {
		return TemplateInput{}, err
	}
	validUntil, err := parseTimestamp(r.ValidUntil)
	if err != nil {
		return TemplateInput{}, err
	}
	return TemplateInput{
		Name:            r.Name,
		ThresholdPoints: r.ThresholdPoints,
		DiscountPoints:  r.DiscountPoints,
		TotalCount:      r.TotalCount,
		PerUserLimit:    r.PerUserLimit,
		ValidFrom:       validFrom,
		ValidUntil:      validUntil,
	}, nil
}

// parseTimestamp accepts the contract's RFC 3339 timestamp text. A value that
// carries no offset is rejected rather than silently read in the server's
// timezone.
func parseTimestamp(raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errors.New("invalid timestamp")
	}
	return parsed, nil
}

// templateJSON is the administrator projection of a coupon template. Coupon
// endpoints are not yet in docs/api/openapi.yaml (contract FU-0a8e7f3b), so
// this endpoint's field-level shape is declared here; points values are
// serialized as numbers to mirror the numeric rule values the create payload
// accepts.
type templateJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ThresholdPoints int64  `json:"threshold_points"`
	DiscountPoints  int64  `json:"discount_points"`
	TotalCount      int32  `json:"total_count"`
	PerUserLimit    int32  `json:"per_user_limit"`
	ReceivedCount   int32  `json:"received_count"`
	ValidFrom       string `json:"valid_from"`
	ValidUntil      string `json:"valid_until"`
	Status          string `json:"status"`
}

func toTemplateJSON(view TemplateView) templateJSON {
	return templateJSON{
		ID:              view.ID.String(),
		Name:            view.Name,
		ThresholdPoints: view.ThresholdPoints,
		DiscountPoints:  view.DiscountPoints,
		TotalCount:      view.TotalCount,
		PerUserLimit:    view.PerUserLimit,
		ReceivedCount:   view.ReceivedCount,
		ValidFrom:       view.ValidFrom.UTC().Format(time.RFC3339),
		ValidUntil:      view.ValidUntil.UTC().Format(time.RFC3339),
		Status:          string(view.Status),
	}
}
