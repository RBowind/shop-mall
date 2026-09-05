package payment

import (
	"context"
	"errors"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrLedgerEntryNotFound is returned when an event key matches no ledger row.
// The order and points usecases translate it into their replay semantics.
var ErrLedgerEntryNotFound = errors.New("ledger entry not found")

// Repository owns the points_ledger queries. Every method accepts the executor
// (a *gorm.DB) so the same query runs against the session or inside a caller's
// transaction; the order and points usecases pass their transaction handle and
// the buyer ledger list passes the session handle.
type Repository struct {
	db *gorm.DB
}

// NewRepository validates the dependency and returns a Repository.
func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("payment repository: database is required")
	}
	return &Repository{db: db}, nil
}

// Create inserts a ledger row inside the caller's transaction.
func (r *Repository) Create(db *gorm.DB, ctx context.Context, row *database.PointsLedger) error {
	return db.WithContext(ctx).Create(row).Error
}

// FindByEventKey returns the ledger row identified by its unique event key.
func (r *Repository) FindByEventKey(db *gorm.DB, ctx context.Context, eventKey string) (*database.PointsLedger, error) {
	var row database.PointsLedger
	if err := db.WithContext(ctx).Where("event_key = ?", eventKey).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLedgerEntryNotFound
		}
		return nil, err
	}
	return &row, nil
}

// LockByEventKey returns the ledger row identified by its event key and locks
// it for update so concurrent replays of the same event serialize instead of
// double-applying.
func (r *Repository) LockByEventKey(db *gorm.DB, ctx context.Context, eventKey string) (*database.PointsLedger, error) {
	var row database.PointsLedger
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("event_key = ?", eventKey).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLedgerEntryNotFound
		}
		return nil, err
	}
	return &row, nil
}

// ListByUser returns the buyer's ledger page ordered by id descending.
func (r *Repository) ListByUser(db *gorm.DB, ctx context.Context, userID uid.ID, page, pageSize int) ([]database.PointsLedger, int64, error) {
	base := db.WithContext(ctx).Model(&database.PointsLedger{}).Where("user_id = ?", userID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []database.PointsLedger
	if err := base.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
