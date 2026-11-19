// Package models defines the application domain models, database entities, and API payloads.
package models

import "time"

/*
Notification represents an in-app or offline notification record stored in the database.
*/
type Notification struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// Target recipient user ID
	UserID int `json:"user_id"`
	// Sender user ID who generated the notification event
	SenderID int `json:"sender_id"`
	// Notification category: 'message' or 'live_location'
	Type string `json:"type"`
	// Notification headline/title
	Title string `json:"title"`
	// Notification detailed content text
	Body string `json:"body"`
	// Read receipt status
	IsRead bool `json:"is_read"`
	// Timestamp when the notification was created
	CreatedAt time.Time `json:"created_at"`
}

/*
MarkNotificationsReadRequest captures the optional JSON payload for marking notifications as read.
If NotificationIDs and NotificationID are omitted, all unread notifications for the user are marked as read.
*/
type MarkNotificationsReadRequest struct {
	// Optional list of specific notification IDs to mark as read
	NotificationIDs []int `json:"notification_ids,omitempty"`
	// Optional single notification ID to mark as read
	NotificationID *int `json:"notification_id,omitempty"`
}
