package access

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
)

// AccessUsecase owns the access-related mutations: ChangeRole, UpdateAdmin
// and ChangePassword. The usecase is the only allowed writer for these
// tables outside of bootstrap and login. Each call runs inside a single
// transaction so the audit log row and the table mutation commit together.
type AccessUsecase struct {
	db      *gorm.DB
	service *admin.Service
	logger  *slog.Logger
	now     func() time.Time
}

// AccessUsecaseDeps is the dependency bundle for NewAccessUsecase.
type AccessUsecaseDeps struct {
	DB      *gorm.DB
	Service *admin.Service
	Logger  *slog.Logger
	Now     func() time.Time
}

// NewAccessUsecase validates the dependency bundle and returns the usecase.
func NewAccessUsecase(deps AccessUsecaseDeps) (*AccessUsecase, error) {
	if deps.DB == nil {
		return nil, errors.New("access usecase: database is required")
	}
	if deps.Service == nil {
		return nil, errors.New("access usecase: admin service is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &AccessUsecase{db: deps.DB, service: deps.Service, logger: logger, now: now}, nil
}

// ChangeRoleInput is the controlled input path for ChangeRole.
type ChangeRoleInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	TargetID     uid.ID
	NewRoleName  string
}

// ChangeRoleOutput mirrors admin.AssignRoleResult and is the observable
// payload of a successful role change.
type ChangeRoleOutput struct {
	AdminID  uid.ID
	Username string
	RoleName string
}

// ChangeRole reassigns the role for the target administrator. The new role's
// permission list is verified against the seed list before the write so a
// future migration that drops a permission cannot silently grant access. A
// failure audit row is written if the role does not exist.
func (u *AccessUsecase) ChangeRole(ctx context.Context, input ChangeRoleInput) (ChangeRoleOutput, error) {
	if u == nil || u.db == nil || u.service == nil {
		return ChangeRoleOutput{}, errors.New("access usecase is not configured")
	}
	if strings.TrimSpace(input.ActorRole) == "" {
		return ChangeRoleOutput{}, errors.New("actor role is required")
	}
	if uid.IsZero(input.ActorAdminID) || uid.IsZero(input.TargetID) {
		return ChangeRoleOutput{}, admin.ErrAdminNotFound
	}
	if strings.TrimSpace(input.NewRoleName) == "" {
		return ChangeRoleOutput{}, admin.ErrUnknownRole
	}

	result, err := u.service.AssignRole(ctx, admin.AssignRoleInput{
		ActorAdminID: input.ActorAdminID,
		ActorRole:    input.ActorRole,
		TargetID:     input.TargetID,
		NewRoleName:  input.NewRoleName,
	})
	if err == nil {
		return ChangeRoleOutput{
			AdminID:  result.AdminID,
			Username: result.Username,
			RoleName: result.RoleName,
		}, nil
	}
	if !isExpectedChangeRoleError(err) {
		return ChangeRoleOutput{}, err
	}
	u.recordRoleChangeFailure(ctx, input, err)
	return ChangeRoleOutput{}, err
}

// UpdateAdminInput is the controlled input path for UpdateAdmin.
type UpdateAdminInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	TargetID     uid.ID
	Enabled      *bool
}

// UpdateAdminOutput mirrors admin.UpdateAdminResult.
type UpdateAdminOutput struct {
	AdminID      uid.ID
	Username     string
	Enabled      bool
	RoleName     string
	TokenVersion int64
}

// UpdateAdmin toggles the enabled flag for the target administrator and
// bumps token_version when disabling so any outstanding JWT becomes invalid.
func (u *AccessUsecase) UpdateAdmin(ctx context.Context, input UpdateAdminInput) (UpdateAdminOutput, error) {
	if u == nil || u.db == nil || u.service == nil {
		return UpdateAdminOutput{}, errors.New("access usecase is not configured")
	}
	if strings.TrimSpace(input.ActorRole) == "" {
		return UpdateAdminOutput{}, errors.New("actor role is required")
	}
	if uid.IsZero(input.ActorAdminID) || uid.IsZero(input.TargetID) {
		return UpdateAdminOutput{}, admin.ErrAdminNotFound
	}
	result, err := u.service.UpdateAdmin(ctx, admin.UpdateAdminInput{
		ActorAdminID: input.ActorAdminID,
		ActorRole:    input.ActorRole,
		TargetID:     input.TargetID,
		Enabled:      input.Enabled,
	})
	if err != nil {
		return UpdateAdminOutput{}, err
	}
	return UpdateAdminOutput{
		AdminID:      result.AdminID,
		Username:     result.Username,
		Enabled:      result.Enabled,
		RoleName:     result.RoleName,
		TokenVersion: result.TokenVersion,
	}, nil
}

// ChangePasswordInput is the controlled input path for ChangePassword.
type ChangePasswordInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	TargetID     uid.ID
	OldPassword  string
	NewPassword  string
}

// ChangePasswordOutput mirrors admin.ChangePasswordResult.
type ChangePasswordOutput struct {
	AdminID      uid.ID
	TokenVersion int64
	Username     string
	RoleName     string
}

// ChangePassword hashes a new password with Argon2id and bumps
// token_version for the target administrator. The handler uses this method
// to invalidate any JWTs the administrator holds. The supplied OldPassword
// MUST match the stored hash so a stolen session alone cannot change the
// password.
func (u *AccessUsecase) ChangePassword(ctx context.Context, input ChangePasswordInput) (ChangePasswordOutput, error) {
	if u == nil || u.db == nil || u.service == nil {
		return ChangePasswordOutput{}, errors.New("access usecase is not configured")
	}
	if strings.TrimSpace(input.ActorRole) == "" {
		return ChangePasswordOutput{}, errors.New("actor role is required")
	}
	if uid.IsZero(input.ActorAdminID) || uid.IsZero(input.TargetID) {
		return ChangePasswordOutput{}, admin.ErrAdminNotFound
	}
	if err := admin.ValidatePassword(input.NewPassword); err != nil {
		return ChangePasswordOutput{}, err
	}

	result, err := u.service.ChangePassword(ctx, admin.ChangePasswordInput{
		ActorAdminID: input.ActorAdminID,
		ActorRole:    input.ActorRole,
		Subject:      adminSubject(input.TargetID),
		OldPassword:  input.OldPassword,
		NewPassword:  input.NewPassword,
	})
	if err != nil {
		return ChangePasswordOutput{}, err
	}
	return ChangePasswordOutput{
		AdminID:      result.AdminID,
		TokenVersion: result.TokenVersion,
		Username:     result.Username,
		RoleName:     result.RoleName,
	}, nil
}

// PermissionsForAdmin returns the current permission codes for the supplied
// administrator. The caller MUST resolve permissions on every authorization
// decision; permissions are NOT embedded in the JWT.
func (u *AccessUsecase) PermissionsForAdmin(ctx context.Context, adminID uid.ID) ([]string, error) {
	if u == nil || u.service == nil {
		return nil, errors.New("access usecase is not configured")
	}
	return u.service.PermissionsForAdmin(ctx, adminID)
}

func (u *AccessUsecase) recordRoleChangeFailure(ctx context.Context, input ChangeRoleInput, cause error) {
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &input.ActorAdminID,
			ActorRole:    input.ActorRole,
			Action:       "admin.role.change",
			TargetType:   "admin",
			TargetID:     &input.TargetID,
			Result:       "failure",
			AfterData: map[string]any{
				"role_name": input.NewRoleName,
				"reason":    cause.Error(),
			},
		})
	})
	if err != nil {
		u.logger.ErrorContext(ctx, "record role change failure audit", "error", err)
	}
}

func isExpectedChangeRoleError(err error) bool {
	switch {
	case errors.Is(err, admin.ErrAdminNotFound),
		errors.Is(err, admin.ErrUnknownRole),
		errors.Is(err, admin.ErrUnknownPermission):
		return true
	default:
		return false
	}
}

// adminSubject is the JWT subject format used for administrator tokens. It
// mirrors the format used by the tokens.Signer so middleware can compare the
// subject against the route-level handler.
func adminSubject(id uid.ID) string {
	return "admin:" + id.String()
}
