package order

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	apppoints "shop-mall/backend/internal/application/points"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/payment"
	platformhttp "shop-mall/backend/internal/platform/http"

	"github.com/gin-gonic/gin"
)

// publicIDPattern is the contract's canonical-UUID request format.
var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// signedIntPattern is the contract's signed public-int64 request format for
// the points adjustment delta.
var signedIntPattern = regexp.MustCompile(`^-?[1-9][0-9]*$`)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// PointsAdjustment and the points sentinels are the application points
// usecase's vocabulary; the handler consumes them through the pointsAdjuster
// interface below and maps them to status codes.
type PointsAdjustment = apppoints.PointsAdjustment

var (
	ErrPointsInvalidIdempotency = apppoints.ErrInvalidIdempotencyKey
	ErrPointsInvalidAdjustment  = apppoints.ErrInvalidAdjustment
	ErrPointsUserNotFound       = apppoints.ErrUserNotFound
	ErrPointsInsufficient       = apppoints.ErrInsufficientPoints
	ErrPointsConflict           = apppoints.ErrIdempotencyConflict
)

// The write paths of the order surface are cross-domain usecases (checkout
// spans cart/product/payment/address; refund and fulfillment span the payment
// ledger and the audit log). This handler defines the consumer-side interfaces
// it needs and the usecases are injected as dependencies; the read side stays
// in the domain OrderService.
type (
	orderCreator interface {
		Execute(ctx context.Context, userID uid.ID, idempotencyKey string, req OrderRequest) (Order, error)
	}
	refundGate interface {
		Request(ctx context.Context, userID, orderID uid.ID, reason string) (Order, error)
		Approve(ctx context.Context, adminID, orderID uid.ID) (Order, error)
		Reject(ctx context.Context, adminID, orderID uid.ID, reason string) (Order, error)
	}
	fulfillmentGate interface {
		Ship(ctx context.Context, adminID, orderID uid.ID) (Order, error)
		Confirm(ctx context.Context, userID, orderID uid.ID) (Order, error)
	}
	pointsAdjuster interface {
		Execute(ctx context.Context, adminID uid.ID, idempotencyKey string, req PointsAdjustment) (payment.LedgerEntry, error)
	}
)

// OrderHandler hosts the buyer and administrator order endpoints plus the
// buyer points ledger and the administrator points adjustment.
type OrderHandler struct {
	service       *OrderService
	create        orderCreator
	refund        refundGate
	fulfillment   fulfillmentGate
	points        pointsAdjuster
	publicBaseURL string
	logger        *slog.Logger
}

// OrderHandlerDeps is the dependency bundle for NewOrderHandler. PublicBaseURL
// is the trusted base used to resolve order-item image keys to public URLs.
type OrderHandlerDeps struct {
	Service       *OrderService
	Create        orderCreator
	Refund        refundGate
	Fulfillment   fulfillmentGate
	Points        pointsAdjuster
	PublicBaseURL string
	Logger        *slog.Logger
}

// NewOrderHandler validates the dependency bundle and returns the handler.
func NewOrderHandler(deps OrderHandlerDeps) (*OrderHandler, error) {
	if deps.Service == nil {
		return nil, errors.New("order handler: service is required")
	}
	if deps.Create == nil {
		return nil, errors.New("order handler: create usecase is required")
	}
	if deps.Refund == nil {
		return nil, errors.New("order handler: refund usecase is required")
	}
	if deps.Fulfillment == nil {
		return nil, errors.New("order handler: fulfillment usecase is required")
	}
	if deps.Points == nil {
		return nil, errors.New("order handler: points usecase is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderHandler{
		service:       deps.Service,
		create:        deps.Create,
		refund:        deps.Refund,
		fulfillment:   deps.Fulfillment,
		points:        deps.Points,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(deps.PublicBaseURL), "/"),
		logger:        logger,
	}, nil
}

// CreateOrder handles POST /api/v1/orders. Order creation is idempotent: the
// Idempotency-Key header together with the request hash decide replay vs
// conflict.
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "Idempotency-Key header is required")
		return
	}
	var req orderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order payload")
		return
	}
	parsed, err := parseOrderCreateRequest(req)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	created, err := h.create.Execute(c.Request.Context(), userID, idempotencyKey, parsed)
	if err != nil {
		status, code := mapCreateOrderError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(created, h.publicBaseURL))
}

// ListOrders handles GET /api/v1/orders. Only the current buyer's orders are
// returned.
func (h *OrderHandler) ListOrders(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	status, err := parseOrderStatusQuery(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	orders, total, err := h.service.List(c.Request.Context(), userID, page, pageSize, status)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, orderListData{
		List:     mapOrderJSON(orders, h.publicBaseURL),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// GetOrder handles GET /api/v1/orders/{orderId}. A buyer-owned order is
// returned; any other buyer's order is indistinguishable from a missing one
// (404).
func (h *OrderHandler) GetOrder(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	order, err := h.service.Get(c.Request.Context(), userID, orderID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "order not found")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// ListPointsLedger handles GET /api/v1/points/ledger.
func (h *OrderHandler) ListPointsLedger(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	entries, total, err := h.service.ListLedger(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, ledgerListData{
		List:     mapLedgerJSON(entries),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// AdminListLedger handles GET /api/admin/v1/points/ledger?user_id=. Requires
// points:adjust. It reuses the buy-side ledger query so the operator and the
// admin surface see byte-identical projections of points_ledger.
func (h *OrderHandler) AdminListLedger(c *gin.Context) {
	userID, ok := parsePositiveID(c.Query("user_id"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid user_id")
		return
	}
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	entries, total, err := h.service.ListLedger(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, ledgerListData{
		List:     mapLedgerJSON(entries),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// AdminListOrders handles GET /api/admin/v1/orders. Requires order:read.
func (h *OrderHandler) AdminListOrders(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	status, err := parseOrderStatusQuery(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	orders, total, err := h.service.ListAll(c.Request.Context(), page, pageSize, status)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, orderListData{
		List:     mapOrderJSON(orders, h.publicBaseURL),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// AdminGetOrder handles GET /api/admin/v1/orders/{orderId}. Requires order:read.
func (h *OrderHandler) AdminGetOrder(c *gin.Context) {
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	order, err := h.service.GetAdmin(c.Request.Context(), orderID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			platformhttp.Error(c, http.StatusNotFound, platformhttp.CodeNotFound, "order not found")
			return
		}
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// RequestRefund handles POST /api/v1/orders/{orderId}/refund. A buyer-owned
// order in a refundable state moves to refund_requested; another buyer's order
// is indistinguishable from a missing one (404).
func (h *OrderHandler) RequestRefund(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	var req refundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid refund payload")
		return
	}
	order, err := h.refund.Request(c.Request.Context(), userID, orderID, req.Reason)
	if err != nil {
		status, code := mapRefundError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// ConfirmOrder handles POST /api/v1/orders/{orderId}/confirm. A shipped
// buyer-owned order moves to completed; another buyer's order is
// indistinguishable from a missing one (404).
func (h *OrderHandler) ConfirmOrder(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	order, err := h.fulfillment.Confirm(c.Request.Context(), userID, orderID)
	if err != nil {
		status, code := mapRefundError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// AdminShipOrder handles POST /api/admin/v1/orders/{orderId}/ship. Requires
// order:ship. Only paid orders transition to shipped.
func (h *OrderHandler) AdminShipOrder(c *gin.Context) {
	adminID, ok := adminIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	order, err := h.fulfillment.Ship(c.Request.Context(), adminID, orderID)
	if err != nil {
		status, code := mapRefundError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// AdminApproveRefund handles POST /api/admin/v1/refunds/{orderId}/approve.
// Requires refund:approve. The status transition, points refund, ledger entry
// and stock restoration commit in one transaction.
func (h *OrderHandler) AdminApproveRefund(c *gin.Context) {
	adminID, ok := adminIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	order, err := h.refund.Approve(c.Request.Context(), adminID, orderID)
	if err != nil {
		status, code := mapRefundError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// AdminRejectRefund handles POST /api/admin/v1/refunds/{orderId}/reject.
// Requires refund:approve. Rejection changes only the refund state and the
// review fields; it does not refund points or stock.
func (h *OrderHandler) AdminRejectRefund(c *gin.Context) {
	adminID, ok := adminIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	orderID, ok := parsePositiveID(c.Param("orderId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid order id")
		return
	}
	var req refundRejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid refund payload")
		return
	}
	order, err := h.refund.Reject(c.Request.Context(), adminID, orderID, req.Reason)
	if err != nil {
		status, code := mapRefundError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toOrderJSON(order, h.publicBaseURL))
}

// AdminListRefunds handles GET /api/admin/v1/refunds. Requires refund:read.
func (h *OrderHandler) AdminListRefunds(c *gin.Context) {
	page, pageSize, err := parsePaging(c)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	orders, total, err := h.service.ListRefunds(c.Request.Context(), page, pageSize)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, orderListData{
		List:     mapOrderJSON(orders, h.publicBaseURL),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// AdjustPoints handles POST /api/admin/v1/points/adjust. Requires points:adjust.
func (h *OrderHandler) AdjustPoints(c *gin.Context) {
	adminID, ok := adminIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing administrator identity")
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "Idempotency-Key header is required")
		return
	}
	var req pointsAdjustRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid points payload")
		return
	}
	adjust, err := parsePointsAdjustRequest(req)
	if err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error())
		return
	}
	entry, err := h.points.Execute(c.Request.Context(), adminID, idempotencyKey, adjust)
	if err != nil {
		status, code := mapAdjustPointsError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toLedgerJSON(entry))
}

func (h *OrderHandler) internalError(c *gin.Context, err error) {
	h.logger.ErrorContext(c.Request.Context(), "order handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "internal server error")
}

// buyerIDFromGin derives the authenticated buyer id from the bearer JWT
// middleware context.
func buyerIDFromGin(c *gin.Context) (uid.ID, bool) {
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

// adminIDFromGin derives the authenticated administrator id from the verified
// admin JWT claims.
func adminIDFromGin(c *gin.Context) (uid.ID, bool) {
	claims, ok := middleware.ClaimsFromGin(c)
	if !ok || claims == nil {
		return uid.ID{}, false
	}
	id, err := middleware.AdminIDFromSubject(claims.Subject)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

func mapCreateOrderError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrInvalidIdempotencyKey), errors.Is(err, ErrPointsInvalidIdempotency), errors.Is(err, ErrInvalidOrderRequest):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrIdempotencyConflict), errors.Is(err, ErrPointsConflict):
		return http.StatusConflict, platformhttp.CodeConflict
	case errors.Is(err, ErrAddressNotFound),
		errors.Is(err, ErrCartItemNotFound),
		errors.Is(err, ErrProductNotFound),
		errors.Is(err, ErrProductOffSale),
		errors.Is(err, ErrInsufficientStock),
		errors.Is(err, ErrInsufficientPoints):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func mapAdjustPointsError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrPointsInvalidIdempotency), errors.Is(err, ErrInvalidIdempotencyKey):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrPointsConflict):
		return http.StatusConflict, platformhttp.CodeConflict
	case errors.Is(err, ErrPointsInvalidAdjustment), errors.Is(err, ErrPointsUserNotFound), errors.Is(err, ErrPointsInsufficient):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

// mapRefundError maps the refund and fulfillment transition errors to the
// contract's status codes: a request-format violation (bad reason) is 400, a
// missing or not-owned order is 404, and a state-transition conflict is 409.
func mapRefundError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrInvalidRefundReason):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	case errors.Is(err, ErrOrderNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	case errors.Is(err, ErrOrderStateConflict):
		return http.StatusConflict, platformhttp.CodeConflict
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func parseOrderCreateRequest(req orderCreateRequest) (OrderRequest, error) {
	if req.AddressID == "" || !publicIDPattern.MatchString(req.AddressID) {
		return OrderRequest{}, errors.New("invalid address id")
	}
	addressID, err := uid.ParseCanonical(req.AddressID)
	if err != nil {
		return OrderRequest{}, errors.New("invalid address id")
	}
	if len(req.CartItemIDs) == 0 {
		return OrderRequest{}, errors.New("cart_item_ids is required")
	}
	seen := make(map[uid.ID]struct{}, len(req.CartItemIDs))
	cartIDs := make([]uid.ID, 0, len(req.CartItemIDs))
	for _, raw := range req.CartItemIDs {
		if !publicIDPattern.MatchString(raw) {
			return OrderRequest{}, errors.New("invalid cart item id")
		}
		id, err := uid.ParseCanonical(raw)
		if err != nil {
			return OrderRequest{}, errors.New("invalid cart item id")
		}
		if _, ok := seen[id]; ok {
			return OrderRequest{}, errors.New("cart_item_ids must be unique")
		}
		seen[id] = struct{}{}
		cartIDs = append(cartIDs, id)
	}
	return OrderRequest{CartItemIDs: cartIDs, AddressID: addressID}, nil
}

func parsePointsAdjustRequest(req pointsAdjustRequest) (PointsAdjustment, error) {
	if !publicIDPattern.MatchString(req.UserID) {
		return PointsAdjustment{}, errors.New("invalid user id")
	}
	userID, err := uid.ParseCanonical(req.UserID)
	if err != nil {
		return PointsAdjustment{}, errors.New("invalid user id")
	}
	if !signedIntPattern.MatchString(req.Delta) {
		return PointsAdjustment{}, errors.New("invalid delta")
	}
	delta, err := strconv.ParseInt(req.Delta, 10, 64)
	if err != nil {
		return PointsAdjustment{}, errors.New("invalid delta")
	}
	if strings.TrimSpace(req.Remark) == "" {
		return PointsAdjustment{}, errors.New("remark is required")
	}
	return PointsAdjustment{UserID: userID, Delta: delta, Remark: req.Remark}, nil
}

func parseOrderStatusQuery(c *gin.Context) (*Status, error) {
	raw := c.Query("status")
	if raw == "" {
		return nil, nil
	}
	status := Status(raw)
	switch status {
	case StatusPaid,
		StatusShipped,
		StatusCompleted,
		StatusRefundRequested,
		StatusRefunded:
		return &status, nil
	default:
		return nil, errors.New("invalid status filter")
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

// orderCreateRequest is the contract's create payload. The address_id and
// cart_item_ids are canonical uuid strings, validated before the service
// layer.
type orderCreateRequest struct {
	CartItemIDs []string `json:"cart_item_ids"`
	AddressID   string   `json:"address_id"`
}

// pointsAdjustRequest is the contract's admin points payload.
type pointsAdjustRequest struct {
	UserID string `json:"user_id"`
	Delta  string `json:"delta"`
	Remark string `json:"remark"`
}

// refundRequest is the contract's buyer refund request payload.
type refundRequest struct {
	Reason string `json:"reason"`
}

// refundRejectRequest is the contract's admin refund rejection payload.
type refundRejectRequest struct {
	Reason string `json:"reason"`
}

// orderItemJSON is the contract's OrderItem projection.
type orderItemJSON struct {
	ProductID     string `json:"product_id"`
	ProductName   string `json:"product_name"`
	ProductImage  string `json:"product_image"`
	PriceSnapshot string `json:"price_snapshot"`
	Quantity      int32  `json:"quantity"`
}

// orderJSON is the contract's Order projection. Public int64 values are
// decimal strings; nullable timestamps are null when absent.
type orderJSON struct {
	ID                 string          `json:"id"`
	OrderNo            string          `json:"order_no"`
	Status             string          `json:"status"`
	TotalPoints        string          `json:"total_points"`
	Receiver           string          `json:"receiver"`
	Phone              string          `json:"phone"`
	Address            string          `json:"address"`
	Items              []orderItemJSON `json:"items"`
	PaidAt             string          `json:"paid_at"`
	ShippedAt          *string         `json:"shipped_at"`
	CompletedAt        *string         `json:"completed_at"`
	RefundedAt         *string         `json:"refunded_at"`
	RefundReason       string          `json:"refund_reason"`
	RefundRejectReason string          `json:"refund_reject_reason"`
	CreatedAt          string          `json:"created_at"`
}

type orderListData struct {
	List     []orderJSON `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// ledgerEntryJSON is the contract's LedgerEntry projection.
type ledgerEntryJSON struct {
	ID           string  `json:"id"`
	OrderID      *string `json:"order_id"`
	Type         string  `json:"type"`
	Delta        string  `json:"delta"`
	BalanceAfter string  `json:"balance_after"`
	Remark       string  `json:"remark"`
	CreatedAt    string  `json:"created_at"`
}

type ledgerListData struct {
	List     []ledgerEntryJSON `json:"list"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

func toOrderJSON(order Order, publicBaseURL string) orderJSON {
	items := make([]orderItemJSON, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, orderItemJSON{
			ProductID:     item.ProductID.String(),
			ProductName:   item.ProductName,
			ProductImage:  productImageURL(publicBaseURL, item.ProductImage),
			PriceSnapshot: strconv.FormatInt(item.PriceSnapshot, 10),
			Quantity:      item.Quantity,
		})
	}
	return orderJSON{
		ID:                 order.ID.String(),
		OrderNo:            order.OrderNo,
		Status:             string(order.Status),
		TotalPoints:        strconv.FormatInt(order.TotalPoints, 10),
		Receiver:           order.Receiver,
		Phone:              order.Phone,
		Address:            order.Address,
		Items:              items,
		PaidAt:             formatRFC3339(order.PaidAt),
		ShippedAt:          formatTimePtr(order.ShippedAt),
		CompletedAt:        formatTimePtr(order.CompletedAt),
		RefundedAt:         formatTimePtr(order.RefundedAt),
		RefundReason:       order.RefundReason,
		RefundRejectReason: order.RefundRejectReason,
		CreatedAt:          formatRFC3339(order.CreatedAt),
	}
}

func mapOrderJSON(orders []Order, publicBaseURL string) []orderJSON {
	out := make([]orderJSON, 0, len(orders))
	for _, order := range orders {
		out = append(out, toOrderJSON(order, publicBaseURL))
	}
	return out
}

func toLedgerJSON(entry payment.LedgerEntry) ledgerEntryJSON {
	var orderID *string
	if entry.OrderID != nil {
		value := entry.OrderID.String()
		orderID = &value
	}
	return ledgerEntryJSON{
		ID:           entry.ID.String(),
		OrderID:      orderID,
		Type:         string(entry.Type),
		Delta:        strconv.FormatInt(entry.Delta, 10),
		BalanceAfter: strconv.FormatInt(entry.BalanceAfter, 10),
		Remark:       entry.Remark,
		CreatedAt:    formatRFC3339(entry.CreatedAt),
	}
}

func mapLedgerJSON(entries []payment.LedgerEntry) []ledgerEntryJSON {
	out := make([]ledgerEntryJSON, 0, len(entries))
	for _, entry := range entries {
		out = append(out, toLedgerJSON(entry))
	}
	return out
}

func productImageURL(publicBaseURL, key string) string {
	if publicBaseURL == "" || key == "" {
		return key
	}
	return publicBaseURL + "/static/images/" + key
}

func formatRFC3339(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}
