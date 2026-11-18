// Package database manages PostgreSQL connections, pooling, and database lifecycle.
package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"backend/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
	ConnectDB establishes a connection pool to PostgreSQL using pgxpool.New.
	It checks if DatabaseURL is provided in the config; if not, it constructs
	the connection string using host, port, user, password, and db name.
	It tests reachability with pool.Ping() and logs upon successful connection.
*/
func ConnectDB(cfg *config.Config) (*pgxpool.Pool, error) {
	var connStr string

	// Check if full connection URL is provided, otherwise construct one
	if cfg.DatabaseURL != "" {
		connStr = cfg.DatabaseURL
	} else {
		// Construct PostgreSQL connection URL from individual credentials
		connStr = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=%s",
			cfg.DBUser,
			cfg.DBPassword,
			cfg.DBHost,
			cfg.DBPort,
			cfg.DBName,
			cfg.DBSSLMode,
		)
	}

	// Context with timeout to avoid hanging indefinitely if the database is unreachable
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create connection pool using pgxpool.New
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("unable to create database connection pool: %w", err)
	}

	// Ping database to verify reachability
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}

	// Print log message indicating successful connection
	log.Printf("PostgreSQL connection pool established successfully to database: %s", cfg.DBName)

	return pool, nil
}
