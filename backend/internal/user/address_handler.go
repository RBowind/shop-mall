package user

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/middleware"
	platformhttp "shop-mall/backend/internal/platform/http"

	"github.com/gin-gonic/gin"
)

// addressIDPattern is the contract's canonical-UUID path parameter format.
var addressIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// AddressHandler hosts the authenticated buyer address endpoints under
// /api/v1/addresses. The authenticated userID always comes from the bearer
// JWT middleware context; no request body carries an owner id.
type AddressHandler struct {
	service *AddressService
	logger  *slog.Logger
}

// AddressHandlerDeps is the dependency bundle for NewAddressHandler.
type AddressHandlerDeps struct {
	Service *AddressService
	Logger  *slog.Logger
}

// NewAddressHandler validates the dependency bundle and returns the handler.
func NewAddressHandler(deps AddressHandlerDeps) (*AddressHandler, error) {
	if deps.Service == nil {
		return nil, errors.New("address handler: service is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AddressHandler{service: deps.Service, logger: logger}, nil
}

// ListAddresses handles GET /api/v1/addresses.
func (h *AddressHandler) ListAddresses(c *gin.Context) {
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	views, err := h.service.List(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, err)
		return
	}
	platformhttp.Success(c, http.StatusOK, addressListData{List: mapAddressJSON(views)})
}

// CreateAddress handles POST /api/v1/addresses.
func (h *AddressHandler) CreateAddress(c *gin.Context) {
	var req addressCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid address payload")
		return
	}
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	view, err := h.service.Create(c.Request.Context(), userID, AddressInput(req))
	if err != nil {
		status, code := mapCreateAddressError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusCreated, toAddressJSON(view))
}

// UpdateAddress handles PATCH /api/v1/addresses/{addressId}.
func (h *AddressHandler) UpdateAddress(c *gin.Context) {
	id, ok := parsePositiveAddressID(c.Param("addressId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid address id")
		return
	}
	var req addressUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid address payload")
		return
	}
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	view, err := h.service.Update(c.Request.Context(), userID, id, AddressUpdate(req))
	if err != nil {
		status, code := mapUpdateAddressError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, toAddressJSON(view))
}

// DeleteAddress handles DELETE /api/v1/addresses/{addressId}.
func (h *AddressHandler) DeleteAddress(c *gin.Context) {
	id, ok := parsePositiveAddressID(c.Param("addressId"))
	if !ok {
		platformhttp.Error(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "invalid address id")
		return
	}
	userID, ok := buyerIDFromGin(c)
	if !ok {
		platformhttp.Error(c, http.StatusUnauthorized, platformhttp.CodeUnauthorized, "missing buyer identity")
		return
	}
	if err := h.service.Delete(c.Request.Context(), userID, id); err != nil {
		status, code := mapDeleteAddressError(err)
		platformhttp.Error(c, status, code, err.Error())
		return
	}
	platformhttp.Success(c, http.StatusOK, nil)
}

func (h *AddressHandler) internalError(c *gin.Context, err error) {
	h.logger.ErrorContext(c.Request.Context(), "address handler", "error", err, "trace_id", middleware.TraceIDFromGin(c))
	platformhttp.Error(c, http.StatusInternalServerError, platformhttp.CodeInternal, "internal server error")
}

// buyerIDFromGin derives the authenticated buyer id from the bearer JWT
// middleware context. It never trusts a client-supplied owner id.
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

// The create endpoint lists 422 in the contract; the patch endpoint does not,
// so its business errors map to 400.
func mapCreateAddressError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrInvalidAddress), errors.Is(err, ErrDefaultAddressConflict):
		return http.StatusUnprocessableEntity, platformhttp.CodeBusiness
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func mapUpdateAddressError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrAddressNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	case errors.Is(err, ErrInvalidAddress), errors.Is(err, ErrEmptyUpdate), errors.Is(err, ErrDefaultAddressConflict):
		return http.StatusBadRequest, platformhttp.CodeBadRequest
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func mapDeleteAddressError(err error) (int, int) {
	switch {
	case errors.Is(err, ErrAddressNotFound):
		return http.StatusNotFound, platformhttp.CodeNotFound
	default:
		return http.StatusInternalServerError, platformhttp.CodeInternal
	}
}

func parsePositiveAddressID(raw string) (uid.ID, bool) {
	if !addressIDPattern.MatchString(raw) {
		return uid.ID{}, false
	}
	id, err := uid.ParseCanonical(raw)
	if err != nil {
		return uid.ID{}, false
	}
	return id, true
}

// addressCreateRequest is the contract's create payload. Receiver, phone,
// region and detail are required; is_default defaults to false.
type addressCreateRequest struct {
	Receiver  string `json:"receiver"`
	Phone     string `json:"phone"`
	Region    string `json:"region"`
	Detail    string `json:"detail"`
	IsDefault bool   `json:"is_default"`
}

// addressUpdateRequest is the contract's partial-update payload.
type addressUpdateRequest struct {
	Receiver  *string `json:"receiver"`
	Phone     *string `json:"phone"`
	Region    *string `json:"region"`
	Detail    *string `json:"detail"`
	IsDefault *bool   `json:"is_default"`
}

// addressJSON is the contract's Address projection: public int64 fields are
// serialized as decimal strings.
type addressJSON struct {
	ID        string `json:"id"`
	Receiver  string `json:"receiver"`
	Phone     string `json:"phone"`
	Region    string `json:"region"`
	Detail    string `json:"detail"`
	IsDefault bool   `json:"is_default"`
	Version   string `json:"version"`
}

type addressListData struct {
	List []addressJSON `json:"list"`
}

func toAddressJSON(view AddressView) addressJSON {
	return addressJSON{
		ID:        view.ID.String(),
		Receiver:  view.Receiver,
		Phone:     view.Phone,
		Region:    view.Region,
		Detail:    view.Detail,
		IsDefault: view.IsDefault,
		Version:   strconv.FormatInt(view.Version, 10),
	}
}

func mapAddressJSON(views []AddressView) []addressJSON {
	out := make([]addressJSON, 0, len(views))
	for _, view := range views {
		out = append(out, toAddressJSON(view))
	}
	return out
}
