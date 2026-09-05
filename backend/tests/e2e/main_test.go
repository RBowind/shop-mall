package e2e

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

// testDB is shared by every test in this package. It is started once in
// TestMain; when it is nil (Docker unavailable and TEST_DATABASE_URL unset)
// NewHarness skips each test.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	db, cleanup, err := StartTestDB("../../migrations")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e TestMain: %v (tests will skip)\n", err)
	} else {
		testDB = db
	}
	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}
