package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/platform/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// runBootstrapService opens the database, runs the migrations and invokes
// BootstrapAdministrator. It returns the Service so tests can inspect the
// resulting state and the populated BootstrapResult. The function is
// exported so the bootstrap test can drive the binary end-to-end.
func runBootstrapService(ctx context.Context, dsn string, input admin.BootstrapInput) (*admin.Service, admin.BootstrapResult, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		return nil, admin.BootstrapResult{}, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, admin.BootstrapResult{}, fmt.Errorf("database pool: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, admin.BootstrapResult{}, fmt.Errorf("ping database: %w", err)
	}
	if err := database.RunMigrations(ctx, db, bootstrapMigrationsDir()); err != nil {
		return nil, admin.BootstrapResult{}, fmt.Errorf("run migrations: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc, err := admin.NewService(admin.ServiceDeps{
		DB:     db,
		Logger: logger,
		Now:    func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, admin.BootstrapResult{}, fmt.Errorf("admin service: %w", err)
	}
	result, err := svc.BootstrapAdministrator(ctx, input)
	if err != nil {
		return svc, admin.BootstrapResult{}, err
	}
	return svc, result, nil
}

// bootstrapMigrationsDir resolves the path to the migrations directory. It
// honors the SHOP_MALL_MIGRATIONS_DIR environment variable so deployments
// can place migrations outside the working directory; otherwise it uses the
// canonical "../../migrations" relative path that matches the binary's
// expected location at backend/cmd/admin-bootstrap.
func bootstrapMigrationsDir() string {
	if override := strings.TrimSpace(os.Getenv("SHOP_MALL_MIGRATIONS_DIR")); override != "" {
		return override
	}
	return "../../migrations"
}

// errBootstrapExists is returned when the controlled input path detects an
// already-existing administrator. The sentinel makes it easy for callers
// (including the bootstrap integration tests) to distinguish bootstrap
// rejections from infrastructure failures.
var errBootstrapExists = errors.New("admin bootstrap: administrator already exists")

// errBootstrapWeakPassword is the sentinel for weak passwords. Callers
// checking against this value can short-circuit before exposing the password
// to the operator.
var errBootstrapWeakPassword = errors.New("admin bootstrap: weak password")

// errBootstrapMissingDatabase is returned when DATABASE_URL is empty. The
// bootstrap binary must never run against a database the operator cannot
// name explicitly.
var errBootstrapMissingDatabase = errors.New("admin bootstrap: DATABASE_URL is required")
