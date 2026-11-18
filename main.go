// Package main is the entry point for the chat application backend service.
// It initializes application configurations, establishes the PostgreSQL connection pool,
// sets up Gin routing, and starts the HTTP server.
package main

import (
	"fmt"
	"log"
	"net/http"

	"backend/internal/config"
	"backend/internal/database"

	"github.com/gin-gonic/gin"
)

// setupRouter configures the Gin engine and registers API routes.
func setupRouter() *gin.Engine {
	r := gin.Default()

	// Base health check endpoint
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Server is running with Gin",
		})
	})

	return r
}

// main initializes dependencies, connects to the database, and runs the server.
func main() {
	/*
		Step 1: Load configurations
		Loads environment variables from .env file and sets fallback defaults.
	*/
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load application configuration: %v", err)
	}

	/*
		Step 2: Initialize Database Connection Pool
		Connects to PostgreSQL using pgxpool and ensures clean shutdown with defer.
	*/
	pool, err := database.ConnectDB(cfg)
	if err != nil {
		log.Fatalf("Database connection initialization failed: %v", err)
	}
	// Ensure the connection pool is cleanly closed when the application terminates
	defer pool.Close()

	/*
		Step 3: Execute Database Migrations
		Runs table migrations in the required serial sequence:
		users -> friendships -> blocks -> reports -> messages -> call_logs.
	*/
	if err := database.RunMigrations(pool); err != nil {
		log.Fatalf("Database migration execution failed: %v", err)
	}

	// Set Gin mode according to configuration
	if cfg.GinMode != "" {
		gin.SetMode(cfg.GinMode)
	}

	// Step 4: Configure router and endpoints
	router := setupRouter()

	// Step 5: Start the HTTP server on configured host and port
	addr := "0.0.0.0:" + cfg.ServerPort
	fmt.Printf("Server listening on: %s\n", addr)
	fmt.Printf("Local Wi-Fi hosting URL for mobile app: http://%s:%s\n", cfg.HostIP, cfg.ServerPort)

	// Run the server
	if err := router.Run(addr); err != nil {
		log.Fatalf("Server failed to run: %v", err)
	}
}
