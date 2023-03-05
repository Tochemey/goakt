package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

type testkitSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the Postgres database engine and set the container
// host and port to use in the tests
func (s *testkitSuite) SetupSuite() {
	s.container = NewTestContainer("testdb", "test", "test")
}

func (s *testkitSuite) TearDownSuite() {
	s.container.Cleanup()
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestTestKitSuite(t *testing.T) {
	suite.Run(t, new(testkitSuite))
}

func (s *testkitSuite) TestDropTable() {
	s.Run("with no table defined", func() {
		ctx := context.TODO()
		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)

		// drop fake table
		err = db.DropTable(ctx, "fake")
		s.Assert().NoError(err)
		s.Assert().Nil(err)

		err = db.Disconnect(ctx)
		s.Assert().NoError(err)
	})
}

func (s *testkitSuite) TestTableExist() {
	s.Run("with no table defined", func() {
		ctx := context.TODO()
		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)

		// check fake table existence
		err = db.TableExists(ctx, "fake")
		s.Assert().NoError(err)
		s.Assert().Nil(err)
		err = db.Disconnect(ctx)
		s.Assert().NoError(err)
	})
}

func (s *testkitSuite) TestCreateAndCheckExistence() {
	s.Run("happy path", func() {
		ctx := context.TODO()
		const schemaName = "example"

		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)

		err = db.CreateSchema(ctx, schemaName)
		s.Assert().NoError(err)

		ok, err := db.SchemaExists(ctx, schemaName)
		s.Assert().NoError(err)
		s.Assert().True(ok)

		err = db.DropSchema(ctx, schemaName)
		s.Assert().NoError(err)

		err = db.Disconnect(ctx)
		s.Assert().NoError(err)
	})
	s.Run("schema does not exist", func() {
		ctx := context.TODO()
		const schemaName = "example"

		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)
		ok, err := db.SchemaExists(ctx, schemaName)
		s.Assert().NoError(err)
		s.Assert().False(ok)

		err = db.Disconnect(ctx)
		s.Assert().NoError(err)
	})
}

func (s *testkitSuite) TestCreateTable() {
	s.Run("happy path", func() {
		ctx := context.TODO()
		const stmt = `create table mangoes(id serial, taste varchar(10));`

		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)

		_, err = db.Exec(ctx, stmt)
		s.Assert().NoError(err)

		err = db.TableExists(ctx, "public.mangoes")
		s.Assert().NoError(err)
		s.Assert().Nil(err)

		err = db.DropTable(ctx, "public.mangoes")
		s.Assert().NoError(err)

		err = db.Disconnect(ctx)
		s.Assert().NoError(err)
	})
	s.Run("happy path in a different schema", func() {
		ctx := context.TODO()
		const schemaName = "example"
		const stmt = `create table example.mangoes(id serial, taste varchar(10));`

		db := s.container.GetTestDB()

		err := db.Connect(ctx)
		s.Assert().NoError(err)

		err = db.CreateSchema(ctx, schemaName)
		s.Assert().NoError(err)

		ok, err := db.SchemaExists(ctx, schemaName)
		s.Assert().NoError(err)
		s.Assert().True(ok)

		_, err = db.Exec(ctx, stmt)
		s.Assert().NoError(err)

		err = db.TableExists(ctx, "example.mangoes")
		s.Assert().NoError(err)
		s.Assert().Nil(err)

		err = db.DropSchema(ctx, schemaName)
		s.Assert().NoError(err)
	})
}

func (s *testkitSuite) TestCount() {
	ctx := context.TODO()
	const schemaName = "example"
	const stmt = `create table example.mangoes(id serial, taste varchar(10));`

	db := s.container.GetTestDB()

	err := db.Connect(ctx)
	s.Assert().NoError(err)

	err = db.CreateSchema(ctx, schemaName)
	s.Assert().NoError(err)

	ok, err := db.SchemaExists(ctx, schemaName)
	s.Assert().NoError(err)
	s.Assert().True(ok)

	_, err = db.Exec(ctx, stmt)
	s.Assert().NoError(err)

	err = db.TableExists(ctx, "example.mangoes")
	s.Assert().NoError(err)
	s.Assert().Nil(err)

	count, err := db.Count(ctx, "example.mangoes")
	s.Assert().NoError(err)
	s.Assert().Equal(0, count)

	err = db.DropSchema(ctx, schemaName)
	s.Assert().NoError(err)

	err = db.Disconnect(ctx)
	s.Assert().NoError(err)
}
