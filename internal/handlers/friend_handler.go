// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"backend/internal/middleware"
	"backend/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
FriendHandler manages social relationship operations including friend requests,
responses (accept/reject), and friends list retrieval.
*/
type FriendHandler struct {
	// Database connection pool for executing friendship and user queries
	DB *pgxpool.Pool
}

// NewFriendHandler constructs and returns a new FriendHandler instance.
func NewFriendHandler(db *pgxpool.Pool) *FriendHandler {
	return &FriendHandler{
		DB: db,
	}
}

// getUserIDFromContext retrieves the authenticated user's ID from the request context.
func getUserIDFromContext(r *http.Request) (int, bool) {
	if val, ok := r.Context().Value(middleware.UserIDKey).(int); ok {
		return val, true
	}
	if val, ok := r.Context().Value("user_id").(int); ok {
		return val, true
	}
	return 0, false
}

// SendFriendRequest handles sending a new friend request to another user.
//
// @Summary Send friend request
// @Description Send a friend request to another user by their user ID
// @Tags Friends
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body models.SendFriendRequest true "Receiver user ID"
// @Success 201 {object} map[string]interface{} "Friend request sent successfully"
// @Failure 400 {object} map[string]string "Invalid request or cannot send to self"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Cannot send request due to user block"
// @Failure 404 {object} map[string]string "Target user not found"
// @Failure 409 {object} map[string]string "Friend request already exists or users are already friends"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/friends/request [post]
func (h *FriendHandler) SendFriendRequest(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	userID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Parse Request Payload
	*/
	var req models.SendFriendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. 'receiver_id' is required.",
		})
		return
	}

	if req.ReceiverID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid receiver_id provided.",
		})
		return
	}

	// Check if user is attempting to send friend request to themselves
	if userID == req.ReceiverID {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You cannot send a friend request to yourself.",
		})
		return
	}

	/*
		Step 3: Check if Target User Exists
	*/
	var receiverExists bool
	userCheckQuery := `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1);`
	if err := h.DB.QueryRow(r.Context(), userCheckQuery, req.ReceiverID).Scan(&receiverExists); err != nil || !receiverExists {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "Target user does not exist.",
		})
		return
	}

	/*
		Step 4: Check Blocks Table
		Verifies neither user has blocked the other.
	*/
	var isBlocked bool
	blockQuery := `
		SELECT EXISTS (
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		);
	`
	if err := h.DB.QueryRow(r.Context(), blockQuery, userID, req.ReceiverID).Scan(&isBlocked); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to verify user blocking status.",
		})
		return
	}

	if isBlocked {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Cannot send friend request: communication between these accounts is blocked.",
		})
		return
	}

	/*
		Step 5: Check Friendships Table for Existing Entry
	*/
	var existingStatus string
	existingQuery := `
		SELECT status FROM friendships
		WHERE (sender_id = $1 AND receiver_id = $2)
		   OR (sender_id = $2 AND receiver_id = $1);
	`
	err := h.DB.QueryRow(r.Context(), existingQuery, userID, req.ReceiverID).Scan(&existingStatus)
	if err == nil {
		switch existingStatus {
		case "accepted":
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "You are already friends with this user.",
			})
		case "pending":
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "A friend request is already pending between both users.",
			})
		case "rejected":
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Previous friend request was rejected.",
			})
		default:
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "A friendship record already exists between both users.",
			})
		}
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error checking existing friendship status.",
		})
		return
	}

	/*
		Step 6: Insert Friendship Record with 'pending' Status
	*/
	insertQuery := `
		INSERT INTO friendships (sender_id, receiver_id, status)
		VALUES ($1, $2, 'pending')
		RETURNING id, created_at;
	`
	var friendship models.Friendship
	friendship.SenderID = userID
	friendship.ReceiverID = req.ReceiverID
	friendship.Status = "pending"

	err = h.DB.QueryRow(r.Context(), insertQuery, userID, req.ReceiverID).Scan(
		&friendship.ID,
		&friendship.CreatedAt,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to save friend request.",
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message":        "Friend request sent successfully.",
		"friend_request": friendship,
	})
}

// RespondFriendRequest handles accepting or rejecting an incoming friend request.
//
// @Summary Respond to friend request
// @Description Accept or reject a pending friend request by request ID
// @Tags Friends
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body models.RespondFriendRequest true "Request ID and action (accepted or rejected)"
// @Success 200 {object} map[string]interface{} "Friend request response processed"
// @Failure 400 {object} map[string]string "Invalid action or request not in pending state"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Forbidden: user is not the recipient of this request"
// @Failure 404 {object} map[string]string "Friend request not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/friends/respond [post]
func (h *FriendHandler) RespondFriendRequest(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	userID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Parse Request Payload
	*/
	var req models.RespondFriendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. 'request_id' and 'action' are required.",
		})
		return
	}

	// Validate action value strictly
	if req.Action != "accepted" && req.Action != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid action value. Expected 'accepted' or 'rejected'.",
		})
		return
	}

	/*
		Step 3: Query Friendship Record and Verify Receiver
	*/
	var f models.Friendship
	query := `
		SELECT id, sender_id, receiver_id, status, created_at
		FROM friendships
		WHERE id = $1;
	`
	err := h.DB.QueryRow(r.Context(), query, req.RequestID).Scan(
		&f.ID,
		&f.SenderID,
		&f.ReceiverID,
		&f.Status,
		&f.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "Friend request not found.",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while fetching friend request.",
		})
		return
	}

	// Verify that the logged-in user is indeed the recipient (receiver_id)
	if f.ReceiverID != userID {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Forbidden: you can only respond to friend requests sent to you.",
		})
		return
	}

	// Ensure the friend request is currently in pending state
	if f.Status != "pending" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Friend request has already been %s.", f.Status),
		})
		return
	}

	/*
		Step 4: Update Friendship Status
	*/
	updateQuery := `
		UPDATE friendships
		SET status = $1
		WHERE id = $2;
	`
	_, err = h.DB.Exec(r.Context(), updateQuery, req.Action, req.RequestID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to update friend request status.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":    fmt.Sprintf("Friend request %s successfully.", req.Action),
		"request_id": req.RequestID,
		"status":     req.Action,
	})
}

// GetFriends retrieves the list of accepted friends for the authenticated user.
//
// @Summary List all friends
// @Description Fetch all users who have an accepted friendship with the authenticated user
// @Tags Friends
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "List of friends"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/friends [get]
func (h *FriendHandler) GetFriends(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	userID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Query Accepted Friends from Database
		Joins friendships and users tables where status is 'accepted'
		and current user is either sender_id or receiver_id.
	*/
	query := `
		SELECT 
			u.id, 
			u.username, 
			u.avatar_url, 
			u.last_seen
		FROM users u
		JOIN friendships f ON (
			(f.sender_id = $1 AND f.receiver_id = u.id)
			OR 
			(f.receiver_id = $1 AND f.sender_id = u.id)
		)
		WHERE f.status = 'accepted'
		ORDER BY u.username ASC;
	`

	rows, err := h.DB.Query(r.Context(), query, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to query friends list.",
		})
		return
	}
	defer rows.Close()

	friends := make([]models.FriendUser, 0)
	for rows.Next() {
		var friend models.FriendUser
		if err := rows.Scan(
			&friend.ID,
			&friend.Username,
			&friend.AvatarURL,
			&friend.LastSeen,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Failed to parse friends list records.",
			})
			return
		}
		friends = append(friends, friend)
	}

	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Error iterating friends records.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"friends": friends,
		"count":   len(friends),
	})
}
