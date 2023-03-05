package postgres

import "time"

type Config struct {
	DBHost                string        // DBHost represents the database host
	DBPort                int           // DBPort is the database port
	DBName                string        // DBName is the database name
	DBUser                string        // DBUser is the database user used to connect
	DBPassword            string        // DBPassword is the database password
	DBSchema              string        // DBSchema represents the database schema
	MaxOpenConnections    int           // MaxOpenConnections represents the number of open connections in the pool
	MaxIdleConnections    int           // MaxIdleConnections represents the number of idle connections in the pool
	ConnectionMaxLifetime time.Duration // ConnectionMaxLifetime represents the connection max life time
}
