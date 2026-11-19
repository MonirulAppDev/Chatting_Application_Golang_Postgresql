// Package models defines the application domain models, database entities, and API payloads.
package models

import "time"

/*
CallLog represents a call history record stored in the call_logs table.
*/
type CallLog struct {
	// Unique record identifier
	ID int `json:"id"`
	// User ID who initiated the call
	CallerID int `json:"caller_id"`
	// User ID who received the call
	ReceiverID int `json:"receiver_id"`
	// Type of call: 'audio' or 'video'
	CallType string `json:"call_type"`
	// Call outcome: 'missed', 'completed', or 'rejected'
	Status string `json:"status"`
	// Total call duration in seconds
	DurationSeconds int `json:"duration_seconds"`
	// Timestamp when the call was answered/connected
	StartedAt *time.Time `json:"started_at,omitempty"`
	// Timestamp when the call was initiated
	CreatedAt time.Time `json:"created_at"`

	// Caller username from users table join
	CallerUsername string `json:"caller_username,omitempty"`
	// Caller avatar URL from users table join
	CallerAvatarURL *string `json:"caller_avatar_url,omitempty"`
	// Receiver username from users table join
	ReceiverUsername string `json:"receiver_username,omitempty"`
	// Receiver avatar URL from users table join
	ReceiverAvatarURL *string `json:"receiver_avatar_url,omitempty"`
}
