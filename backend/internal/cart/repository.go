package cart

import (
	"context"
	"errors"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository owns the cart_items queries that must span a caller's
// transaction. The cart service keeps its own CRUD transactions; the order
// usecase (T11) calls LockForOrder and DeleteByIDs with its own transaction
// handle so the selected cart items are locked, read and deleted in the same
// database transaction as the order, inventory, points and ledger writes.
type Repository struct {
	db *gorm.DB
}

// NewRepository validates the dependency and returns a Repository.
func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("cart repository: database is required")
	}
	return &Repository{db: db}, nil
}

// LockForOrder locks the buyer-owned cart rows selected by ids in ascending id
// order and returns them. Any requested row that is missing or owned by
// another buyer surfaces as ErrCartItemNotFound. The ascending-id lock order
// keeps concurrent order transactions from deadlocking on the same cart rows.
func (r *Repository) LockForOrder(db *gorm.DB, ctx context.Context, userID uid.ID, ids []uid.ID) ([]database.CartItem, error) {
	if len(ids) == 0 {
		return nil, ErrInvalidCartItem
	}
	var items []database.CartItem
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND id IN ?", userID, ids).
		Order("id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	found := make(map[uid.ID]struct{}, len(items))
	for _, item := range items {
		found[item.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			return nil, ErrCartItemNotFound
		}
	}
	return items, nil
}

// DeleteByIDs removes the buyer-owned cart rows selected by ids inside the
// caller's transaction. Rows owned by another buyer are left untouched; a
// requested row that is missing surfaces as ErrCartItemNotFound. The caller
// must have locked the rows first so the affected-row count is exact.
func (r *Repository) DeleteByIDs(db *gorm.DB, ctx context.Context, userID uid.ID, ids []uid.ID) error {
	if len(ids) == 0 {
		return nil
	}
	result := db.WithContext(ctx).
		Where("user_id = ? AND id IN ?", userID, ids).
		Delete(&database.CartItem{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return ErrCartItemNotFound
	}
	return nil
}
