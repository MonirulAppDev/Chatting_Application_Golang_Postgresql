-- ==============================================================================
-- Database Schema for Chat Application
-- Engine: PostgreSQL
-- All table names and column names are kept in strict lowercase.
-- ==============================================================================

/*
    Table 1: users
    Stores account credentials, profile details, and user presence status.
*/
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    avatar_url TEXT,
    last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

/*
    Table 2: friendships
    Manages friend requests and relationship statuses (pending, accepted, rejected).
    Enforces a unique constraint on (sender_id, receiver_id) to prevent duplicate requests.
*/
CREATE TABLE IF NOT EXISTS friendships (
    id SERIAL PRIMARY KEY,
    sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Prevent duplicate friendship requests between the same users
    CONSTRAINT uq_friendships_sender_receiver UNIQUE (sender_id, receiver_id),
    -- Prevent users from sending friend requests to themselves
    CONSTRAINT chk_friendships_different_users CHECK (sender_id <> receiver_id),
    -- Ensure status only contains valid states
    CONSTRAINT chk_friendships_status CHECK (status IN ('pending', 'accepted', 'rejected'))
);

/*
    Table 3: blocks
    Records user blocking actions to prevent unwanted communications.
    Enforces a unique constraint on (blocker_id, blocked_id) to avoid redundant records.
*/
CREATE TABLE IF NOT EXISTS blocks (
    id SERIAL PRIMARY KEY,
    blocker_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Ensure a user cannot be blocked multiple times by the same blocker
    CONSTRAINT uq_blocks_blocker_blocked UNIQUE (blocker_id, blocked_id),
    -- Prevent a user from blocking themselves
    CONSTRAINT chk_blocks_different_users CHECK (blocker_id <> blocked_id)
);

/*
    Table 4: messages
    Stores all direct chat communications: text, image, audio, sticker, and location.
    Includes metadata (JSONB) for location coords or link previews, and read receipts.
*/
CREATE TABLE IF NOT EXISTS messages (
    id SERIAL PRIMARY KEY,
    sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_type TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}'::jsonb,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Ensure message_type is one of the supported types
    CONSTRAINT chk_messages_type CHECK (message_type IN ('text', 'image', 'audio', 'sticker', 'location'))
);

/*
    Table 5: reports
    Stores user and message reports for moderation and administrative review.
    message_id is nullable for general user reports.
*/
CREATE TABLE IF NOT EXISTS reports (
    id SERIAL PRIMARY KEY,
    reporter_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reported_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Ensure reporter cannot report themselves
    CONSTRAINT chk_reports_different_users CHECK (reporter_id <> reported_user_id)
);

/*
    Table 6: call_logs
    Tracks historical audio and video call sessions between users.
*/
CREATE TABLE IF NOT EXISTS call_logs (
    id SERIAL PRIMARY KEY,
    caller_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    call_type TEXT NOT NULL,
    status TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    started_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Validate call types
    CONSTRAINT chk_call_logs_type CHECK (call_type IN ('audio', 'video')),
    -- Validate call status
    CONSTRAINT chk_call_logs_status CHECK (status IN ('missed', 'completed', 'rejected'))
);

/*
    Table 7: notifications
    Stores offline/push and in-app alert notifications for messages and live location events.
*/
CREATE TABLE IF NOT EXISTS notifications (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Validate notification types
    CONSTRAINT chk_notifications_type CHECK (type IN ('message', 'live_location', 'call'))
);

