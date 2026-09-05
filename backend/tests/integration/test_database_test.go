package integration

import (
	"testing"
)

func TestOpenTestDatabaseUsesConfiguredIsolatedDatabase(t *testing.T) {
	db := OpenTestDatabase(t)
	if db == nil {
		t.Fatal("OpenTestDatabase() returned nil")
	}
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("test database is not ready: %v", err)
	}
}
