// Package main is the entry point for the chat application backend service.
// It initializes database connections, registers HTTP routing via Gin,
// and starts the server configured for local and network access (IPv4).
package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

/*
	Server configuration defaults.
	These defaults are used when corresponding environment variables are not provided.
*/
const (
	defaultPort   = "8080"
	defaultDBURL  = "postgres://postgres:postgres@localhost:5432/chat_app_db?sslmode=disable"
	defaultHostIP = "10.192.168.159"
)

// initDB initializes and verifies the connection to the PostgreSQL database.
// It retrieves the connection string from DATABASE_URL or falls back to defaultDBURL.
func initDB() (*sql.DB, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = defaultDBURL
	}

	// Open SQL connection pool with postgres driver
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Ping database to verify connection readiness
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// setupRouter configures the Gin engine and registers API routes.
func setupRouter() *gin.Engine {
	r := gin.Default()

	// Health check endpoint
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Server is running with Gin",
		})
	})

	return r
}

// main starts the chat application backend server.
func main() {
	// Initialize database connection
	db, err := initDB()
	if err != nil {
		// Log warning if database is not reachable yet, without crashing server during development
		log.Printf("Warning: Database connection failed: %v", err)
	} else {
		defer db.Close()
		fmt.Println("Connected to PostgreSQL successfully!")
	}

	// Determine port from environment or default
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// Set up Gin router
	router := setupRouter()

	// Bind to 0.0.0.0 to allow access from local machine and external Wi-Fi devices
	addr := "0.0.0.0:" + port
	fmt.Printf("Server listening on: %s\n", addr)
	fmt.Printf("Local Wi-Fi hosting URL for mobile app: http://%s:%s\n", defaultHostIP, port)

	// Start HTTP server
	if err := router.Run(addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
