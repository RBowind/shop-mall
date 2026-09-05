package product

import (
	"context"
	"errors"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrProductNotFound is returned when a product identifier does not match any
// row. The public detail handler maps it to 404, which also hides off_sale
// products from buyers.
var ErrProductNotFound = errors.New("product not found")

// ErrInsufficientStock is returned by DecrementStock when the product does not
// have enough stock for the requested quantity. Order creation maps it to a
// business error (422).
var ErrInsufficientStock = errors.New("insufficient stock")

// Repository owns the products table queries. Every method accepts the
// executor so the service can run the same query inside a transaction.
type Repository struct {
	db *gorm.DB
}

// NewRepository validates the dependency and returns a Repository.
func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("product repository: database is required")
	}
	return &Repository{db: db}, nil
}

// ListOnSale returns the products with status on_sale ordered by id
// descending, together with the total count. A non-empty category restricts
// the result to that category key; a non-empty keyword matches product names
// case-insensitively (LIKE %keyword%).
func (r *Repository) ListOnSale(ctx context.Context, page, pageSize int, category, keyword string) ([]database.Product, int64, error) {
	query := "status = ?"
	args := []any{StatusOnSale}
	if category != "" {
		query += " AND category = ?"
		args = append(args, category)
	}
	if keyword != "" {
		query += " AND name ILIKE ?"
		args = append(args, "%"+keyword+"%")
	}
	return r.PageSelect(ctx, page, pageSize, database.Product{}, query, args...)
}

// CountOnSaleByCategory groups on-sale products by category key. Products
// with an empty category are excluded; the public categories endpoint pairs
// the counts with the static catalog.
func (r *Repository) CountOnSaleByCategory(ctx context.Context) (map[Category]int64, error) {
	type row struct {
		Category string
		Total    int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&database.Product{}).
		Select("category, COUNT(*) AS total").
		Where("status = ? AND category <> ''", StatusOnSale).
		Group("category").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[Category]int64, len(rows))
	for _, r := range rows {
		counts[Category(r.Category)] = r.Total
	}
	return counts, nil
}

// ListAdmin returns products with an optional status filter ordered by id
// descending. A nil status selects every product.
func (r *Repository) ListAdmin(ctx context.Context, page, pageSize int, status *Status) ([]database.Product, int64, error) {
	var (
		query string
		args  []any
	)
	if status != nil {
		query = "status = ?"
		args = []any{*status}
	}
	return r.PageSelect(ctx, page, pageSize, database.Product{}, query, args...)
}

// GetPublic returns the product identified by id only when it is on sale. An
// off_sale product is returned as ErrProductNotFound so the read API keeps
// off_sale products indistinguishable from missing ones.
func (r *Repository) GetPublic(ctx context.Context, id uid.ID) (database.Product, error) {
	return r.get(ctx, "id = ? AND status = ?", id, StatusOnSale)
}

// GetByID returns the product identified by id regardless of status.
func (r *Repository) GetByID(ctx context.Context, id uid.ID) (database.Product, error) {
	return r.get(ctx, "id = ?", id)
}

func (r *Repository) get(ctx context.Context, query string, args ...any) (database.Product, error) {
	var product database.Product
	err := r.db.WithContext(ctx).Where(query, args...).First(&product).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return database.Product{}, ErrProductNotFound
	}
	return product, err
}

// LockByIDs locks the product rows selected by ids in ascending id order and
// returns them. Any requested row that is missing surfaces as ErrProductNotFound.
// The order usecase (T11) acquires these locks in ascending product_id order so
// concurrent order transactions on overlapping products cannot deadlock.
func (r *Repository) LockByIDs(db *gorm.DB, ctx context.Context, ids []uid.ID) ([]database.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var products []database.Product
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&products).Error; err != nil {
		return nil, err
	}
	found := make(map[uid.ID]struct{}, len(products))
	for _, product := range products {
		found[product.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			return nil, ErrProductNotFound
		}
	}
	return products, nil
}

// DecrementStock conditionally decrements the stock of an on-sale product and
// returns the remaining stock. The WHERE clause makes the decrement atomic so
// stock can never go negative even without a preceding lock;
// ErrInsufficientStock is reported when the row is missing, off-sale or the
// stock is too low. RowsAffected is used instead of the no-row Scan error
// because GORM's Scan does not raise ErrRecordNotFound on its own.
func (r *Repository) DecrementStock(db *gorm.DB, ctx context.Context, id uid.ID, qty int32) (int32, error) {
	var remaining int32
	result := db.WithContext(ctx).Raw(
		`UPDATE products SET stock = stock - ? WHERE id = ? AND status = 'on_sale' AND stock >= ? RETURNING stock`,
		qty, id, qty,
	)
	if err := result.Scan(&remaining).Error; err != nil {
		return 0, err
	}
	if result.RowsAffected == 0 {
		return 0, ErrInsufficientStock
	}
	return remaining, nil
}

// IncrementStock adds qty to a product's stock and returns the remaining stock.
// It is the financial inverse of DecrementStock: the refund approval restores
// inventory by incrementing the current value, never by clobbering it. A
// missing product surfaces as ErrProductNotFound. RowsAffected is used instead
// of the no-row Scan error because GORM's Scan does not raise ErrRecordNotFound
// on its own.
func (r *Repository) IncrementStock(db *gorm.DB, ctx context.Context, id uid.ID, qty int32) (int32, error) {
	var remaining int32
	result := db.WithContext(ctx).Raw(
		`UPDATE products SET stock = stock + ? WHERE id = ? RETURNING stock`,
		qty, id,
	)
	if err := result.Scan(&remaining).Error; err != nil {
		return 0, err
	}
	if result.RowsAffected == 0 {
		return 0, ErrProductNotFound
	}
	return remaining, nil
}

// PageSelect applies the common paging and ordering for product listings.
func (r *Repository) PageSelect(ctx context.Context, page, pageSize int, model database.Product, query string, args ...any) ([]database.Product, int64, error) {
	base := r.db.WithContext(ctx).Model(&database.Product{})
	if query != "" {
		base = base.Where(query, args...)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var products []database.Product
	err := base.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&products).Error
	if err != nil {
		return nil, 0, err
	}
	return products, total, nil
}
