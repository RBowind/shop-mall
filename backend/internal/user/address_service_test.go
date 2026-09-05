package user_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type addressServiceFixture struct {
	db      *gorm.DB
	service *user.AddressService
}

func newAddressServiceFixture(t *testing.T) *addressServiceFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	service, err := user.NewAddressService(user.AddressServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("new address service: %v", err)
	}
	return &addressServiceFixture{db: db, service: service}
}

func (f *addressServiceFixture) createUser(t *testing.T, openid string) uid.ID {
	t.Helper()
	if err := f.db.Exec(`INSERT INTO users (id, openid) VALUES (?, ?)`, uid.New(), openid).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *addressServiceFixture) countDefaults(t *testing.T, userID uid.ID) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM user_addresses WHERE user_id = ? AND is_default = true`, userID).Scan(&count).Error; err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	return count
}

func TestAddressServiceRejectsInvalidFields(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-svc-validation")
	ctx := context.Background()
	valid := func() user.AddressInput {
		return user.AddressInput{Receiver: "R", Phone: "13800138000", Region: "北京市", Detail: "某地址"}
	}
	mutators := []struct {
		name string
		mut  func(*user.AddressInput)
	}{
		{"blank receiver", func(in *user.AddressInput) { in.Receiver = "  " }},
		{"receiver too long", func(in *user.AddressInput) { in.Receiver = string(make([]rune, 33)) }},
		{"phone too short", func(in *user.AddressInput) { in.Phone = "12345" }},
		{"phone too long", func(in *user.AddressInput) { in.Phone = string(make([]rune, 21)) }},
		{"blank region", func(in *user.AddressInput) { in.Region = "" }},
		{"blank detail", func(in *user.AddressInput) { in.Detail = "" }},
	}
	for _, tc := range mutators {
		t.Run(tc.name, func(t *testing.T) {
			in := valid()
			tc.mut(&in)
			_, err := fix.service.Create(ctx, userID, in)
			if !errors.Is(err, user.ErrInvalidAddress) {
				t.Fatalf("error = %v, want ErrInvalidAddress", err)
			}
		})
	}
}

func TestAddressServiceOwnershipMasksOtherUsersAddress(t *testing.T) {
	fix := newAddressServiceFixture(t)
	owner := fix.createUser(t, "openid-svc-owner")
	other := fix.createUser(t, "openid-svc-other")
	ctx := context.Background()
	view, err := fix.service.Create(ctx, owner, user.AddressInput{
		Receiver: "Owner", Phone: "13800138000", Region: "北京市", Detail: "某地址",
	})
	if err != nil {
		t.Fatalf("create owner address: %v", err)
	}
	if _, err := fix.service.Get(ctx, other, view.ID); !errors.Is(err, user.ErrAddressNotFound) {
		t.Fatalf("Get by other user error = %v, want ErrAddressNotFound", err)
	}
	if _, err := fix.service.Update(ctx, other, view.ID, user.AddressUpdate{Receiver: strValue("Hijack")}); !errors.Is(err, user.ErrAddressNotFound) {
		t.Fatalf("Update by other user error = %v, want ErrAddressNotFound", err)
	}
	if err := fix.service.Delete(ctx, other, view.ID); !errors.Is(err, user.ErrAddressNotFound) {
		t.Fatalf("Delete by other user error = %v, want ErrAddressNotFound", err)
	}
}

func TestAddressServiceDefaultReplacementUnderTransaction(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-svc-default")
	ctx := context.Background()
	first, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "First", Phone: "13800138000", Region: "北京市", Detail: "甲", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("create first default: %v", err)
	}
	second, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Second", Phone: "13800138000", Region: "北京市", Detail: "乙", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("create second default: %v", err)
	}
	if second.Version != 1 {
		t.Fatalf("second default version = %d, want 1", second.Version)
	}
	if fix.countDefaults(t, userID) != 1 {
		t.Fatalf("default count = %d, want 1", fix.countDefaults(t, userID))
	}
	firstNow, err := fix.service.Get(ctx, userID, first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstNow.IsDefault {
		t.Fatal("first address still default after replacement")
	}
}

func TestAddressServiceUpdateBumpsVersionAndPromotesDefault(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-svc-promote")
	ctx := context.Background()
	first, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "First", Phone: "13800138000", Region: "北京市", Detail: "甲",
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Second", Phone: "13800138000", Region: "北京市", Detail: "乙",
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	promoted, err := fix.service.Update(ctx, userID, second.ID, user.AddressUpdate{IsDefault: boolValue(true)})
	if err != nil {
		t.Fatalf("promote second: %v", err)
	}
	if !promoted.IsDefault {
		t.Fatal("second not default after promotion")
	}
	if promoted.Version != second.Version+1 {
		t.Fatalf("promoted version = %d, want %d", promoted.Version, second.Version+1)
	}
	if fix.countDefaults(t, userID) != 1 {
		t.Fatalf("default count = %d, want 1", fix.countDefaults(t, userID))
	}
	firstNow, err := fix.service.Get(ctx, userID, first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstNow.IsDefault {
		t.Fatal("first still default after promotion of second")
	}
}

func TestAddressServiceEmptyUpdateIsRejected(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-svc-empty")
	ctx := context.Background()
	view, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Stable", Phone: "13800138000", Region: "北京市", Detail: "甲",
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	if _, err := fix.service.Update(ctx, userID, view.ID, user.AddressUpdate{}); !errors.Is(err, user.ErrEmptyUpdate) {
		t.Fatalf("empty update error = %v, want ErrEmptyUpdate", err)
	}
}

func TestAddressServiceCreateSetsVersionOne(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-version-one")
	ctx := context.Background()
	view, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Receiver One", Phone: "13800138000", Region: "北京市朝阳区", Detail: "建国路 88 号",
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	if uid.IsZero(view.ID) {
		t.Fatal("created address has zero id")
	}
	if view.Version != 1 {
		t.Fatalf("version = %d, want 1", view.Version)
	}
	if view.IsDefault {
		t.Fatal("non-default address reported as default")
	}
}

func TestAddressServiceDefaultAddressRaceKeepsSingleDefault(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-default-race")
	ctx := context.Background()
	// Seed an initial default so the concurrent creates are replacements.
	if _, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Seed Default", Phone: "13800138000", Region: "北京市", Detail: "甲", IsDefault: true,
	}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = fix.service.Create(ctx, userID, user.AddressInput{
				Receiver: fmt.Sprintf("Racer %d", idx), Phone: "13800138000", Region: "北京市", Detail: "乙", IsDefault: true,
			})
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil && !errors.Is(err, user.ErrDefaultAddressConflict) {
			t.Fatalf("goroutine %d unexpected error: %v", i, err)
		}
	}
	if fix.countDefaults(t, userID) != 1 {
		t.Fatalf("default count after race = %d, want 1", fix.countDefaults(t, userID))
	}
}

func TestAddressServiceFirstDefaultRaceNeverLeavesTwoDefaults(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-first-default-race")
	ctx := context.Background()
	const goroutines = 6
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = fix.service.Create(ctx, userID, user.AddressInput{
				Receiver: fmt.Sprintf("First %d", idx), Phone: "13800138000", Region: "北京市", Detail: "甲", IsDefault: true,
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && !errors.Is(err, user.ErrDefaultAddressConflict) {
			t.Fatalf("goroutine %d unexpected error: %v", i, err)
		}
	}
	if fix.countDefaults(t, userID) != 1 {
		t.Fatalf("default count after first-default race = %d, want 1", fix.countDefaults(t, userID))
	}
}

func TestAddressServiceUpdateIncrementsVersionWithFieldChanges(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-version-bump")
	ctx := context.Background()
	view, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "Before", Phone: "13800138000", Region: "北京市朝阳区", Detail: "建国路 88 号",
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	updated, err := fix.service.Update(ctx, userID, view.ID, user.AddressUpdate{
		Receiver: strValue("After"),
		Region:   strValue("上海市浦东新区"),
	})
	if err != nil {
		t.Fatalf("update address: %v", err)
	}
	if updated.Version != view.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, view.Version+1)
	}
	if updated.Receiver != "After" {
		t.Fatalf("receiver = %q, want After", updated.Receiver)
	}
	second, err := fix.service.Update(ctx, userID, view.ID, user.AddressUpdate{
		Region: strValue("广东省深圳市"),
	})
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if second.Version != updated.Version+1 {
		t.Fatalf("version after second update = %d, want %d", second.Version, updated.Version+1)
	}
}

func TestAddressServiceDeleteOwnedAddressTwice(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userID := fix.createUser(t, "openid-delete")
	ctx := context.Background()
	view, err := fix.service.Create(ctx, userID, user.AddressInput{
		Receiver: "To Delete", Phone: "13800138000", Region: "北京市", Detail: "甲",
	})
	if err != nil {
		t.Fatalf("create address: %v", err)
	}
	if err := fix.service.Delete(ctx, userID, view.ID); err != nil {
		t.Fatalf("delete address: %v", err)
	}
	if err := fix.service.Delete(ctx, userID, view.ID); !errors.Is(err, user.ErrAddressNotFound) {
		t.Fatalf("second delete error = %v, want ErrAddressNotFound", err)
	}
	if _, err := fix.service.Get(ctx, userID, view.ID); !errors.Is(err, user.ErrAddressNotFound) {
		t.Fatalf("get after delete error = %v, want ErrAddressNotFound", err)
	}
}

func TestAddressServiceListReturnsOnlyOwners(t *testing.T) {
	fix := newAddressServiceFixture(t)
	userA := fix.createUser(t, "openid-list-a")
	userB := fix.createUser(t, "openid-list-b")
	ctx := context.Background()
	if _, err := fix.service.Create(ctx, userA, user.AddressInput{
		Receiver: "A One", Phone: "13800138000", Region: "北京市", Detail: "甲",
	}); err != nil {
		t.Fatalf("create A1: %v", err)
	}
	if _, err := fix.service.Create(ctx, userA, user.AddressInput{
		Receiver: "A Two", Phone: "13800138000", Region: "北京市", Detail: "乙", IsDefault: true,
	}); err != nil {
		t.Fatalf("create A2: %v", err)
	}
	if _, err := fix.service.Create(ctx, userB, user.AddressInput{
		Receiver: "B One", Phone: "13800138000", Region: "北京市", Detail: "甲",
	}); err != nil {
		t.Fatalf("create B1: %v", err)
	}
	listA, err := fix.service.List(ctx, userA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("A list length = %d, want 2", len(listA))
	}
	listB, err := fix.service.List(ctx, userB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 1 {
		t.Fatalf("B list length = %d, want 1", len(listB))
	}
}

func strValue(value string) *string { return &value }
func boolValue(value bool) *bool    { return &value }
