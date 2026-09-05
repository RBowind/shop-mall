package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const testDatabaseURLVariable = "TEST_DATABASE_URL"

// TestDatabaseDSN returns the DSN for the isolated test database and a cleanup
// function. Concurrency tests use it to open a second, independent connection
// against the same database so a test can hold locks that another transaction
// waits on. When TEST_DATABASE_URL is set it is returned verbatim; otherwise a
// throwaway PostgreSQL container is started.
func TestDatabaseDSN(t *testing.T) (string, func()) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(testDatabaseURLVariable))
	if dsn != "" {
		return dsn, func() {}
	}
	return startPostgresContainer(t)
}

func OpenTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, _ := OpenTestDatabaseWithDSN(t)
	return db
}

// OpenTestDatabaseWithDSN opens the isolated test database and returns it
// together with the DSN and cleanup, so a test can open additional, independent
// connections against the same database (for example to hold locks another
// transaction waits on).
func OpenTestDatabaseWithDSN(t *testing.T) (*gorm.DB, string, func()) {
	t.Helper()
	dsn, cleanupFromDSN := TestDatabaseDSN(t)
	cleanup := cleanupFromDSN

	var (
		db  *gorm.DB
		err error
	)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			if sqlDB, dbErr := db.DB(); dbErr == nil && db.Exec("SELECT 1").Error == nil {
				t.Cleanup(func() {
					_ = sqlDB.Close()
					cleanup()
				})
				return db, dsn, cleanup
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	cleanup()
	t.Fatalf("PostgreSQL test database did not become ready within 30 seconds: %v", err)
	return nil, "", nil
}

func startPostgresContainer(t *testing.T) (string, func()) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("PostgreSQL integration tests skipped: Docker unavailable: %v", err)
	}

	container := fmt.Sprintf("shop-mall-integration-test-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--rm", "--name", container,
		"-e", "POSTGRES_PASSWORD=test", "-e", "POSTGRES_DB=shop_mall_test",
		"-e", "POSTGRES_USER=test", "-p", "127.0.0.1::5432", "-d", "postgres:16-alpine")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start PostgreSQL test container: %v: %s", err, strings.TrimSpace(string(output)))
	}
	cleanup := func() {
		// -v: an explicit docker rm bypasses the --rm cleanup that would
		// otherwise drop the container's anonymous volume, leaking one per test.
		_ = exec.Command("docker", "rm", "-fv", container).Run()
	}

	output, err := exec.Command("docker", "port", container, "5432/tcp").CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("get PostgreSQL test container port: %v: %s", err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	portIndex := strings.LastIndex(line, ":")
	if portIndex < 0 || portIndex == len(line)-1 {
		cleanup()
		t.Fatalf("unexpected PostgreSQL test container port output %q", line)
	}
	port := line[portIndex+1:]
	if _, err := strconv.Atoi(port); err != nil {
		cleanup()
		t.Fatalf("invalid PostgreSQL test container port %q: %v", port, err)
	}
	return fmt.Sprintf("host=127.0.0.1 port=%s user=test password=test dbname=shop_mall_test sslmode=disable", port), cleanup
}
