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

// ErrServiceUnavailable is returned when the Service was constructed without
// a usable database handle.
var ErrServiceUnavailable = errors.New("user service is not configured")

// ErrUserNotFound is returned when no buyer row matches the requested id. The
// /me handler maps it to 404 so a missing profile is indistinguishable from a
// resource that does not exist.
var ErrUserNotFound = errors.New("user not found")

// User is the application projection of a buyer row. The SessionKey and other
// server-only WeChat fields never appear here. UnionID is returned from the
// WeChat adapter and propagated for downstream consumers, but it is NOT
// persisted to the users table because the migration does not declare it.
type User struct {
	ID            uid.ID
	OpenID        string
	UnionID       string
	Nickname      string
	AvatarURL     string
	PointsBalance int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Subject returns the JWT subject string the buyer login usecase embeds in
// the issued token. The subject is stable per user id and matches what
// middleware expects when parsing the bearer token.
func (u User) Subject() string {
	if uid.IsZero(u.ID) {
		return ""
	}
	return fmt.Sprintf("buyer:%s", u.ID)
}

// Service is the application-layer gateway over the users and points_ledger
// tables. It owns the insert-or-read idempotency guarantee and the
// exactly-once signup bonus rule.
type Service struct {
	db *gorm.DB
}

// ServiceDeps is the dependency bundle for NewService.
type ServiceDeps struct {
	DB *gorm.DB
}

// NewService validates the dependency bundle and returns a Service.
func NewService(deps ServiceDeps) (*Service, error) {
	if deps.DB == nil {
		return nil, ErrServiceUnavailable
	}
	return &Service{db: deps.DB}, nil
}

// UpsertByOpenID performs an insert-or-read on users keyed by openid and
// creates the exactly-once signup_bonus ledger entry for new buyers. The
// entire operation runs inside a single transaction so the buyer write and
// the ledger write commit or roll back together.
//
// A negative signup bonus is rejected before the transaction starts so the
// caller gets a clear validation error. The supplied now() clock is used for
// the balance_after column when the bonus is awarded.
func (s *Service) UpsertByOpenID(ctx context.Context, openID, unionID string, signupBonus int64, nickname *string, now time.Time) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrServiceUnavailable
	}
	if strings.TrimSpace(openID) == "" {
		return User{}, errors.New("openid is required")
	}
	if signupBonus < 0 {
		return User{}, errors.New("signup bonus must be non-negative")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	var result User
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		user := database.User{OpenID: openID, Nickname: optionalString(nickname), PointsBalance: 0}
		if err := upsertUser(tx, &user); err != nil {
			return err
		}
		if user.PointsBalance == 0 && signupBonus > 0 && !uid.IsZero(user.ID) {
			created, err := awardSignupBonus(tx, user.ID, signupBonus, now)
			if err != nil {
				return err
			}
			// Only reflect the bonus in the in-memory projection when the
			// ledger row was actually created. An existing buyer who spent
			// their signup bonus re-logging in must not be served a phantom
			// balance: the unique partial index rejects the second bonus and
			// the reported balance must stay exactly what the database holds.
			if created {
				user.PointsBalance = signupBonus
			}
		}
		// Re-read the committed row so the projection always reflects the
		// database's authoritative balance. A concurrent first-login loser
		// raced the winner's bonus award cannot project a stale zero: the
		// INSERT ... ON CONFLICT wait already forced this transaction to see
		// the winner's commit, and this read confirms it.
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", user.ID).First(&user).Error; err != nil {
			return err
		}
		result = User{
			ID:            user.ID,
			OpenID:        user.OpenID,
			UnionID:       unionID,
			Nickname:      user.Nickname,
			AvatarURL:     user.AvatarURL,
			PointsBalance: user.PointsBalance,
			CreatedAt:     user.CreatedAt,
			UpdatedAt:     user.UpdatedAt,
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return result, nil
}

// GetByID returns the buyer profile for the given id. A missing row
// surfaces as ErrUserNotFound.
func (s *Service) GetByID(ctx context.Context, id uid.ID) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrServiceUnavailable
	}
	if uid.IsZero(id) {
		return User{}, errors.New("user id is required")
	}
	row := database.User{}
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	return toUserProjection(row), nil
}

// UpdateProfile applies ONLY the buyer profile fields that are present
// (non-nil nickname and avatar_url) and returns the refreshed profile. A field
// that did not appear in the request keeps its current value, so a partial
// PATCH never wipes the absent field. An id without a row surfaces as
// ErrUserNotFound.
func (s *Service) UpdateProfile(ctx context.Context, id uid.ID, nickname, avatarURL *string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrServiceUnavailable
	}
	if uid.IsZero(id) {
		return User{}, errors.New("user id is required")
	}
	updates := make(map[string]any, 2)
	if nickname != nil {
		updates["nickname"] = *nickname
	}
	if avatarURL != nil {
		updates["avatar_url"] = *avatarURL
	}
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&database.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return User{}, err
		}
	}
	return s.GetByID(ctx, id)
}

// toUserProjection maps a users row onto the application projection.
func toUserProjection(row database.User) User {
	return User{
		ID:            row.ID,
		OpenID:        row.OpenID,
		Nickname:      row.Nickname,
		AvatarURL:     row.AvatarURL,
		PointsBalance: row.PointsBalance,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

// upsertUser performs the insert-or-read against users inside the supplied
// transaction. The implementation uses INSERT ... ON CONFLICT DO NOTHING so
// concurrent first-login requests do not abort the transaction; the row is
// then re-read so callers always observe a stable PointsBalance.
func upsertUser(tx *gorm.DB, user *database.User) error {
	openID := strings.TrimSpace(user.OpenID)
	if openID == "" {
		return errors.New("openid is required")
	}
	insert := database.User{OpenID: openID, Nickname: user.Nickname, AvatarURL: user.AvatarURL, PointsBalance: 0}
	result := tx.WithContext(tx.Statement.Context).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&insert)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// The insert was a no-op because the openid already exists; reload.
		// With service-side UUID primary keys the pre-generated id cannot
		// serve as the conflict sentinel, so RowsAffected decides instead.
		existing := database.User{}
		if err := tx.WithContext(tx.Statement.Context).
			Where("openid = ?", openID).
			First(&existing).Error; err != nil {
			return err
		}
		*user = existing
		return nil
	}
	*user = insert
	return nil
}

// awardSignupBonus inserts the signup_bonus ledger row for the new buyer and
// returns whether the row was actually created. Exactly-once semantics rely on
// the unique partial index uq_signup_bonus_user (and the event_key UNIQUE
// constraint): a conflicting insert is handled with ON CONFLICT DO NOTHING so
// the transaction is never aborted by the conflict, and the caller sees
// created=false and does not reflect a bonus that never happened.
func awardSignupBonus(tx *gorm.DB, userID uid.ID, signupBonus int64, now time.Time) (bool, error) {
	row := database.PointsLedger{
		UserID:       userID,
		EventKey:     fmt.Sprintf("signup:%s", userID),
		Type:         database.LedgerTypeSignupBonus,
		Delta:        signupBonus,
		BalanceAfter: signupBonus,
		Remark:       "",
		CreatedAt:    now,
	}
	result := tx.WithContext(tx.Statement.Context).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		// The signup bonus ledger row already exists for this buyer.
		return false, nil
	}
	if err := tx.WithContext(tx.Statement.Context).
		Model(&database.User{}).
		Where("id = ?", userID).
		Update("points_balance", signupBonus).Error; err != nil {
		return false, err
	}
	return true, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
