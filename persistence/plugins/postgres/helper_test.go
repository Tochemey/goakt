package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/tochemey/goakt/pkg/postgres"
)

var testContainer *postgres.TestContainer

const (
	testUser             = "test"
	testDatabase         = "testdb"
	testDatabasePassword = "test"
)

// TestMain will spawn a postgres database container that will be used for all tests
// making use of the postgres database container
func TestMain(m *testing.M) {
	// set the test container
	testContainer = postgres.NewTestContainer(testDatabase, testUser, testDatabasePassword)
	// execute the tests
	code := m.Run()
	// free resources
	testContainer.Cleanup()
	// exit the tests
	os.Exit(code)
}

// dbHandle returns a test db
func dbHandle(ctx context.Context) (*postgres.TestDB, error) {
	db := testContainer.GetTestDB()
	if err := db.Connect(ctx); err != nil {
		return nil, err
	}
	return db, nil
}
