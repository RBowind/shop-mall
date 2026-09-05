package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"shop-mall/backend/internal/config"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const DefaultTransactionAttempts = 3

func Open(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("database config: %w", err)
	}
	db, err := gorm.Open(postgres.Open(cfg.URL), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get database pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func HealthCheck(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("database is not configured")
	}
	var result int
	if err := db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error; err != nil {
		return fmt.Errorf("database health check: %w", err)
	}
	if result != 1 {
		return fmt.Errorf("database health check returned %d", result)
	}
	return nil
}

func RunMigrations(ctx context.Context, db *gorm.DB, directory string) error {
	if db == nil {
		return errors.New("database is not configured")
	}
	if directory == "" {
		return errors.New("migration directory is required")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database pool for migrations: %w", err)
	}
	migrations, err := readMigrationFS(directory)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	provider, err := goose.NewProvider("postgres", sqlDB, migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run forward migrations: %w", err)
	}
	return nil
}

func readMigrationFS(directory string) (fs.FS, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("migration path %q is not a directory", directory)
	}
	return migrationFS{base: os.DirFS(directory)}, nil
}

type migrationFS struct {
	base fs.FS
}

func (m migrationFS) Open(name string) (fs.File, error) {
	file, err := m.base.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() || !strings.HasSuffix(name, ".sql") {
		return file, err
	}
	contents, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	return &normalizedMigrationFile{
		Reader: bytes.NewReader(normalizeLegacyMigration(contents)),
		info:   info,
	}, nil
}

type normalizedMigrationFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *normalizedMigrationFile) Close() error { return nil }

func (f *normalizedMigrationFile) Stat() (fs.FileInfo, error) { return f.info, nil }

func normalizeLegacyMigration(contents []byte) []byte {
	const (
		functionStart = "CREATE FUNCTION"
		functionEnd   = "$$;"
	)
	if bytes := string(contents); strings.Contains(bytes, "-- +goose StatementBegin") || !strings.Contains(bytes, functionStart) {
		return contents
	}
	text := string(contents)
	start := strings.Index(text, functionStart)
	endOffset := strings.Index(text[start:], functionEnd)
	if start < 0 || endOffset < 0 {
		return contents
	}
	end := start + endOffset + len(functionEnd)
	return []byte(text[:start] + "-- +goose StatementBegin\n" + text[start:end] + "\n-- +goose StatementEnd\n" + text[end:])
}

type TransactionRunner struct {
	DB           *gorm.DB
	MaxAttempts  int
	Backoff      func(attempt int) time.Duration
	SleepContext func(ctx context.Context, delay time.Duration) error
}

func NewTransactionRunner(db *gorm.DB) *TransactionRunner {
	return &TransactionRunner{
		DB:          db,
		MaxAttempts: DefaultTransactionAttempts,
		Backoff: func(attempt int) time.Duration {
			return time.Duration(attempt) * 10 * time.Millisecond
		},
		SleepContext: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (r *TransactionRunner) RunTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if r == nil || r.DB == nil {
		return errors.New("database transaction runner is not configured")
	}
	if fn == nil {
		return errors.New("transaction function is required")
	}
	attempts := r.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := r.DB.WithContext(ctx).Transaction(fn)
		if !IsRetryableTransactionError(err) || attempt == attempts {
			return err
		}
		if r.SleepContext != nil && r.Backoff != nil {
			if err := r.SleepContext(ctx, r.Backoff(attempt)); err != nil {
				return err
			}
		}
	}
	return errors.New("transaction attempts exhausted")
}

func RunTransaction(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	return NewTransactionRunner(db).RunTransaction(ctx, fn)
}

func IsRetryableTransactionError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40P01" || pgErr.Code == "40001"
}

// IsUniqueViolationError reports whether err is a PostgreSQL unique-violation
// (SQLSTATE 23505). Callers translate a partial-index conflict such as the
// one-default-address-per-user index into a business error instead of a 500.
func IsUniqueViolationError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}
