package cart

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/product"

	"github.com/gin-gonic/gin"
)

// publicIDPattern is the contract's canonical-UUID path parameter format.
var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// CartHandler hosts the authenticated buyer cart endpoints under /api/v1/cart.
type CartHandler struct {
	service *CartService
	logger  *slog.Logger
}

// CartHandlerDeps is the dependency bundle for NewCartHandler.
type CartHandlerDeps struct {
	Service *CartService
	Logger  *slog.Logger
}

// NewCartHandler validates the dependency bundle and returns the handler.
func NewCartHandler(deps CartHandlerDeps) (*CartHandler, error) {
	if deps.Service == nil {
		return nil, errors.New("cart handler: service is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CartHandler{service: deps.Service, logger: logger}, nil
}

// GetCart handles GET /api/v1/cart. Returns only the current user's items.
func (h *CartHandler) GetCart(c *gin.Context) {
	userID, ok := cartBuyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	items, err := h.service.List(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, cartListData{List: mapCartItemJSON(items)})
}

// AddCartItem handles POST /api/v1/cart. The response is 200 per the contract.
func (h *CartHandler) AddCartItem(c *gin.Context) {
	var req cartItemCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid cart payload")
		return
	}
	productID, ok := parsePositiveCartID(req.ProductID)
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid product id")
		return
	}
	if req.Quantity == nil || *req.Quantity < 1 {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "quantity must be at least 1")
		return
	}
	userID, ok := cartBuyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	item, err := h.service.Add(c.Request.Context(), userID, productID, *req.Quantity)
	if err != nil {
		status, code := mapAddCartError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toCartItemJSON(item))
}

// UpdateCartItem handles PATCH /api/v1/cart/{itemId}.
func (h *CartHandler) UpdateCartItem(c *gin.Context) {
	itemID, ok := parsePositiveCartID(c.Param("itemId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid cart item id")
		return
	}
	var req cartItemUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid cart payload")
		return
	}
	if req.Quantity == nil || *req.Quantity < 1 {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "quantity must be at least 1")
		return
	}
	userID, ok := cartBuyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	item, err := h.service.Update(c.Request.Context(), userID, itemID, *req.Quantity)
	if err != nil {
		status, code := mapCartItemError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toCartItemJSON(item))
}

// DeleteCartItem handles DELETE /api/v1/cart/{itemId}.
func (h *CartHandler) DeleteCartItem(c *gin.Context) {
	itemID, ok := parsePositiveCartID(c.Param("itemId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid cart item id")
		return
	}
	userID, ok := cartBuyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	if err := h.service.Delete(c.Request.Context(), userID, itemID); err != nil {
		status, code := mapCartItemError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, nil)
}

func (h *CartHandler) internalError(c *gin.Context, err error) {
	h.logger.ErrorContext(c.Request.Context(), "cart handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "internal server error")
}

// cartBuyerIDFromGin derives the authenticated buyer id from the bearer JWT
// middleware context.
func cartBuyerIDFromGin(c *gin.Context) (uid.ID, bool) {
	subject := c.GetString(middleware.BuyerSubjectKey)
	if subject == "" {
		return uid.ID{}, false
	}
	id, err := middleware.BuyerIDFromSubject(subject)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// The add endpoint lists 422 for business errors; the patch endpoint lists
// 400, so it maps item errors separately.
func mapAddCartError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrCartProductNotFound), errors.Is(err, ErrQuantityOverflow):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func mapCartItemError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrCartItemNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	case errors.Is(err, ErrInvalidCartItem):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func parsePositiveCartID(raw string) (uid.ID, bool) {
	if !publicIDPattern.MatchString(raw) {
		return uid.ID{}, false
	}
	id, err := uid.ParseCanonical(raw)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// cartItemCreateRequest is the contract's add payload. product_id is a public
// int64 serialized as a string; quantity is a number with a minimum of 1.
type cartItemCreateRequest struct {
	ProductID string `json:"product_id"`
	Quantity  *int32 `json:"quantity"`
}

// cartItemUpdateRequest is the contract's update payload.
type cartItemUpdateRequest struct {
	Quantity *int32 `json:"quantity"`
}

// cartItemJSON is the contract's CartItem projection.
type cartItemJSON struct {
	ID        string          `json:"id"`
	ProductID string          `json:"product_id"`
	Quantity  int32           `json:"quantity"`
	Product   cartProductJSON `json:"product"`
}

// cartProductJSON is the contract's Product projection nested in a cart item.
type cartProductJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MainImage   string `json:"main_image"`
	PricePoints string `json:"price_points"`
	Stock       int32  `json:"stock"`
	Status      string `json:"status"`
}

type cartListData struct {
	List []cartItemJSON `json:"list"`
}

func toCartItemJSON(view CartItemView) cartItemJSON {
	return cartItemJSON{
		ID:        view.ID.String(),
		ProductID: view.ProductID.String(),
		Quantity:  view.Quantity,
		Product:   toCartProductJSON(view.Product),
	}
}

func toCartProductJSON(view product.ProductView) cartProductJSON {
	return cartProductJSON{
		ID:          view.ID.String(),
		Name:        view.Name,
		Description: view.Description,
		MainImage:   view.MainImage,
		PricePoints: strconv.FormatInt(view.PricePoints, 10),
		Stock:       view.Stock,
		Status:      string(view.Status),
	}
}

func mapCartItemJSON(views []CartItemView) []cartItemJSON {
	out := make([]cartItemJSON, 0, len(views))
	for _, view := range views {
		out = append(out, toCartItemJSON(view))
	}
	return out
}
