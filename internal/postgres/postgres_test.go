package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/suite"
)

// account is a test struct
type account struct {
	AccountID   string
	AccountName string
}

// PostgresTestSuite will run the Postgres tests
type PostgresTestSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the Postgres database engine and set the container
// host and port to use in the tests
func (s *PostgresTestSuite) SetupSuite() {
	s.container = NewTestContainer("testdb", "test", "test")
}

func (s *PostgresTestSuite) TearDownSuite() {
	s.container.Cleanup()
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestPostgresTestSuite(t *testing.T) {
	suite.Run(t, new(PostgresTestSuite))
}

func (s *PostgresTestSuite) TestConnect() {
	s.Run("with valid connection settings", func() {
		ctx := context.TODO()
		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)
	})

	s.Run("with invalid database port", func() {
		ctx := context.TODO()
		db := New(&Config{
			DBUser:     "test",
			DBName:     "testdb",
			DBPassword: "test",
			DBSchema:   s.container.Schema(),
			DBHost:     s.container.Host(),
			DBPort:     -2,
		})
		err := db.Connect(ctx)
		s.Assert().Error(err)
	})

	s.Run("with invalid database name", func() {
		ctx := context.TODO()
		db := New(&Config{
			DBUser:     "test",
			DBName:     "wrong-name",
			DBPassword: "test",
			DBSchema:   s.container.Schema(),
			DBHost:     s.container.Host(),
			DBPort:     s.container.Port(),
		})
		err := db.Connect(ctx)
		s.Assert().Error(err)
	})

	s.Run("with invalid database user", func() {
		ctx := context.TODO()
		db := New(&Config{
			DBUser:     "test-user",
			DBName:     "testdb",
			DBPassword: "test",
			DBSchema:   s.container.Schema(),
			DBHost:     s.container.Host(),
			DBPort:     s.container.Port(),
		})
		err := db.Connect(ctx)
		s.Assert().Error(err)
	})

	s.Run("with invalid database password", func() {
		ctx := context.TODO()
		db := New(&Config{
			DBUser:     "test",
			DBName:     "testdb",
			DBPassword: "invalid-db-pass",
			DBSchema:   s.container.Schema(),
			DBHost:     s.container.Host(),
			DBPort:     s.container.Port(),
		})

		err := db.Connect(ctx)
		s.Assert().Error(err)
	})
}

func (s *PostgresTestSuite) TestExec() {
	ctx := context.TODO()
	db := s.container.GetTestDB()
	err := db.Connect(ctx)
	s.Assert().NoError(err)

	s.Run("with valid SQL statement", func() {
		// let us create a test table
		const schemaDDL = `
		CREATE TABLE accounts
		(
		    account_id		UUID,
			account_name 	VARCHAR(255)  NOT NULL,
		    PRIMARY KEY (account_id)
		);
	`
		_, err = db.Exec(ctx, schemaDDL)
		s.Assert().NoError(err)
	})

	s.Run("with invalid SQL statement", func() {
		const schemaDDL = `SOME-INVALID-SQL`
		_, err = db.Exec(ctx, schemaDDL)
		s.Assert().Error(err)
	})
}

func (s *PostgresTestSuite) TestSelect() {
	ctx := context.TODO()
	db := s.container.GetTestDB()
	err := db.Connect(ctx)
	s.Assert().NoError(err)

	const selectSQL = `SELECT account_id, account_name FROM accounts WHERE account_id = $1`

	s.Run("with valid record", func() {
		// first drop the table
		err = db.DropTable(ctx, "accounts")
		s.Assert().NoError(err)

		// create the database table
		err = createTable(ctx, db)
		s.Assert().NoError(err)

		// let us insert into that table
		inserted := &account{
			AccountID:   uuid.New().String(),
			AccountName: "some-account",
		}
		err = insertInto(ctx, db, inserted)
		s.Assert().NoError(err)

		// let us select the record inserted
		selected := &account{}
		err = db.Select(ctx, selected, selectSQL, inserted.AccountID)
		s.Assert().NoError(err)

		// let us compare the selected data and the record added
		s.Assert().Equal(inserted.AccountID, selected.AccountID)
		s.Assert().Equal(inserted.AccountName, selected.AccountName)
	})

	s.Run("with no records", func() {
		// first drop the table
		err = db.DropTable(ctx, "accounts")
		s.Assert().NoError(err)

		// create the database table
		err = createTable(ctx, db)
		s.Assert().NoError(err)

		var selected *account
		err = db.Select(ctx, selected, selectSQL, uuid.New().String())
		s.Assert().NoError(err)
		s.Assert().Nil(selected)
	})

	s.Run("with invalid SQL statement", func() {
		var selected *account
		err = db.Select(ctx, selected, "weird-sql", uuid.New().String())
		s.Assert().Error(err)
		s.Assert().Nil(selected)
	})
}

func (s *PostgresTestSuite) TestSelectAll() {
	ctx := context.TODO()
	db := s.container.GetTestDB()
	err := db.Connect(ctx)
	s.Assert().NoError(err)

	const selectSQL = `SELECT account_id, account_name FROM accounts;`

	s.Run("with valid records", func() {
		// first drop the table
		err = db.DropTable(ctx, "accounts")
		s.Assert().NoError(err)

		// create the database table
		err = createTable(ctx, db)
		s.Assert().NoError(err)

		// let us insert into that table
		inserted := &account{
			AccountID:   uuid.New().String(),
			AccountName: "some-account",
		}
		err = insertInto(ctx, db, inserted)
		s.Assert().NoError(err)

		// let us select the record inserted
		var selected []*account
		err = db.SelectAll(ctx, &selected, selectSQL)
		s.Assert().NoError(err)
		s.Assert().Equal(1, len(selected))
	})

	s.Run("with no records", func() {
		// first drop the table
		err = db.DropTable(ctx, "accounts")
		s.Assert().NoError(err)

		// create the database table
		err = createTable(ctx, db)
		s.Assert().NoError(err)

		var selected []*account
		err = db.SelectAll(ctx, &selected, selectSQL)
		s.Assert().NoError(err)
		s.Assert().Nil(selected)
	})

	s.Run("with invalid SQL statement", func() {
		var selected []*account
		err = db.SelectAll(ctx, selected, "weird-sql", uuid.New().String())
		s.Assert().Error(err)
		s.Assert().Nil(selected)
	})
}

func (s *PostgresTestSuite) TestClose() {
	ctx := context.TODO()
	db := s.container.GetTestDB()
	err := db.Connect(ctx)
	s.Assert().NoError(err)

	// close the db connection
	err = db.Disconnect(ctx)
	s.Assert().NoError(err)

	// let us execute a query against a closed connection
	err = db.TableExists(ctx, "accounts")
	s.Assert().Error(err)
	s.Assert().EqualError(err, "sql: database is closed")
}

func createTable(ctx context.Context, db IDatabase) error {
	// let us create a test table
	const schemaDDL = `
		CREATE TABLE IF NOT EXISTS accounts
		(
		    account_id		UUID,
			account_name 	VARCHAR(255)  NOT NULL,
		    PRIMARY KEY (account_id)
		);	
	`
	_, err := db.Exec(ctx, schemaDDL)
	return err
}

func insertInto(ctx context.Context, db IDatabase, account *account) error {
	const insertSQL = `INSERT INTO accounts(account_id, account_name) VALUES($1, $2);`
	_, err := db.Exec(ctx, insertSQL, account.AccountID, account.AccountName)
	return err
}
