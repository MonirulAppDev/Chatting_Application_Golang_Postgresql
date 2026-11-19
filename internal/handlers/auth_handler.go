// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

/*
	AuthHandler manages authentication operations such as registration and login.
	It encapsulates the database connection pool and JWT secret key required for auth operations.
*/
type AuthHandler struct {
	// Database connection pool for performing user queries
	DB *pgxpool.Pool
	// Secret key retrieved from .env used for signing and verifying JWT tokens
	JWTSecret string
}

// NewAuthHandler creates and returns a new AuthHandler instance with the provided dependencies.
func NewAuthHandler(db *pgxpool.Pool, jwtSecret string) *AuthHandler {
	return &AuthHandler{
		DB:        db,
		JWTSecret: jwtSecret,
	}
}

// writeJSON is a helper function to send JSON responses with status codes.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// Register handles user registration HTTP requests for http.HandlerFunc.
// It parses the request payload, hashes the password using bcrypt,
// saves the user record in PostgreSQL, and returns 201 Created upon success.
//
// @Summary Register a new user
// @Description Register a new account with username, email, and password
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body models.RegisterRequest true "User registration details"
// @Success 201 {object} map[string]interface{} "User registered successfully"
// @Failure 400 {object} map[string]string "Invalid request payload"
// @Failure 409 {object} map[string]string "Username or email already exists"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/register [post]
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Request Body Decoding
		Decode and validate incoming JSON for username, email, and password.
		Returns 400 Bad Request if validation fails or fields are empty.
	*/
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. Username, email, and password are required.",
		})
		return
	}

	// Validate non-empty fields explicitly
	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Username, email, and password cannot be empty.",
		})
		return
	}

	/*
		Step 2: Password Hashing
		Hash the raw plain password securely with bcrypt using default work factor.
	*/
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to hash password.",
		})
		return
	}

	/*
		Step 3: Database Insertion
		Insert new record into users table and retrieve the generated ID and CreatedAt.
	*/
	query := `
		INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, created_at;
	`

	user := models.User{
		Username: req.Username,
		Email:    req.Email,
	}

	err = h.DB.QueryRow(r.Context(), query, req.Username, req.Email, string(hashedPassword)).Scan(
		&user.ID,
		&user.CreatedAt,
	)

	if err != nil {
		// Detect duplicate email or username constraint violation (PostgreSQL code 23505)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "A user with this username or email already exists.",
			})
			return
		}

		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while creating user.",
		})
		return
	}

	/*
		Step 4: Success Response
		Return 201 Created status code along with the created user details (excluding password).
	*/
	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "User registered successfully",
		"user":    user,
	})
}

// Login handles user authentication HTTP requests for http.HandlerFunc.
// It verifies email and password against the database with bcrypt,
// generates a signed JWT token containing user_id, username, and expiration,
// and returns the token with user info.
//
// @Summary Login user
// @Description Authenticate a user with email and password to receive a JWT Bearer token
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body models.LoginRequest true "User credentials"
// @Success 200 {object} map[string]interface{} "Login successful with token and user profile"
// @Failure 400 {object} map[string]string "Invalid request payload"
// @Failure 401 {object} map[string]string "Invalid email or password"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Parse request body
		Retrieve email and password from client JSON payload.
	*/
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. Email and password are required.",
		})
		return
	}

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Email and password cannot be empty.",
		})
		return
	}

	/*
		Step 2: Database Query
		Find user by email and retrieve id, username, email, password_hash, avatar_url, and created_at.
		Returns 401 Unauthorized if user does not exist.
	*/
	query := `
		SELECT id, username, email, password_hash, avatar_url, created_at
		FROM users
		WHERE email = $1;
	`

	var user models.User
	err := h.DB.QueryRow(r.Context(), query, req.Email).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.AvatarURL,
		&user.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "Invalid email or password.",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while verifying credentials.",
		})
		return
	}

	/*
		Step 3: Password Comparison
		Compare user provided password against hashed password in database using bcrypt.
		Returns 401 Unauthorized if credentials do not match.
	*/
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Invalid email or password.",
		})
		return
	}

	/*
		Step 4: Generate JWT Token
		Construct claims with user_id, username, and 24-hour expiration time (exp).
		Sign the token using the secret key (JWTSecret).
	*/
	claims := jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Username,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(h.JWTSecret))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to generate authentication token.",
		})
		return
	}

	/*
		Step 5: Success Response
		Return 200 OK with the generated JWT token and user profile data (password_hash is excluded).
	*/
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Login successful",
		"token":   tokenString,
		"user":    user,
	})
}

// RegisterGin adapts the standard Register handler for Gin frameworks.
func (h *AuthHandler) RegisterGin(c *gin.Context) {
	h.Register(c.Writer, c.Request)
}

// LoginGin adapts the standard Login handler for Gin frameworks.
func (h *AuthHandler) LoginGin(c *gin.Context) {
	h.Login(c.Writer, c.Request)
}
