package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Address sentinels the HTTP layer maps to status codes. ErrAddressNotFound
// doubles as the ownership-masking error: a buyer id-scoped query that matches
// nothing is indistinguishable from a row that belongs to another buyer. The
// order-creation usecase re-exports the same sentinel for its 422 mapping.
var (
	// ErrAddressNotFound is returned when the address does not exist or is not
	// owned by the supplied userID. The address handler maps it to 404.
	ErrAddressNotFound = errors.New("address not found")
	// ErrInvalidAddress is a field-level validation violation.
	ErrInvalidAddress = errors.New("invalid address")
	// ErrEmptyUpdate is returned when an update request carries no change.
	ErrEmptyUpdate = errors.New("empty update")
	// ErrDefaultAddressConflict is returned when the partial unique index
	// uq_default_address rejects a second default for the same buyer.
	ErrDefaultAddressConflict = errors.New("default address conflict")
)

// AddressInput is the validated create payload.
type AddressInput struct {
	Receiver  string
	Phone     string
	Region    string
	Detail    string
	IsDefault bool
}

// AddressUpdate is the partial-update payload. Nil fields are left unchanged.
type AddressUpdate struct {
	Receiver  *string
	Phone     *string
	Region    *string
	Detail    *string
	IsDefault *bool
}

// AddressView is the read projection. Version is the optimistic-concurrency
// token that order creation embeds in the server-side request hash together
// with the address snapshot.
type AddressView struct {
	ID        uid.ID
	Receiver  string
	Phone     string
	Region    string
	Detail    string
	IsDefault bool
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AddressService owns every read and write against the user_addresses table.
// The address handler and the order-creation usecase consume it through this
// boundary; nothing else writes addresses. All methods take the authenticated
// userID and never accept a client-supplied owner id.
type AddressService struct {
	db *gorm.DB
}

// AddressServiceDeps is the dependency bundle for NewAddressService.
type AddressServiceDeps struct {
	DB *gorm.DB
}

// NewAddressService validates the dependency bundle and returns the service.
func NewAddressService(deps AddressServiceDeps) (*AddressService, error) {
	if deps.DB == nil {
		return nil, errors.New("address service: database is required")
	}
	return &AddressService{db: deps.DB}, nil
}

// Create validates the contract's field rules and inserts an address for the
// buyer. When the new address is the default, the transaction first clears any
// existing default for the buyer so the replacement commits atomically; the
// partial unique index uq_default_address is the backstop that rejects a
// second default under a race. A fresh address starts at version 1.
func (s *AddressService) Create(ctx context.Context, userID uid.ID, input AddressInput) (AddressView, error) {
	if err := validateAddressInput(input); err != nil {
		return AddressView{}, err
	}
	if uid.IsZero(userID) {
		return AddressView{}, ErrInvalidAddress
	}
	var view AddressView
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		if input.IsDefault {
			if err := clearDefaultAddress(ctx, tx, userID, uid.ID{}); err != nil {
				return err
			}
		}
		addr := database.Address{
			UserID:    userID,
			Receiver:  strings.TrimSpace(input.Receiver),
			Phone:     strings.TrimSpace(input.Phone),
			Region:    strings.TrimSpace(input.Region),
			Detail:    strings.TrimSpace(input.Detail),
			IsDefault: input.IsDefault,
			Version:   1,
		}
		if err := tx.WithContext(ctx).Create(&addr).Error; err != nil {
			if database.IsUniqueViolationError(err) {
				return ErrDefaultAddressConflict
			}
			return err
		}
		view = toAddressView(addr)
		return nil
	})
	if err != nil {
		return AddressView{}, err
	}
	return view, nil
}

// List returns the buyer's addresses ordered by id ascending.
func (s *AddressService) List(ctx context.Context, userID uid.ID) ([]AddressView, error) {
	if uid.IsZero(userID) {
		return nil, ErrInvalidAddress
	}
	var rows []database.Address
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]AddressView, 0, len(rows))
	for i := range rows {
		views = append(views, toAddressView(rows[i]))
	}
	return views, nil
}

// Get returns the buyer-owned address. A row owned by another buyer surfaces
// as ErrAddressNotFound. Order creation uses this to build the server-side
// address snapshot and current version for the request hash.
func (s *AddressService) Get(ctx context.Context, userID, id uid.ID) (AddressView, error) {
	if uid.IsZero(userID) || uid.IsZero(id) {
		return AddressView{}, ErrAddressNotFound
	}
	var row database.Address
	if err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AddressView{}, ErrAddressNotFound
		}
		return AddressView{}, err
	}
	return toAddressView(row), nil
}

// Update validates the partial payload and applies the update, incrementing
// version in the same transaction as the field changes. Setting
// is_default=true clears any other default for the buyer in the same
// transaction; the partial unique index translates a concurrent
// double-default into ErrDefaultAddressConflict.
func (s *AddressService) Update(ctx context.Context, userID, id uid.ID, input AddressUpdate) (AddressView, error) {
	if err := validateAddressUpdate(input); err != nil {
		return AddressView{}, err
	}
	if uid.IsZero(userID) || uid.IsZero(id) {
		return AddressView{}, ErrAddressNotFound
	}
	var view AddressView
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		var current database.Address
		if err := tx.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAddressNotFound
			}
			return err
		}
		updates := map[string]any{}
		if input.Receiver != nil {
			updates["receiver"] = strings.TrimSpace(*input.Receiver)
		}
		if input.Phone != nil {
			updates["phone"] = strings.TrimSpace(*input.Phone)
		}
		if input.Region != nil {
			updates["region"] = strings.TrimSpace(*input.Region)
		}
		if input.Detail != nil {
			updates["detail"] = strings.TrimSpace(*input.Detail)
		}
		if input.IsDefault != nil && *input.IsDefault != current.IsDefault {
			if *input.IsDefault {
				// Clear the previous default for this buyer so the promotion
				// commits atomically with the field change.
				if err := clearDefaultAddress(ctx, tx, userID, id); err != nil {
					return err
				}
			}
			updates["is_default"] = *input.IsDefault
		}
		if len(updates) == 0 {
			return ErrEmptyUpdate
		}
		// The version is bumped atomically by the database so two concurrent
		// updates cannot both observe the same version and write it back.
		updates["version"] = gorm.Expr("version + ?", 1)
		if err := tx.WithContext(ctx).
			Model(&database.Address{}).
			Where("id = ? AND user_id = ?", id, userID).
			Updates(updates).Error; err != nil {
			if database.IsUniqueViolationError(err) {
				return ErrDefaultAddressConflict
			}
			return err
		}
		var updated database.Address
		if err := tx.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&updated).Error; err != nil {
			return err
		}
		view = toAddressView(updated)
		return nil
	})
	if err != nil {
		return AddressView{}, err
	}
	return view, nil
}

// Delete removes a buyer-owned address. Deleting the current default simply
// leaves the buyer without a default; no other address is promoted implicitly.
func (s *AddressService) Delete(ctx context.Context, userID, id uid.ID) error {
	if uid.IsZero(userID) || uid.IsZero(id) {
		return ErrAddressNotFound
	}
	return database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).
			Where("id = ? AND user_id = ?", id, userID).
			Delete(&database.Address{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAddressNotFound
		}
		return nil
	})
}

// LockForOrder returns the buyer-owned address with its row locked for update
// inside the caller's transaction. Order creation uses it to snapshot the
// address and its version for the order request hash, and to serialize
// concurrent address edits against order creation. A row owned by another
// buyer surfaces as ErrAddressNotFound.
func (s *AddressService) LockForOrder(ctx context.Context, tx *gorm.DB, userID, id uid.ID) (AddressView, error) {
	if uid.IsZero(userID) || uid.IsZero(id) {
		return AddressView{}, ErrAddressNotFound
	}
	var row database.Address
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ?", id, userID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AddressView{}, ErrAddressNotFound
		}
		return AddressView{}, err
	}
	return toAddressView(row), nil
}

// clearDefaultAddress unsets is_default on every default address of the buyer
// except the excluded one. It runs inside the caller's transaction so the
// default replacement commits atomically.
func clearDefaultAddress(ctx context.Context, tx *gorm.DB, userID, exceptID uid.ID) error {
	query := tx.WithContext(ctx).
		Model(&database.Address{}).
		Where("user_id = ? AND is_default = ?", userID, true)
	if !uid.IsZero(exceptID) {
		query = query.Where("id != ?", exceptID)
	}
	return query.Updates(map[string]any{"is_default": false}).Error
}

func toAddressView(row database.Address) AddressView {
	return AddressView{
		ID:        row.ID,
		Receiver:  row.Receiver,
		Phone:     row.Phone,
		Region:    row.Region,
		Detail:    row.Detail,
		IsDefault: row.IsDefault,
		Version:   row.Version,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

// validateAddressInput enforces the AddressCreateRequest rules: receiver,
// region and detail are 1-32/1-64/1-255 characters, phone is 6-20.
func validateAddressInput(input AddressInput) error {
	if strings.TrimSpace(input.Receiver) == "" || len([]rune(strings.TrimSpace(input.Receiver))) > 32 {
		return fmt.Errorf("%w: receiver must be 1-32 characters", ErrInvalidAddress)
	}
	phone := strings.TrimSpace(input.Phone)
	if len([]rune(phone)) < 6 || len([]rune(phone)) > 20 {
		return fmt.Errorf("%w: phone must be 6-20 characters", ErrInvalidAddress)
	}
	if strings.TrimSpace(input.Region) == "" || len([]rune(strings.TrimSpace(input.Region))) > 64 {
		return fmt.Errorf("%w: region must be 1-64 characters", ErrInvalidAddress)
	}
	if strings.TrimSpace(input.Detail) == "" || len([]rune(input.Detail)) > 255 {
		return fmt.Errorf("%w: detail must be 1-255 characters", ErrInvalidAddress)
	}
	return nil
}

// validateAddressUpdate enforces the AddressUpdateRequest rules on the fields
// that are present. An empty update is rejected by the service.
func validateAddressUpdate(input AddressUpdate) error {
	if input.Receiver != nil {
		if strings.TrimSpace(*input.Receiver) == "" || len([]rune(strings.TrimSpace(*input.Receiver))) > 32 {
			return fmt.Errorf("%w: receiver must be 1-32 characters", ErrInvalidAddress)
		}
	}
	if input.Phone != nil {
		phone := strings.TrimSpace(*input.Phone)
		if len([]rune(phone)) < 6 || len([]rune(phone)) > 20 {
			return fmt.Errorf("%w: phone must be 6-20 characters", ErrInvalidAddress)
		}
	}
	if input.Region != nil {
		if strings.TrimSpace(*input.Region) == "" || len([]rune(strings.TrimSpace(*input.Region))) > 64 {
			return fmt.Errorf("%w: region must be 1-64 characters", ErrInvalidAddress)
		}
	}
	if input.Detail != nil {
		if strings.TrimSpace(*input.Detail) == "" || len([]rune(*input.Detail)) > 255 {
			return fmt.Errorf("%w: detail must be 1-255 characters", ErrInvalidAddress)
		}
	}
	return nil
}
