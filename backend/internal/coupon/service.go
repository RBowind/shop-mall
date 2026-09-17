// Package coupon owns the marketing domain: coupon templates administered by
// the back office and, in later slices, the coupons buyers receive from them
// (docs/architecture/07-coupon-pay-lifecycle.md §3, §5).
package coupon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/uid"

	"gorm.io/gorm"
)

// ErrInvalidTemplate is a rule-set violation on a coupon template write: a
// value outside the constraints techspec 07 §3 states for coupon_templates.
// The handler reports it as the spec's 1001 参数错误 rather than a 500.
var ErrInvalidTemplate = errors.New("invalid coupon template")

// ErrTemplateNotFound reports that the addressed coupon template row does not
// exist. The handler maps it to 404, the same shape the product admin PATCH
// uses for a missing resource.
var ErrTemplateNotFound = errors.New("coupon template not found")

// Status is the template issuance status. The row-level enum lives with the
// table model; this domain alias is the only status vocabulary the handler and
// service layers use, so the HTTP surface never imports the database package.
type Status = database.CouponTemplateStatus

const (
	StatusActive = database.CouponTemplateStatusActive
	StatusHalted = database.CouponTemplateStatusHalted
)

// Actor is the authorized administrator carrying out a coupon template write.
// The handler resolves it from the verified admin JWT.
type Actor struct {
	AdminID  uid.ID
	RoleName string
}

// TemplateView is the read projection of a coupon template.
type TemplateView struct {
	ID              uid.ID
	Name            string
	ThresholdPoints int64
	DiscountPoints  int64
	TotalCount      int32
	PerUserLimit    int32
	ReceivedCount   int32
	ValidFrom       time.Time
	ValidUntil      time.Time
	Status          Status
}

// TemplateInput is the validated create payload: the rule set plus the validity
// window. The rules are frozen once the template exists.
type TemplateInput struct {
	Name            string
	ThresholdPoints int64
	DiscountPoints  int64
	TotalCount      int32
	PerUserLimit    int32
	ValidFrom       time.Time
	ValidUntil      time.Time
}

// Service is the application-layer gateway over the coupon_templates table. It
// owns the template business rules and the audit rows.
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
		return nil, errors.New("coupon service: database is required")
	}
	return &Service{db: deps.DB}, nil
}

// CreateTemplate inserts a template in issuing state with nobody having
// received it yet, and writes a coupon_template.create audit row in the same
// transaction, so a recorded creation always has a template behind it.
//
// The rule set is screened before any write: a rejected submission must leave
// neither a template row nor an audit row behind (specs/coupon/spec.md 规则非法).
func (s *Service) CreateTemplate(ctx context.Context, actor Actor, input TemplateInput) (TemplateView, error) {
	if err := validateTemplateRules(input); err != nil {
		return TemplateView{}, err
	}
	template := database.CouponTemplate{
		Name:            strings.TrimSpace(input.Name),
		ThresholdPoints: input.ThresholdPoints,
		DiscountPoints:  input.DiscountPoints,
		TotalCount:      input.TotalCount,
		PerUserLimit:    input.PerUserLimit,
		// The initial status and counter are written explicitly rather than
		// left to the column defaults: "created means issuing with zero
		// received" is spec behavior (specs/coupon/spec.md 创建成功), not a
		// storage detail.
		ReceivedCount: 0,
		ValidFrom:     input.ValidFrom,
		ValidUntil:    input.ValidUntil,
		Status:        StatusActive,
	}
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		if err := tx.WithContext(tx.Statement.Context).Create(&template).Error; err != nil {
			return err
		}
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &actor.AdminID,
			ActorRole:    actor.RoleName,
			Action:       "coupon_template.create",
			TargetType:   "coupon_template",
			TargetID:     &template.ID,
			Result:       "success",
			AfterData:    templateAuditData(&template),
		})
	})
	if err != nil {
		return TemplateView{}, err
	}
	return toTemplateView(template), nil
}

// SetTemplateStatus switches an existing template between issuing (active) and
// halted (specs/coupon/spec.md 切换发放状态). It is the only write path into an
// existing template row, and the update names the status column alone: the rule
// set and the validity window are frozen at creation (specs/coupon/spec.md
// 规则创建后不可修改), so no rule value ever reaches this statement.
func (s *Service) SetTemplateStatus(ctx context.Context, id uid.ID, status Status) (TemplateView, error) {
	if uid.IsZero(id) {
		return TemplateView{}, ErrTemplateNotFound
	}
	if !isIssuanceStatus(status) {
		return TemplateView{}, fmt.Errorf("%w: unknown status %q", ErrInvalidTemplate, status)
	}
	var updated database.CouponTemplate
	err := database.RunTransaction(ctx, s.db, func(tx *gorm.DB) error {
		// RowsAffected is the existence check: the WHERE clause carries the id
		// alone, so zero affected rows means the template is gone.
		result := tx.WithContext(tx.Statement.Context).
			Model(&database.CouponTemplate{}).
			Where("id = ?", id).
			Update("status", status)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTemplateNotFound
		}
		return tx.WithContext(tx.Statement.Context).Where("id = ?", id).First(&updated).Error
	})
	if err != nil {
		return TemplateView{}, err
	}
	return toTemplateView(updated), nil
}

// isIssuanceStatus reports whether a value is an issuance status a template may
// hold. The column's CHECK constraint remains the storage-level backstop; this
// layer turns an unknown value into the spec's 1001 参数错误 instead of a 500.
func isIssuanceStatus(status Status) bool {
	return status == StatusActive || status == StatusHalted
}

// validateTemplateRules enforces the create-time rule constraints from
// techspec 07 §3: threshold and discount are positive integers, the discount
// stays strictly below the threshold (so a discounted order always pays more
// than zero, §3 决策性约束), total and per-user limit are positive integers, and
// the validity window is non-empty. The matching CHECK constraints on
// coupon_templates remain the storage-level backstop; this layer is the fast
// fail the 1001 response is reported from.
func validateTemplateRules(input TemplateInput) error {
	if input.ThresholdPoints <= 0 {
		return fmt.Errorf("%w: threshold_points must be a positive integer", ErrInvalidTemplate)
	}
	if input.DiscountPoints <= 0 {
		return fmt.Errorf("%w: discount_points must be a positive integer", ErrInvalidTemplate)
	}
	if input.DiscountPoints >= input.ThresholdPoints {
		return fmt.Errorf("%w: discount_points must be less than threshold_points", ErrInvalidTemplate)
	}
	if input.TotalCount <= 0 {
		return fmt.Errorf("%w: total_count must be a positive integer", ErrInvalidTemplate)
	}
	if input.PerUserLimit <= 0 {
		return fmt.Errorf("%w: per_user_limit must be a positive integer", ErrInvalidTemplate)
	}
	if !input.ValidUntil.After(input.ValidFrom) {
		return fmt.Errorf("%w: valid_until must be later than valid_from", ErrInvalidTemplate)
	}
	return nil
}

func toTemplateView(template database.CouponTemplate) TemplateView {
	return TemplateView{
		ID:              template.ID,
		Name:            template.Name,
		ThresholdPoints: template.ThresholdPoints,
		DiscountPoints:  template.DiscountPoints,
		TotalCount:      template.TotalCount,
		PerUserLimit:    template.PerUserLimit,
		ReceivedCount:   template.ReceivedCount,
		ValidFrom:       template.ValidFrom,
		ValidUntil:      template.ValidUntil,
		Status:          template.Status,
	}
}

// templateAuditData is the after-image of a template creation. It carries the
// rule set so the audit trail alone answers "which rules were frozen here".
func templateAuditData(template *database.CouponTemplate) map[string]any {
	if template == nil {
		return nil
	}
	return map[string]any{
		"name":             template.Name,
		"threshold_points": template.ThresholdPoints,
		"discount_points":  template.DiscountPoints,
		"total_count":      template.TotalCount,
		"per_user_limit":   template.PerUserLimit,
		"valid_from":       template.ValidFrom.UTC().Format(time.RFC3339),
		"valid_until":      template.ValidUntil.UTC().Format(time.RFC3339),
		"status":           string(template.Status),
	}
}
