package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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

	wantVersion := latestMigrationVersion(t)

	var version int64
	if err := db.Raw("SELECT version_id FROM goose_db_version WHERE is_applied = true ORDER BY version_id DESC LIMIT 1").Scan(&version).Error; err != nil {
		t.Fatalf("read goose migration version: %v", err)
	}
	if version != wantVersion {
		t.Fatalf("latest migration version = %d, want %d", version, wantVersion)
	}
}

// latestMigrationVersion derives the goose version_id the forward migration
// directory applies last, straight from the migration filenames, so shipping
// the next migration never invalidates this test. Goose versions a migration
// by the leading digits of its filename, and only the forward (.up.sql) files
// run on Up, so the expectation is the maximum numeric prefix among the
// *.up.sql files. Counting files would instead assume a gap-free numbering,
// which the glob plus prefix parse does not.
func latestMigrationVersion(t *testing.T) int64 {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(backendRoot(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob forward migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no *.up.sql migrations found")
	}
	var latest int64
	for _, match := range matches {
		name := filepath.Base(match)
		prefix, _, _ := strings.Cut(name, "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatalf("migration %s has no numeric version prefix: %v", name, err)
		}
		if version > latest {
			latest = version
		}
	}
	return latest
}
