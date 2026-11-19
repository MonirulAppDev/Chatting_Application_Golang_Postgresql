// Package middleware provides HTTP middlewares for request filtering, authentication, and context enrichment.
package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ContextKey defines a custom type for context keys to prevent collisions across packages.
type ContextKey string

// UserIDKey is the context key used to store the authenticated user ID in the request context.
const UserIDKey ContextKey = "user_id"

/*
	AuthMiddleware intercepts incoming HTTP requests to validate authentication.
	It receives an http.Handler (the next handler in the chain) and returns
	a new http.Handler that wraps the execution.
*/
func AuthMiddleware(next http.Handler) http.Handler {
	// Return a new http.Handler using http.HandlerFunc
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/*
			Step 1: Read Authorization Header & Extract JWT Token
			Extracts the Authorization header, verifies the "Bearer " prefix,
			and strips the prefix to extract the raw JWT token string.
		*/
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Missing or invalid Authorization header. Expected format: Bearer <token>",
			})
			return
		}

		// Strip "Bearer " prefix to get the main token string
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		tokenString = strings.TrimSpace(tokenString)

		// Check if token string is empty after trimming
		if tokenString == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Authorization token cannot be empty.",
			})
			return
		}

		/*
			Step 2: Parse and Verify JWT Token
			Retrieve JWT_SECRET from .env/environment and verify token signature and validity.
			Returns 401 Unauthorized if token is invalid, signature mismatch, or expired.
		*/
		jwtSecret := os.Getenv("JWT_SECRET")
		if jwtSecret == "" {
			jwtSecret = "chat_app_super_secret_jwt_key_2026"
		}

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			// Ensure signing algorithm is HMAC
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(jwtSecret), nil
		})

		// Check for parsing error or invalid/expired token
		if err != nil || !token.Valid {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid or expired token.",
			})
			return
		}

		/*
			Step 3: Extract user_id Claims & Store in Context
			Extract user_id from token claims, store it into Go request context
			using context.WithValue, and forward enriched request to next handler.
		*/
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Failed to parse token claims.",
			})
			return
		}

		// Extract user_id from claims (handles numeric JSON float64 or int)
		var userID int
		switch val := claims["user_id"].(type) {
		case float64:
			userID = int(val)
		case int:
			userID = val
		case int64:
			userID = int(val)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid user_id in token claims.",
			})
			return
		}

		// Store user_id into request context with both custom context key and string key for convenience
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		ctx = context.WithValue(ctx, "user_id", userID)

		// Create enriched request with new context
		enrichedReq := r.WithContext(ctx)

		/*
			Step 4: Forward to Main Handler
			Pass the enriched request to the next http.Handler in chain.
		*/
		next.ServeHTTP(w, enrichedReq)
	})
}
