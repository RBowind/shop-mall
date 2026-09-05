package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// startBootstrapPostgresContainer starts a postgres:16-alpine container
// and returns the DSN plus the database handle. The container is removed
// at test cleanup time.
func startBootstrapPostgresContainer(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("bootstrap integration tests skipped: Docker unavailable: %v", err)
	}
	container := fmt.Sprintf("shop-mall-bootstrap-test-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--rm", "--name", container,
		"-e", "POSTGRES_PASSWORD=test", "-e", "POSTGRES_DB=shop_mall_bootstrap_test",
		"-e", "POSTGRES_USER=test", "-p", "127.0.0.1::5432", "-d", "postgres:16-alpine")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start bootstrap PostgreSQL container: %v: %s", err, strings.TrimSpace(string(output)))
	}
	output, err := exec.Command("docker", "port", container, "5432/tcp").CombinedOutput()
	if err != nil {
		_ = exec.Command("docker", "rm", "-fv", container).Run()
		t.Fatalf("get bootstrap container port: %v: %s", err, strings.TrimSpace(string(output)))
	}
	port := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if idx := strings.LastIndex(port, ":"); idx >= 0 && idx < len(port)-1 {
		port = port[idx+1:]
	}
	dsn := fmt.Sprintf("host=127.0.0.1 port=%s user=test password=test dbname=shop_mall_bootstrap_test sslmode=disable", port)

	deadline := time.Now().Add(30 * time.Second)
	var gdb *gorm.DB
	for time.Now().Before(deadline) {
		gdb, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			if pingErr := gdb.Exec("SELECT 1").Error; pingErr == nil {
				sqlDB, _ := gdb.DB()
				t.Cleanup(func() {
					if sqlDB != nil {
						_ = sqlDB.Close()
					}
					_ = exec.Command("docker", "rm", "-fv", container).Run()
				})
				return sqlDB, dsn, container
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = exec.Command("docker", "rm", "-fv", container).Run()
	t.Fatalf("bootstrap PostgreSQL did not become ready within 30 seconds: %s", dsn)
	return nil, "", ""
}

// runBootstrapForTest invokes runBootstrap with the supplied arguments and
// environment variables, capturing stdout and stderr. The function returns
// the exit-style error so tests can assert against the sentinel values.
// The implementation precompiles the bootstrap binary and invokes it
// directly so the test does not pay the cost of `go run` on every
// invocation.
func runBootstrapForTest(t *testing.T, stdout, stderr *concurrentBuffer, args []string, env map[string]string) error {
	t.Helper()
	binPath := buildBootstrapBinary(t)
	cmd := exec.Command(binPath, args...)
	cmd.Env = mergeEnv(baseEnv(), env)
	// go test already runs with the package directory as cwd, so the child
	// inherits the same directory and the relative migrations path
	// "../../migrations" resolves to backend/migrations on every host.
	cmd.Dir = "."
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// buildBootstrapBinary compiles the admin-bootstrap command to a temporary
// binary path and returns the path. The binary is written to t.TempDir() so
// the suite runs on Windows where /tmp is not a writable directory.
func buildBootstrapBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "admin-bootstrap")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile admin-bootstrap: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return binPath
}

// mergeEnv overlays the supplied overrides on top of the base environment so
// the child inherits PATH and the toolchain workspace on every host. The
// overrides always win regardless of the host operating system.
func mergeEnv(base []string, overrides map[string]string) []string {
	replaced := make(map[string]struct{}, len(overrides))
	for k := range overrides {
		replaced[k] = struct{}{}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		if _, ok := replaced[kv[:idx]]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// concurrentBuffer protects a bytes.Buffer for use from multiple goroutines.
type concurrentBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *concurrentBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *concurrentBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// baseEnv returns the parent environment variables the bootstrap binary needs
// to load its runtime on the host. The binary itself reads only DATABASE_URL
// and SHOP_MALL_MIGRATIONS_DIR, but the child must inherit the host PATH (and
// the rest of the environment) so Windows resolves system DLLs and Unix
// resolves the toolchain.
func baseEnv() []string {
	return os.Environ()
}
