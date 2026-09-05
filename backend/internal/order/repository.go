package order

import (
	"context"
	"errors"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository owns the orders read queries. All reads preload the order items
// so the handler can render the full Order projection.
type Repository struct {
	db *gorm.DB
}

// NewRepository validates the dependency and returns a Repository.
func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("order repository: database is required")
	}
	return &Repository{db: db}, nil
}

// ListByUser returns the buyer-owned orders ordered by created_at DESC, id
// DESC, optionally filtered by status.
func (r *Repository) ListByUser(ctx context.Context, userID uid.ID, page, pageSize int, status *Status) ([]database.Order, int64, error) {
	base := r.db.WithContext(ctx).Model(&database.Order{}).Where("user_id = ?", userID)
	if status != nil {
		base = base.Where("status = ?", *status)
	}
	return r.pageSelect(ctx, base, page, pageSize)
}

// ListAll returns every order ordered by created_at DESC, id DESC, optionally
// filtered by status. This is the administrator surface; there is no buyer
// ownership mask on it.
func (r *Repository) ListAll(ctx context.Context, page, pageSize int, status *Status) ([]database.Order, int64, error) {
	base := r.db.WithContext(ctx).Model(&database.Order{})
	if status != nil {
		base = base.Where("status = ?", *status)
	}
	return r.pageSelect(ctx, base, page, pageSize)
}

// GetByUser returns a buyer-owned order. A row owned by another buyer is
// indistinguishable from a missing one (ErrOrderNotFound).
func (r *Repository) GetByUser(ctx context.Context, userID, id uid.ID) (*database.Order, error) {
	var row database.Order
	err := r.db.WithContext(ctx).Preload("Items").
		Where("id = ? AND user_id = ?", id, userID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListRefunds returns the refund-related orders (refund_requested and
// refunded) ordered by created_at DESC, id DESC. This is the administrator
// refund-review surface; there is no buyer ownership mask on it.
func (r *Repository) ListRefunds(ctx context.Context, page, pageSize int) ([]database.Order, int64, error) {
	base := r.db.WithContext(ctx).Model(&database.Order{}).
		Where("status IN ?", []Status{
			StatusRefundRequested,
			StatusRefunded,
		})
	return r.pageSelect(ctx, base, page, pageSize)
}

// GetByID returns any order regardless of the owning buyer. This is the
// administrator detail surface.
func (r *Repository) GetByID(ctx context.Context, id uid.ID) (*database.Order, error) {
	var row database.Order
	err := r.db.WithContext(ctx).Preload("Items").
		Where("id = ?", id).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// LockForUpdate locks a single order row (with its items preloaded) for
// update inside the supplied transaction. A missing row surfaces as
// ErrOrderNotFound. Find with Limit(1) is used instead of First so the
// expected no-row case does not log a record-not-found error on every miss.
func (r *Repository) LockForUpdate(tx *gorm.DB, ctx context.Context, orderID uid.ID) (*database.Order, error) {
	var rows []database.Order
	if err := tx.WithContext(ctx).Preload("Items").Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", orderID).
		Limit(1).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrOrderNotFound
	}
	return &rows[0], nil
}

// LockForUpdateByUser locks a buyer-owned order row for update inside the
// supplied transaction. A row owned by another buyer is indistinguishable
// from a missing one (ErrOrderNotFound).
func (r *Repository) LockForUpdateByUser(tx *gorm.DB, ctx context.Context, userID, orderID uid.ID) (*database.Order, error) {
	var rows []database.Order
	if err := tx.WithContext(ctx).Preload("Items").Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ?", orderID, userID).
		Limit(1).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrOrderNotFound
	}
	return &rows[0], nil
}

// LockForReplay locks the existing same-token order (with its items) for
// update inside the supplied transaction. A missing order returns (nil, nil)
// so the create path continues.
func (r *Repository) LockForReplay(tx *gorm.DB, ctx context.Context, userID uid.ID, clientToken string) (*database.Order, error) {
	var rows []database.Order
	err := tx.WithContext(ctx).Preload("Items").Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND client_token = ?", userID, clientToken).
		Limit(1).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// FindForReplay reads (without locking) the existing same-token order with
// its items, returning (nil, nil) when none exists. It is used ONLY on the
// cart-not-found re-check path inside CreateOrderUsecase: this transaction
// already holds the user-row lock and the documented lock order requires the
// order row to be locked first, so a FOR UPDATE here would invert the order
// (user -> address -> order instead of order -> user -> address) and open a
// deadlock window with a concurrent replay that holds the order row at step 1
// and wants the address row in resolveReplay. The read is safe without a lock
// because the user-row lock serializes this transaction behind a same-token
// winner's commit: when this transaction holds the user row, a winner has
// already committed, so the order row is committed and visible to a plain
// read. If instead the loser's transaction wins the user row, the cart rows
// still exist and this path is never reached.
func (r *Repository) FindForReplay(tx *gorm.DB, ctx context.Context, userID uid.ID, clientToken string) (*database.Order, error) {
	var rows []database.Order
	err := tx.WithContext(ctx).Preload("Items").
		Where("user_id = ? AND client_token = ?", userID, clientToken).
		Limit(1).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// FindByToken reads the existing same-token order (items preloaded) outside
// any transaction. A missing row surfaces as ErrOrderNotFound.
func (r *Repository) FindByToken(ctx context.Context, userID uid.ID, clientToken string) (*database.Order, error) {
	var row database.Order
	err := r.db.WithContext(ctx).Preload("Items").
		Where("user_id = ? AND client_token = ?", userID, clientToken).
		Limit(1).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateFields applies a conditional state change inside the supplied
// transaction. The caller already holds the order-row lock and verified the
// status, so the WHERE clause is a defensive guard; a zero-affected-row
// result surfaces as ErrOrderStateConflict.
func (r *Repository) UpdateFields(tx *gorm.DB, ctx context.Context, where, fields map[string]any) error {
	result := tx.WithContext(ctx).Model(&database.Order{}).Where(where).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrderStateConflict
	}
	return nil
}

func (r *Repository) pageSelect(ctx context.Context, base *gorm.DB, page, pageSize int) ([]database.Order, int64, error) {
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []database.Order
	if err := base.Preload("Items").
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
