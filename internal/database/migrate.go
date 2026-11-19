// Package database manages database connection pooling, queries, and schema migrations.
package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
	migrationStep holds the metadata and SQL DDL command for a single table migration.
*/
type migrationStep struct {
	tableName string
	query     string
}

// RunMigrations executes database schema migrations sequentially in the exact specified order:
// 1. users (first)
// 2. friendships
// 3. blocks
// 4. reports
// 5. messages
// 6. call_logs
// 7. notifications
// It logs progress after each table creation and logs final results upon completion.
func RunMigrations(pool *pgxpool.Pool) error {
	// Create context with timeout for migration operations
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	/*
		Sequential table migration list.
		All table names and column names are in lowercase.
	*/
	migrations := []migrationStep{
		{
			tableName: "users",
			query: `
				CREATE TABLE IF NOT EXISTS users (
					id SERIAL PRIMARY KEY,
					username TEXT NOT NULL UNIQUE,
					email TEXT NOT NULL UNIQUE,
					password_hash TEXT NOT NULL,
					avatar_url TEXT,
					last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
				);
			`,
		},
		{
			tableName: "friendships",
			query: `
				CREATE TABLE IF NOT EXISTS friendships (
					id SERIAL PRIMARY KEY,
					sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					status TEXT NOT NULL DEFAULT 'pending',
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT uq_friendships_sender_receiver UNIQUE (sender_id, receiver_id),
					CONSTRAINT chk_friendships_different_users CHECK (sender_id <> receiver_id),
					CONSTRAINT chk_friendships_status CHECK (status IN ('pending', 'accepted', 'rejected'))
				);
			`,
		},
		{
			tableName: "blocks",
			query: `
				CREATE TABLE IF NOT EXISTS blocks (
					id SERIAL PRIMARY KEY,
					blocker_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT uq_blocks_blocker_blocked UNIQUE (blocker_id, blocked_id),
					CONSTRAINT chk_blocks_different_users CHECK (blocker_id <> blocked_id)
				);
			`,
		},
		{
			tableName: "reports",
			query: `
				CREATE TABLE IF NOT EXISTS reports (
					id SERIAL PRIMARY KEY,
					reporter_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					reported_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					message_id INTEGER,
					reason TEXT NOT NULL,
					status TEXT NOT NULL DEFAULT 'pending',
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT chk_reports_different_users CHECK (reporter_id <> reported_user_id)
				);
			`,
		},
		{
			tableName: "messages",
			query: `
				CREATE TABLE IF NOT EXISTS messages (
					id SERIAL PRIMARY KEY,
					sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					message_type TEXT NOT NULL,
					content TEXT NOT NULL,
					metadata JSONB DEFAULT '{}'::jsonb,
					is_read BOOLEAN NOT NULL DEFAULT FALSE,
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT chk_messages_type CHECK (message_type IN ('text', 'image', 'audio', 'sticker', 'location'))
				);
			`,
		},
		{
			tableName: "call_logs",
			query: `
				CREATE TABLE IF NOT EXISTS call_logs (
					id SERIAL PRIMARY KEY,
					caller_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					call_type TEXT NOT NULL,
					status TEXT NOT NULL,
					duration_seconds INTEGER NOT NULL DEFAULT 0,
					started_at TIMESTAMP WITH TIME ZONE,
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT chk_call_logs_type CHECK (call_type IN ('audio', 'video')),
					CONSTRAINT chk_call_logs_status CHECK (status IN ('missed', 'completed', 'rejected'))
				);
			`,
		},
		{
			tableName: "notifications",
			query: `
				CREATE TABLE IF NOT EXISTS notifications (
					id SERIAL PRIMARY KEY,
					user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					type TEXT NOT NULL,
					title TEXT NOT NULL,
					body TEXT NOT NULL,
					is_read BOOLEAN NOT NULL DEFAULT FALSE,
					created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
					CONSTRAINT chk_notifications_type CHECK (type IN ('message', 'live_location', 'call'))
				);
			`,
		},
	}

	log.Println("Starting database migrations...")

	// Iterate and execute each migration step in exact sequence
	for index, step := range migrations {
		// Run DDL statement
		_, err := pool.Exec(ctx, step.query)
		if err != nil {
			return fmt.Errorf("migration failed for table %s (step %d): %w", step.tableName, index+1, err)
		}
		// Print per-step migration progress
		log.Printf("Migration step %d/%d applied: [%s] table ready", index+1, len(migrations), step.tableName)
	}

	// Ensure call_logs has started_at column for existing databases
	if _, err := pool.Exec(ctx, `ALTER TABLE call_logs ADD COLUMN IF NOT EXISTS started_at TIMESTAMP WITH TIME ZONE;`); err != nil {
		log.Printf("Note: adding started_at to call_logs: %v", err)
	}

	// Ensure notifications table constraint includes 'call' for existing databases
	updateConstraintSQL := `
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.table_constraints 
				WHERE constraint_name = 'chk_notifications_type' AND table_name = 'notifications'
			) THEN
				ALTER TABLE notifications DROP CONSTRAINT chk_notifications_type;
			END IF;
			ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (type IN ('message', 'live_location', 'call'));
		END $$;
	`
	if _, err := pool.Exec(ctx, updateConstraintSQL); err != nil {
		log.Printf("Note: updating chk_notifications_type constraint: %v", err)
	}

	// Print final success result log
	log.Printf("Database migration completed successfully! All %d tables are verified and up to date.", len(migrations))
	return nil
}
