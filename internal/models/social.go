// Package models defines the application domain models, database entities, and API payloads.
package models

import "time"

/*
Friendship represents a relationship between two users in the database.
Status can be 'pending', 'accepted', or 'rejected'.
*/
type Friendship struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// User ID who initiated the friend request
	SenderID int `json:"sender_id"`
	// User ID who received the friend request
	ReceiverID int `json:"receiver_id"`
	// Friendship status: 'pending', 'accepted', or 'rejected'
	Status string `json:"status"`
	// Timestamp when the request was initiated
	CreatedAt time.Time `json:"created_at"`
}

/*
Block represents a block relationship between two users in the database.
*/
type Block struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// User ID who blocked another user
	BlockerID int `json:"blocker_id"`
	// User ID who was blocked
	BlockedID int `json:"blocked_id"`
	// Timestamp when the block action occurred
	CreatedAt time.Time `json:"created_at"`
}

/*
Report represents a user or message moderation report in the database.
MessageID is optional (nullable) for direct user reports.
*/
type Report struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// User ID who submitted the report
	ReporterID int `json:"reporter_id"`
	// User ID who is being reported
	ReportedUserID int `json:"reported_user_id"`
	// Optional reference to a specific message ID (nullable)
	MessageID *int `json:"message_id,omitempty"`
	// Stated reason for the report
	Reason string `json:"reason"`
	// Report status (e.g., 'pending', 'reviewed', 'resolved')
	Status string `json:"status"`
	// Timestamp when the report was created
	CreatedAt time.Time `json:"created_at"`
}

/*
SendFriendRequest captures the incoming JSON payload for sending a friend request.
*/
type SendFriendRequest struct {
	// Target user ID who should receive the friend request
	ReceiverID int `json:"receiver_id" binding:"required"`
}

/*
RespondFriendRequest captures the incoming JSON payload for accepting or rejecting a friend request.
Action must be either 'accepted' or 'rejected'.
*/
type RespondFriendRequest struct {
	// ID of the friendship record to update
	RequestID int `json:"request_id" binding:"required"`
	// Action to perform: 'accepted' or 'rejected'
	Action string `json:"action" binding:"required"`
}

/*
BlockUserRequest captures the incoming JSON payload for blocking a user.
*/
type BlockUserRequest struct {
	// User ID to be blocked
	BlockedID int `json:"blocked_id" binding:"required"`
}

/*
UnblockUserRequest captures the incoming payload or query for unblocking a user.
*/
type UnblockUserRequest struct {
	// User ID to be unblocked
	BlockedID int `json:"blocked_id" binding:"required"`
}

/*
ReportUserRequest captures the incoming JSON payload for submitting a report.
MessageID is optional (nullable) if reporting a profile rather than a specific message.
*/
type ReportUserRequest struct {
	// User ID being reported
	ReportedUserID int `json:"reported_user_id" binding:"required"`
	// Optional message ID being reported (nullable)
	MessageID *int `json:"message_id,omitempty"`
	// Detailed reason for reporting
	Reason string `json:"reason" binding:"required"`
}

/*
FriendUser represents a friend summary object returned in friends list responses.
*/
type FriendUser struct {
	// Unique primary key of the friend
	ID int `json:"id"`
	// Friend display name
	Username string `json:"username"`
	// Friend avatar image URL (nullable)
	AvatarURL *string `json:"avatar_url,omitempty"`
	// Friend last activity timestamp (nullable)
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

