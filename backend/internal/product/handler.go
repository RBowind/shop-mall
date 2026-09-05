package product

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"

	"github.com/gin-gonic/gin"
)

// publicIDPattern is the contract's canonical-UUID path parameter format.
var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// AdminViewer resolves the actor's current role for audit rows. The admin
// service satisfies it structurally.
type AdminViewer interface {
	FindByID(ctx context.Context, adminID uid.ID) (admin.AdminView, error)
}

// Handler hosts the public and admin product endpoints.
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
		return nil, errors.New("product handler: service is required")
	}
	if deps.AdminViewer == nil {
		return nil, errors.New("product handler: admin viewer is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: deps.Service, adminViewer: deps.AdminViewer, logger: logger}, nil
}

// ListProducts handles GET /api/v1/products. Public on-sale listing ordered
// by id descending, with optional category and keyword (name) filters.
func (h *Handler) ListProducts(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	category := c.Query("category")
	keyword := strings.TrimSpace(c.Query("keyword"))
	products, total, err := h.service.ListPublic(c.Request.Context(), page, pageSize, category, keyword)
	if err != nil {
		if errors.Is(err, ErrInvalidProduct) {
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid category filter")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, productListData{
		List:     mapProductJSON(products),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// ListCategories handles GET /api/v1/categories. The static catalog paired
// with live on-sale product counts.
func (h *Handler) ListCategories(c *gin.Context) {
	catalog, err := h.service.ListCategories(c.Request.Context())
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, catalog)
}

// GetProduct handles GET /api/v1/products/{productId}. An off_sale product is
// indistinguishable from a missing one and returns 404.
func (h *Handler) GetProduct(c *gin.Context) {
	id, ok := parsePositiveID(c.Param("productId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product id")
		return
	}
	product, err := h.service.GetPublic(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "product not found")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toProductJSON(product))
}

// AdminListProducts handles GET /api/admin/v1/products. Requires product:read.
func (h *Handler) AdminListProducts(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	var status *Status
	if raw := c.Query("status"); raw != "" {
		parsed := Status(raw)
		status = &parsed
	}
	products, total, err := h.service.ListAdmin(c.Request.Context(), page, pageSize, status)
	if err != nil {
		if errors.Is(err, ErrInvalidProduct) {
			platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid status filter")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, productListData{
		List:     mapProductJSON(products),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// AdminCreateProduct handles POST /api/admin/v1/products. Requires product:write.
func (h *Handler) AdminCreateProduct(c *gin.Context) {
	var req productWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product payload")
		return
	}
	if req.Stock == nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "stock is required")
		return
	}
	actor, ok := h.resolveActor(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	product, err := h.service.Create(c.Request.Context(), actor, req.toInput())
	if err != nil {
		status, code := mapProductError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusCreated, toProductJSON(product))
}

// AdminGetProduct handles GET /api/admin/v1/products/{productId}. Requires product:read.
func (h *Handler) AdminGetProduct(c *gin.Context) {
	id, ok := parsePositiveID(c.Param("productId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product id")
		return
	}
	product, err := h.service.GetAdmin(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "product not found")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toProductJSON(product))
}

// AdminUpdateProduct handles PATCH /api/admin/v1/products/{productId}. Requires product:write.
func (h *Handler) AdminUpdateProduct(c *gin.Context) {
	id, ok := parsePositiveID(c.Param("productId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product id")
		return
	}
	var req productPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product payload")
		return
	}
	actor, ok := h.resolveActor(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	product, err := h.service.Update(c.Request.Context(), actor, id, req.toUpdate())
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "product not found")
			return
		}
		status, code := mapProductError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toProductJSON(product))
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
	h.logger.ErrorContext(c.Request.Context(), "product handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "internal server error")
}

func mapProductError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrInvalidPrice):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	case errors.Is(err, ErrInvalidProduct):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	case errors.Is(err, ErrEmptyUpdate):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func parsePaging(c *gin.Context) (int, int, error) {
	page := defaultPage
	pageSize := defaultPageSize
	if raw := c.Query("page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, errors.New("invalid page")
		}
		page = parsed
	}
	if raw := c.Query("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			return 0, 0, errors.New("invalid page_size")
		}
		pageSize = parsed
	}
	return page, pageSize, nil
}

func parsePositiveID(raw string) (uid.ID, bool) {
	if !publicIDPattern.MatchString(raw) {
		return uid.ID{}, false
	}
	id, err := uid.ParseCanonical(raw)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// productWriteRequest is the contract's create payload. Stock is required and
// kept as a pointer so a missing field is distinguishable from an explicit
// zero.
type productWriteRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Images      []string `json:"images"`
	PricePoints string   `json:"price_points"`
	Stock       *int32   `json:"stock"`
	Status      *Status  `json:"status"`
}

func (r productWriteRequest) toInput() ProductInput {
	status := Status("")
	if r.Status != nil {
		status = *r.Status
	}
	return ProductInput{
		Name:        r.Name,
		Description: r.Description,
		Category:    r.Category,
		Images:      r.Images,
		PricePoints: r.PricePoints,
		Stock:       stockValue(r.Stock),
		Status:      status,
	}
}

func stockValue(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

// productPatchRequest is the contract's partial-update payload.
type productPatchRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Category    *string   `json:"category"`
	Images      *[]string `json:"images"`
	PricePoints *string   `json:"price_points"`
	Stock       *int32    `json:"stock"`
	Status      *Status   `json:"status"`
}

func (r productPatchRequest) toUpdate() ProductUpdate {
	return ProductUpdate(r)
}

// productJSON is the contract's Product projection: public int64 fields are
// serialized as decimal strings, stock as a number. main_image stays as the
// first gallery URL for older clients.
type productJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	MainImage   string   `json:"main_image"`
	Images      []string `json:"images"`
	PricePoints string   `json:"price_points"`
	Stock       int32    `json:"stock"`
	Status      string   `json:"status"`
}

type productListData struct {
	List     []productJSON `json:"list"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

func toProductJSON(view ProductView) productJSON {
	images := view.Images
	if images == nil {
		images = []string{}
	}
	return productJSON{
		ID:          view.ID.String(),
		Name:        view.Name,
		Description: view.Description,
		Category:    view.Category,
		MainImage:   view.MainImage,
		Images:      images,
		PricePoints: strconv.FormatInt(view.PricePoints, 10),
		Stock:       view.Stock,
		Status:      string(view.Status),
	}
}

func mapProductJSON(views []ProductView) []productJSON {
	out := make([]productJSON, 0, len(views))
	for _, view := range views {
		out = append(out, toProductJSON(view))
	}
	return out
}
