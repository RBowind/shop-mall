package payment

import (
	"context"
	"errors"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Balance sentinels. Consumers (checkout, refund, points adjustment)
// translate these onto their own domain sentinels at the usecase boundary;
// the messages stay identical so the HTTP-visible error text is unchanged.
var (
	// ErrUserNotFound is returned when the buyer row does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrInsufficientBalance is returned when a negative adjustment would
	// drive the balance below zero.
	ErrInsufficientBalance = errors.New("insufficient points")
)

// LockUser locks the buyer row for update inside the caller's transaction so
// balance adjustments for the same buyer serialize. A missing row surfaces as
// ErrUserNotFound. Find with First is acceptable here: the no-row case is an
// error path, not an expected miss.
func (r *Repository) LockUser(tx *gorm.DB, ctx context.Context, userID uid.ID) error {
	var user database.User
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	return nil
}

// AdjustBalance applies the signed delta to the buyer's points balance inside
// the caller's transaction and returns the new balance. The caller must hold
// the row lock from LockUser. A negative delta carries the guard
// points_balance >= -delta so the balance can never go below zero; a guard
// miss reports ErrInsufficientBalance. RowsAffected is used instead of the
// no-row Scan error because GORM's Scan does not raise ErrRecordNotFound on
// its own.
func (r *Repository) AdjustBalance(tx *gorm.DB, ctx context.Context, userID uid.ID, delta int64) (int64, error) {
	var newBalance int64
	if delta >= 0 {
		if err := tx.WithContext(ctx).Raw(
			`UPDATE users SET points_balance = points_balance + ? WHERE id = ? RETURNING points_balance`,
			delta, userID,
		).Scan(&newBalance).Error; err != nil {
			return 0, err
		}
		return newBalance, nil
	}
	result := tx.WithContext(ctx).Raw(
		`UPDATE users SET points_balance = points_balance + ? WHERE id = ? AND points_balance >= ? RETURNING points_balance`,
		delta, userID, -delta,
	)
	if err := result.Scan(&newBalance).Error; err != nil {
		return 0, err
	}
	if result.RowsAffected == 0 {
		return 0, ErrInsufficientBalance
	}
	return newBalance, nil
}
