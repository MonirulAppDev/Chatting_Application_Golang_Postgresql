// Package models defines the application domain models, database entities, and API payloads.
package models

import "time"

/*
	User represents a user account entity in the database.
	The PasswordHash field is excluded from JSON serialization (json:"-") for security.
*/
type User struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// Unique user display name
	Username string `json:"username"`
	// Unique user email address
	Email string `json:"email"`
	// Hashed user password, hidden from client JSON responses
	PasswordHash string `json:"-"`
	// Optional avatar image URL (nullable)
	AvatarURL *string `json:"avatar_url,omitempty"`
	// Timestamp of user's last activity/presence
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// Account creation timestamp
	CreatedAt time.Time `json:"created_at"`
}

/*
	RegisterRequest captures the incoming JSON payload for user registration.
*/
type RegisterRequest struct {
	// Desired username for the new account
	Username string `json:"username" binding:"required"`
	// User email address
	Email string `json:"email" binding:"required,email"`
	// Raw account password
	Password string `json:"password" binding:"required"`
}

/*
	LoginRequest captures the incoming JSON payload for user authentication.
*/
type LoginRequest struct {
	// Account email address
	Email string `json:"email" binding:"required,email"`
	// Account password for verification
	Password string `json:"password" binding:"required"`
}
