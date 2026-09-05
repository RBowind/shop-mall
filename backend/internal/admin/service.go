package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/metrics"

	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// ErrServiceUnavailable is returned when the service is constructed without a
// usable database handle.
var ErrServiceUnavailable = errors.New("admin service is not configured")

// ErrAdminNotFound is returned when an administrator row does not exist for
// the supplied identifier.
var ErrAdminNotFound = errors.New("administrator not found")

// ErrAdminDisabled is returned when authentication is attempted for a
// disabled administrator.
var ErrAdminDisabled = errors.New("administrator is disabled")

// ErrInvalidCredentials is returned when the supplied username does not
// exist or the password does not match the stored hash.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrWeakPassword is returned when the supplied password fails the strength
// validation rules.
var ErrWeakPassword = errors.New("weak password")

// ErrInvalidUsername is returned when the supplied username fails the
// validation rules (blank, contains spaces, or has unsupported characters).
var ErrInvalidUsername = errors.New("invalid username")

// ErrUnknownRole is returned when the role name does not exist in the roles
// table.
var ErrUnknownRole = errors.New("unknown role")

// ErrAdministratorExists is returned when bootstrap is called for a username
// that already exists in admin_users.
var ErrAdministratorExists = errors.New("administrator already exists")

// ErrUnknownPermission is returned when a role lookup resolves permissions
// that are not in the seed list.
var ErrUnknownPermission = errors.New("unknown permission")

// ErrRoleNotFound is returned when a role row does not exist for the supplied
// identifier.
var ErrRoleNotFound = errors.New("role not found")

// ErrRoleExists is returned when a role write collides with an existing role
// name.
var ErrRoleExists = errors.New("role already exists")

// ErrInvalidRoleName is returned when a role name is blank or longer than the
// roles.name column allows.
var ErrInvalidRoleName = errors.New("invalid role name")

// ErrRoleUpdateEmpty is returned when a role patch carries neither a name nor
// a permissions replacement.
var ErrRoleUpdateEmpty = errors.New("role update requires name or permissions")

// ErrAdminUserUpdateEmpty is returned when an administrator patch carries
// neither an enabled flag nor a role id.
var ErrAdminUserUpdateEmpty = errors.New("admin user update requires enabled or role_id")

// ErrRateLimited is returned when the login rate limiter rejects a request
// because the account+IP bucket exceeded the configured limit.
var ErrRateLimited = errors.New("rate limit exceeded")

// Argon2id parameters. The cost is conservative for tests and matches the
// recommendation in RFC 9106 for interactive use.
const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

// Service is the application-layer gateway over the admin_users, roles,
// permissions, role_permissions and audit_logs tables. It owns the Argon2id
// password hashing, the role/permission reads, the audit-log writes and the
// login rate-limit policy.
type Service struct {
	db          *gorm.DB
	logger      *slog.Logger
	now         func() time.Time
	rateLimiter *LoginRateLimiter
	metrics     *metrics.Metrics
}

// ServiceDeps is the dependency bundle for NewService.
type ServiceDeps struct {
	DB          *gorm.DB
	Logger      *slog.Logger
	Now         func() time.Time
	RateLimiter *LoginRateLimiter
	Metrics     *metrics.Metrics
}

// NewService validates the dependency bundle and returns a Service.
func NewService(deps ServiceDeps) (*Service, error) {
	if deps.DB == nil {
		return nil, ErrServiceUnavailable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Service{db: deps.DB, logger: deps.Logger, now: deps.Now, rateLimiter: deps.RateLimiter, metrics: deps.Metrics}, nil
}

// SetRateLimiter swaps the LoginRateLimiter dependency after construction.
// Tests use it to attach the deterministic limiter created by the fixture.
func (s *Service) SetRateLimiter(limiter *LoginRateLimiter) {
	if s == nil {
		return
	}
	s.rateLimiter = limiter
}

// BootstrapInput is the controlled input path of the BootstrapAdministrator
// call. Every field is validated before any database write happens.
type BootstrapInput struct {
	Username  string
	Password  string
	RoleName  string
	ActorName string
}

// BootstrapResult is the observable result of a successful bootstrap.
type BootstrapResult struct {
	AdminID      uid.ID
	Username     string
	RoleName     string
	TokenVersion int64
}

// BootstrapAdministrator creates the first administrator for a fresh role
// and refuses to overwrite an existing username. The operation writes an
// audit log row using the system actor so the bootstrap path is always
// traceable. The password is hashed with Argon2id.
func (s *Service) BootstrapAdministrator(ctx context.Context, input BootstrapInput) (BootstrapResult, error) {
	if s == nil || s.db == nil {
		return BootstrapResult{}, ErrServiceUnavailable
	}
	if err := ValidateUsername(input.Username); err != nil {
		return BootstrapResult{}, err
	}
	if err := ValidatePassword(input.Password); err != nil {
		return BootstrapResult{}, err
	}
	if strings.TrimSpace(input.RoleName) == "" {
		return BootstrapResult{}, ErrUnknownRole
	}

	var result BootstrapResult
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		var role database.Role
		if err := tx.WithContext(tx.Statement.Context).
			Where("name = ?", input.RoleName).
			First(&role).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUnknownRole
			}
			return err
		}
		if err := ensureAllPermissionsAreSeeded(tx, role.ID); err != nil {
			return err
		}
		hash, err := hashPassword(input.Password)
		if err != nil {
			return err
		}
		admin := database.AdminUser{
			Username:     strings.TrimSpace(input.Username),
			PasswordHash: hash,
			TokenVersion: 1,
			RoleID:       role.ID,
			Enabled:      true,
		}
		if err := tx.WithContext(tx.Statement.Context).Create(&admin).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrAdministratorExists
			}
			return err
		}
		if err := writeAuditLog(tx, auditEntry{
			Action:     "admin.bootstrap",
			TargetType: "admin",
			TargetID:   &admin.ID,
			Result:     "success",
			AfterData: map[string]any{
				"username":  admin.Username,
				"role_name": role.Name,
				"actor":     input.ActorName,
			},
			ActorRole: "system",
		}); err != nil {
			return err
		}
		result = BootstrapResult{
			AdminID:      admin.ID,
			Username:     admin.Username,
			RoleName:     role.Name,
			TokenVersion: admin.TokenVersion,
		}
		return nil
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	return result, nil
}

// AuthenticateInput is the controlled input path of Authenticate. The IP
// field is used together with the username as a rate-limit bucket.
type AuthenticateInput struct {
	Username string
	Password string
	IP       string
}

// AuthenticateResult is the observable result of a successful login. It
// exposes the freshly issued JWT so the caller does not have to perform the
// signing itself. The signing step runs after the database transaction
// commits so a signer failure cannot roll back the user write.
type AuthenticateResult struct {
	AdminID      uid.ID
	Subject      string
	Username     string
	RoleID       uid.ID
	RoleName     string
	Permissions  []string
	TokenVersion int64
	Token        string
}

// Authenticate verifies the supplied credentials against the stored hash,
// writes an audit log row and signs a JWT after the transaction commits.
// token_version is read but NOT bumped: password change, logout and disable
// are the only events that invalidate outstanding JWTs, so a login always
// coexists with the administrator's other live sessions. The function is safe
// to call concurrently for the same username. The LoginRateLimiter, when
// non-nil, is consulted before any database I/O so a brute-force attack cannot
// exhaust the connection pool.
func (s *Service) Authenticate(ctx context.Context, input AuthenticateInput, signer SignerInterface) (AuthenticateResult, error) {
	if s == nil || s.db == nil {
		return AuthenticateResult{}, ErrServiceUnavailable
	}
	if signer == nil {
		return AuthenticateResult{}, errors.New("authenticate: signer is required")
	}
	if strings.TrimSpace(input.Username) == "" {
		return AuthenticateResult{}, ErrInvalidCredentials
	}
	if input.Password == "" {
		return AuthenticateResult{}, ErrInvalidCredentials
	}
	if s.rateLimiter != nil && !s.rateLimiter.Allow(strings.TrimSpace(input.Username), input.IP) {
		if s.metrics != nil {
			s.metrics.RecordLoginThrottled()
		}
		return AuthenticateResult{}, ErrRateLimited
	}
	var (
		subject      string
		username     string
		roleName     string
		tokenVersion int64
		adminID      uid.ID
		roleID       uid.ID
	)
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		admin := database.AdminUser{}
		err := tx.WithContext(tx.Statement.Context).
			Preload("Role").
			Where("username = ?", strings.TrimSpace(input.Username)).
			First(&admin).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidCredentials
			}
			return err
		}
		if !admin.Enabled {
			return ErrAdminDisabled
		}
		if !verifyPassword(input.Password, admin.PasswordHash) {
			return ErrInvalidCredentials
		}
		// A successful login does NOT bump token_version. The contract ties
		// token_version to password change, logout invalidation and admin
		// disable; bumping it on every login would break concurrent sessions
		// for the same administrator. The issued JWT carries the current
		// version so a single login cannot invalidate another live session.
		now := s.now().UTC()
		if err := tx.WithContext(tx.Statement.Context).
			Model(&database.AdminUser{}).
			Where("id = ?", admin.ID).
			Update("last_login_at", now).Error; err != nil {
			return err
		}
		roleNameTx := ""
		if admin.Role.Name != "" {
			roleNameTx = admin.Role.Name
		} else {
			roleNameTx = lookupRoleName(tx, admin.RoleID)
		}
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &admin.ID,
			ActorRole:    roleNameTx,
			Action:       "admin.login",
			TargetType:   "admin",
			TargetID:     &admin.ID,
			Result:       "success",
			AfterData: map[string]any{
				"username":  admin.Username,
				"role_name": roleNameTx,
				"ip":        input.IP,
				"token_ver": admin.TokenVersion,
			},
		}); err != nil {
			return err
		}
		adminID = admin.ID
		username = admin.Username
		roleID = admin.RoleID
		roleName = roleNameTx
		tokenVersion = admin.TokenVersion
		subject = adminSubject(admin.ID)
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrAdminDisabled):
			s.recordFailedLogin(ctx, input, "account_disabled")
		case errors.Is(err, ErrInvalidCredentials):
			s.recordFailedLogin(ctx, input, "invalid_credentials")
		}
		return AuthenticateResult{}, err
	}
	permissions, err := rolePermissions(s.db, ctx, roleID)
	if err != nil {
		return AuthenticateResult{}, err
	}
	token, err := signer.Issue(subject, tokenVersion, s.now())
	if err != nil {
		return AuthenticateResult{}, fmt.Errorf("sign administrator token: %w", err)
	}
	return AuthenticateResult{
		AdminID:      adminID,
		Subject:      subject,
		Username:     username,
		RoleID:       roleID,
		RoleName:     roleName,
		Permissions:  permissions,
		TokenVersion: tokenVersion,
		Token:        token,
	}, nil
}

// SignerInterface is the subset of tokens.Signer that Authenticate uses. It
// mirrors the application/auth.SignerInterface and lets the admin service
// sign JWTs without depending on the auth package directly. The handler
// supplies the concrete tokens.Signer at request time.
type SignerInterface interface {
	Issue(subject string, tokenVersion int64, now time.Time) (string, error)
}

// Logout bumps the administrator's token_version so previously issued JWTs
// become invalid. The handler middleware checks the token_version claim
// against the database row on each request.
func (s *Service) Logout(ctx context.Context, subject string, actorID uid.ID) error {
	if s == nil || s.db == nil {
		return ErrServiceUnavailable
	}
	return database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		admin := database.AdminUser{}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", actorID).First(&admin).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminNotFound
			}
			return err
		}
		newVersion := admin.TokenVersion + 1
		if err := tx.WithContext(tx.Statement.Context).
			Model(&database.AdminUser{}).
			Where("id = ?", admin.ID).
			Update("token_version", newVersion).Error; err != nil {
			return err
		}
		return writeAuditLog(tx, auditEntry{
			ActorAdminID: &admin.ID,
			ActorRole:    adminRoleName(tx, admin.RoleID),
			Action:       "admin.logout",
			TargetType:   "admin",
			TargetID:     &admin.ID,
			Result:       "success",
			AfterData: map[string]any{
				"username":  admin.Username,
				"token_ver": newVersion,
				"subject":   subject,
			},
		})
	})
}

// ChangePasswordInput is the controlled input path for ChangePassword.
type ChangePasswordInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	Subject      string
	OldPassword  string
	NewPassword  string
}

// ChangePasswordResult is the observable result of a successful change.
type ChangePasswordResult struct {
	AdminID      uid.ID
	TokenVersion int64
	Username     string
	RoleName     string
}

// ChangePassword verifies the old password, hashes the new password with
// Argon2id, bumps the administrator's token_version so the old JWT becomes
// invalid, and writes an audit log row. The function refuses weak passwords
// before touching the database.
func (s *Service) ChangePassword(ctx context.Context, input ChangePasswordInput) (ChangePasswordResult, error) {
	if s == nil || s.db == nil {
		return ChangePasswordResult{}, ErrServiceUnavailable
	}
	if err := ValidatePassword(input.NewPassword); err != nil {
		return ChangePasswordResult{}, err
	}
	if input.OldPassword == "" {
		return ChangePasswordResult{}, ErrInvalidCredentials
	}
	if uid.IsZero(input.ActorAdminID) {
		return ChangePasswordResult{}, ErrAdminNotFound
	}

	var result ChangePasswordResult
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		admin := database.AdminUser{}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", input.ActorAdminID).First(&admin).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminNotFound
			}
			return err
		}
		if !admin.Enabled {
			return ErrAdminDisabled
		}
		if !verifyPassword(input.OldPassword, admin.PasswordHash) {
			return ErrInvalidCredentials
		}
		hash, err := hashPassword(input.NewPassword)
		if err != nil {
			return err
		}
		newVersion := admin.TokenVersion + 1
		if err := tx.WithContext(tx.Statement.Context).
			Model(&database.AdminUser{}).
			Where("id = ?", admin.ID).
			Updates(map[string]any{
				"password_hash": hash,
				"token_version": newVersion,
			}).Error; err != nil {
			return err
		}
		roleName := adminRoleName(tx, admin.RoleID)
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &admin.ID,
			ActorRole:    roleName,
			Action:       "admin.password.change",
			TargetType:   "admin",
			TargetID:     &admin.ID,
			Result:       "success",
			AfterData: map[string]any{
				"username":  admin.Username,
				"role_name": roleName,
				"token_ver": newVersion,
				"actor":     input.ActorRole,
			},
		}); err != nil {
			return err
		}
		result = ChangePasswordResult{
			AdminID:      admin.ID,
			TokenVersion: newVersion,
			Username:     admin.Username,
			RoleName:     roleName,
		}
		return nil
	})
	if err != nil {
		return ChangePasswordResult{}, err
	}
	return result, nil
}

// UpdateAdminInput is the controlled input path of UpdateAdmin.
type UpdateAdminInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	TargetID     uid.ID
	Enabled      *bool
}

// UpdateAdminResult is the observable result of an admin update.
type UpdateAdminResult struct {
	AdminID      uid.ID
	Username     string
	Enabled      bool
	RoleName     string
	TokenVersion int64
}

// UpdateAdmin toggles the enabled flag for the target administrator and
// writes an audit log row. The token_version is bumped whenever the
// administrator is disabled so any outstanding JWTs become invalid.
func (s *Service) UpdateAdmin(ctx context.Context, input UpdateAdminInput) (UpdateAdminResult, error) {
	if s == nil || s.db == nil {
		return UpdateAdminResult{}, ErrServiceUnavailable
	}
	if uid.IsZero(input.ActorAdminID) || uid.IsZero(input.TargetID) {
		return UpdateAdminResult{}, ErrAdminNotFound
	}
	var result UpdateAdminResult
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		admin := database.AdminUser{}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", input.TargetID).First(&admin).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminNotFound
			}
			return err
		}
		before := map[string]any{
			"enabled":   admin.Enabled,
			"token_ver": admin.TokenVersion,
		}
		updates := map[string]any{}
		newVersion := admin.TokenVersion
		if input.Enabled != nil && *input.Enabled != admin.Enabled {
			updates["enabled"] = *input.Enabled
			admin.Enabled = *input.Enabled
			if !*input.Enabled {
				newVersion++
				updates["token_version"] = newVersion
				admin.TokenVersion = newVersion
			}
		}
		if len(updates) > 0 {
			if err := tx.WithContext(tx.Statement.Context).
				Model(&database.AdminUser{}).
				Where("id = ?", admin.ID).
				Updates(updates).Error; err != nil {
				return err
			}
		}
		roleName := adminRoleName(tx, admin.RoleID)
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &input.ActorAdminID,
			ActorRole:    input.ActorRole,
			Action:       "admin.update",
			TargetType:   "admin",
			TargetID:     &admin.ID,
			Result:       "success",
			BeforeData:   before,
			AfterData: map[string]any{
				"enabled":   admin.Enabled,
				"token_ver": admin.TokenVersion,
				"role_name": roleName,
				"actor":     input.ActorRole,
			},
		}); err != nil {
			return err
		}
		result = UpdateAdminResult{
			AdminID:      admin.ID,
			Username:     admin.Username,
			Enabled:      admin.Enabled,
			RoleName:     roleName,
			TokenVersion: admin.TokenVersion,
		}
		return nil
	})
	if err != nil {
		return UpdateAdminResult{}, err
	}
	return result, nil
}

// AssignRoleInput is the controlled input path of AssignRole.
type AssignRoleInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	TargetID     uid.ID
	NewRoleName  string
}

// AssignRoleResult is the observable result of a successful role change.
type AssignRoleResult struct {
	AdminID  uid.ID
	Username string
	RoleName string
}

// AssignRole assigns a new role to the target administrator. The role name
// must exist; the new role's permissions must all be in the seed list.
func (s *Service) AssignRole(ctx context.Context, input AssignRoleInput) (AssignRoleResult, error) {
	if s == nil || s.db == nil {
		return AssignRoleResult{}, ErrServiceUnavailable
	}
	if uid.IsZero(input.ActorAdminID) || uid.IsZero(input.TargetID) {
		return AssignRoleResult{}, ErrAdminNotFound
	}
	if strings.TrimSpace(input.NewRoleName) == "" {
		return AssignRoleResult{}, ErrUnknownRole
	}

	var result AssignRoleResult
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		admin := database.AdminUser{}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", input.TargetID).First(&admin).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminNotFound
			}
			return err
		}
		oldRoleName := adminRoleName(tx, admin.RoleID)
		var role database.Role
		if err := tx.WithContext(tx.Statement.Context).
			Where("name = ?", input.NewRoleName).
			First(&role).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUnknownRole
			}
			return err
		}
		if err := ensureAllPermissionsAreSeeded(tx, role.ID); err != nil {
			return err
		}
		if err := tx.WithContext(tx.Statement.Context).
			Model(&database.AdminUser{}).
			Where("id = ?", admin.ID).
			Update("role_id", role.ID).Error; err != nil {
			return err
		}
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &input.ActorAdminID,
			ActorRole:    input.ActorRole,
			Action:       "admin.role.change",
			TargetType:   "admin",
			TargetID:     &admin.ID,
			Result:       "success",
			BeforeData: map[string]any{
				"role_name": oldRoleName,
			},
			AfterData: map[string]any{
				"role_name": role.Name,
				"actor":     input.ActorRole,
			},
		}); err != nil {
			return err
		}
		result = AssignRoleResult{
			AdminID:  admin.ID,
			Username: admin.Username,
			RoleName: role.Name,
		}
		return nil
	})
	if err != nil {
		return AssignRoleResult{}, err
	}
	return result, nil
}

// PermissionsForAdmin returns the seed permission codes for the target
// administrator's current role. The caller MUST resolve permissions on every
// authorization decision; permissions are not embedded in the JWT. Unknown
// permissions are rejected so a future migration that drops a seed
// permission cannot silently grant access.
func (s *Service) PermissionsForAdmin(ctx context.Context, adminID uid.ID) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, ErrServiceUnavailable
	}
	if uid.IsZero(adminID) {
		return nil, ErrAdminNotFound
	}
	admin := database.AdminUser{}
	if err := s.db.WithContext(ctx).Where("id = ?", adminID).First(&admin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	return rolePermissions(s.db, ctx, admin.RoleID)
}

// rolePermissions resolves the permission codes for a role from the supplied
// connection. Unknown permission codes are rejected so a future migration that
// drops a seed permission cannot silently grant access. The query is shared by
// the permission gate, the login/me role projection and the roles/admin-users
// list endpoints so every code path sees the same permission set.
func rolePermissions(db *gorm.DB, ctx context.Context, roleID uid.ID) ([]string, error) {
	if db == nil {
		return nil, ErrServiceUnavailable
	}
	var rows []struct {
		Code string
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT p.code FROM permissions p JOIN role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id = ? ORDER BY p.id`, roleID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if _, ok := seededPermissions[row.Code]; !ok {
			// errors.Join keeps both sentinels surfaces: admin callers keep
			// matching admin.ErrUnknownPermission while the middleware
			// permission check can detect the same drift via
			// middleware.ErrUnknownPermission and answer the contract's 422.
			return nil, errors.Join(middleware.ErrUnknownPermission, fmt.Errorf("%w: %s", ErrUnknownPermission, row.Code))
		}
		if _, ok := seen[row.Code]; ok {
			continue
		}
		seen[row.Code] = struct{}{}
		out = append(out, row.Code)
	}
	return out, nil
}

// AdminView is the read-only projection returned by FindByID.
type AdminView struct {
	AdminID      uid.ID
	Username     string
	RoleID       uid.ID
	RoleName     string
	Permissions  []string
	Enabled      bool
	TokenVersion int64
}

// FindByID returns the read-only projection of the administrator identified
// by id, including the role object's permission codes. Used by the handler to
// render /auth/me responses after middleware verification.
func (s *Service) FindByID(ctx context.Context, adminID uid.ID) (AdminView, error) {
	if s == nil || s.db == nil {
		return AdminView{}, ErrServiceUnavailable
	}
	admin := database.AdminUser{}
	if err := s.db.WithContext(ctx).Preload("Role").Where("id = ?", adminID).First(&admin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminView{}, ErrAdminNotFound
		}
		return AdminView{}, err
	}
	permissions, err := rolePermissions(s.db, ctx, admin.RoleID)
	if err != nil {
		return AdminView{}, err
	}
	return AdminView{
		AdminID:      admin.ID,
		Username:     admin.Username,
		RoleID:       admin.RoleID,
		RoleName:     adminRoleNameForRead(&admin),
		Permissions:  permissions,
		Enabled:      admin.Enabled,
		TokenVersion: admin.TokenVersion,
	}, nil
}

// RoleView is the read-only projection of a role including its permission
// codes. It backs the contract's Role schema on GET /api/admin/v1/roles.
type RoleView struct {
	ID          uid.ID
	Name        string
	Permissions []string
}

// ListRoles returns every role with its permission codes ordered by id.
func (s *Service) ListRoles(ctx context.Context) ([]RoleView, error) {
	if s == nil || s.db == nil {
		return nil, ErrServiceUnavailable
	}
	var roles []database.Role
	if err := s.db.WithContext(ctx).Order("id").Find(&roles).Error; err != nil {
		return nil, err
	}
	out := make([]RoleView, 0, len(roles))
	for _, role := range roles {
		permissions, err := rolePermissions(s.db, ctx, role.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, RoleView{ID: role.ID, Name: role.Name, Permissions: permissions})
	}
	return out, nil
}

// CreateRoleInput is the controlled input path of CreateRole. Permissions must
// be a subset of the seed list; the role name must be unique.
type CreateRoleInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	Name         string
	Permissions  []string
}

// CreateRoleResult is the observable result of a successful role creation.
type CreateRoleResult struct {
	ID          uid.ID
	Name        string
	Permissions []string
}

// CreateRole inserts a new role, attaches its permission codes and writes an
// audit log row. The permission codes are validated against the seed list so a
// request can never plant a code that a future migration dropped. A duplicate
// name is rejected with ErrRoleExists.
func (s *Service) CreateRole(ctx context.Context, input CreateRoleInput) (CreateRoleResult, error) {
	var result CreateRoleResult
	if s == nil || s.db == nil {
		return result, ErrServiceUnavailable
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 32 {
		return result, ErrInvalidRoleName
	}
	permissions, err := normalizePermissionCodes(input.Permissions)
	if err != nil {
		return result, err
	}
	err = database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		role := database.Role{Name: name, Remark: ""}
		if err := tx.WithContext(tx.Statement.Context).Create(&role).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrRoleExists
			}
			return err
		}
		if err := replaceRolePermissions(tx, role.ID, permissions); err != nil {
			return err
		}
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &input.ActorAdminID,
			ActorRole:    input.ActorRole,
			Action:       "role.create",
			TargetType:   "role",
			TargetID:     &role.ID,
			Result:       "success",
			AfterData: map[string]any{
				"name":        role.Name,
				"permissions": permissions,
			},
		}); err != nil {
			return err
		}
		result = CreateRoleResult{ID: role.ID, Name: role.Name, Permissions: permissions}
		return nil
	})
	if err != nil {
		return CreateRoleResult{}, err
	}
	return result, nil
}

// UpdateRoleInput is the controlled input path of UpdateRole. Name and
// Permissions are optional; at least one must be supplied. Permission codes
// are replaced wholesale when Permissions is non-nil.
type UpdateRoleInput struct {
	ActorAdminID uid.ID
	ActorRole    string
	RoleID       uid.ID
	Name         *string
	Permissions  *[]string
}

// UpdateRoleResult is the observable result of a successful role update.
type UpdateRoleResult struct {
	ID          uid.ID
	Name        string
	Permissions []string
}

// UpdateRole renames and/or replaces the permission codes of an existing role
// inside a single transaction with one audit row. A rename that collides with
// another role is rejected with ErrRoleExists; a missing role is rejected with
// ErrRoleNotFound.
func (s *Service) UpdateRole(ctx context.Context, input UpdateRoleInput) (UpdateRoleResult, error) {
	var result UpdateRoleResult
	if s == nil || s.db == nil {
		return result, ErrServiceUnavailable
	}
	if input.Name == nil && input.Permissions == nil {
		return result, ErrRoleUpdateEmpty
	}
	if uid.IsZero(input.RoleID) {
		return result, ErrRoleNotFound
	}
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		role := database.Role{}
		if err := tx.WithContext(tx.Statement.Context).Where("id = ?", input.RoleID).First(&role).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRoleNotFound
			}
			return err
		}
		beforePermissions, err := rolePermissions(tx, tx.Statement.Context, role.ID)
		if err != nil {
			return err
		}
		var targetName string
		var targetPermissions []string
		if input.Name != nil {
			targetName = strings.TrimSpace(*input.Name)
			if targetName == "" || len(targetName) > 32 {
				return ErrInvalidRoleName
			}
			var collision int64
			if err := tx.WithContext(tx.Statement.Context).
				Model(&database.Role{}).
				Where("name = ? AND id <> ?", targetName, role.ID).
				Count(&collision).Error; err != nil {
				return err
			}
			if collision > 0 {
				return ErrRoleExists
			}
		} else {
			targetName = role.Name
		}
		if input.Permissions != nil {
			targetPermissions, err = normalizePermissionCodes(*input.Permissions)
			if err != nil {
				return err
			}
		} else {
			targetPermissions = beforePermissions
		}
		updates := map[string]any{}
		if targetName != role.Name {
			updates["name"] = targetName
		}
		if len(updates) > 0 {
			if err := tx.WithContext(tx.Statement.Context).
				Model(&database.Role{}).
				Where("id = ?", role.ID).
				Updates(updates).Error; err != nil {
				if isUniqueViolation(err) {
					return ErrRoleExists
				}
				return err
			}
		}
		if input.Permissions != nil {
			if err := replaceRolePermissions(tx, role.ID, targetPermissions); err != nil {
				return err
			}
		}
		if err := writeAuditLog(tx, auditEntry{
			ActorAdminID: &input.ActorAdminID,
			ActorRole:    input.ActorRole,
			Action:       "role.update",
			TargetType:   "role",
			TargetID:     &role.ID,
			Result:       "success",
			BeforeData: map[string]any{
				"name":        role.Name,
				"permissions": beforePermissions,
			},
			AfterData: map[string]any{
				"name":        targetName,
				"permissions": targetPermissions,
			},
		}); err != nil {
			return err
		}
		result = UpdateRoleResult{ID: role.ID, Name: targetName, Permissions: targetPermissions}
		return nil
	})
	if err != nil {
		return UpdateRoleResult{}, err
	}
	return result, nil
}

// AdminUserView is the read-only projection of an administrator account with
// its role object. It backs the contract's AdminUser schema on
// GET /api/admin/v1/admin-users.
type AdminUserView struct {
	ID          uid.ID
	Username    string
	Enabled     bool
	RoleID      uid.ID
	RoleName    string
	Permissions []string
}

// ListAdminUsers returns a page of administrator accounts ordered by id, each
// carrying its role and the role's permission codes. total is the full count
// across pages.
func (s *Service) ListAdminUsers(ctx context.Context, page, pageSize int) ([]AdminUserView, int64, error) {
	if s == nil || s.db == nil {
		return nil, 0, ErrServiceUnavailable
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&database.AdminUser{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var admins []database.AdminUser
	if err := s.db.WithContext(ctx).Preload("Role").
		Order("id").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&admins).Error; err != nil {
		return nil, 0, err
	}
	out := make([]AdminUserView, 0, len(admins))
	for _, admin := range admins {
		permissions, err := rolePermissions(s.db, ctx, admin.RoleID)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, AdminUserView{
			ID:          admin.ID,
			Username:    admin.Username,
			Enabled:     admin.Enabled,
			RoleID:      admin.RoleID,
			RoleName:    adminRoleNameForRead(&admin),
			Permissions: permissions,
		})
	}
	return out, total, nil
}

// RoleNameByID resolves the role name for a role id. The admin-user update
// contract carries role_id instead of a role name, so the handler resolves the
// name through this lookup before delegating to AssignRole.
func (s *Service) RoleNameByID(ctx context.Context, roleID uid.ID) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrServiceUnavailable
	}
	if uid.IsZero(roleID) {
		return "", ErrUnknownRole
	}
	var role database.Role
	if err := s.db.WithContext(ctx).Where("id = ?", roleID).First(&role).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrUnknownRole
		}
		return "", err
	}
	return role.Name, nil
}

// UserView is the read-only projection of a buyer account for the member
// management list behind GET /api/admin/v1/users.
type UserView struct {
	ID            uid.ID
	Nickname      string
	PointsBalance int64
	CreatedAt     time.Time
}

// ListUsers returns a page of buyer accounts ordered by id. keyword is matched
// against the uuid id when it parses as a canonical uuid, otherwise
// against the nickname as a case-insensitive substring.
func (s *Service) ListUsers(ctx context.Context, page, pageSize int, keyword string) ([]UserView, int64, error) {
	if s == nil || s.db == nil {
		return nil, 0, ErrServiceUnavailable
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	base := s.db.WithContext(ctx)
	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		if id, err := uid.ParseCanonical(keyword); err == nil {
			base = base.Where("id = ?", id)
		} else {
			base = base.Where("nickname ILIKE ?", "%"+keyword+"%")
		}
	}
	var total int64
	if err := base.Model(&database.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []database.User
	if err := base.Model(&database.User{}).
		Order("id").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error; err != nil {
		return nil, 0, err
	}
	out := make([]UserView, 0, len(users))
	for _, user := range users {
		out = append(out, UserView{
			ID:            user.ID,
			Nickname:      user.Nickname,
			PointsBalance: user.PointsBalance,
			CreatedAt:     user.CreatedAt,
		})
	}
	return out, total, nil
}

// AuditLogView is the read-only projection of an audit_logs row behind
// GET /api/admin/v1/audit-logs. The JSON columns are decoded into maps so the
// HTTP layer can echo the detail payloads.
type AuditLogView struct {
	ID           uid.ID
	ActorAdminID *uid.ID
	ActorRole    string
	Action       string
	TargetType   string
	TargetID     *uid.ID
	Result       string
	BeforeData   map[string]any
	AfterData    map[string]any
	TraceID      string
	CreatedAt    time.Time
}

// ListAuditLogs returns a page of audit logs ordered by id descending (the
// append-only table's monotonic time ordering).
func (s *Service) ListAuditLogs(ctx context.Context, page, pageSize int) ([]AuditLogView, int64, error) {
	if s == nil || s.db == nil {
		return nil, 0, ErrServiceUnavailable
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&database.AuditLog{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []database.AuditLog
	if err := s.db.WithContext(ctx).
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]AuditLogView, 0, len(rows))
	for _, row := range rows {
		before, err := decodeAuditJSON(row.BeforeData)
		if err != nil {
			return nil, 0, err
		}
		after, err := decodeAuditJSON(row.AfterData)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, AuditLogView{
			ID:           row.ID,
			ActorAdminID: row.ActorAdminID,
			ActorRole:    row.ActorRole,
			Action:       row.Action,
			TargetType:   row.TargetType,
			TargetID:     row.TargetID,
			Result:       row.Result,
			BeforeData:   before,
			AfterData:    after,
			TraceID:      row.TraceID,
			CreatedAt:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// AdminState resolves the live enabled flag and token_version for the
// administrator identified by id. The middleware's admin-authentication path
// calls this on every request so a stale token (after password change, logout
// or disable) and a disabled administrator's session are rejected before any
// route handler runs.
func (s *Service) AdminState(ctx context.Context, adminID uid.ID) (middleware.AdminState, error) {
	if s == nil || s.db == nil {
		return middleware.AdminState{}, ErrServiceUnavailable
	}
	if uid.IsZero(adminID) {
		return middleware.AdminState{}, ErrAdminNotFound
	}
	admin := database.AdminUser{}
	if err := s.db.WithContext(ctx).Where("id = ?", adminID).First(&admin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return middleware.AdminState{}, ErrAdminNotFound
		}
		return middleware.AdminState{}, err
	}
	return middleware.AdminState{Enabled: admin.Enabled, TokenVersion: admin.TokenVersion}, nil
}

// IsTokenVersionCurrent reports whether the supplied token_version matches
// the current row. The handler middleware calls this after parsing the JWT
// to enforce the "password change invalidates old JWT" contract; the signer
// alone cannot enforce this because the JWT signature remains valid until
// the configured TTL elapses.
func (s *Service) IsTokenVersionCurrent(ctx context.Context, adminID uid.ID, tokenVersion int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrServiceUnavailable
	}
	if uid.IsZero(adminID) {
		return false, ErrAdminNotFound
	}
	admin := database.AdminUser{}
	if err := s.db.WithContext(ctx).Where("id = ?", adminID).First(&admin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAdminNotFound
		}
		return false, err
	}
	return admin.TokenVersion == tokenVersion, nil
}

// recordFailedLogin writes a failure audit log row and increments the
// login-failure metric. The IP field comes from the HTTP layer; rate limiting
// lives in the LoginRateLimiter. reason is one of the bounded failure reasons
// ("invalid_credentials" or "account_disabled").
func (s *Service) recordFailedLogin(ctx context.Context, input AuthenticateInput, reason string) {
	if s == nil || s.db == nil {
		return
	}
	if s.metrics != nil {
		s.metrics.RecordLoginFailure(reason)
	}
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		return writeAuditLog(tx, auditEntry{
			Action:     "admin.login",
			TargetType: "admin",
			Result:     "failure",
			ActorRole:  "anonymous",
			AfterData: map[string]any{
				"username": input.Username,
				"ip":       input.IP,
				"reason":   reason,
			},
		})
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "record failed login", "error", err)
	}
}

// ValidatePassword enforces a strong-password policy: minimum length 12,
// upper, lower, digit and symbol. It is exported so the bootstrap command
// can reuse the same validation before writing to the database.
func ValidatePassword(value string) error {
	if len(value) < 12 {
		return fmt.Errorf("%w: shorter than 12 characters", ErrWeakPassword)
	}
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range value {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	if !hasUpper {
		return fmt.Errorf("%w: missing uppercase letter", ErrWeakPassword)
	}
	if !hasLower {
		return fmt.Errorf("%w: missing lowercase letter", ErrWeakPassword)
	}
	if !hasDigit {
		return fmt.Errorf("%w: missing digit", ErrWeakPassword)
	}
	if !hasSymbol {
		return fmt.Errorf("%w: missing symbol", ErrWeakPassword)
	}
	return nil
}

// ValidateUsername enforces the username policy: non-empty, no whitespace,
// allowed characters are letters, digits, dot, dash and underscore, length
// between 3 and 32 characters.
func ValidateUsername(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%w: blank", ErrInvalidUsername)
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return fmt.Errorf("%w: contains whitespace", ErrInvalidUsername)
	}
	if len(trimmed) < 3 || len(trimmed) > 32 {
		return fmt.Errorf("%w: length must be between 3 and 32 characters", ErrInvalidUsername)
	}
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '.', r == '-', r == '_':
		default:
			return fmt.Errorf("%w: unsupported character %q", ErrInvalidUsername, r)
		}
	}
	return nil
}

// hashPassword encodes the password using Argon2id with the parameters
// declared above. The encoded string follows the canonical
// "$argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>" format.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// verifyPassword re-encodes the password with the salt and parameters stored
// in the encoded hash and compares the result in constant time.
func verifyPassword(password, encoded string) bool {
	params, salt, key, err := decodePasswordHash(encoded)
	if err != nil {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(key)))
	return subtle.ConstantTimeCompare(candidate, key) == 1
}

type argonParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

func decodePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return argonParams{}, nil, nil, errors.New("invalid encoded hash")
	}
	if parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errors.New("unsupported hash algorithm")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, err
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, errors.New("unsupported argon2 version")
	}
	var params argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &params.Parallelism); err != nil {
		return argonParams{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	return params, salt, key, nil
}

// adminSubject is the JWT subject format used for administrator tokens. It
// matches the buyer format and stays stable across login/logout events.
func adminSubject(id uid.ID) string {
	return "admin:" + id.String()
}

// ensureAllPermissionsAreSeeded verifies that every permission assigned to
// the role is in the seed list. This is the controlled input path that
// protects against silent permission drift.
func ensureAllPermissionsAreSeeded(tx *gorm.DB, roleID uid.ID) error {
	var codes []string
	if err := tx.WithContext(tx.Statement.Context).
		Raw(`SELECT p.code FROM permissions p JOIN role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id = ?`, roleID).
		Scan(&codes).Error; err != nil {
		return err
	}
	for _, code := range codes {
		if _, ok := seededPermissions[code]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownPermission, code)
		}
	}
	return nil
}

// normalizePermissionCodes trims, deduplicates and validates a requested
// permission set against the seed list. The stable input order is preserved so
// a request cannot rely on a nondeterministic permission order.
func normalizePermissionCodes(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return []string{}, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			return nil, fmt.Errorf("%w: blank permission code", ErrUnknownPermission)
		}
		if _, ok := seededPermissions[code]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownPermission, code)
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out, nil
}

// replaceRolePermissions deletes the role's existing permission rows and
// plants the supplied codes. Codes that are not present in the permissions
// table (i.e. not part of the migration seed) are rejected so the role can
// never reference a permission that does not exist.
func replaceRolePermissions(tx *gorm.DB, roleID uid.ID, codes []string) error {
	if err := tx.WithContext(tx.Statement.Context).
		Where("role_id = ?", roleID).
		Delete(&database.RolePermission{}).Error; err != nil {
		return err
	}
	if len(codes) == 0 {
		return nil
	}
	var perms []database.Permission
	if err := tx.WithContext(tx.Statement.Context).
		Where("code IN ?", codes).
		Find(&perms).Error; err != nil {
		return err
	}
	byCode := make(map[string]uid.ID, len(perms))
	for _, perm := range perms {
		byCode[perm.Code] = perm.ID
	}
	rows := make([]database.RolePermission, 0, len(codes))
	for _, code := range codes {
		id, ok := byCode[code]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownPermission, code)
		}
		rows = append(rows, database.RolePermission{RoleID: roleID, PermissionID: id})
	}
	return tx.WithContext(tx.Statement.Context).Create(&rows).Error
}

// decodeAuditJSON decodes a nullable JSONB audit column into a map. A nil or
// empty column decodes to nil so the HTTP projection can omit the field.
func decodeAuditJSON(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// lookupRoleName resolves the role name for an admin row in a transaction
// when the Role preload was not requested.
func lookupRoleName(tx *gorm.DB, roleID uid.ID) string {
	if uid.IsZero(roleID) {
		return ""
	}
	role := database.Role{}
	if err := tx.WithContext(tx.Statement.Context).Where("id = ?", roleID).First(&role).Error; err != nil {
		return ""
	}
	return role.Name
}

// adminRoleName is the in-transaction variant used by audit log writers.
func adminRoleName(tx *gorm.DB, roleID uid.ID) string {
	if uid.IsZero(roleID) {
		return ""
	}
	role := database.Role{}
	if err := tx.WithContext(tx.Statement.Context).Where("id = ?", roleID).First(&role).Error; err != nil {
		return ""
	}
	return role.Name
}

// adminRoleNameForRead is the read-only variant that uses the already
// preloaded Role on the admin row.
func adminRoleNameForRead(admin *database.AdminUser) string {
	if admin == nil {
		return ""
	}
	if admin.Role.Name != "" {
		return admin.Role.Name
	}
	return ""
}

// seededPermissions is the closed set of permission codes that the role
// migration 0002_seed.up.sql plants. Assignments outside this list are
// rejected at the usecase boundary.
var seededPermissions = map[string]struct{}{
	"product:read":   {},
	"product:write":  {},
	"image:write":    {},
	"order:read":     {},
	"order:ship":     {},
	"refund:read":    {},
	"refund:approve": {},
	"points:adjust":  {},
	"role:manage":    {},
	"admin:self":     {},
	"user:read":      {},
	"audit:read":     {},
}

// auditEntry is the structured input to writeAuditLog. The row itself is
// written through the shared platform/audit facility; the alias keeps the
// admin-side construction vocabulary unchanged.
type auditEntry = audit.Entry

// writeAuditLog appends an audit_logs row inside the caller's transaction
// through platform/audit.
func writeAuditLog(tx *gorm.DB, entry auditEntry) error {
	return audit.Write(tx, entry)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key value")
}
