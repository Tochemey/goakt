package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/XSAM/otelsql"
	"github.com/georgysavva/scany/sqlscan"
	_ "github.com/lib/pq" //nolint
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

// Postgres helps interact with the Postgres database
type Postgres struct {
	connStr      string
	dbConnection *sql.DB
	config       *Config
}

var _ IDatabase = (*Postgres)(nil)

const postgresDriver = "postgres"
const instrumentationName = "storage"

// New returns a storage connecting to the given Postgres database.
func New(config *Config) *Postgres {
	postgres := new(Postgres)
	postgres.config = config
	postgres.connStr = createConnectionString(config.DBHost, config.DBPort, config.DBName, config.DBUser, config.DBPassword, config.DBSchema)
	return postgres
}

// Connect will connect to our Postgres database
func (p *Postgres) Connect(ctx context.Context) error {
	// Register an OTel driver
	driverName, err := otelsql.Register(postgresDriver, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
	if err != nil {
		return errors.Wrap(err, "failed to hook the tracer to the database driver")
	}

	// open the connection and connect to the database
	db, err := sql.Open(driverName, p.connStr)
	if err != nil {
		return errors.Wrap(err, "failed to open connection")
	}

	// let us test the connection
	err = db.PingContext(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to ping database connection")
	}

	// set connection setting
	db.SetMaxOpenConns(p.config.MaxOpenConnections)
	db.SetMaxIdleConns(p.config.MaxIdleConnections)
	db.SetConnMaxLifetime(p.config.ConnectionMaxLifetime)

	// set the db handle
	p.dbConnection = db
	return nil
}

// createConnectionString will create the Postgres connection string from the
// supplied connection details
func createConnectionString(host string, port int, name, user string, password string, schema string) string {
	info := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable", host, port, user, name)
	// The Postgres driver gets confused in cases where the user has no password
	// set but a password is passed, so only set password if its non-empty
	if password != "" {
		info += fmt.Sprintf(" password=%s", password)
	}

	if schema != "" {
		info += fmt.Sprintf(" search_path=%s", schema)
	}

	return info
}

// Exec executes a sql query without returning rows against the database
func (p *Postgres) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	// Create a span
	tracer := otel.GetTracerProvider()
	spanCtx, span := tracer.Tracer(instrumentationName).Start(ctx, "storage.exec")
	defer span.End()
	return p.dbConnection.ExecContext(spanCtx, query, args...)
}

// BeginTx starts a new database transaction
func (p *Postgres) BeginTx(ctx context.Context, txOptions *sql.TxOptions) (*sql.Tx, error) {
	// Create a span
	tracer := otel.GetTracerProvider()
	spanCtx, span := tracer.Tracer(instrumentationName).Start(ctx, "storage.beginTx")
	defer span.End()
	return p.dbConnection.BeginTx(spanCtx, txOptions)
}

// SelectAll fetches rows
func (p *Postgres) SelectAll(ctx context.Context, dst interface{}, query string, args ...interface{}) error {
	// Create a span
	tracer := otel.GetTracerProvider()
	spanCtx, span := tracer.Tracer(instrumentationName).Start(ctx, "storage.selectAll")
	defer span.End()
	err := sqlscan.Select(spanCtx, p.dbConnection, dst, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return err
	}
	return nil
}

// Select fetches only one row
func (p *Postgres) Select(ctx context.Context, dst interface{}, query string, args ...interface{}) error {
	// Create a span
	tracer := otel.GetTracerProvider()
	spanCtx, span := tracer.Tracer(instrumentationName).Start(ctx, "storage.select")
	defer span.End()
	err := sqlscan.Get(spanCtx, p.dbConnection, dst, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	return nil
}

// Disconnect the database connection.
func (p *Postgres) Disconnect(ctx context.Context) error {
	if p.dbConnection == nil {
		return nil
	}
	return p.dbConnection.Close()
}
