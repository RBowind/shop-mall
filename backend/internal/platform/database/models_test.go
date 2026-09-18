package database

import (
	"testing"

	"shop-mall/backend/internal/platform/uid"
)

// TestCouponTemplateModelContract pins the two pieces of the coupon_templates
// row model the domain depends on: the table name the service queries, and the
// UUIDv7 primary-key fill GORM runs before INSERT (the column has no database
// default, so a model that skipped the hook would fail the insert).
func TestCouponTemplateModelContract(t *testing.T) {
	if got := (CouponTemplate{}).TableName(); got != "coupon_templates" {
		t.Fatalf("table name = %q, want coupon_templates", got)
	}

	fresh := CouponTemplate{}
	if err := fresh.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate on a zero id: %v", err)
	}
	if uid.IsZero(fresh.ID) {
		t.Fatal("BeforeCreate left the primary key unset")
	}
	if version := fresh.ID.Version(); version != 7 {
		t.Fatalf("generated id version = %d, want 7 (UUIDv7 keeps id DESC time-ordered)", version)
	}

	// A caller that already carries an id (replay or composite paths) keeps it.
	preset := uid.New()
	kept := CouponTemplate{ID: preset}
	if err := kept.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate on a preset id: %v", err)
	}
	if kept.ID != preset {
		t.Fatalf("BeforeCreate replaced the preset id %s with %s", preset, kept.ID)
	}
}

// TestCouponTemplateStatusVocabulary pins the two issuance statuses the column
// CHECK constraint allows; the handler's request decoder accepts exactly these
// strings.
func TestCouponTemplateStatusVocabulary(t *testing.T) {
	if string(CouponTemplateStatusActive) != "active" {
		t.Fatalf("active status = %q, want active", CouponTemplateStatusActive)
	}
	if string(CouponTemplateStatusHalted) != "halted" {
		t.Fatalf("halted status = %q, want halted", CouponTemplateStatusHalted)
	}
}
