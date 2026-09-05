package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryableTransactionErrorClassifiesPostgresSerializationFailures(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "deadlock", code: "40P01", want: true},
		{name: "serialization failure", code: "40001", want: true},
		{name: "unique violation", code: "23505", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: tt.code})
			if got := IsRetryableTransactionError(err); got != tt.want {
				t.Fatalf("IsRetryableTransactionError(%s) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsRetryableTransactionErrorRejectsNonPostgresErrors(t *testing.T) {
	if IsRetryableTransactionError(errors.New("deadlock detected")) {
		t.Fatal("generic error was classified as retryable")
	}
}

func TestRunMigrationsAppliesForwardMigrations(t *testing.T) {
	db := openMigrationDatabase(t)
	if err := RunMigrations(context.Background(), db, filepath.Join(backendRoot(t), "migrations")); err != nil {
		t.Fatalf("run forward migrations: %v", err)
	}

	var version int64
	if err := db.Raw("SELECT version_id FROM goose_db_version WHERE is_applied = true ORDER BY version_id DESC LIMIT 1").Scan(&version).Error; err != nil {
		t.Fatalf("read goose migration version: %v", err)
	}
	if version != 5 {
		t.Fatalf("latest migration version = %d, want 5", version)
	}
}
