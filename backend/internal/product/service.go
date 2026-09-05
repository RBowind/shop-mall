package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/storage"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Status is the product lifecycle status. The row-level enum lives with the
// table model; this domain alias is the only status vocabulary the handler and
// service layers use, so the HTTP surface never imports the database package.
type Status = database.ProductStatus

const (
	StatusOnSale  = database.ProductStatusOnSale
	StatusOffSale = database.ProductStatusOffSale
)

// Service-level sentinel errors. Handlers map these to the HTTP semantics of
// the OpenAPI contract.
var (
	// ErrInvalidProduct is a business-rule violation (name length, status,
	// stock, invalid image key).
	ErrInvalidProduct = errors.New("invalid product")
	// ErrInvalidPrice is returned when price_points is not a positive
	// decimal integer string as prescribed by the contract.
	ErrInvalidPrice = errors.New("invalid price points")
	// ErrEmptyUpdate is returned when an update request carries no settable
	// field.
	ErrEmptyUpdate = errors.New("empty update")
)

// pricePointsPattern matches the contract's x-public-int64 request format for
// prices: a positive integer without leading zeros, serialized as a string.
var pricePointsPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// Actor is the authorized administrator carrying out a product write. The
// handler resolves it from the verified admin JWT.
type Actor struct {
	AdminID  uid.ID
	RoleName string
}

// Service is the application-layer gateway over the products table. It owns
// the product business rules, the public URL projection, the audit rows, and
// the image-replacement grace period.
type Service struct {
	db            *gorm.DB
	repo          *Repository
	logger        *slog.Logger
	publicBaseURL string
	now           func() time.Time
	toucher       storage.Toucher
}

// ServiceDeps is the dependency bundle for NewService.
type ServiceDeps struct {
	DB            *gorm.DB
	Repository    *Repository
	Logger        *slog.Logger
	PublicBaseURL string
	Now           func() time.Time
	Toucher       storage.Toucher
}

// NewService validates the dependency bundle and returns a Service.
func NewService(deps ServiceDeps) (*Service, error) {
	if deps.DB == nil {
		return nil, errors.New("product service: database is required")
	}
	if deps.Repository == nil {
		return nil, errors.New("product service: repository is required")
	}
	if strings.TrimSpace(deps.PublicBaseURL) == "" {
		return nil, errors.New("product service: public base URL is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		db:            deps.DB,
		repo:          deps.Repository,
		logger:        logger,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(deps.PublicBaseURL), "/"),
		now:           now,
		toucher:       deps.Toucher,
	}, nil
}

// ProductView is the read projection of a product with the public image URLs
// resolved from the trusted PUBLIC_BASE_URL.
type ProductView struct {
	ID          uid.ID
	Name        string
	Description string
	Category    string
	// Images is the ordered gallery of resolved URLs; the first entry doubles
	// as MainImage for backward compatibility with the frozen contract field.
	MainImage   string
	Images      []string
	PricePoints int64
	Stock       int32
	Status      Status
}

// PublicURL resolves an object key to the public image URL served by nginx.
func (s *Service) PublicURL(key string) string {
	if key == "" {
		return ""
	}
	return s.publicBaseURL + "/static/images/" + key
}

// View projects a database product row into the public ProductView the
// handlers serialize, resolving main_image through the trusted
// PUBLIC_BASE_URL. The cart service uses it to render products that remain in
// a cart even when they go off sale.
func (s *Service) View(product database.Product) ProductView {
	return toView(product, s.PublicURL)
}

// ListPublic returns the on-sale products ordered by id descending. A
// non-empty category restricts the result; a non-empty keyword filters by
// product name.
func (s *Service) ListPublic(ctx context.Context, page, pageSize int, category, keyword string) ([]ProductView, int64, error) {
	if category != "" && !validCategory(category) {
		return nil, 0, fmt.Errorf("%w: category", ErrInvalidProduct)
	}
	products, total, err := s.repo.ListOnSale(ctx, page, pageSize, category, keyword)
	if err != nil {
		return nil, 0, err
	}
	return mapViews(products, s.PublicURL), total, nil
}

// ListCategories returns the category catalog with live on-sale product
// counts.
func (s *Service) ListCategories(ctx context.Context) ([]CategoryInfo, error) {
	counts, err := s.repo.CountOnSaleByCategory(ctx)
	if err != nil {
		return nil, err
	}
	return CategoryCatalog(counts), nil
}

// GetPublic returns an on-sale product. An off_sale product surfaces as
// ErrProductNotFound, matching the contract's 404 behavior.
func (s *Service) GetPublic(ctx context.Context, id uid.ID) (ProductView, error) {
	product, err := s.repo.GetPublic(ctx, id)
	if err != nil {
		return ProductView{}, err
	}
	return toView(product, s.PublicURL), nil
}

// ListAdmin returns products with an optional status filter.
func (s *Service) ListAdmin(ctx context.Context, page, pageSize int, status *Status) ([]ProductView, int64, error) {
	if status != nil && !validStatus(*status) {
		return nil, 0, fmt.Errorf("%w: status", ErrInvalidProduct)
	}
	products, total, err := s.repo.ListAdmin(ctx, page, pageSize, status)
	if err != nil {
		return nil, 0, err
	}
	return mapViews(products, s.PublicURL), total, nil
}

// GetAdmin returns any product by id regardless of status.
func (s *Service) GetAdmin(ctx context.Context, id uid.ID) (ProductView, error) {
	product, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return ProductView{}, err
	}
	return toView(product, s.PublicURL), nil
}

// ProductInput is the validated create payload.
type ProductInput struct {
	Name        string
	Description string
	Category    string
	Images      []string
	PricePoints string
	Stock       int32
	Status      Status
}

// Create inserts a product and writes a product.create audit row in the same
// transaction.
func (s *Service) Create(ctx context.Context, actor Actor, input ProductInput) (ProductView, error) {
	if err := validateCreateInput(input); err != nil {
		return ProductView{}, err
	}
	price, err := ParsePricePoints(input.PricePoints)
	if err != nil {
		return ProductView{}, err
	}
	imageKeys, err := normalizeImageKeys(input.Images)
	if err != nil {
		return ProductView{}, err
	}
	status := input.Status
	if status == "" {
		status = StatusOnSale
	}
	var created database.Product
	err = database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		product := database.Product{
			Name:        strings.TrimSpace(input.Name),
			Description: input.Description,
			Category:    input.Category,
			Images:      marshalImageKeys(imageKeys),
			PricePoints: price,
			Stock:       input.Stock,
			Status:      status,
		}
		if err := tx.WithContext(tx.Statement.Context).Create(&product).Error; err != nil {
			return err
		}
		created = product
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &actor.AdminID,
			ActorRole:    actor.RoleName,
			Action:       "product.create",
			TargetType:   "product",
			TargetID:     &product.ID,
			Result:       "success",
			AfterData:    productAuditData(&product),
		})
	})
	if err != nil {
		return ProductView{}, err
	}
	return toView(created, s.PublicURL), nil
}

// ProductUpdate is the validated partial-update payload. Nil fields are left
// unchanged.
type ProductUpdate struct {
	Name        *string
	Description *string
	Category    *string
	Images      *[]string
	PricePoints *string
	Stock       *int32
	Status      *Status
}

// Update applies a partial product update. Keys dropped from the gallery are
// touched so the cleanup grace period starts at replacement time; the DB
// update and the audit row commit together.
func (s *Service) Update(ctx context.Context, actor Actor, id uid.ID, input ProductUpdate) (ProductView, error) {
	if uid.IsZero(id) {
		return ProductView{}, ErrProductNotFound
	}
	if err := validateUpdateInput(input); err != nil {
		return ProductView{}, err
	}
	var updated database.Product
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		var current database.Product
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", id).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProductNotFound
			}
			return err
		}
		updates := map[string]any{}
		if input.Name != nil {
			updates["name"] = strings.TrimSpace(*input.Name)
		}
		if input.Description != nil {
			updates["description"] = *input.Description
		}
		if input.Category != nil {
			updates["category"] = *input.Category
		}
		var nextKeys []string
		if input.Images != nil {
			normalized, err := normalizeImageKeys(*input.Images)
			if err != nil {
				return err
			}
			nextKeys = normalized
			updates["images"] = marshalImageKeys(normalized)
		}
		if input.PricePoints != nil {
			price, err := ParsePricePoints(*input.PricePoints)
			if err != nil {
				return err
			}
			updates["price_points"] = price
		}
		if input.Stock != nil {
			updates["stock"] = *input.Stock
		}
		if input.Status != nil {
			updates["status"] = *input.Status
		}
		if len(updates) == 0 {
			return ErrEmptyUpdate
		}
		if err := tx.WithContext(tx.Statement.Context).
			Model(&database.Product{}).
			Where("id = ?", id).
			Updates(updates).Error; err != nil {
			return err
		}
		// Start the cleanup grace period for dropped gallery images so they are
		// not removed before the grace period elapses. A touch failure is
		// logged but does not roll back the product update.
		if input.Images != nil && s.toucher != nil {
			for _, oldKey := range current.ImageKeys() {
				if !slices.Contains(nextKeys, oldKey) {
					if err := s.toucher.Touch(ctx, oldKey); err != nil {
						s.logger.ErrorContext(ctx, "product update: touch replaced image", "error", err)
					}
				}
			}
		}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", id).First(&updated).Error; err != nil {
			return err
		}
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &actor.AdminID,
			ActorRole:    actor.RoleName,
			Action:       "product.update",
			TargetType:   "product",
			TargetID:     &id,
			Result:       "success",
			BeforeData:   productAuditData(&current),
			AfterData:    productAuditData(&updated),
		})
	})
	if err != nil {
		return ProductView{}, err
	}
	return toView(updated, s.PublicURL), nil
}

// IsImageKeyReferenced implements storage.ReferenceResolver: a key is
// referenced when at least one product keeps it in its gallery.
func (s *Service) IsImageKeyReferenced(ctx context.Context, key string) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.Product{}).
		Where("images @> to_jsonb(ARRAY[?])", key).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ParsePricePoints validates the contract's price_points string format and
// returns the integer value.
func ParsePricePoints(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !pricePointsPattern.MatchString(value) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidPrice, value)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidPrice, value)
	}
	return parsed, nil
}

func validateCreateInput(input ProductInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidProduct)
	}
	if len([]rune(strings.TrimSpace(input.Name))) > 128 {
		return fmt.Errorf("%w: name is too long", ErrInvalidProduct)
	}
	if !validCategory(input.Category) {
		return fmt.Errorf("%w: category", ErrInvalidProduct)
	}
	if input.Stock < 0 {
		return fmt.Errorf("%w: stock must not be negative", ErrInvalidProduct)
	}
	if input.Status != "" && !validStatus(input.Status) {
		return fmt.Errorf("%w: status", ErrInvalidProduct)
	}
	if _, err := normalizeImageKeys(input.Images); err != nil {
		return err
	}
	return nil
}

func validateUpdateInput(input ProductUpdate) error {
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return fmt.Errorf("%w: name must not be blank", ErrInvalidProduct)
		}
		if len([]rune(name)) > 128 {
			return fmt.Errorf("%w: name is too long", ErrInvalidProduct)
		}
	}
	if input.Stock != nil && *input.Stock < 0 {
		return fmt.Errorf("%w: stock must not be negative", ErrInvalidProduct)
	}
	if input.Category != nil && !validCategory(*input.Category) {
		return fmt.Errorf("%w: category", ErrInvalidProduct)
	}
	if input.Images != nil {
		if _, err := normalizeImageKeys(*input.Images); err != nil {
			return err
		}
	}
	if input.Status != nil && !validStatus(*input.Status) {
		return fmt.Errorf("%w: status", ErrInvalidProduct)
	}
	return nil
}

func validStatus(status Status) bool {
	return status == StatusOnSale || status == StatusOffSale
}

// maxProductImages bounds the gallery; the first entry is the main image.
const maxProductImages = 9

// normalizeImageKeys validates an incoming gallery: keys must be valid object
// keys, blanks are dropped, duplicates collapse to the first occurrence, and
// the result keeps order (position 0 = main image).
func normalizeImageKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !storage.ValidObjectKey(key) {
			return nil, fmt.Errorf("%w: invalid image key", ErrInvalidProduct)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) > maxProductImages {
		return nil, fmt.Errorf("%w: at most %d images per product", ErrInvalidProduct, maxProductImages)
	}
	return out, nil
}

func marshalImageKeys(keys []string) datatypes.JSON {
	if len(keys) == 0 {
		return datatypes.JSON("[]")
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return datatypes.JSON("[]")
	}
	return datatypes.JSON(raw)
}

func toView(product database.Product, resolveURL func(string) string) ProductView {
	keys := product.ImageKeys()
	images := make([]string, 0, len(keys))
	for _, key := range keys {
		images = append(images, resolveURL(key))
	}
	mainImage := ""
	if len(images) > 0 {
		mainImage = images[0]
	}
	return ProductView{
		ID:          product.ID,
		Name:        product.Name,
		Description: product.Description,
		Category:    product.Category,
		MainImage:   mainImage,
		Images:      images,
		PricePoints: product.PricePoints,
		Stock:       product.Stock,
		Status:      product.Status,
	}
}

func mapViews(products []database.Product, resolveURL func(string) string) []ProductView {
	out := make([]ProductView, 0, len(products))
	for i := range products {
		out = append(out, toView(products[i], resolveURL))
	}
	return out
}

func productAuditData(product *database.Product) map[string]any {
	if product == nil {
		return nil
	}
	keys := product.ImageKeys()
	redacted := make([]string, 0, len(keys))
	for _, key := range keys {
		redacted = append(redacted, redactImageKey(key))
	}
	return map[string]any{
		"name":         product.Name,
		"category":     product.Category,
		"status":       string(product.Status),
		"price_points": product.PricePoints,
		"stock":        product.Stock,
		"images":       redacted,
	}
}

// redactImageKey shortens an object key so audit data never carries a full
// object key.
func redactImageKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
