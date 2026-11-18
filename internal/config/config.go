// Package config handles loading and parsing of environment variables and application configurations.
package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

/*
Config holds all environment configurations for the application.
It centralizes server, database, file storage, and authentication settings.
*/
type Config struct {
	// Server settings
	ServerPort string
	HostIP     string
	GinMode    string

	// PostgreSQL database settings
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string
	DatabaseURL string

	// Media and static storage settings
	UploadDir        string
	StaticDir        string
	StickersDir      string
	MaxFileSizeBytes string

	// JWT authentication settings
	JWTSecret     string
	JWTExpiration string
}

// getEnv retrieves an environment variable by key or returns a fallback default value if not set.
func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// LoadConfig loads environment variables from .env file via godotenv.Load(),
// reads values using os.Getenv, and returns a populated Config struct pointer.
func LoadConfig() (*Config, error) {
	// Attempt to load .env file into environment
	if err := godotenv.Load(); err != nil {
		// Log warning if .env is missing, will fall back to system environment variables
		log.Println("Notice: No .env file loaded, falling back to system environment variables")
	}

	// Populate Config struct from environment
	cfg := &Config{
		// Server configuration
		ServerPort: getEnv("SERVER_PORT", "8080"),
		HostIP:     getEnv("HOST_IP", "10.192.168.159"),
		GinMode:    getEnv("GIN_MODE", "debug"),

		// Database configuration
		DBHost:      getEnv("DB_HOST", "localhost"),
		DBPort:      getEnv("DB_PORT", "5432"),
		DBUser:      getEnv("DB_USER", "postgres"),
		DBPassword:  getEnv("DB_PASSWORD", "postgres"),
		DBName:      getEnv("DB_NAME", "chat_app_db"),
		DBSSLMode:   getEnv("DB_SSLMODE", "disable"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/chat_app_db?sslmode=disable"),

		// File and storage configuration
		UploadDir:        getEnv("UPLOAD_DIR", "./uploads"),
		StaticDir:        getEnv("STATIC_DIR", "./static"),
		StickersDir:      getEnv("STICKERS_DIR", "./static/stickers"),
		MaxFileSizeBytes: getEnv("MAX_FILE_SIZE_BYTES", "20971520"),

		// JWT configuration
		JWTSecret:     getEnv("JWT_SECRET", "chat_app_super_secret_jwt_key_2026"),
		JWTExpiration: getEnv("JWT_EXPIRATION", "72h"),
	}

	return cfg, nil
}
