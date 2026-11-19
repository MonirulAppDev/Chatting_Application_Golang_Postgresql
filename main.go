// Package main is the entry point for the chat application backend service.
// It initializes application configurations, establishes the PostgreSQL connection pool,
// runs database migrations, registers routes with http.NewServeMux, and starts the HTTP server.
//
// @title Chat Application Backend API
// @version 1.0
// @description RESTful API for Real-time Chat Application with WebSocket, Authentication, and Social Features.
// @termsOfService http://swagger.io/terms/
//
// @contact.name API Support
// @contact.email support@chatapp.com
//
// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
//
// @host localhost:8080
// @BasePath /
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer <token>" to authenticate.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "backend/docs"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/handlers"
	"backend/internal/middleware"
	"backend/internal/websocket"

	httpSwagger "github.com/swaggo/http-swagger/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	/*
		Step 4: Initialize WebSocket Hub and Router
	*/
	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)

	/*
		Step 5: Configure and Run HTTP Server with Graceful Shutdown
		Launches srv.ListenAndServe() in a goroutine and awaits OS termination signals in main goroutine.
	*/
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: mux,
	}

	// Goroutine e srv.ListenAndServe() chalu kora
	go func() {
		fmt.Printf("Server listening on port :%s\n", cfg.ServerPort)
		fmt.Printf("Local Wi-Fi hosting URL for mobile app: http://%s:%s\n", cfg.HostIP, cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed to run: %v", err)
		}
	}()

	// Main goroutine e os/signal (syscall.SIGINT, syscall.SIGTERM) diye signal wait kora
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	sig := <-quit
	log.Printf("Received signal: %v. Initiating graceful shutdown...\n", sig)

	// Graceful shutdown context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v\n", err)
	} else {
		log.Println("HTTP server cleanly stopped.")
	}

	// defer pool.Close() will execute as main() exits, closing PostgreSQL connection pool cleanly
	log.Println("Closing PostgreSQL connection pool and terminating cleanly.")
}

// buildMux registers all HTTP and WebSocket endpoints onto a standard http.ServeMux.
func buildMux(pool *pgxpool.Pool, cfg *config.Config, wsHub *websocket.Hub) *http.ServeMux {
	authHandler := handlers.NewAuthHandler(pool, cfg.JWTSecret)
	friendHandler := handlers.NewFriendHandler(pool)
	moderationHandler := handlers.NewModerationHandler(pool)
	chatHandler := handlers.NewChatHandler(pool)
	mediaHandler := handlers.NewMediaHandler(cfg)
	callHandler := handlers.NewCallHandler(pool)
	wsHandler := handlers.NewWSHandler(wsHub, cfg.JWTSecret)

	mux := http.NewServeMux()

	// Base health check endpoint (exact match for root /)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","message":"Server is running"}`))
	})

	// Bind public authentication endpoints
	mux.HandleFunc("POST /api/register", authHandler.Register)
	mux.HandleFunc("POST /api/login", authHandler.Login)

	// Bind protected friend system endpoints
	mux.Handle("POST /api/friends/request", middleware.AuthMiddleware(http.HandlerFunc(friendHandler.SendFriendRequest)))
	mux.Handle("POST /api/friends/respond", middleware.AuthMiddleware(http.HandlerFunc(friendHandler.RespondFriendRequest)))
	mux.Handle("GET /api/friends", middleware.AuthMiddleware(http.HandlerFunc(friendHandler.GetFriends)))

	// Bind protected moderation & safety endpoints
	mux.Handle("POST /api/users/block", middleware.AuthMiddleware(http.HandlerFunc(moderationHandler.BlockUser)))
	mux.Handle("DELETE /api/users/block", middleware.AuthMiddleware(http.HandlerFunc(moderationHandler.UnblockUser)))
	mux.Handle("POST /api/reports", middleware.AuthMiddleware(http.HandlerFunc(moderationHandler.Report)))

	// Bind protected chat message history endpoint
	mux.Handle("GET /api/messages", middleware.AuthMiddleware(http.HandlerFunc(chatHandler.GetMessages)))

	// Bind protected call history endpoint
	mux.Handle("GET /api/calls", middleware.AuthMiddleware(http.HandlerFunc(callHandler.GetCallHistory)))

	// Bind protected unread notifications endpoint
	mux.Handle("GET /api/notifications", middleware.AuthMiddleware(http.HandlerFunc(chatHandler.GetUnreadNotifications)))
	mux.Handle("POST /api/notifications/read", middleware.AuthMiddleware(http.HandlerFunc(chatHandler.MarkNotificationsAsRead)))

	// Bind media and stickers endpoints
	mux.Handle("POST /api/upload", middleware.AuthMiddleware(http.HandlerFunc(mediaHandler.UploadFile)))
	mux.HandleFunc("GET /api/stickers", mediaHandler.GetStickers)

	// Serve uploaded files and static media assets
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir))))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))

	// Bind WebSocket real-time connection endpoint
	mux.HandleFunc("GET /ws", wsHandler.ServeWS)

	// Swagger documentation endpoint
	mux.Handle("GET /swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	return mux
}
